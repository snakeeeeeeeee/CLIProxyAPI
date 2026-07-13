# Findings: Multi-Pool API Keys, Pricing, Usage, And Console

## Account-Pool Protocol Consistency Implementation Baseline (2026-07-14)

- The approved work starts from a cleanly formatted dirty worktree containing four intentional downstream fixes: pure-mode visible usage, `count_tokens` visible-input correction, explicit prompt-cache preservation, and Anthropic-compatible error envelopes.
- The implementation must not rewrite or discard those changes; `git diff --check` passes at baseline.
- The existing design review established that account-pool Session extraction currently accepts `X-Client-Request-Id` in one path and creates a random UUID for every no-Session request, while generic scheduler extraction uses a different source set.
- The existing built-in profile is `2.1.207-r2`; observed HTTP/1.1 order differs, but existing JA3/JA4/ALPN remains intentionally unchanged because the redacting MITM did not capture native TLS.
- Quota scheduling has a 15-second due-account scanner but still applies hard-coded five-minute follow-up scheduling in some success/passive-update paths; configured `account-quota.interval` must become authoritative.
- Claude OAuth uTLS currently initializes a direct dialer before parsing proxy configuration, so malformed configured proxies can silently become direct traffic. This is the highest-priority network correctness defect.
- No valid authorized account is available, so all implementation can be verified offline/local except the final bound-proxy Anthropic smoke.
- Session extraction is now centralized in `sdk/cliproxy/auth` and supports Claude metadata, execution metadata, `X-Session-ID`, `Session-Id`, `X-Amp-Thread-Id`, the Claude Session Header, `conversation_id`, and payload `session_id`; it intentionally excludes `X-Client-Request-Id`.
- Explicit ordinary Sessions are deterministic by pool, non-secret key identity, and conversation, so account failover does not alter them. Temporary Sessions additionally include selected-account identity and use the existing one-hour sliding cache without creating scheduler affinity.
- Generated pool keys contribute only their database ID. Legacy config keys contribute a request-local SHA-256 digest that is neither persisted nor returned.
- Account-pool Header construction now bypasses the generic access-token-keyed Session cache, removing Token-refresh churn and unrelated KV dependencies from this path.
- The built-in profile is now `2.1.207-r3`. Its ordered HTTP/1.1 fixture places Authorization second, uses the observed Stainless `arch/lang/os/package/retry/runtime/runtime-version/timeout` order, and places beta before dangerous-browser-access/version while preserving the existing TLS JA3/JA4/ALPN tuple.
- Ordinary mode still emits three stable system blocks, but the third block now explicitly uses only capabilities present in the client request and makes no claim that Claude Code built-in tools or a local harness exist. Client system/cache content and actual client tools remain independent request data.
- Account-pool beta construction is now authoritative: ordinary requests use supported client/body capabilities plus the OAuth credential beta; real Claude Code passthrough keeps inbound order and inserts only the required OAuth beta. Empty final sets remove generic defaults.
- The r3 migration compares the complete prior r2 built-in profile rather than revision alone. Exact r2 baselines upgrade and recompute default overhead; modified Headers/prompts remain r2. Calibrated rows for the old profile fingerprint become stale without rewriting historical usage.
- Billing suffix generation remains the existing text/version-dependent algorithm; deterministic fixtures now cover multiple texts and a version change.
- Configured quota intervals now drive successful active probes, passive Header updates, and inference-success rescheduling. The scheduler only scans due accounts every 15 seconds, and shortening the interval can pull forward a previously fixed five-minute deadline.
- Manual/background quota refreshes share a per-database/account singleflight. OAuth usage requests use the account Profile UA, OAuth beta, Claude Code HTTP/1.1 transport, and bound proxy without Session or inference payload fields.
- Invalid configured proxies now install an error dialer instead of retaining a direct fallback across OAuth and protected uTLS paths.
- `pool_config` now owns a persistent random database instance ID. Diagnostics expose only a short hash; copying SQLite preserves it while a new database receives a different identity.
- The Management-only diagnostics response and System Settings panel expose build, database, Profile/transport, quota scheduler, short account/device hashes, safe proxy resource IDs, observed exit IPs, timing, probe category, and normalized issue codes. They exclude credentials, raw Session/Key values, proxy URLs, email, Auth IDs, and raw errors.
- Populated responsive QA showed that fixed pixel table columns can silently collapse the final issue column at 1024px once the desktop sidebar is present. Percentage columns plus a non-wrapping status badge retain all six fields without document overflow.
- A stale browser management key can fan out across many enabled React queries and quickly hit the existing in-memory management IP ban. This is an existing console/login behavior outside the protocol-consistency scope, not a diagnostics endpoint failure.

## Real Claude Code 2.1.207 Live Capture (2026-07-14)

- The locally installed CLI is Claude Code `2.1.207`; `claude auth status --json` reports an OAuth token and the first-party provider.
- The first isolated safe-mode request was not a valid direct-Anthropic baseline. User settings injected `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_BASE_URL`, and `ANTHROPIC_REASONING_MODEL`; the debug log explicitly showed a non-Anthropic custom Base URL.
- `--safe-mode`, an empty tool set, and strict empty MCP configuration do not suppress `settings.json` environment injection. A direct baseline must also exclude user setting sources or explicitly neutralize these settings.
- That custom-base sample returned one successful Opus response with 25 uncached input tokens, 2,566 five-minute cache-write tokens, 18,358 cache-read tokens, and 4 output tokens. It is evidence of the local configured gateway path, not evidence of first-party subscription cache policy.
- The CLI debug log recorded two concurrent `/v1/messages` calls for one print-mode turn: `source=generate_session_title` and `source=sdk`. The returned `iterations` usage represented the SDK message only. This background title request is real client behavior but should not be copied into an API gateway merely to imitate traffic.
- Claude's built-in API debug category exposes request paths and sources but not complete request Headers or bodies, so it is insufficient by itself for the planned structural comparison.
- A redacting MITM capture of the configured custom-base path succeeded without altering the response. It recorded only ordered Header names, allowlisted non-secret values, hashes/lengths for identifiers and text blocks, cache positions, numeric usage, and SSE event types.
- The main SDK request used HTTP/1.1 and the expected `claude-cli/2.1.207 (external, sdk-cli)`, Stainless 0.94.0, Node v26.3.0, MacOS/arm64 tuple. Its Session Header was 36 characters and was shared with the concurrent title request.
- The live ordered request Headers differ from the repository's pinned `ClaudeCodeNodeHeaderOrder`: Authorization is second in the live custom-base request, Stainless fields are ordered arch/lang/os/package/retry/runtime/runtime-version/timeout, and `anthropic-beta` precedes dangerous-direct-browser/version. The pinned order puts Authorization near the end and orders several Stainless and Anthropic Headers differently.
- The main body had exactly three system blocks: a 74-character billing block, the 62-character Agent SDK identity block, and a 3,168-character Claude Code block. The Agent SDK and Claude Code blocks used ephemeral cache controls with omitted TTL; a user-message cache breakpoint was also present. The custom-base response consequently reported five-minute cache creation.
- The current account-pool ordinary mimic also emits three system blocks, but its static prompt must be hash/length-compared before claiming exact equivalence. Real Claude Code passthrough keeps the client's captured blocks and Headers, subject to account metadata rewriting and cache policy normalization.
- The current live beta set is broader and body-dependent: the main request included Claude Code, interleaved thinking, thinking token count, context management, prompt-caching scope, mid-conversation system, advisor tool, effort, and structured outputs. Passthrough should preserve this real set; synthetic requests should include only betas supported by their actual body/capabilities.
- Repeating the print-mode request in a new process with the same explicit Session ID produced the same Session Header hash, nested metadata Session hash, device hash, main-system hashes, and per-source billing fingerprints. The nested `metadata.user_id` is a JSON string with `device_id`, `account_uuid`, and `session_id`; its Session value exactly matched the Header. `account_uuid` was empty in this custom API mode.
- The stable billing suffix differed by source: the main SDK request used `2.1.207.37a`, while session-title generation used `2.1.207.dc9`. Neither captured block contained a `cch` field. A single fixed billing suffix for all request categories would therefore be less accurate than source-aware generation, but an API gateway should not invent title requests solely to imitate this distribution.
- The repeated run completed both real upstream calls. The title request consumed its own usage (134 input, 20,220 cache-read, 16 output), while the main request consumed 25 input, 20,924 cache-read, and 4 output. The CLI's returned `iterations` array exposed only the main SDK message, so gateway accounting must continue counting actual upstream attempts rather than relying on a downstream client's summarized usage.
- The audit-baseline r2 ordinary-mimic static prompt was 5,128 characters (`fabae54e875c`), whereas this safe-mode, tool-disabled real CLI main block was 3,168 characters (`a3e33ecc098a`). The r3 implementation replaces the ordinary third block with a capability-neutral core instead of claiming exact prompt equivalence.
- Interactive `/usage` made no `/api/oauth/usage` request. The local CLI identified the configured session as `API Usage Billing` and displayed only local session totals, confirming that this machine cannot provide a live first-party subscription-usage baseline without a separate OAuth login.
- In safe-mode default-tool operation, the real main request grew from about 4.5 KB to 86.5 KB, declared 26 executable built-in tools, and added a 1,467-character runtime system message while preserving the same three-block stable prefix. It returned 498 uncached input, 20,742 cache-read, 189 five-minute cache-write, and 4 output tokens.
- The default-tool capture reinforces the functional boundary: passing through a genuine Claude Code tool registry is correct, but adding those tool schemas to an ordinary API caller that cannot execute them would be internally inconsistent and could break requests.
- Account-pool passthrough copies the inbound `Anthropic-Beta` after the generic Header path has added `oauth-2025-04-20`. The live custom-base client omitted that beta, and the current regression test explicitly requires passthrough not to add it. This creates a credential-layer ambiguity because the selected upstream account is OAuth even when the inbound Claude Code client is in custom API mode.
- A direct OAuth baseline is required before changing the beta rule. The likely coherent policy is to preserve real client capability betas and add only the selected credential's mandatory OAuth beta, rather than replacing the complete list or applying a synthetic global list.

