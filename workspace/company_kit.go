package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The Company Kit is the stable set of facts + voice every proposal reuses: the
// entity identifiers, Jesse's bio, team, past performance, data-rights language,
// and boilerplate. It's synthesized once (from the grounded dossiers and, when
// reachable, the Rafiq brain), then hand-edited — and from then on every draft is
// grounded in the same true facts instead of re-inventing them per volume.

type PastPerf struct {
	Title    string `json:"title"`
	Customer string `json:"customer,omitempty"`
	Value    string `json:"value,omitempty"`
	Year     string `json:"year,omitempty"`
	Summary  string `json:"summary"`
	Result   string `json:"result,omitempty"`
}

// ProofPoint is a citable piece of evidence — a hard claim + its metric + source —
// the firm's evidence locker that grounds every draft so claims are real, not
// invented. Tags route the most relevant proofs to a given topic/asset.
type ProofPoint struct {
	Claim  string   `json:"claim"`
	Metric string   `json:"metric,omitempty"`
	Source string   `json:"source,omitempty"`
	Tags   []string `json:"tags,omitempty"`
}

type CompanyKit struct {
	Entity      string     `json:"entity"`                 // legal/DBA name
	UEI         string     `json:"uei,omitempty"`          // SAM Unique Entity ID
	CAGE        string     `json:"cage,omitempty"`         // CAGE code
	SmallBiz    bool       `json:"small_business"`         // SBC self-cert
	Address     string     `json:"address,omitempty"`      //
	Bio         string     `json:"bio,omitempty"`          // Jesse — PI bio
	Clearance   string     `json:"clearance,omitempty"`    // e.g. TS/SCI active
	Team        []string   `json:"team,omitempty"`         // team/advisor bios
	PastPerf    []PastPerf `json:"past_performance,omitempty"`
	DataRights  string     `json:"data_rights,omitempty"`  // GPR / SBIR data rights stance
	Proof       []ProofPoint `json:"proof,omitempty"`        // evidence locker — citable claims+metrics auto-injected into drafts
	Differators []string   `json:"differentiators,omitempty"` // cross-cutting win themes
	Partners    []string   `json:"partners,omitempty"`     // teaming partners (e.g. AUS hardware build+fund partner for USV)
	Boilerplate string     `json:"boilerplate,omitempty"`  // voice/tone notes for drafting
	Updated     string     `json:"updated,omitempty"`
}

func companyKitPath(dir string) string { return filepath.Join(dir, "company-kit.json") }

// saveCompanyKit persists the kit (used by the proof-library editor).
func saveCompanyKit(dir string, ck *CompanyKit) error {
	b, _ := json.MarshalIndent(ck, "", " ")
	return os.WriteFile(companyKitPath(dir), b, 0o644)
}

// hProof lists the proof library (GET) or appends a proof point (POST). The
// library auto-grounds every draft so claims cite real evidence, never inventions.
func (s *server) hProof(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var in ProofPoint
		if json.NewDecoder(r.Body).Decode(&in) != nil || strings.TrimSpace(in.Claim) == "" {
			http.Error(w, "bad request", 400)
			return
		}
		ck := LoadCompanyKit(s.opts.Dir)
		if ck == nil {
			ck = &CompanyKit{}
		}
		ck.Proof = append(ck.Proof, in)
		if err := saveCompanyKit(s.opts.Dir, ck); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "count": len(ck.Proof)})
		return
	}
	var proof []ProofPoint
	if ck := LoadCompanyKit(s.opts.Dir); ck != nil {
		proof = ck.Proof
	}
	writeJSON(w, map[string]any{"proof": proof})
}

// LoadCompanyKit reads the local company-kit.json (nil if absent — drafting still
// works, just with less grounding).
func LoadCompanyKit(dir string) *CompanyKit {
	b, err := os.ReadFile(companyKitPath(dir))
	if err != nil {
		return nil
	}
	var ck CompanyKit
	if json.Unmarshal(b, &ck) != nil {
		return nil
	}
	return &ck
}

