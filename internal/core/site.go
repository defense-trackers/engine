package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BaseURL is the public site root (no trailing slash). Used for sitemap/feeds.
const BaseURL = "https://defense-trackers.github.io"

// ListTrackers returns the tracker slugs that have published data.
func ListTrackers(outDir string) []string {
	entries, err := os.ReadDir(filepath.Join(outDir, "data"))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func parseAnyDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, l := range []string{"2006-01-02", time.RFC3339, "01/02/2006", "Jan 2, 2006", "January 2, 2006"} {
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

// PublishDeadlinesFromCloses scans every tracker for records with a future
// "closes" date and publishes them as a synthetic "deadlines-solicitations"
// source — turning solicitation deadlines across the suite into one live
// calendar (and feeding the deadlines .ics). Runs after the normal fetch.
func PublishDeadlinesFromCloses(outDir, quarantineDir string) RunResult {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var recs []Record
	for _, t := range ListTrackers(outDir) {
		if t == "deadlines" {
			continue
		}
		st, err := loadCurrent(outDir, t)
		if err != nil || st == nil {
			continue
		}
		for _, r := range st.Records {
			cl := r.Fields["closes"]
			if cl == "" {
				continue
			}
			d := parseAnyDate(cl)
			if d.IsZero() || d.Before(today) {
				continue
			}
			title := r.Fields["title"]
			if title == "" {
				title = r.Fields["text"]
			}
			f := map[string]string{
				"text":   title,
				"title":  title,
				"date":   d.Format("2006-01-02"),
				"closes": d.Format("2006-01-02"),
				"from":   t,
			}
			if a := r.Fields["agency"]; a != "" {
				f["agency"] = a
			}
			if u := r.Fields["url"]; u != "" {
				f["url"] = u
			}
			recs = append(recs, Record{Key: "close-" + r.Key, Fields: f})
		}
	}
	if len(recs) == 0 {
		return RunResult{Source: "deadlines-solicitations", State: "ok"}
	}
	c := Contract{ID: "deadlines-solicitations", Tracker: "deadlines",
		EmitICal: true, DateField: "date", MinRecords: 1, MaxDeltaPct: 100, CadenceHours: 24}
	return CommitRecords(c, recs, outDir, "", quarantineDir)
}

// WriteSitemap emits sitemap.xml covering the home page, methodology, and each
// tracker page — so the canonical source is discoverable.
func WriteSitemap(outDir string) error {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	add := func(loc string) { fmt.Fprintf(&b, "<url><loc>%s</loc></url>\n", loc) }
	add(BaseURL + "/")
	add(BaseURL + "/methodology.html")
	for _, t := range ListTrackers(outDir) {
		add(BaseURL + "/tracker.html?t=" + t)
	}
	b.WriteString("</urlset>\n")
	return os.WriteFile(filepath.Join(outDir, "sitemap.xml"), []byte(b.String()), 0o644)
}

// WritePerTrackerPages emits /<tracker>/index.html (a byte-copy of tracker.html)
// so each tracker has a clean canonical URL like /pipeline/. tracker.html
// derives its slug from the path, so the copy needs no per-tracker edits.
func WritePerTrackerPages(outDir string) error {
	tpl, err := os.ReadFile(filepath.Join(outDir, "tracker.html"))
	if err != nil {
		return err
	}
	for _, t := range ListTrackers(outDir) {
		dir := filepath.Join(outDir, t)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), tpl, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// WriteFirehose merges recent change events across every tracker into one RSS
// feed — subscribe once, see everything that changes anywhere in the suite.
func WriteFirehose(outDir string) error {
	type item struct {
		ev      Event
		tracker string
	}
	var items []item
	year := time.Now().UTC().Format("2006")
	for _, t := range ListTrackers(outDir) {
		for _, y := range []string{year, fmt.Sprintf("%d", time.Now().UTC().Year()-1)} {
			p := filepath.Join(outDir, "data", t, "events", y+".jsonl")
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
				if line == "" {
					continue
				}
				var e Event
				if json.Unmarshal([]byte(line), &e) == nil {
					items = append(items, item{e, t})
				}
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ev.TS > items[j].ev.TS }) // newest first
	if len(items) > 100 {
		items = items[:100]
	}
	var body strings.Builder
	for _, it := range items {
		pub := it.ev.TS
		if t, err := time.Parse(time.RFC3339, it.ev.TS); err == nil {
			pub = t.Format(time.RFC1123Z)
		}
		fmt.Fprintf(&body,
			"<item><title>[%s · %s] %s</title><link>%s/tracker.html?t=%s</link><guid isPermaLink=\"false\">%s-%s-%s</guid><pubDate>%s</pubDate></item>\n",
			it.tracker, it.ev.Type, xmlEsc.Replace(it.ev.Summary), BaseURL, it.tracker,
			it.tracker, it.ev.Key, xmlEsc.Replace(it.ev.TS), pub)
	}
	feed := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Defense Trackers — all changes</title>
<link>%s/</link>
<description>Every change across all trackers. Append-only, hash-chained.</description>
%s</channel></rss>
`, BaseURL, body.String())
	dir := filepath.Join(outDir, "feeds")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "all.xml"), []byte(feed), 0o644)
}
