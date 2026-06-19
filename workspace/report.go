package workspace

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Portfolio report — a one-click board/investor brief of the whole pipeline:
// the headline numbers, top pursuits, upcoming closes, wins, and team load.
// Reuses the same scored rows the War Room runs on, rendered to markdown and the
// stdlib .docx writer. This is what you hand a partner or put in a raise deck.

func reportMoney(k int) string {
	if k >= 1000 {
		return fmt.Sprintf("$%.1fM", float64(k)/1000)
	}
	return fmt.Sprintf("$%dK", k)
}

func (s *server) buildReportMD() string {
	rows := s.strategizeRows()
	ck := LoadCompanyKit(s.opts.Dir)
	target := s.loadTargetK()

	pursuits := len(rows)
	realized, pipeline, ev, won, lost := 0, 0, 0, 0, 0
	byOwner := map[string]int{} // owner -> expected award value
	var wins, closes []stratRow
	for _, r := range rows {
		ev += r.EV
		switch outcomeForStage(r.Stage) {
		case "won":
			realized += r.Value
			won++
			wins = append(wins, r)
		case "lost":
			lost++
		default:
			pipeline += r.Priority
			o := r.Owner
			if o == "" {
				o = "Unassigned"
			}
			byOwner[o] += r.Priority
		}
		if r.DaysLeft >= 0 && r.DaysLeft <= 30 {
			closes = append(closes, r)
		}
	}
	sort.SliceStable(closes, func(i, j int) bool { return closes[i].DaysLeft < closes[j].DaysLeft })

	var b strings.Builder
	entity := "Realizer"
	if ck != nil && ck.Entity != "" {
		entity = ck.Entity
	}
	b.WriteString("# " + entity + " — defense pipeline brief\n\n")
	b.WriteString("_Generated " + time.Now().Format("January 2, 2006") + " · private · for partner/board review_\n\n")

	b.WriteString("## Headline\n\n")
	b.WriteString(fmt.Sprintf("- **%d active pursuits** in the pipeline.\n", pursuits))
	b.WriteString(fmt.Sprintf("- **%s expected award value** in flight (Σ win-probability × value).\n", reportMoney(pipeline)))
	b.WriteString(fmt.Sprintf("- **%s risk-adjusted expected revenue** to program of record (the brutal SBIR→PoR funnel applied).\n", reportMoney(ev)))
	if won+lost > 0 {
		b.WriteString(fmt.Sprintf("- **%d won / %d lost** decided · **%s realized** award value.\n", won, lost, reportMoney(realized)))
	}
	if target > 0 {
		proj := realized + pipeline
		b.WriteString(fmt.Sprintf("- Target **%s** — projected **%s** (%d%%).\n", reportMoney(target), reportMoney(proj), proj*100/target))
	}
	b.WriteString("\n")

	b.WriteString("## Top pursuits by expected award value\n\n")
	top := rows
	if len(top) > 10 {
		top = top[:10]
	}
	for i, r := range top {
		dl := ""
		if r.DaysLeft >= 0 {
			dl = fmt.Sprintf(", closes in %dd", r.DaysLeft)
		}
		b.WriteString(fmt.Sprintf("%d. **%s** [%s] — win %d%%, %s expected, %s lifetime%s%s\n",
			i+1, r.Title, r.Stage, r.WinProb, reportMoney(r.Priority), reportMoney(r.Value), dl,
			ifs(r.Owner != "", " · "+r.Owner, "")))
	}
	b.WriteString("\n")

	if len(closes) > 0 {
		b.WriteString("## Closing within 30 days\n\n")
		for _, r := range closes {
			b.WriteString(fmt.Sprintf("- **%s** — closes %s (%dd), win %d%%\n", r.Title, dash(r.Closes), r.DaysLeft, r.WinProb))
		}
		b.WriteString("\n")
	}

	if len(wins) > 0 {
		b.WriteString("## Wins\n\n")
		for _, r := range wins {
			b.WriteString(fmt.Sprintf("- **%s** — %s\n", r.Title, reportMoney(r.Value)))
		}
		b.WriteString("\n")
	}

	if len(byOwner) > 0 {
		type ow struct {
			Name string
			Exp  int
		}
		var owners []ow
		for n, e := range byOwner {
			owners = append(owners, ow{n, e})
		}
		sort.SliceStable(owners, func(i, j int) bool { return owners[i].Exp > owners[j].Exp })
		b.WriteString("## Team load (expected award value carried)\n\n")
		for _, o := range owners {
			b.WriteString(fmt.Sprintf("- **%s** — %s\n", o.Name, reportMoney(o.Exp)))
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n_Numbers are model estimates from live solicitation data and a documented win-probability heuristic; not financial guidance._\n")
	return b.String()
}

// hReport serves the portfolio brief as .docx (default) or markdown (?format=md).
func (s *server) hReport(w http.ResponseWriter, r *http.Request) {
	md := s.buildReportMD()
	if r.URL.Query().Get("format") == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Write([]byte(md))
		return
	}
	doc, err := buildDocx(md)
	if err != nil {
		http.Error(w, "docx build: "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", `attachment; filename="realizer-pipeline-brief.docx"`)
	w.Write(doc)
}
