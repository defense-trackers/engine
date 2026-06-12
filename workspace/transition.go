package workspace

// The lifecycle runs the full path to profit — not just to "submitted". Each stage
// carries a rough conversion probability so the pipeline can weight expected revenue.
// Closed stages (lost/pass) are 0.
var stageProb = map[string]float64{
	"watching":   0.05,
	"qualifying": 0.10,
	"drafting":   0.15,
	"submitted":  0.25,
	"won":        0.40, // award
	"pilot":      0.55, // Phase I / OTA prototype executing
	"transition": 0.70, // second-valley engineering underway
	"pom":        0.85, // programmed into the budget
	"program":    1.00, // program of record — revenue realized
	"lost":       0.0,
	"pass":       0.0,
}

// Stages in lifecycle order (for the stage pickers).
var Stages = []string{"watching", "qualifying", "drafting", "submitted", "won", "pilot", "transition", "pom", "program", "lost", "pass"}

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
