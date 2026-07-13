import type { AccountRuntime, ClaudeCodeAccount, HealthStatus, ProxyResource } from "./api";

export function formatTime(value?: string) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  return date.toLocaleString("zh-CN", { hour12: false });
}

export function proxyDisplay(proxy?: ProxyResource) {
  if (!proxy) {
    return "未绑定";
  }
  return proxy.exit_ip || proxy.proxy_url_preview || proxy.proxy_url || proxy.name;
}

export function healthText(status: HealthStatus, enabled = true) {
  if (!enabled || status === "disabled") {
    return "已禁用";
  }
  if (status === "healthy") {
    return "健康";
  }
  if (status === "unhealthy") {
    return "异常";
  }
  return "未知";
}

export function runtimeHealth(account: ClaudeCodeAccount, runtime?: AccountRuntime) {
  if (!account.enabled) {
    return 0;
  }
  const health = runtime?.health;
  if (typeof health === "number" && Number.isFinite(health)) {
    return Math.max(0, Math.min(100, Math.round(health)));
  }
  return 100;
}

export function successRate(runtime?: AccountRuntime) {
  if (!runtime) {
    return "-";
  }
  return `${Math.round((runtime.success_rate || 0) * 100)}%`;
}

export function tagsText(tags?: string[]) {
  return tags?.filter(Boolean).join(", ") || "-";
}

export function formatTokens(value = 0) {
  if (!Number.isFinite(value) || value <= 0) return "0";
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(value >= 10_000_000_000 ? 1 : 2)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 1 : 2)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(value >= 10_000 ? 1 : 2)}K`;
  return Math.round(value).toLocaleString("zh-CN");
}

export function formatUSD(value = 0) {
  if (!Number.isFinite(value)) return "$0.00";
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", minimumFractionDigits: 2, maximumFractionDigits: value < 1 ? 4 : 2 }).format(value);
}

export function formatPercent(value = 0) {
  if (!Number.isFinite(value)) return "0%";
  return `${value.toFixed(value >= 99 || Number.isInteger(value) ? 0 : 1)}%`;
}
