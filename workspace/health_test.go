package workspace

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// /api/health is a liveness/readiness probe: it must always 200 with ok=true and
// report counts, even before any pursuits exist, and never block on Claude.
func TestHealthEndpoint(t *testing.T) {
	s := &server{
		state: map[string]Pursuit{"p1": {Stage: "watching"}},
		opps:  []Opportunity{{ID: "a"}, {ID: "b"}},
		caps:  &Capabilities{},
	}
	rec := httptest.NewRecorder()
	s.hHealth(rec, httptest.NewRequest("GET", "/api/health", nil))
	if rec.Code != 200 {
		t.Fatalf("health should return 200, got %d", rec.Code)
	}
	var r struct {
		OK       bool `json:"ok"`
		Ready    bool `json:"ready"`
		Opps     int  `json:"opps"`
		Pursuits int  `json:"pursuits"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatalf("health body not JSON: %v", err)
	}
	if !r.OK || !r.Ready || r.Opps != 2 || r.Pursuits != 1 {
		t.Fatalf("unexpected health payload: %+v", r)
	}
}
