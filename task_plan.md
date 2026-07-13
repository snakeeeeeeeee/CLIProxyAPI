# Task Plan: Multi-Pool API Keys, Pricing, Usage, And Console

## Current Implementation: Pool Config Inheritance, Model Capacity, And Health

### Goal

Add sparse per-pool overrides on top of global defaults, expose truthful Sonnet/Opus/Fable relative capacity, and provide an explainable pool/global health score without adding upstream probes or changing ordinary `/v1/*` behavior.

### Phases

1. [completed] Map schema/runtime/API/UI boundaries and add idempotent pool-config storage plus effective merge semantics.
2. [completed] Project effective routing and pure-mode settings into isolated pool runtime scopes with global and account precedence.
3. [completed] Add batch model-capacity and health aggregation to pool summaries and scoped/global stats.
4. [completed] Add pool strategy UI, health gauges, capacity summaries, responsive layouts, and SSE refresh behavior.
5. [completed] Update secondary-development docs and run focused/full/backend/frontend/browser verification.

### Decisions

- Persist only sparse pool overrides; missing fields inherit global and explicit `null` clears an override.
- Keep Profile, TLS, pricing, quota/proxy workers, logging, Trace, and client cache-TTL permission global.
- Pool pure mode affects only downstream-visible usage; raw upstream usage and cost remain unchanged.
- Health is observational only and never changes scheduling or automatically disables a pool.
- Relative capacity uses existing fresh quota snapshots and never sends inference heartbeats.

### Errors Encountered

| Error | Attempt | Resolution |
|---|---:|---|
| Focused backend compile previously reported an unused `coreexecutor` import after runtime projection changes | 1 | Removed the stale import; resourcepool, claudeapipool, management, and sdk/cliproxy focused tests now pass. |
| The first isolated UI smoke inherited enabled quota/proxy maintenance workers from the copied SQLite config | 1 | Stopped it immediately and disabled both workers in the temporary database before browser verification; production config/database were untouched. |
| A full-page browser screenshot invalidated the temporary tab binding | 1 | Opened a fresh tab in the existing isolated browser session and continued with viewport screenshots; application state and the isolated service were unaffected. |
| The active Strategy tab initially remained outside the 375px tab-strip viewport | 1 | Added post-pool-load tab-strip alignment using the selected tab offsets; deep links now reveal the active tab without page-level overflow. |

## Implementation: Quota Confidence And Compact Account Cards

### Goal

Represent quota percentages truthfully and reduce account cards to the operational information needed for scanning and repeated actions.

### Phases

1. [completed] Add quota-utilization confidence to OAuth/header parsing, merge, persistence compatibility, and routing evaluation.
2. [completed] Redesign account cards around identity/scheduling, availability, shared/model quota, load/usage, proxy, and actions.
3. [completed] Align table/detail quota labels with exact/shared/observed/unknown semantics.
4. [completed] Run Go/frontend regression, build the embedded console, and verify responsive layouts at 375/768/1024/1440.

### UI Decisions

- Keep one scheduling badge in the card header; remove Auth ID and successful test badges from the card.
- Show shared 5h/7d bars plus compact Sonnet/Opus/Fable status values.
- Use a stable metric grid for concurrency, RPM, requests, tokens, and cost.
- Show one-line bound proxy identity and retain only Test plus overflow actions in the footer.
- Keep errors, test history, model details, session diagnostics, and complete identity data in the account drawer.

### Constraints

- Do not introduce scheduled inference canaries or additional model heartbeat requests.
- Preserve raw upstream quota/usage, existing pool isolation, and routing behavior for known utilization values.
- Existing persisted quota JSON without confidence fields must remain readable.

## Research: Claude Subscription Quota Accuracy

### Goal

Determine how Anthropic, Claude Code, sub2api, and other open-source Claude account pools obtain shared and model-scoped quota utilization/reset data, and separate authoritative values from inferred or estimated displays.

### Phases

