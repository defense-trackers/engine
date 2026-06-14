package workspace

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// Capabilities is Jesse's profile: the assets he can credibly bid behind. Loaded
// from a local (gitignored) capabilities.json; an example ships in the repo.
type Capabilities struct {
	Assets []Asset `json:"assets"`
}

type Asset struct {
	Name    string   `json:"name"`
	Terms   []string `json:"terms"`
	Domains []string `json:"domains"`
	Repo    string   `json:"repo,omitempty"`    // local repo path; Claude Code reviews it to ground the profile
	Summary string   `json:"summary,omitempty"` // grounded one-liner (filled by `ground`)
	TRL     string   `json:"trl,omitempty"`     // grounded technology readiness level
}

func LoadCapabilities(path string) (*Capabilities, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Capabilities
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Score fills the scoring fields on each opportunity. `now` is injected for
// deterministic tests. Composite (0–100) weights the four drivers Jesse named:
// capability fit (40) · eligibility (20) · runway (20) · value (20).
func Score(opps []Opportunity, cap *Capabilities, now time.Time) {
	today := now.UTC().Truncate(24 * time.Hour)
	for i := range opps {
		o := &opps[i]
		// Always compute the best software-asset match (readiness-weighted: a more
		// bid-ready asset wins ties).
		capScore, asset, trl := capabilityFit(o.Text, cap)
		t := " " + strings.ToLower(o.Text) + " "
		o.USVPrime = anyContains(t, usvSignals)
		switch {
		case o.USVPrime:
			// USV / surface vessel — Jesse has a build path for the vessel, so this is
			// a PRIME play, scored normally (can be act-now).
			o.Capability, o.MatchedAsset, o.MatchedAssetTRL = capScore, asset, trl
		case !hardwareFab(o.Text):
			// Software (or software-led mixed) — normal.
			o.Capability, o.MatchedAsset, o.MatchedAssetTRL = capScore, asset, trl
		case anyContains(t, vehiclePlatformSignals):
			// Autonomous-vehicle / unmanned platform (UUV, UAV, UGV, submersible…).
			// Jesse supplies the autonomy/perception software to a vehicle prime — a
			// teaming play even if no current asset term-matches yet.
			o.TeamingOnly = true
			o.Capability, o.MatchedAsset, o.MatchedAssetTRL = capScore, asset, trl
		case anyContains(t, teamingHardwareSignals) && capScore >= 16:
			// Perception/payload hardware (EO/IR, IRST) where a perception asset is the
			// brain — teaming under a hardware prime.
			o.TeamingOnly = true
			o.Capability, o.MatchedAsset, o.MatchedAssetTRL = capScore, asset, trl
		default:
			// Pure component/material/device fabrication — no software role. Hidden in
			// All by default; never act-now or in the brief.
			o.HardwareExcluded = true
			o.Capability, o.MatchedAsset = 0, ""
		}
		// Clearance/IL5 is Jesse's moat: an active TS/SCI + IL5-built products let him
		// compete where most small businesses can't. Flag it and nudge eligibility.
		o.ClearanceEdge = anyContains(" "+strings.ToLower(o.Text)+" ", clearanceSignals)
		o.Eligibility = eligibilityScore(o)
		if o.ClearanceEdge {
			o.Eligibility += 2
			if o.Eligibility > 20 {
				o.Eligibility = 20
			}
		}
		o.DaysLeft, o.Runway = runwayScore(o.Closes, today)
		o.Value = valueScore(o)
		o.Score = o.Capability + o.Eligibility + o.Runway + o.Value
		// Act-now is for plays Jesse can bid himself (solo software or a USV prime).
		// Hardware-excluded and teaming plays need a hardware prime first.
		o.ActNow = !o.HardwareExcluded && !o.TeamingOnly && o.Eligibility >= 12 &&
			o.Capability >= 20 && o.DaysLeft >= 1 && o.DaysLeft <= 30
	}
}

// teamingHardwareSignals are perception/payload PLATFORM hardware where Jesse's
// software (thermalhawk EO/IR perception, autonomy) is genuinely the processing
// brain — so the hardware topic becomes a software-teaming play under a prime
// rather than a flat pass. Materials/component fabrication is deliberately NOT
// here (software has no role in building a focal plane or nanocrystal).
var teamingHardwareSignals = []string{
	"infrared search and track", "irst", "camera technology", "optical payload",
	"electro-optical payload", "eo/ir payload", "eo-ir payload", "isr payload",
	"imaging payload", "gimbal", "targeting pod", "seeker", "sensor payload",
	"electro-optical/infrared",
}

// clearanceSignals mark topics requiring clearance/classified/IL5 work — Jesse's
// competitive moat. Kept conservative to avoid false positives ("secret" alone,
// "sci" alone are too broad).
var clearanceSignals = []string{
	"ts/sci", "sensitive compartmented", "special access program", " sap ",
	"il5", "il-5", "il6", "il-6", " classified", "security clearance",
	"polygraph", "top secret", "secret clearance",
}

// trlNum extracts the integer TRL from a grounded TRL string ("TRL 6 (…)" → 6);
// -1 when absent. Used to lead with the most bid-ready asset.
func trlNum(s string) int {
	s = strings.ToLower(s)
	i := strings.Index(s, "trl")
	if i < 0 {
		return -1
	}
	for j := i + 3; j < len(s); j++ {
		if s[j] >= '0' && s[j] <= '9' {
			return int(s[j] - '0')
		}
	}
	return -1
}

// USV platform topics — Jesse has a build path for unmanned surface vessels, so
// these are NOT excluded even though they're "hardware."
var usvSignals = []string{
	"unmanned surface", "surface vessel", "surface vehicle", " usv", "usv ",
	"(usv", "autonomous surface", "maritime autonomous surface", " asv ",
}

// Software-deliverable signals — if the ask is fundamentally software/AI/data/cyber,
// it's in Jesse's wheelhouse even when hardware nouns appear in passing.
var softwareSignals = []string{
	"software", "algorithm", "artificial intelligence", "machine learning",
	"ai/ml", " ai ", " ai-", "autonomy", "autonomous software", "deep learning",
	"neural", "llm", "large language model", "generative", "analytics",
	"data ", "cyber", "penetration test", "red team", "rmf", " ato ",
	"authorization to operate", "command and control", " c2 ", "mission planning",
	"decision support", "modeling and simulation", "simulat", "digital twin",
	"workflow", "compliance", "computer vision", "object detection",
	"sensor fusion", "situational awareness", "data center",
}

// Hardware-fabrication signals — the deliverable is a physical device/material.
// These are Jesse's no-go (unless the topic is a USV platform).
var hardwareFabSignals = []string{
	"fabricat", "photodiode", "focal plane", "nanocrystal", "colloidal",
	"semiconductor", "wafer", "antenna", "compressor", "battery", "accelerator",
	"linac", "linear accelerator", "amplifier", "transmitter", "waveguide",
	"coating", "alloy", "circuit", "asic", "mems", "actuator", "propulsion",
	"gimbal", "photodetector", "infrared search and track", "camera technology",
	"thermal management", "heat spreader", "microsystem", "resonant cavity",
	"avalanche", "acoustic sensor", "sonar", "hydrophone", "phased array",
	"radio frequency", "rf front", "power supply", "optical payload",
	"electro-optical payload", "transducer", "x-ray", "detector incorporating",
	"infrared imaging", "imaging in the", "mid-wave infrared", "short wave",
	"particulate sensor", "omni-directional acoustic",
	// device / platform / munition builds (physical deliverables, non-USV)
	"electronics", "seabed", "submersible", "buoy", "stowage", "munition",
	"warhead", "projectile", "propellant", "turbine", " valve", " pump",
	"uuv", "underwater vehicle", "unmanned underwater", "hull", "chassis",
	"enclosure",
}

func anyContains(hay string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}

// hardwareFab reports whether an opportunity's deliverable is fundamentally building
// hardware (a fab signal present), with no software-led angle. USV platforms and
// software-led mixed topics are not hardware-fab.
func hardwareFab(text string) bool {
	t := " " + strings.ToLower(text) + " "
	if anyContains(t, usvSignals) {
		return false // USV platform — Jesse builds these
	}
	if !anyContains(t, hardwareFabSignals) {
		return false
	}
	// Hardware nouns present. A clear software/AI deliverable means he can lead it.
	if anyContains(t, softwareSignals) {
		return false
	}
	return true
}

// vehiclePlatformSignals are autonomous-vehicle / unmanned-platform topics where
// Jesse's autonomy/perception software is the brain — so a hardware build becomes a
// teaming play (he supplies the software to a vehicle prime) rather than a pass.
// USVs are handled separately (he builds those outright).
var vehiclePlatformSignals = []string{
	"unmanned underwater", "uuv", "submersible", "seabed", "autonomous underwater",
	"unmanned aerial", "unmanned aircraft", " uav ", " uas ", "unmanned ground",
	" ugv ", "autonomous vehicle", "autonomous vessel", "autonomous system",
	"maritime autonomous", "robotic vehicle", "robotic logistic", "unmanned system",
	"remotely operated", "ground vehicle", "underwater vehicle",
}

// capabilityFit returns 0–40 from the best-matching asset, plus that asset's name
// and TRL. Readiness-weighted: on equal keyword hits the more bid-ready asset wins
// (so a TRL-6 proven product leads over an early-stage idea), and a bid-ready match
// gets a small bump — Jesse should bid his strongest horse.
func capabilityFit(text string, cap *Capabilities) (int, string, string) {
	if cap == nil || text == "" {
		return 0, "", ""
	}
	best, bestName, bestTRL, bestTRLn := 0, "", "", -1
	for _, a := range cap.Assets {
		hits := 0
		for _, t := range a.Terms {
			if t != "" && strings.Contains(text, strings.ToLower(t)) {
				hits++
			}
		}
		for _, d := range a.Domains {
			if d != "" && strings.Contains(text, strings.ToLower(d)) {
				hits++
			}
		}
		if hits == 0 {
			continue
		}
		n := trlNum(a.TRL)
		if hits > best || (hits == best && n > bestTRLn) {
			best, bestName, bestTRL, bestTRLn = hits, a.Name, a.TRL, n
		}
	}
	// diminishing returns: 1 hit is a real signal, 4+ saturates.
	var score int
	switch {
	case best == 0:
		return 0, "", ""
	case best == 1:
		score = 16
	case best == 2:
		score = 26
	case best == 3:
		score = 34
	default:
		score = 40
	}
	// proven-asset nudge: a bid-ready match (TRL ≥ 5) edges out an early one.
	if bestTRLn >= 5 && score < 40 {
		score += 2
	}
	return score, bestName, bestTRL
}

func eligibilityScore(o *Opportunity) int {
	hay := strings.ToLower(o.Setaside + " " + o.Type + " " + o.Text + " " + o.AwardText)
	// "full and open" / "no set aside" — anyone (incl. small biz) can bid. Checked
	// first because "no set aside" contains the substring "set aside".
	if strings.Contains(hay, "no set aside") || strings.Contains(hay, "full and open") {
		return 12
	}
	for _, t := range []string{"sbir", "sttr", "small business", "nontraditional", "8(a)", "sdvosb", "wosb", "hubzone", "set-aside", "set aside"} {
		if strings.Contains(hay, t) {
			return 20
		}
	}
	return 8 // unknown
}

// runwayScore returns days-to-close (-1 if none) and a 0–20 score: an ideal
// writing window scores highest, expired scores 0, rolling stays steady.
func runwayScore(closes string, today time.Time) (int, int) {
	d := parseDate(closes)
	if d.IsZero() {
		return -1, 12 // rolling / no fixed date
	}
	days := int(d.UTC().Truncate(24*time.Hour).Sub(today).Hours() / 24)
	switch {
	case days < 0:
		return days, 0 // expired
	case days <= 7:
		return days, 8 // tight
	case days <= 90:
		return days, 20 // ideal writing window
	case days <= 120:
		return days, 14
	default:
		return days, 10 // plan-later
	}
}

func valueScore(o *Opportunity) int {
	hay := strings.ToLower(o.Type + " " + o.AwardText + " " + o.Text)
	score := 8
	switch {
	case strings.Contains(hay, "sbir") || strings.Contains(hay, "sttr"):
		score = 12 // Phase I → II → TACFI/STRATFI ladder
	case strings.Contains(hay, "cso") || strings.Contains(hay, "ot ") || strings.Contains(hay, "ota") || strings.Contains(hay, "baa") || strings.Contains(hay, "prototype"):
		score = 14
	case strings.Contains(hay, "grant"):
		score = 8
	}
	if strings.Contains(hay, "phase ii") || strings.Contains(hay, "tacfi") || strings.Contains(hay, "stratfi") || strings.Contains(hay, "d2p2") {
		score += 4
	}
	if strings.Contains(hay, "$") || strings.Contains(hay, "million") || strings.Contains(hay, "1.25m") {
		score += 2
	}
	if score > 20 {
		score = 20
	}
	return score
}
