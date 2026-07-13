# Progress: Multi-Pool API Keys, Pricing, Usage, And Console

## 2026-07-13 Pool Config/Insights Resume

- Recovered the existing pool-config implementation and confirmed the approved design remains the active task.
- Formatted `internal/api/handlers/management/resource_pools.go`.
- Passed focused tests for resourcepool, claudeapipool, management handlers, and sdk/cliproxy.
- Mapped the quota confidence helpers, account schema, usage/routing ledgers, runtime pressure APIs, and current N+1 summary path.
- Started the batched model-capacity and health aggregation phase.

## 2026-07-13 Quota Confidence And Compact Cards

- Added persisted `utilization_known` semantics for OAuth usage, scoped limits, unified response Headers, legacy snapshots, merge behavior, and routing evaluation.
- Added focused tests for real zero utilization, scoped zero percentages, observed-only Headers, unknown exhaustion, legacy compatibility, and status-only merges over known OAuth data.
- Rebuilt account cards around one schedulability badge, compact 1h availability, shared/model quota, fixed load/usage metrics, proxy identity, and Test/overflow actions.
- Updated account table and detail quota rendering to distinguish exact, shared, observed, exhausted, and unknown values.
- Focused Go tests, `go test ./...`, the required Go build, frontend type-check/build, `git diff --check`, and responsive browser checks all passed.

## 2026-07-13 (Quota Confidence And Compact Cards)

- Confirmed the requested card content and inspected the current 300px card layout, quota helpers, availability strip, table, and drawer boundaries.
- Selected a compact operational layout with one scheduling badge, shared/model quota, a fixed metrics grid, one-line proxy identity, Test, and overflow actions.

## 2026-07-13 (Quota Accuracy Research)

- Started a read-only comparison of Anthropic/Claude Code quota surfaces, sub2api, this account pool, and representative open-source proxies.
- The research will distinguish authoritative utilization/reset data from passive response-header observations and local estimates.
- Confirmed the locally installed Claude Code binary is version `2.1.207`; it will be inspected only for field names and source precedence, without authentication or upstream requests.
- Verified official Claude Code `/usage` and Pro/Max documentation, the installed client claim names, local sub2api active/passive collection, and representative switchroom/claude-statusline/token-watch/howmuchleft implementations.
- Confirmed this backend already parses dynamic weekly-scoped Sonnet/Opus/Fable windows and merges unified headers; identified utilization-presence as the remaining accuracy gap for header-only observations.
- Completed the read-only research without changing runtime or UI behavior.

## 2026-07-13 (Prompt Cache TTL Research)

- Started a read-only comparison of official cache TTL semantics, real Claude Code traffic, sub2api, and this account-pool pipeline.
- Initial source audit shows this project currently models an omitted TTL as 5 minutes and enables extended 1-hour cache support only for an explicit `ttl:"1h"` body.
- Official API docs confirm omitted TTL defaults to 5 minutes and current 1-hour support no longer requires the old beta header. Current Claude Code docs further confirm subscription sessions automatically request 1h, overage drops to 5m, and API-key/cloud modes remain 5m unless explicitly overridden.
- Existing local Claude Code 2.1.207 recorder traces in API-key/custom-base mode send ephemeral breakpoints without `ttl`, therefore using 5-minute caches; they do not represent first-party subscription OAuth policy.
- Compared sub2api: it defaults to 5m and offers a disabled-by-default global switch that forces existing OAuth/SetupToken cache blocks to 1h; this is a policy override, not transparent passthrough.
- A dummy-key non-interactive opt-in capture did not reach the local recorder, matching the earlier known CLI limitation; it was stopped without contacting Anthropic and will not be repeated.
- Inspected the installed 2.1.207 executable: first-party OAuth main-thread and SDK query sources are allowlisted for explicit 1h TTL unless forced to 5m or running overage, explaining why API-key traces and current OAuth behavior can differ.
- Audited this project: real CLI passthrough preserves 1h, while ordinary mimic, management tests, calibration, and non-CLI count_tokens currently use omitted TTL and therefore 5m.
- Confirmed the current breakpoint-limit and TTL-order normalization run before mimic profile injection; this can leave more than four breakpoints or place a client 1h block after an injected 5m block.
- Found no official support for the claim that 1h prevents bans. Recommended a mimic-only 1h policy control, leaving strict passthrough and one-off management tests unchanged.
- Completed the research without changing runtime or UI behavior and without touching the user's service on port 28317.
- User confirmed the implementation policy: every account-pool cache breakpoint defaults to 1h, while a default-off switch optionally permits explicit client 5m/1h values.
- Added the config/runtime projection, final outbound TTL policy, post-profile breakpoint guards, management-test shape, System Settings control, and focused regression coverage.
- Focused resourcepool, executor, management, and frontend type-check suites passed.
- Full `go test ./...`, frontend production build, required backend build, and `git diff --check` passed.
- Isolated API smoke confirmed `allow_client_cache_ttl` defaults to false and saves as true through the UI/API; 375/768/1440 checks found no horizontal overflow, the help tooltip fit at 375px, and browser logs contained no warnings or errors.
- Stopped the isolated 28319 service and removed its temporary config, database, auth directory, and binary artifacts; the existing 28317 service remained untouched.
- Fixed pure-mode accounting when observed gateway cache tokens exceed the estimated profile overhead: gateway-owned cache buckets are now removed independently instead of moving the estimate shortfall into downstream `input_tokens`.
- A bound-proxy isolated smoke reproduced the old `583` visible input result and confirmed the fix returns `12` while the raw ledger still records `12` input plus `1620` cache-read tokens.

