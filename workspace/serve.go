package workspace

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

//go:embed ui/*
var uiFS embed.FS

//go:embed capabilities.example.json
var exampleCaps []byte

//go:embed playbook.md
var playbookMD []byte

//go:embed contacts.example.json
var exampleContacts []byte

// Options configure a workspace run.
type Options struct {
	Port     int
	Dir      string // workspace dir (capabilities/bidstate/cache live here)
	DataBase string // live trackers URL or local site dir
}

// Pursuit is Jesse's private state for one opportunity. Title/Agency/URL let a
// seeded or manually-added pursuit render even when no live opportunity matches.
type Pursuit struct {
	Stage    string `json:"stage"`              // see transition.go Stages (lifecycle to revenue)
	Decision string `json:"decision,omitempty"` // bid|no-bid
	Notes    string `json:"notes,omitempty"`
	Title    string `json:"title,omitempty"`
	Agency   string `json:"agency,omitempty"`
	URL      string `json:"url,omitempty"`
	Value    int    `json:"value,omitempty"` // estimated lifetime value, $K (Phase I→II→bridge→PoR)
	Walls    Walls  `json:"walls,omitempty"` // four-walls transition-readiness scorecard
	Updated  string `json:"updated,omitempty"`
}

type server struct {
	opts     Options
	mu       sync.Mutex
	opps     []Opportunity
	caps     *Capabilities
	sponsors *SponsorBook
	state    map[string]Pursuit
}

// newServer loads the profile, sponsor book, pursuit state, and scored
// opportunities — everything the dashboard and the brief share — without binding a
// port. Run() serves it; the `brief` subcommand reuses it headless.
func newServer(o Options) (*server, error) {
	if o.Dir == "" {
		o.Dir = `C:\trackers\workspace`
	}
	if o.DataBase == "" {
		o.DataBase = "https://defense-trackers.github.io"
	}
	if o.Port == 0 {
		o.Port = 8765
	}
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return nil, err
	}
	s := &server{opts: o, state: map[string]Pursuit{}}
	s.caps = s.loadCaps()
	s.sponsors = LoadSponsors(o.Dir)
	s.loadState()
	s.ingest()
	return s, nil
}

// Run ingests + scores opportunities and serves the private dashboard locally.
func Run(o Options) error {
	s, err := newServer(o)
	if err != nil {
		return err
	}
	o = s.opts

	mux := http.NewServeMux()
	mux.HandleFunc("/api/brief", s.hBrief)
	mux.HandleFunc("/api/dayread", s.hDayRead)
	mux.HandleFunc("/api/opportunities", s.hOpps)
	mux.HandleFunc("/api/state", s.hState)
	mux.HandleFunc("/api/refresh", s.hRefresh)
	mux.HandleFunc("/api/assist", s.hAssist)
	mux.HandleFunc("/api/assist-status", s.hAssistStatus)
	mux.HandleFunc("/api/assess", s.hAssess)
	mux.HandleFunc("/api/assess-all", s.hAssessAll)
	mux.HandleFunc("/api/playbook", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write(playbookMD)
	})
	mux.HandleFunc("/api/profit", s.hProfit)
	mux.HandleFunc("/api/draft", s.hDraft)
	mux.HandleFunc("/api/company-kit", s.hCompanyKit)
	mux.HandleFunc("/api/awards", s.hAwards)
	mux.HandleFunc("/", s.hStatic)

	addr := fmt.Sprintf("127.0.0.1:%d", o.Port)
	fmt.Printf("bid workspace → http://%s   (data: %s, dir: %s)\n", addr, o.DataBase, o.Dir)
	fmt.Printf("opportunities scored: %d   pursuits: %d\n", len(s.opps), len(s.state))
	fmt.Println(samNote())
	return http.ListenAndServe(addr, mux)
}

