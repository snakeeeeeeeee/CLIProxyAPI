import { Edit3, Plus, RefreshCw, Trash2 } from "lucide-react";
import { FormEvent, useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { api, type AccountRow, type ClaudeCodeModel, type ModelPrice, type ModelPriceUpdate, type ModelPriceVersion } from "../api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "../components/ui/dialog";
import { Input } from "../components/ui/input";
import { Label } from "../components/ui/label";
import { Select } from "../components/ui/select";
import { Textarea } from "../components/ui/textarea";
import { cn } from "../lib/utils";

export function ModelPricingPage({ accounts, models, pricing, onChanged, notify }: {
  accounts: AccountRow[];
  models: ClaudeCodeModel[];
  pricing?: ModelPriceVersion;
  onChanged: () => Promise<void>;
  notify: (message: string, danger?: boolean) => void;
}) {
  const [tab, setTab] = useState<"mapping" | "pricing">("pricing");
  const [fetchDialogOpen, setFetchDialogOpen] = useState(false);
  const [selectedAccountID, setSelectedAccountID] = useState("");
  const [modelEditor, setModelEditor] = useState<ClaudeCodeModel | null | undefined>(undefined);
  const [priceEditor, setPriceEditor] = useState<ModelPrice | null | undefined>(undefined);
  const runnableAccounts = accounts.map((row) => row.account).filter((account) => account.effective_schedulable && account.has_auth_data);
  const fetchModels = useMutation({
    mutationFn: api.fetchPoolModels,
    onSuccess: async (data) => {
      await onChanged();
      setFetchDialogOpen(false);
      notify(`已从账号获取 ${data.items.length} 个模型`);
    },
    onError: (error) => notify(String(error), true)
  });
  const deleteModel = useMutation({ mutationFn: api.deletePoolModel, onSuccess: async () => { await onChanged(); notify("模型映射已删除"); }, onError: (error) => notify(String(error), true) });
  const openFetchDialog = () => {
    if (runnableAccounts.length === 0) {
      notify("暂无可用于获取模型的账号", true);
      return;
    }
    if (!runnableAccounts.some((account) => account.id === selectedAccountID)) {
      setSelectedAccountID(runnableAccounts[0].id);
    }
    setFetchDialogOpen(true);
  };
  return <div className="grid min-w-0 gap-4">
    <div className="flex min-w-0 flex-wrap items-center justify-between gap-2"><div className="inline-flex h-9 rounded-md border bg-muted p-0.5"><button type="button" className={tabClass(tab === "mapping")} onClick={() => setTab("mapping")}>模型映射</button><button type="button" className={tabClass(tab === "pricing")} onClick={() => setTab("pricing")}>标准价格</button></div><div className="flex flex-wrap gap-2">{tab === "mapping" ? <><Button variant="outline" onClick={openFetchDialog} disabled={fetchModels.isPending}><RefreshCw className={cn("h-4 w-4", fetchModels.isPending && "animate-spin")} />从账号获取</Button><Button onClick={() => setModelEditor(null)}><Plus className="h-4 w-4" />新增映射</Button></> : <Button onClick={() => setPriceEditor(null)}><Plus className="h-4 w-4" />新增价格</Button>}</div></div>
    {tab === "mapping" ? <ModelMappings models={models} onEdit={setModelEditor} onDelete={(id) => { if (globalThis.confirm("确认删除这个模型映射？")) deleteModel.mutate(id); }} /> : <PriceTable version={pricing} onEdit={setPriceEditor} onRemove={(price) => { if (globalThis.confirm(`从下一价格版本移除 ${price.model_pattern}？`)) void savePriceUpdate({ model_pattern: price.model_pattern, input_per_million: 0, output_per_million: 0, cache_write_5m_per_million: 0, cache_write_1h_per_million: 0, cache_read_per_million: 0, remove: true }, onChanged, notify); }} />}
    <Dialog open={fetchDialogOpen} onOpenChange={setFetchDialogOpen}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>选择账号</DialogTitle>
          <DialogDescription>使用所选账号及其绑定代理获取 Anthropic 模型列表。</DialogDescription>
        </DialogHeader>
        <div className="grid gap-2">
          <Label htmlFor="model-fetch-account">账号</Label>
          <Select id="model-fetch-account" value={selectedAccountID} onChange={(event) => setSelectedAccountID(event.target.value)}>
            {runnableAccounts.map((account) => <option key={account.id} value={account.id}>{account.email || account.auth_id.slice(0, 12)} · {account.pool_id}</option>)}
          </Select>
        </div>
        <div className="flex justify-end gap-2">
          <Button type="button" variant="outline" onClick={() => setFetchDialogOpen(false)}>取消</Button>
          <Button type="button" onClick={() => fetchModels.mutate(selectedAccountID)} disabled={!selectedAccountID || fetchModels.isPending}>
            <RefreshCw className={cn("h-4 w-4", fetchModels.isPending && "animate-spin")} />
            获取模型
          </Button>
        </div>
      </DialogContent>
    </Dialog>
    <ModelEditor open={modelEditor !== undefined} model={modelEditor || undefined} onClose={() => setModelEditor(undefined)} onChanged={onChanged} notify={notify} />
    <PriceEditor open={priceEditor !== undefined} price={priceEditor || undefined} onClose={() => setPriceEditor(undefined)} onChanged={onChanged} notify={notify} />
  </div>;
}

function ModelMappings({ models, onEdit, onDelete }: { models: ClaudeCodeModel[]; onEdit: (model: ClaudeCodeModel) => void; onDelete: (id: string) => void }) {
  return <section className="min-w-0 rounded-md border bg-card">{models.length === 0 ? <div className="p-12 text-center text-sm text-muted-foreground">暂无模型映射</div> : <><div className="hidden overflow-x-auto lg:block"><table className="w-full min-w-[760px] text-sm"><thead><tr className="border-b bg-muted/70 text-left text-xs text-muted-foreground"><th className="px-4 py-2.5">外部模型</th><th className="px-3 py-2.5">上游模型</th><th className="px-3 py-2.5">状态</th><th className="px-3 py-2.5">来源</th><th className="px-3 py-2.5 text-right">操作</th></tr></thead><tbody>{models.map((model) => <tr key={model.id} className="border-b last:border-b-0"><td className="px-4 py-3 font-mono text-xs">{model.alias}</td><td className="px-3 py-3 font-mono text-xs">{model.name}</td><td className="px-3 py-3"><Badge tone={model.enabled ? "success" : "warning"}>{model.enabled ? "启用" : "停用"}</Badge></td><td className="px-3 py-3 text-muted-foreground">{model.source}</td><td className="px-3 py-3"><div className="flex justify-end gap-1"><Button size="icon" variant="ghost" title="编辑" onClick={() => onEdit(model)}><Edit3 className="h-4 w-4" /></Button><Button size="icon" variant="ghost" title="删除" onClick={() => onDelete(model.id)}><Trash2 className="h-4 w-4" /></Button></div></td></tr>)}</tbody></table></div><div className="divide-y lg:hidden">{models.map((model) => <div key={model.id} className="flex min-w-0 items-center justify-between gap-3 p-4"><button type="button" className="grid min-w-0 gap-1 text-left" onClick={() => onEdit(model)}><span className="truncate font-mono text-xs">{model.alias}</span><span className="truncate text-xs text-muted-foreground">→ {model.name}</span></button><div className="flex shrink-0 items-center gap-1"><Badge tone={model.enabled ? "success" : "warning"}>{model.enabled ? "启用" : "停用"}</Badge><Button size="icon" variant="ghost" title="编辑" onClick={() => onEdit(model)}><Edit3 className="h-4 w-4" /></Button><Button size="icon" variant="ghost" title="删除" onClick={() => onDelete(model.id)}><Trash2 className="h-4 w-4" /></Button></div></div>)}</div></>}</section>;
}

function PriceTable({ version, onEdit, onRemove }: { version?: ModelPriceVersion; onEdit: (price: ModelPrice) => void; onRemove: (price: ModelPrice) => void }) {
  return <section className="min-w-0 rounded-md border bg-card"><div className="flex items-center justify-between border-b px-4 py-3"><div><h2 className="text-sm font-semibold">USD / 百万 Tokens</h2><p className="mt-0.5 text-xs text-muted-foreground">Revision {version?.revision || 0} · {version?.source || "-"}</p></div><Badge tone="neutral">{version?.prices.length || 0} 条</Badge></div>{!version?.prices.length ? <div className="p-12 text-center text-sm text-muted-foreground">暂无价格</div> : <><div className="hidden overflow-x-auto lg:block"><table className="w-full min-w-[900px] text-sm"><thead><tr className="border-b bg-muted/70 text-left text-xs text-muted-foreground"><th className="px-4 py-2.5">模型或前缀</th><th className="px-3 py-2.5">输入</th><th className="px-3 py-2.5">输出</th><th className="px-3 py-2.5">缓存写入 5m</th><th className="px-3 py-2.5">缓存读取</th><th className="px-3 py-2.5">缓存写入 1h</th><th className="px-3 py-2.5 text-right">操作</th></tr></thead><tbody>{version.prices.map((price) => <tr key={price.model_pattern} className="border-b last:border-b-0"><td className="px-4 py-3 font-mono text-xs">{price.model_pattern}</td><PriceCell value={price.input_per_million} /><PriceCell value={price.output_per_million} /><PriceCell value={price.cache_write_5m_per_million} /><PriceCell value={price.cache_read_per_million} /><PriceCell value={price.cache_write_1h_per_million} /><td className="px-3 py-3"><div className="flex justify-end gap-1"><Button size="icon" variant="ghost" title="编辑" onClick={() => onEdit(price)}><Edit3 className="h-4 w-4" /></Button><Button size="icon" variant="ghost" title="移除" onClick={() => onRemove(price)}><Trash2 className="h-4 w-4" /></Button></div></td></tr>)}</tbody></table></div><div className="divide-y lg:hidden">{version.prices.map((price) => <div key={price.model_pattern} className="grid gap-3 p-4"><div className="flex min-w-0 items-start justify-between gap-3"><span className="min-w-0 truncate font-mono text-xs" title={price.model_pattern}>{price.model_pattern}</span><div className="flex shrink-0 gap-1"><Button size="icon" variant="ghost" title="编辑" onClick={() => onEdit(price)}><Edit3 className="h-4 w-4" /></Button><Button size="icon" variant="ghost" title="移除" onClick={() => onRemove(price)}><Trash2 className="h-4 w-4" /></Button></div></div><div className="grid grid-cols-3 gap-x-3 gap-y-2 text-xs max-[430px]:grid-cols-2"><PriceMetric label="输入" value={price.input_per_million} /><PriceMetric label="输出" value={price.output_per_million} /><PriceMetric label="写入 5m" value={price.cache_write_5m_per_million} /><PriceMetric label="缓存读取" value={price.cache_read_per_million} /><PriceMetric label="写入 1h" value={price.cache_write_1h_per_million} /></div></div>)}</div></>}</section>;
}

function PriceCell({ value }: { value: number }) { return <td className="px-3 py-3 font-medium tabular-nums">${value.toFixed(value < 1 ? 2 : 2)}</td>; }
function PriceMetric({ label, value }: { label: string; value: number }) { return <div className="min-w-0"><div className="text-[11px] text-muted-foreground">{label}</div><div className="mt-0.5 font-semibold tabular-nums">${value.toFixed(2)}</div></div>; }

function ModelEditor({ open, model, onClose, onChanged, notify }: { open: boolean; model?: ClaudeCodeModel; onClose: () => void; onChanged: () => Promise<void>; notify: (message: string, danger?: boolean) => void }) {
  const [alias, setAlias] = useState(model?.alias || ""); const [name, setName] = useState(model?.name || ""); const [enabled, setEnabled] = useState(model?.enabled ?? true); const [note, setNote] = useState(model?.note || "");
  useEffect(() => { if (open) { setAlias(model?.alias || ""); setName(model?.name || ""); setEnabled(model?.enabled ?? true); setNote(model?.note || ""); } }, [open, model]);
  const mutation = useMutation({ mutationFn: () => model ? api.patchPoolModel(model.id, { alias, name, enabled, note }) : api.createPoolModel({ alias, name, enabled, note, source: "manual" }), onSuccess: async () => { await onChanged(); notify(model ? "模型映射已更新" : "模型映射已创建"); onClose(); }, onError: (error) => notify(String(error), true) });
  return <Dialog open={open} onOpenChange={(next) => !next && onClose()}><DialogContent><DialogHeader><DialogTitle>{model ? "编辑模型映射" : "新增模型映射"}</DialogTitle></DialogHeader><form className="grid gap-4" onSubmit={(event) => { event.preventDefault(); mutation.mutate(); }}><div className="grid gap-2"><Label>外部模型</Label><Input value={alias} onChange={(event) => setAlias(event.target.value)} /></div><div className="grid gap-2"><Label>上游模型</Label><Input value={name} onChange={(event) => setName(event.target.value)} /></div><div className="grid gap-2"><Label>状态</Label><Select value={enabled ? "enabled" : "disabled"} onChange={(event) => setEnabled(event.target.value === "enabled")}><option value="enabled">启用</option><option value="disabled">停用</option></Select></div><div className="grid gap-2"><Label>备注</Label><Textarea value={note} onChange={(event) => setNote(event.target.value)} /></div><div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Button type="submit" disabled={!alias.trim() || !name.trim() || mutation.isPending}>保存</Button></div></form></DialogContent></Dialog>;
}

function PriceEditor({ open, price, onClose, onChanged, notify }: { open: boolean; price?: ModelPrice; onClose: () => void; onChanged: () => Promise<void>; notify: (message: string, danger?: boolean) => void }) {
  const [form, setForm] = useState<ModelPriceUpdate>(() => priceToUpdate(price));
  useEffect(() => { if (open) setForm(priceToUpdate(price)); }, [open, price]);
  const mutation = useMutation({ mutationFn: () => api.saveModelPrices([form], `Update ${form.model_pattern}`), onSuccess: async () => { await onChanged(); notify("新价格版本已创建"); onClose(); }, onError: (error) => notify(String(error), true) });
  const setNumber = (key: keyof ModelPriceUpdate, value: string) => setForm((current) => ({ ...current, [key]: Number(value) || 0 }));
  return <Dialog open={open} onOpenChange={(next) => !next && onClose()}><DialogContent><DialogHeader><DialogTitle>{price ? "编辑价格" : "新增价格"}</DialogTitle><DialogDescription>保存会创建不可变的新 Revision，不回写历史账单。</DialogDescription></DialogHeader><form className="grid gap-4" onSubmit={(event: FormEvent) => { event.preventDefault(); mutation.mutate(); }}><div className="grid gap-2"><Label>模型或前缀</Label><Input value={form.model_pattern} onChange={(event) => setForm((current) => ({ ...current, model_pattern: event.target.value }))} placeholder="claude-fable-5*" /></div><div className="grid grid-cols-2 gap-3"><PriceInput label="输入" value={form.input_per_million} onChange={(value) => setNumber("input_per_million", value)} /><PriceInput label="输出" value={form.output_per_million} onChange={(value) => setNumber("output_per_million", value)} /><PriceInput label="缓存写入 5m" value={form.cache_write_5m_per_million} onChange={(value) => setNumber("cache_write_5m_per_million", value)} /><PriceInput label="缓存读取" value={form.cache_read_per_million} onChange={(value) => setNumber("cache_read_per_million", value)} /><PriceInput label="缓存写入 1h" value={form.cache_write_1h_per_million} onChange={(value) => setNumber("cache_write_1h_per_million", value)} /></div><div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Button type="submit" disabled={!form.model_pattern.trim() || mutation.isPending}>创建 Revision</Button></div></form></DialogContent></Dialog>;
}

function PriceInput({ label, value, onChange }: { label: string; value: number; onChange: (value: string) => void }) { return <div className="grid gap-2"><Label>{label}</Label><Input type="number" min="0" step="0.01" value={value} onChange={(event) => onChange(event.target.value)} /></div>; }
function priceToUpdate(price?: ModelPrice): ModelPriceUpdate { return { model_pattern: price?.model_pattern || "", input_per_million: price?.input_per_million || 0, output_per_million: price?.output_per_million || 0, cache_write_5m_per_million: price?.cache_write_5m_per_million || 0, cache_write_1h_per_million: price?.cache_write_1h_per_million || 0, cache_read_per_million: price?.cache_read_per_million || 0 }; }
function tabClass(active: boolean) { return cn("h-8 rounded px-3 text-xs text-muted-foreground", active && "bg-card font-medium text-foreground shadow-sm"); }
async function savePriceUpdate(update: ModelPriceUpdate, onChanged: () => Promise<void>, notify: (message: string, danger?: boolean) => void) { try { await api.saveModelPrices([update], `Remove ${update.model_pattern}`); await onChanged(); notify("新价格版本已创建"); } catch (error) { notify(String(error), true); } }
