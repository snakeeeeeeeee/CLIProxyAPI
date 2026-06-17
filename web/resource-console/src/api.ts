export type HealthStatus = "healthy" | "unknown" | "unhealthy" | "disabled" | string;

export interface ProxyResource {
  id: string;
  name: string;
  proxy_url: string;
  proxy_url_preview?: string;
  exit_ip: string;
  enabled: boolean;
  health_status: HealthStatus;
  latency_ms: number;
  consecutive_failures: number;
  last_checked_at?: string;
  last_error?: string;
  bound_account_id?: string;
  bound_account_email?: string;
  tags: string[];
  note?: string;
  created_at: string;
  updated_at: string;
}

export interface ClaudeCodeAccount {
  id: string;
  auth_id: string;
  cloak_user_id?: string;
  email: string;
  has_auth_data?: boolean;
  token_expires_at?: string;
  enabled: boolean;
  priority: number;
  proxy_resource_id?: string;
  proxy?: ProxyResource;
  note?: string;
  excluded_models: string[];
  quota?: AccountQuota;
  capacity?: AccountCapacity;
  runtime_capacity?: AccountRuntimeCapacity;
  availability?: AccountAvailabilitySummary;
  model_statuses?: AccountModelStatus[];
  test_status?: string;
  consecutive_failures?: number;
  last_test_at?: string;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface AccountCapacity {
  account_id?: string;
  base_rpm: number;
  concurrency_limit: number;
  max_sessions: number;
  sticky_buffer: number;
  updated_at?: string;
}

export interface AccountRuntimeCapacity extends AccountCapacity {
  capacity_used: number;
  capacity_limit: number;
  in_flight: number;
  rpm_used: number;
  rpm_limit: number;
  sticky_sessions: number;
  buffer_used: number;
  cooling: boolean;
  cooling_until?: string;
  unavailable: boolean;
}

export type AccountAvailabilityStatus = "none" | "healthy" | "degraded" | "unhealthy" | string;

export interface AccountAvailabilityBucket {
  started_at: string;
  request_count: number;
  success_count: number;
  success_rate: number;
  status: AccountAvailabilityStatus;
}

export interface AccountAvailabilitySummary {
  window_minutes: number;
  request_count: number;
  success_count: number;
  failure_count: number;
  success_rate: number;
  status: AccountAvailabilityStatus;
  buckets: AccountAvailabilityBucket[];
}

export interface AccountModelStatus {
  account_id: string;
  model: string;
  status: string;
  success_count: number;
  failure_count: number;
  rate_limit_count: number;
  overload_count: number;
  consecutive_failures: number;
  cooling_until?: string;
  last_status_code: number;
  last_error?: string;
  last_test_at?: string;
  updated_at: string;
}

export interface AccountQuota {
  account_id?: string;
  status: "ok" | "error" | "checking" | "unknown" | string;
  windows: QuotaWindow[];
  checked_at?: string;
  last_error?: string;
  raw_json?: string;
}

export interface QuotaWindow {
  key: string;
  name: string;
  used_percent: number;
  remain_percent: number;
  resets_at?: string;
  monthly_limit?: number;
  used_credits?: number;
}

export interface AccountRuntime {
  status?: string;
  status_message?: string;
  success?: number;
  failed?: number;
  success_rate?: number;
  health?: number;
  recent_requests?: unknown[];
  cooling_until?: string;
  last_error?: string;
}

export interface AccountRow {
  account: ClaudeCodeAccount;
  runtime?: AccountRuntime;
}

export interface ConsoleSummary {
  proxy_total: number;
  proxy_healthy: number;
  proxy_unknown: number;
  proxy_unhealthy: number;
  proxy_disabled: number;
  proxy_bound: number;
  account_total: number;
  account_enabled: number;
  account_bound: number;
}

export interface ResourcePoolConfig {
  enabled: boolean;
  storage: "sqlite";
  path: string;
  proxy_health: {
    enabled: boolean;
    interval: string;
    timeout: string;
    concurrency: number;
    failure_threshold: number;
    test_url: string;
    optional_exit_ip_url?: string;
  };
  claude_code: Record<string, unknown>;
  summary: ConsoleSummary;
}

export interface VirtualCacheEffectiveConfig {
  enabled: boolean;
  mode: "natural" | "forced" | string;
  hit_rate: number;
  target_cache_reuse_ratio: number;
  min_cache_tokens: number;
  max_cache_tokens: number;
  uncached_input_tokens: number;
  context_shrink_reset_ratio: number;
  min_creation_tokens: number;
  max_creation_tokens: number;
}

export interface RoutingEffectiveConfig {
  per_account_rpm: number;
  per_account_concurrency: number;
  max_switches: number;
  switch_delay_ms: number;
  rate_limit_cooldown_ms: number;
  rate_limit_max_cooldown_ms: number;
  overload_cooldown_ms: number;
  overload_max_cooldown_ms: number;
  same_account_retry_429: number;
  same_account_retry_529: number;
  same_account_retry_delay_ms: number;
  cache_affinity_enabled: boolean;
  cache_affinity_auto: boolean;
  cache_affinity_auto_profile: string;
  account_capacity_profile: string;
  cache_affinity_min_cache_tokens: number;
  cache_affinity_lanes: number;
  cache_affinity_max_lanes: number;
  cache_affinity_wait_ms: number;
  cache_affinity_ttl_ms: number;
}

export interface CloakEffectiveConfig {
  mode: "auto" | "always" | "never" | string;
  strict_mode: boolean;
  sensitive_words: string[];
}

export interface UsageEffectiveConfig {
  clean_input_tokens: boolean;
  system_prompt_overhead_tokens: number;
  profile_fingerprint: string;
}

export interface ClaudeCodePoolEffectiveConfig {
  enabled: boolean;
  pure_mode: boolean;
  cloak: CloakEffectiveConfig;
  usage: UsageEffectiveConfig;
  log: AccountPoolLogEffectiveConfig;
  virtual_cache: VirtualCacheEffectiveConfig;
  routing: RoutingEffectiveConfig;
}

export interface AccountPoolLogEffectiveConfig {
  enabled: boolean;
  level: "debug" | "info" | "warn" | "error" | string;
  dir: string;
  max_size_mb: number;
  max_backups: number;
  redact: boolean;
}

export interface AccountPoolLogRawConfig {
  enabled?: boolean;
  level?: string;
  dir?: string;
  max_size_mb?: number;
  "max-size-mb"?: number;
  max_backups?: number;
  "max-backups"?: number;
  redact?: boolean;
}

export interface ClaudeCodePoolRawConfig {
  enabled?: boolean;
  pure_mode?: boolean;
  cloak?: {
    mode?: string;
    "strict-mode"?: boolean;
    "sensitive-words"?: string[];
  };
  usage?: {
    clean_input_tokens?: boolean;
    system_prompt_overhead_tokens?: number;
  };
  log?: AccountPoolLogRawConfig;
  virtual_cache?: {
    enabled?: boolean;
    mode?: string;
    "hit-rate"?: number;
    "target-cache-reuse-ratio"?: number;
    "min-cache-tokens"?: number;
    "max-cache-tokens"?: number;
    "uncached-input-tokens"?: number;
    "context-shrink-reset-ratio"?: number;
    "min-creation-tokens"?: number;
    "max-creation-tokens"?: number;
  };
  routing?: {
    "per-account-rpm"?: number;
    "per-account-concurrency"?: number;
    "max-switches"?: number;
    "switch-delay-ms"?: number;
    "rate-limit-cooldown-ms"?: number;
    "rate-limit-max-cooldown-ms"?: number;
    "overload-cooldown-ms"?: number;
    "overload-max-cooldown-ms"?: number;
    "same-account-retry-429"?: number;
    "same-account-retry-529"?: number;
    "same-account-retry-delay-ms"?: number;
    "cache-affinity-enabled"?: boolean;
    "cache-affinity-auto"?: boolean;
    "cache-affinity-auto-profile"?: string;
    "account-capacity-profile"?: string;
    "cache-affinity-min-cache-tokens"?: number;
    "cache-affinity-lanes"?: number;
    "cache-affinity-max-lanes"?: number;
    "cache-affinity-wait-ms"?: number;
    "cache-affinity-ttl-ms"?: number;
  };
}

export interface ClaudeCodePoolConfigResponse {
  raw: ClaudeCodePoolRawConfig;
  effective: ClaudeCodePoolEffectiveConfig;
  storage?: string;
  path?: string;
}

export interface ClaudeCodeProfile {
  version?: string;
  user_agent?: string;
  headers?: Record<string, string>;
  betas?: string[];
  system_prompt?: string;
  billing_block_enabled?: boolean;
  metadata_user_id_mode?: string;
  updated_from?: string;
  updated_at?: string;
}

export interface ClaudeCodeProfileResponse {
  raw: ClaudeCodeProfile;
  effective: Required<Pick<ClaudeCodeProfile, "version" | "user_agent" | "headers" | "betas" | "system_prompt" | "billing_block_enabled" | "metadata_user_id_mode">> &
    Pick<ClaudeCodeProfile, "updated_from" | "updated_at">;
}

export interface ClaudeCodeProfileSnapshot {
  id: string;
  source: string;
  version: string;
  status: string;
  meta_json?: string;
  trace_jsonl?: string;
  prompt_md?: string;
  normalized_profile_json?: string;
  normalized_profile?: ClaudeCodeProfile;
  prompt_hash?: string;
  trace_hash?: string;
  diff_report?: string;
  fatal_count: number;
  warn_count: number;
  promoted: boolean;
  last_error?: string;
  fetched_at?: string;
  promoted_at?: string;
  created_at?: string;
  updated_at?: string;
}

export interface ClaudeCodeProfileSnapshotDiff {
  snapshot_id: string;
  version: string;
  current_version: string;
  profile_fingerprint: string;
  fatal_count: number;
  warn_count: number;
  report: string;
  issues: string[];
}

export interface AffinityAutoPlan {
  enabled: boolean;
  effective_lanes: number;
  effective_max_lanes: number;
  pool_size: number;
  available_accounts: number;
  pressure: number;
  reason: string;
}

export interface ClaudeCodePoolStats {
  window_seconds: number;
  account_count: number;
  available_accounts: number;
  cooling_accounts: number;
  in_flight: number;
  rpm_used: number;
  rpm_limit: number;
  active_affinity_keys: number;
  warm_lanes: number;
  request_count: number;
  success_count: number;
  failure_count: number;
  success_rate: number;
  real_cache_ratio: number;
  cache_read_tokens: number;
  cache_creation_tokens: number;
  input_tokens: number;
  output_tokens: number;
  raw_input_tokens: number;
  raw_total_tokens: number;
  local_reject_count: number;
  recent_errors?: RoutingEvent[];
  affinity_auto_plan?: AffinityAutoPlan;
}

export interface ClaudeCodeModel {
  id: string;
  name: string;
  alias: string;
  enabled: boolean;
  source: string;
  note?: string;
  created_at: string;
  updated_at: string;
}

export interface ClaudeCodeModelPayload {
  name?: string;
  alias?: string;
  enabled?: boolean;
  source?: string;
  note?: string;
}

export interface RoutingEvent {
  id?: number;
  request_id?: string;
  account_id?: string;
  auth_id?: string;
  model?: string;
  requested_model?: string;
  proxy_resource_id?: string;
  sticky: boolean;
  session_key?: string;
  capacity_used?: number;
  capacity_limit?: number;
  decision: string;
  reason?: string;
  status_code?: number;
  error?: string;
  created_at: string;
}

export interface UsageLedgerEntry {
  id?: number;
  request_id?: string;
  api_key_preview?: string;
  account_id?: string;
  auth_id?: string;
  model?: string;
  requested_model?: string;
  status_code?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  raw_input_tokens?: number;
  raw_total_tokens?: number;
  estimated_cost?: number;
  success: boolean;
  created_at: string;
}

export interface UsageSummaryItem {
  key: string;
  request_count: number;
  success_count: number;
  failure_count: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_creation_tokens: number;
  raw_input_tokens: number;
  raw_total_tokens: number;
  estimated_cost: number;
}

export interface UsageSummary {
  window_seconds: number;
  request_count: number;
  success_count: number;
  failure_count: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_creation_tokens: number;
  raw_input_tokens: number;
  raw_total_tokens: number;
  estimated_cost: number;
  by_account: UsageSummaryItem[];
  by_model: UsageSummaryItem[];
  by_requested_model: UsageSummaryItem[];
  recent: UsageLedgerEntry[];
}

export interface AccountPoolLogEntry {
  ts: string;
  level: string;
  event: string;
  request_id?: string;
  path?: string;
  model?: string;
  requested_model?: string;
  account_id?: string;
  auth_id?: string;
  proxy_resource_id?: string;
  sticky?: boolean;
  session_key?: string;
  in_flight?: number;
  concurrency_limit?: number;
  rpm_used?: number;
  rpm_limit?: number;
  decision?: string;
  reason?: string;
  status_code?: number;
  latency_ms?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
  total_tokens?: number;
  error?: string;
}

export interface AccountPoolLogLine {
  line: string;
  entry?: AccountPoolLogEntry;
}

export interface UsageCalibration {
  model: string;
  profile_fingerprint: string;
  overhead_tokens: number;
  effective_overhead_tokens: number;
  status: "calibrated" | "estimated" | "stale" | "failed" | string;
  estimated: boolean;
  checked_at?: string;
  last_error?: string;
  created_at?: string;
  updated_at?: string;
}

export interface UsageCalibrationResponse {
  profile_fingerprint: string;
  default_overhead: number;
  clean_input_tokens: boolean;
  items: UsageCalibration[];
}

export interface ProxyPayload {
  name?: string;
  proxy_url?: string;
  exit_ip?: string;
  enabled?: boolean;
  tags?: string[];
  note?: string;
}

export interface ImportResult {
  created: number;
  skipped: number;
  errors?: string[];
}

export interface OAuthURLResponse {
  status: string;
  url: string;
  state: string;
}

export interface OAuthStatusResponse {
  status: "ok" | "wait" | "error" | string;
  error?: string;
}

export interface OAuthCallbackResponse {
  status: "ok" | "wait" | "error" | string;
  error?: string;
}

export type ProxyBatchAction = "test" | "enable" | "disable" | "unbind" | "delete";
export type AccountBatchAction = "test" | "enable" | "disable" | "unbind" | "delete" | "reset-cooling" | "refresh-quota";

export interface ProxyBatchResult {
  action: ProxyBatchAction | AccountBatchAction | string;
  total: number;
  ok: number;
  failed: number;
  errors?: Array<{ id: string; message: string }>;
}

export type AccountBatchResult = ProxyBatchResult;

export interface AccountTestPayload {
  model?: string;
  message?: string;
}

const keyStorage = "account_pool_management_key_v2";

export class ManagementAPIError extends Error {
  status: number;

