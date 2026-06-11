package curate

import (
	"os"
	"testing"

	"engine/internal/core"
)

func TestParseCurated(t *testing.T) {
	raw, err := os.ReadFile("testdata/sample.json")
	if err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	c := core.Contract{ID: "nipr-matrix", KeyField: "tool"}
	recs, err := Parse(raw, c)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d records, want 3", len(recs))
	}
	if recs[0].Key != "CamoGPT" {
		t.Errorf("key from key_field: got %q", recs[0].Key)
	}
	// text falls back to the key when no name/title/text present
	if recs[0].Fields["text"] != "CamoGPT" {
		t.Errorf("text fallback: got %q", recs[0].Fields["text"])
	}
	if recs[0].Fields["network"] != "NIPR" || recs[0].Fields["status"] != "live" {
		t.Errorf("fields not carried through: %+v", recs[0].Fields)
	}
}

func TestParseInvalidFailsLoud(t *testing.T) {
	if _, err := Parse([]byte(`{"not":"an array"}`), core.Contract{}); err == nil {
		t.Fatal("expected loud failure on non-array curated file")
	}
}
