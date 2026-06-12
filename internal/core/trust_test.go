package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// mockTSA returns an httptest server that echoes each request's imprint as a
// minimal token committing to it — lets the full anchor path run hermetically.
func mockTSA(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req tsRequest
		if _, err := asn1.Unmarshal(body, &req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		tok := append([]byte{0x30, 0x22, 0x04, 0x20}, req.MessageImprint.HashedMessage...)
		w.Write(tok)
	}))
}

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

func tokenFor(head string) []byte {
	imp := sha256.Sum256([]byte(head + "\n"))
	return append([]byte{0x30, 0x22, 0x04, 0x20}, imp[:]...)
}

func TestVerifyTrustAnchorsHistory(t *testing.T) {
	td := filepath.Join(t.TempDir(), "data", "tk")
	os.MkdirAll(td, 0o755)
	heads := []string{"headA", "headB"} // oldest→newest

	// token committing to the latest head → fine
	os.WriteFile(filepath.Join(td, "CHAIN.tsr"), tokenFor("headB"), 0o644)
	if r := verifyTrustAnchors(td, heads); r != "" {
		t.Fatalf("current-head timestamp flagged: %q", r)
	}
	// token committing to an older but real head (legit append while TSA was
	// down) → still fine, no false alarm
	os.WriteFile(filepath.Join(td, "CHAIN.tsr"), tokenFor("headA"), 0o644)
	if r := verifyTrustAnchors(td, heads); r != "" {
		t.Fatalf("older-but-real head timestamp flagged (false positive): %q", r)
	}
	// token committing to a head that no longer exists (coordinated rewrite) → flagged
	os.WriteFile(filepath.Join(td, "CHAIN.tsr"), tokenFor("rewritten-head"), 0o644)
	if r := verifyTrustAnchors(td, heads); r == "" {
		t.Fatal("rewritten history not flagged — timestamp anchor not protecting")
	}
	// no token → not an error (anchoring optional)
	os.Remove(filepath.Join(td, "CHAIN.tsr"))
	if r := verifyTrustAnchors(td, heads); r != "" {
		t.Fatalf("absent token treated as error: %q", r)
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

// TestCoordinatedRewriteCaughtByTimestamp is the differentiator: an attacker who
// controls the repo edits an event AND recomputes CHAIN so the bare hash chain
// still verifies — but cannot re-mint the TSA token, so the timestamp anchor
// catches the rewrite. Runs hermetically against a mock TSA.
func TestCoordinatedRewriteCaughtByTimestamp(t *testing.T) {
	srv := mockTSA(t)
	defer srv.Close()
	t.Setenv("TSA_URL", srv.URL)

	dir := t.TempDir()
	if err := appendEvents(dir, "tk", Diff(nil, []Record{rec("a", "alpha")}, "src", "2026-01-01T00:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "tk", "CHAIN.tsr")); err != nil {
		t.Fatal("anchor path did not write CHAIN.tsr")
	}
	if bad, _ := VerifyChain(dir, "tk"); len(bad) != 0 {
		t.Fatalf("clean chain failed verify: %v", bad)
	}

	// coordinated rewrite: change the event line AND fix CHAIN to match it
	evPath := mustGlobOne(t, dir, "tk")
	raw, _ := os.ReadFile(evPath)
	line := bytes.TrimRight(raw, "\n")
	rewritten := bytes.Replace(line, []byte("alpha"), []byte("AAAAA"), 1)
	if bytes.Equal(line, rewritten) {
		t.Fatal("setup: event line unchanged")
	}
	os.WriteFile(evPath, append(rewritten, '\n'), 0o644)
	h := sha256.Sum256(rewritten)
	os.WriteFile(filepath.Join(dir, "data", "tk", "CHAIN"), []byte(hex.EncodeToString(h[:])+"\n"), 0o644)

	bad, _ := VerifyChain(dir, "tk")
	if len(bad) == 0 {
		t.Fatal("coordinated rewrite NOT caught — the timestamp anchor failed its one job")
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