## Claude Code Upstream Fidelity Audit (2026-07-13)

- Project documentation states that account-pool identity uses a stable device/account tuple, while ordinary API requests without an explicit session identifier receive a request-scoped random Session ID.
- A request-scoped random Session is materially different from Claude Code, which keeps one Session ID across a conversation/process. It also prevents normal session affinity unless downstream clients provide `X-Session-ID`, `Session-Id`, or `conversation_id`.
- Fingerprint similarity cannot make subscription OAuth account pooling policy-compliant; account sharing, third-party proxy traffic, abrupt IP/geo changes, and automation patterns remain independent enforcement signals.
- The main deployment questions are whether the same SQLite identity state moved to the server and whether inference, token refresh, OAuth usage polling, and login all use the same bound proxy.
- Anthropic's current legal/compliance page says subscription OAuth is for ordinary use of Claude Code/native Anthropic applications, while third-party products must use Console API keys or a supported cloud provider. It explicitly disallows routing Free/Pro/Max credentials on behalf of users and reserves enforcement without notice.
- The built-in account-pool profile matches the locally verified Claude Code 2.1.207 trace: UA `claude-cli/2.1.207 (external, sdk-cli)`, SDK 0.94.0, Node v26.3.0, fixed MacOS/arm64 Stainless values, ordered HTTP/1.1 headers, and a captured uTLS JA3/JA4 profile.
- Per-account `cloak_user_id` is stored in `claude_code_accounts` and projected into runtime auth. The real OAuth `account_uuid` supersedes its synthetic account component, so device/account identity remains stable only while the same SQLite row/database is retained.
- `X-Client-Request-Id` is currently accepted as an explicit Session source even though it is request-scoped by convention. That can split one conversation or incorrectly bind unrelated calls depending on the downstream client.
- The Linux/Docker host does not automatically leak Linux/amd64 through the pinned account-pool profile. The transport cache is keyed by account ID, proxy URL, and profile revision; recreating the SQLite database or rebinding an account to a new proxy is the meaningful identity change.
- Inference, quota, SessionKey, and normal OAuth refresh paths use the account proxy. One older OAuth uTLS constructor logs malformed proxy configuration and falls back to direct dialing, while inference's account-pool transport fails closed; this is a real consistency defect but only triggers for an invalid proxy configuration.
- Public sub2api and oh-my-pi discussions independently treat stable session metadata and matching OAuth `account_uuid` as correctness requirements. Their acceptance by Anthropic is not evidence that account pooling is permitted or resistant to enforcement.
- Downstream pure-mode token cleanup, count_tokens correction, cache preservation, and Anthropic error envelopes do not alter the request, account behavior, or telemetry visible to Anthropic upstream.
- Four locally captured real Claude Code 2.1.207 requests used one Session ID, and every request's `X-Claude-Code-Session-Id` matched `metadata.user_id.session_id`. The current unit test intentionally proves three ordinary no-session pool calls generate three different Session IDs.
- The current npm package and locally installed Claude Code are both 2.1.207, so a stale advertised version is not a leading explanation. Real traces also confirm `X-App: cli` and `Anthropic-Dangerous-Direct-Browser-Access: true`, matching the built-in mimic profile.
- The local ledger contains only 23 successful attempts across July 12-13, consistent with low-volume debugging. That does not reproduce the multi-user, sustained, or geographically shifted behavior of the server deployment.
- The background OAuth usage refresher is a concrete cross-request fingerprint mismatch: every due account calls `/api/oauth/usage` with `User-Agent: CLIProxyAPI Resource Pool` over the generic Go proxy transport. Inference for the same token uses the pinned Claude Code 2.1.207 Node HTTP/1.1 profile.
- Successful inference, passive quota headers, and successful active usage probes all schedule the next quota check with a hard-coded five-minute interval plus jitter. The configurable quota interval is normalized but not consumed by the scheduler, so an idle account continues producing these visibly non-Claude-Code usage calls roughly every 4-6 minutes.
- OAuth token refresh uses the same bound proxy, but a separate Chrome-uTLS HTTP/2 client and a sparse token request with no explicit User-Agent. This is less directly suspicious than the explicit quota-worker UA because OAuth token endpoints support multiple client shapes, but it is not the same transport profile as inference.
- Current sub2api passes the cached per-account User-Agent and optional TLS profile into its quota fetcher. This is useful comparative evidence that background account traffic should share account identity, but it does not establish provider permission or protection from restrictions.

## Prompt Cache And Error Envelope Follow-up (2026-07-13)

- The failed cache probe explicitly marks a large `system[0]` block with `cache_control`, but account-pool API mimic currently joins all client system text into one reminder and discards block metadata. The observed 571 cache-read tokens therefore belong to the injected profile, while the large client prompt remains uncached.
- The final outbound cache policy already defaults valid account-pool breakpoints to 1h and enforces the four-breakpoint limit. The missing behavior is preserving the client breakpoint when moving system text into the first user message.
- HTTP 400 `upstream_error` is already normalized locally to `invalid_request_error`; a server still returning `upstream_error` is running an older binary. The local envelope still lacks Anthropic's top-level `request_id`.
- Error response headers are populated before serialization and may already contain upstream `request-id` or `x-request-id`; the body should reuse that value instead of generating a second identifier.

