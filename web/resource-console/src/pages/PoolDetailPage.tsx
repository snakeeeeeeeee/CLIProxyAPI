import { AlertTriangle, ArrowLeft, CircleDollarSign, Gauge, KeyRound, ListTree, SlidersHorizontal, UsersRound } from "lucide-react";
import { useLayoutEffect, useRef, type ReactNode } from "react";
import type { ClaudeCodeAccountPool, ClaudeCodePoolStats, ModelCapacityItem, PoolHealthComponent, UsageSummary, UsageWindow } from "../api";
import { HealthGauge, healthStatusClass, healthStatusLabel } from "../components/HealthGauge";
import { Badge } from "../components/ui/badge";
import { UsageWindowControl } from "../components/UsageWindowControl";
import { formatPercent, formatTime, formatTokens, formatUSD } from "../format";
import { cn } from "../lib/utils";

export type PoolDetailTab = "overview" | "accounts" | "api-keys" | "events" | "strategy";

const tabs: Array<{ value: PoolDetailTab; label: string; icon: typeof Gauge }> = [
  { value: "overview", label: "概览", icon: Gauge },
  { value: "accounts", label: "账号", icon: UsersRound },
  { value: "api-keys", label: "API Keys", icon: KeyRound },
  { value: "events", label: "调度事件", icon: ListTree },
  { value: "strategy", label: "策略", icon: SlidersHorizontal }
];

export function PoolDetailPage({ pool, tab, stats, usage, window, children, onBack, onTabChange, onWindowChange }: {
  pool?: ClaudeCodeAccountPool;
  tab: PoolDetailTab;
  stats?: ClaudeCodePoolStats;
  usage?: UsageSummary;
  window: UsageWindow;
  children?: ReactNode;
  onBack: () => void;
  onTabChange: (tab: PoolDetailTab) => void;
  onWindowChange: (window: UsageWindow) => void;
}) {
  const tabScrollerRef = useRef<HTMLDivElement>(null);
  const activeTabRef = useRef<HTMLButtonElement>(null);

  useLayoutEffect(() => {
    const scroller = tabScrollerRef.current;
    const activeTab = activeTabRef.current;
    if (!scroller || !activeTab) return;
    const activeLeft = activeTab.offsetLeft;
    const activeRight = activeLeft + activeTab.offsetWidth;
    if (activeLeft < scroller.scrollLeft) scroller.scrollLeft = activeLeft;
    else if (activeRight > scroller.scrollLeft + scroller.clientWidth) scroller.scrollLeft = activeRight - scroller.clientWidth;
  }, [pool?.id, tab]);

  if (!pool) return <div className="rounded-md border bg-card p-10 text-center text-sm text-muted-foreground">账号池不存在或已归档</div>;
  return <div className="grid min-w-0 gap-4">
    <div className="flex flex-wrap items-start justify-between gap-3"><div className="flex min-w-0 items-start gap-2"><button type="button" className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md border hover:bg-muted" onClick={onBack} title="返回账号池"><ArrowLeft className="h-4 w-4" /></button><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><h2 className="truncate text-lg font-semibold">{pool.name}</h2>{pool.is_default ? <Badge tone="info">default</Badge> : null}<Badge tone={pool.enabled ? "success" : "warning"}>{pool.enabled ? "运行中" : "已暂停"}</Badge>{pool.has_config_override ? <Badge tone="neutral">{pool.config_override_count} 项策略覆盖</Badge> : null}</div><p className="mt-1 truncate text-xs text-muted-foreground">{pool.description || pool.id}</p></div></div>{tab === "overview" || tab === "events" ? <div className="flex items-center gap-2"><span className="text-xs text-muted-foreground">{tab === "events" ? "事件范围" : "统计范围"}</span><UsageWindowControl value={window} onChange={onWindowChange} ariaLabel={tab === "events" ? "调度事件发生时间范围" : "统计时间范围"} /></div> : null}</div>
    <div ref={tabScrollerRef} className="overflow-x-auto"><div className="inline-flex min-w-full gap-1 border-b" role="tablist">{tabs.map(({ value, label, icon: Icon }) => <button ref={tab === value ? activeTabRef : undefined} key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => onTabChange(value)} className={cn("flex h-10 shrink-0 items-center gap-2 border-b-2 border-transparent px-3 text-sm text-muted-foreground", tab === value && "border-primary font-medium text-primary")}><Icon className="h-4 w-4" />{label}</button>)}</div></div>
    {tab === "overview" ? <PoolOverview stats={stats} usage={usage} /> : children}
  </div>;
}

