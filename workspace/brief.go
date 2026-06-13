package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The proactive autopilot. Instead of waiting for Jesse to open the dashboard, the
// brief computes — from the same scored opportunities + pursuit state — what
// actually needs his attention today: deadlines, open Q&A windows (the sanctioned
// channel, time-boxed), newly-surfaced high-fit opportunities, and the single next
// move on each live pursuit (its weakest transition wall). It renders as a
// designed "Today" view and can be pushed to ntfy so the tool works when he isn't
// looking.

// BriefItem is one actionable line in the daily brief.
type BriefItem struct {
	ID      string `json:"id,omitempty"`
	Kind    string `json:"kind"` // deadline | qa | new | move
	Title   string `json:"title"`
	Detail  string `json:"detail,omitempty"`
	URL     string `json:"url,omitempty"`
	Days    int    `json:"days,omitempty"`    // days-left (deadline/qa); -1 = n/a
	Score   int    `json:"score,omitempty"`   // fit score (new/deadline)
	Asset   string `json:"asset,omitempty"`   // matched asset
	Urgent  bool   `json:"urgent,omitempty"`  // closing this week
	Weakest string `json:"weakest,omitempty"` // weakest wall (move)
}

// Brief is the full daily picture.
type Brief struct {
	Generated  string      `json:"generated"`
	Deadlines  []BriefItem `json:"deadlines"`
	QA         []BriefItem `json:"qa"`
	New        []BriefItem `json:"new"`
	Moves      []BriefItem `json:"moves"`
	EV         int         `json:"ev"`          // probability-weighted expected revenue, $K
	TotalValue int         `json:"total_value"` // raw pipeline value, $K
	Pursuits   int         `json:"pursuits"`
	ActNow     int         `json:"act_now"`
	NewCount   int         `json:"new_count"`
}

// briefState persists what's already been surfaced so "new" stays new exactly once.
type briefState struct {
	Seen map[string]bool `json:"seen"`
	Last string          `json:"last"`
}

func (s *server) briefStatePath() string { return filepath.Join(s.opts.Dir, "brief-state.json") }

func (s *server) loadBriefState() *briefState {
	bs := &briefState{Seen: map[string]bool{}}
	if b, err := os.ReadFile(s.briefStatePath()); err == nil {
		_ = json.Unmarshal(b, bs)
		if bs.Seen == nil {
			bs.Seen = map[string]bool{}
		}
	}
	return bs
}

func (s *server) saveBriefState(bs *briefState) {
	b, _ := json.MarshalIndent(bs, "", " ")
	_ = os.WriteFile(s.briefStatePath(), b, 0o644)
}

