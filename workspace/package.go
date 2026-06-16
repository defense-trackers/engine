package workspace

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Phase 5c — submission pre-flight + packaging. A great volume still loses if the
// firm isn't registered, the format is wrong, or the data-rights legend is missing
// — eliminations that have nothing to do with merit. Pre-flight surfaces those as a
// hard checklist; packaging assembles the upload-ready bundle (cover + volume +
// compliance matrix + supporting docs) into one zip.

type checkItem struct {
	Label  string `json:"label"`
	Status string `json:"status"` // ok | warn | blocker
	Detail string `json:"detail,omitempty"`
}

// preflight builds the deterministic submission checklist for an opportunity.
func (s *server) preflight(o *Opportunity) []checkItem {
	ck := LoadCompanyKit(s.opts.Dir)
	hasDraft := s.hasDraft(o.ID)
	reqs := complianceRequirements(s.detailFor(o))
	var c []checkItem
	add := func(label, status, detail string) { c = append(c, checkItem{label, status, detail}) }

	if hasDraft {
		add("Volume drafted", "ok", "volume.md present — export the .docx from this bundle")
	} else {
		add("Volume drafted", "blocker", "no draft yet — run Draft volume / Full workup first")
	}
	if ck == nil {
		add("Company Kit", "warn", "no company-kit.json — run: engine workspace company-kit --build, then fill identifiers")
	} else {
		add("Company Kit", "ok", "loaded")
		if strings.TrimSpace(ck.UEI) != "" {
			add("SAM UEI", "ok", ck.UEI)
		} else {
			add("SAM UEI", "blocker", "no UEI — you cannot submit without an active SAM registration")
		}
		if strings.TrimSpace(ck.CAGE) != "" {
			add("CAGE code", "ok", ck.CAGE)
		} else {
			add("CAGE code", "warn", "no CAGE on file — confirm it's assigned")
		}
		if ck.SmallBiz {
			add("Small-business self-cert", "ok", "SBC")
		} else {
			add("Small-business self-cert", "warn", "SBIR/STTR requires small-business eligibility — confirm")
		}
		if strings.TrimSpace(ck.DataRights) != "" {
			add("Data-rights legend", "ok", "defined — assert SBIR/GPR legend on every deliverable")
		} else {
			add("Data-rights legend", "warn", "no data-rights stance set — protect the Phase III chain (SBIR data rights / GPR)")
		}
	}
	if len(reqs) > 0 {
		add("Compliance requirements", "ok", fmt.Sprintf("%d binding statements extracted — run Verify compliance to confirm coverage", len(reqs)))
	} else {
		add("Compliance requirements", "warn", "no solicitation text to scan — Ingest the RFP so nothing is missed")
	}
	if o.DaysLeft < 0 {
		add("Deadline", "warn", "no close date on file — verify the solicitation is open")
	} else if o.DaysLeft == 0 {
		add("Deadline", "blocker", "closes today — confirm you can still upload")
	} else {
		add("Deadline", "ok", fmt.Sprintf("%d days of runway", o.DaysLeft))
	}
	add("Format compliance", "warn", "verify section structure + page intent against the live solicitation before upload (SBIR Phase I = 12 sections; DARPA = whitepaper+deck)")
	return c
}

// hPreflight returns the submission checklist as JSON.
func (s *server) hPreflight(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	subj := s.subjectFor(r.URL.Query().Get("id"))
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	c := s.preflight(subj)
	blockers, warns := 0, 0
	for _, it := range c {
		if it.Status == "blocker" {
			blockers++
		} else if it.Status == "warn" {
			warns++
		}
	}
	writeJSON(w, map[string]any{"checks": c, "blockers": blockers, "warnings": warns, "ready": blockers == 0})
}

// hPackage assembles the upload-ready submission bundle as a .zip.
func (s *server) hPackage(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	subj := s.subjectFor(r.URL.Query().Get("id"))
	s.mu.Unlock()
	if subj == nil {
		http.Error(w, "not found", 404)
		return
	}
	dir := filepath.Join(s.opts.Dir, "drafts", slugify(subj.ID))
	volume, err := os.ReadFile(filepath.Join(dir, "volume.md"))
	if err != nil {
		http.Error(w, "no draft yet — run a draft first", 404)
		return
	}
	ck := LoadCompanyKit(s.opts.Dir)
	detail := s.detailFor(subj)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeDoc := func(name, md string) {
		if b, e := buildDocx(md); e == nil {
			if f, e := zw.Create(name); e == nil {
				f.Write(b)
			}
		}
	}
	writeText := func(name string, b []byte) {
		if f, e := zw.Create(name); e == nil {
			f.Write(b)
		}
	}

	writeDoc("00-cover.docx", coverMD(subj, ck))
	writeDoc("01-volume.docx", string(volume))
	if cm := complianceMatrixMD(detail); cm != "" {
		writeDoc("02-compliance-matrix.docx", cm)
	}
	// supporting artifacts, if generated
	for _, f := range []string{"00-research.md", "00-reviewer-notes.md", "compliance-report.md", "compliance-fixes.md", "win-plan.md"} {
		if b, e := os.ReadFile(filepath.Join(dir, f)); e == nil {
			writeText(f, b)
		}
	}
	// the pre-flight checklist itself
	writeText("PREFLIGHT.md", []byte(preflightMD(subj, s.preflight(subj))))

	zw.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-submission.zip"`, slugify(subj.ID)))
	w.Write(buf.Bytes())
}

func coverMD(o *Opportunity, ck *CompanyKit) string {
	var b strings.Builder
	b.WriteString("# Proposal cover sheet\n\n")
	b.WriteString("## " + o.Title + "\n\n")
	b.WriteString("**Agency:** " + dash(o.Agency) + "  \n")
	if o.AwardText != "" {
		b.WriteString("**Topic/Solicitation:** " + o.AwardText + "  \n")
	}
	if o.Closes != "" {
		b.WriteString("**Closes:** " + o.Closes + "  \n")
	}
	b.WriteString("**Prepared:** " + time.Now().Format("January 2, 2006") + "  \n\n")
	b.WriteString("---\n\n")
	if ck != nil {
		b.WriteString("**Offeror:** " + dash(ck.Entity) + "  \n")
		if ck.UEI != "" {
			b.WriteString("**UEI:** " + ck.UEI + "  \n")
		}
		if ck.CAGE != "" {
			b.WriteString("**CAGE:** " + ck.CAGE + "  \n")
		}
		if ck.Address != "" {
			b.WriteString("**Address:** " + ck.Address + "  \n")
		}
		if ck.SmallBiz {
			b.WriteString("**Business size:** Small business  \n")
		}
		if ck.Clearance != "" {
			b.WriteString("**Facility/PI clearance:** " + ck.Clearance + "  \n")
		}
		if ck.DataRights != "" {
			b.WriteString("\n**Data rights:** " + ck.DataRights + "\n")
		}
	} else {
		b.WriteString("_No Company Kit on file — fill entity, UEI, CAGE before submission._\n")
	}
	return b.String()
}

func preflightMD(o *Opportunity, checks []checkItem) string {
	var b strings.Builder
	b.WriteString("# Submission pre-flight — " + o.Title + "\n\n")
	mark := map[string]string{"ok": "[x]", "warn": "[!]", "blocker": "[ ]"}
	for _, c := range checks {
		b.WriteString(mark[c.Status] + " **" + c.Label + "** — " + c.Detail + "\n")
	}
	b.WriteString("\n_[x] ready · [!] verify · [ ] blocker. Clear every blocker before upload._\n")
	return b.String()
}
