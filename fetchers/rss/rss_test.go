package rss

import (
	"os"
	"testing"
)

func TestParseRSS(t *testing.T) {
	raw, err := os.ReadFile("testdata/feed.xml")
	if err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	recs, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	first := recs[0]
	if first.Key != "https://example.gov/news/il5" {
		t.Errorf("key: got %q", first.Key)
	}
	if first.Fields["title"] == "" || first.Fields["url"] == "" || first.Fields["date"] == "" {
		t.Errorf("missing fields: %+v", first.Fields)
	}
	if first.Fields["text"] != first.Fields["title"] {
		t.Errorf("text should mirror title for clean changelog summaries")
	}
}

func TestParseAtom(t *testing.T) {
	atom := []byte(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><title>Entry one</title><link href="https://x.gov/1" rel="alternate"/><id>urn:1</id><updated>2026-06-10T00:00:00Z</updated></entry>
</feed>`)
	recs, err := Parse(atom)
	if err != nil {
		t.Fatalf("parse atom: %v", err)
	}
	if len(recs) != 1 || recs[0].Key != "urn:1" || recs[0].Fields["url"] != "https://x.gov/1" {
		t.Fatalf("unexpected atom parse: %+v", recs)
	}
}

func TestEmptyFeedFailsLoud(t *testing.T) {
	if _, err := Parse([]byte(`<rss version="2.0"><channel></channel></rss>`)); err == nil {
		t.Fatal("expected loud failure on an empty feed")
	}
}
