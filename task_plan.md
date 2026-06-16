# Task Plan: Claude Code Account Pool Observability

## Goal
Unify Claude Code Account Pool runtime metrics, usage accounting, routing diagnostics, dedicated logs, and SSE-driven UI refresh so local rejections and real token usage are visible.

## Phases
- [complete] Inspect current resourcepool metrics, usage ledger, routing events, SSE, and console data flow.
- [complete] Add backend account-pool log config, JSONL logger, real usage fields, routing event recording, and management APIs.
- [complete] Update resource console metrics, card capacity display, usage summary, routing events, and logs UI.
- [complete] Run Go/frontend checks and local smoke with a real account-pool request if available.

## Constraints
- Do not modify `internal/translator/`.
- Preserve original `/v1/*` and Claude API Pool behavior.
- Do not log tokens, full API keys, full proxy URLs, or user message content.
- Local routing rejections are diagnostics only and must not enter billing/usage ledger.

## Decisions
- Dedicated logs use JSONL in `acc-pool-logs/account-pool.log`, relative to the resource-pools config directory by default.
- Runtime top metrics use resourcepool usage ledger plus scoped route status, not the old Claude API Pool metrics source.
- Account cards show concurrency, RPM, and sticky buffer separately instead of a mixed capacity number.
- SSE events invalidate stats, usage, routing events, logs, and accounts; one request can trigger several refreshes because these surfaces are written independently.
