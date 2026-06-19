package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// solicitation codes like DON26BZ01-NV010, DLA26BZ02-NV007, DPA26BZ02-DV010.
var solCodeRe = regexp.MustCompile(`[A-Z]{2,4}\d{2}[A-Z]{1,3}\d{2}-[A-Z]{1,2}\d{2,4}`)

// extractCodes pulls solicitation/topic codes out of a pursuit's title so a seeded
// in-flight volume can be matched to its live topic. Falls back to a bare topic
// suffix (NV013, DV010) when no full code is present.
func extractCodes(title string) []string {
	up := strings.ToUpper(title)
	codes := solCodeRe.FindAllString(up, -1)
	if len(codes) == 0 {
		for _, m := range regexp.MustCompile(`\b[A-Z]{2}\d{3}\b`).FindAllString(up, -1) {
			codes = append(codes, m)
		}
	}
	return codes
}

// resolveOpp finds the live opportunity behind a pursuit: a direct ID match, then a
// manual Link, then an auto-match by solicitation code in the title. This is what
// makes win-probability / readiness / EV real for seeded volumes whose tracked ID
// isn't itself a live opp ID.
func resolveOpp(id string, p Pursuit, byID map[string]*Opportunity, opps []Opportunity) (*Opportunity, bool) {
	if o := byID[id]; o != nil {
		return o, false
	}
	if p.Link != "" {
		if o := byID[p.Link]; o != nil {
			return o, true
		}
	}
	codes := extractCodes(p.Title)
	if len(codes) == 0 {
		return nil, false
	}
	for i := range opps {
		hay := strings.ToUpper(opps[i].ID + " " + opps[i].Title)
		for _, c := range codes {
			if strings.Contains(hay, c) {
				return &opps[i], true
			}
		}
	}
	return nil, false
}

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
	OppID    string  `json:"opp_id,omitempty"` // resolved live opp ID to open the cockpit on (else the pursuit ID)
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
	Ready    string  `json:"ready"`     // GO | FIX | NO-GO | — (submission readiness)
	ReadyWhy string  `json:"ready_why,omitempty"`
	Linked   bool    `json:"linked,omitempty"` // scored via a live topic auto-matched to this seeded volume
	Owner    string  `json:"owner,omitempty"`  // team member responsible
	Reasons  []string `json:"reasons,omitempty"`
}

// hasDraft reports whether a submittable volume has been generated for a pursuit.
func (s *server) hasDraft(oppID string) bool {
	_, err := os.Stat(filepath.Join(s.opts.Dir, "drafts", slugify(oppID), "volume.md"))
	return err == nil
}

// submissionState is a deterministic GO / FIX / NO-GO call on whether a pursuit is
// ready to submit and worth submitting — the executive "where do my hours convert"
// signal. It combines fit/win-probability, whether a draft exists, and the clock.
func submissionState(o *Opportunity, p Pursuit, winProb int, hasDraft bool) (string, string) {
	switch p.Stage {
	case "won", "pilot", "transition", "pom", "program":
		return "—", "already won"
	case "lost", "pass":
		return "—", "closed (" + p.Stage + ")"
	}
	if o == nil {
		// A tracked volume with no live scored opp behind it (e.g. a seeded
		// in-flight draft). Can't score the bid — the next move is to confirm the
		// topic is open and link it, not to write it off.
		if hasDraft {
			return "FIX", "draft exists but no live topic matched — verify it's open, then compliance-check"
		}
		return "FIX", "no live topic matched — verify the solicitation is open"
	}
	if o.HardwareExcluded {
		return "NO-GO", "out of scope (exotic fabrication)"
	}
	if o.DaysLeft == 0 {
		return "NO-GO", "closes today — no runway to finish a strong volume"
	}
	if winProb < 12 {
		return "NO-GO", "win-probability too low to spend the hours"
	}
	tight := o != nil && o.DaysLeft >= 0 && o.DaysLeft <= 3
	if hasDraft && winProb >= 25 && !tight {
		return "GO", "draft in hand, win-prob viable, runway OK — verify compliance and submit"
	}
	// Actionable but not ready: say the single biggest blocker.
	switch {
	case !hasDraft:
		return "FIX", "no draft yet — run a full workup"
	case tight:
		return "FIX", fmt.Sprintf("only %dd left — finish + compliance-check now", o.DaysLeft)
	default:
		return "FIX", "raise win-probability / readiness before committing"
	}
}

