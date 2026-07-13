import { ArrowRight, ArrowUpDown, CircleDollarSign, KeyRound, Server, ShieldCheck, Waypoints } from "lucide-react";
import { useMemo, useState } from "react";
import type { ClaudeCodeAccountPool, ClaudeCodePoolStats, ModelCapacitySummary, PoolHealthStatus, UsageWindow } from "../api";
import { HealthGauge, HealthRing, healthStatusClass, healthStatusLabel } from "../components/HealthGauge";
import { Badge } from "../components/ui/badge";
import { UsageWindowControl } from "../components/UsageWindowControl";
import { formatPercent, formatTokens, formatUSD } from "../format";

type HealthSort = "none" | "ascending" | "descending";

export function OverviewPage({ pools, stats, window, loading, onWindowChange, onOpenPool }: {
  pools: ClaudeCodeAccountPool[];
  stats?: ClaudeCodePoolStats;
  window: UsageWindow;
  loading: boolean;
  onWindowChange: (window: UsageWindow) => void;
  onOpenPool: (poolID: string) => void;
}) {
  const [healthSort, setHealthSort] = useState<HealthSort>("none");
  const orderedPools = useMemo(() => {
    if (healthSort === "none") return pools;
    return pools.map((pool, index) => ({ pool, index })).sort((left, right) => {
      const difference = sortableHealth(left.pool) - sortableHealth(right.pool);
      if (difference === 0) return left.index - right.index;
      return healthSort === "ascending" ? difference : -difference;
    }).map(({ pool }) => pool);
  }, [healthSort, pools]);
  if (loading) return <div className="h-40 animate-pulse rounded-md border bg-card" />;
  return (
    <div className="grid min-w-0 gap-4">
      <div className="flex min-w-0 justify-end"><UsageWindowControl value={window} onChange={onWindowChange} /></div>
      <section className="grid min-w-0 divide-x rounded-md border bg-card sm:grid-cols-2 xl:grid-cols-5 max-sm:divide-x-0 max-sm:divide-y">
        <KPI icon={Waypoints} label="账号池" value={String(pools.filter((pool) => !pool.archived_at).length)} detail={`${stats?.account_count || 0} 个账号`} />
        <KPI icon={ShieldCheck} label="可调度账号" value={String(stats?.available_accounts || 0)} detail={`${stats?.cooling_accounts || 0} 个冷却账号`} />
        <KPI icon={Server} label="请求 / Attempts" value={`${stats?.request_count || 0} / ${stats?.attempt_count || 0}`} detail={`${formatPercent(stats?.success_rate || 0)} 成功`} />
        <KPI icon={CircleDollarSign} label="估算成本" value={formatUSD(stats?.estimated_cost || 0)} detail={`${formatTokens(stats?.raw_total_tokens || 0)} Tokens`} />
        <KPI icon={KeyRound} label="计价覆盖" value={formatPercent(stats?.pricing_coverage ?? 100)} detail={`${stats?.unpriced_request_count || 0} 个未计价请求`} />
      </section>

      <ResourceHealth pools={pools} stats={stats} onOpenPool={onOpenPool} />

      <section className="min-w-0 rounded-md border bg-card">
        <div className="flex items-center justify-between gap-3 border-b px-4 py-3"><div><h2 className="text-sm font-semibold">账号池</h2><p className="mt-0.5 text-xs text-muted-foreground">健康、模型余量与用量汇总</p></div><Badge tone="neutral">{pools.length} 个</Badge></div>
        {pools.length === 0 ? <div className="px-4 py-12 text-center text-sm text-muted-foreground">暂无账号池</div> : (
          <>
            <div className="hidden overflow-x-auto xl:block">
              <table className="w-full min-w-[980px] text-sm">
                <thead><tr className="border-b bg-muted/70 text-left text-xs text-muted-foreground"><th className="px-4 py-2.5">账号池</th><th className="px-3 py-2.5"><button type="button" className="inline-flex items-center gap-1 font-medium hover:text-foreground" onClick={() => setHealthSort((current) => current === "descending" ? "ascending" : "descending")} aria-label="按健康度排序">健康度<ArrowUpDown className="h-3.5 w-3.5" /></button></th><th className="px-3 py-2.5">S / O / F</th><th className="px-3 py-2.5">账号</th><th className="px-3 py-2.5">请求 / Attempts</th><th className="px-3 py-2.5">Tokens</th><th className="px-3 py-2.5">成本</th><th className="px-3 py-2.5">成功率</th><th className="px-3 py-2.5">Keys</th><th className="w-12 px-3 py-2.5" /></tr></thead>
                <tbody>{orderedPools.map((pool) => <PoolRow key={pool.id} pool={pool} onOpen={() => onOpenPool(pool.id)} />)}</tbody>
              </table>
            </div>
            <div className="divide-y xl:hidden">{orderedPools.map((pool) => <PoolMobile key={pool.id} pool={pool} onOpen={() => onOpenPool(pool.id)} />)}</div>
          </>
        )}
      </section>
    </div>
  );
}

