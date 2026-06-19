package workspace

import "testing"

// reportMoney (exported reports/docx) must mirror the UI's mK tiers: $K under a
// million, $M under a billion, $B at and above — so a program-of-record value
// reads "$1.3B", not "$1300.0M".
func TestReportMoneyTiers(t *testing.T) {
	cases := map[int]string{
		240:       "$240K",
		1500:      "$1.5M",
		209700:    "$209.7M",
		1_000_000: "$1.0B",
		1_300_000: "$1.3B",
		2_000_000: "$2.0B",
	}
	for in, want := range cases {
		if got := reportMoney(in); got != want {
			t.Errorf("reportMoney(%d) = %q, want %q", in, got, want)
		}
	}
}
