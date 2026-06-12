package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/asn1"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFileAtomicRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "f.json")
	if err := writeFileAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil || string(b) != "two" {
		t.Fatalf("got %q err %v, want two", b, err)
	}
	// no temp leftovers in the dir
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, found %d (temp leak?)", len(entries))
	}
}

func TestChainAppendVerifyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	a := Diff(nil, []Record{rec("a", "alpha"), rec("b", "bravo")}, "src", "2026-01-01T00:00:00Z")
	if err := appendEvents(dir, "tk", a); err != nil {
		t.Fatal(err)
	}
	// a second batch must chain onto the first
	b := Diff(&State{Records: []Record{rec("a", "alpha")}}, []Record{rec("a", "alpha2")}, "src", "2026-01-02T00:00:00Z")
	if err := appendEvents(dir, "tk", b); err != nil {
		t.Fatal(err)
	}
	bad, err := VerifyChain(dir, "tk")
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("clean chain reported bad: %v", bad)
	}
}

func TestChainTamperDetected(t *testing.T) {
	dir := t.TempDir()
	evs := Diff(nil, []Record{rec("a", "alpha"), rec("b", "bravo")}, "src", "2026-01-01T00:00:00Z")
	if err := appendEvents(dir, "tk", evs); err != nil {
		t.Fatal(err)
	}
	// rewrite history: flip a byte in the first event line
	evPath := filepath.Join(dir, "data", "tk", "events", filepath.Base(mustGlobOne(t, dir, "tk")))
	raw, _ := os.ReadFile(evPath)
	tampered := bytes.Replace(raw, []byte("alpha"), []byte("AAAAA"), 1)
	if bytes.Equal(raw, tampered) {
		t.Fatal("test setup: nothing changed")
	}
	os.WriteFile(evPath, tampered, 0o644)
	bad, _ := VerifyChain(dir, "tk")
	if len(bad) == 0 {
		t.Fatal("tamper not detected — chain verify is not protecting history")
	}
}

func mustGlobOne(t *testing.T, dir, tracker string) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "data", tracker, "events", "*.jsonl"))
	if len(files) == 0 {
		t.Fatal("no event files written")
	}
	return files[0]
}

func TestTimestampCommitsTo(t *testing.T) {
	h := sha256.Sum256([]byte("head\n"))
	good := append([]byte{0xaa, 0x04, 0x20}, h[:]...) // OCTET STRING 0x04 len(32)=0x20 || hash
	good = append(good, 0xbb)
	if !timestampCommitsTo(good, h[:]) {
		t.Fatal("should detect a committed imprint")
	}
	other := sha256.Sum256([]byte("different"))
	if timestampCommitsTo(good, other[:]) {
		t.Fatal("must not match a different head")
	}
}

func TestVerifyTrustAnchorsTimestampMismatch(t *testing.T) {
	td := filepath.Join(t.TempDir(), "data", "tk")
	os.MkdirAll(td, 0o755)
	os.WriteFile(filepath.Join(td, "CHAIN"), []byte("abc\n"), 0o644)
	h := sha256.Sum256([]byte("abc\n"))
	// matching token → no complaint
	tok := append([]byte{0x04, 0x20}, h[:]...)
	os.WriteFile(filepath.Join(td, "CHAIN.tsr"), tok, 0o644)
	if r := verifyTrustAnchors(td); r != "" {
		t.Fatalf("matching timestamp flagged: %q", r)
	}
	// stale token (commits to a different head) → flagged
	wrong := sha256.Sum256([]byte("rewritten\n"))
	os.WriteFile(filepath.Join(td, "CHAIN.tsr"), append([]byte{0x04, 0x20}, wrong[:]...), 0o644)
	if r := verifyTrustAnchors(td); r == "" {
		t.Fatal("stale timestamp not flagged — head could be rewritten undetected")
	}
}

func TestRequestTimestampRoundTrip(t *testing.T) {
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req tsRequest
		if _, err := asn1.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		got = req.MessageImprint.HashedMessage
		// echo a token that commits to the requested imprint
		tok := append([]byte{0x30, 0x22, 0x04, 0x20}, got...)
		w.Header().Set("Content-Type", "application/timestamp-reply")
		w.Write(tok)
	}))
	defer srv.Close()

	h := sha256.Sum256([]byte("head\n"))
	tsr, err := requestTimestamp(srv.URL, h[:])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, h[:]) {
		t.Fatal("imprint not round-tripped through the DER request")
	}
	if !timestampCommitsTo(tsr, h[:]) {
		t.Fatal("returned token should commit to the head")
	}
}

func TestRequestTimestampRejectsNonCommitting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("garbage that commits to nothing"))
	}))
	defer srv.Close()
	h := sha256.Sum256([]byte("head\n"))
	if _, err := requestTimestamp(srv.URL, h[:]); err == nil {
		t.Fatal("expected rejection of a token that doesn't commit to the head")
	}
}

func TestSignChainHeadUnsetIsNoop(t *testing.T) {
	os.Unsetenv("SIGNET_CMD")
	out, err := signChainHead("whatever")
	if err != nil || out != nil {
		t.Fatalf("unset SIGNET_CMD should be a no-op, got %q %v", out, err)
	}
}

// TestRealTSAOptIn hits a live public RFC 3161 TSA. Skipped unless TSA_LIVE_TEST
// is set, so the normal suite stays hermetic. Run: TSA_LIVE_TEST=1 go test -run RealTSA
func TestRealTSAOptIn(t *testing.T) {
	if os.Getenv("TSA_LIVE_TEST") == "" {
		t.Skip("set TSA_LIVE_TEST=1 to hit a live TSA")
	}
	url := os.Getenv("TSA_URL")
	if url == "" {
		url = "https://freetsa.org/tsr"
	}
	h := sha256.Sum256([]byte("defense-trackers tsa selftest"))
	tsr, err := requestTimestamp(url, h[:])
	if err != nil {
		t.Fatal(err)
	}
	if !timestampCommitsTo(tsr, h[:]) {
		t.Fatal("live token did not commit to the head")
	}
	t.Logf("live TSA %s returned a %d-byte token committing to our head", url, len(tsr))
}
