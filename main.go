// Command engine runs the tracker pipeline. Three subcommands, stdlib only:
//
//	engine fetch     run enabled contracts; publish current.json + events + RSS + status
//	engine sentinel  flip sources stale past 1.5x cadence (the dead-man check)
//	engine verify    re-derive and check the append-only hash chain
//
// Fetchers are registered here explicitly — no init() magic — so the set of
// behaviors is greppable in one place.
package main

import (
	"flag"
	"fmt"
	"os"

	"engine/fetchers/api"
	"engine/fetchers/curate"
	"engine/fetchers/pagediff"
	"engine/fetchers/rss"
	"engine/internal/core"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(64)
	}
	switch os.Args[1] {
	case "fetch":
		os.Exit(cmdFetch(os.Args[2:]))
	case "sentinel":
		os.Exit(cmdSentinel(os.Args[2:]))
	case "verify":
		os.Exit(cmdVerify(os.Args[2:]))
	default:
		usage()
		os.Exit(64)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `engine <command> [flags]

  fetch      run enabled contracts; publish state, events, RSS, status
  sentinel   mark sources stale once they pass 1.5x their cadence
  verify     re-derive the event hash chain and report tampering

Run "engine <command> -h" for flags.`)
}

func cmdFetch(args []string) int {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	contracts := fs.String("contracts", "contracts", "directory of contract JSON files")
	curated := fs.String("curated", "curated", "directory of curated JSON files (curate method)")
	out := fs.String("out", "../site", "output dir = public site repo root")
	cache := fs.String("cache", "cache", "etag/body cache dir (gitignored)")
	quarantine := fs.String("quarantine", "quarantine", "quarantine dir for failed batches (gitignored)")
	only := fs.String("only", "", "run only this source id")
	_ = fs.Parse(args)

	// Register fetchers explicitly — the full set of behaviors in one place.
	core.Register("pagediff", pagediff.New())
	core.Register("api", api.New())
	core.Register("rss", rss.New())
	core.Register("curate", curate.New(*curated))

	cs, err := core.LoadContracts(*contracts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load contracts:", err)
		return 1
	}
	results := core.RunAll(cs, *out, *cache, *quarantine, *only)
	if len(results) == 0 {
		fmt.Println("  (no enabled contracts matched)")
		return 0
	}
	bad := 0
	for _, r := range results {
		if r.State == "ok" {
			fmt.Printf("  ok         %-22s +%d -%d ~%d\n", r.Source, r.Added, r.Removed, r.Changed)
			continue
		}
		bad++
		fmt.Printf("  %-10s %-22s %v\n", r.State, r.Source, r.Err)
	}
	// Site-wide artifacts: discovery + a single firehose feed.
	if err := core.WriteSitemap(*out); err != nil {
		fmt.Fprintln(os.Stderr, "sitemap:", err)
	}
	if err := core.WriteFirehose(*out); err != nil {
		fmt.Fprintln(os.Stderr, "firehose:", err)
	}
	if bad > 0 {
		fmt.Printf("\n%d/%d source(s) degraded or quarantined — last-good data left live.\n", bad, len(results))
		return 2 // workflow opens an incident; data still published for healthy sources
	}
	return 0
}

func cmdSentinel(args []string) int {
	fs := flag.NewFlagSet("sentinel", flag.ExitOnError)
	out := fs.String("out", "../site", "output dir = public site repo root")
	_ = fs.Parse(args)

	stale, err := core.Sentinel(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sentinel:", err)
		return 1
	}
	if len(stale) == 0 {
		fmt.Println("sentinel: all sources within cadence")
		return 0
	}
	for _, s := range stale {
		fmt.Println("  stale:", s)
	}
	return 3 // workflow opens a stale issue
}

func cmdVerify(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	out := fs.String("out", "../site", "output dir = public site repo root")
	tracker := fs.String("tracker", "", "verify one tracker (default: all)")
	_ = fs.Parse(args)

	bad, err := core.VerifyChain(*out, *tracker)
	if err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		return 1
	}
	if len(bad) == 0 {
		fmt.Println("verify: chain intact")
		return 0
	}
	for _, t := range bad {
		fmt.Println("  BROKEN chain:", t)
	}
	return 4
}