1. [completed] Verify current Anthropic and Claude Code quota surfaces and documented semantics.
2. [completed] Inspect local sub2api and this project's active/passive quota collection paths.
3. [completed] Compare representative open-source proxy/account-pool implementations.
4. [completed] Summarize what can be queried accurately and recommend a truthful UI/routing policy.

### Conclusion

- Treat fresh OAuth usage `utilization/percent/reset` and fresh unified response headers as authoritative observations.
- Treat an absent model claim as shared/unknown, never as 0% used or 100% remaining.
- Preserve source, observation time, reset, and utilization-presence independently for every window.
- Do not estimate subscription percentages from local token totals; Claude and Claude Code share dynamic plan limits.
- Keep active inference canaries optional/manual because they consume real requests; normal routing should combine OAuth polling with passive headers.

### Constraints

- Research only; do not modify runtime or UI behavior in this phase.
- Do not expose OAuth tokens, SessionKeys, API keys, proxy credentials, or raw account identifiers.
- Treat undocumented internal endpoints and response headers as unstable implementation details.

### Errors Encountered

| Error | Attempt | Resolution |
|---|---:|---|
| DuckDuckGo and Google HTML searches returned no extractable official support links | 1 | Continue with known official Claude Code/support URLs, site indexes, local source, and GitHub code search instead of repeating the same search. |
| `gh` CLI is not installed in this workspace | 1 | Switched to the connected GitHub code-search tool and raw public file URLs. |

## Research: Claude Code Prompt Cache TTL Policy

### Goal

Confirm Anthropic's current default prompt-cache TTL, determine the real Claude Code request shape, audit account-pool passthrough/mimic/test behavior, and assess whether a policy switch should ignore client-supplied cache TTL values.

### Phases

1. [completed] Verify current official Anthropic cache TTL and beta requirements.
2. [completed] Inspect local Claude Code traces, Phistory references, and sub2api behavior.
3. [completed] Audit this project's passthrough, mimic, management-test, and count_tokens paths.
4. [completed] Compare policy options, security claims, billing effects, and recommend a scoped design without implementing it.

### Recommendation

- Default every existing account-pool cache breakpoint to 1h so passthrough, mimic, streaming, count_tokens, and management tests share the Claude subscription request policy.
- Keep client TTL control disabled by default. When enabled, preserve only explicit valid 5m/1h values; omitted TTL still becomes 1h.
- Apply cache TTL policy, breakpoint limiting, and TTL-order normalization only after the final outbound body has been constructed.
- Do not change ordinary `/v1/*`, Claude API Pool, or other providers.

### Confirmed Implementation Direction

- All existing cache breakpoints on Claude Code account-pool outbound requests default to 1h, including passthrough, mimic, streaming, count_tokens, and management tests.
- A default-off `allow_client_cache_ttl` setting permits explicit client 5m/1h values; omitted TTL still resolves to 1h.
- Ordinary `/v1/*`, Claude API Pool, and other providers remain unchanged.

### Implementation Phases

1. [completed] Add the default-off client TTL control setting and runtime auth projection.
2. [completed] Apply account-pool-wide default 1h TTL after final request construction for message, stream, passthrough, mimic, and count_tokens paths.
3. [completed] Align management tests/calibration and add backend regression coverage.
4. [completed] Add the System Settings control and update secondary-development documentation.
5. [completed] Run formatting, focused/full tests, frontend/backend builds, isolated API smoke, and responsive browser checks.

### Implementation Errors

| Error | Attempt | Resolution |
|---|---:|---|
| The isolated browser loaded a stale management key and triggered the localhost failed-auth ban | 1 | Persisted the correct temporary key, restarted only the isolated 28319 service, then completed the smoke without touching 28317. |
| A mobile screenshot timed out after the page scroll completed | 1 | Used DOM geometry and responsive width checks for the control and tooltip instead of repeating the timed-out capture. |

### Initial Research Boundary

- The research phase made no runtime or UI behavior changes; implementation began after the user confirmed the policy.
- Treat official documentation and captured requests as evidence; do not infer anti-ban behavior from anecdotal claims.
- Keep the change limited to Claude Code account-pool routes.

