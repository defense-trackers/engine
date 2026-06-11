# SOURCES.md — Data Source Inventory for the Tracker Suite
**Researched 2026-06-10. Verify endpoints on first fetch; .gov sites move things without redirects.**

Legend: `[API]` documented API · `[JSON]` machine-readable file · `[SCRAPE]` HTML parse · `[XHR]` undocumented JSON behind an SPA (inspect network tab) · `[PR]` press-release monitoring · `[CURATE]` manual entry, your expertise is the source

---

## T1 — Defense AI/Autonomy Opportunity Pipeline

| Source | Method | Notes |
|---|---|---|
| SAM.gov Get Opportunities | `[API]` `https://api.sam.gov/prod/opportunities/v2/search` | Free key from Account Details page. **10 req/day public; 1,000/day for registered entities — request the key under the org's SAM registration.** Filter `ptype`, keyword `title`. Active notices update daily. Docs: open.gsa.gov/api/get-opportunities-public-api/ |
| SBIR.gov Topics | `[API]` entry: sbir.gov/data-resources | Topic API covers Open/Future/Closed across all agencies; CSV/JSON/XML bulk downloads. Topic page caps 10K records per pull. |
| DSIP (DoD SBIR/STTR) | `[XHR]` dodsbirsttr.mil/topics-app/ | Angular SPA — the app calls an internal JSON API. Open dev tools, capture the topics XHR, replay it. No public docs; treat as fragile, snapshot responses. |
| DIU open solicitations | `[SCRAPE]` diu.mil/work-with-us/open-solicitations | AoIs posted on rolling basis, "open for a limited period." No API, no RSS. Diff the page; this is your highest-value scrape. |
| DARPA SBIR/STTR + BAAs | `[SCRAPE]` darpa.mil/work-with-us/communities/small-business/sbir-sttr-topics | Clean structured listings (topic #, pre-release/open/close dates). Also mirrored on SAM.gov. |
| Army xTech | `[SCRAPE]` xtech.army.mil | Competition pages with phase deadlines. |
| AFWERX | `[SCRAPE]` afwerx.com | Challenges + STRATFI/TACFI windows. |
| Grants.gov | `[API]` Search2 API, grants.gov | Only for grant-type SBIRs (~half are contracts via SAM). Lower priority. |
| OTA consortia roster | `[SCRAPE]` aida.mitre.org/ota/existing-ota-consortia/ | MITRE-maintained canonical list (statutory under Sec 833, PL 116-283). Use as the *roster*; then scrape each consortium's public opportunities page (NSTXL, ATI, CMG). **SOSSEC and several others gate opportunities behind membership — list the consortium, link the portal, don't pretend to have the data.** |

**Normalization schema:** `{id, mechanism (CSO|SBIR|STTR|BAA|OTA|Challenge), agency, title, tech_tags[], posted, opens, closes, url, status, first_seen, last_seen}` — `first_seen/last_seen` from git history gives you the change feed free.

---

## T2 — DoD AI Policy & Taskings Tracker

| Source | Method | Notes |
|---|---|---|
| Federal Register | `[API]` `https://www.federalregister.gov/api/v1/documents.json` | Free, no key, excellent. Filter `type=PRESDOCU` + term "artificial intelligence" for EOs; also catches rules/notices. Backbone source. |
| DoD Issuances (DoDD/DoDI/DTM) | `[SCRAPE]` esd.whs.mil/DD/ | Tables of all directives/instructions with publish + change dates, PDF links. Diff the tables; "recently published" view exists. |
| CDAO | `[SCRAPE]` ai.mil (Latest + Resources pages) | Memos and toolkits land here. PDFs on ai.mil/Portals/. |
| SecDef / DepSecDef memos | `[SCRAPE+PR]` defense.gov/News/Releases (has RSS) + media.defense.gov PDF URLs | Jan 12 2026 AI Strategy memo pattern: announced via release, PDF on media.defense.gov. |
| Congress (NDAA AI provisions) | `[API]` api.congress.gov | Key via api.data.gov. Optional statutory layer. |

**The killer feature is extraction, not collection:** run each memo PDF through RigRun to pull `{tasking, owner, due_date, source_para}`, verify by hand, render countdown clocks.

---

## T3 — AI Authorization Tracker (FedRAMP / DoD IL)

| Source | Method | Notes |
|---|---|---|
| FedRAMP Marketplace data | `[JSON]` github.com/GSA/marketplace-fedramp-gov-data | The repo that feeds marketplace.fedramp.gov. **FedRAMP org also stands up a new repo for "Auth Log 2.0" + status-changes JSON — watch github.com/FedRAMP.** Diff = authorization change feed, free. |
| DISA Cloud Service Catalog (IL listings) | CAC-gated | Not publicly fetchable. Design around it: IL data from the next two rows, with a `verification` field (`vendor-claimed` / `press-verified` / `corrected-by-vendor`). |
| Vendor IL ATO announcements | `[PR]` Google News RSS | Every IL4/IL5/IL6 ATO gets a release. Queries: "provisional authorization DISA", "Impact Level 5", "achieves IL4". |
| Tradewinds awardable | `[PR]` | List is behind a gov-account login — but every vendor announces "Awardable". RSS: `"Tradewinds Solutions Marketplace" "awardable"`. |
| Platform One / Iron Bank | `[SCRAPE/API]` repo1.dso.mil (GitLab public projects API) + ironbank.dso.mil | Hardened-container list partially public via Repo1's GitLab API. |

The corrections workflow ("vendor: fix your row") is the monetization seed — every correction email is a warm contact.

---

## T4 — "What Can I Actually Use" NIPR AI Matrix

No APIs — pure curation + monitoring, which is why nobody's built it and why it'll be loved.

| Source | Method | Notes |
|---|---|---|
| GenAI.mil | `[CURATE]` | DoD enterprise suite, launched Dec 9 2025; ChatGPT added Feb 9 2026. CAC-gated; track via announcements. |
| MARADMINs | `[SCRAPE]` marines.mil/News/Messages | Structured message archive. The AI-tool MARADMINs are the seed rows. |
| Army | `[SCRAPE]` army.mil news + PEO announcements | CamoGPT now R&D (NIPR + SIPR); Army Enterprise LLM Workspace (Ask Sage; FedRAMP High) is the enterprise offering. |
| Air Force | `[SCRAPE]` af.mil | NIPRGPT decommissioned Dec 31 2025 → GenAI.mil. |
| Navy/USCG | `[SCRAPE]` NAVADMIN archive (mynavyhr) / uscg.mil | USCG: Ask Hamilton + GenAI.mil. |
| Trade press | `[RSS]` DefenseScoop, Air & Space Forces Mag, Breaking Defense | Earliest signal for availability changes. |

**Schema:** `{tool, owner, network (NIPR/SIPR), data_ceiling (PUBLIC/CUI/S/TS), who_can_use (by service), status (live/pilot/blocked/sunset), source_url, as_of}`. The `as_of` per row is the trust feature.

---

## T5 — TAK Ecosystem Tracker

| Source | Method | Notes |
|---|---|---|
| GitHub | `[API]` api.github.com | Crawl: deptofdefense/AndroidTacticalAssaultKit-CIV, FreeTAKTeam org, topic:atak-plugin, plus seeds from 9M2PJU/ATAK-Civ-Plugins and FreeTAKTeam/openTAKpickList. Liveness = last commit, open issues, releases. 5K req/hr with a token. |
| tak.gov | `[SCRAPE, partial]` | Downloads need an account; listing metadata partially public. Capture names/versions, link in, don't mirror. |
| Google Play | `[SCRAPE]` ATAK-CIV + plugin listings | Version + update date per listing. No public API — scrape politely; flag ToS risk in README. |
| civtak.org + Reddit r/ATAK | `[CURATE]` | Discovery layer for plugins that never hit GitHub. |

Differentiator column: **SDK version compatibility** (which plugins build against 5.x). Curated — exactly what the working group complains about.

---

## T6 — Blue UAS / NDAA-Compliance Change Log

| Source | Method | Notes |
|---|---|---|
| Blue UAS lists | `[SCRAPE+DIFF]` **diu.mil/blue-uas** (canonical per DIU) | Three lists, snapshot separately: **Cleared List**, **Select List** (DIU-granted ATO), **Framework** (components/software). Governance shifted to DCMA-overseen two-tier mid-2025 — also watch dcma.mil. |
| Green UAS | `[SCRAPE]` AUVSI Green UAS certified list | Green-certified auto-adds to Blue Cleared — AUVSI's list is an *upstream feed* of Blue changes. |
| DIU announcements | `[SCRAPE]` diu.mil/latest | Refresh cohorts, Recognized Assessor news. |
| Statutes | static refs | FY20 NDAA §848, FY23 NDAA §817, American Security Drone Act 2024, EO 14307. Link, don't track. |
| COTS parts matrix | `[CURATE]` | Your column: common builder components (Matek, Holybro, CubePilot, ARK, ModalAI, Mobilicom...) × Framework status × country-of-origin. FCC ID database (fcc.gov OET) for radio origin. The matrix nobody else can write credibly. |

---

## T7 — Open-Weight Model Ops Tracker

| Source | Method | Notes |
|---|---|---|
| Hugging Face Hub | `[API]` `https://huggingface.co/api/models?sort=createdAt&direction=-1` | Free, no key for public metadata. License + gated status in cardData; filter by downloads to cut noise. |
| Inference stacks | `[API]` GitHub Releases for sgl-project/sglang, vllm-project/vllm, ggml-org/llama.cpp, ollama/ollama | Day-0 support = model name in release notes. Simple grep, reliable. |
| VRAM fit | computed | `params × bytes_per_param(quant) × 1.1 + KV(ctx)` per GPU profile (24/32/48/96GB). Formula on the methodology page. |
| Gov-deployability column | `[CURATE]` | License terms permitting gov/air-gapped use, provenance flags. Your moat column. |

---

## T8 — DoD Open-Source Software Index

| Source | Method | Notes |
|---|---|---|
| GitHub orgs | `[API]` | Crawl: deptofdefense, ngageoint, nsacyber, USArmyResearchLab, NavalResearchLaboratory, AFRL, cisagov, erdc, darpa-i2o. Health score per repo (commit recency, issue response, release cadence). |
| Repo1 (Platform One) | `[API]` repo1.dso.mil GitLab public-projects API | The non-GitHub half of DoD OSS. |
| code.json convention | `[JSON, decaying]` `https://<agency>.gov/code.json` | **Code.gov deprecated** but some agency code.json persist (DHS, DOE). Use opportunistically; GitHub crawl is primary. |

---

## T9 — Prototype→Production Transition Scoreboard

| Source | Method | Notes |
|---|---|---|
| USAspending | `[API]` `https://api.usaspending.gov/api/v2/` | Free, no key. Award search by recipient over time = the transition signal (prototype OT → production contract). |
| FPDS | `[API]` ATOM feeds, fpds.gov | Contract-action detail; clunky but complete. |
| defense.gov contracts | `[SCRAPE]` defense.gov/News/Contracts/ | Daily 5 PM ET, all awards ≥$7.5M. RigRun extraction → rows. |
| DIU | `[SCRAPE/PDF]` diu.mil/latest + annual reports | Success Memo + transition announcements. |
| SBIR.gov awards | `[JSON]` bulk download (~290MB w/ abstracts) | Phase II → Phase III linkage attempts. |
| GAO reports | `[PDF]` gao.gov | Methodology backing (GAO flagged DoD doesn't track consortium award flow). |

Build the methodology page first; pitch to a defense-reform funder before heavy collection.

---

## T10 — Defense Innovation Deadlines Calendar

| Source | Method | Notes |
|---|---|---|
| ~~challenge.gov~~ | **SUNSET March 2026** | Content dispersed to USA.gov innovation section + agency sites. Do not build on it. |
| xTech / AFWERX / DIU | `[SCRAPE]` | Reuse T1 fetchers; deadlines already in the pipeline schema. |
| NDIA / AFCEA / SOF Week / MDM | `[SCRAPE]` ndia.org, afcea.org event pages | Conference + CFP dates. |
| Output | iCal feed | One .ics is the whole product. |

---

## Cross-cutting build notes

- **Politeness:** real User-Agent + contact URL, honor robots.txt, etag/If-Modified-Since on every fetch, cache raw responses in `cache/` (gitignored), commit only normalized JSON.
- **Rate limits that matter:** SAM.gov key tier (get the entity-tier key first, it can lag), GitHub 5K/hr authenticated, HF generous, everything .gov-scraped at ≤1 req/sec (`rate_limit_ms` in the contract).
- **Fragility ranking (build retries/alerts accordingly):** DSIP XHR > tak.gov > Play Store > diu.mil pages > everything with a real API.
- **Diff engine before features:** every tracker's product is its changelog. Snapshot → normalize → diff → RSS. Built once in `internal/core/`.
- **OPSEC line:** solicitations, policies, authorizations, public lists. No incidents, no units, no capability-gap aggregation.
- **Monitoring pattern for all `[PR]` sources:** Google News RSS `news.google.com/rss/search?q=<query>&hl=en-US&gl=US&ceid=US:en` — one fetcher, N queries.

---

## Mapping sources to fetcher methods (engine)

The engine ships `pagediff` (generic page-text diff). The remaining methods are
the build-out work — each is a new package under `fetchers/` implementing
`core.Fetcher`, registered in `main.go`:

| Method | Covers | Status |
|---|---|---|
| `pagediff` | DIU pages, esd.whs.mil tables, xTech/AFWERX, AUVSI, defense.gov | ✅ shipped |
| `api` | SAM.gov, SBIR.gov, Federal Register, USAspending, HF Hub, GitHub | ⬜ next (T1, T7, T8, T9) |
| `rss` | defense.gov releases, trade press, Google News `[PR]` queries | ⬜ (T2, T3, T4) |
| `gitlab` | repo1.dso.mil public projects | ⬜ (T3, T8) |
| `ical` | T10 output (emit, not ingest) | ⬜ (T10) |

Each new method reuses `core.FetchURL` (caching/retries), the validation gate,
diff, chain, status, and RSS for free. Adding a tracker = one contract JSON +
(if its method is new) one fetcher package.
