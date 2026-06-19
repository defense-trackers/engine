package workspace

import "testing"

func TestBuildAwardGraphRanksAndVerdict(t *testing.T) {
	aws := []Award{
		{Firm: "Anduril", Phase: "Phase II", Year: 2025, Amount: 1_500_000},
		{Firm: "Anduril", Phase: "Phase I", Year: 2024, Amount: 150_000},
		{Firm: "Shield AI", Phase: "Phase III", Year: 2025, Amount: 900_000},
		{Firm: "TinyCo", Phase: "Phase I", Year: 2023, Amount: 100_000},
	}
	g := buildAwardGraph(aws)
	firms := g["firms"].([]firmStat)
	if firms[0].Firm != "Anduril" || firms[0].Total != 1_650_000 || firms[0].Count != 2 {
		t.Errorf("ranking wrong: %+v", firms[0])
	}
	if firms[0].Phase2Plus != 1 {
		t.Errorf("Anduril should have 1 Phase II+: %d", firms[0].Phase2Plus)
	}
	if g["distinct"].(int) != 3 {
		t.Errorf("distinct firms: want 3, got %v", g["distinct"])
	}
	// teaming targets = firms with Phase II+ (Anduril, Shield AI), not TinyCo
	teaming := g["teaming"].([]firmStat)
	if len(teaming) != 2 {
		t.Errorf("teaming targets: want 2 (PhII+), got %d", len(teaming))
	}
}

func TestAwardLaneVerdict(t *testing.T) {
	if _, lane := awardLaneVerdict(2, 2, 80); lane != "open" {
		t.Errorf("sparse data should be open lane, got %s", lane)
	}
	if _, lane := awardLaneVerdict(20, 2, 70); lane != "entrenched" {
		t.Errorf("concentrated should be entrenched, got %s", lane)
	}
	if _, lane := awardLaneVerdict(20, 12, 15); lane != "open" {
		t.Errorf("fragmented should read open, got %s", lane)
	}
	if _, lane := awardLaneVerdict(10, 5, 30); lane != "contested" {
		t.Errorf("middle should be contested, got %s", lane)
	}
}

func TestIsPhase2Plus(t *testing.T) {
	for _, p := range []string{"Phase II", "Phase III", "phase ii"} {
		if !isPhase2Plus(p) {
			t.Errorf("%q should be phase 2+", p)
		}
	}
	if isPhase2Plus("Phase I") {
		t.Errorf("Phase I should not be phase 2+")
	}
}
