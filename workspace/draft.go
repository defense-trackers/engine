package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Draft-to-deliverable: Claude writes the actual submittable volume to editable
// files, section by section, grounded in the live topic text, the matched asset's
// grounded dossier + real metrics, the Company Kit, and the transition doctrine.
// SBIR/STTR → the 12 prescribed sections in fixed order. DARPA (DPA-prefix) →
// a WhitePaper (≤10pp, 4 sections) + a ≤5-slide deck with a quad chart.

type draftSection struct {
	Name     string
	Guidance string
}

// The DoD SBIR/STTR Phase I technical volume — 12 prescribed sections in fixed
// order (verify against the live BAA; count is 12). Phase I is feasibility.
var sbirSections = []draftSection{
	{"Identification and Significance of the Problem or Opportunity", "State the specific problem from the topic and why it matters operationally. Tie directly to the topic's OBJECTIVE. No generic background."},
	{"Phase I Technical Objectives", "List concrete, measurable Phase I objectives (feasibility/architecture — NOT full build). Map each to a topic requirement."},
	{"Phase I Statement of Work (Work Plan)", "Tasks, methods, milestones, deliverables, and the schedule for the Phase I period. Make tasks verifiable. Identify what each task proves."},
	{"Related Work", "Relevant prior work — the matched asset's real, grounded capability and metrics, plus the state of the art. Show command of the field; cite only what the dossier supports."},
	{"Relationship with Future Research or Development", "How Phase I sets up Phase II (stand-alone lab / full integration) and the transition path to a program. Begin with the transition in mind."},
	{"Commercialization Strategy", "DoD transition + dual-use path: resource sponsor, the production vehicle (SBIR Phase III sole-source / OTA 4022(f) follow-on), and the bridge (APFIT/MTA/SWP). Name the office type, not a fabricated person."},
	{"Key Personnel", "The PI and team from the Company Kit, with the experience that makes this credible. Use real bios; do not invent credentials."},
	{"Foreign Citizens", "State any foreign nationals expected to work on the effort and their status, per the topic's ITAR/disclosure requirement. If none, say so."},
	{"Facilities/Equipment", "Facilities and equipment available to perform Phase I. Be concrete and honest about what exists vs. what's accessed."},
	{"Subcontractors/Consultants", "Any subs/consultants and what they provide. If none in Phase I, state that and note the Phase II teaming intent."},
	{"Prior, Current, or Pending Support", "Disclose any prior/current/pending support for the same or overlapping work, per the certification. State 'none' if true."},
	{"Technical Data Rights & Transition Plan", "Assert SBIR data rights; specify what (if anything) is offered as Government Purpose Rights and the scoped transition plan. Use the Company Kit's data-rights stance."},
}

// DARPA white-paper sections (DPA-prefix topics): the WhitePaper is ≤10pp, 4
// sections, paired with a ≤5-slide deck whose first slide is a quad chart.
var darpaSections = []draftSection{
	{"Innovation & Technical Approach", "The core innovation and the technical approach, grounded in the matched asset. What's hard, why it's now feasible, and how you'd prove it."},
	{"Feasibility & Risk", "Why this is feasible (the asset's real metrics/TRL) and the principal risks + mitigations. Be specific and honest — feasibility is the whole game."},
	{"Impact & Transition", "Operational impact and the transition path to a program — sponsor, vehicle, bridge. Begin with the transition in mind."},
	{"Cost, Schedule & Team", "FFP cost realism (≤$300K / 6mo intent), schedule with milestones, and the team's credibility from the Company Kit."},
}

// Solution-brief pathway for SAM contracts / OTAs / CSOs / BAAs (not SBIR's 12 sections).
var solutionSections = []draftSection{
	{"Problem & Operational Need", "The specific operational problem + which command/office owns it. Tie to the solicitation's stated need; no generic background."},
	{"Solution & Key Innovation", "The proposed solution and what's genuinely new, grounded in the matched asset. Lead with the discriminator."},
	{"Technical Approach & Feasibility", "How it works + why it's feasible now (the asset's real metrics/TRL), with principal risks + mitigations."},
	{"Transition & Operational Impact", "Operational impact + the production/scale path (OTA 4022(f) follow-on / Phase III / bridge). Begin with the transition in mind."},
	{"Team & Past Performance", "The team + relevant past performance from the Company Kit; for hardware, the US-prime + AUS-partner teaming structure. Real bios only."},
	{"Cost, Schedule & Deliverables", "FFP cost realism, milestone schedule, and concrete deliverables for the period of performance."},
}

