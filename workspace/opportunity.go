// Package workspace is Jesse's private, local-only bid cockpit. It consumes the
// public trackers' JSON (the JSON is the API) plus live DSIP SBIR/STTR topics,
// fit-scores every opportunity against his capabilities, and tracks pursuit state
// on disk. Nothing here is published — it runs at localhost and writes a local
// state file. Keep it stdlib-only, like the rest of the engine.
package workspace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Opportunity is the normalized shape every source maps into; the scorer and UI
// only ever see this.
type Opportunity struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Agency    string `json:"agency"`
	Type      string `json:"type"`   // SBIR | STTR | CSO/OT | BAA | Grant | Contract
	Source    string `json:"source"` // dsip | pipeline | programs
	Status    string `json:"status,omitempty"`
	Posted    string `json:"posted,omitempty"`
	Closes    string `json:"closes,omitempty"` // YYYY-MM-DD ("" = none/rolling)
	AwardText string `json:"award_text,omitempty"`
	Setaside  string `json:"setaside,omitempty"`
	URL       string    `json:"url,omitempty"`
	DetailRef string    `json:"detail_ref,omitempty"` // DSIP topicId, for lazy full-text fetch
	Contacts  []Contact `json:"contacts,omitempty"`   // real government POCs from the source
	Channel   string    `json:"channel,omitempty"`    // the sanctioned engagement channel (e.g. SBIR Q&A window)
	Text      string    `json:"-"`                    // searchable blob; not serialized

	// scoring (filled by Score)
	Score        int    `json:"score"`
	Capability   int    `json:"capability"`
	Eligibility  int    `json:"eligibility"`
	Runway       int    `json:"runway"`
	Value        int    `json:"value"`
	MatchedAsset string `json:"matched_asset,omitempty"`
	DaysLeft     int    `json:"days_left"` // -1 when no/unparseable close date
	ActNow       bool   `json:"act_now"`
}

// Contact is a real, source-provided government point of contact (e.g. a DSIP
// topic's TPOC). Never fabricated — only what the source publishes.
type Contact struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	Role  string `json:"role,omitempty"`
}

// readJSON fetches bytes from an http(s) URL or a local file path.
func readJSON(base, rel string) ([]byte, error) {
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		url := strings.TrimRight(base, "/") + rel
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("User-Agent", "defense-trackers-workspace")
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
		}
		return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	}
	return os.ReadFile(filepath.Join(base, filepath.FromSlash(rel)))
}

type trackerState struct {
	Records []struct {
		Key    string            `json:"key"`
		Source string            `json:"source"`
		Fields map[string]string `json:"fields"`
	} `json:"records"`
}

// LoadTrackerJSON reads the public pipeline + programs trackers (from a live URL
// or a local site dir) and maps their records into Opportunities.
func LoadTrackerJSON(base string) ([]Opportunity, error) {
	var out []Opportunity
	for _, t := range []string{"pipeline", "programs"} {
		b, err := readJSON(base, "/data/"+t+"/current.json")
		if err != nil {
			continue // a missing tracker shouldn't sink the whole load
		}
		var st trackerState
		if json.Unmarshal(b, &st) != nil {
			continue
		}
		for _, r := range st.Records {
			f := r.Fields
			o := Opportunity{
				ID:        t + ":" + r.Key,
				Title:     first(f, "title", "name", "text"),
				Agency:    f["agency"],
				Type:      f["type"],
				Source:    t,
				Status:    f["status"],
				Posted:    f["posted"],
				Closes:    normDate(f["closes"]),
				AwardText: first(f, "amount", "award", "eligibility"),
				Setaside:  f["setaside"],
				URL:       f["url"],
			}
			o.Text = strings.ToLower(strings.Join([]string{
				o.Title, o.Agency, o.Type, o.Status, f["notes"], f["domain"],
				f["description"], o.Setaside, o.AwardText, f["eligibility"],
			}, " "))
			out = append(out, o)
		}
	}
	return out, nil
}

func first(f map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(f[k]); v != "" {
			return v
		}
	}
	return ""
}

// normDate coerces the date formats sources use into YYYY-MM-DD ("" if none).
func normDate(s string) string {
	d := parseDate(s)
	if d.IsZero() {
		return ""
	}
	return d.Format("2006-01-02")
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, l := range []string{"2006-01-02", time.RFC3339, "01/02/2006", "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return t
		}
	}
	return time.Time{}
}
