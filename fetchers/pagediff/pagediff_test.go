package pagediff

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"engine/internal/core"
)

// Refresh workflow when a source page changes legitimately:
//
//	make refresh-fixture        # curls the live page into testdata/
//	go test ./fetchers/pagediff -run Golden -update
//	git diff testdata/          # eyeball the golden diff, then commit
//
// The repair loop performs the same dance automatically and opens a PR.
var update = flag.Bool("update", false, "rewrite golden files from current parser output")

func TestGoldenBlueUAS(t *testing.T) {
	raw, err := os.ReadFile("testdata/blue_uas_fixture.html")
	if err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	c := core.Contract{
		ID:         "blue-uas",
		SliceStart: `<main id="content">`,
		SliceEnd:   `</main>`,
		MinRecords: 5,
	}
	recs, err := Parse(string(raw), c)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := core.Validate(c, nil, recs); err != nil {
		t.Fatalf("validate: %v", err)
	}
	got, err := json.MarshalIndent(recs, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')

	const golden = "testdata/blue_uas_golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden rewritten: %s (%d records)", golden, len(recs))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden missing — run: go test ./fetchers/pagediff -run Golden -update")
	}
	if !bytes.Equal(got, want) {
		t.Errorf("parser output diverged from golden.\nIf the change is intentional, rerun with -update and review the diff.\ngot %d bytes, want %d bytes", len(got), len(want))
	}
}

func TestSliceMarkerMissingFailsLoud(t *testing.T) {
	_, err := Parse("<html><body>no markers here</body></html>",
		core.Contract{SliceStart: `<main id="content">`})
	if err == nil {
		t.Fatal("expected loud failure when slice marker vanishes — that is the repair trigger")
	}
}