// QuadChart is the DARPA slide-1 quad-chart outline emitted alongside the deck.
var quadChart = []draftSection{
	{"Quad — What & Why (top-left)", "One-line concept + the operational problem it solves."},
	{"Quad — Technical Approach (top-right)", "The approach and the key innovation, grounded in the asset."},
	{"Quad — Deliverables & Schedule (bottom-left)", "Phase deliverables + milestone timeline."},
	{"Quad — Team, Cost & Transition (bottom-right)", "Team, FFP cost, and the transition vehicle."},
}

var nonword = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonword.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// isDARPA detects the DPA-prefix / DARPA pathway from the opportunity.
func isDARPA(o *Opportunity) bool {
	hay := strings.ToLower(o.ID + " " + o.Agency + " " + o.AwardText + " " + o.Title)
	if strings.Contains(hay, "darpa") {
		return true
	}
	// DSIP topic codes like DPA26BZ02-DV010 → "dpa" prefix segment
	code := strings.ToLower(o.ID)
	return strings.Contains(code, ":dpa") || strings.HasPrefix(code, "dpa")
}

// draftPlan is what the UI/CLI reports: the pathway + the section list.
type draftPlan struct {
	Pathway  string
	Sections []draftSection
	Quad     []draftSection // DARPA only
}

// isContract detects SAM contract / OTA / CSO / BAA opportunities (not SBIR/STTR).
func isContract(o *Opportunity) bool {
	hay := strings.ToLower(o.Type + " " + o.AwardText + " " + o.Title)
	if strings.Contains(hay, "sbir") || strings.Contains(hay, "sttr") {
		return false
	}
	return o.Source == "sam" || strings.Contains(hay, "commercial solutions") ||
		strings.Contains(hay, "other transaction") || strings.Contains(hay, " cso") ||
		strings.Contains(hay, " ota") || strings.Contains(hay, "baa") || strings.Contains(hay, "prototype")
}

func planFor(o *Opportunity) draftPlan {
	if isDARPA(o) {
		return draftPlan{Pathway: "DARPA WhitePaper (≤10pp, 4 sections) + ≤5-slide deck w/ quad chart", Sections: darpaSections, Quad: quadChart}
	}
	if isContract(o) {
		return draftPlan{Pathway: "Contract/OTA/CSO solution brief — 6 sections (verify against the solicitation)", Sections: solutionSections}
	}
	return draftPlan{Pathway: "SBIR/STTR Phase I technical volume — 12 prescribed sections", Sections: sbirSections}
}

// draftContext builds the shared grounding every section prompt carries. research,
// when present, is the deep-research brief from the agentic chain.
func (s *server) draftContext(o *Opportunity, detail, research string) string {
	var b strings.Builder
	b.WriteString(builderProfile)
	if strings.TrimSpace(research) != "" {
		b.WriteString("DEEP-RESEARCH BRIEF (use this competitive intel — incumbents, white space, your wedge, proof points — to shape every section):\n" + research + "\n\n")
	}
	b.Write(playbookMD)
	b.WriteString("\n\nPHASE GUARDRAILS: Phase I is feasibility/architecture; a stand-alone lab and full integration are Phase II/III. ")
	b.WriteString("Cite the matched asset's REAL grounded metrics — never invent numbers, identifiers, or past performance. No placeholders.\n\n")

	b.WriteString("OPPORTUNITY: " + o.Title + "\nAgency: " + o.Agency + " · Type: " + o.Type + "\n")
	if o.MatchedAsset != "" {
		b.WriteString("Matched asset: " + o.MatchedAsset + "\n")
	}
	if detail != "" {
		b.WriteString("\nFULL TOPIC TEXT:\n" + detail + "\n")
	}
	// matched asset's grounded dossier (the real metrics/discriminators)
	if o.MatchedAsset != "" {
		dp := filepath.Join(s.opts.Dir, "dossiers", o.MatchedAsset+".md")
		if dossier, err := os.ReadFile(dp); err == nil {
			b.WriteString("\nMATCHED ASSET DOSSIER (grounded from the real repo):\n")
			b.Write(dossier)
			b.WriteString("\n")
		}
	}
	// company kit
	if ck := LoadCompanyKit(s.opts.Dir); ck != nil {
		b.WriteString("\n" + ck.kitContext())
	}
	return b.String()
}

