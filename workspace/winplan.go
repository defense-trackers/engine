package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Phase 5 — the conversion engine. Scoring and drafting are table stakes; these
// two close the gap between "I have great tech" and "I won the contract":
//
//   - Win Plan: a dated capture-to-award sequence, grounded in the doctrine, the
//     pursuit's weakest transition wall, the deadline, and the sponsor book. It
//     turns the playbook into a calendar.
//   - Compliance gate: a CLOSED-LOOP check that maps every binding requirement to
//     the section of the actual drafted volume that answers it, scores coverage,
//     and surfaces the unaddressed requirements that get a proposal eliminated
//     before its merit is ever read (non-compliance is the #1 disqualifier).
//
// Both stream over SSE and persist their output next to volume.md.

// runAndSave streams a Claude response, accumulating the deltas so the full text
// can be written to a file in the pursuit's draft dir when it completes.
func (s *server) runAndSave(emit func(any), sys, prompt, oppID, filename, header string) {
	var acc strings.Builder
	wrapped := func(obj any) {
		if m, ok := obj.(map[string]string); ok {
			if t, ok := m["t"]; ok {
				acc.WriteString(t)
			}
		}
		emit(obj)
	}
	if assistBackend() == "subscription" {
		runClaudeCLI(wrapped, sys, prompt)
	} else {
		r, err := claudeOnce(sys, prompt)
		if err != nil {
			emit(map[string]string{"error": err.Error()})
			return
		}
		wrapped(map[string]string{"t": r})
		emit(map[string]string{"done": "1"})
	}
	if txt := strings.TrimSpace(acc.String()); txt != "" {
		dir := filepath.Join(s.opts.Dir, "drafts", slugify(oppID))
		_ = os.MkdirAll(dir, 0o755)
		path := filepath.Join(dir, filename)
		if os.WriteFile(path, []byte(header+"\n\n"+txt+"\n"), 0o644) == nil {
			emit(map[string]string{"saved": path})
		}
	}
}

// hWinPlan streams a dated capture-to-award plan for one pursuit.
func (s *server) hWinPlan(w http.ResponseWriter, r *http.Request) {
	emit, ok := sseStart(w)
	if !ok {
		return
	}
	var in struct {
		OppID string `json:"opp_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.OppID == "" {
		emit(map[string]string{"error": "bad request"})
		return
	}
	if assistBackend() == "" {
		emit(map[string]string{"error": "Claude isn't connected."})
		return
	}
	s.mu.Lock()
	opp := s.subjectFor(in.OppID)
	pursuit := s.state[in.OppID]
	sponsors := s.sponsors.Match(opp, 6)
	s.mu.Unlock()
	if opp == nil {
		emit(map[string]string{"error": "opportunity not found — refresh"})
		return
	}
	detail := s.detailFor(opp)
	wp, _ := winProbability(opp, pursuit)
	readiness, weakest := pursuit.Walls.Readiness()
	today := time.Now().Format("Monday, January 2, 2006")

	sys := s.assistSystem(opp, detail, pursuit, sponsors)
	sys += "\n\nYOU ARE NOW PRODUCING A CAPTURE-TO-AWARD WIN PLAN. Convert the doctrine into a concrete, DATED action sequence Jesse can execute. Be specific to THIS opportunity, his matched asset, and the sponsor/POC targets above — no generic advice. Use real dates relative to today and the close date. Markdown."

	var p strings.Builder
	p.WriteString(fmt.Sprintf("Today is %s. ", today))
	if opp.Closes != "" {
		p.WriteString(fmt.Sprintf("This closes %s (%d days out). ", opp.Closes, opp.DaysLeft))
	}
	p.WriteString(fmt.Sprintf("Current win-probability is %d%%, transition readiness %d/100, weakest wall: %s.\n\n", wp, readiness, weakest))
	p.WriteString("Give me the WIN PLAN as a dated sequence from now to award (and the first transition move beyond it):\n")
	p.WriteString("1. **Capture timeline** — a dated checklist from today to submission: the sanctioned Q&A question to file (and by when), which named office/POC to engage and via which channel, the proof point(s) to stand up, the teaming to lock (US-prime + AUS partner within sub limits if hardware), the compliance/format pass, and the submit date with buffer.\n")
	p.WriteString("2. **Engineer the weakest wall (" + weakest + ") first** — the single highest-leverage move to raise readiness, with the concrete first step.\n")
	p.WriteString("3. **Win themes + discriminators** — the one-sentence reason I win and the 3 provable discriminators to thread through the volume.\n")
	p.WriteString("4. **Kill criteria** — the 2–3 signals that should make me PASS and reallocate the hours.\n")
	p.WriteString("5. **Beyond award** — the first transition move (Phase III / 4022(f) / sponsor) to begin even before the award lands.\n")
	p.WriteString("Keep it tight and executable — dates, names, channels, not theory.")

	s.runAndSave(emit, sys, p.String(), opp.ID, "win-plan.md", "# Win plan — "+opp.Title)
}

// hVerifyCompliance runs the closed-loop compliance gate against the drafted volume.
func (s *server) hVerifyCompliance(w http.ResponseWriter, r *http.Request) {
	emit, ok := sseStart(w)
	if !ok {
		return
	}
	var in struct {
		OppID string `json:"opp_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.OppID == "" {
		emit(map[string]string{"error": "bad request"})
		return
	}
	if assistBackend() == "" {
		emit(map[string]string{"error": "Claude isn't connected."})
		return
	}
	s.mu.Lock()
	opp := s.subjectFor(in.OppID)
	s.mu.Unlock()
	if opp == nil {
		emit(map[string]string{"error": "opportunity not found — refresh"})
		return
	}
	detail := s.detailFor(opp)
	reqs := complianceRequirements(detail)
	if len(reqs) == 0 {
		emit(map[string]string{"error": "No binding requirements found to check. Ingest the real solicitation text (Ingest RFP) first."})
		return
	}
	volume, err := os.ReadFile(filepath.Join(s.opts.Dir, "drafts", slugify(opp.ID), "volume.md"))
	if err != nil {
		emit(map[string]string{"error": "No draft to check. Run “Draft volume → files” or “Full workup” first, then verify."})
		return
	}

	var rb strings.Builder
	for i, rq := range reqs {
		rb.WriteString(fmt.Sprintf("REQ-%02d: %s\n", i+1, rq))
	}
	sys := "You are a Government source-selection COMPLIANCE reviewer. You decide whether a proposal survives the pass/fail compliance screen BEFORE merit is scored. Be strict and literal: a requirement is COVERED only if the draft genuinely and specifically addresses it; vague gestures are PARTIAL; silence is MISSING. Missing requirements are disqualifiers — lead with them."
	var p strings.Builder
	p.WriteString("BINDING REQUIREMENTS extracted from the solicitation:\n")
	p.WriteString(rb.String())
	p.WriteString("\nJESSE'S DRAFT VOLUME:\n")
	p.WriteString(string(volume))
	p.WriteString("\n\nProduce the compliance verdict in markdown:\n")
	p.WriteString("1. **Coverage** — `X of " + fmt.Sprint(len(reqs)) + " fully covered` and a one-line GO / FIX-FIRST / NO-GO call.\n")
	p.WriteString("2. **Disqualifiers first** — every MISSING requirement (REQ-NN), what it asks, and the exact section/sentence to add to cover it.\n")
	p.WriteString("3. **Partials** — each PARTIAL (REQ-NN), the section that gestures at it, and the specific strengthening needed.\n")
	p.WriteString("4. **Covered** — list the REQ-NNs that are genuinely covered and where (brief).\n")
	p.WriteString("Be concrete and reference the volume's section names. Do not pad.")

	s.runAndSave(emit, sys, p.String(), opp.ID, "compliance-report.md", "# Compliance gate — "+opp.Title)
}

