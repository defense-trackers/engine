# Proactive autopilot — the daily brief

The workspace doesn't just wait for you to open it. `engine workspace brief` computes
what actually needs attention today and (optionally) pushes it to your phone, so the
tool works when you aren't looking.

## What's in the brief

- **Deadlines (≤30d)** — tracked pursuits, act-now opps, and strong fits, nearest first;
  ≤7d flagged urgent.
- **Q&A windows** — open SBIR topic Q&A windows (the *sanctioned* channel to engage a
  TPOC on the record), with days left.
- **New high-fit** — opportunities scoring ≥55 that you haven't seen in a prior brief.
- **Next move on each pursuit** — the single weakest transition wall (Money / Requirements /
  Contracts / Incentives) and the concrete action to engineer it.

It also shows **expected revenue risk-adjusted to a program of record** (each pursuit's
best-case PoR ceiling × the *cumulative* probability of that stage actually reaching a
funded program — the SBIR→PoR funnel is brutal, so a submitted bid is ~2%), alongside the
raw best-case ceiling. A ceiling is never presented as expected revenue.

The same data renders as the **Today** tab in the live dashboard (`go run . workspace`).

## Run it

```
# print today's brief
engine workspace brief

# print AND push to ntfy (marks "new" items as seen so they surface once)
engine workspace brief --push
```

`--push` needs an ntfy target (your phone already runs ntfy for Rafiq):

```
set NTFY_TOPIC=jesse-bids                 # → https://ntfy.sh/jesse-bids
# or a full URL / self-hosted server:
set NTFY_URL=https://ntfy.sh/jesse-bids
set NTFY_SERVER=https://ntfy.example      # with NTFY_TOPIC, for self-hosted
```

The push headline leads with the most urgent thing (nearest urgent deadline, else new-fit
count); the body is the full text brief.

## Schedule it (Windows Task Scheduler — daily)

Run once each morning. `schtasks` one-liner (7:30am local):

```
schtasks /Create /TN "DefenseBidBrief" /SC DAILY /ST 07:30 ^
  /TR "cmd /c set NTFY_TOPIC=jesse-bids && C:\trackers\engine\bin\engine.exe workspace brief --push"
```

(Build the binary first: `cd C:\trackers\engine && go build -o bin\engine.exe .`)

Local Task Scheduler is the primary scheduler because the brief reads your **local**
workspace state + residential-IP DSIP — a cloud routine can't reach either.

## Calendar (optional, outward-facing)

Deadline + Q&A events can also be mirrored into Google Calendar. That's outward-facing, so
it's done deliberately (idempotent by a stable key, no duplicate spam) rather than on every
run — confirm before first sync.
