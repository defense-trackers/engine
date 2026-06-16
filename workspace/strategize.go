package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
)

// Phase 2 — the strategic brain. Three capabilities share one analytical backbone
// (winProbability) so the numbers Jesse sees, the numbers Claude reasons over, and
// the numbers the headless autopilot acts on are always the same:
//   - /api/strategize : Claude reasons across the WHOLE pipeline (which to pursue)
//   - winProbability  : a documented, deterministic per-pursuit win estimate
//   - RunAutopilot    : headless triage that writes/pushes a prioritized brief

// winProbability estimates the probability this pursuit WINS ITS AWARD (Phase I /
// prototype / contract) — a near-term, actionable number, deliberately distinct
// from stageProb (the cumulative funnel all the way to a program of record, which
// drives expected value). It's a transparent heuristic, not a trained model: the
// capability-fit sub-score is the spine, eligibility and runway shape it, stage
// reflects commitment, and Jesse's structural edges (clearance moat, USV prime,
// allied teaming) adjust it. Pre-award results are clamped to [2,95] — never claim
// certainty. Returns the percentage and the human-readable drivers behind it.
func winProbability(o *Opportunity, p Pursuit) (int, []string) {
	// Realized / closed stages: the bid outcome is already known.
	switch p.Stage {
	case "won":
		return 100, []string{"award in hand"}
	case "pilot":
		return 100, []string{"prototype/pilot executing — award won"}
	case "transition":
		return 100, []string{"in transition — award won"}
	case "pom":
		return 100, []string{"programmed into the budget — award won"}
	case "program":
		return 100, []string{"program of record — realized"}
	case "lost", "pass":
		return 0, []string{"closed (" + p.Stage + ")"}
	}
	if o == nil {
		return 5, []string{"no live opportunity matched — verify the topic is open"}
	}
	if o.HardwareExcluded {
		return 1, []string{"out of scope (exotic fabrication) — not biddable"}
	}
	capFit := float64(o.Capability) / 40.0 // sub-score max 40
	elig := float64(o.Eligibility) / 20.0  // max 20
	runway := float64(o.Runway) / 20.0     // max 20
	base := 0.60*capFit + 0.25*elig + 0.15*runway

	reasons := []string{fmt.Sprintf("fit %d/100 (capability %d/40, eligibility %d/20)", o.Score, o.Capability, o.Eligibility)}
	switch p.Stage {
	case "drafting":
		base += 0.05
		reasons = append(reasons, "volume already in progress")
	case "qualifying":
		base += 0.02
	}
	if o.TeamingOnly && !o.USVPrime {
		base *= 0.80
		reasons = append(reasons, "teaming play — needs a hardware prime locked")
	}
	if o.USVPrime {
		reasons = append(reasons, "USV prime — partner builds+funds the vessel")
	}
	if o.ClearanceEdge {
		base += 0.04
		reasons = append(reasons, "clearance/IL5 moat thins the field")
	}
	if o.AlliedEdge {
		base += 0.02
		reasons = append(reasons, "AUKUS/allied edge with the AUS partner")
	}
	if o.DaysLeft >= 0 && o.DaysLeft <= 3 {
		base *= 0.70
		reasons = append(reasons, fmt.Sprintf("only %dd left to submit a strong volume", o.DaysLeft))
	}
	pct := int(base * 100)
	if pct < 2 {
		pct = 2
	}
	if pct > 95 {
		pct = 95
	}
	return pct, reasons
}

