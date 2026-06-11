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
		raw, err := core.FetchRaw(method, nextURL, headers, body)
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
			nextURL = nextLink(core.LastLinkHeader())
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
		if excluded(m, c.Exclude) {
			continue
		}
		if len(c.Include) > 0 && !includesScoped(m, c.Include, c.IncludeFields) {
			continue
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
		q.Set(k, v)
	}
	if c.Paginate == "page" {
		q.Set("page", fmt.Sprint(page))
		per := c.PerPage
		if per == 0 {
			per = 100
		}
		q.Set("per_page", fmt.Sprint(per))
	}
	if strings.HasPrefix(c.AuthMode, "query:") {
		if val := os.Getenv(c.AuthEnv); val != "" {
			q.Set(strings.TrimPrefix(c.AuthMode, "query:"), val)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// authHeaders sets a Bearer header when auth_mode=header and the token env is
// present. A missing token is non-fatal — the request proceeds unauthenticated
// (lower rate limit) rather than failing, so local runs work without secrets.
func authHeaders(c core.Contract) map[string]string {
	h := map[string]string{}
	if c.AuthMode == "header" {
		if v := os.Getenv(c.AuthEnv); v != "" {
			h["Authorization"] = "Bearer " + v
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

// excluded drops an item if any exclude term appears anywhere in it
// (case-insensitive over the item's JSON, so it matches id, tags, etc.).
func excluded(m map[string]interface{}, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	b, _ := json.Marshal(m)
	low := strings.ToLower(string(b))
	for _, t := range terms {
		if t != "" && strings.Contains(low, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

// includesScoped keeps an item only if any term appears (case-insensitive). By
// default it searches the whole item JSON; if fields are given it searches only
// those field paths — tighter precision (e.g. match AI in the title, not a
// buried mention in a long description).
func includesScoped(m map[string]interface{}, terms, fields []string) bool {
	var hay string
	if len(fields) == 0 {
		b, _ := json.Marshal(m)
		hay = string(b)
	} else {
		parts := make([]string, 0, len(fields))
		for _, f := range fields {
			parts = append(parts, stringify(get(m, f)))
		}
		hay = strings.Join(parts, " ")
	}
	low := strings.ToLower(hay)
	for _, t := range terms {
		if t != "" && strings.Contains(low, strings.ToLower(t)) {
			return true
		}
	}
	return false
}

var dateTok = regexp.MustCompile(`\{\{today(?:-(\d+)d)?\}\}`)

// expandDates replaces {{today}} and {{today-Nd}} with YYYY-MM-DD so rolling
// query windows (e.g. USAspending) stay current with no manual edits.
func expandDates(s string) string {
	return dateTok.ReplaceAllStringFunc(s, func(tok string) string {
		sub := dateTok.FindStringSubmatch(tok)
		d := time.Now().UTC()
		if sub[1] != "" {
			if n, err := strconv.Atoi(sub[1]); err == nil {
				d = d.AddDate(0, 0, -n)
			}
		}
		return d.Format("2006-01-02")
	})
}
