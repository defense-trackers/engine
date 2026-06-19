package workspace

import "testing"

// validStage gates writes to /api/state: a typo'd or stale stage would drop a
// pursuit out of every pipeline column, so it must be rejected.
func TestValidStage(t *testing.T) {
	for _, s := range Stages {
		if !validStage(s) {
			t.Errorf("known stage %q rejected", s)
		}
	}
	for _, s := range []string{"", "drafted", "Submitted", "in-progress", "unknown"} {
		if validStage(s) {
			t.Errorf("invalid stage %q accepted", s)
		}
	}
}