## Pool Insights Implementation (2026-07-13)

- Pool config persistence and runtime projection compile cleanly across `internal/resourcepool`, `internal/claudeapipool`, management handlers, and `sdk/cliproxy`.
- `ListAccountsByPool` hydrates capacity, model status, 1h availability, and affinity per account; using it for pool summaries creates avoidable N+1 queries.
- The batch insights path can obtain identity, lifecycle, proxy, quota JSON, and persisted capacity in one joined account query, then read live route pressure from the already isolated in-memory pool router.
- `buildQuotaWindowStates` already provides the required exact/shared/observed/stale semantics for Sonnet, Opus, and Fable. Fresh observed-only windows are countable but must not contribute to average headroom or account equivalents.
- Health reliability needs a dedicated fixed-1h final-request rollup. Usage ledger rows represent upstream attempts, so reliability must group by request ID and combine local terminal routing rejects while excluding 400, 422, and 499.
- Existing `AccountPoolStats` and `AccountPoolSummary` contracts already contain health/model-capacity fields; only the shared computation and handler wiring are missing.

## Pool Config, Capacity, And Health Implementation

- `ClaudeCodeAccountPool` currently persists only identity/status metadata; global `ClaudeCodePoolConfig` owns pure mode and all routing fields, while `AccountCapacityConfig` provides the existing highest-precedence per-account RPM/concurrency/session overrides.
- Runtime routing already has one isolated `claude-acc-pool/<pool_id>` namespace per pool, but startup currently writes only the parent account-pool policy. Pool-effective policies can be projected without replacing the scheduler.
- Existing quota evaluation already computes fresh model-aware headroom from the tightest applicable shared/model window and treats stale or unknown utilization neutrally. Aggregation should reuse this evaluator rather than duplicate quota semantics.
- Pool summaries currently expose account counts, success, tokens, cost, and keys. Global/scoped stats expose in-flight and RPM pressure but no model capacity, health components, or distribution.
- The frontend has no chart dependency. A small accessible SVG gauge is sufficient and avoids adding a general charting package for one visualization.
- Health must use a fixed one-hour operational window independent of the selected reporting window; no-data components should reduce confidence rather than become false failures or false 100% values.

## Quota Confidence And Compact Card Implementation

- A quota window needs an explicit utilization-confidence flag because `used_percent=0` represents both a real 0% observation and the placeholder produced by status/reset-only Headers.
- Status/reset-only allowed windows remain useful operational observations but must stay neutral for routing and render as “已观察”. Rejected/exhausted or zero remaining remains a hard exhaustion signal without utilization.
- When a passive Header adds only allowed/reset metadata to an existing known OAuth window, merging must retain the OAuth percentage, source, and utilization timestamp so the percentage is not made falsely fresh or replaced with unknown.
- The compact card is most legible with 5h/7d meters side by side, one S/O/F row, a five-column load/usage grid, and a single proxy line. This reduced the tested card height to about 278px without clipping values.
- Responsive checks at 375, 768, 1024, and 1440 CSS pixels found no document, main-content, or card horizontal overflow.

## Claude Subscription Quota Accuracy Research

- Research started; authoritative, passive-header, and estimated quota sources will be tracked separately.
- The local sub2api implementation has two Claude quota sources: active `GET /api/oauth/usage` polling for `five_hour`, `seven_day`, and optional `seven_day_sonnet`, plus passive inference-response sampling of `anthropic-ratelimit-unified-5h-*`, `7d-*`, and `7d_oi-*` headers.
- sub2api treats `7d_oi` as a Fable-only model window and stores its utilization/reset separately; it does not derive this percentage from locally counted tokens.
- sub2api's UI renders only optional model windows that are present. Its source types expose Sonnet and Fable explicitly; the initial search found no corresponding independent Opus progress field in the main account usage response.
- sub2api calls the internal OAuth usage endpoint with Bearer OAuth, `anthropic-beta: oauth-2025-04-20`, a Claude Code user agent, the account's proxy, and optional TLS profile. It caches successful active responses for three minutes, errors for one minute, and uses singleflight plus jitter to avoid query storms.
- sub2api can actively query only OAuth accounts with the needed profile scope. Setup Token accounts cannot call the usage endpoint, so their 5h display prefers passively observed unified headers and otherwise falls back to coarse local status estimates (for example warning=80%, rejected=100%); 7d is unavailable unless observed passively.
- Active OAuth usage responses are authoritative only for fields actually returned. sub2api omits optional Sonnet/Fable bars when no reset/window exists and fills missing Fable from passive `7d_oi` response headers.
- Anthropic's public API rate-limit documentation covers API organization/workspace headers such as request/token limit, remaining, and reset. It says aggregate token headers report the most restrictive currently active limit. It does not document the subscription-specific `/api/oauth/usage` endpoint or the `anthropic-ratelimit-unified-5h/7d/7d_oi-*` header family used by Claude subscription clients.
- Therefore subscription percentages obtained from `/api/oauth/usage` or unified headers can be authoritative observations of the current upstream response, but their schema and model claim names are not a stable public API contract.
- Official Claude Code cost documentation says Pro/Max/Team/Enterprise subscribers see plan usage bars in the `/usage` command. It separately labels the session dollar figure as a local token-based estimate; the plan bars are not described as estimates.
- The same public Claude Code page does not document the underlying subscription JSON schema, internal endpoint, or a guaranteed Sonnet/Opus/Fable field set. It confirms the product exposes plan usage, not that every account has an independent quota for every model.
- Anthropic's official Pro/Max support article says Claude and Claude Code share the same plan limits and tells users to monitor remaining allocation via Claude Code `/status` (newer docs also expose `/usage`). It does not promise separate Opus, Sonnet, or Fable percentages.
- Official support wording is plan-level and intentionally variable; it describes warning messages and reset behavior rather than fixed token/message limits that a proxy could reconstruct locally.
- Claude Code `2.1.207` itself fetches `/api/oauth/usage`, recognizes `weekly_scoped`, and labels optional claims `seven_day_opus` (Opus limit), `seven_day_sonnet` (Sonnet limit), and `seven_day_overage_included` / `7d_oi` (Fable 5 limit), alongside shared `five_hour` and `seven_day`.
- The official client also parses `anthropic-ratelimit-unified-<claim>-utilization` and `-reset`, so its displayed model limits use the same active-response/passive-header combination seen in sub2api. Missing claims are not evidence of 100% remaining; they mean no independent value was supplied for that account/request.
- Claude Code messages explicitly suggest switching models when an Opus/Sonnet/Fable claim is exhausted, confirming model-scoped limits are conditional claims and can coexist with shared session/weekly limits.
- GitHub code search finds the same claim family in quota/status tools and brokers such as switchroom, claude-statusline, token-watch, howmuchleft, and Claude Agent SDK bindings. This supports the field interpretation but does not turn the internal endpoint into a documented public API.
- switchroom takes a different active-probe approach: it sends a one-token `/v1/messages` request with subscription OAuth/Claude CLI headers and reads unified response headers. A cheap Haiku probe observes shared 5h/7d, while a separate Fable canary is required because model-tier `7d_oi` can be rejected for Fable while Haiku/Opus remain allowed.
- switchroom explicitly tracks header-presence flags so a missing utilization header is rendered as unknown instead of 0%. It also expires cached utilization at the upstream reset time. This is a useful accuracy pattern, but the active inference canary consumes a real request and is not a free quota query.
- claude-statusline uses a detached OAuth usage worker and explicitly calls the endpoint undocumented/fail-soft. It parses every `limits[].kind=weekly_scoped` entry using the upstream `percent` and model display name, then falls back to top-level `seven_day_sonnet` and `seven_day_opus` only when a scoped entry does not already cover that class.
- The current local account's Fable `weekly_scoped` entry actually includes `percent: 0` and a reset timestamp even though `is_active` is false. Thus its current 0%-used / 100%-remaining display is sourced from an upstream percentage, not fabricated from a missing utilization field. Sonnet and Opus remain null for this account.
- This project's `ParseClaudeOAuthUsage` already matches the stronger claude-statusline pattern: it reads top-level Sonnet/Opus/Fable windows, then maps dynamic `weekly_scoped` entries by model display name and uses the upstream `percent`. It does not synthesize an Opus/Sonnet percentage when those fields are null.
- token-watch also calls `/api/oauth/usage` directly, caches the entire upstream JSON, uses singleflight, obeys 429 `Retry-After`, and refreshes OAuth once on 401/403. It adds no local model-percentage formula; its accuracy is exactly the accuracy/availability of the upstream response.
- howmuchleft likewise reads the OAuth usage endpoint and caches last-known-good data with reset-aware invalidation. Its schema currently exposes shared 5h/7d and Fable only, demonstrating that many tools show fewer bars because their parser is incomplete or intentionally narrow, not because the upstream always lacks other claims.
- Mature tools mark stale/error state and preserve the last successful snapshot; they do not continuously recalculate a subscription percentage from local tokens because Anthropic plan limits are dynamic and shared across products.
- This project already actively refreshes due OAuth accounts, skips manual-recovery accounts, limits concurrency, and merges passive unified response headers. Fresh inference headers postpone the next active usage probe, which is more efficient than fixed global polling.
- One concrete accuracy gap remains in the current response-header parser: `QuotaWindow` has no utilization-presence flag, so a header set containing only status/reset/remaining can currently become `used=0 / remaining=100`. switchroom correctly distinguishes this case as unknown. OAuth usage fields with an explicit `utilization` or `limits[].percent` are not affected.
- Accurate means “fresh and explicitly supplied by Anthropic,” not “available for every model.” Model windows vary by account, plan, active experiments, model tier, and overage state; missing Opus/Sonnet claims should fall back to the shared 7d window for routing/display semantics without inventing an independent percentage.
- Recommended display states per model are: exact percentage (fresh explicit value), shared 7d (no independent claim), observed status/reset only (percentage unknown), stale last-known value, or exhausted. This preserves more information than forcing every model into one numeric progress bar.
- Recommended collection policy is the existing OAuth poll plus passive response-header merge. Optional manual model canaries can diagnose a suspected Fable/model-tier wall, but scheduled inference heartbeats should remain disabled because they consume real quota and can add avoidable load.