## Follow-up: Persistent API Key Reveal

### Goal

Allow administrators to view and copy generated account-pool API keys after creation while keeping full secrets out of list responses and routine frontend refreshes.

### Phases

1. [completed] Map the current hash-only storage, management routes, and API Key UI.
2. [completed] Persist newly generated/rotated secrets and add an authenticated reveal endpoint.
3. [completed] Add reveal/copy controls and legacy-key handling to desktop and mobile UI.
4. [completed] Update docs and run focused/full/backend/frontend/browser verification.

### Decisions

- List APIs continue returning prefixes plus `secret_available`; full secrets are fetched only through a dedicated management endpoint.
- Newly created and rotated secrets are persisted because the current hash-only value cannot be reversed.
- Existing hash-only keys remain valid but require one rotation before they can be revealed.
- Revocation clears the persisted secret; disabled but non-revoked keys remain viewable.

## Follow-up: Permanent Keys And Direct Pool Navigation

### Goal

Make generated account-pool API keys permanently valid until disabled, rotated, or revoked; remove usage-window controls from the API Key and pool-list pages; and open a pool's account list directly when selected.

### Phases

1. [completed] Map expiration, usage-window, and pool navigation behavior across backend and frontend.
2. [completed] Remove API-key expiration semantics and migrate legacy expiry values to permanent keys.
3. [completed] Simplify API Key/pool list UI to lifetime usage and route pool selection to accounts.
4. [completed] Update docs and run backend, frontend, build, and responsive browser verification.

### Decisions

- Keep the nullable SQLite `expires_at` column only for schema compatibility; clear it through an idempotent migration and never expose or enforce it.
- API Keys and the top-level pool list always aggregate usage with `window=all`.
- Overview, pool overview, and routing-event views keep their operational time-window controls.
- Pool selection from the pool list navigates to `#/pools/<id>/accounts`.

## Current Goal

Upgrade the Claude Code account pool to strict one-account/one-pool isolation with an immutable `default` pool, pool-bound generated API keys, versioned upstream-cost pricing, scoped usage aggregation, and a clearer multi-page console without changing `/claude-acc-pool/v1/*` paths or ordinary `/v1/*` behavior.

## Current Phases

1. [completed] Add SQLite schema/migration, pool CRUD, account membership, and runtime pool attributes.
2. [completed] Add pool-key authentication, per-pool scheduler namespaces, registration/move behavior, and isolation tests.
3. [completed] Add versioned model pricing, cache-duration accounting, immutable cost snapshots, and scoped usage APIs.
4. [completed] Refactor the resource console into overview, pools, API keys, proxies, models/pricing, and settings views.
5. [completed] Update docs; run focused/full/backend/frontend tests, responsive browser checks, and one bound-proxy smoke.

## Current Decisions

- Pool ID/name `default` is immutable; all existing accounts, usage, and routing events migrate into it.
- One account and one generated API key each belong to exactly one pool.
- Existing config API keys remain compatible with account-pool routes and map only to `default`.
- Generated `sk-cap-...` keys work only on `/claude-acc-pool/v1/*`; full values are persisted for explicit management reveal but never returned by list APIs.
- Execution remains globally recognized as `claude-acc-pool`, while affinity/capacity/cooling use a pool-specific routing namespace.
- Routing policy is global with existing per-account capacity overrides; model pricing is global and versioned.
- Cost uses raw upstream usage, not pure-mode output, and historical price revisions never change.
- UI defaults usage windows to 30 days and moves model quota/token detail out of compact account cards.

## Current Errors Encountered

