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

## Decisions
- Main config only stores `claude-api-pool.enabled` and `claude-api-pool.path`.
- Pool item identity is file order plus `item_hash`; no manual item id.
- Runtime auth IDs are stable hashes of `api-key + base-url`.
- First UI version is a dense table with pagination, filters, import/export, and drawer-style row editing.
- Virtual cache ledger now models rolling Claude cache behavior with 5m/1h buckets, cache-read plus delta cache-write on growing contexts, and context-shrink reset.
- `target-cache-reuse-ratio` now splits the anchored cacheable input budget; cache reads are capped by cache tokens previously created in the local ledger.
- Claude API Pool now uses fixed SQLite primary storage at `claude-api-pool.db`; `claude-api-pool.yaml` remains the import/export format and first-run migration source.
- Virtual cache usage rewrite anchors `input_tokens + cache_creation_input_tokens + cache_read_input_tokens` to the real Claude upstream total when present. The local ledger only splits that total into input/create/read, preserving upstream `input_tokens` when upstream cache fields exist and using local request delta only when upstream reports no cache split.

## Errors Encountered
| Error | Attempt | Resolution |
| --- | --- | --- |
| `context` undefined in management handler | Backend build 1 | Added missing `context` import. |
| ledger SSE test parsed a `data:` line as raw JSON | Backend targeted tests 1 | Stripped SSE prefix in the test assertion. |
| `go test ./internal/runtime/executor` has unrelated Antigravity URL expectation failures | Backend broad package test 1 | Ran targeted executor tests for Claude/virtual cache; will report broad-package residual if still present. |
| Management Center `npm run type-check` could not find `tsc` | Frontend verification 1 | Ran `npm install` from the existing package-lock, then type-check/build passed. |
