package workspace

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Phase 3 — document ingest. Jesse can paste (or drop a .txt of) the real
// solicitation / RFP / sources-sought text for a pursuit; it's stored locally and
// then grounds EVERY AI feature (assist, draft, assess, day-read) so Claude reasons
// over the actual requirement language, not just the short topic blurb. The
// ingested text takes priority over the fetched DSIP detail for that opportunity.

func (s *server) ingestPath() string { return filepath.Join(s.opts.Dir, "ingest.json") }

type ingestEntry struct {
	Text  string `json:"text"`
	Name  string `json:"name,omitempty"`
	Added string `json:"added,omitempty"`
	Chars int    `json:"chars,omitempty"`
}

func (s *server) loadIngest() map[string]ingestEntry {
	m := map[string]ingestEntry{}
	if b, e := os.ReadFile(s.ingestPath()); e == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (s *server) saveIngest(m map[string]ingestEntry) {
	if b, e := json.MarshalIndent(m, "", " "); e == nil {
		_ = os.WriteFile(s.ingestPath(), b, 0o644)
	}
}

// ingestText returns the locally-ingested RFP text for an opportunity, if any.
func (s *server) ingestText(oppID string) ingestEntry {
	return s.loadIngest()[oppID]
}

// detailFor resolves the best grounding text for an opportunity: a hand-ingested
// RFP wins (it's the real requirement Jesse pasted), otherwise the cached DSIP
// topic detail. Every AI feature should ground through this.
func (s *server) detailFor(o *Opportunity) string {
	if o == nil {
		return ""
	}
	if ing := s.ingestText(o.ID); strings.TrimSpace(ing.Text) != "" {
		return ing.Text
	}
	if o.DetailRef != "" {
		return detailCached(s.opts.Dir, o.DetailRef)
	}
	return ""
}

// hIngest stores (POST) or reports (GET) the ingested RFP text for an opportunity.
func (s *server) hIngest(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in struct {
			OppID string `json:"opp_id"`
			Text  string `json:"text"`
			Name  string `json:"name"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.OppID == "" {
			http.Error(w, "bad request", 400)
			return
		}
		m := s.loadIngest()
		txt := strings.TrimSpace(in.Text)
		if txt == "" {
			delete(m, in.OppID) // empty paste clears the ingest
		} else {
			if len(txt) > 200000 {
				txt = txt[:200000] // keep the prompt sane
			}
			m[in.OppID] = ingestEntry{Text: txt, Name: in.Name, Added: time.Now().UTC().Format("2006-01-02"), Chars: len(txt)}
		}
		s.saveIngest(m)
		e := m[in.OppID]
		writeJSON(w, map[string]any{"ok": true, "chars": e.Chars, "name": e.Name})
		return
	}
	e := s.ingestText(r.URL.Query().Get("id"))
	writeJSON(w, map[string]any{"chars": e.Chars, "name": e.Name, "added": e.Added})
}