| Error | Attempt | Resolution |
|---|---:|---|
| Legacy 529 test still used Claude API Pool attributes and exposed account-pool cooldown reads from the parent scope | 1 | Converted the fixture to a real account-pool auth/context and moved account-pool cooldown, refresh, removal, wait, and prefix policy operations to the pool child namespace. |
| Usage filter loop incorrectly treated the range index as a SQL column name | 1 | Replaced it with an explicit filter struct variable before rerunning the package tests. |
| Combined routing-event window patch expected a multiline `api.ts` parameter initializer, but the existing code uses a different one-line initializer | 1 | No files changed; re-read exact contexts and split the patch by file. |
| First isolated Management API curl left a query-string URL unquoted, so zsh treated `?` as a glob | 1 | No request was sent; quote URLs containing query strings for all remaining smoke calls. |
| First generated-secret persistence scan assumed the SQLite path was relative to the repository root | 1 | The initializer resolves it relative to the resource config directory, producing a nested path; locate the actual DB and rerun the scan before claiming success. |
| Browser opened port 28319 with a stale stored management key; parallel queries exceeded the five-failure limit and temporarily banned localhost, making the correct `X-Management-Key` look incompatible | 1 | Confirmed the middleware supports both headers and the 403 body reports the IP ban; restart only the isolated service to clear in-memory failures after the login page removes stale state. |
| Quitting the detached `screen` removed the session but left its isolated server child listening on 28319, so the attempted restart exited on port conflict | 1 | Identify and terminate only the isolated 28319 PID, verify the port is free, then start a fresh detached process. |
| Combined responsive patch expected the API Keys table opening tag on its own line, but the file keeps the full table JSX on one line | 1 | No files changed; split class substitutions and mobile-row refactors into small per-file patches. |
| First screenshot of the rebuilt 768px price-card page timed out in CDP after the DOM layout calculation completed | 1 | Preserve the completed geometry result, read browser recovery guidance, and take a fresh lightweight viewport screenshot instead of replaying the whole call. |
| A copied real pre-multi-pool SQLite database failed startup because pool indexes were created before legacy tables gained `pool_id` | 1 | Move pool index creation behind column migration, add a legacy-table regression test, and verify the corrected binary against a fresh online database backup. |
| The existing browser binding does not expose navigation through `tab.playwright.goto` | 1 | Inspect the already selected tab API and use its documented tab-level navigation method instead of retrying the same call. |
| The previous browser tab binding had already been finalized and was no longer part of the active session | 1 | Create a fresh tab from the existing browser binding and continue without reinitializing the browser runtime. |
| Page evaluation could not access `navigator.clipboard` while cleaning the synthetic copied Key | 1 | Use the tab clipboard capability to clear the test value, then reset the viewport and close the tab. |
| A non-interactive 1h record-only capture with only a dummy API key never reached the local recorder | 1 | Stop the attempt after confirming zero API duration; use the installed CLI source and already captured authenticated 2.1.207 traces instead of retrying the known dummy-key failure. |
| Piping the public Phistory trace through `head` caused curl to report a broken output pipe | 1 | Download the small public trace to `/tmp`, inspect only cache-control metadata with jq, then remove it. |

---

# Previous Task: Simplified Account Pool Capacity And Quota Routing

## Current Implementation: Claude Code Observable Fingerprint Alignment

1. [completed] Inventory current account-pool request, header, TLS, connection, and retry fingerprints.
2. [completed] Compare the local sub2api implementation and verified Claude Code traces.
3. [completed] Review relevant public implementations and separate evidence-backed techniques from cargo-cult spoofing.
4. [completed] Make mimic Session metadata/header identity atomic and pin the verified platform tuple.
5. [completed] Add cross-field trace validation plus raw HTTP/1.1 and TLS fingerprint fixtures.
6. [completed] Add an account-pool-only ordered HTTP/1.1 transport while preserving proxy and streaming behavior.
7. [completed] Update secondary-development docs and run focused/full/build/frontend checks plus one bound-proxy real smoke.

Research boundary: improve protocol compatibility and internal consistency; do not add unstable random fingerprint rotation or bypass-oriented behavior.

Decisions:

- Real Claude Code passthrough remains the highest-fidelity path; ordinary API mimic stays semantically conservative.
- Fix cross-field Session consistency before lower-value cosmetic mimicry.
- Add an account-pool-only ordered HTTP/1.1 writer based on raw 2.1.207 fixtures.
- Treat software headers, platform, TLS, header order, and body rules as one versioned profile.
- Add JA3/JA4 and raw-header fixtures; do not rotate arbitrary public profiles.
- Do not inject the complete dynamic Claude Code prompt or built-in tools into ordinary API requests.
- Real smoke must use an account's bound proxy and send only one short request.

