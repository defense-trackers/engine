package workspace

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// Phase 5d — inline draft editing. Read the generated volume back into the cockpit,
// edit it in place, and save — then the compliance gate re-runs so coverage climbs
// to 100% before submission, without leaving the tool to open files.

// hDraftDoc GETs the current volume markdown for a pursuit, or POSTs an edited one.
func (s *server) hDraftDoc(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in struct {
			OppID   string `json:"opp_id"`
			Content string `json:"content"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.OppID == "" {
			http.Error(w, "bad request", 400)
			return
		}
		s.mu.Lock()
		subj := s.subjectFor(in.OppID)
		s.mu.Unlock()
		if subj == nil {
			http.Error(w, "not found", 404)
			return
		}
		dir := filepath.Join(s.opts.Dir, "drafts", slugify(subj.ID))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "volume.md"), []byte(in.Content), 0o644); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "chars": len(in.Content)})
		return
	}
	s.mu.Lock()
	subj := s.subjectFor(r.URL.Query().Get("id"))
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	b, err := os.ReadFile(filepath.Join(s.opts.Dir, "drafts", slugify(subj.ID), "volume.md"))
	if err != nil {
		writeJSON(w, map[string]any{"exists": false, "content": ""})
		return
	}
	writeJSON(w, map[string]any{"exists": true, "content": string(b)})
}
