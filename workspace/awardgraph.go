package workspace

import (
	"net/http"
	"sort"
	"strings"
)

// Phase 5g — the award graph. The competitive-field action lists raw awards; this
// aggregates them into the firm↔topic map that actually informs strategy: who the
// incumbents are (by $ and count), whether the lane is entrenched or open, and
// which firms have carried Phase II+ (transition-capable primes you could team
// with or around). Built on the same cached SBIR.gov award pull.

type firmStat struct {
	Firm       string `json:"firm"`
	Count      int    `json:"count"`
	Total      int    `json:"total"`       // total $ won
	Phase2Plus int    `json:"phase2_plus"` // Phase II/III awards (transition-capable)
	Recent     int    `json:"recent"`      // most recent award year
}

func isPhase2Plus(phase string) bool {
	return strings.Contains(strings.ToUpper(phase), "II") // matches "Phase II" and "Phase III"
}

// buildAwardGraph aggregates awards into ranked firms + a lane verdict + teaming
// targets. Pure, deterministic — unit-testable.
func buildAwardGraph(aws []Award) map[string]any {
	m := map[string]*firmStat{}
	total := 0
	for _, a := range aws {
		f := m[a.Firm]
		if f == nil {
			f = &firmStat{Firm: a.Firm}
			m[a.Firm] = f
		}
		f.Count++
		f.Total += a.Amount
		total += a.Amount
		if isPhase2Plus(a.Phase) {
			f.Phase2Plus++
		}
		if a.Year > f.Recent {
			f.Recent = a.Year
		}
	}
	firms := make([]firmStat, 0, len(m))
	for _, f := range m {
		firms = append(firms, *f)
	}
	sort.SliceStable(firms, func(i, j int) bool {
		if firms[i].Total != firms[j].Total {
			return firms[i].Total > firms[j].Total
		}
		return firms[i].Count > firms[j].Count
	})

	n := len(aws)
	distinct := len(firms)
	topShare := 0
	if total > 0 && len(firms) > 0 {
		topShare = firms[0].Total * 100 / total
	}
	verdict, lane := awardLaneVerdict(n, distinct, topShare)

	// Teaming targets: transition-capable firms (Phase II+), strongest first.
	var teaming []firmStat
	for _, f := range firms {
		if f.Phase2Plus > 0 {
			teaming = append(teaming, f)
		}
		if len(teaming) >= 5 {
			break
		}
	}
	if len(firms) > 12 {
		firms = firms[:12]
	}
	return map[string]any{
		"firms": firms, "distinct": distinct, "awards": n, "total": total,
		"top_share": topShare, "lane": lane, "verdict": verdict, "teaming": teaming,
	}
}

func awardLaneVerdict(n, distinct, topShare int) (string, string) {
	switch {
	case n < 4:
		return "Open lane — little prior SBIR activity here. First-mover room, but validate the demand.", "open"
	case topShare >= 50 || distinct <= 3:
		return "Entrenched — a few incumbents dominate the dollars. Win by sharp differentiation, or team with/around them.", "entrenched"
	case distinct >= 8:
		return "Active & fragmented — many players, no dominant incumbent. Winnable; lead hard with your discriminators.", "open"
	default:
		return "Moderately contested — a handful of repeat winners. Ghost the incumbents and bring a clear edge.", "contested"
	}
}

// hAwardGraph returns the aggregated competitive map for an opportunity.
func (s *server) hAwardGraph(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s.mu.Lock()
	subj := s.subjectFor(id)
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	kw := awardKeyword(subj)
	aws, ok := FetchAwards(s.opts.Dir, kw)
	g := buildAwardGraph(aws)
	g["keyword"] = kw
	g["ok"] = ok
	writeJSON(w, g)
}
