# engine — Defense Trackers

The collection engine for [defense-trackers](https://github.com/defense-trackers).
Stdlib-only Go. It fetches public sources on a declared cadence, validates and
diffs them, and publishes append-only, hash-chained JSON to the public site repo.

**This repo is private.** It holds the fetchers, secrets, and the self-hosted
runner. The public site repo (`defense-trackers.github.io`) only ever renders
the JSON this engine pushes to it. That split is the security model: fork PRs
can never run code on the runner because the public repo runs nothing but Pages.

## Layout

```
engine/
  main.go                     CLI: fetch | sentinel | verify (registers fetchers)
  internal/core/              the engine — stdlib only, the part that must not rot
    contract.go               Contract/Record/State/Event/SourceStatus + loader
    http.go                   conditional GET with etag cache + retries/backoff
    engine.go                 RunAll, Validate (the gate), Diff, storage, chain, status, RSS
    engine_test.go            diff + invariant unit tests
  fetchers/
    pagediff/                 generic page-text diff fetcher (the shipped method)
      pagediff.go
      pagediff_test.go        golden test (deterministic parser)
      testdata/               synthetic fixture + generated golden
  contracts/                  one JSON per source (data, not code)
    blue-uas.json             T6 — live (pagediff)
    sbir-pipeline.json        T1 — stubbed until an `api` fetcher exists
  scripts/repair.sh           RigRun self-repair bundle (STUB — wire to your endpoint)
  .github/workflows/          fetch · sentinel · repair · secrets-canary · renewals
  SOURCES.md                  the ten-tracker data-source inventory (build reference)
  RENEWALS.md                 the dated human floor (key rotations, etc.)
```

## Bring-up (one-time)

1. **Create the two repos in the org:**
   - `defense-trackers/defense-trackers.github.io` (public) — push the `site/` tree.
   - `defense-trackers/engine` (private) — push this tree.
2. **Public repo → Settings → Pages:** deploy from `main` / root. URL becomes
   `https://defense-trackers.github.io/`.
3. **Deploy key:** generate an SSH keypair. Add the **public** key to the public
   repo as a deploy key *with write access*. Add the **private** key to the engine
   repo as the `SITE_DEPLOY_KEY` secret. (This is how CI pushes data without a PAT.)
4. **Self-hosted runner (the RigRun box):** engine repo → Settings → Actions →
   Runners → add, with labels `self-hosted` and `rigrun`. Set the `RIGRUN_URL`
   secret. Only the `repair` workflow targets it.
5. **Other secrets (as you enable sources):** `SAM_API_KEY` (entity-tier, see
   SOURCES.md), `HEARTBEAT_URL` (a healthchecks.io check — the external dead-man
   switch).
6. **Labels:** create `incident`, `stale`, `secrets`, `renewal`, `repair` so the
   workflows' `gh issue create` calls land cleanly.
7. **Push.** The `fetch` workflow runs on its cron (and `workflow_dispatch`),
   builds, fetches into a checkout of the public repo, and pushes the data.

## Local development

```sh
go build -o bin/engine .                                   # compile
go test ./...                                              # unit + golden
go test ./fetchers/pagediff -run Golden -update            # regenerate golden after an intended parser/fixture change

# run against a local checkout of the public site repo (sibling dir):
./bin/engine fetch    --out ../site
./bin/engine sentinel --out ../site
./bin/engine verify   --out ../site
```

(The `Makefile` wraps these. On Windows without `make`, run the `go` commands directly.)

## Adding a tracker

- **Same method as an existing source:** drop a new JSON in `contracts/`. Done.
- **New method** (api, rss, gitlab, ical — see SOURCES.md): add a package under
  `fetchers/` implementing `core.Fetcher`, register it in `main.go`, then add the
  contract. It inherits caching, the validation gate, diff, chain, status, and RSS
  for free. Ship a golden test for any parser with non-trivial logic.

## Operational model

| Workflow | Trigger | Does |
|---|---|---|
| `fetch` | cron + dispatch | fetch all enabled sources → push data → open `incident` if any degraded |
| `sentinel` | daily | flip sources stale past 1.5× cadence → ping `HEARTBEAT_URL` → open `stale` issue |
| `repair` | issue labeled `incident` | (self-hosted) RigRun regenerates the parser → `go test` gate → open PR (human merges) |
| `secrets-canary` | weekly | exercise `SAM_API_KEY` → open `secrets` issue on 401/403 |
| `renewals` | daily | open an issue 14 days before each dated line in `RENEWALS.md` |

**Exit codes:** `fetch` returns 2 if any source degraded/quarantined (data for
healthy sources still publishes); `sentinel` returns 3 if anything went stale;
`verify` returns 4 on a broken chain. Workflows key off these.

## What's stubbed

`scripts/repair.sh` assembles the quarantine bundle and the signaling, but the
actual RigRun call is marked `WIRE ME`. Until it's wired, an `incident` produces
a no-op repair run; the incident issue still tells you what to fix by hand. Code
never self-merges regardless — every repair is gated by `go test` and a human PR.

## License

MIT (code). Published data is CC-BY-4.0 (see the site repo).
