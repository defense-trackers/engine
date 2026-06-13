package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Grounding rewrites the capability profile from ground truth: Claude Code reviews
// each asset's real repo (on Jesse's subscription) and returns a structured
// dossier — what it actually is, TRL, real metrics, true discriminators, and the
// match terms that should drive fit scoring. The result enriches capabilities.json
// and writes a per-asset dossier the assistant can cite. Repos are reviewed locally
// via `claude -p` with the repo as the working directory.

type groundResult struct {
	Summary        string   `json:"summary"`
	TRL            string   `json:"trl"`
	Metrics        []string `json:"metrics"`
	Discriminators []string `json:"discriminators"`
	Terms          []string `json:"terms"`
}

const groundSystem = "You are grounding a defense-tech capability profile by reviewing a real project repo. " +
	"Read the README, docs, and key code. Be accurate and concrete — do not invent metrics or claims not supported by the repo. " +
	"Output ONLY a JSON object, no prose, no code fence: " +
	`{"summary":"one sentence on what this actually is","trl":"e.g. TRL 4 (prototype)","metrics":["real measured numbers found in the repo"],"discriminators":["what makes it win vs a generic bidder"],"terms":["lowercase keywords a topic would use that should match this asset"]}`

// claudeReview runs Claude Code against a repo (as cwd) on the subscription backend.
func claudeReview(repo string) (string, error) {
	if assistBackend() != "subscription" {
		return "", fmt.Errorf("grounding needs the Claude Code subscription backend (claude CLI)")
	}
	if fi, err := os.Stat(repo); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("repo not found: %s", repo)
	}
	prompt := groundSystem + "\n\nReview THIS repository (the current working directory): read README, docs, and primary source; ignore datasets, model weights, and vendored deps. Return only the JSON."
	cmd := exec.Command("claude", "-p", "--model", assistModel(), "--output-format", "text", prompt)
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("claude review: %w", err)
	}
	return string(out), nil
}

