import { Cable, ExternalLink, Gauge, KeyRound, LogOut, Menu, Network, PackageOpen, RefreshCw, Settings2, Tags, X } from "lucide-react";
import type { ReactNode } from "react";
import { useState } from "react";
import { Button } from "./ui/button";
import { cn } from "../lib/utils";

export type ConsoleSection = "overview" | "pools" | "api-keys" | "proxies" | "models" | "settings";

const navItems: Array<{ id: ConsoleSection; label: string; icon: typeof Gauge }> = [
  { id: "overview", label: "总览", icon: Gauge },
  { id: "pools", label: "账号池", icon: PackageOpen },
  { id: "api-keys", label: "API Keys", icon: KeyRound },
  { id: "proxies", label: "代理 IP", icon: Cable },
  { id: "models", label: "模型与价格", icon: Tags },
  { id: "settings", label: "系统设置", icon: Settings2 }
];

export function AppShell({
  active,
  title,
  subtitle,
  loading,
  actions,
  children,
  onNavigate,
  onRefresh,
  onLogout
}: {
  active: ConsoleSection;
  title: string;
  subtitle?: string;
  loading: boolean;
  actions?: ReactNode;
  children: ReactNode;
  onNavigate: (section: ConsoleSection) => void;
  onRefresh: () => void;
  onLogout: () => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const navigate = (section: ConsoleSection) => {
    setMenuOpen(false);
    onNavigate(section);
  };
  const navigation = (
    <nav className="grid gap-1" aria-label="主导航">
      {navItems.map(({ id, label, icon: Icon }) => (
        <button
          key={id}
          type="button"
          onClick={() => navigate(id)}
          className={cn(
            "flex h-10 items-center gap-2 rounded-md px-3 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
            active === id ? "bg-primary/10 font-medium text-primary" : "text-muted-foreground hover:bg-muted hover:text-foreground"
          )}
        >
          <Icon className="h-4 w-4 shrink-0" />
          <span>{label}</span>
        </button>
      ))}
    </nav>
  );
  return (
    <div className="min-h-dvh bg-background lg:grid lg:grid-cols-[216px_minmax(0,1fr)]">
      <aside className="hidden min-h-dvh border-r bg-card px-3 py-4 lg:flex lg:flex-col">
        <div className="flex items-center gap-2 px-2 pb-5">
          <div className="flex h-8 w-8 items-center justify-center rounded-md bg-primary text-primary-foreground"><Network className="h-4 w-4" /></div>
          <div className="min-w-0"><div className="truncate text-sm font-semibold">Claude Account Pool</div><div className="text-[11px] text-muted-foreground">Resource Console</div></div>
        </div>
        {navigation}
        <div className="mt-auto grid gap-1 border-t pt-3">
          <a href="/management.html#/" className="sidebar-link"><ExternalLink className="h-4 w-4" />管理中心</a>
          <button type="button" className="sidebar-link" onClick={onLogout}><LogOut className="h-4 w-4" />退出登录</button>
        </div>
      </aside>

      <div className="min-w-0">
        <div className="sticky top-0 z-40 flex h-14 items-center justify-between border-b bg-card px-3 lg:hidden">
          <button type="button" className="flex h-9 w-9 items-center justify-center rounded-md border" onClick={() => setMenuOpen(true)} title="打开菜单"><Menu className="h-4 w-4" /></button>
          <div className="flex items-center gap-2 text-sm font-semibold"><Network className="h-4 w-4 text-primary" />资源池控制台</div>
          <Button variant="ghost" size="icon" onClick={onRefresh} title="刷新数据"><RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} /></Button>
        </div>
        {menuOpen ? (
          <div className="fixed inset-0 z-50 lg:hidden" role="dialog" aria-modal="true" aria-label="主菜单">
            <button type="button" className="absolute inset-0 bg-black/35" onClick={() => setMenuOpen(false)} aria-label="关闭菜单" />
            <aside className="absolute inset-y-0 left-0 w-[min(84vw,300px)] border-r bg-card p-3 shadow-panel">
              <div className="mb-3 flex h-11 items-center justify-between border-b px-2"><span className="text-sm font-semibold">资源池控制台</span><Button variant="ghost" size="icon" onClick={() => setMenuOpen(false)}><X className="h-4 w-4" /></Button></div>
              {navigation}
              <div className="mt-4 grid gap-1 border-t pt-3"><a href="/management.html#/" className="sidebar-link"><ExternalLink className="h-4 w-4" />管理中心</a><button type="button" className="sidebar-link" onClick={onLogout}><LogOut className="h-4 w-4" />退出登录</button></div>
            </aside>
          </div>
        ) : null}

        <main className="min-w-0 overflow-x-hidden px-3 py-4 sm:px-5 lg:px-7 lg:py-5">
          <header className="flex flex-wrap items-center justify-between gap-3 border-b pb-4">
            <div className="min-w-0">
              <h1 className="text-xl font-semibold leading-tight">{title}</h1>
              {subtitle ? <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{subtitle}</p> : null}
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="outline" size="icon" onClick={onRefresh} disabled={loading} title="刷新数据"><RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} /></Button>
              {actions}
            </div>
          </header>
          <div className="mt-4 min-w-0">{children}</div>
        </main>
      </div>
    </div>
  );
}