## Quota Confidence And Compact Card Implementation

- The current card repeats Auth ID and test success in the header, then renders load and 30-day usage as prose. This increases height and weakens scan order.
- `CompactModelQuotaSummary` already has shared fallbacks but is not rendered by the account card; it can be adapted to exact/shared/observed/unknown quota semantics.
- `QuotaWindow` currently stores numeric percentages without a presence bit, so confidence must be added without replacing the existing numeric fields or breaking persisted JSON.

- The current executor treats `cache_control: {"type":"ephemeral"}` without `ttl` as a 5-minute block; ordering normalization removes a later `ttl:"1h"` when a default-5m block precedes it.
- The current account-pool beta builder only includes `extended-cache-ttl-2025-04-11` when the body explicitly contains `cache_control.ttl="1h"`; ordinary mimic profile defaults deliberately omit that beta.
- Current Anthropic Prompt Caching and Messages API documentation explicitly says an omitted cache TTL defaults to `5m`; `1h` must be requested and its write price is 2x base input versus 1.25x for 5m.
- Current Claude Code documentation distinguishes authentication modes: Claude subscription sessions request 1-hour TTL automatically; overage usage credits drop back to 5m; API key, Bedrock, Google Cloud, Microsoft Foundry, and Claude Platform on AWS stay at 5m unless `ENABLE_PROMPT_CACHING_1H=1` is set. `FORCE_PROMPT_CACHING_5M=1` overrides all modes.
- Anthropic release notes state 1-hour prompt caching is now generally available without a beta header, so the project's conditional legacy `extended-cache-ttl-2025-04-11` merge should be treated as compatibility debt rather than evidence that 1h is the default.
- Official sources: https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching, https://docs.anthropic.com/en/api/messages, https://code.claude.com/docs/en/prompt-caching, https://code.claude.com/docs/en/env-vars, https://docs.anthropic.com/en/release-notes/api.
- The locally installed Claude Code is `2.1.207`. Captured interactive requests with 25 built-in tools contain three `cache_control:{"type":"ephemeral"}` blocks, none with `ttl`; under the official schema these are 5-minute caches.
- The same real traces omit `extended-cache-ttl-2025-04-11`. Structured helper requests with no tools contain no cache breakpoints, while interactive Sonnet, Opus, and Haiku requests consistently use omitted-TTL ephemeral breakpoints.
- sub2api defines its gateway-generated default as explicit `5m` and exposes `enable_anthropic_cache_ttl_1h_injection`, default `false`. When enabled, it rewrites every existing ephemeral breakpoint to `ttl:"1h"` for Anthropic OAuth/SetupToken accounts, including real Claude Code requests, but does not create new breakpoints.
- sub2api's source comment that real Claude Code can use 1h is consistent with the current official subscription policy, but its global rewrite switch is still an operator override: it also changes requests whose original Claude Code policy selected 5m.
- sub2api separately offers message-breakpoint rewriting and account-level usage TTL classification. Those are billing/cache-layout controls and should not be conflated with request-shape fidelity.
- Claude Code 2.1.207 contains a client-side `MOe(querySource)` TTL policy. `FORCE_PROMPT_CACHING_5M` disables 1h; `ENABLE_PROMPT_CACHING_1H` (and the Bedrock-specific variant) forces it; otherwise standard first-party OAuth subscriptions use a GrowthBook/default allowlist that includes `repl_main_thread*`, `sdk`, `auto_mode`, and `memdir_relevance`, unless overage mode is active.
- On main requests, Claude Code computes `cacheTtl = MOe(querySource) ? "1h" : undefined` and creates system/message breakpoints with that TTL. A helper also adds the selected TTL only to existing cache controls that do not already have one, preserving explicit client TTL values.
- Therefore the protocol default and the Claude Code product policy differ: omitted `ttl` is still 5m at the API, while a current first-party OAuth Claude Code main session can explicitly send `ttl:"1h"` by default through feature policy. API-key/custom-base traces may remain 5m and are not sufficient to characterize OAuth subscription traffic.
- Phistory 2.1.207 interactive traces also contain three omitted-TTL ephemeral breakpoints and no extended-cache beta. Like local record-only traces, this public capture does not establish first-party OAuth policy and is consistent with API-key/custom-base 5m behavior.
- Before implementation, strict Claude Code passthrough preserved body cache controls, API mimic injected two omitted-TTL 5m system breakpoints, and management tests/calibration also used 5m.
- Before implementation, TTL normalization and the four-breakpoint limit ran before mimic profile injection, allowing the final body to exceed the limit or place a 1h client block after an injected 5m block.
- No official Anthropic source links cache TTL to account suspension risk. Official documentation presents TTL as cache lifetime, latency, quota, and billing behavior. The official compliance documentation instead says subscription OAuth is for ordinary use of native Anthropic applications and is not permitted for third-party routing on behalf of users; a 1h rewrite does not change that policy risk.
- The user selected an account-pool-wide operator policy: every existing ephemeral breakpoint defaults to 1h, including passthrough, mimic, streaming, count_tokens, and management tests. A default-off `allow_client_cache_ttl` switch preserves explicit valid client 5m/1h values when enabled; omitted TTL still becomes 1h.
- The implementation does not create tool/message breakpoints where none exist and runs breakpoint limiting plus TTL-order normalization after final account-pool body construction. Ordinary `/v1/*`, Claude API Pool, and other providers remain unchanged.
- Before this follow-up, generated pool API keys persisted only `key_hash` and `key_prefix`; those historical full values cannot be recovered later.
- The repository has no reusable at-rest encryption or master-key facility. Account OAuth/SessionKey material is already persisted in the same local SQLite store, so persistent management retrieval can follow that trust boundary without inventing an unrecoverable key-management dependency.
- Returning every full secret from the list API would expose all credentials during normal React Query refreshes and SSE invalidation. A dedicated management-authenticated reveal endpoint limits exposure to an explicit administrator action.
- Existing rows cannot be backfilled from SHA-256 hashes. They must keep authenticating normally and report `secret_available=false` until rotated.
- The current SQLite schema has one global account set; accounts, routing events, and usage rows have no `pool_id`.
- The usage ledger already stores account/model/raw token dimensions and an `estimated_cost` column, but the runtime plugin never populates cost.
- Incoming account-pool routes currently use the global config API-key middleware and a single literal scheduler scope `claude-acc-pool`.
- The access result already supports metadata, and Gin context already carries `userApiKey` and `accessMetadata`; a dedicated account-pool middleware can add pool/key IDs without exposing them to callers.
- Runtime auths already carry account-pool attributes, so adding `pool_id` there is the narrowest candidate-filter boundary.
- The scheduler stores session bindings inside a router keyed by scope; strict isolation requires a per-pool routing namespace, not candidate filtering alone.
- OAuth and SessionKey registration currently update an existing Auth ID in place; cross-pool duplicate registration must become an explicit conflict rather than an implicit move.
- The current frontend is a 5,851-line `App.tsx` with only home/accounts/proxies routes. The new navigation warrants page/feature extraction while retaining React Query, TanStack Table, Radix, Lucide, and the embedded single-file build.
- Existing usage parsing records aggregate cache creation only. Standard 5m/1h costing requires extending the Claude usage detail while preserving aggregate compatibility.
- The usage plugin emits one ledger row per real executor attempt, so rows already represent upstream attempts; downstream request count must be derived from distinct request IDs instead of renaming row count.
- The current ledger does not persist cache 5m/1h detail, a price revision, or pricing coverage state, and its dormant `estimated_cost` is never populated.
- Pure-mode rewriting happens after the executor usage reporter captures Anthropic usage, so cost can use raw reporter detail without reading the cleaned downstream response.
- sub2api's bundled current data confirms per-million baselines of Sonnet 4.6 `3/15/3.75/6/0.30`, Opus 4.8 `5/25/6.25/10/0.50`, and Haiku 4.5 `1/5/1.25/2/0.10` for input/output/cache-write-5m/cache-write-1h/cache-read. Fable 5 is absent and must remain unpriced until configured.
- Account-pool identity survives detached execution through handler metadata and conductor context, so that path can also pin the request-start price revision.
- The first frontend integration type-check passed. Remaining console gaps were explicit account movement and account-level token/pricing detail; both can reuse existing management responses without adding another API.
- Pool membership is enforced in the Auth Manager candidate filter and the scheduler uses `claude-acc-pool/<pool_id>`, so an empty pool cannot select or promote an account from another pool.
- Generated pool-key extraction accepted a raw `Authorization: sk-cap-...` value even though the public contract only permits Bearer and `x-api-key`; the parser must reject raw and Basic authorization forms.
- Browser testing at 768px found a hidden-clipping failure: the new page grid inherited a wide table's min-content width, placing toolbar actions beyond the viewport while `overflow-x-hidden` kept document width deceptively equal to the viewport. New page roots/sections need `min-w-0`, and operational tables should switch to complete mobile rows below 1024px.

