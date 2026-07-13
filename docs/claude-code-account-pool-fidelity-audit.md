# Claude Code Account Pool Fidelity and Enforcement-Risk Audit

**Audit date:** 2026-07-14
**Repository state reviewed:** `d190b5f9` plus the current uncommitted protocol-consistency implementation
**Purpose:** Provide an independent reviewer with enough evidence to assess request fidelity, recent probe failures, deployment consistency, and account-restriction risk.

## Executive Summary

The account-pool implementation is **partially Claude Code-compatible**, but it is not equivalent to a real Claude Code process for ordinary API callers.

There are two distinct request modes:

1. A request positively identified as real Claude Code is largely passed through. Its real system prompt, tools, and conversation Session ID are preserved, while account credentials and account identity metadata are projected for the selected pool account.
2. An ordinary API request is converted into an Agent SDK-like request. It receives a Claude Code billing block, a stable Agent SDK identity block, and the repository's versioned tool-independent prompt. Client tools are preserved, but nonexistent Claude Code tools are not invented.

The ordinary-request path therefore injects a substantial stable prompt, but it does **not** inject the complete dynamic prompt emitted by a real Claude Code process. That dynamic prompt depends on the actual tool registry, permission mode, working directory, project instructions, plugins, runtime state, feature flags, and conversation state. Injecting a copied full prompt without those capabilities would create contradictions and would not make the request equivalent to Claude Code.

The original audit identified concrete Session, quota-transport, scheduling, proxy, Header-order, and deployment-observability mismatches. The current worktree now addresses them by:

- Centralizing account-pool Session extraction, excluding `X-Client-Request-Id`, and adding a one-hour scoped fallback for no-Session calls.
- Sending `/api/oauth/usage` with the account Profile UA, OAuth beta, compatible HTTP/1.1 transport, and bound proxy.
- Making `account-quota.interval` authoritative and deduplicating manual/background probes per account.
- Failing closed whenever a configured proxy is malformed; only empty or explicit `direct` settings permit direct routing.
- Upgrading the exact built-in baseline to `2.1.207-r3` with the observed HTTP/1.1 Header order while leaving unverified native TLS fixtures unchanged.
- Persisting a database instance identity and exposing a Management-only, redacted runtime diagnostics surface.

Two material boundaries remain. Stable device identity still depends on retaining the same SQLite account row/database across deployments, and the ordinary mimic's stable core is intentionally not an exact copy of a full dynamic Claude Code runtime. Subscription OAuth pooling for third-party users also remains a provider-policy risk independent of headers, TLS, prompts, or Session metadata.

No evidence collected here can establish the exact reason Anthropic restricted an account. The user authorized a small live capture through the local Claude Code installation, but that installation is configured for a custom API Base URL and has no usable direct Anthropic OAuth login when those settings are excluded. The capture is therefore valid evidence of Claude Code's custom-base request construction, not a first-party subscription OAuth baseline.

## Scope and Evidence Boundary

This report uses:

- Current repository source code.
- Existing redacted local Claude Code 2.1.207 captures.
- A user-authorized, redacted live Claude Code 2.1.207 capture through the locally configured custom API Base URL.
- Local routing and usage records from low-volume development tests.
- Public Anthropic legal/compliance guidance.
- Public implementation discussions from sub2api and oh-my-pi.

This report does not use:

- Provider-side enforcement telemetry.
- A live test through the currently restricted account.
- A direct first-party Anthropic OAuth capture.
- Unredacted credentials, proxy URLs, account UUIDs, or Session IDs.
- Claims that matching a client fingerprint prevents enforcement.

## Live Claude Code 2.1.207 Capture

### Environment and limitations

The capture ran from an isolated temporary directory with safe mode enabled, no project instructions, no plugins, no MCP servers, and no persisted conversation. Claude's own API debug log first revealed that user settings inject a custom Base URL and authentication token. Removing all setting sources changed `claude auth status` to `loggedIn=false`, so no request was presented as direct first-party OAuth evidence.

The HTTP shape was then captured through a redacting MITM add-on. It persisted only:

- Ordered Header names and allowlisted non-secret values.
- Hashes and lengths for identifiers and text blocks.
- Body key order, model, cache positions, tool names/counts, and metadata component hashes.
- Numeric usage and SSE event types.

It did not persist Authorization values, tokens, cookies, prompt text, response text, account identifiers, proxy credentials, or raw Session IDs. MITM changes the TLS path, so this run cannot validate the native ClientHello, JA3, JA4, or direct ALPN behavior.

