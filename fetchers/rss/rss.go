// Package rss fetches RSS 2.0 or Atom feeds and emits one record per item,
// keyed by guid (or link). Covers Google News [PR] monitoring queries and any
// agency release feed. The key is stable across runs so re-appearing items
// don't churn; items aging out of the feed become "removed" events.
package rss

import (
	"encoding/xml"
	"fmt"
	"strings"

	"engine/internal/core"
)

type RSS struct{}

func New() *RSS { return &RSS{} }

func (r *RSS) Fetch(c core.Contract, cacheDir string) ([]core.Record, error) {
	body, err := core.FetchURL(c.URL, cacheDir)
	if err != nil {
		return nil, err
	}
	return Parse(body)
}

type doc struct {
	Items   []rssItem   `xml:"channel>item"`
	Entries []atomEntry `xml:"entry"`
}
type rssItem struct {
	Title   string `xml:"title"`
	Link    string `xml:"link"`
	GUID    string `xml:"guid"`
	PubDate string `xml:"pubDate"`
}
type atomEntry struct {
	Title   string     `xml:"title"`
	Links   []atomLink `xml:"link"`
	ID      string     `xml:"id"`
	Updated string     `xml:"updated"`
}
type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

// Parse is the pure, testable path over feed bytes.
func Parse(body []byte) ([]core.Record, error) {
	var d doc
	if err := xml.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("rss: parse failed (%v)", err)
	}
	var records []core.Record
	seen := map[string]bool{}
	add := func(key, title, link, date string) {
		title = strings.TrimSpace(title)
		link = strings.TrimSpace(link)
		if key == "" {
			key = link
		}
		if key == "" {
			key = title
		}
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		f := map[string]string{"text": title, "title": title}
		if link != "" {
			f["url"] = link
		}
		if date != "" {
			f["date"] = strings.TrimSpace(date)
		}
		records = append(records, core.Record{Key: key, Fields: f})
	}
	for _, it := range d.Items {
		add(it.GUID, it.Title, it.Link, it.PubDate)
	}
	for _, e := range d.Entries {
		add(e.ID, e.Title, atomHref(e.Links), e.Updated)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("rss: no items found (not a valid RSS/Atom feed?)")
	}
	return records, nil
}

func atomHref(links []atomLink) string {
	for _, l := range links {
		if l.Rel == "" || l.Rel == "alternate" {
			return l.Href
		}
	}
	if len(links) > 0 {
		return links[0].Href
	}
	return ""
}
