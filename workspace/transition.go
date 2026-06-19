package workspace

// stageProb is the CUMULATIVE probability of a pursuit at this stage actually
// reaching program-of-record revenue — the finish line, not just the next gate.
// It multiplies a pursuit's PoR lifetime-value ceiling to give an honest expected
// value. The SBIR→PoR funnel is brutal (well under 5% of Phase I awards ever reach
// a real program), so early stages are deliberately small. These compound up the
// lifecycle; do NOT read "submitted: 0.02" as "2% chance of award" — it's "2%
// chance this submitted bid ends in a funded program of record."
// Closed stages (lost/pass) are 0.
var stageProb = map[string]float64{
	"watching":   0.003, // a topic on the radar
	"qualifying": 0.006,
	"drafting":   0.010, // actively writing a volume
	"submitted":  0.020, // bid is in
	"won":        0.05,  // Phase I / prototype award in hand
	"pilot":      0.12,  // Phase I / OTA prototype executing
	"transition": 0.30,  // second-valley engineering underway (sponsor + requirement)
	"pom":        0.65,  // programmed into the budget
	"program":    1.00,  // program of record — revenue realized
	"lost":       0.0,
	"pass":       0.0,
}

// Stages in lifecycle order (for the stage pickers).
var Stages = []string{"watching", "qualifying", "drafting", "submitted", "won", "pilot", "transition", "pom", "program", "lost", "pass"}

// validStage reports whether s is one of the known lifecycle stages. An unknown
// stage would make a pursuit vanish from every pipeline column, so writes reject it.
func validStage(s string) bool {
	for _, v := range Stages {
		if v == s {
			return true
		}
	}
	return false
}

// Walls is the transition-readiness scorecard — the four structural walls of the
// second valley of death. Each is "" (unset) | "gap" | "partial" | "ready".
type Walls struct {
	Money        string `json:"money,omitempty"`        // resource sponsor + POM + bridge (APFIT/MTA/SWP)
	Requirements string `json:"requirements,omitempty"` // validated requirement / sponsor
	Contracts    string `json:"contracts,omitempty"`    // production path built in (4022(f) / SBIR Phase III)
	Incentives   string `json:"incentives,omitempty"`   // career-safe yes (MOSA / GPR-in-writing / ATO reciprocity)
}

func wallVal(s string) float64 {
	switch s {
	case "ready":
		return 100
	case "partial":
		return 50
	default:
		return 0 // gap or unset
	}
}

// Readiness returns the 0–100 transition-readiness and the weakest wall to engineer next.
func (w Walls) Readiness() (int, string) {
	vals := map[string]float64{"Money": wallVal(w.Money), "Requirements": wallVal(w.Requirements),
		"Contracts": wallVal(w.Contracts), "Incentives": wallVal(w.Incentives)}
	sum, weakest, low := 0.0, "Money", 101.0
	for _, k := range []string{"Money", "Requirements", "Contracts", "Incentives"} {
		sum += vals[k]
		if vals[k] < low {
			low, weakest = vals[k], k
		}
	}
	return int(sum / 4), weakest
}
