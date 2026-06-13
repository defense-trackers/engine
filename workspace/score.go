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
		// Jesse builds SOFTWARE only (USVs are the one hardware exception). A topic
		// whose deliverable is fabricating a physical device/material is a no-go:
		// zero its capability, strip the (spurious) asset match, and keep it out of
		// act-now / the brief. It stays visible in All (badged) for transparency.
		o.HardwareExcluded = hardwareExcluded(o.Text)
		if o.HardwareExcluded {
			o.Capability, o.MatchedAsset = 0, ""
		} else {
			o.Capability, o.MatchedAsset = capabilityFit(o.Text, cap)
		}
		o.Eligibility = eligibilityScore(o)
		o.DaysLeft, o.Runway = runwayScore(o.Closes, today)
		o.Value = valueScore(o)
		o.Score = o.Capability + o.Eligibility + o.Runway + o.Value
		o.ActNow = !o.HardwareExcluded && o.Eligibility >= 12 && o.Capability >= 20 &&
			o.DaysLeft >= 1 && o.DaysLeft <= 30
	}
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

// hardwareExcluded reports whether an opportunity is a hardware-fabrication ask
// outside Jesse's software-only profile (USV platforms exempted).
func hardwareExcluded(text string) bool {
	t := " " + strings.ToLower(text) + " "
	if anyContains(t, usvSignals) {
		return false // USV platform — he can get it built
	}
	hw := anyContains(t, hardwareFabSignals)
	if !hw {
		return false
	}
	// Hardware nouns present. If there's also a clear software/AI deliverable, keep
	// it (mixed topic with a software angle Jesse can lead). Pure-fab → exclude.
	if anyContains(t, softwareSignals) {
		return false
	}
	return true
}

// capabilityFit returns 0–40 from the best-matching asset and that asset's name.
func capabilityFit(text string, cap *Capabilities) (int, string) {
	if cap == nil || text == "" {
		return 0, ""
	}
	best, bestName := 0, ""
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
		if hits > best {
			best, bestName = hits, a.Name
		}
	}
	// diminishing returns: 1 hit is a real signal, 4+ saturates.
	switch {
	case best == 0:
		return 0, ""
	case best == 1:
		return 16, bestName
	case best == 2:
		return 26, bestName
	case best == 3:
		return 34, bestName
	default:
		return 40, bestName
	}
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
