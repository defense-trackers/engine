package workspace

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Phase 3 — deadline calendar. Exports every solicitation close that matters (a
// pursuit, an act-now, or a strong fit) as an .ics feed Jesse can subscribe to
// from Outlook / Google / Apple Calendar, so closes show up where he already
// looks. Pure stdlib — RFC 5545 text, all-day VEVENTs.

// hCalendar serves the deadline feed at /api/calendar.ics.
func (s *server) hCalendar(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	opps := make([]Opportunity, len(s.opps))
	copy(opps, s.opps)
	state := make(map[string]Pursuit, len(s.state))
	for k, v := range s.state {
		state[k] = v
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="realizer-deadlines.ics"`)

	var b strings.Builder
	b.WriteString("BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Realizer//Bid Cockpit//EN\r\nCALSCALE:GREGORIAN\r\nMETHOD:PUBLISH\r\n")
	b.WriteString("X-WR-CALNAME:Realizer deadlines\r\n")
	stamp := time.Now().UTC().Format("20060102T150405Z")
	today := time.Now().UTC().Truncate(24 * time.Hour)

	seen := map[string]bool{}
	for i := range opps {
		o := &opps[i]
		_, isPursuit := state[o.ID]
		// Only surface what matters: a tracked pursuit, an act-now play, or a strong fit.
		if !isPursuit && !o.ActNow && o.Score < 50 {
			continue
		}
		if seen[o.ID] {
			continue
		}
		seen[o.ID] = true
		// The Q&A window is the higher-leverage capture moment — shape the requirement
		// before writing. Calendar it (tighter 3-day alarm) alongside the close.
		if qd, ok := qaOpenUntil(o.Channel); ok && !qd.Before(today) {
			writeQAEvent(&b, o, qd, stamp)
		}
		// Only calendar deadlines that haven't already passed — a forward-looking
		// feed shouldn't carry closed dates (or alarms scheduled in the past).
		if day, ok := parseCloseDay(o.Closes); ok && !day.Before(today) {
			writeVEvent(&b, o, day, stamp)
		}
	}
	b.WriteString("END:VCALENDAR\r\n")
	w.Write([]byte(b.String()))
}

// writeQAEvent calendars a topic's Q&A-window close — the sanctioned, time-boxed
// window to shape the requirement on the record, with a tighter 3-day reminder.
func writeQAEvent(b *strings.Builder, o *Opportunity, day time.Time, stamp string) {
	start := day.Format("20060102")
	end := day.AddDate(0, 0, 1).Format("20060102")
	summary := "Q&A closes: " + o.Title
	desc := "SBIR topic Q&A window — ask the TPOC on the record before it closes (capture before the RFP)."
	if o.MatchedAsset != "" {
		desc += " Asset: " + o.MatchedAsset + "."
	}
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString("UID:qa-" + icsEscape(o.ID) + "@realizer\r\n")
	b.WriteString("DTSTAMP:" + stamp + "\r\n")
	b.WriteString("DTSTART;VALUE=DATE:" + start + "\r\n")
	b.WriteString("DTEND;VALUE=DATE:" + end + "\r\n")
	b.WriteString(foldICS("SUMMARY:" + icsEscape(summary)))
	b.WriteString(foldICS("DESCRIPTION:" + icsEscape(desc)))
	if o.URL != "" {
		b.WriteString(foldICS("URL:" + icsEscape(o.URL)))
	}
	b.WriteString("BEGIN:VALARM\r\nTRIGGER:-P3D\r\nACTION:DISPLAY\r\nDESCRIPTION:" + icsEscape(summary) + "\r\nEND:VALARM\r\n")
	b.WriteString("END:VEVENT\r\n")
}

func writeVEvent(b *strings.Builder, o *Opportunity, day time.Time, stamp string) {
	start := day.Format("20060102")
	end := day.AddDate(0, 0, 1).Format("20060102") // all-day events are [start, end)
	summary := "Closes: " + o.Title
	desc := fmt.Sprintf("Fit %d/100", o.Score)
	if o.MatchedAsset != "" {
		desc += " · asset: " + o.MatchedAsset
	}
	if o.Agency != "" {
		desc += " · " + o.Agency
	}
	if o.Channel != "" {
		desc += " · channel: " + o.Channel
	}
	b.WriteString("BEGIN:VEVENT\r\n")
	b.WriteString("UID:" + icsEscape(o.ID) + "@realizer\r\n")
	b.WriteString("DTSTAMP:" + stamp + "\r\n")
	b.WriteString("DTSTART;VALUE=DATE:" + start + "\r\n")
	b.WriteString("DTEND;VALUE=DATE:" + end + "\r\n")
	b.WriteString(foldICS("SUMMARY:" + icsEscape(summary)))
	b.WriteString(foldICS("DESCRIPTION:" + icsEscape(desc)))
	if o.URL != "" {
		b.WriteString(foldICS("URL:" + icsEscape(o.URL)))
	}
	// a reminder a week out and the day before
	b.WriteString("BEGIN:VALARM\r\nTRIGGER:-P7D\r\nACTION:DISPLAY\r\nDESCRIPTION:" + icsEscape(summary) + "\r\nEND:VALARM\r\n")
	b.WriteString("END:VEVENT\r\n")
}

// parseCloseDay parses a YYYY-MM-DD close date (the normalized form). Returns
// false for empty/rolling/unparseable dates.
func parseCloseDay(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04:05Z07:00", "01/02/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// icsEscape escapes the RFC 5545 special characters in a text value.
func icsEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ";", `\;`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// foldICS wraps a content line at 75 octets per RFC 5545 (continuation lines
// start with a space) and terminates it with CRLF.
func foldICS(line string) string {
	const max = 73 // leave room for CRLF
	if len(line) <= max {
		return line + "\r\n"
	}
	var b strings.Builder
	for len(line) > max {
		b.WriteString(line[:max] + "\r\n ")
		line = line[max:]
	}
	b.WriteString(line + "\r\n")
	return b.String()
}