## 2026-07-13 (Persistent API Key Reveal)

- Confirmed the requested behavior: generated keys remain permanent and may be viewed/copied after the initial creation dialog.
- Chose an explicit reveal endpoint instead of returning secrets from list queries; legacy hash-only keys will require rotation once.
- Mapped the SQLite schema, lifecycle methods, Management API registration, frontend client, and responsive API Key actions.
- Added persistent secret storage for create/rotate, on-demand reveal, revoke-time clearing, and `secret_available` metadata without changing hash-based request authentication.
- Added desktop/mobile view-and-copy controls and replaced the one-time-only wording; focused resource-pool/management tests and frontend type-check pass.
- Full `go test ./...`, frontend build, required backend build, and diff checks pass.
- Isolated Management API smoke confirmed on-demand reveal returns `Cache-Control: no-store`; browser smoke confirmed desktop/mobile view and copy, the revised create dialog, no 375px overflow, and no console errors.
- Revoked the synthetic Key, cleared the test clipboard, stopped port `28319`, and removed the isolated database/config/binary while leaving the user service on `28317` untouched.

## 2026-07-12

- Accepted the strict one-account/one-pool design and renamed the immutable built-in pool to `default`.
- Read repository development notes and mapped SQLite, access middleware, scheduler scope, registration, usage plugin, and current console structure.
- Confirmed the dirty worktree contains approved prior account-pool work that must be preserved.
- Started phase 1 with schema/migration, pool membership, and runtime projection as the dependency boundary.
- Completed pool CRUD, strict account membership, pool-bound hashed keys, registration/move propagation, and child scheduler namespaces.
- Corrected quota/header and 529 tests to inspect `claude-acc-pool/default`; fixed cooldown, refresh recovery, removal cleanup, sticky wait, and prefix policy reads that still used the parent scope.
- Added scoped per-model route status inspection and reran focused resourcepool, routing, auth, management, and handler suites successfully.
- Audited the usage reporter/ledger path and established immutable price revisions with per-attempt cost accounting as the phase 3 design.

---

# Previous Progress: Proactive Model Quota Scheduling

## Claude Code Observable Fingerprint Research (2026-07-12)

- Started a code-level comparison of this account-pool transport, local sub2api, verified 2.1.207 traces, and relevant public projects.
- No runtime code changes are planned during this analysis phase.
- Captured a fresh local 2.1.207 record-only request using a dummy key and verified current body/header tuple without contacting Anthropic.
- Captured raw HTTP/1.1 header order and proved Go `net/http` serialization differs.
- Confirmed the mimic Session header/body mismatch from both source and existing outbound traces.
- Confirmed current ClientHello computes to sub2api's Node/macOS JA3 fixture and identified the OS/Arch deployment consistency gap.
- Tested a local 529 retry and confirmed the real CLI keeps retry-count zero and reuses the same Session.
- Reviewed sub2api, the current CLIProxyAPI upstream, and several public OAuth proxy implementations; no runtime code was changed.
- User approved implementation of the evidence-backed fingerprint changes and one real bound-proxy smoke request.
- Mimic requests now derive the Session header from the final metadata body, and the built-in profile pins MacOS/arm64 with revision `2.1.207-r2`.
- The official account-pool transport now uses req/v3 ordered HTTP/1.1 serialization over the existing uTLS/proxy dialer and connection cache.
- Trace schema v3 records Session consistency, raw header-name order, and TLS JA3/JA4/ALPN without recording Session values or authorization material.
- Focused executor, transport, trace, resource-pool, and recorder tests pass, including a raw TCP request-order fixture.
- Added credential-level OAuth beta handling for API mimic and management tests while preserving passthrough headers exactly.
- Found and fixed the handler context bug that dropped account-pool scope before Auth Manager selection; known routing context values now survive detached cancellation setup.
- Moved quota-worker startup behind runtime auth initialization and synchronized refreshed SQLite credentials back into the Auth Manager.
- Bound-proxy smoke now passes through `/claude-acc-pool/v1`: `count_tokens` returned 200 and a Sonnet message returned `OK.` with matching selected/success routing events, usage ledger data, and schema-v3 trace.

