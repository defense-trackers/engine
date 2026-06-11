package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// WriteICal emits feeds/<tracker>.ics from records that carry a date field.
// Powers the deadlines-calendar tracker: the .ics is the whole product.
func WriteICal(outDir, tracker, dateField string, recs []Record) error {
	if dateField == "" {
		dateField = "date"
	}
	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\n")
	b.WriteString("VERSION:2.0\r\n")
	fmt.Fprintf(&b, "PRODID:-//defense-trackers//%s//EN\r\n", tracker)
	fmt.Fprintf(&b, "X-WR-CALNAME:%s\r\n", icalEscape(tracker))
	for _, r := range recs {
		dt := parseICalDate(r.Fields[dateField])
		if dt == "" {
			continue
		}
		summary := r.Fields["title"]
		if summary == "" {
			summary = r.Fields["text"]
		}
		b.WriteString("BEGIN:VEVENT\r\n")
		fmt.Fprintf(&b, "UID:%s@defense-trackers\r\n", r.Key)
		fmt.Fprintf(&b, "DTSTART;VALUE=DATE:%s\r\n", dt)
		fmt.Fprintf(&b, "SUMMARY:%s\r\n", icalEscape(summary))
		if u := r.Fields["url"]; u != "" {
			fmt.Fprintf(&b, "URL:%s\r\n", u)
		}
		b.WriteString("END:VEVENT\r\n")
	}
	b.WriteString("END:VCALENDAR\r\n")

	dir := filepath.Join(outDir, "feeds")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, tracker+".ics"), []byte(b.String()), 0o644)
}

// parseICalDate accepts common date formats and returns YYYYMMDD, or "".
func parseICalDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	for _, layout := range []string{
		"2006-01-02", time.RFC3339, "2006-01-02T15:04:05Z07:00",
		time.RFC1123Z, time.RFC1123, "01/02/2006", "Jan 2, 2006", "January 2, 2006",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format("20060102")
		}
	}
	// Fall back to a leading YYYY-MM-DD if present.
	if len(s) >= 10 {
		if t, err := time.Parse("2006-01-02", s[:10]); err == nil {
			return t.Format("20060102")
		}
	}
	return ""
}

func icalEscape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", ";", "\\;", ",", "\\,", "\n", "\\n", "\r", "")
	return r.Replace(s)
}