// computeBrief builds the brief. markSeen=true records newly-surfaced opps so they
// don't re-appear as "new" (the scheduled push sets this; a passive dashboard load
// does not, so opening the page doesn't silently consume the "new" flags).
func (s *server) computeBrief(markSeen bool) *Brief {
	s.mu.Lock()
	defer s.mu.Unlock()

	bs := s.loadBriefState()
	br := &Brief{Generated: time.Now().UTC().Format(time.RFC3339)}

	// --- Deadlines: live opps closing within 30 days that matter (a pursuit, or
	// act-now, or a strong fit). Sorted nearest-first; ≤7d flagged urgent.
	dedup := map[string]bool{}
	for i := range s.opps {
		o := &s.opps[i]
		_, tracked := s.state[o.ID]
		if o.DaysLeft < 0 || o.DaysLeft > 30 {
			continue
		}
		if !tracked && !o.ActNow && o.Score < 55 {
			continue
		}
		if dedup[o.ID] {
			continue
		}
		dedup[o.ID] = true
		br.Deadlines = append(br.Deadlines, BriefItem{
			ID: o.ID, Kind: "deadline", Title: o.Title,
			Detail: o.Agency + " · " + o.Type, URL: o.URL,
			Days: o.DaysLeft, Score: o.Score, Asset: o.MatchedAsset,
			Urgent: o.DaysLeft <= 7,
		})
	}
	sort.SliceStable(br.Deadlines, func(i, j int) bool {
		if br.Deadlines[i].Days != br.Deadlines[j].Days {
			return br.Deadlines[i].Days < br.Deadlines[j].Days
		}
		return br.Deadlines[i].Score > br.Deadlines[j].Score // same day → strongest fit first
	})
	if len(br.Deadlines) > 15 {
		br.Deadlines = br.Deadlines[:15]
	}

	// --- Open Q&A windows (the sanctioned channel) closing soon.
	for i := range s.opps {
		o := &s.opps[i]
		until, ok := qaOpenUntil(o.Channel)
		if !ok {
			continue
		}
		d := daysUntil(until)
		if d < 0 || d > 30 {
			continue
		}
		br.QA = append(br.QA, BriefItem{
			ID: o.ID, Kind: "qa", Title: o.Title,
			Detail: "Q&A window open — ask the TPOC on the record", URL: o.URL,
			Days: d, Asset: o.MatchedAsset, Urgent: d <= 7,
		})
	}
	sort.SliceStable(br.QA, func(i, j int) bool { return br.QA[i].Days < br.QA[j].Days })
	if len(br.QA) > 8 {
		br.QA = br.QA[:8]
	}

	// --- New high-fit opportunities surfaced since the last brief.
	for i := range s.opps {
		o := &s.opps[i]
		if o.Score < 55 || bs.Seen[o.ID] {
			continue
		}
		if o.DaysLeft >= 0 && o.DaysLeft < 1 { // already closed/closing today
			continue
		}
		br.New = append(br.New, BriefItem{
			ID: o.ID, Kind: "new", Title: o.Title,
			Detail: o.Agency + " · " + o.Type, URL: o.URL,
			Days: o.DaysLeft, Score: o.Score, Asset: o.MatchedAsset,
		})
	}
	sort.SliceStable(br.New, func(i, j int) bool { return br.New[i].Score > br.New[j].Score })
	br.NewCount = len(br.New)
	if len(br.New) > 8 {
		br.New = br.New[:8]
	}
	if markSeen {
		for i := range s.opps {
			if s.opps[i].Score >= 55 {
				bs.Seen[s.opps[i].ID] = true
			}
		}
		bs.Last = br.Generated
		s.saveBriefState(bs)
	}

	// --- Per-pursuit next move: the weakest wall on each live pursuit.
	ev := 0.0
	for id, p := range s.state {
		br.Pursuits++
		br.TotalValue += p.Value
		ev += float64(p.Value) * stageProb[p.Stage]
		if p.Stage == "lost" || p.Stage == "pass" || p.Stage == "program" {
			continue
		}
		rd, weakest := p.Walls.Readiness()
		title := p.Title
		if title == "" {
			if o := s.oppByID(id); o != nil {
				title = o.Title
			} else {
				title = id
			}
		}
		br.Moves = append(br.Moves, BriefItem{
			ID: id, Kind: "move", Title: title,
			Detail: nextMove(p.Stage, weakest, rd), Days: -1,
			Score: rd, Weakest: weakest,
		})
	}
	// surface the least-ready live pursuits first — that's where the work is
	sort.SliceStable(br.Moves, func(i, j int) bool { return br.Moves[i].Score < br.Moves[j].Score })
	if len(br.Moves) > 10 {
		br.Moves = br.Moves[:10]
	}
	br.EV = int(ev)

	for i := range s.opps {
		if s.opps[i].ActNow {
			br.ActNow++
		}
	}
	return br
}

func (s *server) oppByID(id string) *Opportunity {
	for i := range s.opps {
		if s.opps[i].ID == id {
			return &s.opps[i]
		}
	}
	return nil
}

// nextMove names the single highest-leverage action given stage + weakest wall.
func nextMove(stage, weakest string, readiness int) string {
	if stage == "" {
		stage = "watching"
	}
	wall := map[string]string{
		"Money":        "engineer the MONEY wall: name the resource sponsor who owns the funding line and get into the POM conversation (APFIT/MTA/SWP bridge until procurement dollars land).",
		"Requirements": "engineer the REQUIREMENTS wall: get a validated requirement / sponsor owner on record so this isn't an orphan capability.",
		"Contracts":    "engineer the CONTRACTS wall: build in the production path now (OTA 4022(f) follow-on or SBIR Phase III sole-source) so a good pilot converts without a recompete.",
		"Incentives":   "engineer the INCENTIVES wall: make adoption career-safe for the PM — MOSA/open interfaces, Government Purpose Rights in writing, ATO reciprocity.",
	}[weakest]
	if wall == "" {
		wall = "advance the weakest transition wall."
	}
	return fmt.Sprintf("[%s · %d/100 ready] %s", stage, readiness, wall)
}

