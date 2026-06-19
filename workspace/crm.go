package workspace

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Engagement CRM. Capture is won on relationships — sponsors, POCs, the pre-RFP
// conversations — and the fastest way to lose is to let a follow-up slip. Every
// touch is logged with a next-action date; open follow-ups (overdue / due soon)
// are surfaced so nothing drops.

type Touch struct {
	Date       string `json:"date"`
	Who        string `json:"who"`
	Channel    string `json:"channel,omitempty"`
	Note       string `json:"note,omitempty"`
	NextAction string `json:"next_action,omitempty"`
	NextDate   string `json:"next_date,omitempty"`
}

func (s *server) touchesPath() string { return filepath.Join(s.opts.Dir, "touches.json") }

func (s *server) loadTouches() map[string][]Touch {
	m := map[string][]Touch{}
	if b, err := os.ReadFile(s.touchesPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *server) saveTouches(m map[string][]Touch) {
	if b, err := json.MarshalIndent(m, "", " "); err == nil {
		_ = os.WriteFile(s.touchesPath(), b, 0o644)
	}
}

// hTouch lists a pursuit's engagement log (GET ?id=) or appends a touch (POST).
func (s *server) hTouch(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in struct {
			OppID string `json:"opp_id"`
			Touch
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.OppID == "" || strings.TrimSpace(in.Who) == "" {
			http.Error(w, "bad request", 400)
			return
		}
		t := in.Touch
		if t.Date == "" {
			t.Date = time.Now().Format("2006-01-02")
		}
		m := s.loadTouches()
		m[in.OppID] = append(m[in.OppID], t)
		s.saveTouches(m)
		writeJSON(w, map[string]any{"ok": true, "count": len(m[in.OppID])})
		return
	}
	m := s.loadTouches()
	touches := m[r.URL.Query().Get("id")]
	// newest first
	sort.SliceStable(touches, func(i, j int) bool { return touches[i].Date > touches[j].Date })
	writeJSON(w, map[string]any{"touches": touches})
}

// hFollowups returns the open follow-up per pursuit (the latest touch's next-action),
// split into overdue and due-soon — the relationship work that can't slip.
func (s *server) hFollowups(w http.ResponseWriter, _ *http.Request) {
	m := s.loadTouches()
	s.mu.Lock()
	state := make(map[string]Pursuit, len(s.state))
	for k, v := range s.state {
		state[k] = v
	}
	opps := map[string]string{}
	for i := range s.opps {
		opps[s.opps[i].ID] = s.opps[i].Title
	}
	s.mu.Unlock()

	type followup struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Action string `json:"action"`
		Who    string `json:"who"`
		Date   string `json:"date"`
		Days   int    `json:"days"` // days until due (negative = overdue)
	}
	var due []followup
	today := time.Now().Truncate(24 * time.Hour)
	for id, touches := range m {
		// latest touch with a next-action defines the open follow-up
		var latest *Touch
		for i := range touches {
			if touches[i].NextDate == "" || touches[i].NextAction == "" {
				continue
			}
			if latest == nil || touches[i].Date > latest.Date {
				latest = &touches[i]
			}
		}
		if latest == nil {
			continue
		}
		nd := parseDate(latest.NextDate)
		if nd.IsZero() {
			continue
		}
		title := state[id].Title
		if title == "" {
			title = opps[id]
		}
		if title == "" {
			title = id
		}
		due = append(due, followup{
			ID: id, Title: title, Action: latest.NextAction, Who: latest.Who, Date: latest.NextDate,
			Days: int(nd.Sub(today).Hours() / 24),
		})
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].Days < due[j].Days })
	overdue := 0
	for _, f := range due {
		if f.Days < 0 {
			overdue++
		}
	}
	writeJSON(w, map[string]any{"followups": due, "overdue": overdue})
}