## Current Goal

Keep the account pool easy to operate: quota below true exhaustion should influence ordering rather than block new sessions, while cards and tables focus on concurrency and RPM. Active-session and sticky-reserve controls remain available as advanced protections.

## Current Phases

1. [completed] Compare the current behavior with sub2api and confirm the simplified scheduling semantics.
2. [completed] Remove pre-exhaustion quota blocking while preserving hard shared/model exhaustion and headroom ordering.
3. [completed] Simplify card/table capacity metrics and move session/sticky reserve controls to advanced/detail surfaces.
4. [completed] Update documentation and run focused, full, build, and responsive UI checks.

## Current Decisions

- Shared `five_hour` and `seven_day` exhaustion applies to the account; model windows apply only to their model family.
- Utilization below 100% remains schedulable and only participates in headroom ordering and low-quota UI warnings.
- `status=rejected`, `status=exhausted`, zero remaining, utilization at least 100%, or a persisted real 429 window is a hard routing exclusion.
- Sticky sessions remain on their primary account unless it is actually unavailable or exhausted.
- Cards and tables show concurrency and RPM only; active sessions, affinity bindings, and sticky reserve stay in account detail or advanced settings.
- Existing management fields remain compatible; this change does not remove the underlying MaxSessions or sticky concurrency protection.

## Current Errors Encountered

| Error | Attempt | Resolution |
|---|---:|---|
| Findings append expected a generic heading that the existing file does not use | 1 | Re-read the file; the detailed fingerprint findings were already present, so no duplicate section was added. |
| One-shot local CLI raw capture exited without connecting when only a dummy `ANTHROPIC_API_KEY` was supplied | 1 | Retain the already verified raw capture documented in findings; transport fixtures will assert that evidence without retrying the same invocation. |
| Smoke readiness query used the API name `schedulable` as a physical SQLite column | 1 | The compatibility schema still stores that flag as `enabled`; reran the join using `enabled` and confirmed one healthy bound account. |
| First real-smoke wrapper failed before sending: detached process exited, old Psych lacks `safe_load_file`, and zsh reserves `status` | 1 | No Anthropic request was made; switch to a persistent screen session, `YAML.safe_load(File.read(...))`, and a non-reserved response variable. |
| Existing header parser test expected legacy `model_7d_oi` | 1 | Update the fixture to canonical `seven_day_fable` and add active/passive Fable coverage. |
| Header-to-routing integration fixtures were blocked in new-account `checking` state | 1 | Clear the scoped lifecycle block in the test fixture before asserting quota-specific routing. |
| Fresh-start smoke reproduced `SQLITE_BUSY` while multiple startup components initialized the same new database | 1 | Serialize the same-process schema/import/migration phase in `resourcepool.Open` and add a 16-opener concurrent regression test. |

---

# Previous Task: Account Pool Lifecycle, Session Capacity, and Headroom

## Goal

Implement the approved Claude Code Account Pool lifecycle controller, immediate and periodic account probes, separate five-minute active-session capacity from one-hour affinity, quota headroom routing, and matching management UI without changing ordinary `/v1/*` behavior.

## Phases

1. [completed] Map the existing SQLite/auth-manager/router/UI state flow and lock migration boundaries.
2. [completed] Add persistent schedulability and health state plus unified probe/error transitions.
3. [completed] Split active-session idle capacity from affinity bindings and tighten waiter defaults.
4. [completed] Merge OAuth usage and response-header quota snapshots into headroom-aware selection.
5. [completed] Extend management APIs, UI, logs, documentation, and run full regression/smoke checks.

## Decisions