func (s *server) loadCaps() *Capabilities {
	path := filepath.Join(s.opts.Dir, "capabilities.json")
	if c, err := LoadCapabilities(path); err == nil {
		return c
	}
	// First run: drop the example in place so Jesse can edit it, and use it now.
	_ = os.WriteFile(path, exampleCaps, 0o644)
	var c Capabilities
	_ = json.Unmarshal(exampleCaps, &c)
	fmt.Printf("no capabilities.json — wrote a starter to %s (edit it, then refresh)\n", path)
	return &c
}

func (s *server) statePath() string { return filepath.Join(s.opts.Dir, "bidstate.json") }

func (s *server) loadState() {
	b, err := os.ReadFile(s.statePath())
	if err == nil {
		_ = json.Unmarshal(b, &s.state)
		return
	}
	s.state = seedPipeline() // first run: open on Jesse's known in-flight volumes
	s.saveState()
}

func (s *server) saveState() {
	b, _ := json.MarshalIndent(s.state, "", " ")
	_ = os.WriteFile(s.statePath(), b, 0o644)
}

// ingest loads tracker JSON + DSIP, scores, and caches DSIP for offline reuse.
func (s *server) ingest() {
	var all []Opportunity
	if t, err := LoadTrackerJSON(s.opts.DataBase); err == nil {
		all = append(all, t...)
	}
	if d, err := FetchDSIP(); err == nil && len(d) > 0 {
		all = append(all, d...)
		if b, e := json.Marshal(d); e == nil {
			_ = os.WriteFile(filepath.Join(s.opts.Dir, "dsip.json"), b, 0o644)
		}
	} else {
		// DSIP unreachable (datacenter IP / outage) — fall back to last good cache.
		if b, e := os.ReadFile(filepath.Join(s.opts.Dir, "dsip.json")); e == nil {
			var cached []Opportunity
			if json.Unmarshal(b, &cached) == nil {
				for i := range cached { // rebuild Text (not persisted)
					cached[i].Text = cached[i].searchText()
				}
				all = append(all, cached...)
			}
		}
		fmt.Println("note: DSIP live fetch unavailable; using cached topics if present")
	}
	// Wider radar: workspace-local SAM.gov sweep (USV/autonomous-vehicle/DIU/IARPA),
	// deduped against what we already have by URL. Cached for offline reuse.
	if sam, err := FetchSAM(); err == nil && len(sam) > 0 {
		all = appendDedupURL(all, sam)
		if b, e := json.Marshal(sam); e == nil {
			_ = os.WriteFile(filepath.Join(s.opts.Dir, "sam.json"), b, 0o644)
		}
	} else if b, e := os.ReadFile(filepath.Join(s.opts.Dir, "sam.json")); e == nil {
		var cached []Opportunity
		if json.Unmarshal(b, &cached) == nil {
			for i := range cached {
				cached[i].Text = cached[i].searchText()
			}
			all = appendDedupURL(all, cached)
		}
	}
	Score(all, s.caps, time.Now())
	sort.SliceStable(all, func(i, j int) bool { return all[i].Score > all[j].Score })
	s.mu.Lock()
	s.opps = all
	s.mu.Unlock()
}

// appendDedupURL appends add to base, skipping any whose URL already appears (the
// public SAM feed and the local SAM sweep can surface the same solicitation).
func appendDedupURL(base, add []Opportunity) []Opportunity {
	seen := map[string]bool{}
	for i := range base {
		if u := strings.TrimSpace(base[i].URL); u != "" {
			seen[u] = true
		}
	}
	for i := range add {
		if u := strings.TrimSpace(add[i].URL); u != "" && seen[u] {
			continue
		}
		base = append(base, add[i])
	}
	return base
}

func (o Opportunity) searchText() string {
	return strings.ToLower(o.Title + " " + o.Agency + " " + o.Type + " " + o.Status + " " + o.Setaside + " " + o.AwardText)
}

func (s *server) hOpps(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.opps)
}

