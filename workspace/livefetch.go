package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Live data for the chat. By default the assistant only sees the snapshot ingested
// at startup; this lets it pull FRESH data mid-conversation. The assistant emits a
// directive like [[fetch:sam:counter-UAS]] and the server runs the real query
// against SAM.gov / grants.gov / SBIR.gov awards / the in-memory scored set, then
// feeds the results back so the next turn answers on current data.

var fetchDirRe = regexp.MustCompile(`\[\[fetch:([^\]]+)\]\]`)

// parseFetchDirectives pulls the source:query requests out of a model reply.
func parseFetchDirectives(text string) []string {
	var out []string
	for _, m := range fetchDirRe.FindAllStringSubmatch(text, -1) {
		if d := strings.TrimSpace(m[1]); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// samSearch runs a single live SAM.gov query (DoD-filtered, noise-stripped).
func samSearch(dir, query string, limit int) ([]Opportunity, error) {
	key := samKey(dir)
	if key == "" {
		return nil, fmt.Errorf("no SAM API key configured")
	}
	now := time.Now().UTC()
	v := url.Values{}
	v.Set("postedFrom", now.AddDate(0, 0, -360).Format("01/02/2006")) // SAM rejects ranges ≥ 1 year
	v.Set("postedTo", now.Format("01/02/2006"))
	v.Set("title", query)
	v.Set("limit", strconv.Itoa(limit*3)) // over-fetch; DoD filter trims
	req, _ := http.NewRequest("GET", samSearchURL+"?"+v.Encode(), nil)
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Accept", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("SAM HTTP %d", resp.StatusCode)
	}
	var sr samResp
	if json.Unmarshal(body, &sr) != nil {
		return nil, fmt.Errorf("SAM parse error")
	}
	var out []Opportunity
	for _, d := range sr.OpportunitiesData {
		if !strings.Contains(strings.ToUpper(d.FullParentPath), "DEPT OF DEFENSE") || samNoise(d.Title) {
			continue
		}
		out = append(out, Opportunity{
			Title: d.Title, Agency: samAgency(d.FullParentPath), Closes: normDate(d.ResponseDeadline),
			AwardText: d.SolicitationNumber, URL: d.UILink, Source: "sam",
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// liveData runs each fetch directive and returns a grounded text block for the
// next prompt. Sources: sam | grants | awards | opps.
func (s *server) liveData(dirs []string) string {
	var b strings.Builder
	for _, d := range dirs {
		parts := strings.SplitN(d, ":", 2)
		src := strings.ToLower(strings.TrimSpace(parts[0]))
		q := ""
		if len(parts) > 1 {
			q = strings.TrimSpace(parts[1])
		}
		switch src {
		case "sam":
			b.WriteString("LIVE SAM.gov (DoD) for \"" + q + "\":\n")
			res, err := samSearch(s.opts.Dir, q, 8)
			if err != nil {
				b.WriteString("  (fetch failed: " + err.Error() + ")\n")
			} else if len(res) == 0 {
				b.WriteString("  (no current DoD results)\n")
			} else {
				for _, o := range res {
					b.WriteString("  - " + o.Title + " | " + o.Agency + " | closes " + dash(o.Closes) + " | " + o.AwardText + " | " + o.URL + "\n")
				}
			}
		case "grants":
			b.WriteString("LIVE grants.gov (DoD) for \"" + q + "\":\n")
			res, err := grantsSearch(q, 8)
			if err != nil {
				b.WriteString("  (fetch failed: " + err.Error() + ")\n")
			} else if len(res) == 0 {
				b.WriteString("  (no current DoD results)\n")
			} else {
				for _, o := range res {
					b.WriteString("  - " + o.Title + " | " + o.Agency + " | closes " + dash(o.Closes) + " | " + o.AwardText + " | " + o.URL + "\n")
				}
			}
		case "awards":
			b.WriteString("LIVE SBIR.gov awards (competitive field) for \"" + q + "\":\n")
			if aw, ok := FetchAwards(s.opts.Dir, q); ok && len(aw) > 0 {
				b.WriteString(awardsSummary(aw))
			} else {
				b.WriteString("  (no awards found or feed throttled)\n")
			}
		case "opps":
			b.WriteString("MATCHING SCORED OPPORTUNITIES (already in the radar) for \"" + q + "\":\n")
			b.WriteString(s.searchOpps(q, 10))
		default:
			b.WriteString("(unknown live source: " + src + ")\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// searchOpps returns up to n already-scored opportunities matching the query terms.
func (s *server) searchOpps(query string, n int) string {
	q := strings.Fields(strings.ToLower(query))
	s.mu.Lock()
	defer s.mu.Unlock()
	var b strings.Builder
	count := 0
	for i := range s.opps {
		o := &s.opps[i]
		hay := strings.ToLower(o.Title + " " + o.Agency + " " + o.MatchedAsset)
		ok := len(q) == 0
		for _, t := range q {
			if strings.Contains(hay, t) {
				ok = true
				break
			}
		}
		if !ok {
			continue
		}
		b.WriteString(fmt.Sprintf("  - [%s] %s | %s | fit %d/100 | closes %s | %s\n", strings.ToUpper(o.Source), o.Title, o.Agency, o.Score, dash(o.Closes), o.URL))
		count++
		if count >= n {
			break
		}
	}
	if count == 0 {
		b.WriteString("  (nothing in the current radar matches)\n")
	}
	return b.String()
}

// grantsSearch runs a single live grants.gov query (DoD-filtered).
func grantsSearch(query string, limit int) ([]Opportunity, error) {
	body, _ := json.Marshal(map[string]any{"keyword": query, "oppStatuses": "posted", "rows": limit * 4})
	req, _ := http.NewRequest("POST", grantsSearchURL, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("grants HTTP %d", resp.StatusCode)
	}
	var gr grantsResp
	if json.Unmarshal(raw, &gr) != nil {
		return nil, fmt.Errorf("grants parse error")
	}
	var out []Opportunity
	for _, h := range gr.Data.OppHits {
		if !grantsIsDoD(h.AgencyCode) {
			continue
		}
		out = append(out, Opportunity{
			Title: h.Title, Agency: h.Agency, Closes: normDate(h.CloseDate),
			AwardText: h.Number, URL: "https://www.grants.gov/search-results-detail/" + h.ID, Source: "grants",
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}
