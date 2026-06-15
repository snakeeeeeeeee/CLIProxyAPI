# Claude API Pool Implementation Plan

## Goal
Implement `claude-api-pool` as a separate file-backed Claude API account pool with backend synthesis, management APIs, and a table-based Management Center page.

## Phases
1. Backend config and pool file model - complete
2. Runtime synthesis and model registration - complete
3. Management API for paged CRUD/import/export - complete
4. Claude pool routing and usage ledger hooks - complete
5. Management Center table UI - complete
6. Verification and cleanup - complete
7. Claude-like virtual cache ledger upgrade - complete
8. Sliding-window target cache reuse tuning - superseded
9. SQLite primary store migration - complete
10. Local-ledger-only virtual cache rewrite - superseded
11. Upstream-total-anchored virtual cache rewrite - complete
12. Growth-based virtual cache read/write split - complete
13. Claude pool real-cache affinity routing, metrics, and UI monitoring - complete

## Decisions
- Main config only stores `claude-api-pool.enabled` and `claude-api-pool.path`.
- Pool item identity is file order plus `item_hash`; no manual item id.
- Runtime auth IDs are stable hashes of `api-key + base-url`.
- First UI version is a dense table with pagination, filters, import/export, and drawer-style row editing.
- Virtual cache ledger now models rolling Claude cache behavior with 5m/1h buckets, cache-read plus delta cache-write on growing contexts, and context-shrink reset.
- `target-cache-reuse-ratio` is kept as a target/stat signal, not a hard per-request split. Warm virtual-cache rewrites now read the local cached budget first and create only the growth remainder.
- Claude API Pool now uses fixed SQLite primary storage at `claude-api-pool.db`; `claude-api-pool.yaml` remains the import/export format and first-run migration source.
- Virtual cache usage rewrite anchors `input_tokens + cache_creation_input_tokens + cache_read_input_tokens` to the real Claude upstream total when present. The local ledger only splits that total into input/create/read, preserving upstream `input_tokens` when upstream cache fields exist and using local request delta only when upstream reports no cache split.
- Real-cache affinity routing is separate from downstream virtual-cache rewriting. It should influence which pool account is used upstream, but must not alter the downstream virtual cache ledger result.

## Errors Encountered
| Error | Attempt | Resolution |
| --- | --- | --- |
| `context` undefined in management handler | Backend build 1 | Added missing `context` import. |
| ledger SSE test parsed a `data:` line as raw JSON | Backend targeted tests 1 | Stripped SSE prefix in the test assertion. |
| `go test ./internal/runtime/executor` has unrelated Antigravity URL expectation failures | Backend broad package test 1 | Ran targeted executor tests for Claude/virtual cache; will report broad-package residual if still present. |
| Management Center `npm run type-check` could not find `tsc` | Frontend verification 1 | Ran `npm install` from the existing package-lock, then type-check/build passed. |
| Live smoke upstream returned `403 Request not allowed` | Claude pool smoke 1 | Confirmed local service, pool load, model listing, affinity key creation, and failure metrics; stopped after one real request to avoid repeated upstream calls. |

# Resource Pools Implementation Plan

## Goal
Implement a SQLite-backed Claude Code OAuth account pool plus an independent proxy IP pool with proxy health checks, 1:1 account/proxy binding, OAuth login integration, Claude pool routing reuse, and an independent Chinese management console.

## Phases
1. Resource pool config, YAML defaults, SQLite schema, and store APIs - complete
2. Proxy CRUD, import, availability selector, unique binding rules, and tests - complete
3. Proxy health checker worker and manual test endpoint - complete
4. Claude Code account APIs and OAuth registration/binding integration - complete
5. Runtime auth overlay and Claude OAuth pool routing reuse - complete
6. Independent Chinese console page and static route - complete
7. Build/test verification and cleanup - complete
8. Resource console launcher, hash routes, and management-key login reuse - complete
9. OAuth manual callback wizard and stricter unauthenticated redirect - complete
10. Proxy selection modal and proxy pool batch operations - complete
11. Claude account pool dedicated API, models, routing config, and dashboard controls - complete
12. Resource console SSE change notifications - complete
13. Claude Code account quota snapshots, refresh API, and UI display - complete