// hProfit weights each pursuit's estimated value by its lifecycle conversion
// probability so Jesse sees expected revenue and where money actually converts.
func (s *server) hProfit(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type row struct {
		Stage    string  `json:"stage"`
		Count    int     `json:"count"`
		Value    int     `json:"value"`
		Weighted int     `json:"weighted"`
		Prob     float64 `json:"prob"`
	}
	agg := map[string]*row{}
	totalVal, ev := 0, 0.0
	for _, p := range s.state {
		st := p.Stage
		if st == "" {
			st = "watching"
		}
		a := agg[st]
		if a == nil {
			a = &row{Stage: st, Prob: stageProb[st]}
			agg[st] = a
		}
		a.Count++
		a.Value += p.Value
		w := float64(p.Value) * stageProb[st]
		a.Weighted += int(w)
		totalVal += p.Value
		ev += w
	}
	var rows []row
	for _, st := range Stages {
		if a, ok := agg[st]; ok {
			rows = append(rows, *a)
		}
	}
	writeJSON(w, map[string]any{"stages": rows, "total_value": totalVal, "expected_value": int(ev)})
}

func (s *server) hState(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in struct {
			ID string `json:"id"`
			Pursuit
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID == "" {
			http.Error(w, "bad request", 400)
			return
		}
		s.mu.Lock()
		p := in.Pursuit
		p.Updated = time.Now().UTC().Format(time.RFC3339)
		empty := p.Stage == "" && p.Decision == "" && p.Notes == "" && p.Value == 0 &&
			p.Walls == (Walls{})
		if empty {
			delete(s.state, in.ID) // clearing a pursuit removes it
		} else {
			s.state[in.ID] = p
		}
		s.saveState()
		s.mu.Unlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.state)
}

func (s *server) hRefresh(w http.ResponseWriter, _ *http.Request) {
	s.caps = s.loadCaps()
	s.ingest()
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]int{"opportunities": len(s.opps)})
}

func (s *server) hStatic(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if p == "/" {
		p = "/index.html"
	}
	b, err := uiFS.ReadFile("ui" + p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(p, ".html"):
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case strings.HasSuffix(p, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(p, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.Write(b)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// seedPipeline pre-populates the board with Jesse's known in-flight SBIR volumes
// so the workspace opens on real work. Stages are best-guess — edit in the UI.
func seedPipeline() map[string]Pursuit {
	now := time.Now().UTC().Format(time.RFC3339)
	mk := func(stage, title, agency, notes string) Pursuit {
		return Pursuit{Stage: stage, Title: title, Agency: agency, Notes: notes, Updated: now, Decision: "bid"}
	}
	// NOTE: nothing is "submitted" — Jesse has draft volumes in preparation, but has
	// not submitted to the Government. Stages reflect that; he sets each as it moves.
	return map[string]Pursuit{
		"seed:NV010": mk("drafting", "DON26BZ01-NV010 ELLMENT (rigrun+signet)", "Navy/NAVAIR", "E-2D on-aircraft traceable LLM. Volume drafted — NOT submitted. Verify the open window/next cycle."),
		"seed:NV013": mk("drafting", "NV013 alchemist→TMPC", "DoW", "Memory-safety / differential-equivalence. Volume drafted — NOT submitted. Verify the open window/next cycle."),
		"seed:NV023": mk("drafting", "DON26BZ01-NV023 VIGIL (rigrun+signet+thermalhawk)", "Navy/ONR", "Risk-aware regenerative multimodal ISRT. Volume drafted — NOT submitted. Verify the open window/next cycle."),
		"seed:DV003": mk("drafting", "OSW26BZ02-DV003 rigrun+signet", "OSW (D2P2)", "SCG/PPP/OPSEC + insider-threat. Closes 6/24."),
		"seed:NV007": mk("drafting", "DLA26BZ02-NV007 STRIKE AI (auspex+signet)", "DLA", "Cover-both governance-framed; demo defensive. Closes 6/24."),
		"seed:DV010": mk("drafting", "DPA26BZ02-DV010 HAWKSTACK (thermalhawk)", "DARPA", "ThermalHawk into fielded EO/IR processor. WhitePaper+deck. Closes 6/24."),
		"seed:NV006": mk("drafting", "DLA26BZ02-NV006 ADJUTANT (signet+rigrun)", "DLA", "RMF pre-adjudication, artifact-centric. Closes 6/24."),
	}
}
