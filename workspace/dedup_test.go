package workspace

import "testing"

// appendDedupURL must drop URL repeats both against the existing base AND within
// the incoming batch, while always keeping URL-less entries (no false collapse).
func TestAppendDedupURL(t *testing.T) {
	base := []Opportunity{{ID: "a", URL: "https://x/1"}}
	add := []Opportunity{
		{ID: "b", URL: "https://x/1"}, // dupe of base → dropped
		{ID: "c", URL: "https://x/2"}, // new → kept
		{ID: "d", URL: "https://x/2"}, // dupe within batch → dropped
		{ID: "e", URL: ""},            // no URL → kept
		{ID: "f", URL: ""},            // no URL → kept (never collapsed)
	}
	got := appendDedupURL(base, add)
	ids := map[string]bool{}
	for _, o := range got {
		ids[o.ID] = true
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 opps (a,c,e,f), got %d: %v", len(got), ids)
	}
	for _, want := range []string{"a", "c", "e", "f"} {
		if !ids[want] {
			t.Errorf("missing expected opp %q", want)
		}
	}
	if ids["b"] || ids["d"] {
		t.Errorf("duplicate URL was not deduped: %v", ids)
	}
}
