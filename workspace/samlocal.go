package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Wider radar: a workspace-local SAM.gov fetch, tuned to Jesse's lanes (autonomous
// vehicles incl. USV, counter-UAS, AI/autonomy, ISR, C2, cyber). This runs locally
// and privately, separate from the public trackers' SAM feed, so he can lean in hard
// on the contract/OTA/BAA side — including DIU CSOs and IARPA BAAs, which post on
// SAM. AFWERX/SpaceWERX open topics already arrive via the DSIP feed (DAF component).
//
// It activates only when SAM_API_KEY is set (the key never lands in a URL — it goes
// in the X-Api-Key header). Without a key the realizer still runs on DSIP + the
// public trackers.

const samSearchURL = "https://api.sam.gov/prod/opportunities/v2/search"

// samDefaultQueries lean into autonomous vehicles / USV first, then the rest of his
// lanes. Override with SAM_QUERIES (comma-separated).
var samDefaultQueries = []string{
	"unmanned surface vessel", "USV", "autonomous surface", "maritime autonomous",
	"unmanned underwater vehicle", "autonomous vehicle", "counter-UAS",
	"autonomy", "artificial intelligence", "machine learning",
	"intelligence surveillance reconnaissance", "command and control",
}

func samQueries() []string {
	if q := strings.TrimSpace(os.Getenv("SAM_QUERIES")); q != "" {
		var out []string
		for _, t := range strings.Split(q, ",") {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return samDefaultQueries
}

type samResp struct {
	OpportunitiesData []struct {
		Title              string `json:"title"`
		SolicitationNumber string `json:"solicitationNumber"`
		FullParentPath     string `json:"fullParentPathName"`
		PostedDate         string `json:"postedDate"`
		ResponseDeadline   string `json:"responseDeadLine"`
		Type               string `json:"type"`
		SetAside           string `json:"typeOfSetAsideDescription"`
		UILink             string `json:"uiLink"`
	} `json:"opportunitiesData"`
}

// FetchSAM queries SAM.gov across Jesse's lane queries and returns DoD opportunities,
// deduped by solicitation number. Empty (no error) when SAM_API_KEY is unset.
func FetchSAM() ([]Opportunity, error) {
	key := strings.TrimSpace(os.Getenv("SAM_API_KEY"))
	if key == "" {
		return nil, nil
	}
	now := time.Now().UTC()
	from := now.AddDate(0, 0, -180).Format("01/02/2006")
	to := now.Format("01/02/2006")

	seen := map[string]bool{}
	var out []Opportunity
	client := &http.Client{Timeout: 30 * time.Second}
	for _, q := range samQueries() {
		v := url.Values{}
		v.Set("postedFrom", from)
		v.Set("postedTo", to)
		v.Set("title", q)
		v.Set("limit", "50")
		req, _ := http.NewRequest("GET", samSearchURL+"?"+v.Encode(), nil)
		req.Header.Set("X-Api-Key", key) // header, never in the URL
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			continue // one bad query shouldn't sink the sweep
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var sr samResp
		if json.Unmarshal(body, &sr) != nil {
			continue
		}
		for _, d := range sr.OpportunitiesData {
			// DoD only.
			if !strings.Contains(strings.ToUpper(d.FullParentPath), "DEPT OF DEFENSE") {
				continue
			}
			id := "sam:" + d.SolicitationNumber
			if d.SolicitationNumber == "" {
				id = "sam:" + d.UILink
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			o := Opportunity{
				ID:        id,
				Title:     d.Title,
				Agency:    samAgency(d.FullParentPath),
				Type:      first(map[string]string{"t": d.Type}, "t"),
				Source:    "sam",
				Posted:    normDate(d.PostedDate),
				Closes:    normDate(d.ResponseDeadline),
				Setaside:  d.SetAside,
				URL:       d.UILink,
				AwardText: d.SolicitationNumber,
			}
			o.Text = strings.ToLower(strings.Join([]string{
				o.Title, o.Agency, o.Type, o.Setaside, q,
			}, " "))
			out = append(out, o)
		}
	}
	return out, nil
}

// samAgency shortens SAM's full org path to the trailing one or two segments.
func samAgency(path string) string {
	parts := strings.Split(path, ".")
	clean := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	if len(clean) <= 2 {
		return strings.Join(clean, " — ")
	}
	return clean[len(clean)-2] + " — " + clean[len(clean)-1]
}

// samNote is printed at startup so Jesse knows whether the wider SAM radar is live.
func samNote() string {
	if strings.TrimSpace(os.Getenv("SAM_API_KEY")) == "" {
		return "wider SAM radar OFF — set SAM_API_KEY to add USV/autonomous-vehicle/DIU/IARPA contract opps (DSIP already covers AFWERX/SpaceWERX SBIR)"
	}
	return fmt.Sprintf("wider SAM radar ON — %d lane queries", len(samQueries()))
}
