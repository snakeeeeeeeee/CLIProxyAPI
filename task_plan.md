# Task Plan: Claude Code Account Pool Request Shape Alignment

## Goal
Align `/claude-acc-pool/v1` outbound requests with real Claude Code behavior by separating real Claude Code passthrough from ordinary API mimicry, adding account-pool-only Node/Claude TLS selection, and improving trace/log diagnostics.

## Phases
- [complete] Inspect current account-pool profile, body/header injection, CCH signing, trace dump, and uTLS implementation.
- [complete] Implement strict passthrough/mimic mode detection, metadata compatibility, body sanitization order, and profile handling.
- [complete] Add account-pool-only Claude Code/Node TLS profile selection and outbound shape logging.
- [complete] Update trace diff/tests/UI summary where needed.
- [complete] Run backend/frontend checks and feasible local trace/smoke verification.

## Constraints
- Do not modify original `/v1/*`, ordinary Claude API Pool, or unrelated providers.
- Do not auto-apply Phistory full prompt to production.
- Do not auto-inject Claude Code's 29 tools for ordinary API requests.
- Keep tool-name obfuscation disabled by default.
- Do not leak tokens, full API keys, proxy URLs, or user content in logs/traces.

## Decisions
- Real Claude Code passthrough still rewrites `metadata.user_id` to the selected account identity.
- Current local Claude Code is `2.1.181`; current built-in profile is `2.1.178`. Upgrade only if trace validation supports it.
- CCH signing must run after all body/profile/sanitize mutations.
- Account-pool TLS profile applies only to official `api.anthropic.com` requests.

---

# Task Plan: Merge Latest Official Upstream (2026-07-11)

## Goal
Merge the latest `router-for-me/CLIProxyAPI` `upstream/main` into the local `main` branch while preserving all secondary-development features and current uncommitted account-pool work.

## Phases
- [complete] Read secondary-development docs and inventory local branch, remotes, commits, and dirty worktree.
- [complete] Create recoverable safety points and fetch the latest official upstream refs.
- [complete] Inspect upstream divergence and merge `upstream/main` into local `main`.
- [complete] Resolve conflicts with priority on preserving account pool, proxy pool, console, trace, observability, and build/deploy customizations.
- [in_progress] Reapply and reconcile the pre-merge uncommitted Claude Code request-shape work.
- [pending] Run Go formatting, backend tests/build, and frontend type-check/build.
- [pending] Review the final diff and report merged upstream version, preserved custom work, and any residual risks.

## Merge Safety Rules
- Keep a backup branch pointing to the pre-merge local `main` commit.
- Save all tracked and untracked work in a named stash before merging.
- Apply rather than immediately drop the stash; remove it only after verification succeeds.
- Do not discard either side of a conflict wholesale in custom account-pool files.
- Do not push to any remote unless the user explicitly requests it.

## Errors Encountered
| Error | Attempt | Resolution |
|-------|---------|------------|
| Eight content conflicts during `git merge upstream/main` | 1 | Expected from merge preview; resolve each hunk by composing upstream semantics with secondary-development hooks. |
| `cpuVariant` undefined after automatic plugin-store merge | 1 | Updated the secondary-development runtime plugin lookup to use upstream's new three-argument `pluginCandidateDirs` API. |
| Account-pool cooldown calls used the old two-argument helper | 1 | Passed the configured delay as both wait and ceiling so official jitter support cannot exceed account-pool retry/affinity settings. |
| Official plugin-store tests failed under mixed stable/versioned filename semantics | 1 | Adopted upstream's versioned artifact paths; retained upstream's existing loaded-plugin overwrite guard instead of the obsolete early lock/stable-path overlay. |
| HOME plugin sync still set removed `VersionedFileName` | 1 | Removed the obsolete option because upstream now always installs versioned plugin artifacts. |
| Codex catalog test hard-coded pre-upgrade priority `129` | 1 | Assert non-template priority is greater than the template priority, preserving the behavior under future catalog growth. |