// strategizeRows scores every pursuit and ranks them by expected award value
// (win-probability × lifetime value) — the honest "where to spend my limited
// hours" ordering.
func (s *server) strategizeRows() []stratRow {
	s.mu.Lock()
	opps := make([]Opportunity, len(s.opps))
	copy(opps, s.opps)
	byID := map[string]*Opportunity{}
	for i := range opps {
		byID[opps[i].ID] = &opps[i]
	}
	state := make(map[string]Pursuit, len(s.state))
	for k, v := range s.state {
		state[k] = v
	}
	s.mu.Unlock()

	cal := calibrationReport(state) // close the loop: correct raw predictions from logged outcomes
	var rows []stratRow
	for id, p := range state {
		o, linked := resolveOpp(id, p, byID, opps)
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
		wp = calibrate(wp, cal.Shift) // apply the learned correction to the number you act on
		_, weakest := p.Walls.Readiness()
		ev := int(float64(p.Value) * stageProb[stage])
		priority := p.Value * wp / 100
		hasDraft := s.hasDraft(id) || (o != nil && s.hasDraft(o.ID))
		ready, readyWhy := submissionState(o, p, wp, hasDraft)
		oppID := id
		if o != nil {
			oppID = o.ID
		}
		rows = append(rows, stratRow{
			ID: id, OppID: oppID, Title: title, Agency: agency, Stage: stage, Fit: fit,
			Value: p.Value, EV: ev, WinProb: wp, Priority: priority,
			Weakest: weakest, DaysLeft: days, Closes: closes, Asset: asset,
			Ready: ready, ReadyWhy: readyWhy, Linked: linked, Owner: p.Owner, Reasons: reasons,
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

// effortHours estimates the work to get a pursuit to submission-ready. A draft in
// hand is mostly polish + compliance + packaging; a fresh topic needs the volume
// built (more if there's no solicitation text to ground it).
func effortHours(r stratRow, hasDraft bool) int {
	switch r.Stage {
	case "won", "pilot", "transition", "pom", "program", "lost", "pass":
		return 0
	}
	if hasDraft {
		return 6
	}
	switch r.Stage {
	case "drafting":
		return 16
	case "qualifying":
		return 24
	default:
		return 28
	}
}

type allocRow struct {
	stratRow
	Effort    int     `json:"effort"`      // hours to submission-ready
	EVPerHour float64 `json:"ev_per_hour"` // expected award value $K per hour
	Bucket    string  `json:"bucket"`      // fund | defer | tight
}

// allocate distributes an hours budget across actionable pursuits to maximize
// expected award value: greedy by EV-per-hour, deadline-feasible first. Pursuits
// that can't be finished before close at 8h/day are flagged "tight" (a triage
// call, not auto-funded); good pursuits past the budget are "defer".
func (s *server) allocate(budget int) map[string]any {
	const workPerDay = 8
	rows := s.strategizeRows()
	var items []allocRow
	for _, r := range rows {
		if r.Ready == "—" { // already won or closed
			continue
		}
		hd := s.hasDraft(r.ID) || (r.OppID != "" && s.hasDraft(r.OppID))
		eff := effortHours(r, hd)
		if eff == 0 {
			continue
		}
		items = append(items, allocRow{stratRow: r, Effort: eff, EVPerHour: float64(r.Priority) / float64(eff)})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].EVPerHour > items[j].EVPerHour })

	used, evCaptured, evTotal := 0, 0, 0
	for i := range items {
		evTotal += items[i].Priority
		// Don't pour hours into a deadline you can't make, or a low-confidence /
		// unconfirmed pursuit — those are triage calls first, not funded work.
		tight := (items[i].DaysLeft >= 0 && items[i].DaysLeft*workPerDay < items[i].Effort) || items[i].WinProb < 15
		switch {
		case tight:
			items[i].Bucket = "tight"
		case used+items[i].Effort <= budget:
			items[i].Bucket = "fund"
			used += items[i].Effort
			evCaptured += items[i].Priority
		default:
			items[i].Bucket = "defer"
		}
	}
	return map[string]any{
		"budget": budget, "hours_used": used, "items": items,
		"ev_captured": evCaptured, "ev_total": evTotal,
	}
}

// hAllocate returns the week's effort allocation for a budget (?hours=40).
func (s *server) hAllocate(w http.ResponseWriter, r *http.Request) {
	budget := 40
	if h := r.URL.Query().Get("hours"); h != "" {
		if n, err := strconv.Atoi(h); err == nil && n > 0 && n <= 200 {
			budget = n
		}
	}
	writeJSON(w, s.allocate(budget))
}

// hStrategizeData returns just the ranked rows as JSON (no Claude call) — for the
// Crew view and any consumer that wants the scored pipeline without the narrative.
func (s *server) hStrategizeData(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"rows": s.strategizeRows()})
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
	sys.WriteString("\n\nThe table is pre-scored. The [GO]/[FIX]/[NO-GO] tag is submission readiness: GO = a draft is in hand and it's worth submitting; FIX = actionable but blocked (usually no draft yet or clock too tight); NO-GO = don't spend the hours. win_prob = probability of WINNING THE AWARD (deterministic heuristic from capability fit + eligibility + runway + stage + Jesse's edges). EV = lifetime value × cumulative probability of reaching a program of record. priority = expected award value (win_prob × value). Trust these numbers; don't recompute them. Weight your recommendation toward GO/FIX pursuits with high priority and a near deadline.\n")

	var p strings.Builder
	p.WriteString("MY PIPELINE (ranked by expected award value):\n\n")
	for i, r := range rows {
		dl := ""
		if r.DaysLeft >= 0 {
			dl = fmt.Sprintf(", closes in %dd", r.DaysLeft)
		}
		val := "value unset"
		if r.Value > 0 {
			val = reportMoney(r.Value) + " lifetime"
		}
		p.WriteString(fmt.Sprintf("%d. [%s] %s [%s] — win %d%%, fit %d/100, %s, EV %s, weakest wall: %s%s",
			i+1, r.Ready, r.Title, r.Stage, r.WinProb, r.Fit, val, reportMoney(r.EV), r.Weakest, dl))
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
	b.WriteString(fmt.Sprintf("Expected (risk-adjusted to PoR) %s · ceiling %s · %d pursuits · %d act-now · %d new\n",
		reportMoney(br.EV), reportMoney(br.TotalValue), br.Pursuits, br.ActNow, br.NewCount))
	b.WriteString("\nPIPELINE — where the hours go (ranked by expected award value):\n")
	for i, r := range rows {
		if i >= 8 {
			break
		}
		dl := ""
		if r.DaysLeft >= 0 {
			dl = fmt.Sprintf(", %dd left", r.DaysLeft)
		}
		b.WriteString(fmt.Sprintf("  %d. [%s] %s [%s] — win %d%%, EV %s, weakest: %s%s\n",
			i+1, r.Ready, short(r.Title, 52), r.Stage, r.WinProb, reportMoney(r.EV), r.Weakest, dl))
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