## 2026-07-12 (Current Task)

- Simplified pre-exhaustion routing after the sub2api comparison: legacy drain-only snapshots normalize to degraded, quota below true exhaustion remains schedulable, and hard rejection remains scoped to shared/model exhaustion.
- Reordered non-sticky candidates by normalized pressure, request-model headroom, prefix lane, and LRU; pressure now includes active-session occupancy in addition to concurrency and RPM.
- Simplified the resource console so cards/tables show concurrency and RPM, while active-session limits and affinity-only extra concurrency moved to detail/advanced surfaces with clearer terminology.
- Isolated API/UI smoke confirmed a fresh 96% Fable snapshot remains effectively schedulable and renders as a low-quota warning rather than sticky-only drain.
- The default routing form now exposes only capacity profile, RPM, concurrency, and switching; advanced settings contain the active-session soft limit and affinity-session extra concurrency, with no horizontal overflow at 375px.
- The 375px account card contains only concurrency/RPM request pressure, and the detail drawer shows active sessions plus affinity extra concurrency without page or drawer overflow; affinity binding remains available in diagnostics.
- Account table smoke confirmed the renamed load column contains only concurrency/RPM, with no session/reserve fields and no page overflow; browser console contained no warnings or errors.
- Final full Go tests, resource-console type-check/build, backend build, and diff hygiene checks passed. The isolated `28319` service and `.codex-capacity-smoke` artifacts were removed.

- Confirmed the desired behavior: proactively stop assigning new sessions before quota rejection, retain bounded sticky draining, and use 429 only as a fallback signal.
- Compared sub2api's active Fable usage, passive `7d_oi` sampling, model-family cooldown, and `7d F` UI against this project and official CLIProxyAPI.
- Confirmed official CLIProxyAPI latest upstream supports the Fable model but not account-pool quota probing or scheduling.
- Defined canonical shared/model window semantics and started mapping the current quota/error/config/UI implementation.
- Completed implementation mapping and selected a single-router extension with built-in 85/95/90 quota bands, canonical Fable windows, and persistent snapshot recovery.
- Added active Fable usage parsing, canonical `seven_day_fable` normalization, passive `7d_oi` fallback, millisecond reset parsing, and aggregate-reset fallback.
- Added proactive normal/degraded/drain-only/exhausted routing states with sticky draining, lower recovery thresholds, short local Retry-After, and Fable-family matching.
- Added shared-window and Fable-only integration tests proving account-level and model-level quota scope remain separate.
- Added management account quota-band fields and compact `7d S/O/F` console summaries; focused backend tests and frontend type-check pass.
- Added serialized SQLite store initialization and a concurrent-new-database regression test after isolated cold-start smoke reproduced `SQLITE_BUSY`; the test passed for 20 repeated runs and the rebuilt server cold-started cleanly.
- Isolated API/UI smoke confirmed Fable 96% maps to sticky-only drain, shared 5h/7d remain normal, all five quota windows render, and account cards have no horizontal overflow at 375/768/1024/1440 widths.
- Narrow account-detail smoke confirmed all five windows remain present without drawer or page overflow; browser console reported no warnings or errors.
- Final `go test ./...`, resource-console type-check/build, backend build, and `git diff --check` passed. The isolated `28319` service and all `.codex-quota-smoke` artifacts were removed.

---

# Previous Progress: Account Pool Lifecycle, Session Capacity, and Headroom

## 2026-07-12 (Current Task)

