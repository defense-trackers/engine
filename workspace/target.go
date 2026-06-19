package workspace

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
)

// Revenue target tracker. Orients the whole pipeline toward a number: realized
// (won) dollars + the expected award value still in flight, against a goal, with
// the highest-leverage pursuits to advance to close the gap.

func (s *server) targetPath() string { return filepath.Join(s.opts.Dir, "target.json") }

type targetCfg struct {
	TargetK int `json:"target_k"`
}

func (s *server) loadTargetK() int {
	if b, err := os.ReadFile(s.targetPath()); err == nil {
		var c targetCfg
		if json.Unmarshal(b, &c) == nil {
			return c.TargetK
		}
	}
	return 0
}

// hTarget GETs the goal picture, or POSTs a new target.
func (s *server) hTarget(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in targetCfg
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.TargetK < 0 {
			http.Error(w, "bad request", 400)
			return
		}
		b, _ := json.MarshalIndent(in, "", " ")
		if os.WriteFile(s.targetPath(), b, 0o644) != nil {
			http.Error(w, "save failed", 500)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "target_k": in.TargetK})
		return
	}

	target := s.loadTargetK()
	rows := s.strategizeRows()
	realized, pipeline := 0, 0
	type lever struct {
		Title    string `json:"title"`
		OppID    string `json:"opp_id"`
		Expected int    `json:"expected"`
		WinProb  int    `json:"win_prob"`
		Ready    string `json:"ready"`
	}
	var levers []lever
	for _, r := range rows {
		switch outcomeForStage(r.Stage) {
		case "won":
			realized += r.Value
		case "lost":
			// no contribution
		default: // open | pending — still in flight
			pipeline += r.Priority // expected award value (win% × value)
			levers = append(levers, lever{r.Title, r.OppID, r.Priority, r.WinProb, r.Ready})
		}
	}
	sort.SliceStable(levers, func(i, j int) bool { return levers[i].Expected > levers[j].Expected })
	if len(levers) > 5 {
		levers = levers[:5]
	}
	projected := realized + pipeline
	pct := 0
	if target > 0 {
		pct = projected * 100 / target
	}
	writeJSON(w, map[string]any{
		"target_k": target, "realized": realized, "pipeline_expected": pipeline,
		"projected": projected, "pct": pct, "levers": levers,
	})
}
