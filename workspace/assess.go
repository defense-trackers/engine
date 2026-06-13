package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Auto-assessment: ask Claude (subscription by default) for a structured starting
// read on a pursuit — estimated lifetime value + the four transition walls — so
// the Profit view and weakest-wall guidance are populated from day one instead of
// blank. Non-streaming + JSON-parsed.

type Assessment struct {
	ValueK       int    `json:"value_k"`
	Money        string `json:"money"`
	Requirements string `json:"requirements"`
	Contracts    string `json:"contracts"`
	Incentives   string `json:"incentives"`
	Stage        string `json:"stage"`
	Rationale    string `json:"rationale"`
}

// claudeOnce runs a single non-streaming completion through the active backend.
func claudeOnce(system, prompt string) (string, error) {
	switch assistBackend() {
	case "subscription":
		f, err := os.CreateTemp("", "ws-asys-*.txt")
		if err != nil {
			return "", err
		}
		defer os.Remove(f.Name())
		f.WriteString(system)
		f.Close()
		cmd := exec.Command("claude", "-p",
			"--system-prompt-file", f.Name(),
			"--model", assistModel(),
			"--output-format", "text")
		cmd.Stdin = strings.NewReader(prompt)
		var out, errb bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("claude CLI: %v: %s", err, strings.TrimSpace(errb.String()))
		}
		return out.String(), nil
	case "api":
		key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
		body, _ := json.Marshal(map[string]any{
			"model": assistModel(), "max_tokens": 1200, "system": system,
			"messages": []map[string]string{{"role": "user", "content": prompt}},
		})
		req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("content-type", "application/json")
		resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			return "", fmt.Errorf("api %d: %s", resp.StatusCode, redactKey(string(b)))
		}
		var r struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		json.Unmarshal(b, &r)
		if len(r.Content) > 0 {
			return r.Content[0].Text, nil
		}
		return "", fmt.Errorf("empty response")
	}
	return "", fmt.Errorf("no Claude backend")
}

// assess produces a structured starting assessment for an opportunity/pursuit.
func (s *server) assess(subject *Opportunity, detail string) (*Assessment, error) {
	var sys strings.Builder
	sys.WriteString("You are Jesse's transition strategist. Score a pursuit's STARTING transition readiness honestly using this doctrine:\n\n")
	sys.Write(playbookMD)
	sys.WriteString("\n\nJESSE'S ASSETS:\n")
	if s.caps != nil {
		for _, a := range s.caps.Assets {
			sys.WriteString("- " + a.Name + ": " + strings.Join(a.Domains, ", ") + "\n")
		}
	}
	sys.WriteString("\nRules: each wall is 'gap' (nothing engineered yet), 'partial' (some in place), or 'ready'. A fresh bid with no sponsor/requirement/production-path/career-safe-yes engineered is mostly 'gap' — be honest, the gaps are the work. value_k = realistic LIFETIME value in $K if it reaches a program of record (SBIR Phase I ~150-250, Phase II ~1250-1800, program of record much higher). Reply with ONLY a JSON object, no prose, no code fence.")

	var p strings.Builder
	p.WriteString("Assess this pursuit and return JSON {\"value_k\":int,\"money\":\"gap|partial|ready\",\"requirements\":\"...\",\"contracts\":\"...\",\"incentives\":\"...\",\"stage\":\"watching|qualifying|drafting|submitted|won|pilot|transition|pom|program\",\"rationale\":\"one sentence\"}.\n\n")
	p.WriteString("Title: " + subject.Title + "\nAgency: " + subject.Agency + "\nType: " + subject.Type + "\n")
	if subject.MatchedAsset != "" {
		p.WriteString("Best-matched asset: " + subject.MatchedAsset + "\n")
	}
	if subject.AwardText != "" {
		p.WriteString("Award/notes: " + subject.AwardText + "\n")
	}
	if detail != "" {
		if len(detail) > 4000 {
			detail = detail[:4000]
		}
		p.WriteString("\nTopic text:\n" + detail + "\n")
	}

	raw, err := claudeOnce(sys.String(), p.String())
	if err != nil {
		return nil, err
	}
	js := extractJSON(raw)
	if js == "" {
		return nil, fmt.Errorf("no JSON in response")
	}
	var a Assessment
	if err := json.Unmarshal([]byte(js), &a); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	a.Money = normWall(a.Money)
	a.Requirements = normWall(a.Requirements)
	a.Contracts = normWall(a.Contracts)
	a.Incentives = normWall(a.Incentives)
	return &a, nil
}