function PoolOverview({ stats, usage }: { stats?: ClaudeCodePoolStats; usage?: UsageSummary }) {
  return <div className="grid min-w-0 gap-4">
    <HealthOverview stats={stats} />
    <ModelCapacity capacity={stats?.model_capacity} />
    <section className="grid divide-x rounded-md border bg-card sm:grid-cols-2 xl:grid-cols-4 max-sm:divide-x-0 max-sm:divide-y"><Metric icon={UsersRound} label="账号" value={`${stats?.available_accounts || 0} / ${stats?.account_count || 0}`} detail="当前可用 / 总数" /><Metric icon={Gauge} label="请求 / Attempts" value={`${usage?.request_count || 0} / ${usage?.attempt_count || 0}`} detail={`${formatPercent(usage?.success_rate || 0)} 成功`} /><Metric icon={CircleDollarSign} label="Tokens / 成本" value={formatTokens(usage?.raw_total_tokens || 0)} detail={formatUSD(usage?.estimated_cost || 0)} /><Metric icon={KeyRound} label="计价覆盖" value={formatPercent(usage?.pricing_coverage ?? 100)} detail={`${usage?.unpriced_request_count || 0} 个未计价请求`} /></section>
    <section className="rounded-md border bg-card"><div className="border-b px-4 py-3"><h3 className="text-sm font-semibold">Token 构成</h3></div><div className="grid grid-cols-2 gap-px bg-border sm:grid-cols-3 lg:grid-cols-6"><TokenMetric label="输入" value={usage?.input_tokens || 0} /><TokenMetric label="输出" value={usage?.output_tokens || 0} /><TokenMetric label="缓存读取" value={usage?.cache_read_tokens || 0} /><TokenMetric label="缓存写入" value={usage?.cache_creation_tokens || 0} /><TokenMetric label="写入 5m" value={usage?.cache_creation_5m_tokens || 0} /><TokenMetric label="写入 1h" value={usage?.cache_creation_1h_tokens || 0} /></div></section>
  </div>;
}

function HealthOverview({ stats }: { stats?: ClaudeCodePoolStats }) {
  const health = stats?.health;
  const components = health?.components || {};
  return <section className="min-w-0 rounded-md border bg-card">
    <div className="flex items-center justify-between gap-3 border-b px-4 py-3"><h3 className="text-sm font-semibold">综合健康度</h3><span className="text-xs text-muted-foreground">更新于 {health?.as_of ? formatTime(health.as_of) : "--"}</span></div>
    <div className="grid min-w-0 gap-5 p-4 lg:grid-cols-[minmax(260px,340px)_1fr] lg:items-center lg:p-5">
      <div className="grid justify-items-center border-b pb-4 lg:border-b-0 lg:border-r lg:pb-0 lg:pr-5"><HealthGauge health={health} label="当前账号池综合健康度" /><div className="flex items-center gap-2 text-xs"><span className={healthStatusClass(health?.status)}>{healthStatusLabel(health?.status)}</span><span className="text-muted-foreground">可信度 {formatPercent((health?.confidence || 0) * 100)}</span></div></div>
      <div className="grid min-w-0 gap-4">
        <div className="grid gap-px overflow-hidden rounded-md border bg-border sm:grid-cols-2"><HealthComponent label="账号就绪度" component={components.account_readiness} /><HealthComponent label="请求可靠性" component={components.request_reliability} /><HealthComponent label="额度韧性" component={components.quota_resilience} /><HealthComponent label="负载余量" component={components.load_headroom} /></div>
        {health?.issues?.length ? <div className="grid gap-2"><div className="text-xs font-medium text-muted-foreground">主要问题</div>{health.issues.slice(0, 4).map((issue, index) => <div key={`${issue.code}-${issue.model || "all"}-${index}`} className="flex min-w-0 items-start gap-2 text-sm"><AlertTriangle className={cn("mt-0.5 h-4 w-4 shrink-0", issue.severity === "critical" ? "text-red-600" : "text-amber-600")} /><span className="min-w-0 flex-1">{issue.message}{issue.model ? ` · ${issue.model}` : ""}</span>{issue.count ? <span className="tabular-nums text-muted-foreground">{issue.count}</span> : null}</div>)}</div> : <div className="text-sm text-muted-foreground">暂无需要处理的问题</div>}
      </div>
    </div>
  </section>;
}

