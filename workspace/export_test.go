package workspace

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func TestBuildDocxValidZip(t *testing.T) {
	md := "# Title\n\n## Section One\n\nBody text with **bold** words and a `code` span.\n\n- bullet one\n- bullet two\n\n---\n\nFinal paragraph."
	b, err := buildDocx(md)
	if err != nil {
		t.Fatalf("buildDocx: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	want := map[string]bool{"[Content_Types].xml": false, "_rels/.rels": false, "word/document.xml": false}
	var docXML string
	for _, f := range zr.File {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			var sb strings.Builder
			buf := make([]byte, 4096)
			for {
				n, e := rc.Read(buf)
				sb.Write(buf[:n])
				if e != nil {
					break
				}
			}
			rc.Close()
			docXML = sb.String()
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("missing required part %s", name)
		}
	}
	if !strings.Contains(docXML, "<w:document") || !strings.Contains(docXML, "Title") {
		t.Errorf("document.xml missing expected content")
	}
	if !strings.Contains(docXML, "<w:b/>") {
		t.Errorf("bold run not emitted")
	}
}

func TestDocxEscaping(t *testing.T) {
	b, err := buildDocx("Text with <tag> & \"quotes\".")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("<tag>")) {
		t.Errorf("raw < not escaped in output")
	}
}

func TestComplianceRequirements(t *testing.T) {
	detail := "The offeror shall demonstrate feasibility. This sentence is background only. " +
		"The system must operate under 2W power. Proposals are required to address transition. " +
		"Another descriptive sentence with no binding language at all here."
	reqs := complianceRequirements(detail)
	if len(reqs) != 3 {
		t.Fatalf("want 3 binding requirements, got %d: %v", len(reqs), reqs)
	}
	for _, r := range reqs {
		if !shallRe.MatchString(r) {
			t.Errorf("extracted non-binding line: %q", r)
		}
	}
}

func TestStrongTerms(t *testing.T) {
	// over-stuffed query → distinctive terms only, generic words dropped, longest first
	got := strongTerms("counter-UAS C-UAS DoD drone systems")
	if len(got) == 0 || got[0] != "counter-UAS" {
		t.Errorf("want counter-UAS first, got %v", got)
	}
	for _, term := range got {
		if term == "DoD" || term == "systems" {
			t.Errorf("generic term not dropped: %v", got)
		}
	}
	// capped at 3
	if len(strongTerms("alpha bravo charlie delta echo foxtrot")) > 3 {
		t.Errorf("not capped at 3")
	}
}

func TestComplianceEmpty(t *testing.T) {
	if r := complianceRequirements(""); r != nil {
		t.Errorf("empty detail should yield nil, got %v", r)
	}
	if md := complianceMatrixMD("no binding statements in this text whatsoever"); md != "" {
		t.Errorf("no requirements should yield empty matrix")
	}
}