function ResourceHealth({ pools, stats, onOpenPool }: { pools: ClaudeCodeAccountPool[]; stats?: ClaudeCodePoolStats; onOpenPool: (id: string) => void }) {
  const distribution = stats?.pool_health_distribution;
  const worstPool = pools.filter((pool) => pool.summary?.health.status !== "paused" && pool.summary?.health.status !== "empty").sort((a, b) => sortableHealth(a) - sortableHealth(b))[0];
  return <section className="min-w-0 rounded-md border bg-card">
    <div className="border-b px-4 py-3"><h2 className="text-sm font-semibold">资源池健康</h2></div>
    <div className="grid min-w-0 gap-5 p-4 lg:grid-cols-[minmax(260px,320px)_1fr] lg:items-center lg:p-5">
      <div className="grid justify-items-center border-b pb-4 lg:border-b-0 lg:border-r lg:pb-0 lg:pr-5">
        <HealthGauge health={stats?.health} label="所有启用账号池综合健康度" />
        <div className="text-xs text-muted-foreground">可信度 {formatPercent((stats?.health.confidence || 0) * 100)}</div>
      </div>
      <div className="grid min-w-0 gap-4">
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
          <Distribution label="健康" value={distribution?.healthy || 0} tone="success" />
          <Distribution label="关注" value={distribution?.attention || 0} tone="warning" />
          <Distribution label="异常" value={distribution?.critical || 0} tone="danger" />
          <Distribution label="不可用" value={distribution?.unavailable || 0} tone="danger" />
          <Distribution label="已暂停" value={distribution?.paused || 0} tone="neutral" />
          <Distribution label="空池" value={distribution?.empty || 0} tone="neutral" />
        </div>
        <div className="min-w-0 border-t pt-3">
          <div className="text-xs text-muted-foreground">需要优先关注</div>
          {worstPool ? <button type="button" className="mt-2 flex w-full min-w-0 items-center gap-3 text-left hover:text-primary" onClick={() => onOpenPool(worstPool.id)}><HealthRing health={worstPool.summary?.health} /><span className="min-w-0 flex-1"><span className="block truncate text-sm font-medium">{worstPool.name}</span><span className="mt-0.5 block truncate text-xs text-muted-foreground">{worstPool.summary?.health.issues?.[0]?.message || healthStatusLabel(worstPool.summary?.health.status)}</span></span><ArrowRight className="h-4 w-4 shrink-0" /></button> : <div className="mt-2 text-sm text-muted-foreground">暂无异常池</div>}
        </div>
      </div>
    </div>
  </section>;
}

function Distribution({ label, value, tone }: { label: string; value: number; tone: "success" | "warning" | "danger" | "neutral" | "info" }) {
  return <div className="flex min-w-0 items-center justify-between gap-2 rounded-md border px-3 py-2"><Badge tone={tone}>{label}</Badge><span className="font-semibold tabular-nums">{value}</span></div>;
}

function KPI({ icon: Icon, label, value, detail }: { icon: typeof Server; label: string; value: string; detail: string }) {
  return <div className="min-w-0 p-4"><div className="flex items-center gap-2 text-xs text-muted-foreground"><Icon className="h-4 w-4" />{label}</div><div className="mt-2 truncate text-lg font-semibold tabular-nums" title={value}>{value}</div><div className="mt-1 truncate text-xs text-muted-foreground" title={detail}>{detail}</div></div>;
}

