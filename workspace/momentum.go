package workspace

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Pipeline momentum. A bid empire dies from pursuits that quietly stall, so every
// stage transition is logged with a timestamp. Momentum surfaces what advanced
// this week (velocity) and what's gone cold (stalled, no movement) so nothing rots.

type stageEvent struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	From  string `json:"from"`
	To    string `json:"to"`
	TS    string `json:"ts"`
}

func (s *server) momentumPath() string { return filepath.Join(s.opts.Dir, "momentum.json") }

func (s *server) loadMomentum() []stageEvent {
	var ev []stageEvent
	if b, err := os.ReadFile(s.momentumPath()); err == nil {
		_ = json.Unmarshal(b, &ev)
	}
	return ev
}

// logStageChange appends a transition (caller holds s.mu). Capped to the last 500.
func (s *server) logStageChange(id, title, from, to string) {
	ev := append(s.loadMomentum(), stageEvent{ID: id, Title: title, From: from, To: to, TS: nowRFC()})
	if len(ev) > 500 {
		ev = ev[len(ev)-500:]
	}
	if b, err := json.MarshalIndent(ev, "", " "); err == nil {
		_ = os.WriteFile(s.momentumPath(), b, 0o644)
	}
}

func stageIndex(st string) int {
	for i, v := range Stages {
		if v == st {
			return i
		}
	}
	return -1
}

// hMomentum reports velocity (forward transitions this week) and stalled pursuits.
func (s *server) hMomentum(w http.ResponseWriter, _ *http.Request) {
	events := s.loadMomentum()
	s.mu.Lock()
	state := make(map[string]Pursuit, len(s.state))
	for k, v := range s.state {
		state[k] = v
	}
	s.mu.Unlock()

	weekAgo := time.Now().AddDate(0, 0, -7)
	type adv struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		From  string `json:"from"`
		To    string `json:"to"`
	}
	var advanced []adv
	forward := 0
	for _, e := range events {
		t := parseDate(e.TS)
		if t.IsZero() || t.Before(weekAgo) {
			continue
		}
		if stageIndex(e.To) > stageIndex(e.From) { // forward progress
			forward++
			advanced = append(advanced, adv{e.ID, e.Title, e.From, e.To})
		}
	}
	// most recent advances first
	for i, j := 0, len(advanced)-1; i < j; i, j = i+1, j-1 {
		advanced[i], advanced[j] = advanced[j], advanced[i]
	}
	if len(advanced) > 8 {
		advanced = advanced[:8]
	}

	// Stalled: pre-award active pursuits with no movement in 14 days.
	active := map[string]bool{"qualifying": true, "drafting": true, "submitted": true}
	staleBefore := time.Now().AddDate(0, 0, -14)
	type stall struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Stage string `json:"stage"`
		Days  int    `json:"days"`
	}
	var stalled []stall
	for id, p := range state {
		if !active[p.Stage] {
			continue
		}
		t := parseDate(p.Updated)
		if t.IsZero() || t.After(staleBefore) {
			continue
		}
		days := int(time.Since(t).Hours() / 24)
		title := p.Title
		if title == "" {
			title = id
		}
		stalled = append(stalled, stall{id, title, p.Stage, days})
	}
	sort.SliceStable(stalled, func(i, j int) bool { return stalled[i].Days > stalled[j].Days })

	writeJSON(w, map[string]any{"velocity": forward, "advanced": advanced, "stalled": stalled})
}
