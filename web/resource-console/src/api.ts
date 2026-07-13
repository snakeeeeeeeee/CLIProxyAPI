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
  reserved: boolean;
  reserved_until?: string;
  tags: string[];
  note?: string;
  created_at: string;
  updated_at: string;
}

export interface ClaudeCodeAccount {
  id: string;
  pool_id: string;
  auth_id: string;
  cloak_user_id?: string;
  email: string;
  has_auth_data?: boolean;
  token_expires_at?: string;
  enabled: boolean;
  schedulable: boolean;
  health_status: "checking" | "healthy" | "temporarily_blocked" | "manual_recovery" | string;
  effective_schedulable: boolean;
  blocked_until?: string;
  blocked_reason?: string;
  last_health_check_at?: string;
  next_health_check_at?: string;
  quota_source?: string;
  quota_freshness: "fresh" | "stale" | "unknown" | string;
  headroom?: number;
  quota_band: "unknown" | "normal" | "degraded" | "drain_only" | "exhausted" | string;
  shared_quota_band: "unknown" | "normal" | "degraded" | "drain_only" | "exhausted" | string;
  quota_window?: string;
  quota_reset_at?: string;
  quota_window_states?: QuotaWindowState[];
  affinity_bindings: number;
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
  usage?: UsageSummaryItem;
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
  sticky_concurrency_reserve: number;
  updated_at?: string;
}

export interface AccountRuntimeCapacity extends AccountCapacity {
  capacity_used: number;
  capacity_limit: number;
  in_flight: number;
  rpm_used: number;
  rpm_limit: number;
  sticky_sessions: number;
  reserve_used: number;
  active_sessions: number;
  waiters: number;
  cooling: boolean;
  account_cooling: boolean;
  model_cooling_count: number;
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
	  source?: string;
}

export interface QuotaWindow {
  key: string;
  name: string;
  used_percent: number;
  remain_percent: number;
  utilization_known?: boolean;
  resets_at?: string;
  monthly_limit?: number;
	  used_credits?: number;
	  status?: string;
	  remaining?: number;
	  representative_claim?: string;
	  source?: string;
	  updated_at?: string;
}

