// Package api is a generic JSON-over-HTTP fetcher. Everything source-specific
// is declared in the contract (array_path, key_field, field_map, query, auth,
// pagination), so one fetcher serves GitHub, GitLab, Hugging Face, Federal
// Register, USAspending, SBIR.gov, SAM.gov, and raw JSON files. Adding such a
// source is a contract, not code.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"engine/internal/core"
)

type API struct{}

func New() *API { return &API{} }

func (a *API) Fetch(c core.Contract, cacheDir string) ([]core.Record, error) {
	headers := authHeaders(c)
	method := strings.ToUpper(c.HTTPMethod)
	if method == "" {
		method = "GET"
	}
	var body []byte
	if c.Body != "" {
		body = []byte(expandDates(c.Body))
	}
	maxPages := c.MaxPages
	if maxPages < 1 {
		maxPages = 1
	}

	var items []interface{}
	nextURL := buildURL(c, 1)
	for page := 1; page <= maxPages; page++ {
		raw, link, err := core.FetchRaw(method, nextURL, headers, body)
		if err != nil {
			return nil, err
		}
		batch, err := extractArray(raw, c.ArrayPath)
		if err != nil {
			return nil, err
		}
		items = append(items, batch...)
		if c.Paginate == "" || len(batch) == 0 {
			break
		}
		switch c.Paginate {
		case "page":
			nextURL = buildURL(c, page+1)
		case "link":
			nextURL = nextLink(link)
			if nextURL == "" {
				page = maxPages // no more pages
			}
		default:
			page = maxPages
		}
	}
	recs := mapItems(items, c)
	if c.LimitRecords > 0 && len(recs) > c.LimitRecords {
		recs = recs[:c.LimitRecords]
	}
	return recs, nil
}

// Parse maps a single JSON response to records — the pure, testable path.
func Parse(raw []byte, c core.Contract) ([]core.Record, error) {
	items, err := extractArray(raw, c.ArrayPath)
	if err != nil {
		return nil, err
	}
	return mapItems(items, c), nil
}

func mapItems(items []interface{}, c core.Contract) []core.Record {
	var records []core.Record
	seen := map[string]bool{}
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		key := stringify(get(m, c.KeyField))
		if key == "" {
			key = hashItem(m)
		}
		if seen[key] {
			continue
		}
		if excludedScoped(m, c.Exclude, c.ExcludeFields) {
			continue
		}
		if len(c.Include) > 0 && !includesScoped(m, c.Include, c.IncludeFields) {
			continue
		}
		if c.RecentField != "" && c.RecentDays > 0 {
			if d := parseItemDate(stringify(get(m, c.RecentField))); !d.IsZero() &&
				time.Since(d) > time.Duration(c.RecentDays)*24*time.Hour {
				continue // older than the freshness window (e.g. an abandoned repo)
			}
		}
		seen[key] = true

		fields := map[string]string{}
		if len(c.FieldMap) == 0 {
			b, _ := json.Marshal(m)
			fields["text"] = truncate(string(b), 400)
		} else {
			for out, path := range c.FieldMap {
				if v := stringify(get(m, path)); v != "" {
					fields[out] = v
				}
			}
			if fields["text"] == "" && fields["title"] != "" {
				fields["text"] = fields["title"] // nicer changelog summaries
			}
		}
		if fields["url"] == "" && c.URLTemplate != "" {
			fields["url"] = applyURLTemplate(c.URLTemplate, m)
		}
		records = append(records, core.Record{Key: key, Fields: fields})
	}
	return records
}

func buildURL(c core.Contract, page int) string {
	u, err := url.Parse(c.URL)
	if err != nil {
		return c.URL
	}
	q := u.Query()
	for k, v := range c.Query {
		q.Set(k, expandDates(v))
	}
	if c.Paginate == "page" {
		q.Set("page", fmt.Sprint(page))
		per := c.PerPage
		if per == 0 {
			per = 100
		}
		q.Set("per_page", fmt.Sprint(per))
	}
	// NOTE: there is deliberately no query-string auth path. Secrets are injected
	// only as request headers (see authHeaders) so an API key can never land in a
	// URL — and therefore never in an error message, a log line, or status.json.
	u.RawQuery = q.Encode()
	return u.String()
}

