# Progress: Claude Code Account Pool Observability

## Session Log
- Started implementation for account-pool observability, dedicated logs, unified metrics, and SSE-driven UI refresh.
- Confirmed top metrics currently miss account-pool usage because old Claude API Pool metrics filter by `config:claude-api-pool[`.
- Confirmed routing events table exists but route acquisition failures are not wired into it.
- Replaced previous planning files with this task's current plan/findings/progress.
- Added account-pool route event hook from `sdk/cliproxy/auth` into `internal/resourcepool`, recording selected/success/upstream_error/rejected decisions.
- Added account-pool JSONL management APIs for log config, tail, clear, and download.
- Reworked account-pool stats API to aggregate scoped route pressure plus SQLite usage ledger, local rejects, real token totals, and recent errors.
- Updated resource console queries/SSE invalidation, top metrics, usage aggregation, routing event table, log panel, and account capacity display.
- `go build -o test-output ./cmd/server` passed once after backend fixes; removed `test-output`.
- `npm run type-check --prefix web/resource-console` passed.
- Fixed the account-pool scheduler scope regression: `claude_oauth_pool=true` auths are eligible for `/claude-acc-pool` scope while `claude_api_pool=true` auths remain excluded.
- Final verification passed:
  - `go test ./...`
  - `go build -o test-output ./cmd/server && rm -f test-output`
  - `npm run type-check --prefix web/resource-console`
  - `npm run build --prefix web/resource-console`
- Local smoke used a temporary server on `127.0.0.1:28318` with a temporary management key. The real `/claude-acc-pool/v1/messages` request reached the selected OAuth account but failed upstream with `401 authentication_error: Invalid authentication credentials`; startup also reported `invalid_grant`, so this is a local OAuth credential issue rather than an observability wiring failure.
- Smoke confirmed the failed request wrote `usage_failure`, `account_result`, and `route_upstream_error` JSONL records, appended selected/upstream_error routing events, updated stats/usage endpoints, and refreshed the account-pool UI through SSE. The UI showed recent `unauthorized` errors, card RPM `1/20`, separate concurrency/RPM/sticky buffer capacity, the logs panel, and recent routing events.
- Fixed log download to use authenticated `fetch` with `X-Management-Key` and Blob download instead of putting the management key in the URL query. Re-ran `npm run type-check --prefix web/resource-console`, `npm run build --prefix web/resource-console`, and `go build -o test-output ./cmd/server && rm -f test-output`; all passed.
