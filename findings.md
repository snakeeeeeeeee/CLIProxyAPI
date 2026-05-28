# Findings

## Backend
- `config.ClaudeKey` already carries API key, base URL, proxy URL, priority, headers, models, excluded models, and disable cooling fields.
- `internal/watcher/synthesizer.ConfigSynthesizer` creates Claude API key runtime auths and uses `StableIDGenerator.Next("claude:apikey", key, base)`.
- Management routes are registered in `internal/api/server.go` under `/v0/management`.
- Management handler can persist the main config but does not currently have a service-level reload callback.
- `claude-api-pool` runtime auths are tagged with `claude_api_pool=true`, position, item hash, and serialized pool models.
- Manager routing now filters Claude candidates to pool auths whenever `claude-api-pool.enabled` is true.
- Virtual cache accounting is best placed before translator output: Claude executor publishes real usage first, then rewrites Claude response/SSE usage fields for downstream translators.

## Kiro.rs Virtual Cache Reference
- `/Users/zhangyu/code/myProject/supertoken-projects/kiro.rs/src/anthropic/usage.rs` implements a useful virtual cache ledger that does not depend on upstream cache fields being non-zero.
- Kiro splits usage into normal input, cache read, and cache creation from a local accounting total; first turn creates cache, later turns read from the local ledger.
- It tracks 5m and 1h buckets, expires them independently, and resets the ledger when the observed context shrinks below roughly 70% of the previous observed context.
- Its token estimator in `src/token.rs` is a simple local heuristic: western characters count as 1 unit, non-western as 4 units, then 4 units per token with small-text multipliers.
- The reference is useful conceptually, but its full configuration surface is more complex than needed for the Claude API Pool first pass.
- The Claude API Pool ledger now borrows the important parts: separate 5m/1h buckets, context shrink detection, cached-token capping, and rolling delta creation after a cache read.
- Prefix fingerprint is retained for diagnostics/state, but the ledger no longer resets merely because the cache-control prefix grows; real Claude prompt cache can still hit earlier prefixes in a growing conversation.
- For user/session scoping, Kiro uses the whole `metadata.user_id` identity: JSON form becomes `device/account/user/session`, plain string remains the full user id, and missing identity falls back to per-request isolation.
- Claude API Pool now uses a dedicated virtual-cache session key extractor instead of the routing-affinity extractor, so message-hash fallback does not create cross-user virtual cache sharing.
- `kiro.rs` target-cache behavior is a sliding-window feedback loop: keep recent rewritten usage samples, compute actual reuse as `cache_read_input_tokens / (input_tokens + cache_read_input_tokens + cache_creation_input_tokens)`, and gradually tune future virtual cache policy when actual reuse drifts outside a small deadband.
- Claude API Pool now borrows that feedback shape with a smaller policy surface: `target-cache-reuse-ratio` defaults to 0/off; when enabled it uses the recent 5-minute sample window to scale effective hit rate, read scale, uncached floor, and creation caps for future rewrites.
- Claude API Pool account storage is now SQLite-first. `claude-api-pool.db` is fixed beside `config.yaml`; `claude-api-pool.yaml` is only a YAML import/export format plus one-time migration source when the DB is empty.

## Frontend
- Management Center uses Vite/React/TS, with routes in `src/router/MainRoutes.tsx`.
- AI provider config APIs live in `src/services/api/providers.ts`.
- Existing Claude provider pages are card-oriented; the new Claude API Pool page should be separate and table-oriented.
- The new Management Center route is `/claude-api-pool`; sidebar navigation uses the existing icon system and locale nav keys.
- The local checkout did not have frontend dependencies installed initially, so `npm install` was required before `npm run type-check` and `npm run build`.

## Claude API Pool Cache Affinity
- Real-cache affinity routing is implemented as a routing-layer feature. It chooses upstream Claude API pool accounts using provider/model/session/cache-prefix identity and does not change the downstream virtual-cache usage rewrite.
- The affinity router uses rendezvous hashing, warm lanes, TTL pruning, and optional automatic lane expansion under 429/529/5xx pressure.
- Current pre-execution affinity is effective for Claude-format inbound requests where the Claude payload is already available before account selection. Non-Claude source formats would need a shared canonical Claude-body preparation step to avoid duplicating executor payload mutation logic before routing.
- Pool monitoring metrics are collected from the existing usage reporter before virtual-cache rewriting, so `real_cache_ratio` reflects actual upstream Claude usage, not the downstream rewritten ledger.
- Claude API pool usage metrics require preserving the synthesized `config:claude-api-pool[...]` source on usage records; pool accounts now keep that source instead of being reported as raw API keys.