// Ground reviews each asset that has a repo and rewrites capabilities.json +
// workspace/dossiers/<asset>.md. `only` limits to one asset (by name) when set.
func Ground(dir, only string) error {
	if dir == "" {
		dir = `C:\trackers\workspace`
	}
	capsPath := filepath.Join(dir, "capabilities.json")
	caps, err := LoadCapabilities(capsPath)
	if err != nil {
		return fmt.Errorf("load capabilities (run the workspace once first): %w", err)
	}
	dossierDir := filepath.Join(dir, "dossiers")
	os.MkdirAll(dossierDir, 0o755)

	grounded := 0
	for i := range caps.Assets {
		a := &caps.Assets[i]
		if a.Repo == "" || (only != "" && a.Name != only) {
			continue
		}
		fmt.Printf("grounding %s from %s …\n", a.Name, a.Repo)
		raw, err := claudeReview(a.Repo)
		if err != nil {
			fmt.Printf("  skip %s: %v\n", a.Name, err)
			continue
		}
		js := extractJSON(raw)
		var g groundResult
		if js == "" || json.Unmarshal([]byte(js), &g) != nil {
			fmt.Printf("  skip %s: could not parse grounding JSON\n", a.Name)
			continue
		}
		// enrich the profile from ground truth
		if g.Summary != "" {
			a.Summary = g.Summary
		}
		if g.TRL != "" {
			a.TRL = g.TRL
		}
		a.Terms = mergeTerms(a.Terms, g.Terms)

		// write a human/assistant-readable dossier
		var d strings.Builder
		d.WriteString("# " + a.Name + " — grounded capability dossier\n\n")
		if a.TRL != "" {
			d.WriteString("**TRL:** " + a.TRL + "\n\n")
		}
		d.WriteString(g.Summary + "\n\n")
		if len(g.Metrics) > 0 {
			d.WriteString("## Real metrics\n")
			for _, m := range g.Metrics {
				d.WriteString("- " + m + "\n")
			}
			d.WriteString("\n")
		}
		if len(g.Discriminators) > 0 {
			d.WriteString("## Discriminators\n")
			for _, x := range g.Discriminators {
				d.WriteString("- " + x + "\n")
			}
			d.WriteString("\n")
		}
		d.WriteString("_Source: " + a.Repo + " (Claude Code review)_\n")
		os.WriteFile(filepath.Join(dossierDir, a.Name+".md"), []byte(d.String()), 0o644)
		grounded++
		fmt.Printf("  ✓ %s: %s\n", a.Name, truncate(g.Summary, 80))
	}

	b, _ := json.MarshalIndent(caps, "", " ")
	if err := os.WriteFile(capsPath, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("grounded %d asset(s); capabilities.json + dossiers updated\n", grounded)
	return nil
}

// --- Full-portfolio grounding over SSH (Phase 1) ---------------------------
//
// Jesse's entire body of work lives on the laptop `book` (D:\projects, ~40 real
// projects). Rather than copy repos, grounding runs ON the laptop over SSH: each
// repo is reviewed by the laptop's own `claude` CLI (his subscription, cwd=repo),
// and only the structured dossier comes back. Results upsert into the local
// capabilities.json so the scorer can match an opportunity to anything he's built.

// remoteTarget is parsed from "user@host:D:/projects".
type remoteTarget struct {
	Host string // user@host (passed straight to ssh)
	Base string // base dir holding the project repos, in the remote's native form
}

func parseRemote(remote string) (remoteTarget, error) {
	// Split off the user@host prefix; the remainder (after the FIRST colon that
	// is not part of a drive letter) is the path. We accept "user@host:D:/path".
	at := strings.Index(remote, "@")
	if at < 0 {
		return remoteTarget{}, fmt.Errorf("remote must be user@host:path, got %q", remote)
	}
	// find the colon that separates host from path: the first colon after '@'
	rest := remote[at+1:]
	c := strings.Index(rest, ":")
	if c < 0 {
		return remoteTarget{}, fmt.Errorf("remote must include :path, got %q", remote)
	}
	host := remote[:at+1+c]
	base := rest[c+1:]
	if host == "" || base == "" {
		return remoteTarget{}, fmt.Errorf("could not parse host/path from %q", remote)
	}
	return remoteTarget{Host: host, Base: toWinPath(base)}, nil
}

// toWinPath normalizes a forward-slash path to backslashes for Windows cmd.
func toWinPath(p string) string { return strings.ReplaceAll(p, "/", `\`) }

// sshRun executes one remote command (cmd.exe on the Windows laptop) and returns
// stdout. stdin, when non-empty, is forwarded — `claude -p` reads its prompt there.
func sshRun(host, remoteCmd, stdin string) (string, error) {
	args := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=20", host, remoteCmd}
	cmd := exec.Command("ssh", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		if ee, ok := err.(*exec.ExitError); ok {
			msg = strings.TrimSpace(string(ee.Stderr))
		}
		return string(out), fmt.Errorf("ssh: %v: %s", err, msg)
	}
	return string(out), nil
}

// sshListDirs lists the immediate subdirectory names of base on the remote.
func sshListDirs(host, winBase string) ([]string, error) {
	out, err := sshRun(host, `cmd /c "dir /b /ad `+winBase+`"`, "")
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(strings.TrimRight(ln, "\r"))
		if ln != "" {
			dirs = append(dirs, ln)
		}
	}
	return dirs, nil
}

// claudeReviewRemote grounds one repo on the laptop: cd into it and run claude -p,
// prompt on stdin. cwd=repo lets Claude read that project's README/docs/source.
func claudeReviewRemote(host, winBase, name string) (string, error) {
	repo := winBase + `\` + name
	prompt := groundSystem + "\n\nReview THIS repository (the current working directory): read README, docs, and primary source; ignore datasets, model weights, and vendored deps. Return only the JSON."
	remoteCmd := fmt.Sprintf(`cmd /c "cd /d %s && claude -p --model %s --output-format text"`, repo, assistModel())
	return sshRun(host, remoteCmd, prompt)
}

// repo names that are upstream forks/frameworks or non-asset scaffolding — never
// Jesse's own capability. Excluded from grounding.
var hardExcludeRepos = map[string]bool{
	"ardupilot": true, "flutter": true, "stable-diffusion-webui": true,
	"node_modules": true, "backups": true, "backup": true, ".git": true,
	"venv": true, ".venv": true, "vendor": true, "tmp": true, "temp": true,
	"claude-backup": true, "downloads": true,
}

// keepRepo decides whether a directory name is one of Jesse's biddable assets.
func keepRepo(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" || strings.HasPrefix(n, ".") || hardExcludeRepos[n] {
		return false
	}
	// review / PR / test / backup working copies
	for _, suf := range []string{"-review", "-pr", "-test", "-tests", "-backup", "-bak", "-old", "-copy"} {
		if strings.HasSuffix(n, suf) {
			return false
		}
	}
	if strings.Contains(n, "-pr-") || strings.Contains(n, "-review-") {
		return false
	}
	return true
}

// dedupeNear collapses near-duplicate variants of the same project (keep the first
// seen, which—after sorting—is the canonical base name). e.g. aisovereign /
// aisovereign-v2, mutawazin / mutawazin-app / mutawazin-legacy.
func dedupeNear(names []string) []string {
	norm := func(s string) string {
		s = strings.ToLower(s)
		s = strings.ReplaceAll(s, "_", "-")
		for _, suf := range []string{"-v2", "-v3", "-app", "-legacy", "-new", "-2", "-mobile", "-desktop", "-web"} {
			s = strings.TrimSuffix(s, suf)
		}
		return s
	}
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		k := norm(n)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, n)
	}
	return out
}

// domainKeywords maps grounded terms to Jesse's bidding domains so the scorer keeps
// a coarse domain signal even when term overlap is thin.
var domainKeywords = map[string][]string{
	"counter-uas":           {"drone", "uas", "counter-uas", "interceptor", "rf", "detection", "tracking"},
	"thermal/eo-ir":         {"thermal", "infrared", "eo/ir", "eo-ir", "fcos", "convnext", "object detection"},
	"autonomy":              {"autonomy", "autonomous", "navigation", "slam", "guidance", "swarm"},
	"ai/ml":                 {"llm", "model", "inference", "training", "rag", "embedding", "neural", "ml", "ai"},
	"resilient-comms":       {"mesh", "tak", "atak", "comms", "radio", "tactical", "edge", "offline"},
	"cyber/governance":      {"signet", "audit", "rmf", "ato", "nist", "compliance", "security", "pentest", "red team"},
	"c2/isr":                {"c2", "isr", "command", "control", "situational", "sensor fusion"},
	"logistics":             {"logistics", "supply", "sustainment", "maintenance", "readiness"},
}

func inferDomains(terms []string) []string {
	hay := " " + strings.ToLower(strings.Join(terms, " ")) + " "
	var got []string
	for dom, kws := range domainKeywords {
		for _, kw := range kws {
			if strings.Contains(hay, " "+kw) || strings.Contains(hay, kw+" ") {
				got = append(got, dom)
				break
			}
		}
	}
	sort.Strings(got)
	return got
}

// GroundRemote enumerates Jesse's portfolio on the laptop, grounds each repo with
// the laptop's own Claude over SSH, and upserts the results into capabilities.json
// + dossiers. Resumable: assets already grounded (with a summary) are skipped
// unless reground is set. only limits to one repo by name.
func GroundRemote(dir, remote, only string, reground bool) error {
	if dir == "" {
		dir = `C:\trackers\workspace`
	}
	tgt, err := parseRemote(remote)
	if err != nil {
		return err
	}
	capsPath := filepath.Join(dir, "capabilities.json")
	caps, err := LoadCapabilities(capsPath)
	if err != nil {
		// no profile yet — start from the embedded example so local assets survive
		var c Capabilities
		if json.Unmarshal(exampleCaps, &c) == nil {
			caps = &c
		} else {
			caps = &Capabilities{}
		}
	}
	dossierDir := filepath.Join(dir, "dossiers")
	os.MkdirAll(dossierDir, 0o755)

	fmt.Printf("enumerating %s on %s …\n", tgt.Base, tgt.Host)
	dirs, err := sshListDirs(tgt.Host, tgt.Base)
	if err != nil {
		return fmt.Errorf("list remote dirs (is the laptop online + ssh reachable?): %w", err)
	}
	sort.Strings(dirs)
	var kept []string
	for _, d := range dirs {
		if keepRepo(d) {
			kept = append(kept, d)
		}
	}
	kept = dedupeNear(kept)
	fmt.Printf("found %d dirs → %d biddable repos after filtering\n", len(dirs), len(kept))

	// index existing assets by lowercased name
	idx := map[string]int{}
	for i := range caps.Assets {
		idx[strings.ToLower(caps.Assets[i].Name)] = i
	}

	grounded, skipped := 0, 0
	for _, name := range kept {
		if only != "" && !strings.EqualFold(name, only) {
			continue
		}
		key := strings.ToLower(name)
		if i, ok := idx[key]; ok && !reground {
			// already in the profile (e.g. locally-grounded rigrun/thermalhawk/auspex,
			// or a prior remote run) — leave it as-is.
			if caps.Assets[i].Summary != "" {
				skipped++
				continue
			}
		}
		fmt.Printf("grounding %s …\n", name)
		raw, err := claudeReviewRemote(tgt.Host, tgt.Base, name)
		if err != nil {
			fmt.Printf("  skip %s: %v\n", name, err)
			continue
		}
		js := extractJSON(raw)
		var g groundResult
		if js == "" || json.Unmarshal([]byte(js), &g) != nil {
			fmt.Printf("  skip %s: could not parse grounding JSON\n", name)
			continue
		}
		if g.Summary == "" {
			fmt.Printf("  skip %s: empty grounding\n", name)
			continue
		}
		// upsert
		var a *Asset
		if i, ok := idx[key]; ok {
			a = &caps.Assets[i]
		} else {
			caps.Assets = append(caps.Assets, Asset{Name: key})
			idx[key] = len(caps.Assets) - 1
			a = &caps.Assets[len(caps.Assets)-1]
		}
		a.Summary = g.Summary
		if g.TRL != "" {
			a.TRL = g.TRL
		}
		a.Terms = mergeTerms(a.Terms, g.Terms)
		if doms := inferDomains(a.Terms); len(doms) > 0 {
			a.Domains = mergeTerms(a.Domains, doms)
		}
		if a.Repo == "" {
			a.Repo = tgt.Host + ":" + tgt.Base + `\` + name // remote marker
		}
		writeDossier(dossierDir, a, &g)
		grounded++
		fmt.Printf("  ✓ %s: %s\n", name, truncate(g.Summary, 80))
	}

	b, _ := json.MarshalIndent(caps, "", " ")
	if err := os.WriteFile(capsPath, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("done: grounded %d, skipped %d already-known; %d total assets in capabilities.json\n",
		grounded, skipped, len(caps.Assets))
	return nil
}

// writeDossier renders a grounded asset's human/assistant-readable dossier.
func writeDossier(dossierDir string, a *Asset, g *groundResult) {
	var d strings.Builder
	d.WriteString("# " + a.Name + " — grounded capability dossier\n\n")
	if a.TRL != "" {
		d.WriteString("**TRL:** " + a.TRL + "\n\n")
	}
	d.WriteString(g.Summary + "\n\n")
	if len(a.Domains) > 0 {
		d.WriteString("**Domains:** " + strings.Join(a.Domains, ", ") + "\n\n")
	}
	if len(g.Metrics) > 0 {
		d.WriteString("## Real metrics\n")
		for _, m := range g.Metrics {
			d.WriteString("- " + m + "\n")
		}
		d.WriteString("\n")
	}
	if len(g.Discriminators) > 0 {
		d.WriteString("## Discriminators\n")
		for _, x := range g.Discriminators {
			d.WriteString("- " + x + "\n")
		}
		d.WriteString("\n")
	}
	d.WriteString("_Source: " + a.Repo + " (Claude Code review)_\n")
	os.WriteFile(filepath.Join(dossierDir, a.Name+".md"), []byte(d.String()), 0o644)
}

func mergeTerms(existing, add []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range append(existing, add...) {
		k := strings.ToLower(strings.TrimSpace(t))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