func extractJSON(s string) string {
	i := strings.Index(s, "{")
	j := strings.LastIndex(s, "}")
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return ""
}

func normWall(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "ready", "partial", "gap":
		return s
	default:
		return "gap"
	}
}

// applyAssessment merges an assessment into a pursuit without clobbering values
// Jesse already set by hand.
func applyAssessment(p Pursuit, a *Assessment, subj *Opportunity) Pursuit {
	if p.Value == 0 {
		p.Value = a.ValueK
	}
	if p.Walls.Money == "" {
		p.Walls.Money = a.Money
	}
	if p.Walls.Requirements == "" {
		p.Walls.Requirements = a.Requirements
	}
	if p.Walls.Contracts == "" {
		p.Walls.Contracts = a.Contracts
	}
	if p.Walls.Incentives == "" {
		p.Walls.Incentives = a.Incentives
	}
	if p.Stage == "" {
		p.Stage = a.Stage
	}
	if p.Notes == "" && a.Rationale != "" {
		p.Notes = a.Rationale
	}
	if p.Title == "" {
		p.Title = subj.Title
	}
	if p.Agency == "" {
		p.Agency = subj.Agency
	}
	if p.URL == "" {
		p.URL = subj.URL
	}
	p.Updated = nowRFC()
	return p
}

func nowRFC() string { return time.Now().UTC().Format(time.RFC3339) }

// subjectFor resolves the opportunity context for an id: a live opp if present,
// else a lightweight subject built from the stored pursuit.
func (s *server) subjectFor(id string) *Opportunity {
	for i := range s.opps {
		if s.opps[i].ID == id {
			return &s.opps[i]
		}
	}
	if p, ok := s.state[id]; ok {
		return &Opportunity{ID: id, Title: p.Title, Agency: p.Agency, URL: p.URL, AwardText: p.Notes}
	}
	return nil
}

func (s *server) hAssess(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID == "" {
		http.Error(w, "bad request", 400)
		return
	}
	s.mu.Lock()
	subj := s.subjectFor(in.ID)
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	detail := ""
	if subj.DetailRef != "" {
		detail = FetchDSIPDetail(subj.DetailRef)
	}
	a, err := s.assess(subj, detail)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	s.state[in.ID] = applyAssessment(s.state[in.ID], a, subj)
	s.saveState()
	out := s.state[in.ID]
	s.mu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "pursuit": out, "rationale": a.Rationale})
}

// hAssessAll runs the starting assessment over every current pursuit (the
// auto-populate pass). Sequential to be gentle on the backend.
func (s *server) hAssessAll(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	ids := make([]string, 0, len(s.state))
	for id := range s.state {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	done, failed := 0, 0
	for _, id := range ids {
		s.mu.Lock()
		subj := s.subjectFor(id)
		s.mu.Unlock()
		if subj == nil {
			continue
		}
		detail := ""
		if subj.DetailRef != "" {
			detail = FetchDSIPDetail(subj.DetailRef)
		}
		a, err := s.assess(subj, detail)
		if err != nil {
			failed++
			continue
		}
		s.mu.Lock()
		s.state[id] = applyAssessment(s.state[id], a, subj)
		s.saveState()
		s.mu.Unlock()
		done++
	}
	writeJSON(w, map[string]int{"assessed": done, "failed": failed, "total": len(ids)})
}