// Draft generates the full volume to workspace/drafts/<oppId>/. progress, when
// non-nil, receives a line per section (for the SSE UI). Returns the output dir.
func (s *server) Draft(o *Opportunity, detail, research string, progress func(string)) (string, error) {
	if assistBackend() == "" {
		return "", fmt.Errorf("Claude isn't connected (install/login Claude Code, or set ANTHROPIC_API_KEY)")
	}
	plan := planFor(o)
	ctx := s.draftContext(o, detail, research)
	outDir := filepath.Join(s.opts.Dir, "drafts", slugify(o.ID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	if strings.TrimSpace(research) != "" {
		os.WriteFile(filepath.Join(outDir, "00-research.md"), []byte("# Deep-research brief\n\n"+strings.TrimSpace(research)+"\n"), 0o644)
	}
	log := func(m string) {
		if progress != nil {
			progress(m)
		}
	}
	log("Pathway: " + plan.Pathway)

	var volume strings.Builder
	volume.WriteString("# " + o.Title + "\n\n_" + plan.Pathway + " · matched asset: " + dash(o.MatchedAsset) + " · drafted " + nowRFC()[:10] + "_\n\n")
	volume.WriteString("> First-draft volume generated from the live topic, the grounded asset dossier, and the Company Kit. Verify every claim, fill identifiers, and check section structure against the live BAA before submission.\n\n")

	sections := plan.Sections
	for i, sec := range sections {
		n := i + 1
		log(fmt.Sprintf("[%d/%d] %s …", n, len(sections), sec.Name))
		sys := "You are drafting ONE section of a real defense proposal as Jesse's strategist. Write only that section's body — focused, concrete, specific to THIS topic and his matched asset. No preamble, no restating the section title, no placeholders or TODOs. If a fact isn't supported by the grounding, write what's true and flag what he must supply in [brackets].\n\nGROUNDING:\n" + ctx
		prompt := fmt.Sprintf("Write section %d of %d: \"%s\".\nGuidance: %s", n, len(sections), sec.Name, sec.Guidance)
		body, err := claudeOnce(sys, prompt)
		if err != nil {
			log("  ! " + sec.Name + ": " + err.Error())
			body = "_(generation failed: " + err.Error() + ")_"
		}
		body = strings.TrimSpace(body)
		fname := fmt.Sprintf("%02d-%s.md", n, slugify(sec.Name))
		os.WriteFile(filepath.Join(outDir, fname), []byte("# "+sec.Name+"\n\n"+body+"\n"), 0o644)
		volume.WriteString("## " + sec.Name + "\n\n" + body + "\n\n")
	}

	// DARPA: also emit the quad-chart + deck outline.
	if len(plan.Quad) > 0 {
		log("Quad chart + slide outline …")
		volume.WriteString("---\n\n# Slide deck (≤5 slides) — slide 1 is the quad chart\n\n")
		for _, q := range plan.Quad {
			sys := "You are filling one cell of a DARPA quad chart for Jesse. One tight paragraph or 3 bullets, grounded, no placeholders.\n\nGROUNDING:\n" + ctx
			body, err := claudeOnce(sys, "Fill: \""+q.Name+"\". "+q.Guidance)
			if err != nil {
				body = "_(failed: " + err.Error() + ")_"
			}
			volume.WriteString("### " + q.Name + "\n\n" + strings.TrimSpace(body) + "\n\n")
		}
		os.WriteFile(filepath.Join(outDir, "00-quad-chart.md"), []byte(volume.String()), 0o644)
	}

	os.WriteFile(filepath.Join(outDir, "volume.md"), []byte(volume.String()), 0o644)

	// Red-team critique pass: an adversarial reviewer scores the draft against the
	// topic + win themes and lists the highest-leverage fixes — written to its own file.
	log("Red-team review of the draft …")
	critSys := "You are a hard, fair Government source-selection evaluator AND a capture coach reviewing Jesse's draft proposal. Score it honestly and find the highest-leverage fixes before submission. Be specific and blunt; reference sections by name.\n\nGROUNDING:\n" + ctx
	critPrompt := "Review this DRAFT against the topic. Output markdown with: (1) a one-line verdict + a 0–100 score; (2) the 3 strongest elements; (3) the 5 highest-leverage fixes (most impactful first), each tied to a section; (4) any compliance/format risks (section structure, page intent, placeholders, unsupported claims); (5) the single sharpest win theme to lead with.\n\nDRAFT:\n" + volume.String()
	if crit, err := claudeOnce(critSys, critPrompt); err == nil && strings.TrimSpace(crit) != "" {
		os.WriteFile(filepath.Join(outDir, "00-reviewer-notes.md"), []byte("# Red-team reviewer notes\n\n"+strings.TrimSpace(crit)+"\n"), 0o644)
		log("Reviewer notes → 00-reviewer-notes.md")
	}

	log("Done → " + outDir)
	return outDir, nil
}

// hDraft streams a full-volume draft over SSE (one event per section) and reports
// the output directory when done.
func (s *server) hDraft(w http.ResponseWriter, r *http.Request) {
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
	var in struct {
		OppID string `json:"opp_id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.OppID == "" {
		emit(map[string]string{"error": "bad request"})
		return
	}
	s.mu.Lock()
	subj := s.subjectFor(in.OppID)
	s.mu.Unlock()
	if subj == nil {
		emit(map[string]string{"error": "opportunity not found — refresh"})
		return
	}
	detail := s.detailFor(subj)
	dir, err := s.Draft(subj, detail, "", func(line string) { emit(map[string]string{"t": line}) })
	if err != nil {
		emit(map[string]string{"error": err.Error()})
		return
	}
	emit(map[string]string{"dir": dir})
	emit(map[string]string{"done": "1"})
}

// hWorkup runs the full agentic chain: deep research → grounded draft → red-team
// critique, streamed as one flow.
func (s *server) hWorkup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flush, _ := w.(http.Flusher)
	emit := func(obj any) { b, _ := json.Marshal(obj); fmt.Fprintf(w, "data: %s\n\n", b); if flush != nil { flush.Flush() } }
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
	subj := s.subjectFor(in.OppID)
	pursuit := s.state[in.OppID]
	sponsors := s.sponsors.Match(subj, 6)
	s.mu.Unlock()
	if subj == nil {
		emit(map[string]string{"error": "opportunity not found — refresh"})
		return
	}
	detail := s.detailFor(subj)
	// Phase 1 — deep research (full grounding: dossier, company kit, competitive field).
	emit(map[string]string{"t": "Phase 1/3 · deep research — competitive landscape, white space, your wedge…"})
	research, err := claudeOnce(s.assistSystem(subj, detail, pursuit, sponsors), assistActions["deepresearch"])
	if err != nil {
		emit(map[string]string{"error": "research: " + err.Error()})
		return
	}
	emit(map[string]string{"t": "Phase 1 complete · research brief saved (00-research.md)."})
	// Phase 2 + 3 — draft grounded in the research, then red-team critique (inside Draft).
	emit(map[string]string{"t": "Phase 2/3 · drafting the volume, grounded in the research…"})
	dir, err := s.Draft(subj, detail, research, func(line string) { emit(map[string]string{"t": line}) })
	if err != nil {
		emit(map[string]string{"error": err.Error()})
		return
	}
	emit(map[string]string{"dir": dir})
	emit(map[string]string{"done": "1"})
}

// hCompanyKit GETs the current kit; POST {"build":true} synthesizes a fresh draft.
func (s *server) hCompanyKit(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in struct {
			Build bool `json:"build"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Build {
			if err := BuildCompanyKit(s.opts.Dir); err != nil {
				writeJSON(w, map[string]string{"error": err.Error()})
				return
			}
		}
	}
	ck := LoadCompanyKit(s.opts.Dir)
	if ck == nil {
		writeJSON(w, map[string]any{"present": false})
		return
	}
	writeJSON(w, map[string]any{"present": true, "kit": ck})
}

// RunCompanyKit is the CLI entrypoint: `engine workspace company-kit --build`.
func RunCompanyKit(o Options, build bool) error {
	if o.Dir == "" {
		o.Dir = `C:\trackers\workspace`
	}
	if build {
		return BuildCompanyKit(o.Dir)
	}
	ck := LoadCompanyKit(o.Dir)
	if ck == nil {
		fmt.Println("no company-kit.json yet — run: engine workspace company-kit --build")
		return nil
	}
	fmt.Print(ck.kitContext())
	return nil
}

// RunDraft is the CLI entrypoint: `engine workspace draft <oppId>`.
func RunDraft(o Options, oppID string) error {
	s, err := newServer(o)
	if err != nil {
		return err
	}
	subj := s.subjectFor(oppID)
	if subj == nil {
		// help the user: list a few close matches
		fmt.Fprintf(os.Stderr, "opportunity %q not found. Try an id from the dashboard (e.g. dsip:XXXX, pipeline:YYYY, or a seed: id).\n", oppID)
		return fmt.Errorf("not found")
	}
	detail := ""
	if subj.DetailRef != "" {
		detail = detailCached(s.opts.Dir, subj.DetailRef)
	}
	dir, err := s.Draft(subj, detail, "", func(m string) { fmt.Println(m) })
	if err != nil {
		return err
	}
	fmt.Println("\nVolume + per-section files written to:", dir)
	return nil
}