### Request categories and dynamic body shape

One print-mode turn emitted two concurrent `POST /v1/messages` requests:

1. `source=generate_session_title`: approximately 2.3 KB, no tools, a 1,190-character request-specific prompt block, thinking disabled, and no cache breakpoints.
2. `source=sdk`: the actual user turn, with adaptive thinking, context management, effort configuration, and the normal stable system prefix.

With built-in tools disabled, the main request was approximately 4.5 KB. Its top-level system array had exactly three blocks:

- A 74-character billing block.
- The 62-character Agent SDK identity block.
- A 3,168-character stable Claude Code block with `Harness`, `Environment`, and `Context management` headings.

With safe-mode built-in tools enabled, the main body grew to approximately 86.5 KB and declared 26 real tools plus a 1,467-character runtime system message. The stable three-block prefix remained unchanged. This is direct evidence that a normal Claude Code request is a coordinated prompt, tool registry, runtime context, and client-side tool loop rather than a static prompt string.

### Stable and source-specific identity

Repeated processes using one explicit Session ID produced the same:

- `X-Claude-Code-Session-Id` hash.
- Nested `metadata.user_id.session_id` hash.
- Device ID hash.
- Main and title prompt hashes.
- Source-specific billing suffixes.

The Session Header exactly matched the nested metadata Session. The current metadata encoding is a JSON string containing `device_id`, `account_uuid`, and `session_id`. The custom API mode left `account_uuid` empty; the account pool is expected to replace that component with the selected OAuth account UUID while preserving the real conversation Session.

Billing fingerprints were stable by request category, not globally identical: the main SDK request and session-title request used different three-character suffixes. Neither captured billing block contained a `cch` field.

### HTTP tuple, cache, and usage

The capture confirmed this tuple:

- HTTP/1.1.
- `claude-cli/2.1.207 (external, sdk-cli)`.
- Stainless package `0.94.0`.
- Node `v26.3.0`.
- `MacOS/arm64`.
- Session Header before Stainless and Anthropic feature Headers.

The exact live Header order differs from the repository's pinned order. In the capture, Authorization was second, Stainless fields were ordered `arch/lang/os/package/retry/runtime/runtime-version/timeout`, and `anthropic-beta` preceded dangerous-direct-browser/version. The repository currently puts Authorization near the end and orders several of these fields differently.

The custom-base main request sent three ephemeral cache breakpoints with omitted TTL. Its response reported five-minute cache creation, as expected for custom API mode. This does not contradict the documented first-party subscription behavior that can explicitly select one-hour caching; it proves that cache policy is authentication/mode dependent and must not be inferred from the CLI version alone.

On a repeated run, the main request returned 25 input, 20,924 cache-read, and 4 output tokens. The concurrent title request separately returned 134 input, 20,220 cache-read, and 16 output tokens. Claude's final `iterations` summary exposed only the main SDK request, reinforcing that gateway billing must count real upstream attempts rather than downstream summarized iterations.

Interactive `/usage` displayed local `API Usage Billing` totals and made no `/api/oauth/usage` call. A natural `count_tokens` request was also not observed; forcing one with an artificially huge context was deliberately avoided.

## Current Request Modes

### Real Claude Code passthrough

The executor classifies a request as real Claude Code only when several signals agree:

- `User-Agent` starts with `claude-cli/` and contains a valid version.
- `metadata.user_id` follows the Claude Code device/account/session structure.
- The first system block contains a matching Claude Code billing header.
- The body contains a Claude Code identity block.
- Required Claude headers are present.
- When present, `X-Claude-Code-Session-Id` matches the metadata Session ID.

Relevant code:

- `internal/runtime/executor/claude_executor.go:2103-2137`
- `internal/runtime/executor/claude_executor.go:2217-2273`

For this mode, the system prompt and tools supplied by the real client are not replaced with a synthetic tool set. Account metadata is rewritten for the selected account, and the account-pool cache TTL policy can still normalize explicit cache breakpoints.

### Ordinary API mimic

An ordinary `/claude-acc-pool/v1/*` request is transformed as follows:

1. Any untrusted client-provided Claude Code billing prefix is removed.
2. The client's original `system` text is preserved as context in the first user message. Cached system blocks retain their ordering and cache metadata in the current uncommitted fix.
3. The outbound top-level `system` becomes three blocks:
   - Claude Code-style billing metadata.
   - `You are a Claude agent, built on Anthropic's Claude Agent SDK.`
   - The stable, tool-independent Claude Code 2.1.207 prompt sections.
