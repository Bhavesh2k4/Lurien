# Company backlog

Target companies **not yet monitored**, because they're not on Greenhouse. Each
is bucketed by the provider that would unlock it. ATS guesses are best-effort and
should be confirmed by probing the provider's public API (the same way we
verified Greenhouse). Inclusion filter: pays >~₹12 LPA base and hires
early-career SWE in India (in-person or remote).

Companies already **live** (Greenhouse, in `configs/companies.yaml`) are not
repeated here.

Legend: 🟢 easy provider · 🟡 medium · 🔴 bespoke.

---

## ✅ Ashby / Lever — providers BUILT (2026-07-30)
Provider code lives in `internal/provider/{ashby,lever}`. Companies confirmed on
these APIs are now **live** in `configs/companies.yaml`:
- Ashby: Redis, Confluent, Snowflake, Cohere, OpenAI, Sentry, Perplexity,
  Sarvam AI, Supabase, Snyk, Wiz
- Lever: Mistral AI

Still **not found on Ashby or Lever** (404 on both) — these are Workday/custom,
moved to those buckets below:
HashiCorp, Rippling, DigitalOcean, GitHub, Couchbase, Cloudera, New Relic,
Cohesity, Hugging Face, Glean, Krutrim, SentinelOne, Harness, Hasura, dbt Labs,
Sprinklr, Chargebee, BrowserStack, Freshworks (Freshworks/Mistral have Lever
sites but 0 live postings — post elsewhere).

## 🟡 Workday — provider BUILT (2026-07-30)
Code in `internal/provider/workday`. **Live now (26):** Nvidia, Adobe,
Salesforce, Mastercard, Autodesk, CrowdStrike, PayPal, Workday, Visa, NXP,
Marvell, Cadence (`max_early_level: 2`), Micron, Broadcom, Qualys, HPE, Cisco,
Analog Devices, Altera, Deutsche Bank, Barclays, Wells Fargo, Fidelity, KLA,
Capital One, F5 (tenant `ffive`).

Adding a Workday tenant is a **discovery** task, not a code task: look up the
company's real `myworkdayjobs.com` URL (web search — landing pages are
bot-blocked), which gives `tenant`/`wd-number`/`site`, then verify with
`lurienctl dryrun workday <tenant> <host> <site>`. The provider handles the rest.

The provider now has a **city-level India fallback** (unions Bengaluru/Hyderabad/…
facets) for tenants with no country-level "India" facet — this unlocked Micron
and Broadcom.

Still blocked:
- **Dell** (`dell.wd1`/External) — CxS returns 0 even unfiltered (API gated).
- **VMware** (`vmware.wd1`/VMware) — CxS errors; Broadcom covers VMware roles.

## 🔵 Eightfold — provider BUILT (2026-07-30)
Code in `internal/provider/eightfold`. **Live now (1): NetApp** (85 India jobs).

Reality check from building it: the Eightfold customer overlap with our targets
is **smaller than hoped**, and several tenants gate their public API:
- **NetApp** — clean `{tenant}.eightfold.ai/api/apply/v2/jobs?domain=&location=India` works.
- **Amex** (`aexp.eightfold.ai`), **Nutanix** (`nutanix.eightfold.ai`) — have
  Eightfold career sites but the public jobs API returns count 0 / 404 / 307
  (gated; needs per-tenant pid/session/version handling).
- **ServiceNow, Intuit, AMD** — `{name}.eightfold.ai` doesn't resolve; they are
  NOT on Eightfold (some other custom ATS).
- Other Eightfold tenants seen (non-target): Fortive, John Deere, Micron
  (already covered via Workday).

Still needed for the true "own website" cluster: **Phenom / SmartRecruiters /
custom** adapters, and per-tenant Eightfold API handling.

Still to look up (tenant/host/site unknown): AMD, HPE, Teradata, Analog Devices,
Western Digital, American Express, Pure Storage, Commvault, Akamai, Palo Alto,
Arista, Juniper, Wayfair, Expedia, Texas Instruments.

**Walmart** — careers site is a **303 redirect** off the standard CxS path; needs
its real ATS confirmed. When added, set `classify: { max_early_level: 2 }` since
its entry level is Software Engineer II.

## 🔴 Custom career sites (one-off adapter each — highest value per company)
FAANG + a few giants; each hires the most India new-grads but needs a bespoke
API adapter.

**Done:**
- **Amazon** — provider BUILT (`internal/provider/amazon`, amazon.jobs API,
  country=IND). ~124 early-career India matches — the highest-yield single source.
- **Databricks** — turned out to be **Greenhouse** behind a Gatsby front-end
  (board_token `databricks`); added, no custom adapter needed.
- **Airbnb** — also **Greenhouse** (`airbnb`); added.

- **Uber** — provider BUILT (`internal/provider/uber`, POST loadSearchJobsResults,
  country=IND). ~12 India jobs (Uber's India hiring is quiet now).

**Still custom — but PROTECTED (need browser token extraction; deferred):**
- **Apple** — jobs.apple.com API returns 436 / CSRF-gated.
- **Microsoft** — gcsservices.careers.microsoft.com is Akamai-blocked even with a
  browser UA.
- **Google** — the v3 JSON API is gone (404); only the HTML results page works
  (would need scraping).
- **Meta** — GraphQL, needs a session token.

**Still custom — other ATS / not yet built:**
- LinkedIn (MS), Oracle (Taleo), SAP (SuccessFactors), ServiceNow
  (SmartRecruiters), Atlassian, Intuit
- Flipkart, Meesho, Swiggy, Zomato, CRED, Juspay, Zerodha

## Banks / quant on non-Greenhouse ATS (verify)
- Banks (Workday/custom): Goldman Sachs, Morgan Stanley, JPMorgan Chase, BlackRock, Bloomberg, Wells Fargo, Deutsche Bank, Barclays, UBS
- Quant not on Greenhouse (custom): Tower Research, DRW, Hudson River Trading, Citadel Securities, Graviton, AlphaGrep, Quadeye, Da Vinci, Squarepoint

## Semiconductors (mostly Workday/custom)
- Intel, Texas Instruments, MediaTek, Synopsys, Cadence, Arm, Samsung (SRI-B)

---

## Now live (added 2026-07-30)
- **Greenhouse:** Jane Street, IMC Trading, WorldQuant, Optiver, Twilio, Netskope,
  Dropbox, Groww, Scale AI, Together AI, Fireworks AI, Fivetran, Temporal, Vercel.
- **Ashby:** Redis, Confluent, Snowflake, Cohere, OpenAI, Sentry, Perplexity,
  Sarvam AI, Supabase, Snyk, Wiz.
- **Lever:** Mistral AI.
- **Workday (24):** Nvidia, Adobe, Salesforce, Mastercard, Autodesk, CrowdStrike,
  PayPal, Workday, Visa, NXP, Marvell, Cadence, Micron, Broadcom, Qualys, HPE,
  Cisco, Analog Devices, Altera, Deutsche Bank, Barclays, Wells Fargo, Fidelity, KLA.

- **Eightfold (1):** NetApp.
- **Custom:** Amazon (amazon.jobs), Uber (uber.com/careers). **Greenhouse:** +Databricks, +Airbnb.

**78 sources across 7 providers.** Verified end-to-end at scale on Neon: 74/74
sources polled, 0 errors, 46 matches persisted, outbox fully drained
(47 enqueued → 47 delivered), and 4 matches delivered live to Telegram.
Redis — earlier unconfirmed on Greenhouse — turned out to be on **Ashby**.
