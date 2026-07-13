import { ChevronLeft, ChevronRight, Loader2 } from "lucide-react";
import type { RoutingEvent } from "../api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { formatTime } from "../format";

export function PoolEventsPage({ events, total, page, pageSize, loading, onPageChange }: {
  events: RoutingEvent[];
  total: number;
  page: number;
  pageSize: number;
  loading: boolean;
  onPageChange: (page: number) => void;
}) {
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  const start = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const end = total === 0 ? 0 : Math.min(total, start + events.length - 1);

  return (
    <section className="min-w-0 overflow-hidden rounded-md border bg-card">
      {events.length === 0 ? (
        <div className="flex min-h-48 items-center justify-center gap-2 p-12 text-center text-sm text-muted-foreground">
          {loading ? <><Loader2 className="h-4 w-4 animate-spin" />正在加载调度事件</> : "当前范围内暂无调度事件"}
        </div>
      ) : (
        <>
          <div className="hidden overflow-x-auto lg:block">
            <table className="w-full min-w-[980px] table-fixed text-sm">
              <thead>
                <tr className="border-b bg-muted/70 text-left text-xs text-muted-foreground">
                  <th className="w-40 px-4 py-2.5">发生时间</th>
                  <th className="w-28 px-3 py-2.5">决策</th>
                  <th className="w-52 px-3 py-2.5">账号</th>
                  <th className="w-52 px-3 py-2.5">模型</th>
                  <th className="w-28 px-3 py-2.5">亲和</th>
                  <th className="w-48 px-3 py-2.5">负载</th>
                  <th className="px-3 py-2.5">原因</th>
                </tr>
              </thead>
              <tbody className={loading ? "opacity-60" : undefined}>
                {events.map((event) => (
                  <tr key={event.id || `${event.request_id}-${event.created_at}`} className="h-12 border-b last:border-b-0">
                    <td className="whitespace-nowrap px-4 py-2.5 text-xs text-muted-foreground">{formatTime(event.created_at)}</td>
                    <td className="px-3 py-2.5"><Badge tone={event.decision === "success" ? "success" : event.status_code && event.status_code >= 400 ? "danger" : "neutral"}>{event.decision}</Badge></td>
                    <td className="truncate px-3 py-2.5 font-mono text-xs" title={event.auth_id || event.account_id}>{event.auth_id || event.account_id || "-"}</td>
                    <td className="truncate px-3 py-2.5 font-mono text-xs" title={event.requested_model || event.model}>{event.requested_model || event.model || "-"}</td>
                    <td className="truncate px-3 py-2.5 text-xs">{event.primary_hit ? "主账号" : event.backup_lane ? "备用 lane" : event.affinity_mode || "-"}</td>
                    <td className="whitespace-nowrap px-3 py-2.5 text-xs tabular-nums">并发 {event.in_flight || 0}/{event.concurrency_limit || 0} · RPM {event.rpm_used || 0}/{event.rpm_limit || 0}</td>
                    <td className="truncate px-3 py-2.5 text-xs text-muted-foreground" title={event.error || event.reason}>{event.error || event.reason || "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className={loading ? "divide-y opacity-60 lg:hidden" : "divide-y lg:hidden"}>
            {events.map((event) => (
              <div key={event.id || `${event.request_id}-${event.created_at}`} className="grid gap-2 p-4">
                <div className="flex items-center justify-between gap-2">
                  <Badge tone={event.decision === "success" ? "success" : event.status_code && event.status_code >= 400 ? "danger" : "neutral"}>{event.decision}</Badge>
                  <span className="text-xs text-muted-foreground">{formatTime(event.created_at)}</span>
                </div>
                <div className="truncate font-mono text-xs">{event.requested_model || event.model || "-"}</div>
                <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                  <span>并发 {event.in_flight || 0}/{event.concurrency_limit || 0}</span>
                  <span>RPM {event.rpm_used || 0}/{event.rpm_limit || 0}</span>
                  <span className="min-w-0 flex-1 truncate">{event.reason || event.error || "-"}</span>
                </div>
              </div>
            ))}
          </div>
        </>
      )}
      {total > 0 ? (
        <div className="flex min-h-12 flex-wrap items-center justify-between gap-3 border-t bg-muted/20 px-4 py-2 text-xs text-muted-foreground">
          <span className="tabular-nums">第 {start}-{end} 条 / 共 {total} 条</span>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="icon" className="h-8 w-8" onClick={() => onPageChange(Math.max(1, page - 1))} disabled={page <= 1 || loading} title="上一页" aria-label="上一页"><ChevronLeft className="h-4 w-4" /></Button>
            <span className="min-w-16 text-center tabular-nums">{page} / {pageCount}</span>
            <Button variant="outline" size="icon" className="h-8 w-8" onClick={() => onPageChange(Math.min(pageCount, page + 1))} disabled={page >= pageCount || loading} title="下一页" aria-label="下一页"><ChevronRight className="h-4 w-4" /></Button>
          </div>
        </div>
      ) : null}
    </section>
  );
}
