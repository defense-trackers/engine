package workspace

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Phase 4 — submission-ready output. Turns a generated volume.md into a real .docx
// Jesse can open in Word (stdlib only: a .docx is a zip of a few XML parts), and
// extracts a compliance matrix (every shall/must/required statement → the section
// that must answer it) so nothing in the solicitation goes unaddressed. No deps.

// hExport serves the current draft's volume as a Word document:
//
//	GET /api/export?id=<oppId>[&compliance=1]
func (s *server) hExport(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s.mu.Lock()
	subj := s.subjectFor(id)
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	dir := filepath.Join(s.opts.Dir, "drafts", slugify(subj.ID))
	md, err := os.ReadFile(filepath.Join(dir, "volume.md"))
	if err != nil {
		http.Error(w, "no draft yet — run a draft first", 404)
		return
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK) // probe: draft exists, skip the docx build
		return
	}
	body := string(md)
	if r.URL.Query().Get("compliance") == "1" {
		if cm := complianceMatrixMD(s.detailFor(subj)); cm != "" {
			body += "\n\n---\n\n" + cm
		}
	}
	doc, err := buildDocx(body)
	if err != nil {
		http.Error(w, "docx build: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.docx"`, slugify(subj.ID)))
	w.Write(doc)
}

// hCompliance returns the compliance matrix for an opportunity as JSON (the
// extracted shall/must/required statements). Grounds on the ingested RFP if present.
func (s *server) hCompliance(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	s.mu.Lock()
	subj := s.subjectFor(id)
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	reqs := complianceRequirements(s.detailFor(subj))
	writeJSON(w, map[string]any{"count": len(reqs), "requirements": reqs, "has_detail": s.detailFor(subj) != ""})
}

// --- compliance extraction -------------------------------------------------

var shallRe = regexp.MustCompile(`(?i)\b(shall|must|is required to|are required to|will be required|required to)\b`)

// complianceRequirements pulls the binding requirement sentences (shall/must/
// required) out of the solicitation text — the literal list an evaluator checks.
func complianceRequirements(detail string) []string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return nil
	}
	// Normalize whitespace, then split into sentence-ish units.
	clean := regexp.MustCompile(`\s+`).ReplaceAllString(detail, " ")
	parts := regexp.MustCompile(`(?:\.\s+|;\s+|\n)`).Split(clean, -1)
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) < 12 || len(p) > 400 {
			continue
		}
		if !shallRe.MatchString(p) {
			continue
		}
		key := strings.ToLower(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		if !strings.HasSuffix(p, ".") {
			p += "."
		}
		out = append(out, p)
		if len(out) >= 60 {
			break
		}
	}
	return out
}

// complianceMatrixMD renders the requirements as a markdown table mapping each to
// the section that must answer it (left for Jesse/Claude to fill).
func complianceMatrixMD(detail string) string {
	reqs := complianceRequirements(detail)
	if len(reqs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# Compliance matrix\n\n")
	b.WriteString("_Every binding requirement (shall/must/required) extracted from the solicitation. Map each to the volume section that answers it and confirm coverage before submission._\n\n")
	for i, rq := range reqs {
		b.WriteString(fmt.Sprintf("%d. **REQ-%02d** — %s  \n    _Answered in: ___________  · Status: ☐_\n\n", i+1, i+1, rq))
	}
	return b.String()
}

// --- minimal OOXML .docx builder -------------------------------------------

type docRun struct {
	text string
	bold bool
}
type docPara struct {
	runs  []docRun
	size  int // half-points for headings; 0 = body (22 = 11pt)
	bold  bool
	bullet bool
}

// buildDocx converts a markdown subset (#/##/### headings, **bold**, - bullets,
// paragraphs, --- rules) into a valid Word document as raw bytes.
func buildDocx(md string) ([]byte, error) {
	paras := mdToParas(md)
	var body strings.Builder
	for _, p := range paras {
		body.WriteString(renderPara(p))
	}
	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		body.String() +
		`<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:bottom="1440" w:left="1440" w:right="1440"/></w:sectPr>` +
		`</w:body></w:document>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`,
		"_rels/.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
			`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>` +
			`</Relationships>`,
		"word/document.xml": document,
	}
	// Stable order so the package is deterministic.
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		f, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := f.Write([]byte(files[name])); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func mdToParas(md string) []docPara {
	var out []docPara
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimRight(raw, "\r")
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			continue
		case t == "---" || t == "***" || t == "___":
			out = append(out, docPara{runs: []docRun{{text: ""}}}) // spacer
		case strings.HasPrefix(t, "### "):
			out = append(out, docPara{runs: inlineRuns(t[4:], true), size: 24, bold: true})
		case strings.HasPrefix(t, "## "):
			out = append(out, docPara{runs: inlineRuns(t[3:], true), size: 28, bold: true})
		case strings.HasPrefix(t, "# "):
			out = append(out, docPara{runs: inlineRuns(t[2:], true), size: 36, bold: true})
		case strings.HasPrefix(t, "> "):
			out = append(out, docPara{runs: inlineRuns(t[2:], false)})
		case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
			out = append(out, docPara{runs: inlineRuns(t[2:], false), bullet: true})
		default:
			out = append(out, docPara{runs: inlineRuns(t, false)})
		}
	}
	return out
}

// inlineRuns splits on **bold** markers (and strips _italics_/`code` markers,
// which Word renders as plain text here). forceBold makes the whole line bold.
func inlineRuns(s string, forceBold bool) []docRun {
	s = strings.ReplaceAll(s, "`", "")
	parts := strings.Split(s, "**")
	var runs []docRun
	for i, p := range parts {
		if p == "" {
			continue
		}
		runs = append(runs, docRun{text: stripEmph(p), bold: forceBold || i%2 == 1})
	}
	if len(runs) == 0 {
		runs = []docRun{{text: "", bold: forceBold}}
	}
	return runs
}

func stripEmph(s string) string {
	// drop single-underscore/asterisk emphasis markers, keep the words
	s = regexp.MustCompile(`(^|[^_])_([^_]+)_`).ReplaceAllString(s, `$1$2`)
	s = regexp.MustCompile(`(^|[^*])\*([^*]+)\*`).ReplaceAllString(s, `$1$2`)
	return s
}

func renderPara(p docPara) string {
	var b strings.Builder
	b.WriteString("<w:p>")
	if p.size > 0 || p.bullet {
		b.WriteString("<w:pPr>")
		b.WriteString(`<w:spacing w:before="120" w:after="80"/>`)
		if p.bullet {
			b.WriteString(`<w:ind w:left="360" w:hanging="180"/>`)
		}
		b.WriteString("</w:pPr>")
	}
	runs := p.runs
	if p.bullet {
		b.WriteString(`<w:r><w:t xml:space="preserve">• </w:t></w:r>`)
	}
	for _, r := range runs {
		b.WriteString("<w:r><w:rPr>")
		if r.bold || p.bold {
			b.WriteString("<w:b/>")
		}
		if p.size > 0 {
			b.WriteString(fmt.Sprintf(`<w:sz w:val="%d"/>`, p.size))
		}
		b.WriteString("</w:rPr>")
		b.WriteString(`<w:t xml:space="preserve">` + xmlEscape(r.text) + `</w:t></w:r>`)
	}
	b.WriteString("</w:p>")
	return b.String()
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}