- New accounts are `checking` and do not route until their first OAuth usage probe succeeds.
- Paused accounts continue Token, quota, and proxy maintenance.
- Active sessions stop consuming `MaxSessions` after five idle minutes; affinity remains sliding one hour.
- Sticky reserve increases concurrency only, never RPM.
- Existing accounts migrate without a pool-wide checking blackout and are scheduled for immediate background review.
- SQLite owns persistent account-pool lifecycle state; existing auth/router state is the runtime projection.
- No inference heartbeat, Redis, prompt-hash session fallback, 120-second wait, or unlimited sticky RPM exemption.

## Errors Encountered

| Error | Attempt | Resolution |
|---|---:|---|

---

# Previous Task: Account Pool Pure Usage Semantics

## Goal

Make `pure_mode` the single downstream usage-cleaning switch for Claude Code Account Pool. Preserve upstream requests and raw usage, while removing account-pool mimic overhead from downstream input/cache usage fields.

## Phases

1. [completed] Unify `pure_mode` and the legacy clean-input toggle without breaking stored config compatibility.
2. [completed] Rewrite downstream non-stream, stream, nested iteration, and count-token usage including cache fields.
3. [completed] Simplify UI wording and retain calibration as a pure-mode support tool.
4. [completed] Update secondary-development documentation and run backend/frontend regression tests plus local smoke.

## Constraints

- Preserve all existing dirty SessionKey, scheduling, proxy, observability, profile, and UI changes.
- Do not modify the separate Claude API Pool virtual ledger or its `/v1/*` behavior.
- Never modify the upstream request or the raw Anthropic usage captured by the reporter/logs.
- Only account-pool `api-mimic` overhead is removable; real Claude Code passthrough usage stays unchanged.
- Preserve client-owned cache usage when the original request contains cache controls.
- Existing account-pool routes and management paths remain unchanged.

## Decisions

- `pure_mode` is authoritative; legacy `usage.clean_input_tokens` remains a compatibility alias only.
- For mimic requests without client cache controls, downstream cache creation/read fields are entirely gateway-owned and become zero.
- For requests with client cache controls, subtract only the calibrated/estimated injected overhead from cache buckets and retain the remainder.
- Raw usage remains available in the ledger, logs, and real-token management metrics.

## Errors Encountered

| Error | Attempt | Resolution |
|---|---:|---|
| Account-pool virtual-cache config is stored but never applied to the account-pool runtime | 1 | Remove it from account-pool config/UI; leave the separate Claude API Pool implementation untouched. |
| Two legacy Claude OAuth files predate SQLite account storage and have no pool marker | 1 | Use credential shape plus resource-pool enablement for one-way verified migration. |
| Management filter test declared two constants that resolve to the same map key | 1 | Kept the canonical `AttrOAuthPool` key in the fixture and reran the focused tests. |
| Isolated startup reported `auth filestore: directory not configured` | 1 | Added `AuthStore.SetBaseDir` forwarding so the wrapped file store can enumerate and migrate legacy files. |
| SQLite-backed auths have no source file path, so the account page omitted their runtime summary | 1 | Added an account-runtime fallback independent of the management auth-file entry builder and covered it with a regression test. |
| Focused tests still asserted the old independent `clean_input_tokens` switch | 1 | Updated the tests to make `pure_mode` authoritative and require an explicit runtime false attribute. |
| New migration test used `gjson` without importing it | 1 | Added the missing test-only import and reran the focused suite. |
| First-start smoke briefly hit SQLite busy while the health worker and auth store initialized the same new database | 1 | Serialized startup initialization in `resourcepool.Open`; a clean cold start and repeated concurrent-open regression test now pass. |
| Parallel frontend/backend smoke build embedded the previous console asset | 1 | Rebuilt the backend after the frontend asset copy, matching the repository build script order. |
| Temporary server shutdown timed out while the browser SSE tab was still open | 1 | Finalized the browser tab before the final shutdown; the next shutdown completed cleanly. |
# Current Task: SessionKey Cookie OAuth compatibility

- [completed] Compare our Cookie OAuth request shape with the working local sub2api implementation.
- [completed] Align the minimum required web request transport, headers, scope, and response handling without exposing credentials.
- [completed] Add synthetic tests for request shape and 401/403 classification.
- [completed] Run focused tests, full Go tests, formatting, required build verification, and an application-level live smoke.

