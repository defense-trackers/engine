package workspace

import (
	"net/http"
)

// Phase 5e — the win/loss ledger. Records outcomes and, crucially, the win-prob
// predicted at submission, so the heuristic can be calibrated against reality:
// of the bids predicted 50–75% likely, what share actually won? Over time this
// turns the win-probability from a guess into a defensible, tuned model — and a
// credibility record to show partners and program offices.

type ledgerRow struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Stage        string `json:"stage"`
	Outcome      string `json:"outcome"` // won | lost | pending | open
	PredictedWin int    `json:"predicted_win"`
	Value        int    `json:"value"`
	Owner        string `json:"owner,omitempty"`
}

type calBucket struct {
	Band   string `json:"band"`   // e.g. "50–75%"
	N      int    `json:"n"`      // resolved bids in band
	Won    int    `json:"won"`    // of those, won
	Actual int    `json:"actual"` // actual win rate %, -1 if n==0
}

func outcomeForStage(stage string) string {
	switch stage {
	case "won", "pilot", "transition", "pom", "program":
		return "won"
	case "lost", "pass":
		return "lost"
	case "submitted":
		return "pending"
	default:
		return "open"
	}
}

// hLedger returns the outcome ledger + calibration of predicted vs actual.
func (s *server) hLedger(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	rows := make([]ledgerRow, 0, len(s.state))
	for id, p := range s.state {
		title := p.Title
		if title == "" {
			title = id
		}
		rows = append(rows, ledgerRow{
			ID: id, Title: title, Stage: p.Stage, Outcome: outcomeForStage(p.Stage),
			PredictedWin: p.PredictedWin, Value: p.Value, Owner: p.Owner,
		})
	}
	s.mu.Unlock()

	// Headline + calibration over resolved (won/lost) bids with a stamped prediction.
	won, lost, wonValue := 0, 0, 0
	bands := []struct{ lo, hi int }{{0, 25}, {25, 50}, {50, 75}, {75, 101}}
	labels := []string{"0–25%", "25–50%", "50–75%", "75–100%"}
	bn := make([]int, len(bands))
	bw := make([]int, len(bands))
	var brierSum float64
	brierN := 0
	for _, r := range rows {
		if r.Outcome == "won" {
			won++
			wonValue += r.Value
		} else if r.Outcome == "lost" {
			lost++
		}
		if (r.Outcome == "won" || r.Outcome == "lost") && r.PredictedWin > 0 {
			for i, b := range bands {
				if r.PredictedWin >= b.lo && r.PredictedWin < b.hi {
					bn[i]++
					if r.Outcome == "won" {
						bw[i]++
					}
					break
				}
			}
			p := float64(r.PredictedWin) / 100
			actual := 0.0
			if r.Outcome == "won" {
				actual = 1.0
			}
			brierSum += (p - actual) * (p - actual)
			brierN++
		}
	}
	var cal []calBucket
	for i := range bands {
		actual := -1
		if bn[i] > 0 {
			actual = bw[i] * 100 / bn[i]
		}
		cal = append(cal, calBucket{Band: labels[i], N: bn[i], Won: bw[i], Actual: actual})
	}
	decided := won + lost
	winRate := -1
	if decided > 0 {
		winRate = won * 100 / decided
	}
	brier := -1.0
	if brierN > 0 {
		brier = brierSum / float64(brierN)
	}
	writeJSON(w, map[string]any{
		"rows": rows, "won": won, "lost": lost, "decided": decided,
		"win_rate": winRate, "won_value": wonValue, "calibration": cal,
		"brier": brier, "brier_n": brierN,
		"model": s.calReport(), // the closed-loop correction now applied to live predictions
	})
}