---

# Previous Findings: Account Pool Lifecycle, Session Capacity, and Headroom

## Claude Code Observable Fingerprint Research (2026-07-12)

- Research in progress. Comparison dimensions are body shape, HTTP headers/order, TLS/connection behavior, and session/retry behavior.
- Current verified baseline is local Claude Code 2.1.207; public implementations are references only and do not override captured evidence.
- The account-pool path already uses a dedicated custom uTLS ClientHello with HTTP/1.1-only ALPN and a bounded transport cache keyed by account, proxy, and profile revision.
- Real Claude Code traffic has a separate strict passthrough mode; ordinary API traffic uses a conservative mimic path rather than injecting the CLI tool set.
- The first likely remaining gaps are exact HTTP/1.1 header order/casing, connection reuse behavior, and whether the static Node ClientHello fixture remains byte-level aligned with the currently captured CLI runtime.
- The account-pool request still passes through Go `net/http.Transport`; its HTTP/1.1 writer canonicalizes/sorts headers, while Node/Undici has a distinct lowercase insertion order. Existing trace capture stores values but not raw on-wire order/casing, so this layer is not yet verified.
- Account-pool streaming correctly overrides the generic executor's `text/event-stream + identity` behavior back to the captured Claude Code `Accept: application/json` and compressed encodings.
- Passthrough preserves the inbound Claude Code software/header tuple but still traverses the server's own outbound transport; it is semantic passthrough, not byte-for-byte network passthrough.
- sub2api models TLS fingerprints as profiles and has capture/integration tests asserting a Node 24.x JA3 hash and JA4 components. Its reusable testing approach is valuable; random per-account profile selection is less appropriate than pinning one profile to the verified Claude Code release.
- This project currently verifies ALPN and transport settings structurally, but does not assert a captured JA3/JA4 baseline for the custom 2.1.207 ClientHello.
- The locally installed Claude Code remains 2.1.207. The account-pool software tuple is intentionally the captured SDK/embedded runtime tuple, not the shell's separately installed `node` binary version.
- The custom ClientHello is effectively the same cipher/curve/signature/extension sequence as sub2api's `macos_arm64_node_v2430` profile. Our headers identify the captured Claude Code runtime as v26.3.0; this may still be valid if the OpenSSL defaults are unchanged, but it should be proven with a repeatable real-CLI TLS capture rather than inferred from version labels.
- sub2api also uses Go `http.Transport` after its uTLS dialer, so it does not solve HTTP/1.1 header order/casing either. It is a useful TLS baseline, not evidence of complete Node wire equivalence.
- Public implementations fall into three groups: minimal OAuth validator compatibility, aggressive body/header rewrite, and wrapping the official CLI as a subprocess. Most prove API acceptance, not full wire equivalence.
- `ansg191/claude-auth-proxy` and related OpenCode auth projects are useful references for idempotent billing/identity insertion, tool-name mapping, and keeping CLI version, entrypoint, User-Agent, and Stainless headers internally consistent.
- Many public snippets are stale: they force CCH, old entrypoint `cli`, old SDK/runtime tuples, or fixed beta lists. The API-key trace baseline does not prove OAuth requests can omit `oauth-2025-04-20`; live OAuth validation and sub2api both support adding it only at the credential layer.
- Projects that run the official Claude CLI provide the highest fidelity but sacrifice transparent API semantics, throughput, observability, and account-pool scheduling; they are not a good core architecture for this service.
- A concrete mimic inconsistency exists in the current pipeline: body metadata gets a request/explicit-session-derived Session ID, while the generic header path sets `X-Claude-Code-Session-Id` from the OAuth-token session cache. Passthrough keeps them aligned, but mimic can send two different IDs.
- Existing tests assert passthrough Session preservation and mimic metadata randomness separately, but do not cross-check the mimic header against metadata. The trace diff marks Session ID values as dynamic, so it also misses this internal invariant.
- The current recorder is built on `net/http.Server`; by capture time original HTTP/1.1 header order and casing are already lost. A raw-header capture mode or packet-level fixture is needed before changing the outbound writer.
- Current trace comparison already covers request kind, model, stream, selected header values, Stainless tuple, billing form, system/tool hashes, cache-control placement, thinking, context management, and top-level keys. Its highest-value extensions are cross-field invariants and raw transport evidence, not more independent field snapshots.
- A fresh local record-only trace from Claude Code 2.1.207 confirmed HTTP/1.1, `Accept: application/json`, compressed encodings, SDK 0.94.0, runtime v26.3.0, `x-app: cli`, no CCH, and no `X-Client-Request-Id`.
- The fresh real trace had identical `X-Claude-Code-Session-Id` and `metadata.user_id.session_id`, confirming this is a real invariant rather than an assumption.
- The same minimal CLI run exposed 24 tools, not a universal fixed count. Tool availability varies with runtime capabilities/configuration, reinforcing the existing decision not to inject a fake fixed tool set into ordinary API mimic requests.
- The real interactive request used adaptive thinking with `display=omitted`, effort high, context management, and three system blocks. These are appropriate only when the caller actually has Claude Code's execution environment; semantic mimic requests should remain conservative.
- A one-shot raw local capture established the current 2.1.207 HTTP/1.1 header order: Accept, Content-Type, User-Agent, Session ID, Stainless tuple, anthropic headers, auth, x-app, Connection, Host, Accept-Encoding, Content-Length. Casing is mixed and stable rather than uniformly lowercase.
- A matching Go `net/http` probe serialized Host/User-Agent/Content-Length first and then sorted the remaining headers, confirming a real on-wire difference. Header insertion order cannot fix this; an account-pool-only ordered HTTP/1.1 writer would be required.
- A local 529-then-success probe showed Claude Code 2.1.207 keeps `X-Stainless-Retry-Count: 0` on both attempts and preserves the same Session ID. The current header value is therefore correct; do not copy public projects that increment or synthesize a different retry tuple without trace evidence.
- The fresh real trace's third system block is 27,234 bytes, while the builtin mimic stable prompt is 5,124 bytes. This is an intentional semantic compromise, not a transport bug: copying the full prompt into ordinary API requests adds hidden cost, references unavailable tools/environment, changes answers, and can trigger Fable refusals.
- Exact full-prompt fidelity belongs to real Claude Code passthrough. API mimic should target internally consistent protocol identity and preserve caller semantics rather than become a fake full CLI runtime.
- Trace `top_level_keys` is currently sorted, so it cannot report raw JSON key order. Executor tests preserve key order in selected transforms, but end-to-end real-vs-mimic raw body order is not part of the trace diff.
- The locked account-pool profile pins UA/SDK/runtime but omits OS/Arch, so generic header code derives them from the server runtime. The TLS spec is macOS ARM64-like; a Linux deployment would therefore emit a contradictory Linux/x64 Stainless tuple with a macOS-style TLS profile.
- A fingerprint profile should atomically own CLI version, SDK/runtime, OS/Arch, TLS spec, header order, and body rules. Either pin the verified macOS ARM64 profile everywhere or add separately captured platform variants; do not mix fields dynamically.
- The current custom ClientHello fields compute to JA3 `44f88fca027f27bab4bb08d4af15f23e`, matching sub2api's captured macOS ARM64 Node 24.x fixture. This is a good baseline, but JA4 and a direct 2.1.207 TLS capture should still be stored as regression evidence.
- Existing `traces/ours` samples directly confirm the mimic Session mismatch: the outbound `X-Claude-Code-Session-Id` UUID differs from the UUID inside `metadata.user_id` for the same request.
- Both OpenCode auth and the sampled Rust proxy use a stable per-process Session header, so public popularity does not make that behavior equivalent to the freshly captured CLI invariant.
- The existing direct dependency `github.com/imroc/req/v3` exposes a standard-library-compatible `http.RoundTripper` with ordered HTTP/1.1 serialization. Its order includes normally special-cased Host, User-Agent, transfer headers, and Content-Length, so it can replace only the account-pool serializer while retaining the existing uTLS dialer.
- `req/v3` also retains mature connection pooling, request cancellation, HTTP/SOCKS tunnel support, streaming response bodies, and explicit compression behavior; using it avoids a second hand-written HTTP/1.1 implementation.
- The public implementations remain useful for robust tool-name forward/reverse mapping and malformed tool-pair repair, but their current default software tuples and beta/CCH behavior lag the local 2.1.207 baseline.
- The account-pool middleware correctly tagged `c.Request.Context()`, but `GetContextWithCancel(..., context.Background())` discarded that scope before building execution metadata. This made account-pool routes select ordinary Claude auths and explained the missing routing events/traces and misleading 401 during early smoke tests.
- Preserving only known routing values when creating the detached handler context fixes scope propagation without inheriting request deadlines as the execution parent.
- The quota worker previously persisted refreshed OAuth tokens only to SQLite. It now starts after runtime auth initialization and synchronizes token rotations into the Auth Manager, preventing a future DB/runtime credential split.
- Real passthrough correctly preserves Claude Code tool names. For API mimic, the current `>5 tools` path replaces custom names with deterministic-but-artificial semantic prefixes (`analyze_`, `fetch_`, etc.). This is reversible but less Claude-Code-like than a stable `mcp_` + PascalCase normalization and can reduce cache stability when the tool set changes.
- Any tool-name change must remain per-request reversible across normal and SSE responses and must never map custom schemas onto built-in names such as `Bash` or `Read`.