  constructor(message: string, status: number) {
    super(message);
    this.name = "ManagementAPIError";
    this.status = status;
  }
}

export function getManagementKey() {
  return normalizeManagementKey(localStorage.getItem(keyStorage));
}

export function setManagementKey(value: string) {
  const trimmed = value.trim();
  if (!trimmed) {
    localStorage.removeItem(keyStorage);
    return;
  }
  localStorage.setItem(keyStorage, trimmed);
}

function normalizeManagementKey(value: unknown) {
  if (typeof value !== "string") {
    return "";
  }
  const trimmed = value.trim();
  if (!trimmed || trimmed.startsWith("{") || trimmed.startsWith("[")) {
    return "";
  }
  return trimmed;
}

export function isManagementAuthError(error: unknown) {
  return error instanceof ManagementAPIError && (error.status === 401 || error.status === 403);
}

export function managementEventsURL() {
  const params = new URLSearchParams();
  const key = getManagementKey().trim();
  if (key) {
    params.set("key", key);
  }
  const suffix = params.toString();
  return `/v0/management/resource-pools/events${suffix ? `?${suffix}` : ""}`;
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const key = getManagementKey().trim();
  const headers = new Headers(options.headers);
  headers.set("Content-Type", "application/json");
  if (key) {
    headers.set("X-Management-Key", key);
  }
  const response = await fetch(`/v0/management${path}`, {
    ...options,
    headers
  });
  let data: unknown = {};
  try {
    data = await response.json();
  } catch {
    data = {};
  }
  if (!response.ok) {
    const payload = data as { message?: string; error?: string };
    throw new ManagementAPIError(payload.message || payload.error || `HTTP ${response.status}`, response.status);
  }
  return data as T;
}

async function download(path: string): Promise<Blob> {
  const key = getManagementKey().trim();
  const headers = new Headers();
  if (key) {
    headers.set("X-Management-Key", key);
  }
  const response = await fetch(`/v0/management${path}`, { headers });
  if (!response.ok) {
    let message = `HTTP ${response.status}`;
    try {
      const payload = (await response.json()) as { message?: string; error?: string };
      message = payload.message || payload.error || message;
    } catch {
      // Keep the status message when the download endpoint returns a non-JSON body.
    }
    throw new ManagementAPIError(message, response.status);
  }
  return response.blob();
}

export const api = {
  config: () => request<ResourcePoolConfig>("/resource-pools/config"),
  accounts: () => request<{ items: AccountRow[] }>("/claude-code-account-pool/accounts"),
  poolConfig: () => request<ClaudeCodePoolConfigResponse>("/claude-code-account-pool/config"),
  poolProfile: () => request<ClaudeCodeProfileResponse>("/claude-code-account-pool/profile"),
  profileSnapshots: () => request<{ items: ClaudeCodeProfileSnapshot[] }>("/claude-code-account-pool/profile-snapshots"),
  profileSnapshot: (id: string) => request<{ item: ClaudeCodeProfileSnapshot }>(`/claude-code-account-pool/profile-snapshots/${id}`),
  fetchProfileSnapshot: (version?: string, latest = false) =>
    request<{ item: ClaudeCodeProfileSnapshot }>("/claude-code-account-pool/profile-snapshots/fetch", {
      method: "POST",
      body: JSON.stringify({ version: version || "", latest })
    }),
  diffProfileSnapshot: (id: string) =>
    request<{ diff: ClaudeCodeProfileSnapshotDiff }>(`/claude-code-account-pool/profile-snapshots/${id}/diff`, {
      method: "POST"
    }),
  savePoolConfig: (payload: ClaudeCodePoolRawConfig) =>
    request<ClaudeCodePoolConfigResponse>("/claude-code-account-pool/config", {
      method: "PUT",
      body: JSON.stringify(payload)
    }),
  poolStats: () => request<{ stats: ClaudeCodePoolStats }>("/claude-code-account-pool/stats"),
  routingEvents: () => request<{ items: RoutingEvent[] }>("/claude-code-account-pool/routing-events?limit=80"),
  usageSummary: () => request<{ summary: UsageSummary }>("/claude-code-account-pool/usage?window=1h&limit=80"),
  poolLogConfig: () => request<{ raw: AccountPoolLogRawConfig; effective: AccountPoolLogEffectiveConfig }>("/claude-code-account-pool/log-config"),
  savePoolLogConfig: (payload: AccountPoolLogRawConfig) =>
    request<{ raw: AccountPoolLogRawConfig; effective: AccountPoolLogEffectiveConfig }>("/claude-code-account-pool/log-config", {
      method: "PUT",
      body: JSON.stringify(payload)
  }),
  poolLogs: () => request<{ items: AccountPoolLogLine[] }>("/claude-code-account-pool/logs?limit=120"),
  clearPoolLogs: () => request<{ status: string }>("/claude-code-account-pool/logs/clear", { method: "POST" }),
  downloadPoolLogs: () => download("/claude-code-account-pool/logs/download"),
  usageCalibrations: () => request<UsageCalibrationResponse>("/claude-code-account-pool/usage-calibrations"),
  calibrateUsage: (model: string, accountID?: string) =>
    request<{ item: UsageCalibration; warning?: string }>("/claude-code-account-pool/usage-calibrations/calibrate", {
      method: "POST",
      body: JSON.stringify({ model, account_id: accountID || "" })
    }),
  poolModels: () => request<{ items: ClaudeCodeModel[] }>("/claude-code-account-pool/models"),
  createPoolModel: (payload: ClaudeCodeModelPayload) =>
    request<{ item: ClaudeCodeModel }>("/claude-code-account-pool/models", {
      method: "POST",
      body: JSON.stringify(payload)
    }),
  patchPoolModel: (id: string, payload: ClaudeCodeModelPayload) =>
    request<{ item: ClaudeCodeModel }>(`/claude-code-account-pool/models/${id}`, {
      method: "PATCH",
      body: JSON.stringify(payload)
    }),
  deletePoolModel: (id: string) => request<{ status: string }>(`/claude-code-account-pool/models/${id}`, { method: "DELETE" }),
  fetchPoolModels: (accountID: string) =>
    request<{ items: ClaudeCodeModel[] }>("/claude-code-account-pool/models/fetch", {
      method: "POST",
      body: JSON.stringify({ account_id: accountID })
    }),
  proxies: () => request<{ items: ProxyResource[] }>("/proxy-pool/resources"),
  availableProxies: () => request<{ items: ProxyResource[] }>("/proxy-pool/available"),
  createProxy: (payload: ProxyPayload) =>
    request<{ item: ProxyResource }>("/proxy-pool/resources", { method: "POST", body: JSON.stringify(payload) }),
  updateProxy: (id: string, payload: ProxyPayload) =>
    request<{ item: ProxyResource }>(`/proxy-pool/resources/${id}`, { method: "PATCH", body: JSON.stringify(payload) }),
  deleteProxy: (id: string) => request<{ status: string }>(`/proxy-pool/resources/${id}`, { method: "DELETE" }),
  testProxy: (id: string) => request<{ item: ProxyResource; warning?: string }>(`/proxy-pool/resources/${id}/test`, { method: "POST" }),
  unbindProxy: (id: string) => request<{ status: string }>(`/proxy-pool/resources/${id}/unbind`, { method: "POST" }),
  batchProxies: (action: ProxyBatchAction, ids: string[]) =>
    request<ProxyBatchResult>("/proxy-pool/batch", {
      method: "POST",
      body: JSON.stringify({ action, ids })
    }),
  importProxies: (text: string) =>
    request<ImportResult>("/proxy-pool/import", { method: "POST", body: JSON.stringify({ text }) }),
  authURL: (params: URLSearchParams) =>
    request<OAuthURLResponse>(`/claude-code-account-pool/auth-url?${params.toString()}`),
  submitOAuthCallback: (redirectURL: string, state?: string) =>
    request<OAuthCallbackResponse>("/oauth-callback", {
      method: "POST",
      body: JSON.stringify({
        provider: "anthropic",
        redirect_url: redirectURL,
        state: redirectURL.includes("#") ? undefined : state
      })
    }),
  authStatus: (state: string) => request<OAuthStatusResponse>(`/get-auth-status?state=${encodeURIComponent(state)}`),
  patchAccount: (id: string, payload: Partial<ClaudeCodeAccount>) =>
    request<{ account: ClaudeCodeAccount }>(`/claude-code-account-pool/accounts/${id}`, {
      method: "PATCH",
      body: JSON.stringify(payload)
    }),
  patchAccountCapacity: (id: string, payload: Partial<AccountCapacity>) =>
    request<{ capacity: AccountCapacity }>(`/claude-code-account-pool/accounts/${id}/capacity`, {
      method: "PATCH",
      body: JSON.stringify(payload)
    }),
  testAccount: (id: string, payload: AccountTestPayload = {}) =>
    request<{ account: ClaudeCodeAccount; warning?: string; reply?: string }>(`/claude-code-account-pool/accounts/${id}/test`, {
      method: "POST",
      body: JSON.stringify(payload)
    }),
  refreshAccountQuota: (id: string) =>
    request<{ account: ClaudeCodeAccount; warning?: string }>(`/claude-code-account-pool/accounts/${id}/quota/refresh`, {
      method: "POST"
    }),
  refreshAccountToken: (id: string) =>
    request<{ account: ClaudeCodeAccount; warning?: string }>(`/claude-code-account-pool/accounts/${id}/token/refresh`, {
      method: "POST"
    }),
  deleteAccount: (id: string) => request<{ status: string }>(`/claude-code-account-pool/accounts/${id}`, { method: "DELETE" }),
  batchAccounts: (action: AccountBatchAction, ids: string[]) =>
    request<AccountBatchResult>("/claude-code-account-pool/accounts/batch", {
      method: "POST",
      body: JSON.stringify({ action, ids })
    }),
  bindAccountProxy: (accountID: string, proxyID: string) =>
    request<{ account: ClaudeCodeAccount }>(`/claude-code-account-pool/accounts/${accountID}/bind-proxy`, {
      method: "POST",
      body: JSON.stringify({ proxy_resource_id: proxyID })
    }),
  unbindAccountProxy: (accountID: string) =>
    request<{ account: ClaudeCodeAccount }>(`/claude-code-account-pool/accounts/${accountID}/unbind-proxy`, {
      method: "POST"
    }),
  resetAccountCooling: (accountID: string) =>
    request<{ status: string }>(`/claude-code-account-pool/accounts/${accountID}/reset-cooling`, { method: "POST" })
};