## Decisions
- Main `config.yaml` only gets `resource-pools.enabled` and `resource-pools.config-file`.
- `resource-pools.yaml` is first-run initialization input; after SQLite has rows/config, SQLite is the source of truth.
- The proxy pool stores proxy URLs with static exit-IP notes, not raw IP addresses.
- Binding is 1:1 through a unique `claude_code_accounts.proxy_resource_id`.
- OAuth pool accounts are marked with `claude_oauth_pool=true`, while existing Claude API Pool UI keeps using `claude_api_pool=true`.
- Virtual cache rewriting remains API-pool-only for now; OAuth pool reuses conservative routing, cooldown, same-account retry, and affinity behavior.
- The first UI pass is an independent embedded console route to minimize upstream Management Center conflicts. It uses Chinese copy and shadcn-like dense admin styling without changing `/management.html`.
- `management.html#/` remains the primary login entry. The resource console reuses the Management Center `cli-proxy-auth` local storage format when available, and otherwise shows its own fallback login page at `/account-pool.html#/login`.
- Resource pool navigation is hash-based: `/account-pool.html#/` launcher, `/account-pool.html#/accounts`, and `/account-pool.html#/proxies`.
- Claude Code OAuth in the resource console should use a persistent wizard: generate URL, copy/open it, then submit the callback URL into the existing management OAuth callback API.
- Proxy selection in account/OAuth flows should use a compact modal table rather than a native select, so health, latency, exit IP, and last check state are visible before choosing.
- Proxy pool batch operations should use a dedicated management API endpoint for test/enable/disable/unbind/delete to avoid fragile client-side request loops.
- Claude Code account pool must expose a dedicated public API at `/claude-acc-pool/v1` that only selects `claude_oauth_pool=true` auths and leaves global `/v1` unchanged.
- Claude Code account pool models live in SQLite, support real model name plus external alias, and drive `/claude-acc-pool/v1/models` plus request alias rewriting.
- Claude Code account pool UI should mirror the useful Claude API Pool controls: runtime metrics, enabled/pure mode, virtual cache controls, routing protection, models, account batch actions.
- Resource console live refresh uses SSE at `/v0/management/resource-pools/events`; events only identify the changed resource class and never include full proxy/auth rows.
- Claude Code account quota snapshots should use Anthropic OAuth usage data, store only summarized windows plus raw response in SQLite, and never disable/delete/unbind accounts on refresh failure.

## Errors Encountered
| Error | Attempt | Resolution |
| --- | --- | --- |
| Temporary smoke server started warning-only mode | Browser smoke 1 | Replaced example API keys in the temporary config with a non-example smoke key. |
| Browser initially served cached console HTML | Browser smoke 1 | Forced a cache-busting URL and confirmed the rebuilt embedded console was served. |

# Claude Code Pure Account Pool Enhancement Plan

## Goal
Implement the first three phases of the pure Claude Code account pool enhancement without adding payment, subscription, user-group, balance, or billing-deduction features. The dedicated `/claude-acc-pool/v1` API should look closer to real Claude Code CLI traffic, expose account capacity and model-level health, and improve operations visibility in the new resource console.

## Phases
1. Claude Code profile and request-shape injection - complete
2. Account capacity, sticky observability, and model-level health backend - complete
3. Routing events, usage ledger, and resource console observability UI - complete
4. Build/test verification and cleanup - complete

## Decisions
- Keep the public prefix fixed at `/claude-acc-pool/v1`; leave global `/v1/*` behavior unchanged.
- Keep all changes concentrated in `internal/resourcepool`, scoped Claude routing/executor integration, and `web/resource-console` where possible.
- Use a default Claude Code profile version of `2.1.177`; phistory/sub2api are references only, not automatic profile update sources.
- `metadata.user_id` is account-bound and stable, not API-consumer-bound.
- Capacity and usage tracking are observability/routing controls only; no billing deduction or SaaS user system.

## Errors Encountered
| Error | Attempt | Resolution |
| --- | --- | --- |

# Claude Code Clean Input Tokens Plan

## Goal
Add an optional Claude Code Account Pool-only clean input token display mode. When enabled, `/claude-acc-pool/v1/*` responses and resource-pool usage display subtract the built-in Claude Code prompt/profile overhead from visible input-token counts, while upstream Anthropic requests, real usage reporting, quota, cooldown, and rate-limit behavior remain unchanged.

## Phases
1. Config, profile fingerprint, SQLite calibration schema, and store APIs - complete
2. Management APIs for calibration state and manual model calibration - complete
3. Runtime Claude response/SSE usage rewrite scoped to Claude Code Account Pool - complete
4. Resource console config tab toggle and calibration UI - complete
5. Build/test verification and cleanup - complete

## Decisions
- `usage.clean-input-tokens` defaults to false.
- Runtime uses `model + profile_fingerprint` calibrated overhead when present; otherwise it falls back to the current observed estimate of 1909 tokens.
- Cleaned input tokens are display/billing-facing only. The upstream request and internal real usage reporter keep Anthropic's original numbers.
- The minimum cleaned value is 1 when the original upstream input count is positive.
- Profile fingerprint includes the locked Claude Code profile shape plus the built-in system prompt sources so old calibration values naturally stop matching after a profile/prompt change.

## Errors Encountered
| Error | Attempt | Resolution |
| --- | --- | --- |