- Approved the complete lifecycle, active-session, headroom, API/UI, migration, and test plan.
- Confirmed the dirty worktree contains the scheduling, SessionKey, profile, usage-cleaning, and UI work that must be preserved.
- Confirmed the current quota worker, registration path, account-pool runtime error policy, and sub2api session-capacity behavior.
- Selected SQLite as the persistent lifecycle source and the existing Auth Manager/router as its runtime projection.
- Added persistent schedulable/health lifecycle fields, compatibility migration, a unified transition path, and manual-recovery protection.
- New OAuth and SessionKey registrations now enter checking and launch a concurrency-limited immediate usage probe.
- Reworked the quota worker around per-account due times with jitter; paused accounts remain maintained and manual-recovery accounts are skipped.
- Split five-minute active-session capacity from one-hour affinity bindings and reduced the default per-account waiter limit to five.
- Added dynamic unified rate-limit header parsing, fresh/stale quota snapshots, model-aware headroom and same-pressure-band routing preference.
- Updated account management API/UI with lifecycle status, explicit recovery, quota source/freshness/headroom, and separate active-session/affinity metrics.
- Added focused lifecycle, active-session, headroom and response-header tests; focused backend packages and frontend type-check pass.
- Completed full backend tests, server build, frontend type-check/build, and diff hygiene checks.
- Isolated API smoke confirmed the new five-waiter and five-minute active-session defaults without contacting Anthropic.
- Browser smoke covered 375/768/1024/1440 widths with no page-level overflow or console errors; narrow segmented tabs now keep the active tab visible.
- Stopped the isolated server and removed all temporary smoke configuration, database, and auth-directory artifacts.

---

# Previous Progress: Account Pool Pure Usage Semantics

## 2026-07-12

- Confirmed the intended definition: pure mode changes only downstream-visible usage and never changes upstream requests or raw Anthropic accounting.
- Mapped non-stream, streaming, nested iteration, count-token, runtime attribute, ledger, calibration, and UI paths.
- Selected a compatibility design where `pure_mode` is authoritative and the legacy clean-input field remains an API/storage alias.
- Implemented total-input cleaning across regular input, cache creation, cache reads, 5m/1h breakdowns, nested iterations, streams, and count-token responses.
- Added passthrough protection and client-owned cache preservation.
- Removed the second UI toggle, clarified the pure-mode wording, and retained per-model overhead calibration.
- Added the account-pool pure usage v4 SQLite config migration.
- Full `go test ./...`, frontend type-check, frontend production build, and backend build passed.
- Isolated API smoke confirmed contradictory legacy config migrates to `pure_mode=true` with its compatibility alias synchronized.
- Browser smoke confirmed the single pure billing toggle and calibration wording at 1280px with no page overflow or console errors.
- Removed the final legacy ledger-side input rewrite so every stored usage field remains raw; pure cleaning now exists only in downstream responses.

- Confirmed the account-pool virtual-ledger panel is disconnected from account-pool runtime behavior.
- Confirmed the separate Claude API Pool virtual ledger is independent and must not be removed.
- Located two legacy file-backed Claude OAuth credentials and confirmed no matching SQLite account rows exist yet.
- Confirmed new marked account-pool credentials already persist through `resourcepool.AuthStore` into SQLite.
- Confirmed management auth files and ordinary Claude routing do not currently isolate account-pool auths.
- Defined verified migration boundaries and started backend implementation.
- Implemented idempotent legacy Claude OAuth migration in `resourcepool.AuthStore.List`.
- Added SQLite token verification before legacy source deletion and Auth ID deduplication.
- Added tests proving OAuth migration and API-key exclusion; `go test ./internal/resourcepool` passed.
- Added management auth-file filtering and ordinary-route isolation with focused tests.
- Removed account-pool virtual-cache config/API/UI while preserving the separate Claude API Pool ledger.
- Added the config v3 migration that rewrites legacy account-pool JSON without `virtual_cache`.
- Simplified routing settings into common controls and a collapsed advanced section.
- Removed the editable request-cloak panel; passthrough/mimic remains visible through the read-only profile.
- Updated the embedded resource console and secondary-development documentation.
- Forwarded the configured auth directory through `resourcepool.AuthStore`, then completed an isolated legacy-file migration smoke with two fake-token copies.
- Confirmed both migrated SQLite accounts remain active in the runtime Auth Manager while the management auth-file list returns zero account-pool entries.
- Added a runtime-summary fallback for SQLite-backed account-pool auths, which intentionally have no file path.
- Browser-checked the simplified configuration page at 1280px: no virtual-ledger panel, no editable cloak panel, no page overflow, and no console errors.
- Final `go test ./...` and resource-console type-check passed.

