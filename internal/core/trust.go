package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/asn1"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// writeFileAtomic writes data to a temp file in the same directory and renames it
// into place, so a crash mid-write can never leave a half-written file (e.g. a
// CHAIN head inconsistent with the events it summarizes). rename is atomic on the
// same filesystem.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ---- chain-head trust anchoring: signature + RFC 3161 trusted timestamp ----
//
// A bare hash chain proves internal consistency but not that the head wasn't
// rewritten by whoever controls the repo. Two independent anchors close that gap:
//   1. an optional detached signature from `signet` (SIGNET_CMD), and
//   2. an RFC 3161 timestamp token from a public TSA (TSA_URL) committing to the
//      head — a third party attests the head existed at a point in time, and a
//      rewritten head will no longer match the stored token.
// Both are non-fatal at fetch time (network/tooling may be absent) and checked by
// `engine verify` when present.

var oidSHA256 = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}

type tsAlgID struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type tsMessageImprint struct {
	HashAlgorithm tsAlgID
	HashedMessage []byte
}

type tsRequest struct {
	Version        int
	MessageImprint tsMessageImprint
	CertReq        bool `asn1:"optional"`
}

// tsaEndpoint returns the configured RFC 3161 TSA URL, or "" if timestamping is
// off. Opt-in via TSA_URL so local runs and tests never hit the network. A common
// public, no-account TSA is https://freetsa.org/tsr.
func tsaEndpoint() string { return strings.TrimSpace(os.Getenv("TSA_URL")) }

// requestTimestamp asks a public RFC 3161 TSA to timestamp headHash and returns
// the raw TimeStampResp (DER token). It rejects any response that does not commit
// to headHash, so a wrong/forged token can't be stored.
func requestTimestamp(tsaURL string, headHash []byte) ([]byte, error) {
	req := tsRequest{
		Version: 1,
		MessageImprint: tsMessageImprint{
			HashAlgorithm: tsAlgID{
				Algorithm:  oidSHA256,
				Parameters: asn1.RawValue{FullBytes: []byte{0x05, 0x00}}, // NULL
			},
			HashedMessage: headHash,
		},
		CertReq: true, // ask the TSA to embed its cert so the token is self-verifiable
	}
	body, err := asn1.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", tsaURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/timestamp-query")
	httpReq.Header.Set("User-Agent", UserAgent)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("TSA %s: HTTP %d", tsaURL, resp.StatusCode)
	}
	out, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if !timestampCommitsTo(out, headHash) {
		return nil, fmt.Errorf("TSA response does not commit to the chain head")
	}
	return out, nil
}

// timestampCommitsTo reports whether an RFC 3161 token embeds headHash as its
// SHA-256 messageImprint, encoded as a DER OCTET STRING (0x04, len, bytes). A TSA
// cannot mint a token for a head it never saw, so a rewritten head will not be
// found in a previously-issued token — this is the forgery-resistant core check.
// Full TSA-signature validation is available with `openssl ts -verify`.
func timestampCommitsTo(tsr, headHash []byte) bool {
	if len(headHash) == 0 || len(headHash) > 255 {
		return false
	}
	needle := append([]byte{0x04, byte(len(headHash))}, headHash...)
	return bytes.Contains(tsr, needle)
}

// signChainHead runs SIGNET_CMD (if set) over chainPath and returns the detached
// signature it writes to stdout. SIGNET_CMD is a command line; the engine appends
// the CHAIN file path as the final argument, e.g.
//
//	SIGNET_CMD="signet sign --key /run/secrets/signet.key"
//
// Returns (nil, nil) when SIGNET_CMD is unset — signing is optional.
func signChainHead(chainPath string) ([]byte, error) {
	cmdline := strings.TrimSpace(os.Getenv("SIGNET_CMD"))
	if cmdline == "" {
		return nil, nil
	}
	parts := strings.Fields(cmdline)
	args := append(parts[1:], chainPath)
	out, err := exec.Command(parts[0], args...).Output()
	if err != nil {
		return nil, fmt.Errorf("signet: %w", err)
	}
	return out, nil
}

// anchorChainHead signs and timestamps the freshly written CHAIN file. Both steps
// are best-effort and non-fatal: a missing signer or unreachable TSA logs a notice
// and leaves the (still hash-chained) data published.
func anchorChainHead(trackerDir string) {
	chainPath := filepath.Join(trackerDir, "CHAIN")
	chainBytes, err := os.ReadFile(chainPath)
	if err != nil {
		return
	}
	if sig, err := signChainHead(chainPath); err != nil {
		fmt.Fprintln(os.Stderr, "signet (non-fatal):", err)
	} else if len(sig) > 0 {
		if err := writeFileAtomic(chainPath+".sig", sig, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write CHAIN.sig:", err)
		}
	}
	if tsa := tsaEndpoint(); tsa != "" {
		h := sha256.Sum256(chainBytes)
		if tsr, err := requestTimestamp(tsa, h[:]); err != nil {
			fmt.Fprintln(os.Stderr, "rfc3161 (non-fatal):", err)
		} else if err := writeFileAtomic(chainPath+".tsr", tsr, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write CHAIN.tsr:", err)
		}
	}
}

// verifyTrustAnchors checks the trust artifacts for one tracker dir when present.
// It returns a non-empty reason if an artifact exists but does not match the
// current CHAIN head (e.g. the head was rewritten but the old timestamp kept).
// Absent artifacts are not an error — anchoring is optional.
func verifyTrustAnchors(trackerDir string) string {
	chainBytes, err := os.ReadFile(filepath.Join(trackerDir, "CHAIN"))
	if err != nil {
		return ""
	}
	tsrPath := filepath.Join(trackerDir, "CHAIN.tsr")
	if tsr, err := os.ReadFile(tsrPath); err == nil && len(tsr) > 0 {
		h := sha256.Sum256(chainBytes)
		if !timestampCommitsTo(tsr, h[:]) {
			return "timestamp does not match current head"
		}
	}
	return ""
}
