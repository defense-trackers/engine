package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DSIP's topics-app SPA calls this public GET endpoint with the search criteria
// URL-encoded into `searchParam`. It blocks datacenter IPs, so this only works
// run locally (Jesse's residential IP) — which is exactly why DSIP lives in the
// private workspace and not the autonomous public pipeline.
const dsipSearchURL = "https://www.dodsbirsttr.mil/topics/api/public/topics/search"

// openTopicsParam mirrors what the SPA sends for "open + pre-release" topics.
const openTopicsParam = `{"searchText":null,"components":null,"programYear":null,"solicitationCycleNames":["openTopics"],"releaseNumbers":[],"topicReleaseStatus":[591,592],"modernizationPriorities":null,"sortBy":"finalTopicCode,asc"}`

type dsipResp struct {
	Total int `json:"total"`
	Data  []struct {
		TopicCode         string `json:"topicCode"`
		TopicID           string `json:"topicId"`
		TopicTitle        string `json:"topicTitle"`
		Program           string `json:"program"`
		Component         string `json:"component"`
		Command           string `json:"command"`
		TopicStatus       string `json:"topicStatus"`
		SolicitationTitle string `json:"solicitationTitle"`
		SolicitationNum   string `json:"solicitationNumber"`
		PhaseHierarchy    string `json:"phaseHierarchy"`
		TopicStartDate    int64  `json:"topicStartDate"`
		TopicEndDate      int64  `json:"topicEndDate"`
	} `json:"data"`
}

// FetchDSIP pulls all open/pre-release DoD SBIR/STTR topics and maps them to
// Opportunities. Paginates until the reported total is covered.
func FetchDSIP() ([]Opportunity, error) {
	const page = 50
	var out []Opportunity
	for start := 0; ; start += page {
		u := dsipSearchURL + "?searchParam=" + url.QueryEscape(openTopicsParam) +
			"&size=" + strconv.Itoa(page) + "&page=" + strconv.Itoa(start/page)
		req, _ := http.NewRequest("GET", u, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (defense-trackers-workspace)")
		req.Header.Set("Accept", "application/json")
		resp, err := (&http.Client{Timeout: 45 * time.Second}).Do(req)
		if err != nil {
			return out, fmt.Errorf("DSIP fetch: %w", err)
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return out, fmt.Errorf("DSIP fetch: HTTP %d", resp.StatusCode)
		}
		var dr dsipResp
		if err := json.Unmarshal(body, &dr); err != nil {
			return out, fmt.Errorf("DSIP parse (datacenter IP block?): %w", err)
		}
		for _, t := range dr.Data {
			o := Opportunity{
				ID:     "dsip:" + t.TopicCode,
				Title:  t.TopicTitle,
				Agency: strings.TrimSpace(t.Component + " — " + t.Command),
				Type:   t.Program, // SBIR | STTR
				Source: "dsip",
				Status: t.TopicStatus, // Open | Pre-Release
				Posted: epochMS(t.TopicStartDate),
				Closes: epochMS(t.TopicEndDate),
				// SBIR/STTR are small-business programs by statute.
				Setaside:  "SBIR/STTR (small business)",
				AwardText: t.SolicitationNum + " " + phaseSummary(t.PhaseHierarchy),
				URL:       "https://www.dodsbirsttr.mil/topics-app/#/topics/" + t.TopicID,
			}
			o.Text = strings.ToLower(strings.Join([]string{
				o.Title, o.Agency, o.Type, t.SolicitationTitle, o.Status, o.Setaside,
			}, " "))
			out = append(out, o)
		}
		if start+page >= dr.Total || len(dr.Data) == 0 {
			break
		}
	}
	return out, nil
}

func epochMS(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02")
}

// phaseSummary turns the phaseHierarchy JSON blob into a short label like "Phase I/II".
func phaseSummary(raw string) string {
	if raw == "" {
		return ""
	}
	var h struct {
		Config []struct {
			DisplayValue string `json:"displayValue"`
		} `json:"config"`
	}
	if json.Unmarshal([]byte(raw), &h) != nil || len(h.Config) == 0 {
		return ""
	}
	var p []string
	for _, c := range h.Config {
		p = append(p, c.DisplayValue)
	}
	return "Phase " + strings.Join(p, "/")
}
