package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// Phase 5f — amendment & change watch. Solicitations move: deadlines slip or get
// pulled in, Q&A windows shift, statuses change, topics disappear. Missing an
// amendment loses a bid. Each ingest snapshots the relevant opps and diffs against
// the last run, so "what changed since I last looked" is never a surprise.

type ChangeItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"` // deadline | qa | status | gone
	Detail string `json:"detail"`
	URL    string `json:"url,omitempty"`
	Good   bool   `json:"good,omitempty"` // a change in Jesse's favor (e.g. deadline extended)
}

type watchSnap struct {
	Closes string `json:"closes"`
	QAEnd  string `json:"qa_end"`
	Status string `json:"status"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

func (s *server) watchPath() string { return filepath.Join(s.opts.Dir, "watch.json") }

func (s *server) loadWatch() map[string]watchSnap {
	m := map[string]watchSnap{}
	if b, err := os.ReadFile(s.watchPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// detectChangesLocked diffs the current relevant opps against the last snapshot and
// returns the changes, persisting the new snapshot. Caller holds s.mu.
func (s *server) detectChangesLocked() []ChangeItem {
	prev := s.loadWatch()
	cur := make(map[string]watchSnap)
	var changes []ChangeItem
	seen := map[string]bool{}

	for i := range s.opps {
		o := &s.opps[i]
		_, isPursuit := s.state[o.ID]
		if !isPursuit && !o.ActNow && o.Score < 50 {
			continue // only watch what matters, to keep the signal clean
		}
		qaEnd := ""
		if t, ok := qaOpenUntil(o.Channel); ok {
			qaEnd = t.Format("2006-01-02")
		}
		snap := watchSnap{Closes: o.Closes, QAEnd: qaEnd, Status: o.Status, Title: o.Title, URL: o.URL}
		cur[o.ID] = snap
		seen[o.ID] = true
		old, had := prev[o.ID]
		if !had {
			continue // newly relevant — "new high-fit" is handled by the brief
		}
		if old.Closes != o.Closes && old.Closes != "" && o.Closes != "" {
			extended := o.Closes > old.Closes // YYYY-MM-DD compares lexically
			changes = append(changes, ChangeItem{
				ID: o.ID, Title: o.Title, Kind: "deadline", URL: o.URL, Good: extended,
				Detail: fmt.Sprintf("deadline moved %s → %s (%s)", old.Closes, o.Closes, ifs(extended, "extended", "pulled in")),
			})
		}
		if old.QAEnd != qaEnd {
			switch {
			case qaEnd == "":
				changes = append(changes, ChangeItem{ID: o.ID, Title: o.Title, Kind: "qa", URL: o.URL, Detail: "Q&A window closed"})
			case old.QAEnd == "":
				changes = append(changes, ChangeItem{ID: o.ID, Title: o.Title, Kind: "qa", URL: o.URL, Good: true, Detail: "Q&A window opened — closes " + qaEnd})
			default:
				changes = append(changes, ChangeItem{ID: o.ID, Title: o.Title, Kind: "qa", URL: o.URL, Good: qaEnd > old.QAEnd, Detail: "Q&A window moved " + old.QAEnd + " → " + qaEnd})
			}
		}
		if old.Status != o.Status && o.Status != "" && old.Status != "" {
			changes = append(changes, ChangeItem{ID: o.ID, Title: o.Title, Kind: "status", URL: o.URL, Detail: "status: " + old.Status + " → " + o.Status})
		}
	}
	// Disappeared: was relevant last run, gone from the feed now (closed or pulled).
	for id, old := range prev {
		if !seen[id] {
			changes = append(changes, ChangeItem{ID: id, Title: old.Title, Kind: "gone", URL: old.URL, Detail: "no longer in the feed — likely closed or withdrawn"})
		}
	}

	if b, err := json.MarshalIndent(cur, "", " "); err == nil {
		_ = os.WriteFile(s.watchPath(), b, 0o644)
	}
	return changes
}

// hChanges returns the changes detected on the last ingest.
func (s *server) hChanges(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{"changes": s.changes, "count": len(s.changes)})
}
