package workspace

import "testing"

// stageProb is the cumulative funnel that multiplies every EV figure. The active
// lifecycle must be monotonically non-decreasing (progress never lowers expected
// value), bounded in [0,1], and end at exactly 1.0; closed stages are 0.
func TestStageProbFunnel(t *testing.T) {
	lifecycle := []string{"watching", "qualifying", "drafting", "submitted", "won", "pilot", "transition", "pom", "program"}
	prev := -1.0
	for _, st := range lifecycle {
		p, ok := stageProb[st]
		if !ok {
			t.Fatalf("stageProb missing %q", st)
		}
		if p < 0 || p > 1 {
			t.Fatalf("stageProb[%q]=%v out of [0,1]", st, p)
		}
		if p < prev {
			t.Fatalf("stageProb not monotonic at %q (%v < %v)", st, p, prev)
		}
		prev = p
	}
	if stageProb["program"] != 1.0 {
		t.Fatalf("program (PoR) should be 1.0, got %v", stageProb["program"])
	}
	if stageProb["lost"] != 0 || stageProb["pass"] != 0 {
		t.Fatal("closed stages (lost/pass) must be 0")
	}
}

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