## Prior 2.1.207 Progress

## 2026-07-11 (2.1.207 alignment)

- Confirmed the approved implementation plan and locally captured 2.1.207 baseline.
- Read repository and secondary-development instructions.
- Inspected the resumed dirty worktree and preserved earlier SessionKey, scheduling, proxy, and UI changes.
- Resumed partially implemented early passthrough detection and profile updates.
- Implemented strict real Claude Code passthrough and minimal ordinary-request mimic behavior.
- Upgraded the built-in profile to `2.1.207-r1`, added legacy builtin migration, and made clean-input overhead profile-derived.
- Added the account-pool-only Node HTTP/1.1 uTLS transport and bounded account/proxy/revision connection cache.
- Extended Phistory snapshots with homepage manifest discovery, static/full prompt metadata, and request-kind summaries.
- Extended trace capture/diff and recorder synthetic responses.
- Updated the resource console profile and snapshot views.
- Added focused passthrough, beta, session, TLS, migration, Phistory, request-kind, and recorder tests.
- `go test ./...` passed after the implementation changes.
- `go build -o test-output ./cmd/server` passed and the temporary binary was removed.
- `npm run type-check --prefix web/resource-console` and `npm run build --prefix web/resource-console` passed; the embedded console was regenerated.
- Record-only recorder smoke returned valid HTTP 200 JSON, SSE, and count_tokens responses and wrote exactly three traces with the expected request kinds.

## Previous Scheduling Work

## 2026-07-11

- Read repository instructions and secondary-development documentation.
- Confirmed dirty worktree contains existing SessionKey and UI changes.
- Created implementation tracking files.
- Mapped normal, count-token, and stream account-pool execution paths.
- Confirmed scoped configuration, affinity, capacity, retry accounting, and upstream-header gaps.
- Implemented account-pool-only atomic session-aware scheduler and explicit session extraction.
- Implemented sticky concurrency reserve, MaxSessions, event-driven waits, queue limits, and per-attempt RPM.
- Implemented typed routing errors, account/model/proxy cooldown separation, reset-header parsing, and auth recovery.
- Extended SQLite routing events, account runtime capacity, JSONL logs, management UI, and routing v2 migration.
- Added focused scheduler, error-policy, metadata extraction, and response-header tests.
- Updated secondary-development log and usage documentation.

## Verification

| Command | Result |
|---|---|
| `go test ./internal/claudeapipool ./internal/resourcepool ./sdk/cliproxy/auth ./internal/runtime/executor ./internal/api/handlers/management` | Passed |
| `npm run type-check --prefix web/resource-console` | Passed |
| `go test ./...` | Passed |
| `go build -o test-output ./cmd/server && rm -f test-output` | Passed |
| `npm run build --prefix web/resource-console` | Passed |

## Local Smoke

- Started a temporary server on `127.0.0.1:28318` without touching the user's service on `28317`.
- Management endpoints for accounts, route status, stats, events, logs, models, usage, and config returned HTTP 200.
- Browser-checked the account cards, account detail drawer, runtime metrics, routing configuration, and routing-event table.
- Confirmed conservative defaults render as 6 RPM, 1 base concurrency, 1 sticky reserve, 30 sessions, 2s sticky wait, and 500ms fallback wait.
- Confirmed account and model cooling are presented separately and the account detail capacity shows concurrency/RPM and sessions/waiters.
- At a 1200px viewport, document and body widths matched the viewport with no page-level horizontal overflow; browser console contained no errors.
- Real upstream smoke was stopped because the existing bound SOCKS proxy rejected username/password authentication and OAuth refresh returned HTTP 403. No repeated upstream pressure was generated.
- Stopped the temporary server and removed its temporary configuration.
# Multi-Pool API Keys, Pricing, Usage, And Console (2026-07-12)

