package workspace

import "testing"

// The deadline "extended vs pulled-in" flag parses dates instead of comparing
// strings, so a MM/DD/YYYY close that lexically sorts wrong still orders right.
func TestParseCloseDayOrdering(t *testing.T) {
	// "03/01/2026" < "12/01/2025" lexically, but March 2026 is LATER than Dec 2025.
	a, okA := parseCloseDay("12/01/2025")
	b, okB := parseCloseDay("03/01/2026")
	if !okA || !okB {
		t.Fatal("both dates should parse")
	}
	if !b.After(a) {
		t.Fatal("03/01/2026 should parse as after 12/01/2025 (lexical compare would get this wrong)")
	}
	// ISO format still parses and orders correctly.
	c, _ := parseCloseDay("2026-06-30")
	d, _ := parseCloseDay("2026-07-01")
	if !d.After(c) {
		t.Fatal("2026-07-01 should be after 2026-06-30")
	}
}
