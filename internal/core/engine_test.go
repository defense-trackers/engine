package core

import "testing"

func rec(key, text string) Record {
	return Record{Key: key, Fields: map[string]string{"text": text}}
}

func TestDiff(t *testing.T) {
	old := &State{Records: []Record{rec("a", "alpha"), rec("b", "bravo")}}
	new := []Record{rec("a", "alpha v2"), rec("c", "charlie")}
	evs := Diff(old, new, "test", "2026-06-10T00:00:00Z")
	added, removed, changed := countTypes(evs)
	if added != 1 || removed != 1 || changed != 1 {
		t.Fatalf("got +%d -%d ~%d, want +1 -1 ~1", added, removed, changed)
	}
}

func TestDiffFirstRunAllAdded(t *testing.T) {
	evs := Diff(nil, []Record{rec("a", "alpha"), rec("b", "bravo")}, "test", "ts")
	added, removed, changed := countTypes(evs)
	if added != 2 || removed != 0 || changed != 0 {
		t.Fatalf("got +%d -%d ~%d, want +2 -0 ~0", added, removed, changed)
	}
}

func TestValidateMinRecords(t *testing.T) {
	c := Contract{MinRecords: 5}
	if err := Validate(c, nil, []Record{rec("a", "x")}); err == nil {
		t.Fatal("expected min-records invariant to fail")
	}
}

func TestValidateChurnQuarantines(t *testing.T) {
	c := Contract{MinRecords: 1, MaxDeltaPct: 40}
	// Base must be >= churnMinBase for the percentage gate to apply.
	var old []Record
	for i := 0; i < 30; i++ {
		old = append(old, rec(string(rune('a'+i)), "v"))
	}
	// Replace most of the base = high churn → quarantine.
	var big []Record
	for i := 0; i < 30; i++ {
		big = append(big, rec("new"+string(rune('a'+i)), "v"))
	}
	if err := Validate(c, &State{Records: old}, big); err == nil {
		t.Fatal("expected churn invariant to fail on a large base")
	}
	// Small drift on a large base passes: 1 removed + 1 added = ~6%.
	small := append([]Record{}, old[:29]...)
	small = append(small, rec("z", "z"))
	if err := Validate(c, &State{Records: old}, small); err != nil {
		t.Fatalf("expected small drift to pass, got %v", err)
	}
}

func TestValidateSmallBaseSkipsChurn(t *testing.T) {
	c := Contract{MinRecords: 1, MaxDeltaPct: 40}
	old := []Record{rec("a", "a"), rec("b", "b")} // tiny base
	// +11 on a base of 2 would be 550% — but a small base must skip the churn gate.
	var big []Record
	for i := 0; i < 13; i++ {
		big = append(big, rec("k"+string(rune('a'+i)), "v"))
	}
	if err := Validate(c, &State{Records: old}, big); err != nil {
		t.Fatalf("small-base churn should be skipped, got %v", err)
	}
}

func TestValidateAllowEmpty(t *testing.T) {
	// Default: 0 records fails the floor.
	if err := Validate(Contract{}, nil, nil); err == nil {
		t.Fatal("expected empty result to fail by default")
	}
	// allow_empty: a legitimately-empty filtered result is accepted.
	if err := Validate(Contract{AllowEmpty: true}, nil, nil); err != nil {
		t.Fatalf("allow_empty should accept 0 records, got %v", err)
	}
	// allow_empty must override an explicit min_records: 1 (the bug this guards).
	if err := Validate(Contract{MinRecords: 1, AllowEmpty: true}, nil, nil); err != nil {
		t.Fatalf("allow_empty should override min_records=1, got %v", err)
	}
}
