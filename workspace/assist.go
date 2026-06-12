package workspace

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// assist wires the real Claude API into the private workspace. The key is read
// from ANTHROPIC_API_KEY and stays on this machine — nothing here is published,
// so (unlike the public site) a live frontier model is appropriate. This is a
// bid-proposal strategist, NOT any offensive-cyber capability path.

func assistModel() string {
	if m := strings.TrimSpace(os.Getenv("ASSIST_MODEL")); m != "" {
		return m
	}
	return "claude-opus-4-8"
}

// quick-action prompts oriented at conversion (decide → theme → outline → draft).
var assistActions = map[string]string{
	"bidpass":  "Give me a clear BID or PASS recommendation for this opportunity. 2–4 concrete reasons tied to capability fit, eligibility, deadline runway, and award/strategic value. End with one line: 'RECOMMENDATION: BID' or 'RECOMMENDATION: PASS'.",
	"wintheme": "What is the single strongest win theme for this topic using my matched capability? One-sentence theme, then 3 supporting discriminators that separate me from a generic bidder.",
	"outline":  "Outline the proposal in the correct prescribed structure for this opportunity. SBIR/STTR Phase I technical volume = the 12 prescribed sections in fixed order. DARPA (DPA-prefix) = WhitePaper (≤10pp, 4 sections) + ≤5-slide deck with a quad chart. For each section give one line on exactly what to put, given my matched asset and this topic's requirements.",
	"draft":    "Draft the Technical Approach / Phase I objectives for this topic, grounded in my matched asset's real capabilities. Be concrete and specific to the requirements — no placeholders, no generic filler.",
	"gaps":     "What are the gaps between my matched asset and this topic's requirements, and what would I need to build, demonstrate, or partner for to be competitive? Be blunt.",
	// second-valley / transition + profit-realization actions
	"transition": "Begin with the transition in mind for THIS opportunity: does this vehicle have a built-in production/scale path (OTA follow-on production under 10 USC 4022(f), or SBIR Phase III sole-source eligibility)? If not, how do I structure the award now so a successful pilot converts directly into a contract instead of a recompete?",
	"sponsor":    "Help me find and approach the RESOURCE SPONSOR (who owns the money), not just the end user. For this agency/capability, who likely sponsors it, how do I get into the POM conversation early (~2 years before execution), and what bridge mechanism fits here — APFIT, mid-tier acquisition, or the software acquisition pathway? Draft the opening outreach.",
	"pom":        "What does it take to get this programmed into the POM? Walk the timeline and the specific steps, name the validated-requirement and resource-sponsor dependencies, and recommend the bridge funding to survive until procurement dollars land.",
	"pmadopt":    "Make adoption cheap and career-safe for the program manager. Give me the PM-risk-framed pitch (they own every schedule slip and capture none of the upside) plus the concrete integration-tax cuts to put in writing: MOSA/open interfaces, Government Purpose Rights, and ATO reciprocity.",
	"nextstep":   "Given this pursuit's current stage and its weakest transition wall, what is the single highest-leverage action I should take next? One clear next move, why it matters, and the first concrete step.",
	"outreach":   "Build my outreach plan for THIS opportunity. (1) Name the specific offices/POCs to engage from the named targets and real POCs in context — not a vague 'find a sponsor'. (2) For each, give the SANCTIONED channel (SBIR topic Q&A window, industry day, SAM RFI, BAA white paper, consortium, the program-office mailbox) — never a cold mass email. (3) Draft the actual message for the top 1–2: short, mission-first (their requirement, not my product), referencing the specific topic, asking one real question, signed as me. Make it the opposite of spam.",
}

