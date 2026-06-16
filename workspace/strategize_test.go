package workspace

import "testing"

func TestWinProbabilityRealizedStages(t *testing.T) {
	o := &Opportunity{Capability: 30, Eligibility: 18, Runway: 15}
	for _, st := range []string{"won", "pilot", "transition", "pom", "program"} {
		if p, _ := winProbability(o, Pursuit{Stage: st}); p != 100 {
			t.Errorf("stage %s: want 100, got %d", st, p)
		}
	}
	for _, st := range []string{"lost", "pass"} {
		if p, _ := winProbability(o, Pursuit{Stage: st}); p != 0 {
			t.Errorf("stage %s: want 0, got %d", st, p)
		}
	}
}

func TestWinProbabilityClamped(t *testing.T) {
	// hardware-excluded is never biddable
	if p, _ := winProbability(&Opportunity{HardwareExcluded: true}, Pursuit{}); p != 1 {
		t.Errorf("excluded: want 1, got %d", p)
	}
	// a perfect-fit pre-award pursuit stays under the certainty ceiling
	strong := &Opportunity{Capability: 40, Eligibility: 20, Runway: 20, ClearanceEdge: true, AlliedEdge: true}
	if p, _ := winProbability(strong, Pursuit{Stage: "drafting"}); p > 95 || p < 2 {
		t.Errorf("clamp: want [2,95], got %d", p)
	}
	// a zero-fit pursuit floors at 2, not 0
	if p, _ := winProbability(&Opportunity{}, Pursuit{Stage: "watching"}); p < 2 {
		t.Errorf("floor: want >=2, got %d", p)
	}
}

func TestWinProbabilityFitDominates(t *testing.T) {
	hi := &Opportunity{Capability: 38, Eligibility: 18, Runway: 16}
	lo := &Opportunity{Capability: 8, Eligibility: 10, Runway: 16}
	ph, _ := winProbability(hi, Pursuit{Stage: "qualifying"})
	pl, _ := winProbability(lo, Pursuit{Stage: "qualifying"})
	if ph <= pl {
		t.Errorf("higher capability fit should win: hi=%d lo=%d", ph, pl)
	}
}

func TestWinProbabilityTeamingHaircut(t *testing.T) {
	base := &Opportunity{Capability: 30, Eligibility: 16, Runway: 16}
	team := &Opportunity{Capability: 30, Eligibility: 16, Runway: 16, TeamingOnly: true}
	pb, _ := winProbability(base, Pursuit{Stage: "qualifying"})
	pt, _ := winProbability(team, Pursuit{Stage: "qualifying"})
	if pt >= pb {
		t.Errorf("teaming-only should be haircut vs solo: solo=%d team=%d", pb, pt)
	}
	// a USV prime takes no haircut
	usv := &Opportunity{Capability: 30, Eligibility: 16, Runway: 16, TeamingOnly: true, USVPrime: true}
	pu, _ := winProbability(usv, Pursuit{Stage: "qualifying"})
	if pu < pb {
		t.Errorf("USV prime should not be haircut: solo=%d usv=%d", pb, pu)
	}
}