4. Client-provided tools remain present.
5. No fake `Read`, `Bash`, `Edit`, `Agent`, or other Claude Code tools are added.

Relevant code:

- `internal/runtime/executor/claude_executor.go:2372-2417`
- `internal/runtime/executor/helps/claude_system_prompt.go:5-76`

The `2.1.207-r3` ordinary core includes:

- Agent SDK identity and software-engineering context.
- A requirement to use only capabilities and tools actually present in the request.
- Safety, prompt-injection, scope, and truthful-reporting guidance.
- Tone, style, and output-efficiency guidance.

It no longer claims that a Claude Code harness, terminal, permission mode, filesystem, or built-in tool registry exists for an ordinary API caller.

It intentionally excludes dynamic tool descriptions and runtime-specific context.

## Why a Synthetic Full Prompt Is Not a Reliable Fix

A full Claude Code request is more than a static prompt string. Its dynamic sections must agree with the real client state:

- Exact installed version and enabled experiments.
- Actual executable tool names and JSON schemas.
- Permission mode and allowed operations.
- Working directory, repository state, and project instructions.
- Plugins, MCP servers, hooks, and skills.
- Session compaction and reminder state.
- Client-side handling for tool calls and tool results.

Copying these sections into an ordinary API request without implementing the corresponding client behavior creates observable and functional inconsistencies:

- The model may call tools the downstream client cannot execute.
- Tool calls and tool results will not follow a real Claude Code interaction loop.
- A fixed copied prompt can become stale while headers claim a newer version.
- Token and prompt-cache patterns become much larger without matching activity.
- Client system instructions may conflict with the invented runtime instructions.

The current design choice is therefore coherent: real Claude Code requests preserve their real dynamic prompt and tools; ordinary callers receive a stable Agent SDK-like shape and only the tools they can actually service.

This choice improves protocol consistency. It does not make subscription credential pooling compliant, and it cannot guarantee account survival.

## Upstream-Fidelity Findings

### 1. Subscription OAuth pooling is an independent policy risk

**Severity:** Structural / outside fingerprint fixes

Anthropic's current Claude Code legal and compliance guidance says third-party products should use Anthropic Console API keys or supported cloud providers. It also states that Free, Pro, and Max credentials must not be routed on behalf of users by third-party products.

This means a technically accurate request fingerprint does not resolve the product-policy issue. Account sharing, third-party brokering, concurrency, geography, and behavioral patterns remain independently observable.

References:

- <https://code.claude.com/docs/en/legal-and-compliance>
- <https://www.anthropic.com/legal/consumer-terms>
- <https://support.anthropic.com/en/articles/8241253-trust-and-safety-warnings-and-appeals>

### 2. Ordinary Session semantics are now stable and scoped

**Status:** Resolved in the current worktree

Four locally captured requests from one real Claude Code 2.1.207 conversation reused one Session ID, with the Header and `metadata.user_id.session_id` matching on every request. The implementation now follows that invariant:

- Real Claude Code preserves the inbound Session value.
- Explicit ordinary Sessions support metadata, `X-Session-ID`, `Session-Id`, `X-Amp-Thread-Id`, `conversation_id`, and `session_id`.
- `X-Client-Request-Id` is not an account-pool Session source.
- Explicit ordinary Sessions are deterministic across account failover and isolated by pool, non-secret API-key identity, and conversation.
- No-Session ordinary calls reuse a sliding one-hour Session isolated by pool, Key, and selected account. This fallback does not create scheduler affinity.
- The final Session Header and nested metadata value are identical.

### 3. Background OAuth usage traffic now uses the account profile

**Status:** Resolved in the current worktree

OAuth usage probes now use the selected account's Profile User-Agent, OAuth beta, Claude Code-compatible HTTP/1.1 transport, and account-bound proxy. They carry no Session, billing prompt, model, or inference body. `CLIProxyAPI Resource Pool` is no longer sent. A safe persisted probe summary records only time, Profile revision, transport category, proxy mode/resource ID, and status code.

### 4. Quota scheduling now honors the configured interval

**Status:** Resolved in the current worktree

The 15-second scheduler tick only scans due accounts. Successful active probes, passive quota Header observations, and successful inference postponement all calculate the next check from `account-quota.interval` plus bounded jitter. Shortening the configuration can pull forward an old fixed deadline, manual refresh bypasses due checks, and manual/background calls are singleflight-deduplicated per database/account.

