package workspace

import "testing"

func TestCalibrationLoop(t *testing.T) {
	// No outcomes → zero shift (honest raw model from day one).
	if r := calibrationReport(map[string]Pursuit{}); r.Shift != 0 || r.N != 0 {
		t.Fatalf("empty state should give shift 0 / n 0, got %+v", r)
	}

	// Bids with no stamped prediction don't count.
	noPred := map[string]Pursuit{"a": {Stage: "won"}, "b": {Stage: "lost"}}
	if r := calibrationReport(noPred); r.N != 0 {
		t.Fatalf("unstamped outcomes must not calibrate, got n=%d", r.N)
	}

	// Overconfident: predicted 80%, all lost → strong negative shift.
	over := map[string]Pursuit{}
	for i := 0; i < 3; i++ {
		over[string(rune('a'+i))] = Pursuit{Stage: "lost", PredictedWin: 80}
	}
	r := calibrationReport(over)
	if r.Shift >= 0 || r.N != 3 {
		t.Fatalf("overconfident losses should shift negative, got %+v", r)
	}
	// residual = (0 - 2.4)/(3+5) = -0.30 → -30
	if r.Shift != -30 {
		t.Fatalf("expected -30 shift, got %d", r.Shift)
	}
	if r.Trusted {
		t.Fatal("3 outcomes should not be 'trusted' (< calTrustN)")
	}

	// Underconfident: predicted 20%, all won → positive shift.
	under := map[string]Pursuit{}
	for i := 0; i < 6; i++ {
		under[string(rune('a'+i))] = Pursuit{Stage: "won", PredictedWin: 20}
	}
	ru := calibrationReport(under)
	if ru.Shift <= 0 {
		t.Fatalf("underconfident wins should shift positive, got %d", ru.Shift)
	}
	if !ru.Trusted {
		t.Fatal("6 outcomes should be trusted (>= calTrustN)")
	}

	// calibrate() applies the shift, clamps, and never touches realized facts.
	if got := calibrate(50, -30); got != 20 {
		t.Fatalf("calibrate(50,-30)=%d, want 20", got)
	}
	if got := calibrate(10, -30); got != 2 {
		t.Fatalf("calibrate should clamp low to 2, got %d", got)
	}
	if got := calibrate(100, -30); got != 100 {
		t.Fatal("a won bid (100) must stay 100")
	}
	if got := calibrate(0, 40); got != 0 {
		t.Fatal("a lost/out-of-scope bid (0) must stay 0")
	}
}