function HealthComponent({ label, component }: { label: string; component?: PoolHealthComponent }) {
  return <div className="min-w-0 bg-card p-3"><div className="flex items-center justify-between gap-2 text-xs text-muted-foreground"><span>{label}</span><span>权重 {formatPercent((component?.effective_weight || 0) * 100)}</span></div><div className="mt-2 text-lg font-semibold tabular-nums">{typeof component?.score === "number" ? formatPercent(component.score) : "--"}</div><div className="mt-1 text-xs text-muted-foreground">覆盖 {formatPercent((component?.coverage || 0) * 100)} · 样本 {component?.sample_count || 0}</div></div>;
}

function ModelCapacity({ capacity }: { capacity?: ClaudeCodePoolStats["model_capacity"] }) {
  return <section className="rounded-md border bg-card"><div className="border-b px-4 py-3"><h3 className="text-sm font-semibold">模型相对可用额度</h3></div><div className="grid divide-y md:grid-cols-3 md:divide-x md:divide-y-0"><CapacityItem label="Sonnet" item={capacity?.sonnet} /><CapacityItem label="Opus" item={capacity?.opus} /><CapacityItem label="Fable" item={capacity?.fable} /></div></section>;
}

function CapacityItem({ label, item }: { label: string; item?: ModelCapacityItem }) {
  const available = typeof item?.average_headroom === "number" ? formatPercent(item.average_headroom * 100) : "--";
  return <div className="min-w-0 p-4"><div className="flex items-center justify-between gap-2"><span className="text-sm font-medium">{label}</span><Badge tone={item?.exhausted_count ? "danger" : item?.stale_count ? "warning" : "neutral"}>{item?.routable_count || 0} 可路由</Badge></div><div className="mt-3 text-2xl font-semibold tabular-nums">{available}</div><div className="mt-1 text-xs text-muted-foreground">{(item?.headroom_equivalent || 0).toFixed(2)} 账号当量 · 覆盖 {formatPercent((item?.coverage || 0) * 100)}</div><div className="mt-3 grid grid-cols-3 gap-2 border-t pt-3 text-xs"><CapacityMetric label="可计量" value={item?.measured_count || 0} /><CapacityMetric label="过期" value={item?.stale_count || 0} /><CapacityMetric label="未知" value={item?.unknown_count || 0} /></div></div>;
}

function CapacityMetric({ label, value }: { label: string; value: number }) { return <div><div className="text-muted-foreground">{label}</div><div className="mt-1 font-semibold tabular-nums">{value}</div></div>; }
function Metric({ icon: Icon, label, value, detail }: { icon: typeof Gauge; label: string; value: string; detail: string }) { return <div className="min-w-0 p-4"><div className="flex items-center gap-2 text-xs text-muted-foreground"><Icon className="h-4 w-4" />{label}</div><div className="mt-2 truncate text-lg font-semibold tabular-nums">{value}</div><div className="mt-1 text-xs text-muted-foreground">{detail}</div></div>; }
function TokenMetric({ label, value }: { label: string; value: number }) { return <div className="bg-card p-4"><div className="text-xs text-muted-foreground">{label}</div><div className="mt-1 font-semibold tabular-nums">{formatTokens(value)}</div></div>; }
