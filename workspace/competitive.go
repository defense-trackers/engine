package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Competitive intel: who has actually WON SBIR/STTR awards in a topic's space. Pulled
// from the SBIR.gov public award API. The API is aggressively rate-limited, so this is
// on-demand (never bulk) and hard-cached (7 days) per keyword, degrading to cache/empty
// on throttle. It tells Jesse the competitive field — incumbents, $ sizes, recency —
// not just the requirement.

const sbirAwardsURL = "https://api.www.sbir.gov/public/api/awards"

type Award struct {
	Firm   string `json:"firm"`
	Title  string `json:"title"`
	Branch string `json:"branch"`
	Phase  string `json:"phase"`
	Year   int    `json:"year"`
	Amount int    `json:"amount"`
}

type awardCache struct {
	Fetched string  `json:"fetched"`
	Awards  []Award `json:"awards"`
}

// raw SBIR.gov award shape (subset).
type sbirAward struct {
	Firm        string `json:"firm"`
	AwardTitle  string `json:"award_title"`
	Branch      string `json:"branch"`
	Phase       string `json:"phase"`
	AwardYear   any    `json:"award_year"`
	AwardAmount any    `json:"award_amount"`
}

func awardsCachePath(dir string) string { return filepath.Join(dir, "awards-cache.json") }

func loadAwardCache(dir string) map[string]awardCache {
	m := map[string]awardCache{}
	if b, err := os.ReadFile(awardsCachePath(dir)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func saveAwardCache(dir string, m map[string]awardCache) {
	b, _ := json.MarshalIndent(m, "", " ")
	_ = os.WriteFile(awardsCachePath(dir), b, 0o644)
}

// FetchAwards returns recent DoD SBIR/STTR awards matching a keyword, cached for 7
// days. On throttle/error it returns the cached set (possibly empty) and ok=false.
func FetchAwards(dir, keyword string) ([]Award, bool) {
	keyword = strings.TrimSpace(strings.ToLower(keyword))
	if keyword == "" {
		return nil, false
	}
	cache := loadAwardCache(dir)
	if c, ok := cache[keyword]; ok {
		if t := parseDate(c.Fetched); !t.IsZero() && time.Since(t) < 7*24*time.Hour {
			return c.Awards, true
		}
	}
	u := sbirAwardsURL + "?agency=DOD&rows=20&keyword=" + url.QueryEscape(keyword)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (defense-trackers-workspace)")
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 25 * time.Second}).Do(req)
	if err != nil {
		return cache[keyword].Awards, false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	// throttle / error responses are a JSON object with a "Code" field, not an array.
	if len(body) == 0 || body[0] != '[' {
		return cache[keyword].Awards, false // keep last-good cache
	}
	var raw []sbirAward
	if json.Unmarshal(body, &raw) != nil {
		return cache[keyword].Awards, false
	}
	var out []Award
	for _, r := range raw {
		out = append(out, Award{
			Firm: r.Firm, Title: r.AwardTitle, Branch: r.Branch, Phase: r.Phase,
			Year: toInt(r.AwardYear), Amount: toInt(r.AwardAmount),
		})
	}
	// most recent / largest first
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Year != out[j].Year {
			return out[i].Year > out[j].Year
		}
		return out[i].Amount > out[j].Amount
	})
	if len(out) > 12 {
		out = out[:12]
	}
	cache[keyword] = awardCache{Fetched: time.Now().UTC().Format("2006-01-02"), Awards: out}
	saveAwardCache(dir, cache)
	return out, true
}

func toInt(v any) int {
	switch x := v.(type) {
	case float64:
		return int(x)
	case string:
		x = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(x, "$", ""), ",", ""))
		if i, err := strconv.Atoi(x); err == nil {
			return i
		}
		if f, err := strconv.ParseFloat(x, 64); err == nil {
			return int(f)
		}
	}
	return 0
}

var awardStop = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "system": true,
	"systems": true, "using": true, "based": true, "direct": true, "phase": true, "ii": true,
	"advanced": true, "novel": true, "innovative": true, "development": true, "technology": true,
	"technologies": true, "approach": true, "solution": true, "solutions": true, "new": true,
	"high": true, "low": true, "cost": true, "improved": true, "enhanced": true, "of": true,
	"a": true, "an": true, "to": true, "in": true, "on": true, "scalable": true, "next": true,
	"generation": true,
}

// awardKeyword derives a search keyword for an opportunity: prefer the matched asset's
// domain lane; else the two most distinctive words from the title.
func awardKeyword(o *Opportunity) string {
	switch o.MatchedAsset {
	case "thermalhawk":
		return "drone detection"
	case "rigrun":
		return "large language model"
	case "auspex":
		return "cybersecurity"
	case "signet":
		return "zero trust"
	}
	words := strings.FieldsFunc(strings.ToLower(o.Title), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	var pick []string
	for _, w := range words {
		if len(w) >= 5 && !awardStop[w] {
			pick = append(pick, w)
			if len(pick) == 2 {
				break
			}
		}
	}
	return strings.Join(pick, " ")
}

// awardsSummary renders a one-block competitive readout for prompts/UI.
func awardsSummary(aw []Award) string {
	if len(aw) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d recent DoD SBIR/STTR awards in this space:\n", len(aw)))
	for i, a := range aw {
		if i >= 8 {
			break
		}
		b.WriteString("- " + a.Firm)
		if a.Branch != "" {
			b.WriteString(" (" + a.Branch + ")")
		}
		if a.Phase != "" {
			b.WriteString(" · " + a.Phase)
		}
		if a.Year > 0 {
			b.WriteString(fmt.Sprintf(" · %d", a.Year))
		}
		if a.Amount > 0 {
			b.WriteString(fmt.Sprintf(" · $%s", humanK(a.Amount)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// hDetail returns the full cached topic readout for an opportunity.
func (s *server) hDetail(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	subj := s.subjectFor(r.URL.Query().Get("id"))
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	txt := ""
	if subj.DetailRef != "" {
		txt = detailCached(s.opts.Dir, subj.DetailRef)
	}
	writeJSON(w, map[string]any{
		"title": subj.Title, "agency": subj.Agency, "type": subj.Type,
		"setaside": subj.Setaside, "closes": subj.Closes, "url": subj.URL,
		"detail": txt, "award_text": subj.AwardText,
	})
}

// hAwards returns the competitive field for an opportunity (cached, on-demand).
func (s *server) hAwards(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s.mu.Lock()
	subj := s.subjectFor(id)
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	kw := awardKeyword(subj)
	aw, ok := FetchAwards(s.opts.Dir, kw)
	writeJSON(w, map[string]any{"keyword": kw, "ok": ok, "awards": aw, "summary": awardsSummary(aw)})
}

func humanK(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	if n >= 1000 {
		return fmt.Sprintf("%dK", n/1000)
	}
	return strconv.Itoa(n)
}
