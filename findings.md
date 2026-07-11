# Findings: Claude Code Account Pool Request Shape Alignment

## Current State
- Local `claude --version` reports `2.1.181 (Claude Code)`.
- Built-in account-pool profile is still `2.1.178`.
- UI fallback strings also still show `2.1.178`.
- Account-pool profile headers are applied in `applyClaudeCodeAccountPoolProfileHeaders`.
- Account-pool body profile injection is handled by `applyClaudeCodeAccountPoolProfile`.
- Existing CCH/billing logic is in `generateBillingHeader`, `checkSystemInstructionsWithPrompt`, and `signAnthropicMessagesBody`.
- Existing trace dump records final outbound body before send, but request mode inference is loose and mostly UA/tool-count based.
- Existing uTLS helper uses Chrome profile for protected hosts; there is no separate Node/Claude Code TLS profile yet.

## Implementation Notes
- Current executor order is: translate -> thinking -> payload config -> cache normalization -> extract betas -> account-pool profile -> body betas -> tool-name rewrite -> sanitize -> CCH signing -> headers -> send.
- This mostly matches the desired order, but mode detection and body rewriting need to happen inside account-pool profile handling.
- Header betas are computed after body betas and account-pool body compatibility, so sanitize decisions should share the same beta list or be deterministic from body/model/client headers.
- Trace dump can reuse `claudetrace.BuildBodyShape` for outbound shape summary.
- `checkSystemInstructionsWithPrompt` already moves ordinary user-provided `system` content into the first user message as a system-reminder for OAuth mode, so API mimic can reuse it instead of inventing a separate migration path.
- `count_tokens` currently strips account-pool metadata after profile injection, which avoids Anthropic's `metadata: Extra inputs are not permitted` error while keeping billing/system shape.

## Official Upstream Merge Findings (2026-07-11)
- Local branch is `main` at `37741a7e` and is 154 commits ahead of `origin/main` before fetching.
- `origin` points to the user's fork `snakeeeeeeeee/CLIProxyAPI`.
- `upstream` points to official `router-for-me/CLIProxyAPI`.
- The worktree contains uncommitted Claude Code request-shape, TLS, trace, account-pool log, UI, and planning-file changes from the latest completed implementation.
- Existing pre-upstream backup branches show that previous official synchronizations used the same preservation strategy.
- The merge must preserve both committed secondary development and the current uncommitted request-shape implementation.
- The current local `HEAD` is the merge commit `37741a7e` whose first parent is the secondary-development branch and whose official parent is tagged `v7.2.42` (`4c0c6029`).
- The local `upstream/main` ref is still at `v7.2.42` before the new fetch, so it is stale and does not yet indicate the current official release.
- The dirty code/UI change set is 538 insertions and 89 deletions across 15 files including planning records; `git diff --check` reports no whitespace errors.
- The secondary-development docs are fully read. They identify account-pool runtime config, SQLite state, `/claude-acc-pool/v1`, account-pool logging/SSE, trace tooling, and generated `internal/resourcepool/console.html` as protected custom integration surfaces.
- Fetch advanced official `upstream/main` from `v7.2.42` to `v7.2.66` (`e99a2056`), adding 100 official commits and 24 release tags.
- A non-mutating `git merge-tree` preview reports eight committed-history conflicts: `internal/api/handlers/management/auth_files.go`, `internal/api/server.go`, `internal/cmd/run.go`, `internal/pluginstore/install.go`, `internal/translator/gemini/openai/responses/gemini_openai-responses_request.go`, `sdk/api/handlers/handlers.go`, `sdk/cliproxy/auth/conductor.go`, and `sdk/cliproxy/service.go`.
- Official upstream also modifies `internal/runtime/executor/claude_executor.go` and its tests, but the committed-history merge preview resolves those automatically. The stashed request-shape changes may still require a second reconciliation pass there.
- Official changes include OAuth/device-flow refactoring, automatic credential refresh handling, API handler/model routing changes, plugin store refactoring, Interactions API support, and Claude/Codex usage improvements; conflict resolution must retain these upstream semantics alongside account-pool hooks.
- First-pass conflict intent is mostly composable: imports from both sides, OAuth pending-session guard before either account-pool or ordinary token save, direct-plugin install routing plus loaded-plugin lock protection, upstream prefill helper plus local thought-part protection, and upstream forced-provider execution options plus account-pool provider forcing.
- The SDK conductor conflicts require the most care: upstream separates auth-selection model from execution model and adds unauthorized-refresh behavior, while the secondary development adds account-pool scope, affinity leases, retries, result events, and local capacity limits. Both must remain in all sync/count/stream paths.