- Resumed the approved implementation with the SQLite pool/key/pricing/usage backend and initial multi-page console already present.
- Confirmed the integrated frontend type-check passes.
- Added account movement from card, table, and detail surfaces, including target-pool selection and the historical-usage retention warning.
- Added account-level 30-day requests/attempts, token composition, estimated cost, unpriced count, and pricing coverage to the detail drawer; account tables now include compact 30-day usage.
- Audited the auth-selection boundary and confirmed pool membership and scheduler namespaces prevent cross-pool fallback.
- Tightened generated key extraction to accept only Bearer and `x-api-key`, with raw Authorization, Basic, and query-string regression cases.
- Completed the resource-console split into overview, pools, pool details, API keys, proxies, models/pricing, and settings, including responsive mobile rows and API Key secret handling.
- Revoked both synthetic Team Alpha keys and removed every temporary plaintext credential artifact from the isolated UI smoke environment.
- Found and fixed a real legacy-database migration ordering bug: pool indexes now run only after `pool_id/api_key_id` columns exist; the new legacy-table regression test passes.
- Completed one isolated real bound-proxy request with a temporary `default` pool key: HTTP 200, one request/one attempt, matching pool/key/account routing events, bound-proxy confirmation, priced Revision 1 ledger cost, account-card availability, and SSE refresh.
- Revoked the real-smoke key, deleted its plaintext response and complete copied database environment, and confirmed the existing service on port 28317 was untouched.
- Final `gofmt -w .`, `go test ./...`, resource-console type-check/build, required backend build, `git diff --check`, and 375/768/1024/1440 browser checks pass. The latest isolated console is running on port 28319.

# Pool Config Inheritance, Model Capacity, And Health (2026-07-13)

- Read repository and secondary-development instructions and preserved the existing dirty multi-pool/quota/UI worktree.
- Mapped the current global configuration, account-capacity overrides, per-pool scheduler namespaces, quota evaluator, pool summaries/stats, and React pages.
- Selected sparse pool overrides, batch-derived model capacity, and an explainable four-component observational health score with no additional Anthropic requests.
- Added the v9 sparse pool-config migration, GET/PATCH management contract, effective runtime projection, source metadata, and explicit `null` inheritance reset semantics.
- Added batched Sonnet/Opus/Fable relative-capacity aggregation and four-component pool/global health scoring with low-sample and quota-coverage confidence scaling.
- Reworked pool list/stats handlers to reuse one operational snapshot and removed account-hydration N+1 work from summary paths.
- Added accessible semicircle/ring gauges, global health distribution, sortable pool health, compact S/O/F capacity, pool-detail components/issues, and responsive pool rows.
- Added the pool Strategy tab with effective/global/source values, staged edits, per-field inheritance reset, reset-all, and SSE/query invalidation wiring.
- Added focused Store, runtime projection, Management API, capacity confidence, reliability, special-state, missing-data, and global-aggregation tests; focused tests and frontend production build pass.
- Completed Strategy interaction QA against the isolated copied database: RPM saved as a pool override and then returned to inherited global value through the per-field reset action.
- Verified the Strategy page at 375/768/1024/1440 has no document-level horizontal overflow and all 29 form controls have accessible names.
- Fixed mobile pool-detail deep links so the selected Strategy tab is aligned into the horizontal tab viewport after pool data loads.
- Rechecked the global health overview at desktop and mobile widths: the gauge is nonblank, controls remain in bounds, and the browser console is clean.
- Final browser matrix passed at 375/768/1024/1440: document width equals viewport width, the active Strategy tab is visible, and all 29 form controls remain named.
- Final `go test ./...`, required backend build, frontend type-check/build, Go formatting check, and `git diff --check` passed.
- Closed the isolated browser tab, reset the viewport, stopped port 28319, and removed `.codex-health-smoke`; the production service and database were not touched.

# SessionKey Cookie OAuth compatibility (2026-07-12)

- Read secondary-development docs and current SessionKey implementation.
- Located the working sub2api Cookie OAuth repository/service implementation.
- Identified browser impersonation and overly broad 403 classification as the first concrete differences.
- Confirmed OAuth scope, endpoint paths, client ID, redirect URI, PKCE fields, and token exchange body already match sub2api.
- Confirmed the existing auth transport applies Chrome uTLS and the selected proxy, but does not yet reproduce sub2api's browser HTTP headers.
- Added a dedicated req/v3 Chrome 120 client for SessionKey OAuth only, plus exact browser headers and sanitized 401/403/challenge classification.
- Added synthetic request-shape, Cookie-boundary, retry, and error-classification tests; focused auth, management, and proxy tests pass.
- Completed an authorized live smoke against the same SOCKS5 proxy; full standard OAuth token acquisition passed without persisting or printing credentials. The temporary smoke test was removed afterward.
- Completed the application-level batch login: one account was added, bound to the reserved proxy, passed its immediate usage probe, and became healthy. Exact credential scanning found no SessionKey in SQLite, auth files, or account-pool logs.
- Full Go tests, frontend type-check/build, backend build, and diff checks pass. The updated service is running on port 28317.
# Account test and scoped quota compatibility (2026-07-12)

