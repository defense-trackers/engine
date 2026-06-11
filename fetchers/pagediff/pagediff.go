// Package pagediff implements the engine's first fetcher: fetch a page,
// optionally slice it between literal markers, strip markup, normalize the
// visible text into lines, and emit one Record per unique line keyed by
// content hash. Diffing those keys across runs yields a change log for any
// page — no per-site parser required.
//
// Deliberate limitation: this diffs *rendered text*, not DOM structure.
// Inline tags inside a word (rare) will split it. When a source needs real
// DOM queries, write a dedicated fetcher (vendoring golang.org/x/net/html)
// and let the repair loop own that parser. For list-shaped pages like the
// Blue UAS lists, text diffing is exactly the right fidelity.
package pagediff

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"strings"
	"unicode"

	"engine/internal/core"
)

type Pagediff struct{}

func New() *Pagediff { return &Pagediff{} }

func (p *Pagediff) Fetch(c core.Contract, cacheDir string) ([]core.Record, error) {
	body, err := core.FetchURL(c.URL, cacheDir)
	if err != nil {
		return nil, err
	}
	return Parse(string(body), c)
}

// Parse is pure and deterministic — the golden test runs it directly.
func Parse(doc string, c core.Contract) ([]core.Record, error) {
	if c.SliceStart != "" {
		i := strings.Index(doc, c.SliceStart)
		if i < 0 {
			return nil, fmt.Errorf("parse: slice_start marker not found (page structure changed?)")
		}
		doc = doc[i+len(c.SliceStart):]
	}
	if c.SliceEnd != "" {
		i := strings.Index(doc, c.SliceEnd)
		if i < 0 {
			return nil, fmt.Errorf("parse: slice_end marker not found (page structure changed?)")
		}
		doc = doc[:i]
	}

	text := stripTags(doc)
	seen := map[string]bool{}
	var records []core.Record
	for _, raw := range strings.Split(text, "\n") {
		line := strings.Join(strings.Fields(html.UnescapeString(raw)), " ")
		if len(line) < 3 || !hasAlnum(line) || seen[line] {
			continue
		}
		seen[line] = true
		h := sha256.Sum256([]byte(line))
		records = append(records, core.Record{
			Key:    hex.EncodeToString(h[:8]),
			Fields: map[string]string{"text": line},
		})
	}
	return records, nil
}

// blockTags break lines; other tags collapse to a space so inline markup
// inside a phrase keeps the phrase on one line.
var blockTags = map[string]bool{
	"br": true, "p": true, "div": true, "li": true, "ul": true, "ol": true,
	"tr": true, "td": true, "th": true, "table": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "section": true,
	"article": true, "header": true, "footer": true, "nav": true,
	"main": true, "aside": true, "figcaption": true, "blockquote": true,
}

func stripTags(s string) string {
	lower := strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s) / 2)
	i := 0
	for i < len(s) {
		if s[i] != '<' {
			b.WriteByte(s[i])
			i++
			continue
		}
		// Comments.
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i:], "-->")
			if end < 0 {
				break
			}
			i += end + 3
			continue
		}
		// Script and style: skip their entire contents.
		for _, skip := range []string{"script", "style"} {
			if strings.HasPrefix(lower[i:], "<"+skip) {
				end := strings.Index(lower[i:], "</"+skip)
				if end < 0 {
					return b.String()
				}
				i += end
				break
			}
		}
		// Consume the tag itself.
		close := strings.IndexByte(s[i:], '>')
		if close < 0 {
			break
		}
		name := tagName(lower[i : i+close+1])
		if blockTags[name] {
			b.WriteByte('\n')
		} else {
			b.WriteByte(' ')
		}
		i += close + 1
	}
	return b.String()
}

func tagName(tag string) string {
	t := strings.TrimPrefix(strings.TrimPrefix(tag, "<"), "/")
	var name strings.Builder
	for _, r := range t {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			break
		}
		name.WriteRune(r)
	}
	return name.String()
}

func hasAlnum(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
