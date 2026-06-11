# Renewals — the irreducible human floor

The `renewals` workflow reads the dated lines below every day and opens an
issue 14 days before each one. This list is the only calendar the system has —
keep it current. Format is exact: `YYYY-MM-DD | description`.

2026-09-08 | Rotate SAM.gov API key (registered-entity tier, ~90-day cycle); update SAM_API_KEY secret
2026-12-01 | Review GitHub PAT and SITE_DEPLOY_KEY expiry on engine + site repos
2027-06-01 | Annual: confirm self-hosted runner host (RigRun box) is healthy and on a supported OS

Notes:
- SAM.gov keys roll on roughly a 90-day cycle — when you rotate, set the next date here.
- Add a line whenever you introduce a new credentialed source.
- There is deliberately **no custom domain**, so there is no domain renewal to track.
- GitHub disables scheduled workflows after ~60 days of repo inactivity; healthy
  daily commits prevent that, and the external HEARTBEAT_URL catches it if they stop.