## Current Findings

- Account registration persists and reloads the new account but does not trigger an immediate quota probe; the current background sweep can leave a new account without quota state for up to five minutes.
- The quota worker already calls `/api/oauth/usage` every five minutes with concurrency two and retries a 401 after refreshing the access token, but probe errors currently update only quota display state.
- The current account `Enabled` flag both removes the auth from routing and causes the quota worker to skip it, so a manually paused account is not maintained.
- Account-pool runtime errors already distinguish 401, 402, Cloudflare/unknown 403, 429, 529, and repeated transport failures; the missing piece is a persistent account-pool lifecycle projection shared by probes and request results.
- Session affinity and active-session counting currently share the same sliding one-hour binding map. This retains cache affinity but makes inactive sessions consume `MaxSessions` too long.
- Sticky concurrency reserve is already concurrency-only, and the current defaults are 6 RPM, 1 base concurrency, 1 sticky reserve, 30 sessions, 2s sticky wait, and 500ms fallback wait.
- sub2api separates a one-hour sticky binding from a default five-minute active-session idle window. Its RPM yellow zone is intentionally not part of this implementation.

---

# Previous Findings: Account Pool Pure Usage Semantics

## Current Findings

- The current response rewriter only changes `input_tokens`; `cache_creation_input_tokens`, `cache_read_input_tokens`, nested `cache_creation`, and matching `iterations[]` cache values remain raw.
- Usage reporting occurs before downstream response rewriting, so raw Anthropic usage can remain untouched while the client receives a cleaned envelope.
- `pure_mode` defaults to true, while `usage.clean_input_tokens` defaults to false and currently controls the rewriter. This contradicts the intended single-switch behavior.
- The request pipeline already knows passthrough versus api-mimic and retains the original translated request, so it can avoid cleaning passthrough and detect client-owned cache controls.
- The visible-input estimator provides a lower bound close to the original client request token count and can drive cleaned `input_tokens`.

