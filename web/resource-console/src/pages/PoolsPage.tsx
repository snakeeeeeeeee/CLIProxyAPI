import { Archive, Edit3, Plus, Power, PowerOff } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api, type ClaudeCodeAccountPool } from "../api";
import { HealthRing, healthStatusClass, healthStatusLabel } from "../components/HealthGauge";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Textarea } from "../components/ui/textarea";
import { formatPercent, formatTokens, formatUSD } from "../format";

export function PoolsPage({ pools, onOpenPool, onChanged, notify }: {
  pools: ClaudeCodeAccountPool[];
  onOpenPool: (id: string) => void;
  onChanged: () => Promise<void>;
  notify: (message: string, danger?: boolean) => void;
}) {
  const [editing, setEditing] = useState<ClaudeCodeAccountPool | null | undefined>(undefined);
  const patch = useMutation({ mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.patchAccountPool(id, { enabled }), onSuccess: async () => { await onChanged(); notify("账号池状态已更新"); }, onError: (error) => notify(String(error), true) });
  const archive = useMutation({ mutationFn: api.archiveAccountPool, onSuccess: async () => { await onChanged(); notify("账号池已归档"); }, onError: (error) => notify(String(error), true) });
  return <div className="grid min-w-0 gap-4">
    <div className="flex min-w-0 justify-end"><Button onClick={() => setEditing(null)}><Plus className="h-4 w-4" />新建账号池</Button></div>
    <section className="min-w-0 rounded-md border bg-card">
      <div className="hidden overflow-x-auto xl:block"><table className="w-full min-w-[940px] text-sm"><thead><tr className="border-b bg-muted/70 text-left text-xs text-muted-foreground"><th className="px-4 py-2.5">名称</th><th className="px-3 py-2.5">健康度</th><th className="px-3 py-2.5">状态</th><th className="px-3 py-2.5">账号</th><th className="px-3 py-2.5">请求</th><th className="px-3 py-2.5">Tokens</th><th className="px-3 py-2.5">成本</th><th className="px-3 py-2.5 text-right">操作</th></tr></thead><tbody>{pools.map((pool) => <tr key={pool.id} className="border-b last:border-b-0 hover:bg-muted/30"><td className="px-4 py-3"><button type="button" className="font-medium hover:text-primary" onClick={() => onOpenPool(pool.id)}>{pool.name}</button><div className="mt-0.5 flex max-w-72 items-center gap-2 truncate text-xs text-muted-foreground"><span className="truncate">{pool.description || pool.id}</span>{pool.has_config_override ? <Badge tone="neutral">{pool.config_override_count} 项覆盖</Badge> : null}</div></td><td className="px-3 py-3"><div className="flex items-center gap-2"><HealthRing health={pool.summary?.health} size={36} /><span className={healthStatusClass(pool.summary?.health.status)}>{healthStatusLabel(pool.summary?.health.status)}</span></div></td><td className="px-3 py-3"><Badge tone={pool.enabled ? "success" : "warning"}>{pool.enabled ? "运行中" : "已暂停"}</Badge></td><td className="px-3 py-3">{pool.summary?.healthy_account_count || 0} / {pool.summary?.account_count || 0}</td><td className="px-3 py-3">{pool.summary?.request_count || 0} · {formatPercent(pool.summary?.success_rate || 0)}</td><td className="px-3 py-3">{formatTokens(pool.summary?.raw_total_tokens || 0)}</td><td className="px-3 py-3 font-medium">{formatUSD(pool.summary?.estimated_cost || 0)}</td><td className="px-3 py-3"><div className="flex justify-end gap-1"><Button size="icon" variant="ghost" title="编辑" onClick={() => setEditing(pool)}><Edit3 className="h-4 w-4" /></Button><Button size="icon" variant="ghost" title={pool.enabled ? "暂停" : "启用"} onClick={() => patch.mutate({ id: pool.id, enabled: !pool.enabled })}>{pool.enabled ? <PowerOff className="h-4 w-4" /> : <Power className="h-4 w-4" />}</Button>{!pool.is_default ? <Button size="icon" variant="ghost" title="归档" onClick={() => { if (globalThis.confirm("账号池必须没有账号和有效 Key 才能归档。确认继续？")) archive.mutate(pool.id); }}><Archive className="h-4 w-4" /></Button> : null}</div></td></tr>)}</tbody></table></div>
      <div className="divide-y xl:hidden">{pools.map((pool) => <PoolMobileRow key={pool.id} pool={pool} onOpen={() => onOpenPool(pool.id)} onEdit={() => setEditing(pool)} onToggle={() => patch.mutate({ id: pool.id, enabled: !pool.enabled })} onArchive={() => { if (globalThis.confirm("账号池必须没有账号和有效 Key 才能归档。确认继续？")) archive.mutate(pool.id); }} />)}</div>
    </section>
    <PoolEditor open={editing !== undefined} pool={editing || undefined} onClose={() => setEditing(undefined)} onChanged={onChanged} notify={notify} />
  </div>;
}

