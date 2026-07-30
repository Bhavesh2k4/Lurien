# Lurien

Early-career hiring intelligence. Lurien polls company career APIs **directly**
and detects newly-posted **early-career technical** roles in **India** before
LinkedIn/Indeed index them.

"Technical" is broad on purpose — software, DevOps/SRE, data/ML/AI, security,
QA, hardware, solutions engineering, and **internships** — as long as the role
is **entry-level**. Non-technical functions (sales, marketing, HR, finance,
legal, product/program management) are excluded.

**Milestone 1** (this repo) ships **seven providers — Greenhouse, Ashby, Lever,
Workday, Eightfold, Amazon, Uber —** across **78 verified boards**, with a **Postgres store (sqlc + goose)** and a
**transactional-outbox notifier** delivering to **log / Telegram / webhook**
channels. Matches persist and are notified **exactly once**, atomically, even
across restarts — verified end-to-end on both local Docker Postgres and hosted
**Neon**. An in-memory store backs tests. More providers are the next layer; the
interfaces are already in place for them.

---

## Table of contents

1. [The 60-second mental model](#the-60-second-mental-model)
2. [End-to-end workflow](#end-to-end-workflow)
3. [How to run it](#how-to-run-it)
4. [Repository layout](#repository-layout)
5. [Every file, explained](#every-file-explained)
6. [The classification model in depth](#the-classification-model-in-depth)
7. [Configuration](#configuration)
8. [Design rules (the boundaries)](#design-rules-the-boundaries)
9. [Testing](#testing)
10. [Current limitations & roadmap](#current-limitations--roadmap)

---

## The 60-second mental model

Lurien is a **modular monolith** built in the **ports-and-adapters** (hexagonal)
style. Everything depends inward on a dependency-free `core` package:

```
   cmd/ (wiring)
      │  builds concrete adapters, injects them as interfaces
      ▼
   scheduler ─► engine ─► ┌ provider (Greenhouse) ─► fetch (HTTP)
                          ├ classify (rules)
                          └ store    (Repo port) ── writes job + outbox row (1 tx)
                                          │
   dispatcher ◄── store (Outbox port) ◄───┘
        └─► channel (log | telegram | webhook)
                              │
                              ▼
                            core      (types only — imports nothing)
```

Four rules follow directly from this shape, and they're the whole point:

| Rule | Enforced by |
|------|-------------|
| The engine never knows *which* provider fetched the jobs | engine holds a `provider.Provider` interface |
| Providers never know *how* jobs are stored | providers return `core.RawJob`; they can't import `store` |
| Notifications never know *where* jobs came from | channels consume `core.Notification`; can't import `provider` |
| Adding a provider doesn't touch the scheduler | scheduler dispatches `core.Source`, not provider types |

---

## End-to-end workflow

One **poll cycle** for one company, start to finish:

```
                                  ┌─────────────────────────────────────────┐
 configs/companies.yaml ─────────►│ config.Load → []core.Source              │
                                  └───────────────────┬─────────────────────┘
                                                      │ at boot
                                                      ▼
                                  ┌─────────────────────────────────────────┐
                                  │ scheduler: per-source ticker + jitter    │
                                  │ bounded by a concurrency semaphore       │
                                  └───────────────────┬─────────────────────┘
                                                      │ every src.Interval
                                                      ▼
   ┌──────────────────────────────── engine.Run(src) ───────────────────────────────┐
   │                                                                                  │
   │  1. provider.Get("greenhouse")           resolve adapter by kind (registry)      │
   │  2. provider.Fetch(src, fetchClient)     GET board API → []core.RawJob (ALL jobs) │
   │        └─ fetch.Client: rate-limit host, retry 429/5xx w/ jittered backoff        │
   │  3. store.ActiveBySource(src.ID)         load what we already know (map by extID) │
   │  4. for each RawJob:                                                              │
   │        classify.Classify(raw) ──► Decision ∈ {match, review, reject}              │
   │          ├─ reject → drop (M1) / (Postgres will persist for audit)                │
   │          └─ match|review → job state machine:                                     │
   │                • unseen extID      → DISCOVERED  → store.Save → (match) notify     │
   │                • seen, hash changed → UPDATED     → store.Save                     │
   │                • seen, hash same    → ACTIVE      → store.Save (touch LastSeen)     │
   │  5. any known extID NOT in this fetch → store.Close (CLOSED)                       │
   │  6. return Stats{fetched, match, review, new, updated, closed}                    │
   └──────────────────────────────────────────────────────────────────────────────────┘
                                                      │ on a new MATCH
                                                      ▼
                                  ┌─────────────────────────────────────────┐
                                  │ notify.Notifier.Notify(job)              │
                                  │  M1: structured log line w/ apply URL     │
                                  │  next: outbox row → Telegram/webhook      │
                                  └─────────────────────────────────────────┘
```

**Why this ordering matters:**

- The provider returns **every job on the board, unfiltered** — all departments,
  all locations. A single Greenhouse board mixing "University" and "Engineering"
  is a non-problem: department is just a field, and `classify` reads it as one
  signal. This is what keeps a provider tiny.
- **Change detection** is a `content_hash` (SHA-256 of title+location+URL+body).
  We only re-notify on a genuinely *new* external ID, never on a re-poll.
- **Closing** happens by set difference: anything we had that the feed no longer
  returns is marked `CLOSED`.

---

## How to run it

Requires Go 1.22+.

```bash
# 1. Does a board exist, and how many jobs? (no DB, no classify)
go run ./cmd/lurienctl probe greenhouse mongodb   # provider: greenhouse|ashby|lever
go run ./cmd/lurienctl probe ashby redis

# 2. Fetch + classify a board and print matches/review (no DB) — the tuning tool
go run ./cmd/lurienctl dryrun greenhouse stripe
go run ./cmd/lurienctl dryrun ashby sarvam
#   → === stripe ===
#     fetched=538  match=1  review=0  reject=537
#     --- MATCH (early-career tech role in India) (1) ---
#       • Software Engineer, Intern  Bengaluru  [tech:title=software engineer ...]

# 3. Run the daemon over all enabled companies (in-memory store, stdout notifier)
go run ./cmd/lurien -config configs/companies.yaml -concurrency 6
#   → logs one "poll" line per source per cycle, and a "MATCH" line per new hit
```

Build binaries: `go build -o bin/lurien ./cmd/lurien && go build -o bin/lurienctl ./cmd/lurienctl`.

Run tests: `go test ./...`.

### Running with Postgres (durable, notify-once)

The default `-store memory` loses state on restart (fine for dev). For durable,
deduped operation use Postgres:

```bash
# 1. Start Postgres (any instance; here via Docker)
docker run -d --name lurien-pg \
  -e POSTGRES_USER=lurien -e POSTGRES_PASSWORD=lurien -e POSTGRES_DB=lurien \
  -p 5433:5432 postgres:16

export LURIEN_DB="postgres://lurien:lurien@localhost:5433/lurien?sslmode=disable"

# 2. Apply migrations
goose -dir db/migrations postgres "$LURIEN_DB" up

# 3. Run the daemon against Postgres
go run ./cmd/lurien -store postgres      # reads LURIEN_DB (or pass -db <dsn>)
```

On the first cycle every match is stored (`state=discovered`), and a
notification is enqueued in the outbox **in the same transaction**. A background
dispatcher delivers it and marks it `sent`. On every later cycle the job is
already known, so it moves `discovered → active` and is **not** re-enqueued or
re-delivered — verified end-to-end on local Postgres and Neon.

### Secrets & `.env`

`cmd/lurien` loads `.env` at boot (no dependency; see `config.LoadDotEnv`).
`.env` is **gitignored**; `.env.example` is the committed template. Recognized
keys: `LURIEN_DB`, `LURIEN_STORE` (memory|postgres), `LURIEN_NOTIFY`
(log|telegram|webhook), `TELEGRAM_TOKEN`, `TELEGRAM_CHAT_ID`, `WEBHOOK_URL`.
Any hosted Postgres works by just setting `LURIEN_DB` — e.g. **Neon**
(`...neon.tech/db?sslmode=require`). With `.env` populated, `go run ./cmd/lurien`
needs no flags.

### Notification channels

```bash
# Telegram: create a bot via @BotFather, get your chat id, then:
LURIEN_NOTIFY=telegram TELEGRAM_TOKEN=... TELEGRAM_CHAT_ID=... go run ./cmd/lurien
# Webhook: POST each match as JSON to any URL:
LURIEN_NOTIFY=webhook  WEBHOOK_URL=https://... go run ./cmd/lurien
```

### Database & migrations

- **Schema** lives in `db/migrations/*.sql` (goose). Forward-only in prod; the
  daemon expects migrations to be applied before it runs.
- **Queries** live in `db/query/*.sql`; `sqlc generate` compiles them into typed
  Go in `internal/store/db/`. Edit SQL, never the generated Go.
- Tooling: `go install github.com/pressly/goose/v3/cmd/goose@latest` and
  `go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest`.
- The `jobs` table is keyed by `(source_id, external_id)`, stores the lifecycle
  `state`, the full classification (`decision`/`function`/`seniority`/
  `confidence`/`reasons`), and `first_seen`/`last_seen`/`closed_at`. `first_seen`
  is preserved across upserts so "when did we first see this" stays accurate.

---

## Repository layout

```
lurien/
├── go.mod, go.sum              module + pinned deps (yaml.v3, x/time/rate)
├── README.md                   this file
│
├── cmd/                        entrypoints (the composition roots)
│   ├── lurien/main.go          the daemon: load config → schedule → run
│   └── lurienctl/main.go       operator CLI: probe / dryrun a board
│
├── configs/
│   └── companies.yaml          the ONLY file you edit to add a company
│
├── .env / .env.example         secrets (gitignored) + template
├── sqlc.yaml                   sqlc codegen config (pgx/v5)
├── db/
│   ├── migrations/             goose SQL migrations (forward-only in prod)
│   │   ├── 00001_jobs.sql
│   │   └── 00002_notification_outbox.sql
│   └── query/                  sqlc source queries
│       ├── jobs.sql
│       └── outbox.sql
│
└── internal/                   all logic; not importable outside the module
    ├── core/core.go            domain types — imports nothing internal
    ├── config/config.go        parse + validate companies.yaml → []Source
    ├── fetch/client.go         HTTP: retry, backoff, per-host rate limit
    ├── provider/
    │   ├── provider.go         Provider interface + plugin registry
    │   ├── greenhouse/…        Greenhouse adapter (~130 lines)
    │   ├── ashby/…             Ashby adapter (~130 lines)
    │   ├── lever/…             Lever adapter (~125 lines)
    │   ├── workday/…           Workday CxS adapter (~220 lines, India-filtered)
    │   ├── eightfold/…         Eightfold adapter (~130 lines, India-filtered)
    │   ├── amazon/…            Amazon custom API adapter (~130 lines, India-filtered)
    │   └── uber/…              Uber custom API adapter (~140 lines, POST, India-filtered)
    ├── classify/
    │   ├── lexicon.go          tunable vocabulary (the three axes)
    │   ├── classify.go         the rule engine (pure, no I/O)
    │   └── classify_test.go    table-driven tests incl. real-world traps
    ├── store/
    │   ├── store.go            Repo port + in-memory adapter
    │   ├── postgres.go         durable Repo (pgx + sqlc), same interface
    │   └── db/                 sqlc-GENERATED code (do not edit)
    ├── engine/engine.go        orchestrates a poll cycle + job state machine
    ├── scheduler/scheduler.go  when each source runs (tickers + jitter)
    └── notify/notify.go        Notifier port + stdout adapter
```

The `internal/` prefix is a Go convention: nothing under it can be imported by
another module, so the package boundaries are enforced by the compiler.

---

## Every file, explained

### `go.mod` / `go.sum`
Module path `lurien`. Three external deps, all standard: `gopkg.in/yaml.v3`
(config), `golang.org/x/time/rate` (token-bucket rate limiter), and
`github.com/jackc/pgx/v5` (Postgres driver used by the sqlc-generated code).
Everything else is stdlib (`net/http`, `log/slog`, `crypto/sha256`, `regexp`).

---

### `internal/core/core.go` — the domain vocabulary (91 lines)
The center of the dependency graph. **Imports nothing internal, does no I/O.**
Every other package speaks in these types, which is what lets them stay
decoupled.

| Type | Purpose |
|------|---------|
| `Company` | an employer (`Slug`, `Name`) |
| `Source` | one polled feed: `ID` (`"greenhouse:stripe"`), `Provider`, `Params` (`{board_token}`), `Interval`, `Enabled` |
| `RawJob` | a posting as a provider returns it — normalized shape, **not yet filtered**: title, location, departments, offices, content, `Meta`, and `Raw` (the untouched original payload, kept for future reprocessing) |
| `Decision` | terminal classification outcome: `match` / `review` / `reject` |
| `Classification` | the explainable result: `Function`, `Seniority`, `Country`, `Confidence`, and `Reasons` (e.g. `["tech:title=software engineer", "india:loc=bengaluru"]`) |
| `JobState` | lifecycle: `discovered` → `active` → `updated` → `closed` |
| `Job` | the persisted, classified posting: everything above + `ContentHash`, `State`, `FirstSeen`, `LastSeen` |

Design note: `RawJob.Raw` (the original JSON) plus the stored `Classification`
mean a **better classifier can be re-run over history without re-fetching** —
critical for tuning over the years.

---

### `internal/fetch/client.go` — cross-cutting HTTP (196 lines)
So providers never hand-roll transport concerns. This is what keeps a provider
under 200 lines.

- **`Client` interface** — a one-method seam (`Do(ctx, Request) → Response`)
  that providers depend on. Trivially mockable in tests.
- **`HTTPClient`** — the production implementation:
  - **Per-host rate limiting** via `golang.org/x/time/rate`, one limiter per host
    (all Greenhouse boards share `boards-api.greenhouse.io`, so they share a
    limiter — 2 req/s, burst 4 by default).
  - **Retries** on network errors, `429`, and `5xx`, with **exponential backoff +
    ≤50% jitter**, capped at `MaxRetries` (4).
  - **Conditional requests**: honors `If-None-Match`; a `304` returns
    `NotModified` so an unchanged board costs almost nothing.
  - Sets a shared `User-Agent`, `Accept: application/json`, and a request timeout.
- `parseRetryAfter` is present for a future step (honoring `Retry-After` on 429).

---

### `internal/provider/provider.go` — the plugin seam (60 lines)
- **`Provider` interface**: `Kind() string` + `Fetch(ctx, src, hc) ([]RawJob, error)`.
  A provider *fetches and maps*. It does not filter, classify, or store.
- **Registry**: `Register` / `Get` / `Kinds`, guarded by a mutex. Providers
  self-register from their package `init()`. A new source type is a new package
  that calls `Register` — **no other file changes**, which is exactly the
  extensibility goal.

---

### `internal/provider/greenhouse/greenhouse.go` — the Greenhouse adapter (127 lines)
The whole reason for the abstraction, in one small file.

- `init()` calls `provider.Register(Provider{})`.
- `Fetch` builds `https://boards-api.greenhouse.io/v1/boards/{token}/jobs?content=true`,
  calls the injected `fetch.Client`, unmarshals, and maps **every** job to a
  `core.RawJob`. On `304` it returns `nil` (board unchanged).
- The `apiJob` struct mirrors only the fields Lurien consumes, verified against
  the live API: `id`, `title`, `updated_at`, `first_published`, `absolute_url`,
  `location.name`, `departments[]`, `offices[]`, `metadata[]`, `content`.
- Helpers `names`/`meta`/`mustRaw` flatten the API shape and stash the original
  payload in `RawJob.Raw`.

**It filters nothing** — University and Engineering postings come back in the
same list and are separated downstream by `classify`.

### `internal/provider/{ashby,lever,workday}/`
Three more adapters proving the plugin claim: each maps its API's JSON to
`core.RawJob` and self-registers via `init()`. Config keys differ per provider:
greenhouse `board_token`, ashby `board_name`, lever `site`, workday
`tenant`+`host`+`site`. `registry_test.go` asserts all four register.

**Workday is the interesting one.** Its CxS API is POST-based (hence `Method`/
`Body` on `fetch.Request`), the job list carries **no description and often no
location** (`"2 Locations"`), and a tenant can hold thousands of postings. So
the adapter **filters to India server-side**: it first discovers the tenant's
India location-facet id (facet ids are tenant-specific — Nvidia's id is
meaningless to Adobe), then paginates only the India-filtered set (20/page, cap
600) and tags every result `Offices:["India"]`. Because there's no description,
Workday jobs have no YoE signal — early-career is inferred from the **title
level** alone, so titles without a level (e.g. "ASIC Design Engineer") land in
**review**, while "Computer Scientist 1" (Adobe's entry title) matches. India
discovery prefers a country-level "India" facet and **falls back to unioning
India-city facets** (Bengaluru/Hyderabad/…) for tenants that only expose
city-level locations (Micron, Broadcom).

Adding all three **touched nothing** in engine, scheduler, store, or notify —
only blank imports in `cmd/`, `companies.yaml` lines, and (for Workday's POST)
the shared `fetch` layer.

### `internal/provider/eightfold/`
The 5th provider. Eightfold powers companies' **own** branded career sites
(careers.X.com) via a public positions API at `{tenant}.eightfold.ai` that
supports a server-side `location=India` filter. Config keys: `tenant`+`domain`.
Caveat learned in practice: some Eightfold tenants **gate** their public jobs API
(return count 0, or 404/redirect) — the clean endpoint works for NetApp but
Amex/Nutanix need per-tenant handling, so Eightfold's real reach is narrower than
"anyone with careers.X.com".

---

### `internal/classify/lexicon.go` — the tunable vocabulary (129 lines)
Data, not logic. The vocabulary the rule engine matches against, split by axis so
tuning is a lexicon edit, not a code change:

- **`FunctionLex`** — `CoreTech`, `NonTech`, `BroadTech` (tiered; see below).
- **`SeniorityLex`** — `RejectTokens` (senior/lead/manager/level≥2), `EarlyTokens`
  (new-grad/intern/junior/associate/level-1), `EarlyDepartments` (weak prior),
  `MaxYears` (the YoE ceiling, 2).
- **`LocationLex`** — `IndiaTerms` (country + city gazetteer).
- `DefaultLexicon()` returns the built-ins. (A `configs/classify.yaml` override
  loader is the intended next step so tuning needs no rebuild.)

---

### `internal/classify/classify.go` — the rule engine (166 lines)
**Pure. No I/O, no provider awareness.** A job matches only if it is **technical
AND early-career AND in India**; otherwise `review` (quarantine) or `reject`.

- `Classify(RawJob) → Classification` runs three axes and records `Reasons`.
- `function` — tiered: CoreTech → (NonTech reject) → BroadTech → ambiguous.
- `seniority` — reject-tokens dominate, then early-tokens, then YoE regex, then a
  weak early-department prior, else ambiguous.
- `location` — India gazetteer over location text + offices.
- `decide` — combines the three axes into `match`/`review`/`reject`.
- `norm` — the safety-critical helper: lowercases, replaces non-alphanumerics
  with spaces, and pads with spaces so **phrase matching respects word
  boundaries**. This is why `intern` never matches `internal`.
- `minYears` — smallest plausible years figure found in the (HTML-stripped) body.

---

### `internal/classify/classify_test.go` — the safety net (94 lines)
Table-driven tests, including the real-world traps discovered against live data:
the `internal auditor ≠ intern` substring trap, MongoDB's numeric `Software
Engineer 3`, `Senior Software Engineer` in Bengaluru (title beats location),
non-tech rejects even when entry-level, and the full happy path (SWE/DevOps/data
science/AI/solutions + interns). Because `classify` is pure, every case is a
one-line struct.

---

### `internal/config/config.go` — configuration (78 lines)
- `Load(path)` reads `companies.yaml`, validates it (slug + provider required,
  no duplicate source IDs, non-empty), applies `default_interval`, and returns
  ready-to-run `[]core.Source`.
- `enabled` defaults to true (pointer field distinguishes "unset" from "false").
- Adding a company is purely a data edit here — no code path changes.

---

### `internal/store/` — persistence port + two adapters
- **`store.go` — `Repo` interface** — the port the engine depends on:
  `ActiveBySource`, `Save`, `SaveAndEnqueue` (upsert job + enqueue notification
  atomically), `Close`. Plus the **`Outbox` interface** (claim/mark/reschedule/
  fail) the dispatcher uses, and **`Memory`**, a mutex-guarded map implementing
  both for dev/tests.
- **`postgres.go` — `Postgres`** — the durable adapter over pgx + the
  sqlc-generated `db` package. It implements the **exact same `Repo` interface**,
  so the engine is byte-for-byte unchanged; only `cmd/lurien` chooses which
  backend to build. `toParams`/`fromDB` map `core.Job` ↔ the generated row type
  (including `pgtype.Timestamptz` handling and `first_seen` preservation on
  upsert). This is the payoff of the ports design: persistence dropped in with
  zero change to engine, classify, provider, or scheduler.
- **`db/`** — sqlc **generated** code (`models.go`, `querier.go`, `jobs.sql.go`).
  Never hand-edited; regenerated with `sqlc generate`.

---

### `internal/notify/notify.go` — outbox dispatcher + channels
- **`Dispatcher`** — leases due notifications from the `Outbox` port, sends each
  through a `Channel`, and records the outcome: `MarkSent`, `RescheduleNotification`
  (exponential backoff, 10s→30m), or `FailNotification` (dead-letter after 6
  attempts). Runs on a 5s tick in its own goroutine.
- **`Outbox` interface** — defined here (consumer-owned) so notify doesn't import
  `store`; both `store.Memory` and `store.Postgres` satisfy it.
- **`Channel` interface** — `Name()` + `Send(ctx, core.Notification)`. Three
  implementations: **`LogChannel`** (stdout), **`TelegramChannel`** (Bot API
  `sendMessage`), **`WebhookChannel`** (POST JSON). Adding a channel touches
  nothing else. None can import `provider` — delivery is origin-agnostic.

---

### `internal/engine/engine.go` — orchestration + job state machine (160 lines)
The conductor. Depends only on interfaces (`provider`, `Classifier`, `store.Repo`,
`notify.Notifier`) — it names no concrete provider or channel.

- `Run(ctx, src)` executes one poll cycle (the flow in the diagram above) and
  returns `Stats`.
- `upsert` implements the **job state machine**: DISCOVERED (new) → notify on
  match; UPDATED (hash changed); ACTIVE (unchanged). Rejects are dropped in M1.
- `contentHash` is the change-detection key.
- `Classifier` is declared here as a local interface (the consumer defines the
  interface it needs) so the engine doesn't import the classifier's concrete type.

---

### `internal/scheduler/scheduler.go` — timing + concurrency (100 lines)
- `Scheduler.Run` starts one goroutine per enabled source; each loops on
  `src.Interval` with **±15% jitter** (no thundering herd against the shared
  Greenhouse host), and dispatches through a **bounded semaphore** so global
  in-flight cycles are capped.
- It is **provider-agnostic**: it invokes a `RunFunc(ctx, src)` and knows nothing
  about fetching. The same seam later swaps in Postgres `SKIP LOCKED` leasing for
  multi-instance distributed operation.

---

### `cmd/lurien/main.go` — the daemon (67 lines)
The composition root. Loads config, constructs the concrete adapters
(`fetch.HTTPClient`, `classify.Default`, `store.Memory`, `notify.Log`), wires
them into an `engine.Engine`, hands a `RunFunc` to the scheduler, and blocks
until `SIGINT`/`SIGTERM`. The blank import `_ ".../greenhouse"` triggers provider
registration. **This is the only place concrete types meet interfaces.**

---

### `cmd/lurienctl/main.go` — the operator CLI (102 lines)
- `probe <token>` — fetch a board, print the job count (is it on Greenhouse?).
- `dryrun <token>` — fetch + classify + print MATCH and REVIEW buckets with
  reasons, **no database**. This is the fastest loop for validating a new board
  or tuning the lexicon against real data.

---

### `configs/companies.yaml` — the company list
35 verified Greenhouse boards (confirmed live 2026-07-29/30). Each entry is
name + slug + provider + `config.board_token`. `default_interval: 3m`. Adding a
company = one line here. Targets not on Greenhouse (Redis, banks, semis, most
unicorns, FAANG) are tracked in `docs/BACKLOG.md`, bucketed by the provider that
will unlock them.

---

## The classification model in depth

A job is a **MATCH** only when all three axes pass:

```
MATCH  =  isTech(function)  AND  isEarly(seniority)  AND  isIndia(location)
```

No single field decides — that's the core insight from the real data (a
"University Recruiting" department contains PMs and interns; a Bengaluru
"Engineering" department contains Directors and Senior SWEs).

### Axis 1 — Function (tiered, order matters)
1. **CoreTech** phrases (`software engineer`, `devops`, `data scientist`,
   `ai engineer`, `solutions engineer`, …) → **tech**, immediately. So
   `Software Engineer, Marketing Platform` is tech before "marketing" is seen.
2. **NonTech** phrases (`sales`, `marketing`, `recruiter`, `finance`, `legal`,
   `product manager`, …) → **reject**. Safe to use bare words here because
   CoreTech already claimed the real engineering titles.
3. **BroadTech** fallback (`engineer`, `developer`, `scientist`, `technical`) →
   **tech**. Catches the long tail (`Cloud Ops Engineer`).
4. else **ambiguous**.

### Axis 2 — Seniority (reject dominates)
1. **Non-numeric senior words** in the title (`senior`, `sr`, `staff`, `lead`,
   `manager`, `director`, `principal`, `architect`, `l4..l6`) → **senior → reject**.
   Dominates even a "University" department.
2. **Early tokens** (`new grad`, `intern`, `junior`, `associate`, `fresher`,
   `software engineer i`, `sde 1`, …) → **early**.
3. **Numeric title level** — `levelOf()` extracts the level (`Software Engineer
   II` → 2, `SDE 3` → 3, `Computer Scientist 1` → 1) and compares it to the
   company's **early ceiling**: `≤ ceiling` → early, else senior. The ceiling is
   **1 by default**, overridable per company via `classify.max_early_level` in
   `companies.yaml` — set it to 2 for employers whose entry level is "Software
   Engineer II" (Walmart), 3 where new grads come in at III. This is why level
   semantics are company-relative, not global.
4. **YoE** parsed from the body: `> MaxYears` → mid (reject); `≤ MaxYears` →
   early. *(Fuzziest signal — see limitations.)*
5. **Early department** prior (`university`, `campus`, `early talent`) → early.
6. else **ambiguous**.

### Axis 3 — Location (India)
India gazetteer (`india`, `bengaluru`/`bangalore`, `hyderabad`, `pune`, …) over
the location string + office names. Handles the messy real formats: `Bengaluru`,
`IN-Bengaluru`, `India, Bangalore`, `IN - Bengaluru`.

### The decision
```
country != IN              → reject
function reject OR senior   → reject
tech AND early             → MATCH
otherwise (in India, not hard-rejected, but ambiguous) → REVIEW  (quarantine)
```

**REVIEW is deliberate**: ambiguous jobs are logged, not notified. It's the
feedback queue — you inspect it, adjust the lexicon, and it becomes the labeled
set if an LLM ever assists the ambiguous tail (see roadmap).

### Why rules, not an LLM (today)
Polling ~20 boards every few minutes = thousands of postings per cycle, mostly
unchanged. Rules are **microseconds per job, free, deterministic, and unit-
testable** — the right default for a system that runs forever. An LLM is reserved
for the small ambiguous residue later, cached by `content_hash` so each unique
JD is classified at most once.

---

## Configuration

`configs/companies.yaml`:

```yaml
default_interval: 3m          # used when an entry omits `interval`

companies:
  - name: Stripe              # display name
    slug: stripe              # stable id; source ID becomes "greenhouse:stripe"
    provider: greenhouse      # which adapter (registry key)
    config:                   # provider-specific params
      board_token: stripe     # Greenhouse board slug
    # enabled: true           # optional; defaults true
    # interval: 5m            # optional; overrides default_interval
```

Verify a token before adding: `go run ./cmd/lurienctl probe <token>`.

---

## Design rules (the boundaries)

These are the invariants the structure enforces:

- **`core` imports nothing internal.** It's the shared vocabulary; if it depended
  on anything, everything would.
- **Providers → `core` + `fetch` only.** They can't reach `store` or `classify`.
- **`engine` depends on interfaces**, injected at `cmd`. It never imports a
  concrete provider, store, or notifier.
- **`classify` is pure.** No I/O means every rule is a fast, deterministic test.
- **The consumer defines the interface** (e.g. `engine.Classifier`) so packages
  don't take dependencies they don't need.

A new provider is a new package + one `companies.yaml` line. The scheduler,
engine, store, and notifier are untouched.

---

## Testing

- **`classify`** has table-driven tests today (`go test ./internal/classify`),
  seeded from real API traps. Grow the table whenever a real posting surprises
  you — that's the precision/recall ledger.
- **Planned** (documented, not yet written): fixture-based provider tests
  (recorded Greenhouse JSON via `httptest`), fault-injection for `fetch` (429/5xx
  → assert retry/backoff), engine tests with a fake provider + memory store +
  spy notifier, and a `store` suite against ephemeral Postgres once that adapter
  lands.

Because every boundary is an interface, each package is testable in isolation.

---

## Current limitations & roadmap

**M1 shortcuts (intentional, isolated behind ports):**

| Area | Today | Why it's safe to defer |
|------|-------|------------------------|
| Store | **Postgres (sqlc+goose)** ✅ + in-memory for tests | done — notify-once verified across restarts |
| Notify | **transactional outbox** ✅ → log/Telegram/webhook | done — atomic enqueue, at-least-once delivery, backoff, dead-letter |
| Providers | **Greenhouse + Ashby + Lever + Workday** ✅ | done — added with zero engine change (proves the plugin claim) |
| Scheduler | per-source tickers | `RunFunc` seam → Postgres `SKIP LOCKED` later |
| YoE parsing | smallest-number heuristic | fuzziest signal; ambiguous → review, not notify |
| Rejects | not persisted | engine drops them; add if audit/reprocess needs it |
| Lexicon | Go defaults | `classify.yaml` override loader is the next small step |

**Roadmap:**
1. ✅ **Postgres store** — sqlc + goose; DISCOVERED/UPDATED/CLOSED history,
   notify-once across restarts. *(done)*
2. ✅ **Outbox notifier** — transactional outbox (same tx as the job upsert) +
   log/Telegram/webhook channels, at-least-once delivery, backoff + dead-letter.
   *(done)*
3. ✅ **Ashby + Lever + Workday providers** — added with zero engine change; 55
   sources across 4 providers. Workday unlocked the enterprise bucket (Nvidia,
   Adobe, Salesforce, Mastercard, CrowdStrike…). **Next:** custom FAANG adapters
   (Amazon/Google/Microsoft/Apple/Meta) and per-company level tuning.
4. **Observability** — Prometheus metrics + OpenTelemetry traces per cycle.
5. **Distributed** — swap the scheduler for DB-leased work; run N instances.
6. **Intelligence** — freshness SLAs, cross-company dedup, LLM assist on the
   review tail (cached by content hash).

**Boundary calls you can flip in `internal/classify/lexicon.go` (one line each):**
- Product/Program Managers — currently **excluded** (treated as non-IC).
- Customer/Technical Support Engineers — currently **included** via broad-tech.
