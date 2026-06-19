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

func TestExtractCodes(t *testing.T) {
	if c := extractCodes("DON26BZ01-NV010 ELLMENT (rigrun+signet)"); len(c) == 0 || c[0] != "DON26BZ01-NV010" {
		t.Errorf("full code: got %v", c)
	}
	if c := extractCodes("NV013 alchemist→TMPC"); len(c) == 0 || c[0] != "NV013" {
		t.Errorf("short code: got %v", c)
	}
	if c := extractCodes("Just a plain title with no code"); len(c) != 0 {
		t.Errorf("no code: got %v", c)
	}
}

func TestResolveOppAutoLink(t *testing.T) {
	opps := []Opportunity{
		{ID: "dsip:DLA26BZ02-NV007", Title: "STRIKE AI cyber"},
		{ID: "dsip:OTHER", Title: "something else"},
	}
	byID := map[string]*Opportunity{}
	for i := range opps {
		byID[opps[i].ID] = &opps[i]
	}
	// seeded pursuit, no direct ID match → auto-match by code in title
	o, linked := resolveOpp("seed:NV007", Pursuit{Title: "DLA26BZ02-NV007 STRIKE AI (auspex+signet)"}, byID, opps)
	if o == nil || !linked || o.ID != "dsip:DLA26BZ02-NV007" {
		t.Errorf("auto-link failed: o=%v linked=%v", o, linked)
	}
	// direct ID match → not flagged as linked
	if o2, lk := resolveOpp("dsip:OTHER", Pursuit{}, byID, opps); o2 == nil || lk {
		t.Errorf("direct match should not be linked: %v %v", o2, lk)
	}
	// no code, no match → nil
	if o3, _ := resolveOpp("seed:x", Pursuit{Title: "no code here"}, byID, opps); o3 != nil {
		t.Errorf("expected nil, got %v", o3)
	}
	// explicit Link override → resolves to the linked opp (flagged linked), even with no code in title
	if o4, lk := resolveOpp("seed:manual", Pursuit{Title: "no code", Link: "dsip:OTHER"}, byID, opps); o4 == nil || !lk || o4.ID != "dsip:OTHER" {
		t.Errorf("explicit Link override failed: o=%v linked=%v", o4, lk)
	}
}

func TestSubmissionState(t *testing.T) {
	strong := &Opportunity{Capability: 38, Eligibility: 18, Runway: 18, DaysLeft: 20}
	// draft in hand, viable win-prob, runway OK → GO
	if st, _ := submissionState(strong, Pursuit{Stage: "drafting"}, 60, true); st != "GO" {
		t.Errorf("strong+draft: want GO, got %s", st)
	}
	// no draft → FIX
	if st, _ := submissionState(strong, Pursuit{Stage: "drafting"}, 60, false); st != "FIX" {
		t.Errorf("no draft: want FIX, got %s", st)
	}
	// closes today → NO-GO
	if st, _ := submissionState(&Opportunity{Capability: 38, Eligibility: 18, Runway: 2, DaysLeft: 0}, Pursuit{Stage: "drafting"}, 60, true); st != "NO-GO" {
		t.Errorf("closes today: want NO-GO, got %s", st)
	}
	// already won → not actionable
	if st, _ := submissionState(strong, Pursuit{Stage: "won"}, 100, true); st != "—" {
		t.Errorf("won: want —, got %s", st)
	}
	// excluded → NO-GO
	if st, _ := submissionState(&Opportunity{HardwareExcluded: true}, Pursuit{Stage: "watching"}, 5, false); st != "NO-GO" {
		t.Errorf("excluded: want NO-GO")
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