function PoolMobileRow({ pool, onOpen, onEdit, onToggle, onArchive }: { pool: ClaudeCodeAccountPool; onOpen: () => void; onEdit: () => void; onToggle: () => void; onArchive: () => void }) {
  return <div className="grid gap-3 p-4"><div className="flex items-start justify-between gap-2"><button type="button" className="flex min-w-0 items-center gap-3 text-left" onClick={onOpen}><HealthRing health={pool.summary?.health} /><span className="min-w-0"><span className="block truncate font-medium">{pool.name}</span><span className={"mt-0.5 block text-xs " + healthStatusClass(pool.summary?.health.status)}>{healthStatusLabel(pool.summary?.health.status)}</span></span></button><Badge tone={pool.enabled ? "success" : "warning"}>{pool.enabled ? "运行中" : "已暂停"}</Badge></div><div className="grid grid-cols-3 gap-2 text-xs text-muted-foreground"><span>账号 {pool.summary?.account_count || 0}</span><span>请求 {pool.summary?.request_count || 0}</span><span>{formatUSD(pool.summary?.estimated_cost || 0)}</span></div>{pool.has_config_override ? <div className="text-xs text-muted-foreground">独立策略 {pool.config_override_count} 项</div> : null}<div className="flex flex-wrap justify-end gap-1"><Button size="sm" variant="outline" onClick={onEdit}>编辑</Button><Button size="sm" variant="outline" onClick={onToggle}>{pool.enabled ? "暂停" : "启用"}</Button>{!pool.is_default ? <Button size="sm" variant="outline" onClick={onArchive}>归档</Button> : null}</div></div>;
}

function PoolEditor({ open, pool, onClose, onChanged, notify }: { open: boolean; pool?: ClaudeCodeAccountPool; onClose: () => void; onChanged: () => Promise<void>; notify: (message: string, danger?: boolean) => void }) {
  const [name, setName] = useState(pool?.name || "");
  const [description, setDescription] = useState(pool?.description || "");
  useEffect(() => { if (open) { setName(pool?.name || ""); setDescription(pool?.description || ""); } }, [open, pool]);
  const mutation = useMutation({ mutationFn: () => pool ? api.patchAccountPool(pool.id, { name, description }) : api.createAccountPool({ name, description }), onSuccess: async () => { await onChanged(); notify(pool ? "账号池已更新" : "账号池已创建"); onClose(); }, onError: (error) => notify(String(error), true) });
  const submit = (event: FormEvent) => { event.preventDefault(); mutation.mutate(); };
  return <Dialog open={open} onOpenChange={(next) => !next && onClose()}><DialogContent><DialogHeader><DialogTitle>{pool ? "编辑账号池" : "新建账号池"}</DialogTitle><DialogDescription>账号与 API Key 在池之间严格隔离。</DialogDescription></DialogHeader><form className="grid gap-4" onSubmit={submit}><div className="grid gap-2"><Label htmlFor="pool-name">名称</Label><Input id="pool-name" value={name} onChange={(event) => setName(event.target.value)} disabled={pool?.is_default} autoFocus /></div><div className="grid gap-2"><Label htmlFor="pool-description">备注</Label><Textarea id="pool-description" value={description} onChange={(event) => setDescription(event.target.value)} /></div><div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Button type="submit" disabled={!name.trim() || mutation.isPending}>{pool ? "保存" : "创建"}</Button></div></form></DialogContent></Dialog>;
}