// hRemediate closes the loop: it regenerates ready-to-paste content for every
// binding requirement the draft doesn't fully cover, so the volume becomes
// submittable instead of merely diagnosed.
func (s *server) hRemediate(w http.ResponseWriter, r *http.Request) {
	emit, ok := sseStart(w)
	if !ok {
		return
	}
	var in struct {
		OppID string `json:"opp_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.OppID == "" {
		emit(map[string]string{"error": "bad request"})
		return
	}
	if assistBackend() == "" {
		emit(map[string]string{"error": "Claude isn't connected."})
		return
	}
	s.mu.Lock()
	opp := s.subjectFor(in.OppID)
	pursuit := s.state[in.OppID]
	sponsors := s.sponsors.Match(opp, 6)
	s.mu.Unlock()
	if opp == nil {
		emit(map[string]string{"error": "opportunity not found — refresh"})
		return
	}
	detail := s.detailFor(opp)
	reqs := complianceRequirements(detail)
	if len(reqs) == 0 {
		emit(map[string]string{"error": "No binding requirements to remediate against. Ingest the real solicitation text first."})
		return
	}
	volume, err := os.ReadFile(filepath.Join(s.opts.Dir, "drafts", slugify(opp.ID), "volume.md"))
	if err != nil {
		emit(map[string]string{"error": "No draft to remediate. Run a draft first, then close the gaps."})
		return
	}
	var rb strings.Builder
	for i, rq := range reqs {
		rb.WriteString(fmt.Sprintf("REQ-%02d: %s\n", i+1, rq))
	}
	// Full grounding (dossier, company kit, doctrine) so the new content is real, not filler.
	sys := s.assistSystem(opp, detail, pursuit, sponsors)
	sys += "\n\nYOU ARE NOW CLOSING COMPLIANCE GAPS. For each binding requirement the draft does NOT fully and specifically cover, write the exact, ready-to-paste content that makes it compliant — grounded in Jesse's real assets, no placeholders, no filler. Requirements the draft already covers well: skip them. This text will be pasted straight into the volume."
	var p strings.Builder
	p.WriteString("BINDING REQUIREMENTS:\n")
	p.WriteString(rb.String())
	p.WriteString("\nCURRENT DRAFT VOLUME:\n")
	p.WriteString(string(volume))
	p.WriteString("\n\nFor each requirement that is MISSING or only PARTIAL in the draft, output a block:\n")
	p.WriteString("### REQ-NN — <short label> · add to: <section name>\n<the exact paragraph(s) to insert — concrete, grounded, submission-grade>\n\n")
	p.WriteString("Cover every gap so all " + fmt.Sprint(len(reqs)) + " requirements would pass a strict compliance screen. If a fact must come from Jesse, write the sentence and mark only the specific unknown in [brackets]. Skip requirements already well covered (say so in one line at the top).")

	s.runAndSave(emit, sys, p.String(), opp.ID, "compliance-fixes.md", "# Compliance fixes (drop-in) — "+opp.Title)
}

// sseStart writes SSE headers and returns an emit func + ok flag.
func sseStart(w http.ResponseWriter) (func(any), bool) {
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
	return emit, true
}
