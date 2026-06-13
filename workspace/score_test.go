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

func TestHardwareExcluded(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		// pure hardware fabrication — excluded
		{"ir detector", "resonant cavity infrared detector incorporating an avalanche photodiode active region", true},
		{"antenna", "multifrequency position navigation and timing pnt antenna solution", true},
		{"compressor", "compact high-pressure co2 compressor for aviation thermal management systems", true},
		{"battery", "next-generation non-lithium battery technology for resilient military logistics", true},
		{"linac", "creating a mobile l-band linear accelerator linac", true},
		{"nanocrystal", "colloidal nanocrystals for improved mid-wave infrared imaging", true},
		{"acoustic sensor", "high frequency omni-directional acoustic sensor with open architecture telemetry", true},
		{"camera tech", "innovative camera technology for simultaneous imaging in the extended short wave bands", true},
		{"hardened electronics", "temperature-hardened electronics for reliable mission-critical applications", true},
		{"seabed nodes", "low-cost bottoming seabed nodes for unmanned underwater vehicle (uuv) support", true},
		{"submersible package", "universal submersible logistics deployment and stowage package", true},
		// software / AI — kept
		{"agentic cyber", "strengthening defensive cybersecurity and penetration testing through agentic ai and automation", false},
		{"rmf", "ai-assisted rmf pre-adjudication for research development and rapid prototyping environments", false},
		{"voice c2", "resilient voice-enabled artificial intelligence assistant for autonomous logistics command and control", false},
		{"eo detection sw", "active detection of low-observable surface targets through electro-optical means", false},
		// USV exception — kept even though it's a platform build
		{"usv", "low-cost unmanned surface vessel for maritime resupply", false},
		{"asv autonomy", "autonomous surface vehicle (usv) for contested logistics", false},
		// mixed hardware+software — kept (software angle to lead)
		{"x-ray sim", "modernization of flash x-ray simulated environments modeling and simulation", false},
	}
	for _, c := range cases {
		if got := hardwareExcluded(c.text); got != c.want {
			t.Errorf("%s: hardwareExcluded=%v want %v", c.name, got, c.want)
		}
	}
}

func TestHardwareExcludedZerosCapability(t *testing.T) {
	now := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	// thermalhawk terms (thermal/detection) would normally match, but a focal-plane
	// fabrication topic must be excluded and not act-now.
	o := opp("MWIR focal plane", "colloidal nanocrystals for mid-wave infrared imaging thermal detection", "SBIR", "SBIR small business", "2026-06-30")
	opps := []Opportunity{o}
	Score(opps, testCaps(), now)
	if !opps[0].HardwareExcluded {
		t.Fatal("focal-plane fab topic should be hardware-excluded")
	}
	if opps[0].Capability != 0 || opps[0].MatchedAsset != "" || opps[0].ActNow {
		t.Fatalf("excluded topic must have capability 0, no asset, not act-now (cap=%d asset=%q act=%v)",
			opps[0].Capability, opps[0].MatchedAsset, opps[0].ActNow)
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
