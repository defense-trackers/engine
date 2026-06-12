# engine — Defense Trackers

The collection engine for [defense-trackers](https://github.com/defense-trackers).
Stdlib-only Go (no third-party deps). It fetches public sources on a declared
cadence, validates and diffs them, and writes append-only, hash-chained JSON
that the public site renders.

This repo is **public** and contains no secrets — just code, source contracts,
and curated data files. The site updates itself by running this engine in CI
(see below), so there is nothing to operate.

## How the autonomy works

The site repo (`defense-trackers.github.io`) owns a scheduled workflow
(`.github/workflows/update.yml`) that:

1. checks out itself and this engine (both public),
2. builds the engine and runs `engine fetch --out .`,
3. runs `engine sentinel` (flip stale) and `engine verify` (check the chain),
4. commits the changed data and pushes — using the built-in `GITHUB_TOKEN`, so
   no deploy key or PAT is required.

GitHub-API sources authenticate with that same built-in token. The only source
that needs a human-provided secret is SAM.gov (`SAM_API_KEY`); it stays disabled
until that secret is set, and everything else runs without it.

## Layout

```
engine/
  main.go                     CLI: fetch | sentinel | verify (registers fetchers)
  internal/core/              the engine — stdlib only
    contract.go               Contract/Record/State/Event/SourceStatus + loader
    http.go                   conditional GET (etag cache) + FetchRaw (api)
    engine.go                 RunAll, Validate (gate), Diff, storage, chain, status, RSS
    ical.go                   feeds/<tracker>.ics emitter
    site.go                   sitemap.xml + all-changes firehose
  fetchers/
    pagediff/                 page-text diff (HTML lists)
    api/                      generic JSON over HTTP (GitHub, HF, FedRAMP, Fed Register, USAspending, GitLab…)
    rss/                      RSS / Atom feeds (Google News [PR], agency feeds)
    curate/                   maintained JSON files (the "moat columns")
  contracts/                  one JSON per source (~22 across 10 trackers)
  curated/                    hand-maintained data for the curate method
  .github/workflows/          secrets-canary · renewals · repair (dormant)
  SOURCES.md                  the ten-tracker data-source inventory
  RENEWALS.md                 dated human-floor reminders (e.g. SAM key rotation)
```

## Local development

```sh
go build -o bin/engine .
go test ./...
go test ./fetchers/<name> -run Golden -update   # regenerate a golden after an intended change

# run against a local checkout of the site repo (sibling dir):
GITHUB_TOKEN=$(gh auth token) ./bin/engine fetch --out ../site --contracts contracts --curated curated
./bin/engine sentinel --out ../site
./bin/engine verify   --out ../site
```

## Adding a tracker

- **Existing method:** drop a JSON in `contracts/` (and, for `curate`, a file in
  `curated/`). That's it — caching, the validation gate, diff, hash chain,
  status, RSS, and the firehose all come for free.
- **New method:** add a package under `fetchers/` implementing `core.Fetcher`,
  register it in `main.go`, then add the contract. Ship a golden test.

## Trust & integrity

Each tracker's changelog is append-only and hash-chained (`events/*.jsonl` with
`prev = sha256(previous line)`, head in `CHAIN`). Anyone can re-derive the chain
with `engine verify` — no keys needed.

A hash chain alone proves internal consistency but not that the head wasn't
rewritten by whoever controls the repo. Two independent anchors close that gap:

- **RFC 3161 trusted timestamp.** When `TSA_URL` is set (e.g. a public TSA like
  `https://freetsa.org/tsr`, no account required), each new head is timestamped by
  a third party and the token stored at `CHAIN.tsr`. `engine verify` confirms the
  token commits to the current head, so a rewritten head no longer matches its
  timestamp. Full TSA-signature validation: `openssl ts -verify -data CHAIN -in CHAIN.tsr -CAfile <tsa-ca>`.
- **Optional signature.** When `SIGNET_CMD` is set (e.g. `signet sign --key …`),
  the head is countersigned and the detached signature stored at `CHAIN.sig`.

Both are best-effort at fetch time (a missing signer or unreachable TSA never
blocks publishing) and checked by `engine verify` when present.

## What's intentionally dormant

`scripts/repair.sh` + `.github/workflows/repair.yml` sketch an LLM-assisted
self-repair loop (regenerate a broken parser → `go test` gate → open a PR) that
runs on a self-hosted runner. It only triggers on an `incident`-labeled issue
and is inert until someone wires the `RIGRUN_URL` call and registers a runner.

## License

MIT (code). Published data is CC-BY-4.0 (see the site repo).
