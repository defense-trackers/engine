package workspace

import (
	"testing"
	"time"
)

func testCaps() *Capabilities {
	return &Capabilities{Assets: []Asset{
		{Name: "rigrun", Terms: []string{"llm", "on-prem", "inference"}, Domains: []string{"on-prem ai"}},
		{Name: "thermalhawk", Terms: []string{"thermal", "drone", "detection"}, Domains: []string{"edge autonomy"}},
	}}
}

func opp(title, text, typ, setaside, closes string) Opportunity {
	o := Opportunity{Title: title, Type: typ, Setaside: setaside, Closes: closes}
	o.Text = text
	return o
}

func TestScoreRanksCapabilityFitHigher(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	opps := []Opportunity{
		opp("On-prem LLM inference for edge", "on-prem llm inference artificial intelligence", "SBIR", "SBIR small business", "2026-07-15"),
		opp("Alfalfa seed grant", "agriculture farming seed", "Grant", "", "2026-07-15"),
	}
	Score(opps, testCaps(), now)
	if opps[0].Score <= opps[1].Score {
		t.Fatalf("capability-fit SBIR (%d) should outrank off-domain grant (%d)", opps[0].Score, opps[1].Score)
	}
	if opps[0].MatchedAsset != "rigrun" {
		t.Fatalf("expected rigrun match, got %q", opps[0].MatchedAsset)
	}
}

func TestActNowFlag(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	// high fit, eligible, closes in 18 days → act now
	a := opp("Thermal drone detection", "thermal drone detection edge autonomy", "SBIR", "SBIR small business", "2026-06-30")
	// same fit but closes in 200 days → not act-now
	b := opp("Thermal drone detection later", "thermal drone detection edge autonomy", "SBIR", "SBIR small business", "2026-12-30")
	opps := []Opportunity{a, b}
	Score(opps, testCaps(), now)
	if !opps[0].ActNow {
		t.Fatalf("near-deadline high-fit eligible opp should be act_now (days=%d, cap=%d, elig=%d)", opps[0].DaysLeft, opps[0].Capability, opps[0].Eligibility)
	}
	if opps[1].ActNow {
		t.Fatal("far-deadline opp should not be act_now")
	}
}

func TestExpiredRunwayZero(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	opps := []Opportunity{opp("Closed thing", "on-prem llm", "SBIR", "SBIR", "2026-03-01")}
	Score(opps, testCaps(), now)
	if opps[0].Runway != 0 || opps[0].ActNow {
		t.Fatalf("expired opp should have runway 0 and not be act_now (runway=%d)", opps[0].Runway)
	}
}

func TestEligibilityParse(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	opps := []Opportunity{
		opp("a", "x", "SBIR", "Total Small Business Set-Aside", "2026-07-01"),
		opp("b", "x", "Contract", "No set aside used", "2026-07-01"),
		opp("c", "x", "Contract", "", "2026-07-01"),
	}
	Score(opps, testCaps(), now)
	if opps[0].Eligibility != 20 || opps[1].Eligibility != 12 || opps[2].Eligibility != 8 {
		t.Fatalf("eligibility parse wrong: %d %d %d", opps[0].Eligibility, opps[1].Eligibility, opps[2].Eligibility)
	}
}
