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
	var old []Record
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		old = append(old, rec(k, k))
	}
	// 8 removed + 1 added on a base of 10 = 90% churn.
	if err := Validate(c, &State{Records: old},
		[]Record{rec("a", "a"), rec("b", "b"), rec("z", "z")}); err == nil {
		t.Fatal("expected churn invariant to fail")
	}
	// Small drift passes: 1 removed + 1 added = 20%.
	small := append([]Record{}, old[:9]...)
	small = append(small, rec("z", "z"))
	if err := Validate(c, &State{Records: old}, small); err != nil {
		t.Fatalf("expected small drift to pass, got %v", err)
	}
}