export interface QuotaWindowState {
  key: "five_hour" | "seven_day" | "seven_day_sonnet" | "seven_day_opus" | "seven_day_fable" | string;
  name: string;
  confidence: "exact" | "shared" | "observed" | "unknown" | string;
  freshness: "fresh" | "stale" | "unknown" | string;
  source?: string;
  observed_at?: string;
  shared_from?: string;
  utilization_known: boolean;
  used_percent?: number;
  remain_percent?: number;
  resets_at?: string;
  status?: string;
  remaining?: number;
  exhausted: boolean;
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

export interface RoutingEffectiveConfig {
  per_account_rpm: number;
  per_account_concurrency: number;
  sticky_concurrency_reserve: number;
  max_sessions: number;
  sticky_wait_ms: number;
  fallback_wait_ms: number;
  max_waiters_per_account: number;
  max_waiters_global: number;
	  session_affinity_ttl_ms: number;
	  active_session_idle_ttl_ms: number;
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
  allow_client_cache_ttl: boolean;
  cloak: CloakEffectiveConfig;
  usage: UsageEffectiveConfig;
  log: AccountPoolLogEffectiveConfig;
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
  allow_client_cache_ttl?: boolean;
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
  routing?: {
    "per-account-rpm"?: number;
    "per-account-concurrency"?: number;
    "sticky-concurrency-reserve"?: number;
    "max-sessions"?: number;
    "sticky-wait-ms"?: number;
    "fallback-wait-ms"?: number;
    "max-waiters-per-account"?: number;
    "max-waiters-global"?: number;
	    "session-affinity-ttl-ms"?: number;
	    "active-session-idle-ttl-ms"?: number;
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
  revision?: string;
  version?: string;
  user_agent?: string;
  headers?: Record<string, string>;
  header_order?: string[];
  betas?: string[];
  system_prompt?: string;
  billing_block_enabled?: boolean;
  metadata_user_id_mode?: string;
  updated_from?: string;
  updated_at?: string;
  tls_profile?: string;
  tls_ja3?: string;
  tls_ja4?: string;
  tls_alpn?: string;
  system_prompt_mode?: string;
}

export interface ClaudeCodeProfileResponse {
  raw: ClaudeCodeProfile;
  effective: Required<Pick<ClaudeCodeProfile, "revision" | "version" | "user_agent" | "headers" | "header_order" | "betas" | "system_prompt" | "billing_block_enabled" | "metadata_user_id_mode" | "tls_profile" | "tls_ja3" | "tls_ja4" | "tls_alpn" | "system_prompt_mode">> &
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
  static_prompts_md?: string;
  static_prompts_json?: string;
  normalized_profile_json?: string;
  normalized_profile?: ClaudeCodeProfile;
  prompt_hash?: string;
  static_prompt_hash?: string;
  static_prompt_length: number;
  full_prompt_hash?: string;
  full_prompt_length: number;
  request_kind_summary?: Record<string, number>;
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
  attempt_count: number;
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
  estimated_cost: number;
  unpriced_request_count: number;
  pricing_coverage: number;
  local_reject_count: number;
  recent_errors?: RoutingEvent[];
  affinity_auto_plan?: AffinityAutoPlan;
  health: PoolHealthSummary;
  model_capacity: ModelCapacitySummary;
  pool_health_distribution?: PoolHealthDistribution;
}

export interface ClaudeCodeModel {
  id: string;
  name: string;
  alias: string;
  enabled: boolean;
  source: string;
  note?: string;
  price?: ModelPrice;
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
  pool_id: string;
  api_key_id?: string;
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
  in_flight?: number;
  concurrency_limit?: number;
  rpm_used?: number;
  rpm_limit?: number;
  attempt?: number;
  switch_count?: number;
  wait_ms?: number;
  affinity_mode?: string;
  primary_hit: boolean;
  backup_lane: boolean;
  decision: string;
  reason?: string;
  status_code?: number;
  error?: string;
  created_at: string;
}

export interface RoutingEventsPage {
  items: RoutingEvent[];
  total: number;
  limit: number;
  offset: number;
}

export interface UsageLedgerEntry {
  id?: number;
  pool_id: string;
  api_key_id?: string;
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
  cache_creation_5m_tokens?: number;
  cache_creation_1h_tokens?: number;
  raw_input_tokens?: number;
  raw_total_tokens?: number;
  estimated_cost?: number;
  price_version_id?: number;
  price_model_pattern?: string;
  pricing_status: "priced" | "estimated" | "unpriced" | string;
  success: boolean;
  created_at: string;
}

export interface UsageSummaryItem {
  key: string;
  request_count: number;
  attempt_count: number;
  success_count: number;
  failure_count: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_creation_tokens: number;
  cache_creation_5m_tokens: number;
  cache_creation_1h_tokens: number;
  raw_input_tokens: number;
  raw_total_tokens: number;
  estimated_cost: number;
  unpriced_request_count: number;
  pricing_coverage: number;
}

export interface UsageSummary {
  window_seconds: number;
  request_count: number;
  attempt_count: number;
  success_count: number;
  failure_count: number;
  success_rate: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_creation_tokens: number;
  cache_creation_5m_tokens: number;
  cache_creation_1h_tokens: number;
  raw_input_tokens: number;
  raw_total_tokens: number;
  estimated_cost: number;
  unpriced_request_count: number;
  pricing_coverage: number;
  by_pool: UsageSummaryItem[];
  by_account: UsageSummaryItem[];
  by_api_key: UsageSummaryItem[];
  by_model: UsageSummaryItem[];
  by_requested_model: UsageSummaryItem[];
  recent: UsageLedgerEntry[];
}

export type UsageWindow = "24h" | "7d" | "30d" | "all";

export interface AccountPoolSummary {
  account_count: number;
  healthy_account_count: number;
  api_key_count: number;
  request_count: number;
  attempt_count: number;
  success_rate: number;
  raw_total_tokens: number;
  estimated_cost: number;
  unpriced_request_count: number;
  pricing_coverage: number;
  health: PoolHealthSummary;
  model_capacity: ModelCapacitySummary;
}

export type PoolHealthStatus = "healthy" | "attention" | "critical" | "unavailable" | "paused" | "empty" | string;

export interface PoolHealthComponent {
  score?: number;
  base_weight: number;
  effective_weight: number;
  coverage: number;
  sample_count: number;
}

export interface PoolHealthIssue {
  code: string;
  severity: "critical" | "warning" | string;
  message: string;
  count?: number;
  model?: string;
}

export interface PoolHealthSummary {
  score?: number;
  status: PoolHealthStatus;
  confidence: number;
  components: Record<string, PoolHealthComponent>;
  issues: PoolHealthIssue[];
  as_of: string;
}

export interface PoolHealthDistribution {
  healthy: number;
  attention: number;
  critical: number;
  unavailable: number;
  paused: number;
  empty: number;
}

export interface ModelCapacityItem {
  account_count: number;
  eligible_count: number;
  routable_count: number;
  measured_count: number;
  exhausted_count: number;
  stale_count: number;
  unknown_count: number;
  exact_count: number;
  shared_count: number;
  observed_count: number;
  average_headroom?: number;
  headroom_equivalent: number;
  coverage: number;
  latest_observation_time?: string;
}

export interface ModelCapacitySummary {
  sonnet: ModelCapacityItem;
  opus: ModelCapacityItem;
  fable: ModelCapacityItem;
}

export interface ClaudeCodeAccountPool {
  id: string;
  name: string;
  description?: string;
  enabled: boolean;
  is_default: boolean;
  has_config_override: boolean;
  config_override_count: number;
  archived_at?: string;
  created_at: string;
  updated_at: string;
  summary?: AccountPoolSummary;
}

export type AccountPoolRoutingOverrides = Partial<RoutingEffectiveConfig>;

export interface AccountPoolConfigOverrides {
  pure_mode?: boolean;
  routing?: AccountPoolRoutingOverrides;
}

export interface AccountPoolConfigView {
  overrides: AccountPoolConfigOverrides;
  effective: Pick<ClaudeCodePoolEffectiveConfig, "pure_mode" | "routing">;
  global: Pick<ClaudeCodePoolEffectiveConfig, "pure_mode" | "routing">;
  sources: Record<string, "global" | "pool" | string>;
}

export type AccountPoolConfigPatch = {
  pure_mode?: boolean | null;
  routing?: ({ [K in keyof RoutingEffectiveConfig]?: RoutingEffectiveConfig[K] | null }) | null;
};

export interface ClaudeCodePoolAPIKey {
  id: string;
  pool_id: string;
  name: string;
  key_prefix: string;
  secret_available: boolean;
  enabled: boolean;
  revoked_at?: string;
  last_used_at?: string;
  created_at: string;
  updated_at: string;
  usage?: UsageSummaryItem;
}

export interface PoolAPIKeyCredential {
  item: ClaudeCodePoolAPIKey;
  secret: string;
}

export interface ModelPrice {
  version_id: number;
  revision: number;
  model_pattern: string;
  input_per_million: number;
  output_per_million: number;
  cache_write_5m_per_million: number;
  cache_write_1h_per_million: number;
  cache_read_per_million: number;
  created_at: string;
}

export interface ModelPriceVersion {
  id: number;
  revision: number;
  source: string;
  note?: string;
  created_at: string;
  prices: ModelPrice[];
}

export interface ModelPriceUpdate {
  model_pattern: string;
  input_per_million: number;
  output_per_million: number;
  cache_write_5m_per_million: number;
  cache_write_1h_per_million: number;
  cache_read_per_million: number;
  remove?: boolean;
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
  attempt?: number;
  switch_count?: number;
  wait_ms?: number;
  affinity_mode?: string;
  primary_hit?: boolean;
  backup_lane?: boolean;
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
  pure_mode: boolean;
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

export type SessionKeyJobStatus = "queued" | "running" | "cancelling" | "completed" | "cancelled" | string;

export interface SessionKeyJobItem {
  index: number;
  fingerprint: string;
  proxy_id?: string;
  proxy_name?: string;
  proxy_exit_ip?: string;
  account_id?: string;
  account_email?: string;
  status: string;
  error_code?: string;
  error_message?: string;
  started_at?: string;
  completed_at?: string;
}

export interface SessionKeyJob {
  id: string;
  status: SessionKeyJobStatus;
  concurrency: number;
  total: number;
  queued: number;
  running: number;
  succeeded: number;
  updated: number;
  failed: number;
  no_proxy: number;
  cancelled: number;
  items: SessionKeyJobItem[];
  created_at: string;
  started_at?: string;
  completed_at?: string;
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
  accountPools: (window: UsageWindow = "30d", includeArchived = false) =>
    request<{ items: ClaudeCodeAccountPool[] }>(`/claude-code-account-pools?window=${window}&include_archived=${includeArchived}`),
  accountPool: (id: string, window: UsageWindow = "30d") =>
    request<{ item: ClaudeCodeAccountPool }>(`/claude-code-account-pools/${encodeURIComponent(id)}?window=${window}`),
  createAccountPool: (payload: { name: string; description?: string }) =>
    request<{ item: ClaudeCodeAccountPool }>("/claude-code-account-pools", { method: "POST", body: JSON.stringify(payload) }),
  patchAccountPool: (id: string, payload: Partial<Pick<ClaudeCodeAccountPool, "name" | "description" | "enabled">>) =>
    request<{ item: ClaudeCodeAccountPool }>(`/claude-code-account-pools/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(payload) }),
  archiveAccountPool: (id: string) =>
    request<{ status: string }>(`/claude-code-account-pools/${encodeURIComponent(id)}`, { method: "DELETE" }),
  accountPoolConfig: (id: string) =>
    request<AccountPoolConfigView>(`/claude-code-account-pools/${encodeURIComponent(id)}/config`),
  patchAccountPoolConfig: (id: string, payload: AccountPoolConfigPatch) =>
    request<AccountPoolConfigView>(`/claude-code-account-pools/${encodeURIComponent(id)}/config`, { method: "PATCH", body: JSON.stringify(payload) }),
  accounts: (poolID = "", window: UsageWindow = "30d") => {
    const params = new URLSearchParams({ window });
    if (poolID) params.set("pool_id", poolID);
    return request<{ items: AccountRow[] }>(`/claude-code-account-pool/accounts?${params.toString()}`);
  },
  poolAPIKeys: (poolID = "", window: UsageWindow = "30d", includeRevoked = false) => {
    const params = new URLSearchParams({ window, include_revoked: String(includeRevoked) });
    if (poolID) params.set("pool_id", poolID);
    return request<{ items: ClaudeCodePoolAPIKey[] }>(`/claude-code-account-pool/api-keys?${params.toString()}`);
  },
  createPoolAPIKey: (payload: { pool_id: string; name: string }) =>
    request<PoolAPIKeyCredential>("/claude-code-account-pool/api-keys", { method: "POST", body: JSON.stringify(payload) }),
  patchPoolAPIKey: (id: string, payload: { name?: string; enabled?: boolean }) =>
    request<{ item: ClaudeCodePoolAPIKey }>(`/claude-code-account-pool/api-keys/${encodeURIComponent(id)}`, { method: "PATCH", body: JSON.stringify(payload) }),
  poolAPIKeySecret: (id: string) =>
    request<{ secret: string }>(`/claude-code-account-pool/api-keys/${encodeURIComponent(id)}/secret`),
  revokePoolAPIKey: (id: string) =>
    request<{ status: string }>(`/claude-code-account-pool/api-keys/${encodeURIComponent(id)}`, { method: "DELETE" }),
  rotatePoolAPIKey: (id: string) =>
    request<PoolAPIKeyCredential>(`/claude-code-account-pool/api-keys/${encodeURIComponent(id)}/rotate`, { method: "POST" }),
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
  poolStats: (poolID = "", window: UsageWindow = "30d") => {
    const params = new URLSearchParams({ window });
    if (poolID) params.set("pool_id", poolID);
    return request<{ stats: ClaudeCodePoolStats }>(`/claude-code-account-pool/stats?${params.toString()}`);
  },
  routingEvents: (poolID = "", window: UsageWindow = "30d", page = 1, pageSize = 20) => {
    const normalizedPage = Math.max(1, page);
    const normalizedPageSize = Math.max(1, Math.min(100, pageSize));
    const params = new URLSearchParams({
      window,
      limit: String(normalizedPageSize),
      offset: String((normalizedPage - 1) * normalizedPageSize)
    });
    if (poolID) params.set("pool_id", poolID);
    return request<RoutingEventsPage>(`/claude-code-account-pool/routing-events?${params.toString()}`);
  },
  usageSummary: (poolID = "", window: UsageWindow = "30d") => {
    const params = new URLSearchParams({ window, limit: "80" });
    if (poolID) params.set("pool_id", poolID);
    return request<{ summary: UsageSummary }>(`/claude-code-account-pool/usage?${params.toString()}`);
  },
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
  modelPrices: () => request<{ current: ModelPriceVersion; versions: ModelPriceVersion[] }>("/claude-code-account-pool/model-prices"),
  saveModelPrices: (updates: ModelPriceUpdate[], note = "") =>
    request<{ current: ModelPriceVersion }>("/claude-code-account-pool/model-prices", { method: "PUT", body: JSON.stringify({ updates, note }) }),
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
  createSessionKeyJob: (sessionKeys: string[], concurrency: number, poolID = "default") =>
    request<{ job: SessionKeyJob }>("/claude-code-account-pool/session-key-jobs", {
      method: "POST",
      body: JSON.stringify({ session_keys: sessionKeys, concurrency, pool_id: poolID })
    }),
  currentSessionKeyJob: async () => {
    try {
      return await request<{ job: SessionKeyJob }>("/claude-code-account-pool/session-key-jobs/current");
    } catch (error) {
      if (error instanceof ManagementAPIError && error.status === 404) {
        return { job: null as SessionKeyJob | null };
      }
      throw error;
    }
  },
  sessionKeyJob: (id: string) => request<{ job: SessionKeyJob }>(`/claude-code-account-pool/session-key-jobs/${id}`),
  cancelSessionKeyJob: (id: string) =>
    request<{ job: SessionKeyJob }>(`/claude-code-account-pool/session-key-jobs/${id}/cancel`, { method: "POST" }),
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
  moveAccount: (id: string, poolID: string) =>
    request<{ account: ClaudeCodeAccount }>(`/claude-code-account-pool/accounts/${id}/move`, {
      method: "POST",
      body: JSON.stringify({ pool_id: poolID })
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
	  recheckAccount: (id: string) =>
	    request<{ account: ClaudeCodeAccount }>(`/claude-code-account-pool/accounts/${id}/recheck`, { method: "POST" }),
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
