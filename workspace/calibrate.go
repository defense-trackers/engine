package workspace

import (
	"fmt"
	"math"
)

// Outcome-calibration loop. The win-probability heuristic is open-loop on its own:
// it predicts, but never learns. This closes the loop. Resolved bids that carried a
// prediction stamped BEFORE their outcome (no look-ahead) are compared to reality,
// and the average residual — shrunk by a pseudo-count so small samples nudge gently
// rather than swing — becomes a correction applied to every future win-probability
// the operator acts on. With zero outcomes the correction is exactly zero (the raw
// model), so the tool is honest from day one and only bends toward reality as the
// evidence accrues. This is the artifact that turns "plausible heuristic" into a
// calibrated, defensible model with a track record.

// calShrinkK is the pseudo-count: a band of evidence this size is needed before the
// correction reaches half its raw magnitude. Keeps early corrections conservative.
const calShrinkK = 5.0

// calTrustN is the number of resolved outcomes below which the signal is flagged
// "early" — shown, applied, but labelled as not-yet-trustworthy.
const calTrustN = 5

// CalReport is the state of the calibration loop at a point in time.
type CalReport struct {
	N        int    `json:"n"`         // resolved bids with an honest (pre-outcome) prediction
	Shift    int    `json:"shift"`     // signed pct points added to raw predictions
	MeanPred int    `json:"mean_pred"` // mean raw prediction over those bids, %
	MeanAct  int    `json:"mean_act"`  // mean actual outcome (win rate), %
	Trusted  bool   `json:"trusted"`   // N >= calTrustN
	Verdict  string `json:"verdict"`
}

// calibrationReport derives the correction from a snapshot of pursuit state.
func calibrationReport(state map[string]Pursuit) CalReport {
	var sumPred, sumAct, n float64
	for _, p := range state {
		if p.PredictedWin <= 0 {
			continue // no honest pre-outcome prediction → can't calibrate on it
		}
		switch outcomeForStage(p.Stage) {
		case "won":
			sumAct++
			sumPred += float64(p.PredictedWin) / 100
			n++
		case "lost":
			sumPred += float64(p.PredictedWin) / 100
			n++
		}
	}
	if n == 0 {
		return CalReport{Verdict: "No resolved bids yet — win-probabilities are the raw model (uncalibrated). Log won/lost outcomes to start the loop."}
	}
	// Shrunk mean residual: dividing the total residual by (n + K) both averages and
	// pulls toward zero, so one surprising outcome can't swing the whole model.
	shift := int(math.Round((sumAct - sumPred) / (n + calShrinkK) * 100))
	r := CalReport{
		N:        int(n),
		Shift:    shift,
		MeanPred: int(math.Round(sumPred / n * 100)),
		MeanAct:  int(math.Round(sumAct / n * 100)),
		Trusted:  int(n) >= calTrustN,
	}
	r.Verdict = calVerdict(r)
	return r
}

func calVerdict(r CalReport) string {
	var base string
	switch {
	case r.Shift <= -4:
		base = fmt.Sprintf("Overconfident by ~%d pts — the model predicts higher than you actually win.", -r.Shift)
	case r.Shift >= 4:
		base = fmt.Sprintf("Underconfident by ~%d pts — you win more often than the model predicts.", r.Shift)
	default:
		d := r.Shift
		if d < 0 {
			d = -d
		}
		base = fmt.Sprintf("Well-calibrated (within ±%d pts).", d)
	}
	suffix := "s"
	if r.N == 1 {
		suffix = ""
	}
	if r.N < calTrustN {
		return fmt.Sprintf("%s Early signal from %d outcome%s — ~%d more to trust it.", base, r.N, suffix, calTrustN-r.N)
	}
	return fmt.Sprintf("%s Calibrated on %d resolved bid%s.", base, r.N, suffix)
}

// calibrate applies the calibration shift to a raw win-probability, leaving realized
// facts (0 = lost/out-of-scope, 100 = already won) untouched, and clamping the rest.
func calibrate(raw, shift int) int {
	if raw <= 1 || raw >= 100 {
		return raw
	}
	c := raw + shift
	if c < 2 {
		c = 2
	}
	if c > 95 {
		c = 95
	}
	return c
}

// calReport snapshots state under the lock and computes the current calibration.
func (s *server) calReport() CalReport {
	s.mu.Lock()
	st := make(map[string]Pursuit, len(s.state))
	for k, v := range s.state {
		st[k] = v
	}
	s.mu.Unlock()
	return calibrationReport(st)
}