### 5. Account identity is stable only with persistent SQLite state

**Severity:** Medium deployment risk

Each account has a persisted `cloak_user_id`. The runtime extracts a device component from it, while a real OAuth `account_uuid` overrides the synthetic account component.

Relevant code:

- `internal/resourcepool/store.go:99+`
- `internal/resourcepool/store.go:1645+`
- `internal/resourcepool/store.go:1810+`
- `internal/runtime/executor/claude_executor.go:2432-2456`

Rebuilding the binary does not change this identity. Recreating the database, importing the account as a new row, or mounting a different database does.

For Docker deployment, the configured database must remain under the persistent `/CLIProxyAPI/data` mount. Copying only `config.yaml` is insufficient.

### 6. Configured proxy errors now fail closed

**Status:** Resolved in the current worktree

Empty proxy configuration and explicit `direct` remain valid direct-routing modes. Any malformed configured proxy now installs an error dialer in both Claude OAuth and protected uTLS transports. Login, refresh, quota, and inference therefore return a proxy error instead of silently moving the account to the server's direct IP.

### 7. The built-in Header order is aligned in `2.1.207-r3`

**Status:** Resolved for the captured HTTP/1.1 evidence; native TLS remains intentionally unchanged

The fresh live capture still matches these built-in values:

- `claude-cli/2.1.207 (external, sdk-cli)`
- Stainless SDK package `0.94.0`
- Node `v26.3.0`
- `MacOS/arm64`

The `2.1.207-r3` built-in profile now places Authorization second, uses the observed Stainless `arch/lang/os/package/retry/runtime/runtime-version/timeout` order, and places beta before dangerous-browser-access/version. Exact built-in r2 baselines migrate automatically; customized profiles remain untouched.

The capture still cannot revalidate JA3/JA4/ALPN because the deliberate MITM hop terminates native TLS. Those values therefore remain unchanged pending a first-party native capture.

A Linux Docker host does not automatically leak Linux/amd64 through these pinned account-pool headers. However, profile fixtures need a reproducible versioned capture and should be updated atomically across UA, Header order, body shape, and TLS evidence. A permanently fixed or partially updated snapshot becomes internally inconsistent.

### 8. Credential-layer OAuth beta is merged with client capabilities

**Status:** Resolved in the current worktree; direct first-party confirmation remains pending

The fresh custom-base Claude Code requests did not include `oauth-2025-04-20`, which is expected for that client mode. The account-pool pipeline now preserves client/body capability betas and adds only the selected OAuth credential's required `oauth-2025-04-20` item. Real passthrough keeps inbound order while inserting the missing credential requirement. API-key, non-account-pool, and other-provider paths are unchanged. A direct first-party OAuth capture is still needed to validate the provider's current wire requirement independently.

## Recent Probe Failures and Their Causes

### Downstream `input_tokens` looked too high

Observed example: an ordinary streaming request returned `input_tokens=101` even with pure mode enabled.

Important distinction:

- Anthropic sees and bills the transformed upstream request.
- Pure mode only rewrites the usage object returned downstream.
- Pure mode cannot make the injected upstream prompt disappear.
- A downstream visible-token value remains an estimate unless the provider separately reports client-visible and gateway-injected tokens.

The current uncommitted fix improves this by estimating the original client-visible request when profile/cache overhead cannot be separated safely. It does not alter upstream billing or enforcement telemetry.

### `count_tokens=630` but message usage reported `17`

The probe compared an upstream token count for a rewritten request against a downstream pure-mode usage value that had profile overhead removed. The two values were produced under different accounting views.

The current uncommitted fix applies the same client-visible estimation policy to `count_tokens` when the ordinary client did not request prompt caching. This removes a large fixed offset in downstream compatibility tests, but it remains a presentation policy rather than an upstream accounting change.

### Explicit prompt-cache probe did not write the expected client block

The ordinary mimic path moved client `system` text into the first user message but previously flattened the blocks and discarded their `cache_control` metadata. The observed cache read therefore came from the injected stable profile, while the client's large cached system block was not preserved as its own breakpoint.

The current uncommitted fix moves cached system blocks individually, preserves cache metadata and order, and then applies the existing account-pool 1-hour TTL policy and four-breakpoint limit.

### Anthropic error envelope was incomplete

The deployed response used `error.type=upstream_error` for an HTTP 400 and omitted the top-level `request_id` even though request-ID headers were present.

The current uncommitted fix:

