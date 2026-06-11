package api

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"testing"

	"engine/internal/core"
)

var update = flag.Bool("update", false, "rewrite golden files from current output")

func githubContract() core.Contract {
	return core.Contract{
		ID:       "oss-dod",
		ArrayPath: "",
		KeyField: "full_name",
		FieldMap: map[string]string{
			"title":       "full_name",
			"url":         "html_url",
			"description": "description",
			"pushed":      "pushed_at",
			"stars":       "stargazers_count",
			"tags":        "topics",
		},
	}
}

func TestGoldenGitHubRepos(t *testing.T) {
	raw, err := os.ReadFile("testdata/github_repos.json")
	if err != nil {
		t.Fatalf("fixture missing: %v", err)
	}
	recs, err := Parse(raw, githubContract())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	got, _ := json.MarshalIndent(recs, "", " ")
	got = append(got, '\n')

	const golden = "testdata/github_repos_golden.json"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("golden rewritten (%d records)", len(recs))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("golden missing — run: go test ./fetchers/api -run Golden -update")
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output diverged from golden; rerun with -update if intended")
	}
}

func TestNestedArrayPath(t *testing.T) {
	raw := []byte(`{"results":[{"document_number":"2026-1","title":"AI EO","html_url":"http://x"}]}`)
	c := core.Contract{ArrayPath: "results", KeyField: "document_number",
		FieldMap: map[string]string{"title": "title", "url": "html_url"}}
	recs, err := Parse(raw, c)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 1 || recs[0].Key != "2026-1" || recs[0].Fields["title"] != "AI EO" {
		t.Fatalf("unexpected: %+v", recs)
	}
}

func TestMissingArrayPathFailsLoud(t *testing.T) {
	_, err := Parse([]byte(`{"data":[]}`), core.Contract{ArrayPath: "results"})
	if err == nil {
		t.Fatal("expected loud failure when array_path is absent — that is the repair trigger")
	}
}

func TestStringifyNumbersAndArrays(t *testing.T) {
	if got := stringify(float64(42)); got != "42" {
		t.Errorf("int float: got %q", got)
	}
	if got := stringify([]interface{}{"a", "b"}); got != "a, b" {
		t.Errorf("array join: got %q", got)
	}
}