type assistReq struct {
	OppID   string `json:"opp_id"`
	Action  string `json:"action"`
	Message string `json:"message"`
	History []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"history"`
}

func (s *server) hAssistStatus(w http.ResponseWriter, _ *http.Request) {
	enabled := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) != ""
	writeJSON(w, map[string]any{"enabled": enabled, "model": assistModel()})
}

func (s *server) hAssist(w http.ResponseWriter, r *http.Request) {
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

	key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if key == "" {
		emit(map[string]string{"error": "Set ANTHROPIC_API_KEY in your environment and restart the workspace to enable Claude."})
		return
	}
	var in assistReq
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		emit(map[string]string{"error": "bad request"})
		return
	}

	s.mu.Lock()
	var opp *Opportunity
	for i := range s.opps {
		if s.opps[i].ID == in.OppID {
			opp = &s.opps[i]
			break
		}
	}
	pursuit := s.state[in.OppID]
	s.mu.Unlock()
	if opp == nil {
		emit(map[string]string{"error": "opportunity not found — refresh"})
		return
	}

	detail := ""
	if opp.DetailRef != "" {
		detail = FetchDSIPDetail(opp.DetailRef) // full topic text for real grounding
	}
	userText := strings.TrimSpace(in.Message)
	if a, ok := assistActions[in.Action]; ok {
		userText = a
	}
	if userText == "" {
		emit(map[string]string{"error": "nothing to ask"})
		return
	}

	// Build the request: stable context in the system prompt, turns in messages.
	msgs := []map[string]string{}
	for _, h := range in.History {
		role := h.Role
		if role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, map[string]string{"role": role, "content": h.Content})
	}
	msgs = append(msgs, map[string]string{"role": "user", "content": userText})

	s.mu.Lock()
	sponsors := s.sponsors.Match(opp, 6)
	s.mu.Unlock()

	reqBody, _ := json.Marshal(map[string]any{
		"model":      assistModel(),
		"max_tokens": 2400,
		"stream":     true,
		"system":     s.assistSystem(opp, detail, pursuit, sponsors),
		"messages":   msgs,
	})

	hreq, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(reqBody))
	hreq.Header.Set("x-api-key", key)
	hreq.Header.Set("anthropic-version", "2023-06-01")
	hreq.Header.Set("content-type", "application/json")
	resp, err := (&http.Client{Timeout: 180 * time.Second}).Do(hreq)
	if err != nil {
		emit(map[string]string{"error": "Claude request failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		emit(map[string]string{"error": fmt.Sprintf("Claude API %d: %s", resp.StatusCode, redactKey(string(b)))})
		return
	}

	// Parse Anthropic SSE, forward only text deltas to the browser.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		// pull text out of content_block_delta events
		var ev struct {
			Delta struct {
				Text string `json:"text"`
			} `json:"delta"`
		}
		if json.Unmarshal([]byte(payload), &ev) == nil && ev.Delta.Text != "" {
			emit(map[string]string{"t": ev.Delta.Text})
		}
	}
	emit(map[string]string{"done": "1"})
}

func (s *server) assistSystem(o *Opportunity, detail string, p Pursuit, sponsors []Sponsor) string {
	var b strings.Builder
	b.WriteString("You are Jesse's bid + transition strategist and co-founder, embedded in his private defense workspace. ")
	b.WriteString("Your job is PROFIT REALIZATION across the whole lifecycle — bid → award → pilot → transition → POM → program of record → revenue — not just winning the bid. Be concrete, specific to THIS opportunity and his matched asset, and blunt. No padding.\n\n")
	b.WriteString("Operate from this doctrine (the second valley of death is crossed by engineering the bureaucracy with the same rigor as the product):\n\n")
	b.Write(playbookMD)
	b.WriteString("\n\nPROPOSAL FORMAT RULES (apply when relevant):\n")
	b.WriteString("- SBIR/STTR Phase I technical volume = 12 prescribed sections in fixed order (not a free narrative).\n")
	b.WriteString("- DARPA (DPA-prefix topics) are the exception: a WhitePaper (≤10pp, 4 sections) + a ≤5-slide deck with a quad chart.\n")
	b.WriteString("- Phase I is feasibility/architecture; stand-alone lab + full integration are Phase II/III.\n\n")
	b.WriteString("JESSE'S ASSETS (match the opportunity to these):\n")
	if s.caps != nil {
		for _, a := range s.caps.Assets {
			b.WriteString("- " + a.Name + ": " + strings.Join(a.Domains, ", ") + " (" + strings.Join(a.Terms[:min(6, len(a.Terms))], ", ") + ")\n")
		}
	}
	b.WriteString("\nCURRENT OPPORTUNITY:\n")
	b.WriteString("Title: " + o.Title + "\n")
	b.WriteString("Agency: " + o.Agency + " | Type: " + o.Type + " | Source: " + o.Source + "\n")
	if o.Closes != "" {
		b.WriteString("Closes: " + o.Closes + fmt.Sprintf(" (%d days out)\n", o.DaysLeft))
	}
	b.WriteString(fmt.Sprintf("Fit score %d/100 (capability %d, eligibility %d, runway %d, value %d)", o.Score, o.Capability, o.Eligibility, o.Runway, o.Value))
	if o.MatchedAsset != "" {
		b.WriteString(" — best-matched asset: " + o.MatchedAsset)
	}
	b.WriteString("\n")
	if o.URL != "" {
		b.WriteString("URL: " + o.URL + "\n")
	}
	if detail != "" {
		b.WriteString("\nFULL TOPIC TEXT:\n" + detail + "\n")
	}
	if p.Stage != "" || p.Walls != (Walls{}) || p.Value > 0 {
		b.WriteString("\nPURSUIT STATUS (Jesse's private tracking — tailor guidance to where this is in the lifecycle and engineer the weakest wall next):\n")
		if p.Stage != "" {
			b.WriteString("Lifecycle stage: " + p.Stage + "\n")
		}
		if p.Value > 0 {
			b.WriteString(fmt.Sprintf("Est. lifetime value: $%dK\n", p.Value))
		}
		rd, weakest := p.Walls.Readiness()
		b.WriteString(fmt.Sprintf("Transition readiness %d/100 — Money:%s · Requirements:%s · Contracts:%s · Incentives:%s · weakest wall: %s\n",
			rd, dash(p.Walls.Money), dash(p.Walls.Requirements), dash(p.Walls.Contracts), dash(p.Walls.Incentives), weakest))
	}

	if len(o.Contacts) > 0 {
		b.WriteString("\nREAL POCs (published by the source — use exactly, do not invent others):\n")
		for _, c := range o.Contacts {
			b.WriteString("- " + c.Name)
			if c.Role != "" {
				b.WriteString(" — " + c.Role)
			}
			if c.Email != "" {
				b.WriteString(" <" + c.Email + ">")
			}
			b.WriteString("\n")
		}
	}
	if o.Channel != "" {
		b.WriteString("SANCTIONED CHANNEL: " + o.Channel + "\n")
	}
	if len(sponsors) > 0 {
		b.WriteString("\nNAMED TRANSITION TARGETS (real DoD offices for money/requirements/program/transition — name these specifically, reach via each one's channel):\n")
		for _, s := range sponsors {
			b.WriteString("- " + s.Office + " [" + s.Role + ", " + s.Component + "] — channel: " + s.Channel)
			if s.Notes != "" {
				b.WriteString(" — " + s.Notes)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\nOUTREACH RULES (critical): Never give vague advice like 'find a resource sponsor.' Always name the specific office(s)/POC(s) above and the exact sanctioned channel. NEVER recommend cold or mass email — prefer the official channel (SBIR topic Q&A window, industry day / APBI, SAM RFI, BAA white paper, consortium marketplace, the program-office mailbox, a warm intro). Any drafted message must be short, mission-first (their requirement, not Jesse's product), reference the specific topic, ask one real question, and read as the opposite of spam. If you don't have a named person, name the office + role and the channel to find the current incumbent — never fabricate a name or email.\n")
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "unset"
	}
	return s
}

func redactKey(s string) string {
	if i := strings.Index(s, "sk-ant"); i >= 0 {
		return s[:i] + "[redacted]"
	}
	return s
}