- Normalizes generic HTTP 400 errors to `invalid_request_error`.
- Adds top-level `request_id`.
- Reuses the same request ID in the response body and headers.
- Applies the same shape to streaming terminal errors.

### OAuth appeared not to refresh

An OAuth refresh can replace an expired access token when the refresh token remains valid. It cannot restore an account that Anthropic has disabled, restricted, or invalidated server-side. The observed account state was consistent with an account restriction rather than a simple access-token expiry.

There are still background request-fidelity issues to fix, but those do not imply that refresh can recover a restricted account.

### Local debugging survived while server use was restricted quickly

The local ledger contained only a small number of successful attempts. That workload is not equivalent to sustained server use by multiple callers.

Possible differences include:

- More concurrency and more distinct conversations.
- One account used simultaneously from local and server environments.
- Different exit IPs or abrupt region changes.
- A recreated database/device identity during deployment.
- Periodic background usage calls from the server.
- Third-party account pooling behavior itself.

The current evidence does not identify which one triggered enforcement.

## Deployment Findings

- The server had a separate systemd-managed instance and a Docker instance. They listened on different ports and were separate deployments, but duplicate use of the same account credentials across deployments should be avoided.
- The Docker service stores active configuration and databases under the host `docker-data` directory mounted to `/CLIProxyAPI/data`.
- Resource-pool configuration placed only in the repository root is not visible to that container unless copied or mounted under the active data directory.
- Pulling Git changes does not update an already built binary or Docker image. A rebuild and container recreation/restart are required.
- A server built from Git commit `d190b5f9` does not include the current uncommitted token, cache, and error-envelope fixes.
- Several earlier test discrepancies were old-binary or wrong-key/configuration issues rather than upstream Anthropic behavior.
- The current diagnostics endpoint exposes the active database path and a persistent instance fingerprint. A copied database keeps that fingerprint; a newly initialized or incorrectly mounted database does not.
- Builds produced without version ldflags report an unknown Commit and are marked for attention. `scripts/build-all.sh` injects the version, Commit, and build time used by deployment diagnostics.

## Current Worktree State

The current uncommitted worktree now combines the downstream compatibility fixes with the protocol-consistency implementation. It addresses:

- Pure-mode visible usage estimation, `count_tokens`, explicit cache metadata, and Anthropic error envelopes.
- Stable/scoped account-pool Sessions and removal of `X-Client-Request-Id` from Session extraction.
- `2.1.207-r3` Header-order/profile migration and credential-aware beta merging.
- Configured quota intervals, jitter, singleflight, account-compatible usage transport, and safe probe persistence.
- Fail-closed proxy behavior for OAuth and protected uTLS paths.
- Persistent database identity and a redacted Management diagnostics API/UI.

Offline and local synthetic verification can cover these behaviors. A valid authorized account is still required for the final bound-proxy quota refresh, ordinary short request, and genuine Claude Code passthrough smoke.

## What Comparative Projects Actually Support

The useful patterns from sub2api, oh-my-pi, switchroom, claude-statusline, and similar projects are operational consistency patterns, not proven enforcement bypasses:

- Persist one device identity per account instead of randomizing it per request.
- Keep the real OAuth account UUID paired with that account identity.
- Reuse one Session ID for one conversation and isolate it by pool/account context.
- Bind inference, refresh, quota, and login traffic to the same account proxy and fail closed when that route is invalid.
- Cache quota responses, use singleflight/deduplication, honor `Retry-After`, and add bounded jitter instead of polling every account in lockstep.
- Treat missing model quota claims as unknown/shared rather than fabricating 100% remaining.
- Use bounded retries, cooldowns, concurrency, and RPM controls to protect service reliability.
- Pin a verified client profile to a release; do not randomly rotate TLS or Header identities per request.

These techniques reduce self-inflicted inconsistencies and request storms. They do not demonstrate that subscription credential brokering is supported, and they cannot establish why a provider restricted a specific account.

## Recommended Remediation Order

### P0: Choose a supported production authentication model

For an external multi-user product, use Anthropic Console API keys or a supported cloud provider. This is the only recommendation that addresses the provider-policy boundary. Request-shape work should be treated as protocol correctness, not as a substitute for supported authentication.

### P1: Make account background traffic internally coherent

- Send quota usage requests through the same account proxy.
- Reuse the account's Claude Code User-Agent, header policy, and compatible transport profile.
- Honor the configured refresh interval.
- Add jitter and singleflight/deduplication without creating idle query storms.

