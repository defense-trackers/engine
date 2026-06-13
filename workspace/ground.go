package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