// stratRow is one pursuit, fully scored for portfolio reasoning.
type stratRow struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Agency   string  `json:"agency,omitempty"`
	Stage    string  `json:"stage"`
	Fit      int     `json:"fit"`
	Value    int     `json:"value"`     // est. lifetime value, $K
	EV       int     `json:"ev"`        // value * cumulative-to-PoR probability, $K
	WinProb  int     `json:"win_prob"`  // probability of winning the award, %
	Priority int     `json:"priority"`  // ranking key: expected award value, $K
	Weakest  string  `json:"weakest"`   // weakest transition wall
	DaysLeft int     `json:"days_left"` // -1 = n/a
	Closes   string  `json:"closes,omitempty"`
	Asset    string  `json:"asset,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
}

// strategizeRows scores every pursuit and ranks them by expected award value
// (win-probability × lifetime value) — the honest "where to spend my limited
// hours" ordering.
func (s *server) strategizeRows() []stratRow {
	s.mu.Lock()
	byID := map[string]*Opportunity{}
	for i := range s.opps {
		byID[s.opps[i].ID] = &s.opps[i]
	}
	state := make(map[string]Pursuit, len(s.state))
	for k, v := range s.state {
		state[k] = v
	}
	s.mu.Unlock()

	var rows []stratRow
	for id, p := range state {
		o := byID[id]
		stage := p.Stage
		if stage == "" {
			stage = "watching"
		}
		title := p.Title
		agency := p.Agency
		fit, days, asset, closes := 0, -1, "", ""
		if o != nil {
			if title == "" {
				title = o.Title
			}
			if agency == "" {
				agency = o.Agency
			}
			fit, days, asset, closes = o.Score, o.DaysLeft, o.MatchedAsset, o.Closes
		}
		if title == "" {
			title = id
		}
		wp, reasons := winProbability(o, p)
		_, weakest := p.Walls.Readiness()
		ev := int(float64(p.Value) * stageProb[stage])
		priority := p.Value * wp / 100
		rows = append(rows, stratRow{
			ID: id, Title: title, Agency: agency, Stage: stage, Fit: fit,
			Value: p.Value, EV: ev, WinProb: wp, Priority: priority,
			Weakest: weakest, DaysLeft: days, Closes: closes, Asset: asset, Reasons: reasons,
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Priority != rows[j].Priority {
			return rows[i].Priority > rows[j].Priority
		}
		return rows[i].WinProb > rows[j].WinProb
	})
	return rows
}

// hStrategize streams a portfolio-level strategic read: it emits the ranked
// pursuit table first (so the UI can render meters immediately), then Claude's
// cross-pipeline call — which few to pursue, what to drop, how to sequence.
func (s *server) hStrategize(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flush, _ := w.(http.Flusher)
	emit := func(obj any) {
		b, _ := json.Marshal(obj)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flush != nil {
			flush.Flush()
		}
	}

	rows := s.strategizeRows()
	emit(map[string]any{"rows": rows})
	if len(rows) == 0 {
		emit(map[string]string{"t": "No active pursuits yet — open Claude on an opportunity and move it into the pipeline first."})
		emit(map[string]string{"done": "1"})
		return
	}
	backend := assistBackend()
	if backend == "" {
		emit(map[string]string{"error": "Claude isn't connected — the ranking above is still live."})
		return
	}

	var sys strings.Builder
	sys.WriteString("You are Jesse's portfolio capture strategist and co-founder. He has limited hours and cannot pursue everything. Reason ACROSS his whole pipeline and tell him where to spend them. Be decisive and specific — name pursuits, don't hedge. Markdown, under 320 words.\n\n")
	sys.WriteString(builderProfile)
	sys.WriteString("Operate from this doctrine:\n\n")
	sys.Write(playbookMD)
	if ck := LoadCompanyKit(s.opts.Dir); ck != nil {
		sys.WriteString("\n" + ck.kitContext())
	}
	sys.WriteString("\n\nThe table is pre-scored. win_prob = probability of WINNING THE AWARD (deterministic heuristic from capability fit + eligibility + runway + stage + Jesse's edges). EV = lifetime value × cumulative probability of reaching a program of record. priority = expected award value (win_prob × value). Trust these numbers; don't recompute them.\n")

	var p strings.Builder
	p.WriteString("MY PIPELINE (ranked by expected award value):\n\n")
	for i, r := range rows {
		dl := ""
		if r.DaysLeft >= 0 {
			dl = fmt.Sprintf(", closes in %dd", r.DaysLeft)
		}
		val := "value unset"
		if r.Value > 0 {
			val = fmt.Sprintf("$%dK lifetime", r.Value)
		}
		p.WriteString(fmt.Sprintf("%d. %s [%s] — win %d%%, fit %d/100, %s, EV $%dK, weakest wall: %s%s",
			i+1, r.Title, r.Stage, r.WinProb, r.Fit, val, r.EV, r.Weakest, dl))
		if r.Asset != "" {
			p.WriteString(", asset: " + r.Asset)
		}
		p.WriteString("\n")
	}
	p.WriteString("\nGive me the call: which 2–3 to put my hours behind this week and why, which to drop or hold and why, the single highest-leverage move on the top pursuit (engineer its weakest wall), and any deadline that forces sequencing. Ground every pick in the numbers above.")

	if backend == "subscription" {
		runClaudeCLI(emit, sys.String(), p.String())
	} else {
		r, err := claudeOnce(sys.String(), p.String())
		if err != nil {
			emit(map[string]string{"error": err.Error()})
		} else {
			emit(map[string]string{"t": r})
		}
		emit(map[string]string{"done": "1"})
	}
}

// RunAutopilot is the headless triage subcommand: `engine workspace autopilot
// [--push]`. It scores everything, ranks the pipeline by expected award value,
// prints the prioritized picture, and (with --push) sends the daily brief to
// ntfy. Schedulable — the tool works when Jesse isn't looking.
func RunAutopilot(o Options, push bool) error {
	s, err := newServer(o)
	if err != nil {
		return err
	}
	rows := s.strategizeRows()
	br := s.computeBrief(push) // a real push consumes the "new" flags

	fmt.Println(autopilotText(rows, br))
	if push {
		if err := pushNtfy(br); err != nil {
			fmt.Fprintln(os.Stderr, "ntfy push:", err)
			return err
		}
		fmt.Println("(pushed to ntfy)")
	}
	return nil
}

func autopilotText(rows []stratRow, br *Brief) string {
	var b strings.Builder
	b.WriteString("Realizer autopilot — " + br.Generated[:10] + "\n")
	b.WriteString(fmt.Sprintf("Expected (risk-adjusted to PoR) $%dK · ceiling $%dK · %d pursuits · %d act-now · %d new\n",
		br.EV, br.TotalValue, br.Pursuits, br.ActNow, br.NewCount))
	b.WriteString("\nPIPELINE — where the hours go (ranked by expected award value):\n")
	for i, r := range rows {
		if i >= 8 {
			break
		}
		dl := ""
		if r.DaysLeft >= 0 {
			dl = fmt.Sprintf(", %dd left", r.DaysLeft)
		}
		b.WriteString(fmt.Sprintf("  %d. %s [%s] — win %d%%, EV $%dK, weakest: %s%s\n",
			i+1, short(r.Title, 56), r.Stage, r.WinProb, r.EV, r.Weakest, dl))
	}
	if len(br.Deadlines) > 0 {
		b.WriteString("\nDEADLINES:\n")
		for i, it := range br.Deadlines {
			if i >= 6 {
				break
			}
			b.WriteString(fmt.Sprintf("  • %s (%dd)\n", short(it.Title, 56), it.Days))
		}
	}
	return b.String()
}
