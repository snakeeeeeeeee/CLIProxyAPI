import { useEffect, useMemo, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { RotateCcw, Save, Undo2 } from "lucide-react";
import {
  api,
  type AccountPoolConfigPatch,
  type AccountPoolConfigView,
  type RoutingEffectiveConfig
} from "../api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/input";
import { Select } from "../components/ui/select";
import { cn } from "../lib/utils";

type RoutingKey = keyof RoutingEffectiveConfig;
type StrategyPath = "pure_mode" | `routing.${RoutingKey}`;
type StrategyValue = string | number | boolean;

interface RoutingField {
  key: RoutingKey;
  label: string;
  control: "number" | "boolean" | "select";
  options?: Array<{ value: string; label: string }>;
}

const commonFields: RoutingField[] = [
  {
    key: "account_capacity_profile",
    label: "承载档位",
    control: "select",
    options: [
      { value: "custom", label: "自定义" },
      { value: "conservative", label: "保守" },
      { value: "standard", label: "标准" },
      { value: "aggressive", label: "激进" }
    ]
  },
  { key: "per_account_rpm", label: "每账号 RPM", control: "number" },
  { key: "per_account_concurrency", label: "每账号并发", control: "number" },
  { key: "max_sessions", label: "活跃会话软上限", control: "number" },
  { key: "sticky_concurrency_reserve", label: "亲和额外并发", control: "number" },
  { key: "max_switches", label: "最大换号次数", control: "number" }
];

const waitAndRetryFields: RoutingField[] = [
  { key: "sticky_wait_ms", label: "Sticky 等待 (ms)", control: "number" },
  { key: "fallback_wait_ms", label: "普通等待 (ms)", control: "number" },
  { key: "max_waiters_per_account", label: "单账号等待上限", control: "number" },
  { key: "max_waiters_global", label: "全局等待上限", control: "number" },
  { key: "switch_delay_ms", label: "换号间隔 (ms)", control: "number" },
  { key: "same_account_retry_429", label: "429 同号重试", control: "number" },
  { key: "same_account_retry_529", label: "529 同号重试", control: "number" },
  { key: "same_account_retry_delay_ms", label: "同号重试间隔 (ms)", control: "number" },
  { key: "rate_limit_cooldown_ms", label: "429 初始冷却 (ms)", control: "number" },
  { key: "rate_limit_max_cooldown_ms", label: "429 最大冷却 (ms)", control: "number" },
  { key: "overload_cooldown_ms", label: "529 初始冷却 (ms)", control: "number" },
  { key: "overload_max_cooldown_ms", label: "529 最大冷却 (ms)", control: "number" }
];

const affinityFields: RoutingField[] = [
  { key: "cache_affinity_enabled", label: "缓存亲和", control: "boolean" },
  { key: "cache_affinity_auto", label: "自动亲和策略", control: "boolean" },
  {
    key: "cache_affinity_auto_profile",
    label: "自动亲和档位",
    control: "select",
    options: [
      { value: "cost", label: "成本优先" },
      { value: "balanced", label: "均衡" },
      { value: "throughput", label: "吞吐优先" }
    ]
  },
  { key: "session_affinity_ttl_ms", label: "会话亲和 TTL (ms)", control: "number" },
  { key: "active_session_idle_ttl_ms", label: "活跃会话 TTL (ms)", control: "number" },
  { key: "cache_affinity_min_cache_tokens", label: "最小缓存 Tokens", control: "number" },
  { key: "cache_affinity_lanes", label: "亲和 lanes", control: "number" },
  { key: "cache_affinity_max_lanes", label: "最大 lanes", control: "number" },
  { key: "cache_affinity_wait_ms", label: "亲和等待 (ms)", control: "number" },
  { key: "cache_affinity_ttl_ms", label: "缓存亲和 TTL (ms)", control: "number" }
];

const allRoutingFields = [...commonFields, ...waitAndRetryFields, ...affinityFields];

export function PoolStrategyPage({ poolID, config, loading, error, onChanged, notify }: {
  poolID: string;
  config?: AccountPoolConfigView;
  loading: boolean;
  error?: unknown;
  onChanged: () => Promise<void>;
  notify: (message: string, danger?: boolean) => void;
}) {
  const [draft, setDraft] = useState<AccountPoolConfigView["effective"]>();
  const [dirty, setDirty] = useState<Set<StrategyPath>>(new Set());
  const [resets, setResets] = useState<Set<StrategyPath>>(new Set());

  useEffect(() => {
    if (config && dirty.size === 0) setDraft(config.effective);
  }, [config, dirty.size]);

  const mutation = useMutation({
    mutationFn: ({ patch }: { patch: AccountPoolConfigPatch; success: string }) => api.patchAccountPoolConfig(poolID, patch),
    onSuccess: async (view, variables) => {
      setDraft(view.effective);
      setDirty(new Set());
      setResets(new Set());
      await onChanged();
      notify(variables.success);
    },
    onError: (mutationError) => notify(`保存失败：${errorMessage(mutationError)}`, true)
  });

  const overrideCount = useMemo(() => {
    if (!config) return 0;
    return (config.overrides.pure_mode === undefined ? 0 : 1) + Object.keys(config.overrides.routing || {}).length;
  }, [config]);

  if (loading && !config) return <div className="rounded-md border bg-card px-4 py-12 text-center text-sm text-muted-foreground">正在加载策略</div>;
  if (error && !config) return <div className="rounded-md border border-red-200 bg-red-50 px-4 py-10 text-center text-sm text-red-700">策略加载失败：{errorMessage(error)}</div>;
  if (!config || !draft) return <div className="rounded-md border bg-card px-4 py-12 text-center text-sm text-muted-foreground">暂无策略数据</div>;

  const markChanged = (path: StrategyPath, reset: boolean) => {
    setDirty((current) => new Set(current).add(path));
    setResets((current) => {
      const next = new Set(current);
      if (reset) next.add(path);
      else next.delete(path);
      return next;
    });
  };

  const updatePureMode = (value: boolean) => {
    setDraft((current) => current ? { ...current, pure_mode: value } : current);
    markChanged("pure_mode", false);
  };

  const updateRouting = (key: RoutingKey, value: StrategyValue) => {
    setDraft((current) => current ? { ...current, routing: { ...current.routing, [key]: value } as RoutingEffectiveConfig } : current);
    markChanged(`routing.${key}`, false);
  };

  const resetField = (path: StrategyPath) => {
    if (path === "pure_mode") {
      setDraft((current) => current ? { ...current, pure_mode: config.global.pure_mode } : current);
    } else {
      const key = path.slice("routing.".length) as RoutingKey;
      setDraft((current) => current ? { ...current, routing: { ...current.routing, [key]: config.global.routing[key] } as RoutingEffectiveConfig } : current);
    }
    markChanged(path, true);
  };

  const sourceFor = (path: StrategyPath) => {
    if (dirty.has(path)) return resets.has(path) ? "global" : "pool";
    return config.sources[path] === "pool" ? "pool" : "global";
  };

  const save = () => {
    const patch: AccountPoolConfigPatch = {};
    if (dirty.has("pure_mode")) patch.pure_mode = resets.has("pure_mode") ? null : draft.pure_mode;
    const routingPatch: Record<string, StrategyValue | null> = {};
    for (const field of allRoutingFields) {
      const path = `routing.${field.key}` as StrategyPath;
      if (!dirty.has(path)) continue;
      routingPatch[field.key] = resets.has(path) ? null : draft.routing[field.key];
    }
    if (Object.keys(routingPatch).length > 0) patch.routing = routingPatch as AccountPoolConfigPatch["routing"];
    mutation.mutate({ patch, success: "账号池策略已保存" });
  };

  const resetAll = () => {
    if (dirty.size > 0 && !globalThis.confirm("未保存的修改将被丢弃，继续恢复全部继承？")) return;
    mutation.mutate({ patch: { pure_mode: null, routing: null }, success: "已恢复全部全局默认策略" });
  };

  return <div className="grid min-w-0 gap-4">
    <section className="min-w-0 rounded-md border bg-card">
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
        <div className="flex min-w-0 items-center gap-2">
          <h3 className="text-sm font-semibold">策略继承</h3>
          <Badge tone={overrideCount > 0 ? "info" : "neutral"}>{overrideCount} 项池覆盖</Badge>
          {dirty.size > 0 ? <Badge tone="warning">{dirty.size} 项待保存</Badge> : null}
        </div>
        <div className="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="outline" size="sm" onClick={resetAll} disabled={mutation.isPending || (overrideCount === 0 && dirty.size === 0)}><RotateCcw className="h-4 w-4" />全部继承</Button>
          <Button type="button" size="sm" onClick={save} disabled={mutation.isPending || dirty.size === 0}><Save className="h-4 w-4" />保存更改</Button>
        </div>
      </div>
      <div className="grid divide-y">
        <PureModeField
          value={draft.pure_mode}
          globalValue={config.global.pure_mode}
          source={sourceFor("pure_mode")}
          dirty={dirty.has("pure_mode")}
          onChange={updatePureMode}
          onReset={() => resetField("pure_mode")}
        />
        {commonFields.map((field) => <StrategyField
          key={field.key}
          field={field}
          value={draft.routing[field.key]}
          globalValue={config.global.routing[field.key]}
          source={sourceFor(`routing.${field.key}`)}
          dirty={dirty.has(`routing.${field.key}`)}
          onChange={(value) => updateRouting(field.key, value)}
          onReset={() => resetField(`routing.${field.key}`)}
        />)}
      </div>
    </section>

    <details className="min-w-0 rounded-md border bg-card">
      <summary className="cursor-pointer px-4 py-3 text-sm font-semibold">高级策略</summary>
      <div className="grid border-t xl:grid-cols-2 xl:divide-x">
        <StrategyGroup title="等待、换号与冷却" fields={waitAndRetryFields} draft={draft.routing} global={config.global.routing} dirty={dirty} sourceFor={sourceFor} onChange={updateRouting} onReset={resetField} />
        <StrategyGroup title="会话与缓存亲和" fields={affinityFields} draft={draft.routing} global={config.global.routing} dirty={dirty} sourceFor={sourceFor} onChange={updateRouting} onReset={resetField} />
      </div>
    </details>
  </div>;
}

function StrategyGroup({ title, fields, draft, global, dirty, sourceFor, onChange, onReset }: {
  title: string;
  fields: RoutingField[];
  draft: RoutingEffectiveConfig;
  global: RoutingEffectiveConfig;
  dirty: Set<StrategyPath>;
  sourceFor: (path: StrategyPath) => "pool" | "global";
  onChange: (key: RoutingKey, value: StrategyValue) => void;
  onReset: (path: StrategyPath) => void;
}) {
  return <div className="min-w-0"><div className="border-b bg-muted/35 px-4 py-2.5 text-xs font-medium text-muted-foreground">{title}</div><div className="grid divide-y">{fields.map((field) => {
    const path = `routing.${field.key}` as StrategyPath;
    return <StrategyField key={field.key} field={field} value={draft[field.key]} globalValue={global[field.key]} source={sourceFor(path)} dirty={dirty.has(path)} onChange={(value) => onChange(field.key, value)} onReset={() => onReset(path)} />;
  })}</div></div>;
}

function PureModeField({ value, globalValue, source, dirty, onChange, onReset }: {
  value: boolean;
  globalValue: boolean;
  source: "pool" | "global";
  dirty: boolean;
  onChange: (value: boolean) => void;
  onReset: () => void;
}) {
  return <FieldShell label="纯净计费模式" source={source} dirty={dirty} globalText={booleanText(globalValue)} onReset={onReset}>
    <BooleanControl label="纯净计费模式" value={value} onChange={onChange} />
  </FieldShell>;
}

function StrategyField({ field, value, globalValue, source, dirty, onChange, onReset }: {
  field: RoutingField;
  value: StrategyValue;
  globalValue: StrategyValue;
  source: "pool" | "global";
  dirty: boolean;
  onChange: (value: StrategyValue) => void;
  onReset: () => void;
}) {
  return <FieldShell label={field.label} source={source} dirty={dirty} globalText={displayValue(field, globalValue)} onReset={onReset}>
    {field.control === "boolean" ? <BooleanControl label={field.label} value={Boolean(value)} onChange={onChange} /> : field.control === "select" ? <Select aria-label={field.label} className="min-h-9" value={String(value)} onChange={(event) => onChange(event.target.value)}>{field.options?.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</Select> : <Input aria-label={field.label} className="min-h-9 h-9 tabular-nums" type="number" min={0} value={Number(value)} onChange={(event) => onChange(Number(event.target.value))} />}
  </FieldShell>;
}

function FieldShell({ label, source, dirty, globalText, onReset, children }: {
  label: string;
  source: "pool" | "global";
  dirty: boolean;
  globalText: string;
  onReset: () => void;
  children: React.ReactNode;
}) {
  return <div className="grid min-w-0 gap-2 px-4 py-3 sm:grid-cols-[minmax(0,1fr)_minmax(150px,220px)_2rem] sm:items-center">
    <div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><span className="text-sm font-medium">{label}</span><Badge tone={source === "pool" ? "info" : "neutral"}>{source === "pool" ? "池覆盖" : "继承全局"}</Badge>{dirty ? <span className="text-xs text-amber-700">待保存</span> : null}</div><div className="mt-1 truncate text-xs text-muted-foreground" title={`全局值：${globalText}`}>全局值：{globalText}</div></div>
    <div className="min-w-0">{children}</div>
    <button type="button" className={cn("flex h-8 w-8 items-center justify-center rounded-md hover:bg-muted disabled:cursor-not-allowed disabled:opacity-35", source === "global" && !dirty && "text-muted-foreground")} onClick={onReset} disabled={source === "global" && !dirty} title="恢复继承" aria-label={`${label}恢复继承`}><Undo2 className="h-4 w-4" /></button>
  </div>;
}

function BooleanControl({ label, value, onChange }: { label: string; value: boolean; onChange: (value: boolean) => void }) {
  return <label className="inline-flex h-9 cursor-pointer items-center gap-2"><input type="checkbox" aria-label={label} className="peer sr-only" checked={value} onChange={(event) => onChange(event.target.checked)} /><span className="relative h-5 w-9 rounded-full bg-muted-foreground/35 transition-colors peer-checked:bg-primary peer-focus-visible:ring-2 peer-focus-visible:ring-ring"><span className="absolute left-0.5 top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform peer-checked:translate-x-4" /></span><span className="text-sm">{booleanText(value)}</span></label>;
}

function displayValue(field: RoutingField, value: StrategyValue) {
  if (field.control === "boolean") return booleanText(Boolean(value));
  if (field.control === "select") return field.options?.find((option) => option.value === String(value))?.label || String(value);
  return String(value);
}

function booleanText(value: boolean) { return value ? "开启" : "关闭"; }
function errorMessage(error: unknown) { return error instanceof Error ? error.message : String(error); }