### P1: Refresh the versioned Claude Code profile coherently

- Replace the stale pinned Header order with a tested 2.1.207 fixture, while keeping authentication-mode differences explicit.
- Preserve genuine Claude Code Headers, prompt blocks, tools, and Session metadata in passthrough mode except for the required account credential/identity projection.
- Reconcile client capability betas with the selected upstream credential instead of blindly replacing or broadly synthesizing the complete beta list.
- Keep custom-base 5m and direct subscription 1h cache evidence separate; do not hard-code one observed mode as universal Claude Code behavior.
- Store hashes/lengths and structural fixtures, not private prompt text or account identifiers.
- Revalidate native TLS separately without MITM before changing the pinned JA3/JA4/ALPN fixture.

### P1: Correct Session semantics

- Preserve real Claude Code Session IDs unchanged.
- Use an explicit downstream conversation/session identifier when provided.
- Remove `X-Client-Request-Id` as a Session source.
- Define a bounded stable fallback for callers that provide no Session ID, with clear isolation by API key/pool/client and inactivity expiry.
- Keep the header Session ID and `metadata.user_id.session_id` identical.

### P1: Fail closed on proxy errors

- Reject malformed bound proxies before any Anthropic request.
- Never silently retry direct when an account is configured to use a proxy.
- Verify login, refresh, quota, and inference exit through the intended account route.

### P1: Add redacted deployment diagnostics

Expose or log only non-secret diagnostics:

- Active database path and database identity hash.
- Account row ID hash and device identity hash.
- Active profile revision/version.
- Bound proxy resource ID and observed exit IP.
- Last quota request transport/profile category.
- Current Session source category, without recording the Session value.

This would make local/server drift visible without leaking credentials.

### P2: Keep ordinary API behavior coherent, not fictitiously complete

- Retain the stable Agent SDK-like identity for ordinary callers.
- Preserve client tools and client system context.
- Do not claim unavailable Claude Code tools.
- Keep downstream usage and errors clean, while retaining raw upstream usage in the internal ledger.

## Verification Plan

No restricted account is required for most verification. The current worktree includes local coverage for:

1. Explicit, fallback, TTL-expired, Key/pool/account-isolated Sessions and `X-Client-Request-Id` exclusion.
2. Ordered Headers, ordinary/real system and tool boundaries, billing fixtures, cache policy, and OAuth beta merging.
3. Configured quota intervals, shortening, jitter, manual/background singleflight, Token refresh, request shape, and failure handling.
4. Invalid HTTP/SOCKS proxy configurations with zero direct upstream calls in covered OAuth, quota, and inference transports.
5. Probe and database-identity persistence plus diagnostics redaction across response, SQLite, and safe summaries.
6. Downstream streaming, `count_tokens`, explicit cache, and Anthropic error-envelope compatibility through local recording upstreams.

Final local verification completed `gofmt`, full Go tests, the required backend build, frontend type-check/build, diff hygiene, and populated 375/768/1024/1440 browser checks. A valid, authorized account is still required for the final bound-proxy quota refresh, ordinary short request, and real Claude Code passthrough smoke. A separate direct first-party OAuth capture is also needed before native TLS or credential-beta fixtures are changed again.

## Questions for Independent Review

1. Is the ordinary Agent SDK identity the most internally consistent shape for clients that cannot execute Claude Code's native tools?
2. Is the one-hour pool-Key-selected-account fallback the least surprising policy for stateless clients that supply no conversation identity?
3. Does a direct first-party OAuth capture confirm that `oauth-2025-04-20` remains credential-required in the current release?
4. Should a build without injected Commit metadata be operationally `attention`, or only informational in local development?
5. Which downstream pure-mode guarantees should be documented as estimates rather than exact token accounting?
6. Should the subscription OAuth pool be explicitly marked development-only while a separate Console API-key pool is introduced for production?
7. Which native TLS evidence can be collected without a terminating MITM before JA3/JA4/ALPN fixtures are reconsidered?

## External Comparative References

- sub2api stable Session change: <https://github.com/Wei-Shaw/sub2api/commit/480f0cba2268cf9584645bc953df77b86107fb44>
- sub2api account identity mismatch discussion: <https://github.com/Wei-Shaw/sub2api/issues/766>
- oh-my-pi Session inflation fix: <https://github.com/can1357/oh-my-pi/pull/971>

These projects are useful implementation comparisons. Their behavior is not evidence that subscription OAuth pooling is supported or protected from enforcement.
