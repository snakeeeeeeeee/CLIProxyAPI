import { Copy, Edit3, Eye, KeyRound, Plus, RefreshCw, ShieldOff, ToggleLeft, ToggleRight } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api, type ClaudeCodeAccountPool, type ClaudeCodePoolAPIKey, type PoolAPIKeyCredential } from "../api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { formatPercent, formatTime, formatTokens, formatUSD } from "../format";

export function APIKeysPage({ keys, pools, poolID = "", onChanged, notify }: {
  keys: ClaudeCodePoolAPIKey[];
  pools: ClaudeCodeAccountPool[];
  poolID?: string;
  onChanged: () => Promise<void>;
  notify: (message: string, danger?: boolean) => void;
}) {
  const [createOpen, setCreateOpen] = useState(false);
  const [credential, setCredential] = useState<PoolAPIKeyCredential | null>(null);
  const [editing, setEditing] = useState<ClaudeCodePoolAPIKey | null>(null);
  const patch = useMutation({ mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.patchPoolAPIKey(id, { enabled }), onSuccess: async () => { await onChanged(); notify("API Key 状态已更新"); }, onError: (error) => notify(String(error), true) });
  const revoke = useMutation({ mutationFn: api.revokePoolAPIKey, onSuccess: async () => { await onChanged(); notify("API Key 已撤销"); }, onError: (error) => notify(String(error), true) });
  const rotate = useMutation({ mutationFn: api.rotatePoolAPIKey, onSuccess: async (result) => { setCredential(result); await onChanged(); }, onError: (error) => notify(String(error), true) });
  const reveal = useMutation({
    mutationFn: async (item: ClaudeCodePoolAPIKey): Promise<PoolAPIKeyCredential> => ({ item, ...(await api.poolAPIKeySecret(item.id)) }),
    onSuccess: setCredential,
    onError: (error) => notify(String(error), true)
  });
  const poolName = (id: string) => pools.find((pool) => pool.id === id)?.name || id;
  return <div className="grid min-w-0 gap-4">
    <div className="flex min-w-0 justify-end"><Button onClick={() => setCreateOpen(true)}><Plus className="h-4 w-4" />生成 API Key</Button></div>
    <div className="flex items-start gap-2 rounded-md border border-blue-200 bg-blue-50 px-3 py-2 text-xs leading-5 text-blue-900"><KeyRound className="mt-0.5 h-4 w-4 shrink-0" /><span><code>config.yaml</code> 中的旧式 API Key 继续可用，并固定访问 <code>default</code> 池。新 Key 只用于 <code>/claude-acc-pool/v1/*</code>。</span></div>
    <section className="min-w-0 rounded-md border bg-card">
      {keys.length === 0 ? <div className="px-4 py-12 text-center text-sm text-muted-foreground">暂无 API Key</div> : <>
        <div className="hidden overflow-x-auto lg:block"><table className="w-full min-w-[920px] text-sm"><thead><tr className="border-b bg-muted/70 text-left text-xs text-muted-foreground"><th className="px-4 py-2.5">名称 / Key</th><th className="px-3 py-2.5">账号池</th><th className="px-3 py-2.5">状态</th><th className="px-3 py-2.5">请求 / Attempts</th><th className="px-3 py-2.5">Tokens</th><th className="px-3 py-2.5">成本</th><th className="px-3 py-2.5">最后使用</th><th className="px-3 py-2.5 text-right">操作</th></tr></thead><tbody>{keys.map((key) => <tr key={key.id} className="border-b last:border-b-0"><td className="px-4 py-3"><div className="font-medium">{key.name}</div><div className="mt-0.5 font-mono text-xs text-muted-foreground">{key.key_prefix}</div></td><td className="px-3 py-3">{poolName(key.pool_id)}</td><td className="px-3 py-3"><KeyStatus item={key} /></td><td className="px-3 py-3 tabular-nums">{key.usage?.request_count || 0} / {key.usage?.attempt_count || 0}</td><td className="px-3 py-3 tabular-nums">{formatTokens(key.usage?.raw_total_tokens || 0)}</td><td className="px-3 py-3 font-medium tabular-nums">{formatUSD(key.usage?.estimated_cost || 0)}</td><td className="px-3 py-3 text-xs text-muted-foreground">{formatTime(key.last_used_at)}</td><td className="px-3 py-3"><div className="flex justify-end gap-1"><Button size="icon" variant="ghost" title={key.revoked_at ? "已撤销" : key.secret_available ? "查看并复制" : "轮换后可查看完整 Key"} disabled={Boolean(key.revoked_at) || !key.secret_available || reveal.isPending} onClick={() => reveal.mutate(key)}><Eye className="h-4 w-4" /></Button><Button size="icon" variant="ghost" title="编辑" disabled={Boolean(key.revoked_at)} onClick={() => setEditing(key)}><Edit3 className="h-4 w-4" /></Button><Button size="icon" variant="ghost" title={key.enabled ? "停用" : "启用"} disabled={Boolean(key.revoked_at)} onClick={() => patch.mutate({ id: key.id, enabled: !key.enabled })}>{key.enabled ? <ToggleRight className="h-4 w-4" /> : <ToggleLeft className="h-4 w-4" />}</Button><Button size="icon" variant="ghost" title="轮换" disabled={Boolean(key.revoked_at)} onClick={() => rotate.mutate(key.id)}><RefreshCw className="h-4 w-4" /></Button><Button size="icon" variant="ghost" title="撤销" disabled={Boolean(key.revoked_at)} onClick={() => { if (globalThis.confirm("撤销后该 Key 无法恢复。确认继续？")) revoke.mutate(key.id); }}><ShieldOff className="h-4 w-4" /></Button></div></td></tr>)}</tbody></table></div>
        <div className="divide-y lg:hidden">{keys.map((key) => <div key={key.id} className="grid gap-3 p-4"><div className="flex items-start justify-between gap-2"><div className="min-w-0"><div className="truncate font-medium">{key.name}</div><div className="mt-1 truncate font-mono text-xs text-muted-foreground">{key.key_prefix}</div><div className="mt-1 text-[11px] text-muted-foreground">{poolName(key.pool_id)} · 永久有效{!key.revoked_at && !key.secret_available ? " · 轮换后可查看" : ""}</div></div><KeyStatus item={key} /></div><div className="grid grid-cols-3 gap-2 text-xs"><Metric label="请求" value={String(key.usage?.request_count || 0)} /><Metric label="Tokens" value={formatTokens(key.usage?.raw_total_tokens || 0)} /><Metric label="成本" value={formatUSD(key.usage?.estimated_cost || 0)} /></div><div className="flex flex-wrap justify-end gap-1"><Button size="sm" variant="outline" onClick={() => reveal.mutate(key)} disabled={Boolean(key.revoked_at) || !key.secret_available || reveal.isPending}><Eye className="h-4 w-4" />查看</Button><Button size="sm" variant="outline" onClick={() => setEditing(key)} disabled={Boolean(key.revoked_at)}>编辑</Button><Button size="sm" variant="outline" onClick={() => rotate.mutate(key.id)} disabled={Boolean(key.revoked_at)}>轮换</Button><Button size="sm" variant="outline" onClick={() => patch.mutate({ id: key.id, enabled: !key.enabled })} disabled={Boolean(key.revoked_at)}>{key.enabled ? "停用" : "启用"}</Button><Button size="sm" variant="outline" onClick={() => { if (globalThis.confirm("撤销后该 Key 无法恢复。确认继续？")) revoke.mutate(key.id); }} disabled={Boolean(key.revoked_at)}>撤销</Button></div></div>)}</div>
      </>}
    </section>
    <CreateKeyDialog open={createOpen} pools={pools} defaultPoolID={poolID || "default"} onClose={() => setCreateOpen(false)} onCreated={async (result) => { setCreateOpen(false); setCredential(result); await onChanged(); }} notify={notify} />
    <EditKeyDialog item={editing} onClose={() => setEditing(null)} onChanged={onChanged} notify={notify} />
    <SecretDialog credential={credential} onClose={() => setCredential(null)} notify={notify} />
  </div>;
}

function KeyStatus({ item }: { item: ClaudeCodePoolAPIKey }) {
  if (item.revoked_at) return <Badge tone="danger">已撤销</Badge>;
  return <Badge tone={item.enabled ? "success" : "warning"}>{item.enabled ? "启用" : "停用"}</Badge>;
}

function CreateKeyDialog({ open, pools, defaultPoolID, onClose, onCreated, notify }: { open: boolean; pools: ClaudeCodeAccountPool[]; defaultPoolID: string; onClose: () => void; onCreated: (credential: PoolAPIKeyCredential) => Promise<void>; notify: (message: string, danger?: boolean) => void }) {
  const [poolID, setPoolID] = useState(defaultPoolID);
  const [name, setName] = useState("");
  useEffect(() => { if (open) { setPoolID(defaultPoolID); setName(""); } }, [open, defaultPoolID]);
  const mutation = useMutation({ mutationFn: () => api.createPoolAPIKey({ pool_id: poolID, name }), onSuccess: onCreated, onError: (error) => notify(String(error), true) });
  const submit = (event: FormEvent) => { event.preventDefault(); mutation.mutate(); };
  return <Dialog open={open} onOpenChange={(next) => !next && onClose()}><DialogContent><DialogHeader><DialogTitle>生成 API Key</DialogTitle><DialogDescription>Key 永久有效，创建后可随时在此页面查看和复制。</DialogDescription></DialogHeader><form className="grid gap-4" onSubmit={submit}><div className="grid gap-2"><Label>账号池</Label><Select value={poolID} onChange={(event) => setPoolID(event.target.value)}>{pools.filter((pool) => !pool.archived_at).map((pool) => <option key={pool.id} value={pool.id}>{pool.name}</option>)}</Select></div><div className="grid gap-2"><Label htmlFor="key-name">名称</Label><Input id="key-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="例如：客户 A" /></div><div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Button type="submit" disabled={!poolID || !name.trim() || mutation.isPending}>生成</Button></div></form></DialogContent></Dialog>;
}

function SecretDialog({ credential, onClose, notify }: { credential: PoolAPIKeyCredential | null; onClose: () => void; notify: (message: string, danger?: boolean) => void }) {
  const copy = async () => { if (!credential) return; try { await navigator.clipboard.writeText(credential.secret); notify("API Key 已复制"); } catch { notify("复制失败", true); } };
  return <Dialog open={Boolean(credential)} onOpenChange={(next) => !next && onClose()}><DialogContent><DialogHeader><DialogTitle>{credential?.item.name || "API Key"}</DialogTitle><DialogDescription>完整 Key 可随时在此页面查看和复制。</DialogDescription></DialogHeader>{credential ? <div className="grid gap-4"><div className="break-all rounded-md border bg-muted p-3 font-mono text-sm">{credential.secret}</div><div className="flex justify-end gap-2"><Button variant="outline" onClick={copy}><Copy className="h-4 w-4" />复制</Button><Button onClick={onClose}>完成</Button></div></div> : null}</DialogContent></Dialog>;
}

function EditKeyDialog({ item, onClose, onChanged, notify }: { item: ClaudeCodePoolAPIKey | null; onClose: () => void; onChanged: () => Promise<void>; notify: (message: string, danger?: boolean) => void }) {
  const [name, setName] = useState("");
  useEffect(() => {
    if (item) {
      setName(item.name);
    }
  }, [item]);
  const mutation = useMutation({
    mutationFn: () => api.patchPoolAPIKey(item?.id || "", { name: name.trim() }),
    onSuccess: async () => { await onChanged(); notify("API Key 已更新"); onClose(); },
    onError: (error) => notify(String(error), true)
  });
  return <Dialog open={Boolean(item)} onOpenChange={(next) => !next && onClose()}><DialogContent><DialogHeader><DialogTitle>编辑 API Key</DialogTitle><DialogDescription>Key 永久有效；账号池绑定不可修改。</DialogDescription></DialogHeader><form className="grid gap-4" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}><div className="grid gap-2"><Label htmlFor="edit-key-name">名称</Label><Input id="edit-key-name" value={name} onChange={(event) => setName(event.target.value)} /></div><div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Button type="submit" disabled={!name.trim() || mutation.isPending}>保存</Button></div></form></DialogContent></Dialog>;
}

function Metric({ label, value }: { label: string; value: string }) { return <div className="min-w-0"><div className="text-muted-foreground">{label}</div><div className="mt-1 truncate font-semibold tabular-nums">{value}</div></div>; }
