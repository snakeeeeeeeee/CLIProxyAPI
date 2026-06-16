# Findings: Claude Code Account Pool Observability

## Current State
- Top Account Pool metrics use `claudeapipool.RuntimeStats`, whose usage metrics plugin only records sources beginning with `config:claude-api-pool[`.
- Resourcepool usage ledger does record Claude Code Account Pool usage separately, but it currently powers the separate "用量账本" UI rather than top metrics.
- `claude_code_routing_events` and `RecordRoutingEvent` exist, but local route acquisition failures such as `pool_route_limited` are not consistently written there.
- Account card capacity currently mixes RPM and sticky buffer (`base_rpm + sticky_buffer`), which can produce confusing values like `0 / 130`.
- SSE already exists through resourcepool events and currently invalidates account, stats, usage, model, proxy, and config queries.

## Implementation Notes
- Usage records already include raw token breakdown in `sdk/cliproxy/usage.Detail`; resourcepool can store both display input tokens and raw input tokens.
- Log rotation can reuse `gopkg.in/natefinch/lumberjack.v2`, already present in `go.mod`.
- Management routes for account-pool stats, usage, routing events, and SSE are already registered near other resource-pool routes.
- A single request can publish multiple resourcepool events (`stats_changed`, `account_changed`) because usage, account result, and routing diagnostics are persisted separately. The current UI handles this, but a small client-side debounce can reduce duplicate refetches later if needed.
- The local smoke account currently has invalid OAuth refresh credentials (`invalid_grant`/401). Observability still works for this failure path, but a successful-token smoke requires re-authorizing the account.