function PoolRow({ pool, onOpen }: { pool: ClaudeCodeAccountPool; onOpen: () => void }) {
  const summary = pool.summary;
  return <tr className="cursor-pointer border-b last:border-b-0 hover:bg-muted/35" onClick={onOpen}><td className="px-4 py-3"><div className="flex items-center gap-2"><span className="font-medium">{pool.name}</span>{pool.is_default ? <Badge tone="info">default</Badge> : null}{pool.has_config_override ? <Badge tone="neutral">独立策略</Badge> : null}</div><div className="mt-0.5 max-w-56 truncate text-xs text-muted-foreground">{pool.description || pool.id}</div></td><td className="px-3 py-3"><div className="flex items-center gap-2"><HealthRing health={summary?.health} size={34} /><span className={healthStatusClass(summary?.health.status)}>{healthStatusLabel(summary?.health.status)}</span></div></td><td className="px-3 py-3"><CapacityCompact capacity={summary?.model_capacity} /></td><td className="px-3 py-3 tabular-nums">{summary?.healthy_account_count || 0} / {summary?.account_count || 0}</td><td className="px-3 py-3 tabular-nums">{summary?.request_count || 0} / {summary?.attempt_count || 0}</td><td className="px-3 py-3 tabular-nums">{formatTokens(summary?.raw_total_tokens || 0)}</td><td className="px-3 py-3 font-medium tabular-nums">{formatUSD(summary?.estimated_cost || 0)}</td><td className="px-3 py-3 tabular-nums">{formatPercent(summary?.success_rate || 0)}</td><td className="px-3 py-3 tabular-nums">{summary?.api_key_count || 0}</td><td className="px-3 py-3"><ArrowRight className="h-4 w-4 text-muted-foreground" /></td></tr>;
}

function PoolMobile({ pool, onOpen }: { pool: ClaudeCodeAccountPool; onOpen: () => void }) {
  const summary = pool.summary;
  return <button type="button" className="grid w-full gap-3 p-4 text-left hover:bg-muted/35" onClick={onOpen}><div className="flex items-center gap-3"><HealthRing health={summary?.health} /><span className="min-w-0 flex-1"><span className="block truncate font-medium">{pool.name}</span><span className={"mt-0.5 block text-xs " + healthStatusClass(summary?.health.status)}>{healthStatusLabel(summary?.health.status)}</span></span><ArrowRight className="h-4 w-4 shrink-0 text-muted-foreground" /></div><CapacityCompact capacity={summary?.model_capacity} /><div className="grid grid-cols-3 gap-3 text-xs"><Metric label="账号" value={`${summary?.healthy_account_count || 0}/${summary?.account_count || 0}`} /><Metric label="请求" value={String(summary?.request_count || 0)} /><Metric label="成本" value={formatUSD(summary?.estimated_cost || 0)} /></div></button>;
}

function CapacityCompact({ capacity }: { capacity?: ModelCapacitySummary }) {
  return <div className="flex flex-wrap gap-x-2 gap-y-1 text-xs tabular-nums"><span><b className="font-medium text-muted-foreground">S</b> {headroomText(capacity?.sonnet.average_headroom)}</span><span><b className="font-medium text-muted-foreground">O</b> {headroomText(capacity?.opus.average_headroom)}</span><span><b className="font-medium text-muted-foreground">F</b> {headroomText(capacity?.fable.average_headroom)}</span></div>;
}

function headroomText(value?: number) { return typeof value === "number" ? formatPercent(value * 100) : "--"; }
function Metric({ label, value }: { label: string; value: string }) { return <div className="min-w-0"><div className="text-muted-foreground">{label}</div><div className="mt-1 truncate font-semibold tabular-nums">{value}</div></div>; }
function sortableHealth(pool: ClaudeCodeAccountPool) {
  const health = pool.summary?.health;
  if (typeof health?.score === "number") return health.score;
  const fallback: Record<PoolHealthStatus, number> = { paused: -3, empty: -2, unavailable: -1 };
  return fallback[health?.status || ""] ?? -4;
}