// authHeaders sets a Bearer header when auth_mode=header and the token env is
// present. A missing token is non-fatal — the request proceeds unauthenticated
// (lower rate limit) rather than failing, so local runs work without secrets.
func authHeaders(c core.Contract) map[string]string {
	h := map[string]string{}
	switch {
	case c.AuthMode == "header":
		if v := os.Getenv(c.AuthEnv); v != "" {
			h["Authorization"] = "Bearer " + v
		}
	case strings.HasPrefix(c.AuthMode, "header:"):
		// header:X-Api-Key injects the raw token under a custom header (no Bearer
		// prefix). Keeping the key in a header instead of the query string means
		// it never appears in a URL, so it can't leak into error messages or the
		// public status.json. Used for SAM.gov / api.data.gov.
		if name := strings.TrimPrefix(c.AuthMode, "header:"); name != "" {
			if v := os.Getenv(c.AuthEnv); v != "" {
				h[name] = v
			}
		}
	}
	if strings.EqualFold(c.HTTPMethod, "POST") {
		h["Content-Type"] = "application/json"
	}
	return h
}

func extractArray(raw []byte, arrayPath string) ([]interface{}, error) {
	var root interface{}
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("api: invalid JSON response (%v)", err)
	}
	node := root
	if arrayPath != "" {
		node = get2(root, arrayPath)
		if node == nil {
			return nil, fmt.Errorf("api: array_path %q not found (response shape changed?)", arrayPath)
		}
	}
	arr, ok := node.([]interface{})
	if !ok {
		return nil, fmt.Errorf("api: expected an array at %q", arrayPath)
	}
	return arr, nil
}

// get walks a dot-path through nested objects from a map root.
func get(m map[string]interface{}, path string) interface{} {
	if path == "" {
		return nil
	}
	return get2(m, path)
}

func get2(root interface{}, path string) interface{} {
	cur := root
	for _, p := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func stringify(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return html.UnescapeString(t)
	case bool:
		return fmt.Sprint(t)
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case []interface{}:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s := stringify(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]interface{}:
		b, _ := json.Marshal(t)
		return string(b)
	default:
		return fmt.Sprint(t)
	}
}

var linkRe = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func nextLink(h string) string {
	if m := linkRe.FindStringSubmatch(h); len(m) == 2 {
		return m[1]
	}
	return ""
}

func hashItem(m map[string]interface{}) string {
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-3] + "..."
	}
	return s
}

// excludedScoped drops an item if any exclude term appears in the search text.
// By default it searches the whole item JSON; if fields are given it searches
// only those field paths (precision — e.g. exclude on the type field without
// risking a false match buried in a description).
func excludedScoped(m map[string]interface{}, terms, fields []string) bool {
	if len(terms) == 0 {
		return false
	}
	low := strings.ToLower(scopedHaystack(m, fields))
	for _, t := range terms {
		if t != "" && strings.Contains(low, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// scopedHaystack returns the text to match filters against: the whole item JSON
// when fields is empty, otherwise just the named field paths joined.
func scopedHaystack(m map[string]interface{}, fields []string) string {
	if len(fields) == 0 {
		b, _ := json.Marshal(m)
		return string(b)
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		parts = append(parts, stringify(get(m, f)))
	}
	return strings.Join(parts, " ")
}

// includesScoped keeps an item only if any term appears (case-insensitive). By
// default it searches the whole item JSON; if fields are given it searches only
// those field paths — tighter precision (e.g. match AI in the title, not a
// buried mention in a long description).
func includesScoped(m map[string]interface{}, terms, fields []string) bool {
	low := strings.ToLower(scopedHaystack(m, fields))
	for _, t := range terms {
		if t != "" && strings.Contains(low, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

var tplTok = regexp.MustCompile(`\{(\w+)\}`)

// applyURLTemplate fills {field} placeholders from the item (e.g. award/{id}).
func applyURLTemplate(tmpl string, m map[string]interface{}) string {
	return tplTok.ReplaceAllStringFunc(tmpl, func(tok string) string {
		field := tplTok.FindStringSubmatch(tok)[1]
		return stringify(get(m, field))
	})
}

// parseItemDate parses common API date formats (RFC3339 / ISO date) for the
// recency filter; returns the zero time when unparseable.
func parseItemDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
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

var dateTok = regexp.MustCompile(`\{\{today(_us)?(?:-(\d+)d)?\}\}`)

// expandDates replaces date tokens so rolling query windows stay current with no
// manual edits: {{today}} / {{today-Nd}} emit YYYY-MM-DD (USAspending, grants.gov);
// {{today_us}} / {{today_us-Nd}} emit MM/DD/YYYY (SAM.gov postedFrom/postedTo).
func expandDates(s string) string {
	return dateTok.ReplaceAllStringFunc(s, func(tok string) string {
		sub := dateTok.FindStringSubmatch(tok)
		layout := "2006-01-02"
		if sub[1] == "_us" {
			layout = "01/02/2006"
		}
		d := time.Now().UTC()
		if sub[2] != "" {
			if n, err := strconv.Atoi(sub[2]); err == nil {
				d = d.AddDate(0, 0, -n)
			}
		}
		return d.Format(layout)
	})
}