- Account-pool `virtual_cache` is persisted in `resource-pools.db`, but `ApplyClaudeCodePoolRuntimeConfig` only applies scoped routing. The executor virtual ledger also checks `AttrPool`, while account-pool OAuth auths use `AttrOAuthPool`; therefore the account-pool virtual-ledger panel is ineffective.
- The separate Claude API Pool still has a functional virtual ledger backed by `claude-api-pool.db` / `claude-api-pool.yaml`; it must remain unchanged.
- `resourcepool.AuthStore` already persists marked account-pool auths to SQLite and delegates ordinary auths to the file store.
- The two local Claude files were created before SQLite account storage, contain no account-pool marker, and the current `claude_code_accounts` table has no matching rows.
- `ListAuthFiles` renders every Auth Manager entry with a path and does not filter account-pool auths.
- Ordinary Claude routing allows account-pool auths whenever the separate Claude API Pool is disabled; explicit account-pool scope already correctly requires account-pool auths.
- Safe migration must write and verify SQLite before deleting from the delegate file store. Candidate detection must require provider Claude plus OAuth access and refresh tokens and exclude `AttrPool` API-pool entries.
- SQLite-backed account-pool auths intentionally have no auth-file path. Account runtime summaries must therefore not depend on `buildAuthFileEntry`, whose path requirement is correct for the management auth-file page.
- The startup `auth files` count excludes SQLite-synthesized credentials; direct account API smoke confirmed the migrated credentials are still active in the runtime manager.

## Prior 2.1.207 Findings

## Verified 2.1.207 Baseline

- Local Claude Code version is `2.1.207`.
- Real traces use `claude-cli/2.1.207 (external, sdk-cli)`, SDK `0.94.0`, and Node `v26.3.0`.
- Billing is `cc_version=2.1.207.<fingerprint>; cc_entrypoint=sdk-cli;` and has no CCH.
- The identity block is `You are a Claude agent, built on Anthropic's Claude Agent SDK.`.
- Real account-pool target traffic negotiates HTTP/1.1 only and advertises `gzip, deflate, br, zstd`.
- Main request tools do not receive an automatically added cache-control marker.
- Phistory 2.1.207 exposes a homepage manifest and separate `static-prompts.md/json` files; full prompt content remains reference-only.
- The homepage manifest identifies Claude Code `2.1.207` as latest and includes raw artifact paths.
- Phistory 2.1.207 traces contain both structured title helpers and main interactive requests, confirming request-kind pairing is required.

## Existing Work To Preserve

- The worktree contains uncommitted SessionKey login, proxy reservation, scheduling refactor, observability, and resource-console redesign changes.
- The previous account-pool scheduling refactor is complete and documented below; this task must not revert it.

## Previous Scheduling Baseline

- Existing worktree contains uncommitted SessionKey login and resource-console UI changes; these are user-owned and must remain intact.
- SQLite runtime configuration is authoritative after first initialization.
- Existing positive behavior to preserve includes account-wide concurrency/RPM accounting, model cooldowns, separate 429/529 backoff, refresh locking, stream lease release, and cooldown persistence.

## Known Problems From Approved Analysis

- Account-pool session affinity is stored but not wired to runtime selection.
- Prefix affinity reads an unscoped Claude API Pool routing config.
- Capacity is checked after selection, allowing full accounts to consume switch budget.
- Sticky buffer currently expands both concurrency and RPM.
- MaxSessions is stored but unused.
- Retries do not consistently consume RPM per upstream attempt.
- 401/403 failures can be applied only at model scope instead of account scope.
- Retry/reset headers are not propagated into routing decisions.
- Runtime cooldown, persisted model status, and aggregate account availability can diverge.

## Discoveries During Implementation

- Account-pool requests are marked through `Options.Metadata[pool_scope] = claude-acc-pool` before auth selection.
- The three relevant execution loops are normal, count-tokens, and stream; each currently selects auth first and acquires route capacity afterward.
- `ExtractVirtualCacheSessionKey` avoids message-hash fallback but includes `X-Client-Request-Id`; the new account-pool extractor must use only the approved explicit identifiers.
- Existing `statusErr` supports status and retry duration but Claude executor does not populate retry/reset headers. It can be extended with optional response headers without changing other providers.
- Account-pool runtime policy is applied with `SetScopedRoutingConfig`, but existing affinity helpers call unscoped `CurrentRoutingConfig`, `SelectAffinityAuth`, and `NoteAffinityResult`.
- Existing `availableAuthsForRouteModel` discards lower-priority accounts before capacity evaluation. Account-pool selection needs all eligible priorities so a full high-priority account does not cause a false local rejection.
- Stream requests retain their route lease until the downstream stream closes, which should remain unchanged.
# Findings: Proactive Model Quota Scheduling

## 2026-07-13: Pool Config, Capacity, And Health

- Sparse pool overrides can be projected without changing the global config schema: routing scopes consume the merged policy, while stored account-capacity rows remain the highest-precedence capacity values.
- The pool-config management contract returns `overrides`, `effective`, `global`, and per-field `sources`; JSON `null` safely removes either one override or the complete routing override object.
- Model capacity can be derived in one joined account/quota/capacity/proxy query plus fixed aggregate queries. Fresh exact/shared percentages and explicit exhaustion participate in averages; observed-only, stale, and unknown evidence remain visible but unmeasured.
- Pool health must reweight known components instead of treating missing traffic or quota as zero. A healthy idle single-account pool therefore scores from readiness/load with reduced confidence and a separate single-point warning.
- Global health is recomputed from all accounts in enabled pools. A test with one ready and three unavailable accounts produces 25% readiness and a 40-point global score, proving it is not a simple average of pool scores.
- The console can expose all pool-overridable settings without duplicating global-only Profile, TLS, pricing, quota worker, proxy health, Trace, logging, or client-cache-TTL controls.
- Browser QA confirmed every one of the 29 Strategy controls has an accessible name and the page has no document-level horizontal overflow at 375/768/1024/1440.
- Mobile pool-detail tabs need explicit selected-item alignment after asynchronous pool loading; a static horizontally scrollable strip otherwise hides a deep-linked Strategy tab even though the page itself does not overflow.
- A real Strategy edit on the isolated copied database changed default-pool RPM from inherited 15 to pool override 16, then removed that override and restored the effective global value 15.
- The global overview gauge renders as a stable 299px SVG with four paths at 1440px, while the complete page remains exactly viewport-width at both 1440px and 375px.
- Overview mobile controls stay inside the viewport and the isolated console produced no browser console errors during health, pool, and Strategy navigation.

## 2026-07-12

- Follow-up comparison confirmed sub2api does not hard-block Anthropic OAuth accounts at 95% official utilization; it primarily uses true 429/rejected windows for exclusion. Its configurable sticky-only zones apply to local window-cost/RPM limits, not the official quota percentage.
- The account-pool policy is therefore simpler when utilization below exhaustion remains eligible: normalized request pressure selects the least-loaded tier, model-specific headroom breaks ties, and real exhaustion drives failover/cooldown.
- Active-session occupancy is a useful third pressure signal but remains an advanced diagnostic; concurrency and RPM are the primary card/table indicators.