// qaOpenUntil extracts the close date from a "...Q&A window OPEN until YYYY-MM-DD..."
// channel string.
func qaOpenUntil(channel string) (time.Time, bool) {
	const marker = "OPEN until "
	i := strings.Index(channel, marker)
	if i < 0 {
		return time.Time{}, false
	}
	rest := channel[i+len(marker):]
	if len(rest) < 10 {
		return time.Time{}, false
	}
	t := parseDate(rest[:10])
	if t.IsZero() {
		return time.Time{}, false
	}
	return t, true
}

func daysUntil(d time.Time) int {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	return int(d.UTC().Truncate(24 * time.Hour).Sub(today).Hours() / 24)
}

func (s *server) hBrief(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.computeBrief(false))
}

// --- Push (ntfy) -----------------------------------------------------------

// RunBrief builds the brief headless (no server bound) and prints it; with push,
// it also sends a concise summary to ntfy. This is the schedulable subcommand.
func RunBrief(o Options, push bool) error {
	s, err := newServer(o)
	if err != nil {
		return err
	}
	br := s.computeBrief(push) // a real push consumes the "new" flags
	fmt.Print(briefText(br))
	if push {
		if err := pushNtfy(br); err != nil {
			fmt.Fprintln(os.Stderr, "ntfy push:", err)
			return err
		}
		fmt.Println("\n(pushed to ntfy)")
	}
	return nil
}

// briefText renders a plain-text brief for the terminal / ntfy body.
func briefText(br *Brief) string {
	var b strings.Builder
	b.WriteString("Defense bid brief — " + br.Generated[:10] + "\n")
	b.WriteString(fmt.Sprintf("Expected (risk-adjusted to program of record) $%dK · best-case ceiling $%dK · %d pursuits · %d act-now · %d new\n",
		br.EV, br.TotalValue, br.Pursuits, br.ActNow, br.NewCount))
	line := func(it BriefItem) {
		d := ""
		if it.Days >= 0 {
			d = fmt.Sprintf(" (%dd)", it.Days)
		}
		b.WriteString("  • " + it.Title + d)
		if it.Asset != "" {
			b.WriteString(" → " + it.Asset)
		}
		b.WriteString("\n")
	}
	if len(br.Deadlines) > 0 {
		b.WriteString("\nDEADLINES:\n")
		for _, it := range br.Deadlines {
			line(it)
		}
	}
	if len(br.QA) > 0 {
		b.WriteString("\nQ&A WINDOWS (sanctioned channel):\n")
		for _, it := range br.QA {
			line(it)
		}
	}
	if len(br.New) > 0 {
		b.WriteString("\nNEW HIGH-FIT:\n")
		for _, it := range br.New {
			line(it)
		}
	}
	if len(br.Moves) > 0 {
		b.WriteString("\nNEXT MOVES:\n")
		for _, it := range br.Moves {
			b.WriteString("  • " + it.Title + " — " + it.Detail + "\n")
		}
	}
	return b.String()
}

// pushNtfy posts a concise brief to Jesse's ntfy topic. Topic/URL from env:
//
//	NTFY_URL   full topic URL (e.g. https://ntfy.sh/jesse-bids) — wins if set
//	NTFY_TOPIC topic name appended to https://ntfy.sh/ (or NTFY_SERVER)
func pushNtfy(br *Brief) error {
	url := strings.TrimSpace(os.Getenv("NTFY_URL"))
	if url == "" {
		topic := strings.TrimSpace(os.Getenv("NTFY_TOPIC"))
		if topic == "" {
			return fmt.Errorf("set NTFY_URL or NTFY_TOPIC to enable push")
		}
		server := strings.TrimSpace(os.Getenv("NTFY_SERVER"))
		if server == "" {
			server = "https://ntfy.sh"
		}
		url = strings.TrimRight(server, "/") + "/" + topic
	}

	// Headline: the most urgent thing.
	title := "Defense bids — nothing urgent today"
	for _, it := range br.Deadlines {
		if it.Urgent {
			title = fmt.Sprintf("⏰ %s closes in %dd", short(it.Title, 48), it.Days)
			break
		}
	}
	if title == "Defense bids — nothing urgent today" && br.NewCount > 0 {
		title = fmt.Sprintf("%d new high-fit opportunit%s", br.NewCount, plural(br.NewCount))
	}

	body := briefText(br)
	if len(body) > 3500 {
		body = body[:3500] + "…"
	}
	req, _ := http.NewRequest("POST", url, bytes.NewReader([]byte(body)))
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "dart,defense")
	req.Header.Set("Priority", "default")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy HTTP %d", resp.StatusCode)
	}
	return nil
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