// BuildCompanyKit synthesizes a first-draft kit from real context — the Phase-1
// grounded dossiers plus (best-effort) the Rafiq brain on its box — and merges it
// into company-kit.json without clobbering anything Jesse already filled in. He
// edits the result; it's the stable voice/facts for every draft.
func BuildCompanyKit(dir string) error {
	if dir == "" {
		dir = `C:\trackers\workspace`
	}
	os.MkdirAll(dir, 0o755)
	existing := LoadCompanyKit(dir)

	var ctx strings.Builder
	// 1) grounded asset dossiers (local, written by grounding)
	dossierDir := filepath.Join(dir, "dossiers")
	if ents, err := os.ReadDir(dossierDir); err == nil {
		for _, e := range ents {
			if strings.HasSuffix(e.Name(), ".md") {
				if b, err := os.ReadFile(filepath.Join(dossierDir, e.Name())); err == nil {
					ctx.WriteString("\n--- DOSSIER " + e.Name() + " ---\n")
					ctx.Write(b)
				}
			}
		}
	}
	// 2) Rafiq brain (best-effort): pull any brain-generated dossier/summary text.
	if brain := pullBrainDossiers(); brain != "" {
		ctx.WriteString("\n--- RAFIQ BRAIN CONTEXT ---\n")
		ctx.WriteString(brain)
	}

	context := ctx.String()
	if len(context) > 24000 {
		context = context[:24000]
	}

	ck := &CompanyKit{}
	if context != "" && assistBackend() != "" {
		sys := "You are assembling a defense-contracting Company Kit for Jesse Morgan (a transitioning active-duty defense-tech founder; TS/SCI active). " +
			"Using ONLY the grounded context provided, produce a stable facts-and-voice kit reused across SBIR/DARPA proposals. " +
			"Do NOT fabricate identifiers (UEI/CAGE), award values, or customers you cannot support from the context — leave those blank for him to fill. " +
			"Past performance must be real and grounded (e.g. SPIRE 1st place, prior SBIR awards) — if you can't support it, omit it. " +
			"Reply with ONLY a JSON object, no prose, no fence: " +
			`{"entity":"","bio":"PI bio grounded in the assets","clearance":"TS/SCI active","team":["..."],"past_performance":[{"title":"","customer":"","value":"","year":"","summary":"","result":""}],"data_rights":"SBIR data-rights / GPR stance","differentiators":["cross-cutting win themes true across his portfolio"],"boilerplate":"voice/tone notes for drafting"}`
		raw, err := claudeOnce(sys, "GROUNDED CONTEXT:\n"+context)
		if err == nil {
			if js := extractJSON(raw); js != "" {
				_ = json.Unmarshal([]byte(js), ck)
			}
		} else {
			fmt.Fprintln(os.Stderr, "company-kit synth (using template):", err)
		}
	}

	// Seed sensible defaults so the file is useful even with no backend/context.
	if ck.Entity == "" && (existing == nil || existing.Entity == "") {
		ck.Entity = "" // Jesse fills the legal entity name
	}
	if ck.Clearance == "" {
		ck.Clearance = "TS/SCI (active)"
	}
	if ck.DataRights == "" {
		ck.DataRights = "Assert SBIR data rights on all deliverables; offer Government Purpose Rights where a transition requires it, in writing, scoped to the delivered components."
	}
	ck.SmallBiz = true
	if len(ck.Partners) == 0 {
		ck.Partners = []string{"Australian hardware partner — builds and funds hardware (esp. USV / unmanned surface vessels); Jesse leads software + design (US prime, partner as subcontractor; mind ITAR/EAR + SBIR foreign-sub limits; AUKUS Pillar II tailwind). [Fill exact entity/UEI + teaming-agreement status.]"}
	}

	merged := mergeKit(existing, ck)
	merged.Updated = nowRFC()
	b, _ := json.MarshalIndent(merged, "", " ")
	if err := os.WriteFile(companyKitPath(dir), b, 0o644); err != nil {
		return err
	}
	fmt.Printf("company-kit.json written to %s — review and fill UEI/CAGE + verify past performance.\n", companyKitPath(dir))
	return nil
}

