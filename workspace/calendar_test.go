package workspace

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The .ics feed is forward-looking: a deadline already in the past must not emit
// a VEVENT (it would carry an alarm scheduled in the past), while a future
// deadline must. Guards the "omit already-closed deadlines" fix.
func TestCalendarSkipsPastDeadlines(t *testing.T) {
	s := &server{
		state: map[string]Pursuit{},
		opps: []Opportunity{
			{ID: "past", Title: "Already closed", Score: 90, ActNow: true, Closes: "2020-01-01"},
			{ID: "future", Title: "Still open", Score: 90, ActNow: true, Closes: "2030-01-01"},
		},
	}
	rec := httptest.NewRecorder()
	s.hCalendar(rec, httptest.NewRequest("GET", "/api/calendar.ics", nil))
	body := rec.Body.String()

	if strings.Contains(body, "DTSTART;VALUE=DATE:20200101") {
		t.Fatal("past deadline should not appear in the .ics feed")
	}
	if !strings.Contains(body, "DTSTART;VALUE=DATE:20300101") {
		t.Fatal("future deadline should appear in the .ics feed")
	}
	if n := strings.Count(body, "BEGIN:VEVENT"); n != 1 {
		t.Fatalf("expected exactly 1 future event, got %d", n)
	}
}
