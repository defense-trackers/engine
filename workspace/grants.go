package workspace

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Wider radar, part 2: grants.gov. DoD research organizations (ARO/ARL, ONR,
// AFOSR, DARPA, DTRA) post BAAs and research funding opportunities here that never
// touch DSIP or SAM. The public Search2 JSON API needs no key, so this widens the
// net for free. Results are filtered to DoD agencies and Jesse's lanes, deduped,
// and cached 7 days (the feed moves slowly and we don't want to hammer it).

const grantsSearchURL = "https://api.grants.gov/v1/api/search2"

// grantsQueries mirror Jesse's lanes; grants.gov keyword search is broad so the
// DoD-agency filter does the real narrowing.
var grantsQueries = []string{
	"autonomy", "autonomous systems", "artificial intelligence",
	"machine learning", "unmanned", "maritime autonomy",
	"intelligence surveillance reconnaissance", "cyber", "counter-UAS",
}

// grantsDoDAgencyPrefixes are the agencyCode prefixes we keep (defense only).
var grantsDoDAgencyPrefixes = []string{"DOD", "DARPA", "ONR", "USAF", "ARMY", "NAVY", "DTRA", "DLA", "MDA", "DHA"}

type grantsResp struct {
	Data struct {
		OppHits []struct {
			ID         string `json:"id"`
			Number     string `json:"number"`
			Title      string `json:"title"`
			Agency     string `json:"agency"`
			AgencyCode string `json:"agencyCode"`
			OpenDate   string `json:"openDate"`
			CloseDate  string `json:"closeDate"`
			OppStatus  string `json:"oppStatus"`
		} `json:"oppHits"`
	} `json:"data"`
}

// FetchGrants queries grants.gov across Jesse's lanes and returns DoD funding
// opportunities, deduped by id. Cached 7 days under <dir>/grants-cache.json.
func FetchGrants(dir string) ([]Opportunity, error) {
	cache := filepath.Join(dir, "grants-cache.json")
	if b, err := os.ReadFile(cache); err == nil {
		var c struct {
			Fetched string        `json:"fetched"`
			Opps    []Opportunity `json:"opps"`
		}
		if json.Unmarshal(b, &c) == nil {
			if t := parseDate(c.Fetched); !t.IsZero() && time.Since(t) < 7*24*time.Hour {
				for i := range c.Opps {
					c.Opps[i].Text = c.Opps[i].searchText()
				}
				return c.Opps, nil
			}
		}
	}

	seen := map[string]bool{}
	var out []Opportunity
	client := &http.Client{Timeout: 30 * time.Second}
	for _, q := range grantsQueries {
		body, _ := json.Marshal(map[string]any{"keyword": q, "oppStatuses": "posted", "rows": 50})
		req, _ := http.NewRequest("POST", grantsSearchURL, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			continue // one bad query shouldn't sink the sweep
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var gr grantsResp
		if json.Unmarshal(raw, &gr) != nil {
			continue
		}
		for _, h := range gr.Data.OppHits {
			if !grantsIsDoD(h.AgencyCode) {
				continue
			}
			id := "grants:" + h.ID
			if seen[id] {
				continue
			}
			seen[id] = true
			o := Opportunity{
				ID:        id,
				Title:     h.Title,
				Agency:    h.Agency,
				Type:      "Grant/BAA",
				Source:    "grants",
				Status:    h.OppStatus,
				Posted:    normDate(h.OpenDate),
				Closes:    normDate(h.CloseDate),
				AwardText: h.Number,
				URL:       "https://www.grants.gov/search-results-detail/" + h.ID,
			}
			o.Text = o.searchText() + " " + strings.ToLower(q)
			out = append(out, o)
		}
	}

	if len(out) > 0 {
		c := struct {
			Fetched string        `json:"fetched"`
			Opps    []Opportunity `json:"opps"`
		}{Fetched: time.Now().UTC().Format("2006-01-02"), Opps: out}
		if b, e := json.MarshalIndent(c, "", " "); e == nil {
			_ = os.WriteFile(cache, b, 0o644)
		}
	}
	return out, nil
}

func grantsIsDoD(code string) bool {
	u := strings.ToUpper(strings.TrimSpace(code))
	for _, p := range grantsDoDAgencyPrefixes {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}