- Parsed the supplied management response and confirmed the two independent compatibility gaps.
- Verified persisted model tests: Fable and Opus requests returned HTTP 200; no Sonnet test result has been recorded for this account yet.
- Added JSON, nested-message, SSE delta, legacy completion, and sanitized empty-response handling to account tests.
- Added `limits[].weekly_scoped` parsing and Fable quota support; missing Sonnet/Opus windows render as shared seven-day quota.
- Live smoke confirmed Sonnet 4.6 and Opus 4.8 return text, while Fable returns HTTP 200 with `stop_reason=refusal` and no text.
- Reclassified unified `overage` as extra-usage eligibility so `overage=rejected` no longer produces a false model-quota warning.
- Focused tests, `go test ./...`, frontend type-check/build, required backend build, and diff checks pass; the rebuilt service is running on port 28317.

# Fable plan semantics and management-test availability (2026-07-12)

- Checked current Anthropic Help Center plan and Fable fallback documentation; no fixed Fable/Opus 50% sharing rule is documented.
- Inactive scoped usage limits are now omitted, while active scoped limits and fresh response-header fallbacks remain supported.
- Management account tests now persist actual status and response usage so the one-hour availability strip reflects them.
- Live smoke confirmed a Haiku management test produces a `1/1` healthy bucket and real token totals; a fresh usage probe renders Fable as shared for this account.
- Full Go tests, frontend type-check/build, required backend build, and diff checks passed.
# Permanent API Key And Pool List Follow-up (2026-07-13)

- Confirmed the requested scope: permanent generated keys, lifetime usage on API Key/pool list pages, and direct pool-to-accounts navigation.
- Mapped the backend expiry checks, management payloads, shared React Query usage window, and existing pool-name click path.
- Added the permanent-key v8 migration, removed expiry from create/patch/auth/public JSON behavior, and replaced expiry tests with permanent-key coverage.
- Removed usage-window controls from API Key and pool-list pages, fixed both queries to `window=all`, and routed pool selection directly to the accounts tab.
- Updated secondary-development documentation; focused and full Go tests, frontend type-check/build, required backend build, and diff hygiene checks pass.
- Verified the pool list at 1440px and the API Key create flow at 375px: no list time-range controls, no expiration input, no horizontal overflow, and no browser console errors.
- Verified clicking a pool opens its scoped account list at `#/pools/<pool-id>/accounts`.
- Verified the idempotent v8 migration clears legacy expiry values on an isolated SQLite database.
# Current Task: Per-window quota confidence and freshness

- Started mapping the existing quota parser, persisted `QuotaWindow` evidence, account management response, and React account views.
- Confirmed the change can remain additive and requires no new probing or database migration.
- Chosen API shape: add derived `quota_window_states` to each account rather than modifying persisted raw quota windows.
- Chosen UI behavior: cards show only compact current state; the account drawer always renders five provenance rows with confidence, freshness, source, observed time, and reset.
- Added the derived five-window API view and regression coverage for exact, shared, observed-only, stale, exhausted, elapsed-reset, and missing states; focused resource-pool tests pass.
- Added the reported routing-event table growth issue to scope; selected 20-row server-side pagination rather than an internal scroll region.
- Updated cards and tables to use compact per-window states; the account drawer now always shows five rows with confidence, freshness, source, observation time, reset, and last known value.
- Added 20-row routing-event pagination with total counts, an explicit event-range label, a `发生时间` column, and non-wrapping load metrics; focused backend tests and frontend type-check pass.
- Completed responsive browser verification at 375/768/1024/1440: event pages and quota drawers have no horizontal overflow, all five quota provenance rows remain available, and load metrics stay on one line where displayed.
- Verified server-side event pagination against 28 copied events: page 1 renders 20 rows, page 2 renders rows 21-28, and previous/next controls enable and disable correctly.
- Confirmed the resource-console browser log contains no errors, reset the temporary viewport, closed the isolated browser tabs, and stopped the isolated `28319` service.
- Final focused/full Go tests, frontend type-check/build, required backend build, and `git diff --check` pass.
