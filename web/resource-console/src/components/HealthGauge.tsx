import { useId } from "react";
import type { PoolHealthSummary, PoolHealthStatus } from "../api";
import { cn } from "../lib/utils";

export function healthStatusLabel(status?: PoolHealthStatus) {
  switch (status) {
    case "healthy": return "健康";
    case "attention": return "关注";
    case "critical": return "异常";
    case "unavailable": return "不可用";
    case "paused": return "已暂停";
    case "empty": return "空池";
    default: return "未知";
  }
}

export function healthStatusClass(status?: PoolHealthStatus) {
  switch (status) {
    case "healthy": return "text-emerald-700";
    case "attention": return "text-amber-700";
    case "critical":
    case "unavailable": return "text-red-700";
    default: return "text-muted-foreground";
  }
}

function healthStroke(status?: PoolHealthStatus) {
  switch (status) {
    case "healthy": return "#15803d";
    case "attention": return "#b45309";
    case "critical":
    case "unavailable": return "#b91c1c";
    default: return "#64748b";
  }
}

function healthFillClass(status?: PoolHealthStatus) {
  switch (status) {
    case "healthy": return "fill-emerald-700";
    case "attention": return "fill-amber-700";
    case "critical":
    case "unavailable": return "fill-red-700";
    default: return "fill-muted-foreground";
  }
}

export function HealthGauge({ health, className, label = "综合健康度" }: { health?: PoolHealthSummary; className?: string; label?: string }) {
  const titleID = useId();
  const descriptionID = useId();
  const score = typeof health?.score === "number" ? Math.max(0, Math.min(100, health.score)) : undefined;
  const rotation = score === undefined ? 0 : -90 + score * 1.8;
  const status = healthStatusLabel(health?.status);
  return (
    <div className={cn("grid w-full max-w-[320px] justify-items-center", className)}>
      <svg viewBox="0 0 200 126" className="block h-auto w-full" role="img" aria-labelledby={`${titleID} ${descriptionID}`}>
        <title id={titleID}>{label}</title>
        <desc id={descriptionID}>{score === undefined ? status : `${score.toFixed(1)} 分，${status}`}</desc>
        <path d="M 20 100 A 80 80 0 0 1 180 100" pathLength="100" fill="none" stroke="#e2e8f0" strokeWidth="14" strokeLinecap="butt" />
        <path d="M 20 100 A 80 80 0 0 1 180 100" pathLength="100" fill="none" stroke="#dc2626" strokeWidth="14" strokeDasharray="65 35" strokeLinecap="butt" />
        <path d="M 20 100 A 80 80 0 0 1 180 100" pathLength="100" fill="none" stroke="#d97706" strokeWidth="14" strokeDasharray="20 80" strokeDashoffset="-65" strokeLinecap="butt" />
        <path d="M 20 100 A 80 80 0 0 1 180 100" pathLength="100" fill="none" stroke="#16a34a" strokeWidth="14" strokeDasharray="15 85" strokeDashoffset="-85" strokeLinecap="butt" />
        {score !== undefined ? (
          <g transform={`rotate(${rotation} 100 100)`}>
            <line x1="100" y1="100" x2="100" y2="39" stroke="#111827" strokeWidth="3" strokeLinecap="round" />
          </g>
        ) : null}
        <circle cx="100" cy="100" r="6" fill="#111827" />
        <text x="100" y="91" textAnchor="middle" className="fill-foreground text-[24px] font-semibold tabular-nums">
          {score === undefined ? "--" : Math.round(score)}
        </text>
        <text x="100" y="119" textAnchor="middle" className={cn("text-[11px] font-medium", healthFillClass(health?.status))}>
          {status}
        </text>
      </svg>
    </div>
  );
}

export function HealthRing({ health, size = 42, className }: { health?: PoolHealthSummary; size?: number; className?: string }) {
  const titleID = useId();
  const score = typeof health?.score === "number" ? Math.max(0, Math.min(100, health.score)) : undefined;
  const radius = 16;
  const circumference = 2 * Math.PI * radius;
  const offset = score === undefined ? circumference : circumference * (1 - score / 100);
  return (
    <svg width={size} height={size} viewBox="0 0 40 40" role="img" aria-labelledby={titleID} className={cn("shrink-0", className)}>
      <title id={titleID}>{score === undefined ? healthStatusLabel(health?.status) : `健康度 ${score.toFixed(1)} 分，${healthStatusLabel(health?.status)}`}</title>
      <circle cx="20" cy="20" r={radius} fill="none" stroke="#e2e8f0" strokeWidth="4" />
      {score !== undefined ? <circle cx="20" cy="20" r={radius} fill="none" stroke={healthStroke(health?.status)} strokeWidth="4" strokeLinecap="round" strokeDasharray={circumference} strokeDashoffset={offset} transform="rotate(-90 20 20)" /> : null}
      <text x="20" y="23" textAnchor="middle" className="fill-foreground text-[9px] font-semibold tabular-nums">{score === undefined ? "--" : Math.round(score)}</text>
    </svg>
  );
}