Errors encountered:

- The first background start with plain `nohup` exited without binding a port; the verified binary is now running in a detached `screen` session.
- The stored management key is hashed and correctly failed direct Bearer use; the live job was submitted through the already authenticated local control panel instead.
# Current Task: Account test response and scoped quota compatibility

- [completed] Support current Anthropic JSON/SSE reply shapes in account tests and surface a useful warning for textless 200 responses.
- [completed] Parse the new OAuth usage `limits[].weekly_scoped` model quota structure without inventing absent Sonnet/Opus limits.
- [completed] Clarify model-specific quota absence in the account UI.
- [completed] Exclude unified `overage` eligibility from model quota routing and badges.
- [completed] Rebuild, restart, and verify Fable, Sonnet, and Opus through the local console.
- [completed] Run the final full regression suite.

# Current Task: Fable plan semantics and account-test availability

- [completed] Check current official plan and Fable fallback documentation.
- [completed] Stop exposing inactive Fable scoped limits as 100% remaining.
- [completed] Record management account tests in availability and usage accounting.
- [completed] Verify one-hour availability and shared Fable display through the local console.
- [completed] Run final full regression and rebuild the service.

# Current Task: Claude Code 2.1.207 fingerprint and bound-proxy smoke

- [completed] Align Session identity, MacOS/arm64 Stainless tuple, ordered HTTP/1.1 serialization, and JA3/JA4 trace evidence.
- [completed] Add OAuth credential beta without changing real Claude Code passthrough.
- [completed] Fix account-pool scope propagation through detached handler contexts.
- [completed] Synchronize quota-worker OAuth rotations from SQLite into runtime auth state.
- [completed] Verify count-tokens and one short Sonnet request through the account's bound proxy.
- [completed] Run final Go/backend/frontend regression and diff hygiene checks.

Errors encountered:

- Initial smoke requests selected ordinary Claude auth because `GetContextWithCancel` dropped the middleware pool scope; preserving known routing values fixed selection and restored routing/trace records.
- A zsh smoke helper used the reserved variable name `status`; the response was recovered from its temporary file and later scripts use `http_code`.
- A temporary package test resolved relative config paths from the package working directory; using the absolute main config path confirmed `GetStoredAuth` itself was correct.
# Current Task: Per-window quota confidence and freshness

## Goal

Expose source, observation time, freshness, and confidence independently for the 5h, 7d, Sonnet, Opus, and Fable quota windows without adding inference probes or changing routing semantics.

## Phases

1. [completed] Map persisted quota windows, management DTOs, and account card/detail rendering.
2. [completed] Add canonical per-window confidence/freshness derivation and API fields.
3. [completed] Render compact summaries and detailed source/time/confidence states responsively.
4. [completed] Add regression tests and run Go/frontend/build/browser verification.
5. [completed] Update secondary-development documentation.

## Constraints

- Continue using OAuth usage probes and passive Anthropic response headers only.
- Preserve existing headroom and hard-exhaustion routing behavior.
- Do not overwrite precise OAuth utilization when a later header only reports status/reset.
- Keep the account card compact; full provenance belongs in the detail drawer.

## Added Scope: Bounded routing-event history

- Rename the event timestamp column to `发生时间` and label the range selector as `事件范围`.
- Replace the current one-shot 80-row render with server-side pagination at 20 rows per page.
- Return total, limit, and offset from the management endpoint and reset the page when pool or time range changes.

## Errors Encountered

| Error | Attempt | Resolution |
|---|---:|---|
| Planning-file patch used one cross-file context block and failed verification | 1 | Re-read the file tails and applied each addition against exact local context. |
| Phase-status patch repeated the same malformed cross-file context pattern | 2 | Applied the status and progress additions with independent exact hunks. |
| Documentation patch matched an outdated wording variant | 1 | Located the exact current sentence and reapplied the documentation changes without altering surrounding user edits. |