- Anthropic OAuth usage exposes shared `five_hour` and `seven_day` windows plus optional `seven_day_sonnet`, `seven_day_opus`, and Fable `seven_day_overage_included` claims.
- Anthropic response headers expose utilization/status/reset data under `anthropic-ratelimit-unified-*`; Fable uses the `7d_oi` wire window and `seven_day_overage_included` representative claim.
- sub2api actively parses Fable usage, passively samples `7d_oi`, falls back to passive data when active usage omits it, and treats `7d_oi` exhaustion as a Fable-family model limit rather than an account limit.
- Official CLIProxyAPI latest `upstream/main` supports the `claude-fable-5` model registry but has no OAuth usage or Fable quota scheduling implementation; account-pool quota behavior remains local secondary development.
- This project already parses Sonnet and Opus active windows and dynamically stores `7d_oi`, but currently creates `model_7d_oi`, does not parse active Fable usage, and does not include Fable in model-aware headroom.
- Existing account-pool routing already has sticky-first selection, model cooldown separation, active-session capacity, headroom ordering, and response-header persistence, so the feature should extend those boundaries rather than add another scheduler.
- Account-pool 429 handling already cools only the exact request model in the Auth Manager; persisted quota routing can add shared-window and Fable-family scope without changing ordinary `/v1/*` behavior.
- Active usage currently replaces the complete snapshot, so Fable passive data would disappear whenever `/api/oauth/usage` omits it; active refresh must preserve a fresh passive Fable fallback.
- Legacy passive Fable rows use `model_7d_oi`; normalization must accept and rewrite this key as canonical `seven_day_fable`.
- The control plane previously became too configuration-heavy, so proactive thresholds will be conservative built-in policy rather than new editable routing fields in this iteration.
- A clean database exposed a same-process initialization race between health, quota, and auth startup paths; normal SQLite busy timeout was insufficient because schema/migration setup ran concurrently. Serializing only `Open` initialization removes the race without affecting runtime reads or writes.
- Isolated UI smoke confirms a 96% Fable window produces `drain_only` while shared 5h/7d remain normal, and the card/detail surfaces expose all five windows without conflating model and account scope.

---
# SessionKey Cookie OAuth investigation (2026-07-12)

- The same credential succeeds in local sub2api, so the current failure is likely request-shape or transport related rather than an invalid cookie.
- Our implementation uses the regular `NewClaudeAuthWithProxyURL` Go HTTP client for `claude.ai` web endpoints.
- sub2api constructs a dedicated req/v3 client with `ImpersonateChrome()`, disables its CookieJar, and applies the selected proxy for every Cookie OAuth step.
- Our current error mapper converts both HTTP 401 and HTTP 403 from web endpoints to `invalid_session`, hiding Cloudflare/browser-fingerprint/proxy rejection behind a misleading credential error.
- The exposed credential will not be submitted during debugging; verification must use synthetic handlers and the user's own later UI retry.
- The user supplied a local `info/sk.txt` containing one record per line and explicitly authorized a smoke test. A single record may be read in-process after the implementation is corrected, but its account, password, and SessionKey must never be printed, passed in argv, or persisted.
- Exact OAuth constants and JSON bodies match sub2api. Our auth transport does use Chrome uTLS and the assigned proxy, but it sends sparse Go HTTP headers; sub2api's req/v3 `ImpersonateChrome()` also supplies browser-style HTTP headers. This TLS/header consistency is now the leading cause.
- The current auth uTLS transport uses Chrome TLS over default x/net HTTP/2 behavior; sub2api additionally fixes Chrome 120 HTTP/2 settings, pseudo/header order, common browser headers, and Client Hints through req/v3.
- A live non-persisting smoke with the first authorized local record and the exact SOCKS5 proxy from the failed job completed the full organization/authorize/token flow in 5.52 seconds after alignment. The root cause is confirmed as request fingerprint mismatch, not an invalid SessionKey.
# Account test and scoped quota investigation (2026-07-12)

- The account test endpoint received HTTP 200 for Fable and multiple Opus models, but `extractClaudeMessageText` only accepts a non-stream top-level `content[].text` JSON shape and silently returns an empty string for all other successful shapes.
- The account's OAuth usage response contains shared `five_hour` and `seven_day` windows. Legacy `seven_day_sonnet`, `seven_day_opus`, and `seven_day_overage_included` fields are null.
- The same response contains the new `limits[]` representation with a `weekly_scoped` Fable entry at 0%, which the current parser ignores.
- Sonnet and Opus have no separate model quota claim in this account response. They must not be shown with fabricated percentages; their effective constraint is the shared 7-day window unless later response headers provide model-specific data.
- Live tests confirmed Sonnet 4.6 and Opus 4.8 return normal text. Fable's successful HTTP response has `stop_reason=refusal`, explaining the empty reply without treating it as a transport failure.
- The response also exposes `overage=rejected`; this is extra-usage eligibility, not a model quota window, and must be excluded from model quota bands.
- Anthropic's public plan docs do not document a fixed Fable/Opus 50% sharing rule. Pro currently documents one all-model weekly limit; Max documents an all-model weekly limit plus a Sonnet-only weekly limit. Fable-to-Opus safety fallback is billed and counted as Opus.
- This account's OAuth usage payload has an inactive Fable `weekly_scoped` item. Treating `percent=0, is_active=false, resets_at=null` as 100% remaining was misleading; inactive scoped items must fall back to shared seven-day quota.
- Management account tests bypass the runtime usage plugin, so they previously updated `test_status` but not the usage ledger that powers the one-hour availability chart.
# Permanent API Key And Pool List Follow-up (2026-07-13)

- API-key expiry currently exists in four places: create/patch management payloads, Store authentication, the safe API response type, and create/edit/list UI.
- The top-level API Key and account-pool pages share the global usage window, so merely hiding their controls would leave an invisible window inherited from another page.
- Pool names are already clickable, but the callback currently opens the pool overview rather than the account list.
- The compatibility-safe change is to retain the nullable database column, clear existing values in a new migration, remove expiry from public interfaces, and ignore legacy values during authentication.
# Current Findings: Per-window quota confidence and freshness

- `QuotaWindow` already persists `source`, `updated_at`, `utilization_known`, status, remaining, and reset; no SQLite migration is required.
- The management account DTO currently exposes only account-level `quota_source` and `quota_freshness`, while routing already evaluates individual window timestamps.
- Frontend helpers distinguish exact utilization, hard exhaustion, observed-only headers, shared fallback, and unknown data, but do not consistently account for each window's age.
- The safe implementation is to derive canonical window `confidence` and `freshness` at the management boundary while leaving stored raw evidence and scheduler semantics unchanged.
- Use two orthogonal dimensions: confidence is `exact/shared/observed/unknown`; freshness is `fresh/stale/unknown`. A stale exact observation remains diagnostically exact but is not presented as current balance.
- Add a derived account-level list containing exactly 5h, 7d, Sonnet, Opus, and Fable. Missing model windows inherit only the shared 7d display evidence and are marked `shared`; they are never inserted into persisted quota windows or routing state.
- `observed_at` should use the window `updated_at`, with the quota snapshot `checked_at` only as a legacy fallback. An elapsed reset also makes the display state stale.
- Existing status-only header merges intentionally preserve older OAuth percentage provenance. The derived state must therefore report the source/time of the displayed percentage, while retaining observed status/reset details from the raw window.
- The routing-events endpoint currently caps results at 80 but returns no total or offset support; the page renders all 80 rows at once, which creates an increasingly tall page and prevents access to older history.
- The existing `UsageQuery` and SQL filter builder can support pagination additively by adding a normalized offset plus a matching count query. Other usage aggregation callers ignore the offset.
- The pool header range control filters event occurrence time (`created_at`); labeling it `事件范围` and the table column `发生时间` removes the current ambiguity.