// pullBrainDossiers best-effort reads brain-generated dossier/summary text from the
// Rafiq box. The history DB is vec0-encoded, so we use generated dossiers/summaries,
// never the raw vector tables. Silent no-op if the box is unreachable.
func pullBrainDossiers() string {
	host := "exx@100.109.172.64"
	key := os.ExpandEnv(`${HOME}/.ssh/id_ed25519`)
	// Try a few likely dossier/summary locations; concatenate whatever exists.
	remote := `cat /data/rigrun/rag/dossiers/*.md /data/rigrun/rag/summaries/*.md 2>/dev/null | head -c 20000`
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=12", "-i", key, host, remote)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return string(out)
}

// mergeKit overlays freshly-synthesized fields onto whatever Jesse already set,
// never clobbering non-empty existing values.
func mergeKit(existing, fresh *CompanyKit) *CompanyKit {
	if existing == nil {
		return fresh
	}
	out := *existing
	str := func(cur *string, neu string) {
		if strings.TrimSpace(*cur) == "" && neu != "" {
			*cur = neu
		}
	}
	str(&out.Entity, fresh.Entity)
	str(&out.UEI, fresh.UEI)
	str(&out.CAGE, fresh.CAGE)
	str(&out.Address, fresh.Address)
	str(&out.Bio, fresh.Bio)
	str(&out.Clearance, fresh.Clearance)
	str(&out.DataRights, fresh.DataRights)
	str(&out.Boilerplate, fresh.Boilerplate)
	if len(out.Team) == 0 {
		out.Team = fresh.Team
	}
	if len(out.PastPerf) == 0 {
		out.PastPerf = fresh.PastPerf
	}
	if len(out.Differators) == 0 {
		out.Differators = fresh.Differators
	}
	if len(out.Partners) == 0 {
		out.Partners = fresh.Partners
	}
	out.SmallBiz = existing.SmallBiz || fresh.SmallBiz
	return &out
}

// kitContext renders the kit as a compact grounding block for the draft prompts.
func (ck *CompanyKit) kitContext() string {
	if ck == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("COMPANY KIT (use these real facts; do not invent identifiers or past performance):\n")
	if ck.Entity != "" {
		b.WriteString("Entity: " + ck.Entity + "\n")
	}
	if ck.UEI != "" || ck.CAGE != "" {
		b.WriteString("UEI: " + dash(ck.UEI) + " · CAGE: " + dash(ck.CAGE) + "\n")
	}
	if ck.SmallBiz {
		b.WriteString("Small business: yes\n")
	}
	if ck.Clearance != "" {
		b.WriteString("PI clearance: " + ck.Clearance + "\n")
	}
	if ck.Bio != "" {
		b.WriteString("PI bio: " + ck.Bio + "\n")
	}
	for _, t := range ck.Team {
		b.WriteString("Team: " + t + "\n")
	}
	for _, pp := range ck.PastPerf {
		b.WriteString("Past perf: " + pp.Title)
		if pp.Customer != "" {
			b.WriteString(" (" + pp.Customer + ")")
		}
		if pp.Result != "" {
			b.WriteString(" — " + pp.Result)
		}
		b.WriteString(": " + pp.Summary + "\n")
	}
	if ck.DataRights != "" {
		b.WriteString("Data rights: " + ck.DataRights + "\n")
	}
	if len(ck.Proof) > 0 {
		b.WriteString("PROOF LIBRARY (cite these verbatim as evidence; NEVER invent or round metrics):\n")
		for _, p := range ck.Proof {
			line := "- " + p.Claim
			if p.Metric != "" {
				line += " — " + p.Metric
			}
			if p.Source != "" {
				line += " [" + p.Source + "]"
			}
			b.WriteString(line + "\n")
		}
	}
	if len(ck.Differators) > 0 {
		b.WriteString("Differentiators: " + strings.Join(ck.Differators, "; ") + "\n")
	}
	for _, p := range ck.Partners {
		b.WriteString("Teaming partner: " + p + "\n")
	}
	if ck.Boilerplate != "" {
		b.WriteString("Voice/tone: " + ck.Boilerplate + "\n")
	}
	return b.String()
}
