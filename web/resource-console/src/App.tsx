import {
  ColumnDef,
  Table,
  flexRender,
  getCoreRowModel,
  useReactTable
} from "@tanstack/react-table";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  ArrowRight,
  Cable,
  Check,
  CheckCircle2,
  Clock3,
  Database,
  Download,
  Eraser,
  GitCompareArrows,
  CopyPlus,
  Copy,
  ExternalLink,
  FileCode2,
  FileText,
  Home,
  KeyRound,
  Link2Off,
  Loader2,
  LogIn,
  LogOut,
  Network,
  Play,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  ShieldAlert,
  SlidersHorizontal,
  Trash2,
  Unlink,
  UserRoundCog,
  UsersRound
} from "lucide-react";
import { FormEvent, ReactNode, RefObject, UIEvent, useEffect, useMemo, useRef, useState } from "react";
import {
  AccountRow,
  AccountAvailabilitySummary,
  AccountBatchAction,
  AccountCapacity,
  AccountModelStatus,
  AccountQuota,
  ClaudeCodeAccount,
  ClaudeCodeProfileResponse,
  ClaudeCodeProfileSnapshot,
  CloakEffectiveConfig,
  ClaudeCodeModel,
  ClaudeCodeModelPayload,
  ClaudeCodePoolConfigResponse,
  ClaudeCodePoolEffectiveConfig,
  ClaudeCodePoolRawConfig,
  ClaudeCodePoolStats,
  AccountPoolLogEffectiveConfig,
  AccountPoolLogLine,
  AccountPoolLogRawConfig,
  ProxyBatchAction,
  ProxyPayload,
  ProxyResource,
  RoutingEffectiveConfig,
  RoutingEvent,
  UsageSummary,
  UsageCalibrationResponse,
  VirtualCacheEffectiveConfig,
  api,
  getManagementKey,
  isManagementAuthError,
  managementEventsURL,
  setManagementKey
} from "./api";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle
} from "./components/ui/dialog";
import { Input } from "./components/ui/input";
import { Label } from "./components/ui/label";
import { Progress } from "./components/ui/progress";
import { Select } from "./components/ui/select";
import { Textarea } from "./components/ui/textarea";
import { formatTime, healthText, proxyDisplay, successRate } from "./format";
import { cn } from "./lib/utils";

type View = "home" | "accounts" | "proxies";
type Route = View | "login";
type ModalState =
  | { type: "oauth" }
  | { type: "proxy"; proxy?: ProxyResource }
  | { type: "import" }
  | { type: "bind"; account: ClaudeCodeAccount }
  | { type: "test-account"; account: ClaudeCodeAccount; models: ClaudeCodeModel[] }
  | null;

interface ToastState {
  message: string;
  tone: "default" | "danger";
}

function routeFromHash(): Route {
  const normalized = window.location.hash.replace(/^#\/?/, "").split("?")[0].split("&")[0];
  if (normalized === "accounts" || normalized === "proxies" || normalized === "login") {
    return normalized;
  }
  return "home";
}

function routeHash(route: Route) {
  return route === "home" ? "#/" : `#/${route}`;
}

function emitHashChange() {
  try {
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  } catch {
    window.dispatchEvent(new Event("hashchange"));
  }
}

function replaceHashRoute(route: Route) {
  const hash = routeHash(route);
  window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}${hash}`);
  emitHashChange();
}

function pushHashRoute(route: Route) {
  const hash = routeHash(route);
  if (window.location.hash === hash) {
    emitHashChange();
    return;
  }
  window.location.hash = hash;
}

function App() {
  const initialManagementKey = getManagementKey();
  const [route, setRoute] = useState<Route>(() => (initialManagementKey.trim() ? routeFromHash() : "login"));
  const [afterLoginRoute, setAfterLoginRoute] = useState<View>(() => {
    const initialRoute = routeFromHash();
    return initialRoute === "login" ? "home" : initialRoute;
  });
  const [authRequired, setAuthRequired] = useState(false);
  const [managementKey, setManagementKeyState] = useState(initialManagementKey);
  const [modal, setModal] = useState<ModalState>(null);
  const [toast, setToast] = useState<ToastState | null>(null);
  const queryClient = useQueryClient();
  const hasManagementKey = managementKey.trim().length > 0;
  const dataEnabled = hasManagementKey && !authRequired && route !== "login";
  const view: View = route === "login" ? afterLoginRoute : route;
  const accountDataEnabled = dataEnabled && view === "accounts";

  const showToast = (message: string, tone: ToastState["tone"] = "default") => {
    setToast({ message, tone });
    window.clearTimeout((window as unknown as { __resourceToast?: number }).__resourceToast);
    (window as unknown as { __resourceToast?: number }).__resourceToast = window.setTimeout(() => setToast(null), 4200);
  };

  useEffect(() => {
    const syncRoute = () => setRoute(routeFromHash());
    syncRoute();
    window.addEventListener("hashchange", syncRoute);
    return () => window.removeEventListener("hashchange", syncRoute);
  }, []);

  useEffect(() => {
    if (!hasManagementKey) {
      if (route !== "login") {
        setAfterLoginRoute(view);
      }
      replaceHashRoute("login");
    }
  }, [hasManagementKey, route, view]);

  const configQuery = useQuery({ queryKey: ["resource-config"], queryFn: api.config, enabled: dataEnabled });
  const accountsQuery = useQuery({ queryKey: ["accounts"], queryFn: api.accounts, enabled: dataEnabled });
  const proxiesQuery = useQuery({ queryKey: ["proxies"], queryFn: api.proxies, enabled: dataEnabled });
  const availableQuery = useQuery({ queryKey: ["available-proxies"], queryFn: api.availableProxies, enabled: dataEnabled });
  const poolConfigQuery = useQuery({ queryKey: ["account-pool-config"], queryFn: api.poolConfig, enabled: accountDataEnabled });
  const poolProfileQuery = useQuery({ queryKey: ["account-pool-profile"], queryFn: api.poolProfile, enabled: accountDataEnabled });
  const profileSnapshotsQuery = useQuery({
    queryKey: ["account-pool-profile-snapshots"],
    queryFn: api.profileSnapshots,
    enabled: accountDataEnabled
  });
  const poolStatsQuery = useQuery({ queryKey: ["account-pool-stats"], queryFn: api.poolStats, enabled: accountDataEnabled, refetchInterval: 30_000 });
  const poolModelsQuery = useQuery({ queryKey: ["account-pool-models"], queryFn: api.poolModels, enabled: accountDataEnabled });
  const usageSummaryQuery = useQuery({ queryKey: ["account-pool-usage"], queryFn: api.usageSummary, enabled: accountDataEnabled, refetchInterval: 30_000 });
  const routingEventsQuery = useQuery({ queryKey: ["account-pool-routing-events"], queryFn: api.routingEvents, enabled: accountDataEnabled, refetchInterval: 30_000 });
  const logConfigQuery = useQuery({ queryKey: ["account-pool-log-config"], queryFn: api.poolLogConfig, enabled: accountDataEnabled });
  const poolLogsQuery = useQuery({ queryKey: ["account-pool-logs"], queryFn: api.poolLogs, enabled: accountDataEnabled, refetchInterval: 30_000 });
  const usageCalibrationsQuery = useQuery({
    queryKey: ["account-pool-usage-calibrations"],
    queryFn: api.usageCalibrations,
    enabled: accountDataEnabled
  });
  const authError = [
    configQuery.error,
    accountsQuery.error,
    proxiesQuery.error,
    availableQuery.error,
    poolConfigQuery.error,
    poolProfileQuery.error,
    profileSnapshotsQuery.error,
    poolStatsQuery.error,
    poolModelsQuery.error,
    usageSummaryQuery.error,
    routingEventsQuery.error,
    logConfigQuery.error,
    poolLogsQuery.error,
    usageCalibrationsQuery.error
  ].some(isManagementAuthError);

  useEffect(() => {
    if (!authError) {
      return;
    }
    setAuthRequired(true);
    setManagementKey("");
    setManagementKeyState("");
    if (route !== "login") {
      setAfterLoginRoute(view);
      replaceHashRoute("login");
    }
  }, [authError, route, view]);

  const invalidateAll = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["resource-config"] }),
      queryClient.invalidateQueries({ queryKey: ["accounts"] }),
      queryClient.invalidateQueries({ queryKey: ["proxies"] }),
      queryClient.invalidateQueries({ queryKey: ["available-proxies"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-config"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-profile"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-profile-snapshots"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-stats"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-models"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-usage"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-routing-events"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-log-config"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-logs"] })
    ]);
  };

  useEffect(() => {
    if (!dataEnabled) {
      return;
    }
    const source = new EventSource(managementEventsURL());
    const invalidate = (keys: string[]) => {
      keys.forEach((key) => {
        void queryClient.invalidateQueries({ queryKey: [key] });
      });
    };
    const onProxyChanged = () => invalidate(["resource-config", "proxies", "available-proxies", "accounts"]);
    const onAccountChanged = () =>
      invalidate(["resource-config", "accounts", "proxies", "available-proxies", "account-pool-stats", "account-pool-usage", "account-pool-routing-events", "account-pool-logs"]);
    const onConfigChanged = () =>
      invalidate(["resource-config", "account-pool-config", "account-pool-profile", "account-pool-profile-snapshots", "account-pool-stats", "account-pool-log-config"]);
    const onStatsChanged = () => invalidate(["account-pool-stats", "account-pool-usage", "account-pool-routing-events", "account-pool-logs", "accounts"]);
    const onModelChanged = () => invalidate(["account-pool-models"]);

    source.addEventListener("proxy_changed", onProxyChanged);
    source.addEventListener("account_changed", onAccountChanged);
    source.addEventListener("config_changed", onConfigChanged);
    source.addEventListener("stats_changed", onStatsChanged);
    source.addEventListener("model_changed", onModelChanged);

    source.onerror = () => {
      // EventSource reconnects automatically; polling remains as a fallback.
    };

    return () => {
      source.removeEventListener("proxy_changed", onProxyChanged);
      source.removeEventListener("account_changed", onAccountChanged);
      source.removeEventListener("config_changed", onConfigChanged);
      source.removeEventListener("stats_changed", onStatsChanged);
      source.removeEventListener("model_changed", onModelChanged);
      source.close();
    };
  }, [dataEnabled, queryClient]);

  const accountPoolLoading =
    accountDataEnabled &&
    (poolConfigQuery.isLoading || poolProfileQuery.isLoading || profileSnapshotsQuery.isLoading || poolStatsQuery.isLoading || poolModelsQuery.isLoading);
  const loading = configQuery.isLoading || accountsQuery.isLoading || proxiesQuery.isLoading || availableQuery.isLoading;
  const accounts = accountsQuery.data?.items || [];
  const proxies = proxiesQuery.data?.items || [];
  const available = availableQuery.data?.items || [];
  const summary = configQuery.data?.summary;
  const poolConfig = poolConfigQuery.data;
  const poolProfile = poolProfileQuery.data;
  const profileSnapshots = profileSnapshotsQuery.data?.items || [];
  const poolStats = poolStatsQuery.data?.stats;
  const poolModels = poolModelsQuery.data?.items || [];
  const usageSummary = usageSummaryQuery.data?.summary;
  const routingEvents = routingEventsQuery.data?.items || [];
  const logConfig = logConfigQuery.data;
  const poolLogs = poolLogsQuery.data?.items || [];
  const usageCalibrations = usageCalibrationsQuery.data;

  const pageTitle = view === "home" ? "资源池控制台" : view === "accounts" ? "Claude Code 账号池" : "代理 IP 池";
  const pageSubtitle =
    view === "home"
      ? "从独立入口管理 Claude Code 账号池和静态出口代理，登录态与管理中心分开保存。"
      : view === "accounts"
      ? "OAuth 登录账号、固定出口绑定、健康度和保守调度集中管理。"
      : "维护静态出口代理，后端定时检测健康度，账号绑定时只选择空闲可用代理。";

  const navigate = (nextRoute: Route) => {
    pushHashRoute(nextRoute);
  };

  const logout = async () => {
    setAfterLoginRoute(view);
    setAuthRequired(false);
    setModal(null);
    setManagementKey("");
    setManagementKeyState("");
    await queryClient.cancelQueries();
    queryClient.clear();
    replaceHashRoute("login");
    showToast("已退出资源池控制台");
  };

  const refresh = async () => {
    try {
      await invalidateAll();
      showToast("数据已刷新");
    } catch (error) {
      showToast(errorMessage(error), "danger");
    }
  };

  const submitLogin = async (value: string) => {
    const trimmed = value.trim();
    if (!trimmed) {
      showToast("请输入管理密钥", "danger");
      return;
    }
    setManagementKey(trimmed);
    setManagementKeyState(trimmed);
    setAuthRequired(false);
    await queryClient.resetQueries();
    pushHashRoute(afterLoginRoute);
  };

  const showLogin = route === "login" || !hasManagementKey || authRequired;

  if (showLogin) {
    return (
      <>
        <LoginPage initialKey={managementKey} authError={authRequired} onSubmit={submitLogin} />
        {toast ? <ToastView toast={toast} /> : null}
      </>
    );
  }

  return (
    <div className="grid min-h-dvh grid-cols-[260px_minmax(0,1fr)] max-[1024px]:grid-cols-1">
      <aside className="border-r bg-card p-4 max-[1024px]:sticky max-[1024px]:top-0 max-[1024px]:z-20 max-[1024px]:grid max-[1024px]:gap-3 max-[1024px]:border-b max-[1024px]:border-r-0">
        <div className="grid gap-1 border-b pb-4 max-[1024px]:border-b-0 max-[1024px]:pb-0">
          <div className="flex items-center gap-2 text-base font-semibold">
            <Network className="h-5 w-5 text-primary" />
            资源池控制台
          </div>
          <p className="text-xs leading-5 text-muted-foreground">Claude Code 账号与静态出口代理</p>
        </div>
        <nav className="mt-5 grid gap-2 max-[1024px]:mt-0 max-[1024px]:grid-cols-[repeat(auto-fit,minmax(150px,1fr))]" aria-label="主导航">
          <NavButton active={view === "home"} icon={Home} onClick={() => navigate("home")}>
            入口
          </NavButton>
          <NavButton active={view === "accounts"} icon={UsersRound} onClick={() => navigate("accounts")}>
            Claude Code 账号池
          </NavButton>
          <NavButton active={view === "proxies"} icon={Cable} onClick={() => navigate("proxies")}>
            代理 IP 池
          </NavButton>
        </nav>
        <div className="mt-5 grid gap-2 max-[1024px]:mt-0 max-[1024px]:max-w-sm">
          <div className="rounded-lg border bg-muted/40 p-3">
            <div className="flex items-center gap-2 text-sm font-medium">
              <CheckCircle2 className="h-4 w-4 text-emerald-600" />
              已登录
            </div>
            <p className="mt-1 text-xs leading-5 text-muted-foreground">管理 API 已授权，密钥仅在登录页输入。</p>
          </div>
          <Button variant="outline" onClick={logout}>
            <LogOut className="h-4 w-4" />
            退出登录
          </Button>
          <Button variant="outline" asChild>
            <a href="/management.html#/" className="w-full">
              <ExternalLink className="h-4 w-4" />
              管理中心
            </a>
          </Button>
        </div>
      </aside>

      <main className="min-w-0 overflow-x-hidden p-6 max-[640px]:p-4">
        <header className="flex flex-wrap items-start justify-between gap-4">
          <div className="grid gap-1">
            <h1 className="text-2xl font-semibold leading-tight max-[640px]:text-xl">{pageTitle}</h1>
            <p className="max-w-3xl text-sm leading-6 text-muted-foreground">{pageSubtitle}</p>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={refresh} disabled={loading} title="刷新">
              <RefreshCw className={cn("h-4 w-4", loading && "animate-spin")} />
              刷新
            </Button>
            {view === "home" ? (
              <Button variant="outline" asChild>
                <a href="/management.html#/">
                  <ExternalLink className="h-4 w-4" />
                  返回管理中心
                </a>
              </Button>
            ) : view === "accounts" ? (
              <Button onClick={() => setModal({ type: "oauth" })}>
                <KeyRound className="h-4 w-4" />
                新增 OAuth 账号
              </Button>
            ) : (
              <>
                <Button onClick={() => setModal({ type: "proxy" })}>
                  <Plus className="h-4 w-4" />
                  新增代理
                </Button>
                <Button variant="outline" onClick={() => setModal({ type: "import" })}>
                  <CopyPlus className="h-4 w-4" />
                  批量导入
                </Button>
              </>
            )}
          </div>
        </header>

        <section className="mt-5 grid grid-cols-4 gap-3 max-[1024px]:grid-cols-2 max-[640px]:grid-cols-1">
          {view === "home" ? (
            <>
              <StatCard label="账号总数" value={summary?.account_total || 0} icon={UsersRound} />
              <StatCard label="已绑定代理" value={summary?.account_bound || 0} icon={Network} />
              <StatCard label="代理总数" value={summary?.proxy_total || 0} icon={Cable} />
              <StatCard label="空闲可用代理" value={available.length} icon={Activity} />
            </>
          ) : view === "accounts" ? (
            <>
              <StatCard label="账号总数" value={summary?.account_total || 0} icon={UsersRound} />
              <StatCard label="启用账号" value={summary?.account_enabled || 0} icon={CheckCircle2} />
              <StatCard label="已绑定代理" value={summary?.account_bound || 0} icon={Network} />
              <StatCard label="空闲可用代理" value={available.length} icon={Activity} />
            </>
          ) : (
            <>
              <StatCard label="代理总数" value={summary?.proxy_total || 0} icon={Cable} />
              <StatCard label="健康" value={summary?.proxy_healthy || 0} icon={CheckCircle2} />
              <StatCard label="异常" value={summary?.proxy_unhealthy || 0} icon={ShieldAlert} />
              <StatCard label="已绑定" value={summary?.proxy_bound || 0} icon={Network} />
            </>
          )}
        </section>

        <div className="mt-5">
          {view === "home" ? (
            <ResourceHome summary={summary} availableCount={available.length} loading={loading} />
          ) : view === "accounts" ? (
            <AccountsView
              accounts={accounts}
              available={available}
              loading={loading || accountPoolLoading}
              config={poolConfig}
              profile={poolProfile}
              profileSnapshots={profileSnapshots}
              stats={poolStats}
              models={poolModels}
              usage={usageSummary}
              routingEvents={routingEvents}
              logConfig={logConfig}
              logs={poolLogs}
              calibrations={usageCalibrations}
              onBind={(account) => setModal({ type: "bind", account })}
              onTest={(account) => setModal({ type: "test-account", account, models: poolModels })}
              onToast={showToast}
              onDone={invalidateAll}
            />
          ) : (
            <ProxyView
              proxies={proxies}
              loading={loading}
              onEdit={(proxy) => setModal({ type: "proxy", proxy })}
              onToast={showToast}
              onDone={invalidateAll}
            />
          )}
        </div>
      </main>

      <ResourceModal
        modal={modal}
        available={available}
        onClose={() => setModal(null)}
        onRefresh={invalidateAll}
        onDone={async (message) => {
          setModal(null);
          showToast(message);
          await invalidateAll();
        }}
        onToast={showToast}
      />

      {toast ? <ToastView toast={toast} /> : null}
    </div>
  );
}

function LoginPage({
  initialKey,
  authError,
  onSubmit
}: {
  initialKey: string;
  authError: boolean;
  onSubmit: (value: string) => Promise<void>;
}) {
  const [value, setValue] = useState(initialKey);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    setValue(initialKey);
  }, [initialKey]);

  const handleSubmit = async (event: FormEvent) => {
    event.preventDefault();
    setSubmitting(true);
    try {
      await onSubmit(value);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <main className="grid min-h-dvh place-items-center p-6 max-[640px]:p-4">
      <Card className="w-full max-w-[520px] shadow-panel">
        <CardHeader>
          <div className="mb-2 flex h-11 w-11 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <LogIn className="h-5 w-5" />
          </div>
          <CardTitle>Admin 控制台</CardTitle>
          <CardDescription>请输入管理密钥。</CardDescription>
        </CardHeader>
        <CardContent>
          <form className="grid gap-4" onSubmit={handleSubmit}>
            {authError ? (
              <div className="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-sm text-red-800" role="alert">
                当前管理密钥无效或已过期，请重新输入。
              </div>
            ) : null}
            <Field label="管理密钥" htmlFor="account-pool-management-key">
              <Input
                id="account-pool-management-key"
                type="password"
                value={value}
                onChange={(event) => setValue(event.target.value)}
                placeholder="X-Management-Key"
                autoFocus
                autoComplete="current-password"
              />
            </Field>
            <Button type="submit" disabled={submitting}>
              {submitting ? <Loader2 className="h-4 w-4 animate-spin" /> : <LogIn className="h-4 w-4" />}
              登录
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  );
}

function ResourceHome({
  summary,
  availableCount,
  loading
}: {
  summary?: { account_total: number; account_enabled: number; account_bound: number; proxy_total: number; proxy_healthy: number };
  availableCount: number;
  loading: boolean;
}) {
  if (loading) {
    return <LoadingPanel />;
  }
  return (
    <div className="grid gap-5">
      <div className="grid grid-cols-2 gap-4 max-[860px]:grid-cols-1">
        <EntryCard
          href="#/accounts"
          icon={UsersRound}
          title="Claude Code 账号池"
          description="管理 OAuth 登录账号、代理绑定、健康度和冷却状态。"
          metrics={[
            ["账号", summary?.account_total || 0],
            ["启用", summary?.account_enabled || 0],
            ["已绑 IP", summary?.account_bound || 0]
          ]}
        />
        <EntryCard
          href="#/proxies"
          icon={Cable}
          title="代理 IP 池"
          description="维护静态出口代理，查看健康检测、延迟、占用和解绑。"
          metrics={[
            ["代理", summary?.proxy_total || 0],
            ["健康", summary?.proxy_healthy || 0],
            ["空闲可用", availableCount]
          ]}
        />
      </div>
      <Card>
        <CardHeader>
          <CardTitle>入口方式</CardTitle>
          <CardDescription>
            资源池控制台使用独立页面和独立 hash 深链，后续同步上游管理面板时冲突最小。
          </CardDescription>
        </CardHeader>
        <CardContent className="grid gap-3 text-sm leading-6 text-muted-foreground">
          <p>
            常用地址：<span className="font-medium text-foreground">/account-pool.html#/accounts</span> 和{" "}
            <span className="font-medium text-foreground">/account-pool.html#/proxies</span>。
          </p>
          <p>未登录时会自动进入本页自己的登录页，不读取 management.html 保存的登录状态。</p>
        </CardContent>
      </Card>
    </div>
  );
}

function EntryCard({
  href,
  icon: Icon,
  title,
  description,
  metrics
}: {
  href: string;
  icon: typeof UsersRound;
  title: string;
  description: string;
  metrics: Array<[string, number]>;
}) {
  return (
    <a
      href={href}
      className="group grid min-h-56 gap-5 rounded-lg border bg-card p-5 text-foreground shadow-none transition-colors hover:border-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex h-11 w-11 items-center justify-center rounded-lg bg-accent text-accent-foreground">
          <Icon className="h-5 w-5" />
        </div>
        <ArrowRight className="h-5 w-5 text-muted-foreground transition-transform group-hover:translate-x-1 group-hover:text-primary" />
      </div>
      <div className="grid gap-2">
        <h2 className="text-lg font-semibold leading-tight">{title}</h2>
        <p className="text-sm leading-6 text-muted-foreground">{description}</p>
      </div>
      <dl className="grid grid-cols-3 gap-2">
        {metrics.map(([label, value]) => (
          <div key={label} className="rounded-lg border bg-muted/35 p-3">
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="mt-1 text-xl font-semibold leading-none">{value}</dd>
          </div>
        ))}
      </dl>
    </a>
  );
}

function NavButton({
  active,
  icon: Icon,
  children,
  onClick
}: {
  active: boolean;
  icon: typeof UsersRound;
  children: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex min-h-11 items-center gap-2 rounded-lg px-3 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        active ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground"
      )}
    >
      <Icon className="h-4 w-4 shrink-0" />
      <span className="whitespace-nowrap">{children}</span>
    </button>
  );
}

function ToastView({ toast }: { toast: ToastState }) {
  return (
    <div
      className={cn(
        "fixed bottom-5 right-5 z-50 max-w-[min(460px,calc(100vw-2.5rem))] rounded-lg px-4 py-3 text-sm shadow-panel",
        toast.tone === "danger" ? "bg-red-700 text-white" : "bg-slate-950 text-white"
      )}
      role="status"
      aria-live="polite"
    >
      {toast.message}
    </div>
  );
}

function StatCard({ label, value, icon: Icon }: { label: string; value: number; icon: typeof UsersRound }) {
  return (
    <Card className="shadow-none">
      <CardContent className="flex items-center justify-between gap-3 p-4">
        <div className="grid gap-1">
          <span className="text-sm text-muted-foreground">{label}</span>
          <strong className="text-2xl leading-none">{value}</strong>
        </div>
        <div className="rounded-lg bg-accent p-2 text-accent-foreground">
          <Icon className="h-5 w-5" />
        </div>
      </CardContent>
    </Card>
  );
}

function AccountsView({
  accounts,
  available,
  loading,
  config,
  profile,
  profileSnapshots,
  stats,
  models,
  usage,
  routingEvents,
  logConfig,
  logs,
  calibrations,
  onBind,
  onTest,
  onToast,
  onDone
}: {
  accounts: AccountRow[];
  available: ProxyResource[];
  loading: boolean;
  config?: ClaudeCodePoolConfigResponse;
  profile?: ClaudeCodeProfileResponse;
  profileSnapshots: ClaudeCodeProfileSnapshot[];
  stats?: ClaudeCodePoolStats;
  models: ClaudeCodeModel[];
  usage?: UsageSummary;
  routingEvents: RoutingEvent[];
  logConfig?: { raw: AccountPoolLogRawConfig; effective: AccountPoolLogEffectiveConfig };
  logs: AccountPoolLogLine[];
  calibrations?: UsageCalibrationResponse;
  onBind: (account: ClaudeCodeAccount) => void;
  onTest: (account: ClaudeCodeAccount) => void;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
  onDone: () => Promise<void>;
}) {
  const effectiveConfig = config?.effective || defaultPoolEffectiveConfig();
  const [activeTab, setActiveTab] = useState<"accounts" | "config">("accounts");
  const [selectedIDs, setSelectedIDs] = useState<string[]>([]);
  const [page, setPage] = useState(1);
  const [detailRow, setDetailRow] = useState<AccountRow | null>(null);
  const selectedSet = useMemo(() => new Set(selectedIDs), [selectedIDs]);
  const selectedCount = selectedIDs.length;
  const pageSize = 10;
  const pageCount = Math.max(1, Math.ceil(accounts.length / pageSize));
  const currentPage = Math.min(page, pageCount);
  const pageRows = useMemo(() => accounts.slice((currentPage - 1) * pageSize, currentPage * pageSize), [accounts, currentPage]);
  const pageIDs = pageRows.map((row) => row.account.id);
  const pageSelected = pageIDs.length > 0 && pageIDs.every((id) => selectedSet.has(id));
  const pagePartiallySelected = pageIDs.some((id) => selectedSet.has(id)) && !pageSelected;
  useEffect(() => {
    setSelectedIDs((current) => current.filter((id) => accounts.some((row) => row.account.id === id)));
  }, [accounts]);
  useEffect(() => {
    setPage((current) => Math.min(current, Math.max(1, Math.ceil(accounts.length / pageSize))));
  }, [accounts.length]);

  const setAccountSelected = (id: string, selected: boolean) => {
    setSelectedIDs((current) => {
      if (selected) {
        return current.includes(id) ? current : [...current, id];
      }
      return current.filter((item) => item !== id);
    });
  };
  const setAllSelected = (ids: string[], selected: boolean) => {
    setSelectedIDs((current) => {
      const next = new Set(current);
      for (const id of ids) {
        if (selected) {
          next.add(id);
        } else {
          next.delete(id);
        }
      }
      return Array.from(next);
    });
  };
  const clearSelection = () => setSelectedIDs([]);

  const patchMutation = useMutation({
    mutationFn: ({ accountID, enabled }: { accountID: string; enabled: boolean }) =>
      api.patchAccount(accountID, { enabled } as Partial<ClaudeCodeAccount>),
    onSuccess: async () => {
      await onDone();
      onToast("账号状态已更新");
    },
    onError: (error) => onToast(`更新失败：${errorMessage(error)}`, "danger")
  });
  const unbindMutation = useMutation({
    mutationFn: api.unbindAccountProxy,
    onSuccess: async () => {
      await onDone();
      onToast("账号已解绑");
    },
    onError: (error) => onToast(`解绑失败：${errorMessage(error)}`, "danger")
  });
  const resetMutation = useMutation({
    mutationFn: api.resetAccountCooling,
    onSuccess: async () => {
      await onDone();
      onToast("冷却已清除");
    },
    onError: (error) => onToast(`清除失败：${errorMessage(error)}`, "danger")
  });
  const deleteMutation = useMutation({
    mutationFn: api.deleteAccount,
    onSuccess: async () => {
      await onDone();
      onToast("账号已删除");
    },
    onError: (error) => onToast(`删除失败：${errorMessage(error)}`, "danger")
  });
  const quotaMutation = useMutation({
    mutationFn: api.refreshAccountQuota,
    onSuccess: async (data) => {
      await onDone();
      if (data.warning) {
        onToast(`额度刷新异常：${data.warning}`, "danger");
      } else {
        onToast("额度已刷新");
      }
    },
    onError: (error) => onToast(`额度刷新失败：${errorMessage(error)}`, "danger")
  });
  const tokenMutation = useMutation({
    mutationFn: api.refreshAccountToken,
    onSuccess: async (data) => {
      await onDone();
      if (data.warning) {
        onToast(`Token 刷新异常：${data.warning}`, "danger");
      } else {
        onToast("Token 已刷新");
      }
    },
    onError: (error) => onToast(`Token 刷新失败：${errorMessage(error)}`, "danger")
  });
  const batchMutation = useMutation({
    mutationFn: ({ action, ids }: { action: AccountBatchAction; ids: string[] }) => api.batchAccounts(action, ids),
    onSuccess: async (data) => {
      await onDone();
      if (data.failed > 0) {
        const firstError = data.errors?.[0]?.message;
        onToast(`批量操作完成：成功 ${data.ok}，失败 ${data.failed}${firstError ? `，首个错误：${firstError}` : ""}`, "danger");
      } else {
        onToast(`批量操作完成：成功 ${data.ok}`);
      }
      if (data.action === "delete") {
        clearSelection();
      }
    },
    onError: (error) => onToast(`批量操作失败：${errorMessage(error)}`, "danger")
  });

  const runBatch = (action: AccountBatchAction) => {
    if (selectedCount === 0 || batchMutation.isPending) {
      return;
    }
    if (action === "delete" && !window.confirm(`确认删除选中的 ${selectedCount} 个账号？绑定代理会自动释放。`)) {
      return;
    }
    batchMutation.mutate({ action, ids: selectedIDs });
  };

  if (loading) {
    return <LoadingPanel />;
  }

  return (
    <div className="grid gap-5">
      <PublicAPIPanel modelsCount={models.filter((model) => model.enabled).length} />
      <AccountPoolMetrics stats={stats} usage={usage} config={effectiveConfig} />
      <SegmentedTabs
        value={activeTab}
        onChange={setActiveTab}
        items={[
          { value: "accounts", label: "账号卡片" },
          { value: "config", label: "配置" }
        ]}
      />

      {activeTab === "accounts" ? (
        <AccountCardsPanel
          rows={pageRows}
          total={accounts.length}
          page={currentPage}
          pageCount={pageCount}
          pageSelected={pageSelected}
          pagePartiallySelected={pagePartiallySelected}
          selectedSet={selectedSet}
          selectedCount={selectedCount}
          pending={batchMutation.isPending}
          availableCount={available.length}
          onPageChange={setPage}
          onSelectPage={(selected) => setAllSelected(pageIDs, selected)}
          onSelectAccount={setAccountSelected}
          onRunBatch={runBatch}
          onClearSelection={clearSelection}
          onDetails={setDetailRow}
          onTest={onTest}
          onBind={onBind}
          onUnbind={(account) => unbindMutation.mutate(account.id)}
          onReset={(account) => resetMutation.mutate(account.id)}
          onRefreshQuota={(account) => quotaMutation.mutate(account.id)}
          quotaPending={quotaMutation.isPending}
          onToggle={(account) => patchMutation.mutate({ accountID: account.id, enabled: !account.enabled })}
          onDelete={(account) => {
            if (window.confirm("确认删除这个账号？绑定代理会自动释放。")) {
              deleteMutation.mutate(account.id);
            }
          }}
        />
      ) : (
        <div className="grid gap-5">
          <ClaudeCodeProfilePanel profile={profile} />
          <ClaudeCodeProfileSnapshotsPanel snapshots={profileSnapshots} profile={profile} onDone={onDone} onToast={onToast} />
          <div className="grid grid-cols-2 gap-5 max-[1180px]:grid-cols-1">
            <AccountPoolConfigPanel config={effectiveConfig} path={config?.path} onDone={onDone} onToast={onToast} />
            <VirtualCachePanel config={effectiveConfig} stats={stats} onDone={onDone} onToast={onToast} />
          </div>
          <CloakConfigPanel config={effectiveConfig} onDone={onDone} onToast={onToast} />
          <RoutingProtectionPanel config={effectiveConfig} onDone={onDone} onToast={onToast} />
          <ModelManagementPanel models={models} accounts={accounts} onDone={onDone} onToast={onToast} />
          <CleanInputUsagePanel config={effectiveConfig} calibrations={calibrations} accounts={accounts} models={models} onDone={onDone} onToast={onToast} />
          <AccountPoolLogPanel config={logConfig?.effective || effectiveConfig.log} raw={logConfig?.raw} logs={logs} onDone={onDone} onToast={onToast} />
          <RoutingEventsPanel events={routingEvents} />
        </div>
      )}

      <AccountDetailDialog
        row={detailRow}
        open={detailRow !== null}
        onOpenChange={(open) => !open && setDetailRow(null)}
        onTest={onTest}
        onBind={onBind}
        onUnbind={(account) => unbindMutation.mutate(account.id)}
        onReset={(account) => resetMutation.mutate(account.id)}
        onRefreshQuota={(account) => quotaMutation.mutate(account.id)}
        quotaPending={quotaMutation.isPending}
        onRefreshToken={(account) => tokenMutation.mutate(account.id)}
        tokenPending={tokenMutation.isPending}
        onToggle={(account) => patchMutation.mutate({ accountID: account.id, enabled: !account.enabled })}
        onDelete={(account) => {
          if (window.confirm("确认删除这个账号？绑定代理会自动释放。")) {
            deleteMutation.mutate(account.id);
            setDetailRow(null);
          }
        }}
      />
    </div>
  );
}

function PublicAPIPanel({ modelsCount }: { modelsCount: number }) {
  return (
    <Card>
      <CardContent className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-4 p-4 max-[760px]:grid-cols-1">
        <div className="grid gap-2">
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone="info">专属 API</Badge>
            <span className="text-sm font-medium">Base URL</span>
            <code className="break-all rounded-md bg-muted px-2 py-1 text-xs">http://127.0.0.1:8317/claude-acc-pool/v1</code>
          </div>
          <p className="text-sm leading-6 text-muted-foreground">
            使用 config.yaml 里的 api-keys；Claude-native 客户端使用 /claude-acc-pool 作为 Base URL 后继续请求 /v1/messages。
          </p>
        </div>
        <div className="grid grid-cols-2 gap-2 max-[480px]:grid-cols-1">
          <div className="rounded-lg border bg-muted/35 px-3 py-2">
            <div className="text-xs text-muted-foreground">可暴露模型</div>
            <div className="mt-1 text-xl font-semibold leading-none">{modelsCount}</div>
          </div>
          <div className="rounded-lg border bg-muted/35 px-3 py-2">
            <div className="text-xs text-muted-foreground">路径前缀</div>
            <div className="mt-1 text-sm font-semibold leading-none">claude-acc-pool</div>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function ClaudeCodeProfilePanel({
  profile
}: {
  profile?: ClaudeCodeProfileResponse;
}) {
  const effective = profile?.effective;
  const headers = Object.entries(effective?.headers || {});

  return (
    <Card>
      <CardHeader>
        <CardTitle>Claude Code 请求形态</CardTitle>
        <CardDescription>请求形态由后端内置维护，页面只展示当前生效摘要。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-5 gap-3 max-[1280px]:grid-cols-3 max-[760px]:grid-cols-2 max-[560px]:grid-cols-1">
          <ReadOnlyTile label="Claude Code 版本" value={effective?.version || "2.1.178"} />
          <ReadOnlyTile label="身份策略" value="账号固定 · 会话生成" />
          <ReadOnlyTile label="Billing/CCH" value={effective?.billing_block_enabled === false ? "关闭" : "内置签名"} />
          <ReadOnlyTile label="Prompt" value="完整静态提示词" />
          <ReadOnlyTile label="TLS 指纹" value={effective?.tls_profile || "node-claude-code"} />
        </div>

        <div className="rounded-lg border bg-muted/30 p-3">
          <div className="text-xs font-medium text-muted-foreground">User-Agent</div>
          <div className="mt-1 break-all font-mono text-sm">{effective?.user_agent || "claude-cli/2.1.178 (external, sdk-cli)"}</div>
        </div>

        <div className="grid grid-cols-2 gap-3 max-[860px]:grid-cols-1">
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="flex items-center justify-between gap-3">
              <div className="text-xs font-medium text-muted-foreground">Anthropic Beta</div>
              <Badge tone="info">{effective?.betas?.length || 0} 项</Badge>
            </div>
            <div className="mt-3 flex flex-wrap gap-2">
              {(effective?.betas || []).map((beta) => (
                <Badge key={beta} className="break-all font-mono">
                  {beta}
                </Badge>
              ))}
            </div>
          </div>
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="flex items-center justify-between gap-3">
              <div className="text-xs font-medium text-muted-foreground">固定 Headers</div>
              <Badge tone="info">{headers.length} 项</Badge>
            </div>
            <div className="mt-3 grid gap-2">
              {headers.map(([key, value]) => (
                <div key={key} className="grid grid-cols-[minmax(0,0.45fr)_minmax(0,0.55fr)] gap-2 rounded-md bg-card px-2 py-1.5 text-xs">
                  <span className="break-all font-mono text-muted-foreground">{key}</span>
                  <span className="break-all font-mono">{value}</span>
                </div>
              ))}
            </div>
          </div>
        </div>

        <div className="rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm leading-6 text-emerald-900">
          完整 Claude Code 静态提示词、billing block、CCH 签名、账号级 metadata.user_id 和官方域名 TLS profile 都由后端内置维护，不跟随 API 使用者变化。
        </div>
      </CardContent>
    </Card>
  );
}

function ClaudeCodeProfileSnapshotsPanel({
  snapshots,
  profile,
  onDone,
  onToast
}: {
  snapshots: ClaudeCodeProfileSnapshot[];
  profile?: ClaudeCodeProfileResponse;
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [version, setVersion] = useState(profile?.effective?.version || "2.1.178");
  const [activeDiff, setActiveDiff] = useState<{ snapshot: ClaudeCodeProfileSnapshot; report: string } | null>(null);
  const fetchMutation = useMutation({
    mutationFn: ({ version, latest }: { version?: string; latest: boolean }) => api.fetchProfileSnapshot(version, latest),
    onSuccess: async (data) => {
      await onDone();
      onToast(`已拉取 Profile 基线 ${data.item.version}`);
    },
    onError: (error) => onToast(`拉取失败：${errorMessage(error)}`, "danger")
  });
  const diffMutation = useMutation({
    mutationFn: async (id: string) => {
      const data = await api.diffProfileSnapshot(id);
      const detail = await api.profileSnapshot(id);
      return { diff: data.diff, snapshot: detail.item };
    },
    onSuccess: async (data) => {
      setActiveDiff({ snapshot: data.snapshot, report: data.diff.report || "无差异" });
      await onDone();
      onToast("Diff 已更新");
    },
    onError: (error) => onToast(`Diff 失败：${errorMessage(error)}`, "danger")
  });
  const currentFingerprint = profile?.effective
    ? `${profile.effective.version}:${shortText(profile.effective.user_agent || "", 18)}`
    : "未加载";
  const pending = fetchMutation.isPending || diffMutation.isPending;

  return (
    <Card>
      <CardHeader>
        <CardTitle>Profile 基线</CardTitle>
        <CardDescription>从 Phistory 拉取 Claude Code trace/prompt 快照，仅用于对比参考，不会应用到正常业务请求。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-[minmax(0,1fr)_auto_auto] items-end gap-3 max-[760px]:grid-cols-1">
          <Label className="grid gap-2">
            <span>指定版本</span>
            <Input value={version} onChange={(event) => setVersion(event.target.value)} placeholder="2.1.178" />
          </Label>
          <Button
            variant="outline"
            disabled={pending}
            onClick={() => fetchMutation.mutate({ version: version.trim(), latest: false })}
          >
            {fetchMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <FileCode2 className="h-4 w-4" />}
            拉取指定版本
          </Button>
          <Button disabled={pending} onClick={() => fetchMutation.mutate({ latest: true })}>
            {fetchMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
            检查最新
          </Button>
        </div>

        <div className="grid grid-cols-3 gap-3 max-[900px]:grid-cols-1">
          <ReadOnlyTile label="当前运行版本" value={profile?.effective?.version || "-"} />
          <ReadOnlyTile label="当前来源" value={profile?.effective?.updated_from || "builtin"} />
          <ReadOnlyTile label="Profile 摘要" value={currentFingerprint} />
        </div>

        <div className="overflow-x-auto rounded-lg border">
          <table className="min-w-[920px] text-sm">
            <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-3 py-2 font-medium">版本</th>
                <th className="px-3 py-2 font-medium">状态</th>
                <th className="px-3 py-2 font-medium">Prompt Hash</th>
                <th className="px-3 py-2 font-medium">Trace Hash</th>
                <th className="px-3 py-2 font-medium">Diff</th>
                <th className="px-3 py-2 font-medium">拉取时间</th>
                <th className="px-3 py-2 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {snapshots.length === 0 ? (
                <tr>
                  <td colSpan={7} className="px-3 py-8 text-center text-muted-foreground">
                    还没有 Profile 基线，先拉取当前版本或最新版本。
                  </td>
                </tr>
              ) : (
                snapshots.map((item) => (
                  <tr key={item.id} className="border-t">
                    <td className="px-3 py-2">
                      <div className="flex items-center gap-2">
                        <span className="font-medium">{item.version}</span>
                      </div>
                      <div className="text-xs text-muted-foreground">{item.source}</div>
                    </td>
                    <td className="px-3 py-2">
                      <Badge tone={item.status === "promoted" ? "success" : item.status === "failed" ? "danger" : "info"}>{snapshotStatusText(item.status)}</Badge>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">{shortText(item.prompt_hash, 12)}</td>
                    <td className="px-3 py-2 font-mono text-xs">{shortText(item.trace_hash, 12)}</td>
                    <td className="px-3 py-2">
                      <div className="flex flex-wrap items-center gap-2">
                        {item.fatal_count > 0 ? <Badge tone="danger">fatal {item.fatal_count}</Badge> : null}
                        {item.warn_count > 0 ? <Badge tone="warning">warn {item.warn_count}</Badge> : null}
                        {item.fatal_count === 0 && item.warn_count === 0 ? <Badge tone="success">ok</Badge> : null}
                      </div>
                    </td>
                    <td className="px-3 py-2">{formatTime(item.fetched_at)}</td>
                    <td className="px-3 py-2">
                      <div className="flex justify-end gap-2">
                        <Button variant="outline" size="sm" disabled={pending} onClick={() => diffMutation.mutate(item.id)}>
                          <GitCompareArrows className="h-3.5 w-3.5" />
                          Diff
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        {activeDiff ? (
          <ProfileDiffPanel current={profile?.effective} snapshot={activeDiff.snapshot} report={activeDiff.report} onClose={() => setActiveDiff(null)} />
        ) : null}
      </CardContent>
    </Card>
  );
}

type ProfileDiffTab = "prompt" | "headers" | "betas" | "report";
type DiffStatus = "same" | "missing-current" | "missing-snapshot" | "changed";

interface ProfileDiffKVRow {
  key: string;
  current: string;
  snapshot: string;
  status: DiffStatus;
}

interface ProfileDiffLine {
  currentLine?: number;
  snapshotLine?: number;
  current: string;
  snapshot: string;
  status: "same" | "changed" | "added" | "removed";
}

function ProfileDiffPanel({
  current,
  snapshot,
  report,
  onClose
}: {
  current?: ClaudeCodeProfileResponse["effective"];
  snapshot: ClaudeCodeProfileSnapshot;
  report: string;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<ProfileDiffTab>("prompt");
  const currentPrompt = current?.system_prompt || "";
  const snapshotProfile = snapshot.normalized_profile;
  const snapshotPrompt = snapshot.prompt_md || snapshotProfile?.system_prompt || "";
  const lineRows = useMemo(() => buildProfileLineDiff(currentPrompt, snapshotPrompt), [currentPrompt, snapshotPrompt]);
  const headerRows = useMemo(() => buildProfileMapDiff(current?.headers, snapshotProfile?.headers), [current?.headers, snapshotProfile?.headers]);
  const betaRows = useMemo(() => buildProfileListDiff(current?.betas, snapshotProfile?.betas), [current?.betas, snapshotProfile?.betas]);
  const fatalCount = snapshot.fatal_count || 0;
  const warnCount = snapshot.warn_count || 0;
  return (
    <div className="grid gap-3 rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <div className="flex flex-wrap items-center gap-2 text-sm font-semibold">
            <GitCompareArrows className="h-4 w-4 text-muted-foreground" />
            Profile 对比
            <Badge tone={fatalCount ? "danger" : warnCount ? "warning" : "success"}>
              {fatalCount ? `fatal ${fatalCount}` : warnCount ? `warn ${warnCount}` : "一致"}
            </Badge>
          </div>
          <div className="text-xs text-muted-foreground">
            当前运行 {current?.version || "-"} · Phistory {snapshot.version || "-"}
          </div>
        </div>
        <Button variant="ghost" size="sm" onClick={onClose}>
          收起
        </Button>
      </div>

      <div className="flex flex-wrap gap-2">
        <ProfileDiffTabButton active={tab === "prompt"} onClick={() => setTab("prompt")}>
          System Prompt
        </ProfileDiffTabButton>
        <ProfileDiffTabButton active={tab === "headers"} onClick={() => setTab("headers")}>
          Headers
        </ProfileDiffTabButton>
        <ProfileDiffTabButton active={tab === "betas"} onClick={() => setTab("betas")}>
          Beta Headers
        </ProfileDiffTabButton>
        <ProfileDiffTabButton active={tab === "report"} onClick={() => setTab("report")}>
          原始报告
        </ProfileDiffTabButton>
      </div>

      {tab === "prompt" ? (
        <ProfilePromptSideBySide rows={lineRows} currentTitle="当前运行" snapshotTitle={`Phistory ${snapshot.version || ""}`} />
      ) : null}
      {tab === "headers" ? (
        <ProfileKVTable rows={headerRows} leftTitle="当前运行" rightTitle="Phistory 快照" emptyText="Headers 一致或均为空。" />
      ) : null}
      {tab === "betas" ? (
        <ProfileKVTable rows={betaRows} leftTitle="当前运行" rightTitle="Phistory 快照" emptyText="Beta Headers 一致或均为空。" />
      ) : null}
      {tab === "report" ? (
        <pre className="max-h-[520px] overflow-auto whitespace-pre-wrap rounded-md bg-slate-950 p-3 text-xs leading-5 text-slate-50">
          {report || "无差异"}
        </pre>
      ) : null}
    </div>
  );
}

function ProfileDiffTabButton({
  active,
  children,
  onClick
}: {
  active: boolean;
  children: ReactNode;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "min-h-9 rounded-lg border px-3 text-sm font-medium transition-colors",
        active ? "border-primary bg-primary text-primary-foreground" : "bg-card text-muted-foreground hover:bg-muted hover:text-foreground"
      )}
    >
      {children}
    </button>
  );
}

function ProfilePromptSideBySide({
  rows,
  currentTitle,
  snapshotTitle
}: {
  rows: ProfileDiffLine[];
  currentTitle: string;
  snapshotTitle: string;
}) {
  const leftRef = useRef<HTMLDivElement>(null);
  const rightRef = useRef<HTMLDivElement>(null);
  const scrollingRef = useRef<"left" | "right" | null>(null);
  const syncScroll = (side: "left" | "right", event: UIEvent<HTMLDivElement>) => {
    const target = side === "left" ? rightRef.current : leftRef.current;
    if (!target || scrollingRef.current === side) {
      return;
    }
    scrollingRef.current = side === "left" ? "right" : "left";
    target.scrollTop = event.currentTarget.scrollTop;
    target.scrollLeft = event.currentTarget.scrollLeft;
    window.setTimeout(() => {
      scrollingRef.current = null;
    }, 0);
  };
  return (
    <div className="grid gap-3 lg:grid-cols-2">
      <PromptDiffColumn title={currentTitle} side="current" rows={rows} refValue={leftRef} onScroll={(event) => syncScroll("left", event)} />
      <PromptDiffColumn title={snapshotTitle} side="snapshot" rows={rows} refValue={rightRef} onScroll={(event) => syncScroll("right", event)} />
    </div>
  );
}

function PromptDiffColumn({
  title,
  side,
  rows,
  refValue,
  onScroll
}: {
  title: string;
  side: "current" | "snapshot";
  rows: ProfileDiffLine[];
  refValue: RefObject<HTMLDivElement | null>;
  onScroll: (event: UIEvent<HTMLDivElement>) => void;
}) {
  return (
    <div className="min-w-0 overflow-hidden rounded-lg border bg-card">
      <div className="flex items-center justify-between gap-2 border-b bg-muted/40 px-3 py-2">
        <div className="truncate text-sm font-medium">{title}</div>
        <Badge tone="neutral">{rows.length} 行</Badge>
      </div>
      <div ref={refValue} onScroll={onScroll} className="max-h-[70vh] overflow-auto">
        <div className="min-w-[720px] py-1 font-mono text-xs leading-5">
          {rows.length ? (
            rows.map((row) => {
              const text = side === "current" ? row.current : row.snapshot;
              const lineNumber = side === "current" ? row.currentLine : row.snapshotLine;
              const mutedSide =
                (row.status === "added" && side === "current") ||
                (row.status === "removed" && side === "snapshot");
              return (
                <div
                  key={`${side}-${row.currentLine || "x"}-${row.snapshotLine || "x"}`}
                  className={cn(
                    "grid grid-cols-[64px_minmax(0,1fr)] gap-2 px-2",
                    row.status === "changed" && side === "current" && "bg-amber-50 text-amber-950",
                    row.status === "changed" && side === "snapshot" && "bg-amber-50 text-amber-950",
                    row.status === "removed" && side === "current" && "bg-red-50 text-red-950",
                    row.status === "added" && side === "snapshot" && "bg-emerald-50 text-emerald-950",
                    mutedSide && "bg-muted/20 text-muted-foreground"
                  )}
                >
                  <span className="select-none text-right text-muted-foreground">{lineNumber || ""}</span>
                  <span className={cn("whitespace-pre", mutedSide && "select-none")}>{text || " "}</span>
                </div>
              );
            })
          ) : (
            <div className="px-3 py-8 text-center text-muted-foreground">没有可对比的 System Prompt。</div>
          )}
        </div>
      </div>
    </div>
  );
}

function ProfileKVTable({
  rows,
  leftTitle,
  rightTitle,
  emptyText
}: {
  rows: ProfileDiffKVRow[];
  leftTitle: string;
  rightTitle: string;
  emptyText: string;
}) {
  return (
    <div className="overflow-x-auto rounded-lg border bg-card">
      <table className="min-w-[860px] text-sm">
        <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
          <tr>
            <th className="px-3 py-2 font-medium">Key</th>
            <th className="px-3 py-2 font-medium">{leftTitle}</th>
            <th className="px-3 py-2 font-medium">{rightTitle}</th>
            <th className="px-3 py-2 font-medium">状态</th>
          </tr>
        </thead>
        <tbody>
          {rows.length ? (
            rows.map((row) => (
              <tr key={row.key} className={cn("border-t", row.status !== "same" && "bg-amber-50/35")}>
                <td className="px-3 py-2 font-mono text-xs">{row.key}</td>
                <td className="max-w-[320px] break-all px-3 py-2 font-mono text-xs">{row.current || "-"}</td>
                <td className="max-w-[320px] break-all px-3 py-2 font-mono text-xs">{row.snapshot || "-"}</td>
                <td className="px-3 py-2">
                  <DiffStatusBadge status={row.status} />
                </td>
              </tr>
            ))
          ) : (
            <tr>
              <td colSpan={4} className="px-3 py-8 text-center text-muted-foreground">
                {emptyText}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function DiffStatusBadge({ status }: { status: DiffStatus }) {
  if (status === "same") {
    return <Badge tone="success">一致</Badge>;
  }
  if (status === "missing-current") {
    return <Badge tone="warning">当前缺失</Badge>;
  }
  if (status === "missing-snapshot") {
    return <Badge tone="neutral">快照缺失</Badge>;
  }
  return <Badge tone="danger">不一致</Badge>;
}

function buildProfileLineDiff(current: string, snapshot: string): ProfileDiffLine[] {
  if (!current.trim() && !snapshot.trim()) {
    return [];
  }
  const currentLines = splitProfileLines(current);
  const snapshotLines = splitProfileLines(snapshot);
  const aligned = buildProfileAlignedLineDiff(currentLines, snapshotLines);
  return collapseAdjacentLineChanges(aligned);
}

function buildProfileAlignedLineDiff(currentLines: string[], snapshotLines: string[]): ProfileDiffLine[] {
  const currentLength = currentLines.length;
  const snapshotLength = snapshotLines.length;
  const width = snapshotLength + 1;
  const table = new Uint16Array((currentLength + 1) * (snapshotLength + 1));
  for (let i = currentLength - 1; i >= 0; i--) {
    const currentLine = currentLines[i];
    const row = i * width;
    const nextRow = (i + 1) * width;
    for (let j = snapshotLength - 1; j >= 0; j--) {
      table[row + j] =
        currentLine === snapshotLines[j]
          ? table[nextRow + j + 1] + 1
          : Math.max(table[nextRow + j], table[row + j + 1]);
    }
  }
  const rows: ProfileDiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < currentLength && j < snapshotLength) {
    if (currentLines[i] === snapshotLines[j]) {
      rows.push({
        currentLine: i + 1,
        snapshotLine: j + 1,
        current: currentLines[i],
        snapshot: snapshotLines[j],
        status: "same"
      });
      i++;
      j++;
      continue;
    }
    const removeScore = table[(i + 1) * width + j];
    const addScore = table[i * width + j + 1];
    if (removeScore >= addScore) {
      rows.push({
        currentLine: i + 1,
        current: currentLines[i],
        snapshot: "",
        status: "removed"
      });
      i++;
    } else {
      rows.push({
        snapshotLine: j + 1,
        current: "",
        snapshot: snapshotLines[j],
        status: "added"
      });
      j++;
    }
  }
  while (i < currentLength) {
    rows.push({
      currentLine: i + 1,
      current: currentLines[i],
      snapshot: "",
      status: "removed"
    });
    i++;
  }
  while (j < snapshotLength) {
    rows.push({
      snapshotLine: j + 1,
      current: "",
      snapshot: snapshotLines[j],
      status: "added"
    });
    j++;
  }
  return rows;
}

function collapseAdjacentLineChanges(rows: ProfileDiffLine[]): ProfileDiffLine[] {
  const out: ProfileDiffLine[] = [];
  for (let i = 0; i < rows.length; i++) {
    const current = rows[i];
    const next = rows[i + 1];
    if (
      current?.status === "removed" &&
      next?.status === "added" &&
      current.current.trim() !== "" &&
      next.snapshot.trim() !== ""
    ) {
      out.push({
        currentLine: current.currentLine,
        snapshotLine: next.snapshotLine,
        current: current.current,
        snapshot: next.snapshot,
        status: "changed"
      });
      i++;
      continue;
    }
    out.push(current);
  }
  return out;
}

function splitProfileLines(value: string) {
  const normalized = value.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  if (normalized === "") {
    return [];
  }
  return normalized.split("\n");
}

function buildProfileMapDiff(current?: Record<string, string>, snapshot?: Record<string, string>): ProfileDiffKVRow[] {
  const currentNorm = normalizeProfileMap(current);
  const snapshotNorm = normalizeProfileMap(snapshot);
  const keys = Array.from(new Set([...Object.keys(currentNorm), ...Object.keys(snapshotNorm)])).sort((a, b) => a.localeCompare(b));
  return keys.map((key) => {
    const currentValue = currentNorm[key] || "";
    const snapshotValue = snapshotNorm[key] || "";
    return {
      key,
      current: currentValue,
      snapshot: snapshotValue,
      status: diffValueStatus(currentValue, snapshotValue)
    };
  });
}

function buildProfileListDiff(current?: string[], snapshot?: string[]): ProfileDiffKVRow[] {
  const currentSet = normalizeProfileList(current);
  const snapshotSet = normalizeProfileList(snapshot);
  const keys = Array.from(new Set([...Object.keys(currentSet), ...Object.keys(snapshotSet)])).sort((a, b) => a.localeCompare(b));
  return keys.map((key) => {
    const currentValue = currentSet[key] || "";
    const snapshotValue = snapshotSet[key] || "";
    return {
      key,
      current: currentValue,
      snapshot: snapshotValue,
      status: diffValueStatus(currentValue, snapshotValue)
    };
  });
}

function normalizeProfileMap(values?: Record<string, string>) {
  const out: Record<string, string> = {};
  for (const [rawKey, rawValue] of Object.entries(values || {})) {
    const key = rawKey.trim().toLowerCase();
    const value = String(rawValue || "").trim();
    if (!key || !value) {
      continue;
    }
    out[key] = value;
  }
  return out;
}

function normalizeProfileList(values?: string[]) {
  const out: Record<string, string> = {};
  for (const value of values || []) {
    const trimmed = String(value || "").trim();
    if (!trimmed) {
      continue;
    }
    out[trimmed] = trimmed;
  }
  return out;
}

function diffValueStatus(current: string, snapshot: string): DiffStatus {
  if (!current && snapshot) {
    return "missing-current";
  }
  if (current && !snapshot) {
    return "missing-snapshot";
  }
  return current === snapshot ? "same" : "changed";
}

function ReadOnlyTile({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border bg-muted/30 p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 break-words text-sm font-semibold">{value}</div>
    </div>
  );
}

function AccountPoolMetrics({ stats, usage, config }: { stats?: ClaudeCodePoolStats; usage?: UsageSummary; config: ClaudeCodePoolEffectiveConfig }) {
  const rpmLimit = stats?.rpm_limit || config.routing.per_account_rpm * Math.max(1, stats?.account_count || 0);
  return (
    <Card>
      <CardHeader>
        <CardTitle>运行指标</CardTitle>
        <CardDescription>独立统计 Claude Code Account Pool，不混入原 Claude API Pool。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-6 gap-3 max-[1280px]:grid-cols-3 max-[760px]:grid-cols-2 max-[480px]:grid-cols-1">
          <MetricTile label="可用账号" value={`${stats?.available_accounts || 0}`} sub={`总数 ${stats?.account_count || 0}`} icon={UsersRound} />
          <MetricTile label="全局并发" value={`${stats?.in_flight || 0}`} sub={`冷却/不可用 ${stats?.cooling_accounts || 0}`} icon={Activity} />
          <MetricTile label="RPM" value={`${stats?.rpm_used || 0} / ${rpmLimit || 0}`} sub={`本地拒绝 ${stats?.local_reject_count || 0}`} icon={Clock3} />
          <MetricTile
            label="真实缓存率"
            value={formatRatioPercent(stats?.real_cache_ratio)}
            sub={`读 ${formatTokenCompact(stats?.cache_read_tokens || 0)} / 建 ${formatTokenCompact(stats?.cache_creation_tokens || 0)}`}
            icon={Database}
          />
          <MetricTile
            label="亲和 Key / lanes"
            value={`${stats?.active_affinity_keys || 0} / ${stats?.warm_lanes || stats?.affinity_auto_plan?.effective_lanes || 0}`}
            sub="缓存路由"
            icon={Network}
          />
          <MetricTile
            label="成功率"
            value={formatRatioPercent(stats?.success_rate)}
            sub={`${stats?.success_count || 0} / ${stats?.request_count || 0}`}
            icon={CheckCircle2}
          />
        </div>

        <div className="grid grid-cols-5 gap-3 max-[1180px]:grid-cols-3 max-[760px]:grid-cols-2 max-[480px]:grid-cols-1">
          <CompactStat label="真实输入" value={formatTokenLarge(stats?.raw_input_tokens || stats?.input_tokens || 0)} />
          <CompactStat label="真实输出" value={formatTokenLarge(stats?.output_tokens || 0)} />
          <CompactStat label="缓存创建" value={formatTokenLarge(stats?.cache_creation_tokens || 0)} />
          <CompactStat label="缓存读取" value={formatTokenLarge(stats?.cache_read_tokens || 0)} />
          <CompactStat label="真实总量" value={formatTokenLarge(stats?.raw_total_tokens || realTotalTokens(stats))} />
        </div>

        <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.2fr)] gap-3 max-[1080px]:grid-cols-1">
          <UsageBucket title="近 1h 按模型" items={usage?.by_requested_model?.length ? usage.by_requested_model : usage?.by_model || []} />
          <UsageBucket title="近 1h 按账号" items={usage?.by_account || []} />
          <RecentRoutingErrors errors={stats?.recent_errors || []} />
        </div>
      </CardContent>
    </Card>
  );
}

function MetricTile({
  label,
  value,
  sub,
  icon: Icon
}: {
  label: string;
  value: string;
  sub: string;
  icon: typeof UsersRound;
}) {
  return (
    <div className="grid min-h-28 gap-2 rounded-lg border bg-muted/25 p-3">
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>{label}</span>
        <Icon className="h-4 w-4" />
      </div>
      <div className="text-2xl font-semibold leading-none">{value}</div>
      <div className="break-words text-xs text-muted-foreground">{sub}</div>
    </div>
  );
}

function AccountPoolConfigPanel({
  config,
  path,
  onDone,
  onToast
}: {
  config: ClaudeCodePoolEffectiveConfig;
  path?: string;
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [enabled, setEnabled] = useState(config.enabled);
  const [pureMode, setPureMode] = useState(config.pure_mode);
  useEffect(() => {
    setEnabled(config.enabled);
    setPureMode(config.pure_mode);
  }, [config.enabled, config.pure_mode]);
  const mutation = useMutation({
    mutationFn: () => api.savePoolConfig(toRawPoolConfig({ ...config, enabled, pure_mode: pureMode })),
    onSuccess: async () => {
      await onDone();
      onToast("基础配置已保存");
    },
    onError: (error) => onToast(`保存失败：${errorMessage(error)}`, "danger")
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>基础配置</CardTitle>
        <CardDescription>账号池开关和 Claude Code 请求净化策略。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <ToggleRow label="启用账号池" checked={enabled} onChange={setEnabled} />
        <ToggleRow label="纯净模式" checked={pureMode} onChange={setPureMode} />
        <div className="rounded-lg border bg-muted/30 px-3 py-2 text-sm leading-6 text-muted-foreground">
          <div className="flex items-center gap-2 font-medium text-foreground">
            <Database className="h-4 w-4" />
            SQLite 主存储
          </div>
          <div className="mt-1 break-all">{path || "resource-pools.db"}</div>
        </div>
        <div className="flex justify-end">
          <Button onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
            保存配置
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function VirtualCachePanel({
  config,
  stats,
  onDone,
  onToast
}: {
  config: ClaudeCodePoolEffectiveConfig;
  stats?: ClaudeCodePoolStats;
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [virtualCache, setVirtualCache] = useState<VirtualCacheEffectiveConfig>(config.virtual_cache);
  useEffect(() => {
    setVirtualCache(config.virtual_cache);
  }, [config.virtual_cache]);
  const setField = <K extends keyof VirtualCacheEffectiveConfig>(key: K, value: VirtualCacheEffectiveConfig[K]) =>
    setVirtualCache((prev) => ({ ...prev, [key]: value }));
  const mutation = useMutation({
    mutationFn: () => api.savePoolConfig(toRawPoolConfig({ ...config, virtual_cache: virtualCache })),
    onSuccess: async () => {
      await onDone();
      onToast("虚拟账本配置已保存");
    },
    onError: (error) => onToast(`保存失败：${errorMessage(error)}`, "danger")
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>虚拟账本</CardTitle>
        <CardDescription>对外缓存口径。默认关闭，真实缓存优先靠会话亲和和固定出口。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-3 gap-3 max-[640px]:grid-cols-1">
          <CompactStat label="目标" value={formatRatioPercent(virtualCache.target_cache_reuse_ratio)} />
          <CompactStat label="实际" value={formatRatioPercent(stats?.real_cache_ratio)} />
          <CompactStat label="样本" value={`${stats?.request_count || 0}`} />
        </div>
        <ToggleRow label="启用虚拟缓存账本" checked={virtualCache.enabled} onChange={(value) => setField("enabled", value)} />
        <Field label="账本模式">
          <Select value={virtualCache.mode} onChange={(event) => setField("mode", event.target.value)}>
            <option value="natural">自然增长</option>
            <option value="forced">强制目标</option>
          </Select>
        </Field>
        <div className="grid grid-cols-3 gap-3 max-[760px]:grid-cols-1">
          <PercentInput label="缓存命中率 %" value={virtualCache.hit_rate} onChange={(value) => setField("hit_rate", value)} />
          <PercentInput
            label="目标复用率 %"
            value={virtualCache.target_cache_reuse_ratio}
            onChange={(value) => setField("target_cache_reuse_ratio", value)}
          />
          <PercentInput
            label="压缩重置比例 %"
            value={virtualCache.context_shrink_reset_ratio}
            onChange={(value) => setField("context_shrink_reset_ratio", value)}
          />
          <NumberField label="最小缓存 Tokens" value={virtualCache.min_cache_tokens} onChange={(value) => setField("min_cache_tokens", value)} />
          <NumberField label="最大缓存 Tokens" value={virtualCache.max_cache_tokens} onChange={(value) => setField("max_cache_tokens", value)} />
          <NumberField
            label="未缓存输入 Tokens"
            value={virtualCache.uncached_input_tokens}
            onChange={(value) => setField("uncached_input_tokens", value)}
          />
          <NumberField
            label="最小创建 Tokens"
            value={virtualCache.min_creation_tokens}
            onChange={(value) => setField("min_creation_tokens", value)}
          />
          <NumberField
            label="最大创建 Tokens"
            value={virtualCache.max_creation_tokens}
            onChange={(value) => setField("max_creation_tokens", value)}
          />
        </div>
        <div className="flex justify-end">
          <Button onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
            保存账本
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function CloakConfigPanel({
  config,
  onDone,
  onToast
}: {
  config: ClaudeCodePoolEffectiveConfig;
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [cloak, setCloak] = useState<CloakEffectiveConfig>(config.cloak);
  const [enabled, setEnabled] = useState(config.cloak.mode !== "never");
  const [sensitiveWordsText, setSensitiveWordsText] = useState((config.cloak.sensitive_words || []).join("\n"));
  useEffect(() => {
    setCloak(config.cloak);
    setEnabled(config.cloak.mode !== "never");
    setSensitiveWordsText((config.cloak.sensitive_words || []).join("\n"));
  }, [config.cloak]);
  const setField = <K extends keyof CloakEffectiveConfig>(key: K, value: CloakEffectiveConfig[K]) =>
    setCloak((prev) => ({ ...prev, [key]: value }));
  const effectiveCloak: CloakEffectiveConfig = {
    ...cloak,
    mode: enabled ? (cloak.mode === "never" ? "auto" : cloak.mode || "auto") : "never",
    sensitive_words: parseSensitiveWords(sensitiveWordsText)
  };
  const mutation = useMutation({
    mutationFn: () => api.savePoolConfig(toRawPoolConfig({ ...config, cloak: effectiveCloak })),
    onSuccess: async () => {
      await onDone();
      onToast("请求伪装已保存");
    },
    onError: (error) => onToast(`保存失败：${errorMessage(error)}`, "danger")
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>请求伪装</CardTitle>
        <CardDescription>全局作用于 Claude Code Account Pool 的 OAuth 账号。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-3 gap-3 max-[860px]:grid-cols-1">
          <ToggleBox label="启用请求伪装" checked={enabled} onChange={setEnabled} />
          <Field label="模式">
            <Select
              value={effectiveCloak.mode}
              onChange={(event) => {
                const mode = event.target.value;
                setEnabled(mode !== "never");
                setField("mode", mode);
              }}
            >
              <option value="auto">自动</option>
              <option value="always">始终</option>
              <option value="never">关闭</option>
            </Select>
          </Field>
          <ToggleBox label="严格模式" checked={cloak.strict_mode} onChange={(value) => setField("strict_mode", value)} />
          <Field label="敏感词" className="col-span-2 max-[860px]:col-span-1">
            <Textarea
              value={sensitiveWordsText}
              onChange={(event) => setSensitiveWordsText(event.target.value)}
              placeholder="每行一个，或用英文逗号分隔"
              className="min-h-24"
            />
          </Field>
        </div>
        <div className="flex justify-end">
          <Button onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
            保存伪装
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function RoutingProtectionPanel({
  config,
  onDone,
  onToast
}: {
  config: ClaudeCodePoolEffectiveConfig;
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [routing, setRouting] = useState<RoutingEffectiveConfig>(config.routing);
  useEffect(() => {
    setRouting(config.routing);
  }, [config.routing]);
  const setField = <K extends keyof RoutingEffectiveConfig>(key: K, value: RoutingEffectiveConfig[K]) =>
    setRouting((prev) => ({ ...prev, [key]: value }));
  const mutation = useMutation({
    mutationFn: () => api.savePoolConfig(toRawPoolConfig({ ...config, routing })),
    onSuccess: async () => {
      await onDone();
      onToast("路由保护已保存");
    },
    onError: (error) => onToast(`保存失败：${errorMessage(error)}`, "danger")
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>路由保护</CardTitle>
        <CardDescription>限速、换号与真实缓存亲和。Claude Code OAuth 账号建议保持保守。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-3 gap-3 max-[900px]:grid-cols-2 max-[560px]:grid-cols-1">
          <ToggleBox label="启用缓存亲和" checked={routing.cache_affinity_enabled} onChange={(value) => setField("cache_affinity_enabled", value)} />
          <ToggleBox label="自动策略" checked={routing.cache_affinity_auto} onChange={(value) => setField("cache_affinity_auto", value)} />
          <Field label="容量档位">
            <Select value={routing.account_capacity_profile} onChange={(event) => setField("account_capacity_profile", event.target.value)}>
              <option value="custom">自定义</option>
              <option value="conservative">保守</option>
              <option value="balanced">均衡</option>
              <option value="aggressive">激进</option>
            </Select>
          </Field>
          <NumberField label="每账号 RPM" value={routing.per_account_rpm} onChange={(value) => setField("per_account_rpm", value)} />
          <NumberField
            label="每账号并发"
            value={routing.per_account_concurrency}
            onChange={(value) => setField("per_account_concurrency", value)}
          />
          <NumberField label="最大换号次数" value={routing.max_switches} onChange={(value) => setField("max_switches", value)} />
          <NumberField label="换号间隔 ms" value={routing.switch_delay_ms} onChange={(value) => setField("switch_delay_ms", value)} />
          <NumberField
            label="429 初始冷却 ms"
            value={routing.rate_limit_cooldown_ms}
            onChange={(value) => setField("rate_limit_cooldown_ms", value)}
          />
          <NumberField
            label="429 最大冷却 ms"
            value={routing.rate_limit_max_cooldown_ms}
            onChange={(value) => setField("rate_limit_max_cooldown_ms", value)}
          />
          <NumberField
            label="529 初始冷却 ms"
            value={routing.overload_cooldown_ms}
            onChange={(value) => setField("overload_cooldown_ms", value)}
          />
          <NumberField
            label="529 最大冷却 ms"
            value={routing.overload_max_cooldown_ms}
            onChange={(value) => setField("overload_max_cooldown_ms", value)}
          />
          <NumberField
            label="429 同号重试"
            value={routing.same_account_retry_429}
            onChange={(value) => setField("same_account_retry_429", value)}
          />
          <NumberField
            label="529 同号重试"
            value={routing.same_account_retry_529}
            onChange={(value) => setField("same_account_retry_529", value)}
          />
          <NumberField
            label="同号重试间隔 ms"
            value={routing.same_account_retry_delay_ms}
            onChange={(value) => setField("same_account_retry_delay_ms", value)}
          />
          <NumberField
            label="最小缓存 Tokens"
            value={routing.cache_affinity_min_cache_tokens}
            onChange={(value) => setField("cache_affinity_min_cache_tokens", value)}
          />
          <NumberField label="亲和 lanes" value={routing.cache_affinity_lanes} onChange={(value) => setField("cache_affinity_lanes", value)} />
          <NumberField
            label="亲和最大 lanes"
            value={routing.cache_affinity_max_lanes}
            onChange={(value) => setField("cache_affinity_max_lanes", value)}
          />
          <NumberField
            label="亲和等待 ms"
            value={routing.cache_affinity_wait_ms}
            onChange={(value) => setField("cache_affinity_wait_ms", value)}
          />
          <NumberField label="亲和 TTL ms" value={routing.cache_affinity_ttl_ms} onChange={(value) => setField("cache_affinity_ttl_ms", value)} />
        </div>
        <div className="flex justify-end">
          <Button onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
            保存路由
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function ModelManagementPanel({
  models,
  accounts,
  onDone,
  onToast
}: {
  models: ClaudeCodeModel[];
  accounts: AccountRow[];
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [payload, setPayload] = useState<ClaudeCodeModelPayload>({ name: "", alias: "", enabled: true, source: "manual", note: "" });
  const createMutation = useMutation({
    mutationFn: () => api.createPoolModel({ ...payload, alias: payload.alias || payload.name, source: "manual" }),
    onSuccess: async () => {
      setPayload({ name: "", alias: "", enabled: true, source: "manual", note: "" });
      await onDone();
      onToast("模型已添加");
    },
    onError: (error) => onToast(`添加失败：${errorMessage(error)}`, "danger")
  });
  const fetchMutation = useMutation({
    mutationFn: api.fetchPoolModels,
    onSuccess: async (data) => {
      await onDone();
      onToast(`拉取完成：写入 ${data.items.length} 个模型`);
    },
    onError: (error) => onToast(`拉取模型失败：${errorMessage(error)}`, "danger")
  });
  const runnableAccounts = accounts.filter((row) => row.account.enabled && row.account.has_auth_data);

  return (
    <Card>
      <CardHeader>
        <CardTitle>模型管理</CardTitle>
        <CardDescription>/claude-acc-pool/v1/models 只返回启用模型的对外名称，请求进入后会映射到真实模型名。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5">
        <form
          className="grid grid-cols-[minmax(220px,1fr)_minmax(180px,0.8fr)_minmax(140px,0.7fr)_auto] gap-3 max-[980px]:grid-cols-2 max-[560px]:grid-cols-1"
          onSubmit={(event) => {
            event.preventDefault();
            createMutation.mutate();
          }}
        >
          <Field label="真实模型名">
            <Input value={payload.name || ""} onChange={(event) => setPayload((prev) => ({ ...prev, name: event.target.value }))} required />
          </Field>
          <Field label="对外名称 / alias">
            <Input value={payload.alias || ""} onChange={(event) => setPayload((prev) => ({ ...prev, alias: event.target.value }))} />
          </Field>
          <Field label="备注">
            <Input value={payload.note || ""} onChange={(event) => setPayload((prev) => ({ ...prev, note: event.target.value }))} />
          </Field>
          <div className="flex items-end">
            <Button type="submit" disabled={createMutation.isPending || !String(payload.name || "").trim()} className="w-full">
              {createMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
              添加模型
            </Button>
          </div>
        </form>

        <div className="rounded-lg border bg-muted/25 p-3">
          <div className="mb-3 flex items-center gap-2 text-sm font-medium">
            <FileCode2 className="h-4 w-4" />
            从账号拉取支持模型
          </div>
          {runnableAccounts.length ? (
            <div className="flex flex-wrap gap-2">
              {runnableAccounts.map(({ account }) => (
                <Button
                  key={account.id}
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => fetchMutation.mutate(account.id)}
                  disabled={fetchMutation.isPending}
                  title={account.auth_id}
                >
                  {fetchMutation.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
                  {account.email || account.auth_id.slice(0, 12)}
                </Button>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">暂无可用于拉取模型的启用账号。</p>
          )}
        </div>

        <div className="table-scroll">
          <table className="data-table min-w-[980px]">
            <thead>
              <tr>
                <th>真实模型名</th>
                <th>对外名称</th>
                <th>状态</th>
                <th>来源</th>
                <th>备注</th>
                <th>更新时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {models.length ? (
                models.map((model) => <ModelEditorRow key={model.id} model={model} onDone={onDone} onToast={onToast} />)
              ) : (
                <tr>
                  <td colSpan={7} className="text-center text-muted-foreground">
                    暂无模型
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );
}

function ModelEditorRow({
  model,
  onDone,
  onToast
}: {
  model: ClaudeCodeModel;
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [payload, setPayload] = useState<ClaudeCodeModelPayload>({
    name: model.name,
    alias: model.alias,
    enabled: model.enabled,
    source: model.source,
    note: model.note || ""
  });
  useEffect(() => {
    setPayload({
      name: model.name,
      alias: model.alias,
      enabled: model.enabled,
      source: model.source,
      note: model.note || ""
    });
  }, [model]);
  const saveMutation = useMutation({
    mutationFn: () => api.patchPoolModel(model.id, payload),
    onSuccess: async () => {
      await onDone();
      onToast("模型已保存");
    },
    onError: (error) => onToast(`保存模型失败：${errorMessage(error)}`, "danger")
  });
  const deleteMutation = useMutation({
    mutationFn: () => api.deletePoolModel(model.id),
    onSuccess: async () => {
      await onDone();
      onToast("模型已删除");
    },
    onError: (error) => onToast(`删除模型失败：${errorMessage(error)}`, "danger")
  });
  return (
    <tr>
      <td>
        <Input value={payload.name || ""} onChange={(event) => setPayload((prev) => ({ ...prev, name: event.target.value }))} />
      </td>
      <td>
        <Input value={payload.alias || ""} onChange={(event) => setPayload((prev) => ({ ...prev, alias: event.target.value }))} />
      </td>
      <td>
        <button
          type="button"
          className="focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={() => setPayload((prev) => ({ ...prev, enabled: !prev.enabled }))}
        >
          <Badge tone={payload.enabled ? "success" : "danger"}>{payload.enabled ? "启用" : "禁用"}</Badge>
        </button>
      </td>
      <td>
        <span className="text-muted-foreground">{payload.source || "-"}</span>
      </td>
      <td>
        <Input value={payload.note || ""} onChange={(event) => setPayload((prev) => ({ ...prev, note: event.target.value }))} />
      </td>
      <td>{formatTime(model.updated_at)}</td>
      <td>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="outline" onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
            {saveMutation.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <CheckCircle2 className="h-3.5 w-3.5" />}
            保存
          </Button>
          <Button
            size="sm"
            variant="destructive"
            onClick={() => {
              if (window.confirm("确认删除这个模型映射？")) {
                deleteMutation.mutate();
              }
            }}
            disabled={deleteMutation.isPending}
          >
            <Trash2 className="h-3.5 w-3.5" />
            删除
          </Button>
        </div>
      </td>
    </tr>
  );
}

function SegmentedTabs<T extends string>({
  value,
  onChange,
  items
}: {
  value: T;
  onChange: (value: T) => void;
  items: Array<{ value: T; label: string }>;
}) {
  return (
    <div className="flex w-full flex-wrap gap-2 rounded-lg border bg-card p-1">
      {items.map((item) => {
        const active = item.value === value;
        return (
          <button
            key={item.value}
            type="button"
            className={cn(
              "min-h-10 rounded-md px-4 py-2 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              active ? "bg-primary text-primary-foreground shadow-sm" : "text-muted-foreground hover:bg-muted hover:text-foreground"
            )}
            onClick={() => onChange(item.value)}
          >
            {item.label}
          </button>
        );
      })}
    </div>
  );
}

function AccountCardsPanel({
  rows,
  total,
  page,
  pageCount,
  pageSelected,
  pagePartiallySelected,
  selectedSet,
  selectedCount,
  pending,
  availableCount,
  onPageChange,
  onSelectPage,
  onSelectAccount,
  onRunBatch,
  onClearSelection,
  onDetails,
  onTest,
  onBind,
  onUnbind,
  onReset,
  onRefreshQuota,
  quotaPending,
  onToggle,
  onDelete
}: {
  rows: AccountRow[];
  total: number;
  page: number;
  pageCount: number;
  pageSelected: boolean;
  pagePartiallySelected: boolean;
  selectedSet: Set<string>;
  selectedCount: number;
  pending: boolean;
  availableCount: number;
  onPageChange: (page: number) => void;
  onSelectPage: (selected: boolean) => void;
  onSelectAccount: (id: string, selected: boolean) => void;
  onRunBatch: (action: AccountBatchAction) => void;
  onClearSelection: () => void;
  onDetails: (row: AccountRow) => void;
  onTest: (account: ClaudeCodeAccount) => void;
  onBind: (account: ClaudeCodeAccount) => void;
  onUnbind: (account: ClaudeCodeAccount) => void;
  onReset: (account: ClaudeCodeAccount) => void;
  onRefreshQuota: (account: ClaudeCodeAccount) => void;
  quotaPending: boolean;
  onToggle: (account: ClaudeCodeAccount) => void;
  onDelete: (account: ClaudeCodeAccount) => void;
}) {
  const start = total === 0 ? 0 : (page - 1) * 10 + 1;
  const end = total === 0 ? 0 : start + rows.length - 1;

  return (
    <Card>
      <CardHeader className="gap-4">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="grid gap-1">
            <CardTitle>账号卡片</CardTitle>
            <CardDescription>OAuth 登录账号、固定出口绑定、健康度和保守调度集中管理。</CardDescription>
          </div>
          <div className="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
            <Badge tone="info">每页 10 个</Badge>
            <span>空闲代理 {availableCount}</span>
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-muted/25 px-3 py-2">
          <label className="flex min-h-10 items-center gap-2 text-sm">
            <input
              type="checkbox"
              className="h-4 w-4 rounded border-border"
              checked={pageSelected}
              ref={(node) => {
                if (node) {
                  node.indeterminate = pagePartiallySelected;
                }
              }}
              onChange={(event) => onSelectPage(event.target.checked)}
            />
            <span>选择本页</span>
            <span className="text-muted-foreground">已选 {selectedCount}</span>
          </label>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" onClick={() => onRunBatch("test")} disabled={selectedCount === 0 || pending}>
              {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
              测试
            </Button>
            <Button variant="outline" size="sm" onClick={() => onRunBatch("enable")} disabled={selectedCount === 0 || pending}>
              启用
            </Button>
            <Button variant="outline" size="sm" onClick={() => onRunBatch("disable")} disabled={selectedCount === 0 || pending}>
              禁用
            </Button>
            <Button variant="outline" size="sm" onClick={() => onRunBatch("unbind")} disabled={selectedCount === 0 || pending}>
              <Unlink className="h-3.5 w-3.5" />
              解绑
            </Button>
            <Button variant="outline" size="sm" onClick={() => onRunBatch("reset-cooling")} disabled={selectedCount === 0 || pending}>
              <Clock3 className="h-3.5 w-3.5" />
              清冷却
            </Button>
            <Button variant="outline" size="sm" onClick={() => onRunBatch("refresh-quota")} disabled={selectedCount === 0 || pending}>
              <RefreshCw className={cn("h-3.5 w-3.5", pending && "animate-spin")} />
              刷新额度
            </Button>
            <Button variant="destructive" size="sm" onClick={() => onRunBatch("delete")} disabled={selectedCount === 0 || pending}>
              <Trash2 className="h-3.5 w-3.5" />
              删除
            </Button>
            <Button variant="ghost" size="sm" onClick={onClearSelection} disabled={selectedCount === 0 || pending}>
              清空
            </Button>
          </div>
        </div>
      </CardHeader>
      <CardContent className="grid gap-4">
        {total > 0 ? (
          <div className="max-h-[680px] overflow-y-auto pr-1">
            <div className="grid grid-cols-[repeat(auto-fill,minmax(min(100%,280px),1fr))] gap-3">
              {rows.map((row) => (
                <AccountPoolCard
                  key={row.account.id}
                  row={row}
                  selected={selectedSet.has(row.account.id)}
                  quotaPending={quotaPending}
                  onSelectedChange={(selected) => onSelectAccount(row.account.id, selected)}
                  onDetails={() => onDetails(row)}
                  onTest={() => onTest(row.account)}
                  onBind={() => onBind(row.account)}
                  onUnbind={() => onUnbind(row.account)}
                  onReset={() => onReset(row.account)}
                  onRefreshQuota={() => onRefreshQuota(row.account)}
                  onToggle={() => onToggle(row.account)}
                  onDelete={() => onDelete(row.account)}
                />
              ))}
            </div>
          </div>
        ) : (
          <EmptyState title="暂无账号" description="点击顶部新增 OAuth 账号，授权完成后会出现在这里。" />
        )}

        <div className="flex flex-wrap items-center justify-between gap-3 border-t pt-4 text-sm text-muted-foreground">
          <div>
            第 {start}-{end} 个 / 共 {total} 个
          </div>
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => onPageChange(Math.max(1, page - 1))} disabled={page <= 1}>
              上一页
            </Button>
            <span className="min-w-20 text-center tabular-nums">
              {page} / {pageCount}
            </span>
            <Button variant="outline" size="sm" onClick={() => onPageChange(Math.min(pageCount, page + 1))} disabled={page >= pageCount}>
              下一页
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function AccountPoolCard({
  row,
  selected,
  quotaPending,
  onSelectedChange,
  onDetails,
  onTest,
  onBind,
  onUnbind,
  onReset,
  onRefreshQuota,
  onToggle,
  onDelete
}: {
  row: AccountRow;
  selected: boolean;
  quotaPending: boolean;
  onSelectedChange: (selected: boolean) => void;
  onDetails: () => void;
  onTest: () => void;
  onBind: () => void;
  onUnbind: () => void;
  onReset: () => void;
  onRefreshQuota: () => void;
  onToggle: () => void;
  onDelete: () => void;
}) {
  const { account, runtime } = row;
  const displayName = account.email || account.auth_id;
  const bound = Boolean(account.proxy_resource_id || account.proxy);

  return (
    <article
      role="button"
      tabIndex={0}
      className={cn(
        "grid min-h-[360px] min-w-0 cursor-pointer gap-3 overflow-hidden rounded-lg border bg-card p-4 transition-colors hover:border-primary/45 hover:bg-muted/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        selected && "border-primary bg-primary/5"
      )}
      onClick={onDetails}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onDetails();
        }
      }}
    >
      <div className="flex items-start justify-between gap-3">
        <label className="flex min-w-0 items-start gap-2" onClick={(event) => event.stopPropagation()}>
          <input
            type="checkbox"
            className="mt-1 h-4 w-4 shrink-0 rounded border-border"
            checked={selected}
            aria-label={`选择账号 ${displayName}`}
            onChange={(event) => onSelectedChange(event.target.checked)}
          />
          <span className="min-w-0">
            <span className="block break-all text-sm font-semibold leading-5">{displayName}</span>
            <span className="mt-0.5 block break-all text-xs text-muted-foreground">{account.auth_id}</span>
          </span>
        </label>
        <div className="flex shrink-0 flex-col items-end gap-1">
          <AccountStatusBadge account={account} runtime={runtime} />
          <AccountTestBadge account={account} />
        </div>
      </div>

      <AvailabilityPanel availability={account.availability} compact />

      <CompactQuotaGrid quota={account.quota} />
      <CompactCapacity account={account} />
      <BoundProxyIndicator account={account} />

      <div className="mt-auto flex flex-wrap gap-2 border-t pt-3" onClick={(event) => event.stopPropagation()}>
        <Button variant="outline" size="sm" onClick={onDetails}>
          详情
        </Button>
        <Button variant="outline" size="sm" onClick={onTest}>
          <Play className="h-3.5 w-3.5" />
          测试
        </Button>
        <Button variant="outline" size="sm" onClick={bound ? onUnbind : onBind}>
          {bound ? <Unlink className="h-3.5 w-3.5" /> : <Network className="h-3.5 w-3.5" />}
          {bound ? "解绑" : "绑定"}
        </Button>
        <Button variant="outline" size="sm" onClick={onRefreshQuota} disabled={quotaPending}>
          <RefreshCw className={cn("h-3.5 w-3.5", quotaPending && "animate-spin")} />
          额度
        </Button>
        <Button variant="outline" size="sm" onClick={onReset}>
          <Clock3 className="h-3.5 w-3.5" />
          清冷却
        </Button>
        <Button variant="outline" size="sm" onClick={onToggle}>
          {account.enabled ? "禁用" : "启用"}
        </Button>
        <Button variant="destructive" size="sm" onClick={onDelete}>
          <Trash2 className="h-3.5 w-3.5" />
          删除
        </Button>
      </div>
    </article>
  );
}

function CompactQuotaGrid({ quota }: { quota?: AccountQuota }) {
  return (
    <div className="grid min-w-0 grid-cols-2 gap-2">
      <CompactQuotaWindow label="5h" window={findQuotaWindow(quota, ["five_hour", "5 小时", "5小时"])} />
      <CompactQuotaWindow label="7天" window={findQuotaWindow(quota, ["seven_day", "7 天", "7天"])} />
    </div>
  );
}

function CompactQuotaWindow({ label, window }: { label: string; window?: AccountQuota["windows"][number] }) {
  if (!window) {
    return (
      <div className="grid min-w-0 gap-1 rounded-lg border bg-muted/20 p-3">
        <div className="text-xs text-muted-foreground">{label}</div>
        <div className="text-sm font-semibold">未检测</div>
        <Progress value={0} />
      </div>
    );
  }
  return (
    <div className="grid min-w-0 gap-1 rounded-lg border bg-muted/20 p-3">
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>{label}</span>
        <span className="tabular-nums">{formatPercent(window.remain_percent)}</span>
      </div>
      <Progress value={window.remain_percent} />
      <div className="truncate text-xs text-muted-foreground" title={window.resets_at ? `重置 ${formatTime(window.resets_at)}` : "无重置时间"}>
        {window.resets_at ? formatTime(window.resets_at) : "无重置时间"}
      </div>
    </div>
  );
}

function CompactCapacity({ account }: { account: ClaudeCodeAccount }) {
  const runtime = account.runtime_capacity;
  const configured = account.capacity;
  const concurrencyLimit = runtime?.concurrency_limit ?? configured?.concurrency_limit ?? 0;
  const inFlight = runtime?.in_flight ?? 0;
  const percent = concurrencyLimit > 0 ? Math.min(100, Math.round((inFlight / concurrencyLimit) * 100)) : 0;

  return (
    <div className="grid min-w-0 gap-2 rounded-lg border bg-muted/20 p-3">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className="text-muted-foreground">并发</span>
        <span className="font-semibold tabular-nums">{concurrencyLimit > 0 ? `${inFlight} / ${concurrencyLimit}` : "-"}</span>
      </div>
      <Progress value={percent} />
      <div className="grid gap-1 text-xs text-muted-foreground">
        <span>
          RPM {runtime?.rpm_used ?? 0}/{runtime?.rpm_limit || configured?.base_rpm || 0}
        </span>
        <span>
          Sticky buffer {runtime?.buffer_used ?? 0}/{runtime?.sticky_buffer ?? configured?.sticky_buffer ?? 0}
        </span>
      </div>
    </div>
  );
}

function BoundProxyIndicator({ account }: { account: ClaudeCodeAccount }) {
  const bound = Boolean(account.proxy_resource_id || account.proxy);
  return (
    <div className="flex min-h-11 min-w-0 items-center justify-between gap-3 overflow-hidden rounded-lg border bg-muted/20 px-3 py-2 text-sm">
      <span className="text-muted-foreground">固定出口</span>
      {bound ? (
        <span className="inline-flex min-w-0 items-center gap-1.5 text-emerald-700">
          <Check className="h-4 w-4 shrink-0" />
          <span className="truncate font-medium" title={account.proxy ? proxyDisplay(account.proxy) : account.proxy_resource_id}>
            {account.proxy ? proxyDisplay(account.proxy) : "已绑定"}
          </span>
        </span>
      ) : (
        <span className="inline-flex items-center gap-1.5 text-amber-700">
          <Link2Off className="h-4 w-4" />
          未绑定
        </span>
      )}
    </div>
  );
}

function AvailabilityPanel({
  availability,
  compact = false
}: {
  availability?: AccountAvailabilitySummary;
  compact?: boolean;
}) {
  const tone = availabilityTone(availability?.status || "none");
  const hasData = Boolean(availability && availability.request_count > 0);
  const value = hasData ? formatPercent(availability?.success_rate) : "暂无数据";
  const failureCount = availability ? Math.max(0, availability.failure_count || 0) : 0;
  return (
    <div className={cn("grid min-w-0 overflow-hidden rounded-lg border bg-muted/20", compact ? "gap-2 p-2.5" : "gap-3 p-3 bg-background/70")}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="grid gap-0.5">
          <div className="text-xs font-medium text-muted-foreground">2小时可用性</div>
          <div className={cn(compact ? "text-base" : "text-lg", "font-semibold tabular-nums", tone.textClass)}>{value}</div>
        </div>
        <Badge tone={tone.badgeTone}>{tone.label}</Badge>
      </div>
      <AvailabilityStrip availability={availability} compact={compact} />
      <div className={cn("flex items-center justify-between gap-2 text-xs text-muted-foreground", !compact && "flex-wrap")}>
        <span className="shrink-0">{compact ? "2 小时" : "过去 2 小时 · 每格 2 分钟"}</span>
        <span className="min-w-0 truncate tabular-nums">
          {hasData
            ? compact
              ? `${availability?.success_count || 0}/${availability?.request_count || 0} 成功`
              : `${availability?.success_count || 0}/${availability?.request_count || 0} 成功 · 失败 ${failureCount}`
            : compact
              ? "无请求"
              : "灰色表示无请求"}
        </span>
      </div>
    </div>
  );
}

function AvailabilityStrip({ availability, compact = false }: { availability?: AccountAvailabilitySummary; compact?: boolean }) {
  const buckets = aggregateAvailabilityBuckets(normalizeAvailabilityBuckets(availability), 2);
  return (
    <div
      className={cn("grid min-w-0 max-w-full grid-flow-col gap-px overflow-hidden", compact ? "h-4" : "h-8")}
      style={{ gridTemplateColumns: `repeat(${buckets.length}, minmax(0, 1fr))` }}
      aria-label="最近 2 小时每分钟请求可用性"
    >
      {buckets.map((bucket, index) => {
        const tone = availabilityTone(bucket.status);
        const title = availabilityBucketTitle(bucket);
        return (
          <span
            key={`${bucket.started_at || "empty"}-${index}`}
            className={cn("block min-w-0 rounded-[1px]", tone.barClass)}
            title={title}
            aria-label={title}
          />
        );
      })}
    </div>
  );
}

function normalizeAvailabilityBuckets(availability?: AccountAvailabilitySummary) {
  const buckets = availability?.buckets || [];
  if (buckets.length >= 120) {
    return buckets.slice(-120);
  }
  const padding = Array.from({ length: 120 - buckets.length }, () => ({
    started_at: "",
    request_count: 0,
    success_count: 0,
    success_rate: 0,
    status: "none"
  }));
  return [...padding, ...buckets];
}

function aggregateAvailabilityBuckets(buckets: ReturnType<typeof normalizeAvailabilityBuckets>, size: number) {
  if (size <= 1) {
    return buckets;
  }
  const out: ReturnType<typeof normalizeAvailabilityBuckets> = [];
  for (let index = 0; index < buckets.length; index += size) {
    const group = buckets.slice(index, index + size);
    const requestCount = group.reduce((sum, bucket) => sum + (bucket.request_count || 0), 0);
    const successCount = group.reduce((sum, bucket) => sum + (bucket.success_count || 0), 0);
    out.push({
      started_at: group[0]?.started_at || "",
      request_count: requestCount,
      success_count: successCount,
      success_rate: requestCount > 0 ? (successCount * 100) / requestCount : 0,
      status: availabilityStatusForCount(requestCount, successCount)
    });
  }
  return out;
}

function availabilityBucketTitle(bucket: ReturnType<typeof normalizeAvailabilityBuckets>[number]) {
  const time = bucket.started_at ? `${formatMinute(bucket.started_at)} 起 2 分钟` : "无数据";
  if (!bucket.request_count) {
    return `${time} · 无请求`;
  }
  return `${time} · ${bucket.success_count}/${bucket.request_count} 成功 · ${formatPercent(bucket.success_rate)}`;
}

function AvailabilityStats({ availability }: { availability?: AccountAvailabilitySummary }) {
  const hasData = Boolean(availability && availability.request_count > 0);
  return (
    <div className="grid grid-cols-4 gap-3 max-[720px]:grid-cols-2">
      <CompactStat label="请求数" value={hasData ? formatNumber(availability?.request_count) : "暂无"} />
      <CompactStat label="成功" value={hasData ? formatNumber(availability?.success_count) : "暂无"} />
      <CompactStat label="失败" value={hasData ? formatNumber(availability?.failure_count) : "暂无"} />
      <CompactStat label="成功率" value={hasData ? formatPercent(availability?.success_rate) : "暂无数据"} />
    </div>
  );
}

function AccountDetailDialog({
  row,
  open,
  onOpenChange,
  onTest,
  onBind,
  onUnbind,
  onReset,
  onRefreshQuota,
  quotaPending,
  onRefreshToken,
  tokenPending,
  onToggle,
  onDelete
}: {
  row: AccountRow | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onTest: (account: ClaudeCodeAccount) => void;
  onBind: (account: ClaudeCodeAccount) => void;
  onUnbind: (account: ClaudeCodeAccount) => void;
  onReset: (account: ClaudeCodeAccount) => void;
  onRefreshQuota: (account: ClaudeCodeAccount) => void;
  quotaPending: boolean;
  onRefreshToken: (account: ClaudeCodeAccount) => void;
  tokenPending: boolean;
  onToggle: (account: ClaudeCodeAccount) => void;
  onDelete: (account: ClaudeCodeAccount) => void;
}) {
  const account = row?.account;
  const runtime = row?.runtime;
  const bound = Boolean(account?.proxy_resource_id || account?.proxy);
  const identity = parseClaudeCodeIdentity(account?.cloak_user_id);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-5xl">
        {account ? (
          <>
            <DialogHeader>
              <DialogTitle>账号详情</DialogTitle>
              <DialogDescription>{account.email || account.auth_id}</DialogDescription>
            </DialogHeader>
            <div className="grid gap-5">
              <div className="flex flex-wrap items-center gap-2">
                <AccountStatusBadge account={account} runtime={runtime} />
                <AccountTestBadge account={account} />
                {runtime?.cooling_until || account.runtime_capacity?.cooling_until ? <Badge tone="warning">冷却中</Badge> : null}
                {bound ? <Badge tone="success">已绑定代理</Badge> : <Badge tone="warning">未绑定代理</Badge>}
              </div>

              <div className="grid grid-cols-3 gap-3 max-[900px]:grid-cols-1">
                <ReadOnlyTile label="Auth ID" value={account.auth_id} />
                <ReadOnlyTile label="Device ID" value={identity.deviceId || "-"} />
                <ReadOnlyTile label="Account UUID" value={identity.accountUUID || "-"} />
                <ReadOnlyTile label="Session ID" value="请求时按会话生成" />
                <ReadOnlyTile label="运行成功率" value={successRate(runtime)} />
                <ReadOnlyTile label="连续失败" value={`${account.consecutive_failures || 0}`} />
                <ReadOnlyTile label="最近测试" value={formatTime(account.last_test_at)} />
                <ReadOnlyTile label="Token 过期" value={formatTime(account.token_expires_at)} />
                <ReadOnlyTile label="更新时间" value={formatTime(account.updated_at)} />
              </div>

              <div className="grid grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)] gap-4 max-[900px]:grid-cols-1">
                <div className="grid gap-4">
                  <div className="grid gap-3 rounded-lg border bg-muted/20 p-3">
                    <AvailabilityPanel availability={account.availability} />
                    <AvailabilityStats availability={account.availability} />
                    <div className="text-xs text-muted-foreground">
                      当前请求 {account.runtime_capacity?.in_flight || 0} · RPM {account.runtime_capacity?.rpm_used || 0}
                    </div>
                  </div>
                  <div className="rounded-lg border bg-muted/20 p-3">
                    <div className="mb-2 text-sm font-medium">容量</div>
                    <CapacitySummary account={account} />
                  </div>
                  {account.model_statuses ? <ModelStatusStrip statuses={account.model_statuses} /> : null}
                </div>
                <QuotaPanel quota={account.quota} onRefresh={() => onRefreshQuota(account)} refreshing={quotaPending} />
              </div>

              <div className="grid grid-cols-2 gap-4 max-[900px]:grid-cols-1">
                <div className="grid gap-2 rounded-lg border bg-muted/20 p-3">
                  <div className="text-sm font-medium">绑定代理</div>
                  {account.proxy ? (
                    <div className="grid gap-1 text-sm">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{account.proxy.name}</span>
                        <HealthBadge status={account.proxy.health_status} enabled={account.proxy.enabled} />
                        <span className="text-xs text-muted-foreground">{account.proxy.latency_ms || 0} ms</span>
                      </div>
                      <div className="break-all text-xs text-muted-foreground">{proxyDisplay(account.proxy)}</div>
                      <div className="text-xs text-muted-foreground">最近测试 {formatTime(account.proxy.last_checked_at)}</div>
                    </div>
                  ) : (
                    <div className="text-sm text-muted-foreground">未绑定代理</div>
                  )}
                </div>
                <div className="grid gap-2 rounded-lg border bg-muted/20 p-3">
                  <div className="text-sm font-medium">最近错误</div>
                  <div className="break-words text-sm leading-6 text-muted-foreground">{account.last_error || runtime?.last_error || "暂无"}</div>
                </div>
              </div>

              <div className="flex flex-wrap justify-end gap-2 border-t pt-4">
                <Button variant="outline" onClick={() => onTest(account)}>
                  <Play className="h-4 w-4" />
                  测试
                </Button>
                <Button variant="outline" onClick={() => (bound ? onUnbind(account) : onBind(account))}>
                  {bound ? <Unlink className="h-4 w-4" /> : <Network className="h-4 w-4" />}
                  {bound ? "解绑代理" : "绑定代理"}
                </Button>
                <Button variant="outline" onClick={() => onReset(account)}>
                  <Clock3 className="h-4 w-4" />
                  清冷却
                </Button>
                <Button variant="outline" onClick={() => onRefreshToken(account)} disabled={tokenPending}>
                  <RefreshCw className={cn("h-4 w-4", tokenPending && "animate-spin")} />
                  刷新 Token
                </Button>
                <Button variant="outline" onClick={() => onToggle(account)}>
                  {account.enabled ? "禁用账号" : "启用账号"}
                </Button>
                <Button variant="destructive" onClick={() => onDelete(account)}>
                  <Trash2 className="h-4 w-4" />
                  删除账号
                </Button>
              </div>
            </div>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function findQuotaWindow(quota: AccountQuota | undefined, candidates: string[]) {
  const windows = quota?.windows || [];
  const normalized = candidates.map((item) => item.toLowerCase());
  return windows.find((item) => normalized.includes(String(item.key || "").toLowerCase()) || normalized.includes(String(item.name || "").toLowerCase()));
}

function AccountBatchToolbar({
  selectedCount,
  pending,
  onRun,
  onClear
}: {
  selectedCount: number;
  pending: boolean;
  onRun: (action: AccountBatchAction) => void;
  onClear: () => void;
}) {
  return (
    <Card>
      <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
        <div className="grid gap-1">
          <div className="text-sm font-medium">账号批量操作</div>
          <div className="text-xs text-muted-foreground">已选择 {selectedCount} 个账号</div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => onRun("test")} disabled={selectedCount === 0 || pending}>
            {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
            批量测试
          </Button>
          <Button variant="outline" size="sm" onClick={() => onRun("enable")} disabled={selectedCount === 0 || pending}>
            启用
          </Button>
          <Button variant="outline" size="sm" onClick={() => onRun("disable")} disabled={selectedCount === 0 || pending}>
            禁用
          </Button>
          <Button variant="outline" size="sm" onClick={() => onRun("unbind")} disabled={selectedCount === 0 || pending}>
            <Unlink className="h-3.5 w-3.5" />
            解绑代理
          </Button>
          <Button variant="outline" size="sm" onClick={() => onRun("reset-cooling")} disabled={selectedCount === 0 || pending}>
            <Clock3 className="h-3.5 w-3.5" />
            清冷却
          </Button>
          <Button variant="outline" size="sm" onClick={() => onRun("refresh-quota")} disabled={selectedCount === 0 || pending}>
            <RefreshCw className={cn("h-3.5 w-3.5", pending && "animate-spin")} />
            刷新额度
          </Button>
          <Button variant="destructive" size="sm" onClick={() => onRun("delete")} disabled={selectedCount === 0 || pending}>
            <Trash2 className="h-3.5 w-3.5" />
            删除
          </Button>
          <Button variant="ghost" size="sm" onClick={onClear} disabled={selectedCount === 0 || pending}>
            清空选择
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function CapacitySummary({ account }: { account: ClaudeCodeAccount }) {
  const runtime = account.runtime_capacity;
  if (!runtime) {
    return <span className="text-muted-foreground">-</span>;
  }
  return (
    <div className="grid gap-1 text-sm tabular-nums">
      <span>并发 {runtime.in_flight}/{runtime.concurrency_limit}</span>
      <span>RPM {runtime.rpm_used}/{runtime.rpm_limit || runtime.base_rpm}</span>
      <span>Sticky buffer {runtime.buffer_used}/{runtime.sticky_buffer}</span>
    </div>
  );
}

function ModelStatusStrip({ statuses }: { statuses: NonNullable<ClaudeCodeAccount["model_statuses"]> }) {
  const visible = statuses.slice(0, 4);
  if (!visible.length) {
    return (
      <div className="rounded-lg border bg-muted/20 px-3 py-2 text-xs text-muted-foreground">
        暂无模型级健康记录
      </div>
    );
  }
  return (
    <div className="grid gap-2 rounded-lg border bg-muted/20 p-3">
      <div className="text-xs font-medium text-muted-foreground">模型级健康</div>
      <div className="flex flex-wrap gap-2">
        {visible.map((item) => (
          <Badge key={item.model} tone={modelStatusTone(item.status)}>
            {compactModelName(item.model)} · {modelStatusText(item.status)}
          </Badge>
        ))}
      </div>
    </div>
  );
}

function CleanInputUsagePanel({
  config,
  calibrations,
  accounts,
  models,
  onDone,
  onToast
}: {
  config: ClaudeCodePoolEffectiveConfig;
  calibrations?: UsageCalibrationResponse;
  accounts: AccountRow[];
  models: ClaudeCodeModel[];
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [enabled, setEnabled] = useState(config.usage.clean_input_tokens);
  const [accountID, setAccountID] = useState("");
  useEffect(() => {
    setEnabled(config.usage.clean_input_tokens);
  }, [config.usage.clean_input_tokens]);
  const runnableAccounts = accounts.filter((row) => row.account.enabled && row.account.has_auth_data);
  useEffect(() => {
    if (accountID && !runnableAccounts.some((row) => row.account.id === accountID)) {
      setAccountID("");
    }
  }, [accountID, runnableAccounts]);
  const saveMutation = useMutation({
    mutationFn: () =>
      api.savePoolConfig(
        toRawPoolConfig({
          ...config,
          usage: {
            ...config.usage,
            clean_input_tokens: enabled
          }
        })
      ),
    onSuccess: async () => {
      await onDone();
      onToast("纯净输入用量已保存");
    },
    onError: (error) => onToast(`保存失败：${errorMessage(error)}`, "danger")
  });
  const calibrateMutation = useMutation({
    mutationFn: (model: string) => api.calibrateUsage(model, accountID),
    onSuccess: async (data) => {
      await onDone();
      if (data.warning) {
        onToast(`校准异常：${data.warning}`, "danger");
      } else {
        onToast("模型校准已完成");
      }
    },
    onError: (error) => onToast(`校准失败：${errorMessage(error)}`, "danger")
  });
  const calibrationRows: UsageCalibrationResponse["items"] =
    calibrations?.items?.length
      ? calibrations.items
      : models
          .filter((model) => model.enabled)
          .map((model) => ({
            model: model.name,
            profile_fingerprint: config.usage.profile_fingerprint,
            overhead_tokens: config.usage.system_prompt_overhead_tokens,
            effective_overhead_tokens: config.usage.system_prompt_overhead_tokens,
            status: "estimated",
            estimated: true
          }));
  const fingerprint = calibrations?.profile_fingerprint || config.usage.profile_fingerprint;
  const hasUnsavedUsageChange = enabled !== config.usage.clean_input_tokens;
  return (
    <Card>
      <CardHeader>
        <CardTitle>纯净输入用量</CardTitle>
        <CardDescription>只改写对外返回和控制台展示的 input tokens；Anthropic 实际消耗不变。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5">
        <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-4 max-[860px]:grid-cols-1">
          <div className="grid gap-3">
            <ToggleRow label="开启纯净 input_tokens" checked={enabled} onChange={setEnabled} />
            <div className="grid grid-cols-2 gap-3 max-[640px]:grid-cols-1">
              <CompactStat label="默认估算扣减" value={`${calibrations?.default_overhead || config.usage.system_prompt_overhead_tokens} tokens`} />
              <CompactStat label="已保存状态" value={config.usage.clean_input_tokens ? "已开启" : "未开启"} />
              <CompactStat label="未保存变更" value={hasUnsavedUsageChange ? (enabled ? "开启" : "关闭") : "无"} />
            </div>
            <div className="rounded-lg border bg-muted/30 p-3">
              <div className="text-xs font-medium text-muted-foreground">Profile Fingerprint</div>
              <div className="mt-1 break-all font-mono text-xs">{fingerprint || "unknown"}</div>
            </div>
          </div>
          <Button onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
            {saveMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
            保存开关
          </Button>
        </div>

        <div className="grid gap-3 rounded-lg border bg-muted/20 p-3">
          <div className="flex flex-wrap items-end gap-3">
            <Field label="校准账号" className="min-w-[260px] flex-1">
              <Select value={accountID} onChange={(event) => setAccountID(event.target.value)}>
                <option value="">自动选择可用账号</option>
                {runnableAccounts.map(({ account }) => (
                  <option key={account.id} value={account.id}>
                    {account.email || account.auth_id}
                  </option>
                ))}
              </Select>
            </Field>
            <div className="pb-2 text-sm text-muted-foreground">校准会对指定模型发送一次最小 count_tokens。</div>
          </div>
          <div className="table-scroll">
            <table className="data-table min-w-[760px]">
              <thead>
                <tr>
                  <th>模型</th>
                  <th>状态</th>
                  <th>当前扣减</th>
                  <th>检查时间</th>
                  <th>错误</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {calibrationRows.length ? (
                  calibrationRows.map((item) => (
                    <tr key={`${item.model}:${item.profile_fingerprint}`}>
                      <td className="font-mono text-xs">{item.model}</td>
                      <td>
                        <Badge tone={item.status === "calibrated" ? "success" : item.status === "failed" ? "danger" : "neutral"}>
                          {calibrationStatusText(item.status, item.estimated)}
                        </Badge>
                      </td>
                      <td>{item.effective_overhead_tokens ?? item.overhead_tokens}</td>
                      <td>{formatTime(item.checked_at)}</td>
                      <td className="max-w-[260px] truncate text-xs text-muted-foreground" title={item.last_error || ""}>
                        {item.last_error || "-"}
                      </td>
                      <td>
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() => calibrateMutation.mutate(item.model)}
                          disabled={calibrateMutation.isPending || !item.model}
                        >
                          {calibrateMutation.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Activity className="h-3.5 w-3.5" />}
                          校准
                        </Button>
                      </td>
                    </tr>
                  ))
                ) : (
                  <tr>
                    <td colSpan={6} className="text-center text-muted-foreground">
                      暂无可校准模型
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}

function calibrationStatusText(status: string, estimated?: boolean) {
  if (status === "calibrated") {
    return "已校准";
  }
  if (status === "failed") {
    return "失败";
  }
  if (status === "stale") {
    return "已过期";
  }
  return estimated ? "估算" : "未校准";
}

function UsageBucket({ title, items }: { title: string; items: NonNullable<UsageSummary["by_model"]> }) {
  return (
    <div className="rounded-lg border bg-muted/20 p-3">
      <div className="mb-2 text-sm font-medium">{title}</div>
      <div className="grid gap-2">
        {items.length ? (
          items.slice(0, 5).map((item) => (
            <div key={item.key} className="grid grid-cols-[minmax(0,1fr)_auto] gap-2 text-sm">
              <span className="truncate" title={item.key}>
                {item.key}
              </span>
              <span className="text-muted-foreground">
                {item.request_count} 次 · {Math.round(item.success_rate)}% · {formatTokenLarge(item.raw_total_tokens || realTotalTokens(item))}
              </span>
            </div>
          ))
        ) : (
          <p className="text-sm text-muted-foreground">暂无数据</p>
        )}
      </div>
    </div>
  );
}

function RecentRoutingErrors({ errors }: { errors: RoutingEvent[] }) {
  return (
    <div className="rounded-lg border bg-muted/20 p-3">
      <div className="mb-2 text-sm font-medium">最近错误 / 本地拒绝</div>
      <div className="grid gap-2">
        {errors.length ? (
          errors.slice(0, 5).map((event, index) => (
            <div key={`${event.id || index}-${event.created_at}`} className="grid gap-1 rounded-md bg-background/65 px-2 py-2 text-xs">
              <div className="flex flex-wrap items-center gap-2">
                <Badge tone={event.decision === "rejected" ? "warning" : "danger"}>{event.decision || "error"}</Badge>
                <span className="font-medium">{event.requested_model || event.model || "-"}</span>
                <span className="text-muted-foreground">{formatTime(event.created_at)}</span>
              </div>
              <div className="break-words text-muted-foreground">{event.reason || event.error || `HTTP ${event.status_code || "-"}`}</div>
            </div>
          ))
        ) : (
          <p className="text-sm text-muted-foreground">暂无错误或本地拒绝</p>
        )}
      </div>
    </div>
  );
}

function AccountPoolLogPanel({
  config,
  raw,
  logs,
  onDone,
  onToast
}: {
  config: AccountPoolLogEffectiveConfig;
  raw?: AccountPoolLogRawConfig;
  logs: AccountPoolLogLine[];
  onDone: () => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [enabled, setEnabled] = useState(config.enabled);
  const [level, setLevel] = useState(config.level || "info");
  const [maxSizeMB, setMaxSizeMB] = useState(config.max_size_mb || 50);
  const [maxBackups, setMaxBackups] = useState(config.max_backups || 3);
  const [redact, setRedact] = useState(config.redact);
  useEffect(() => {
    setEnabled(config.enabled);
    setLevel(config.level || "info");
    setMaxSizeMB(config.max_size_mb || 50);
    setMaxBackups(config.max_backups || 3);
    setRedact(config.redact);
  }, [config.enabled, config.level, config.max_size_mb, config.max_backups, config.redact]);
  const saveMutation = useMutation({
    mutationFn: () =>
      api.savePoolLogConfig({
        ...raw,
        enabled,
        level,
        dir: config.dir || raw?.dir || "acc-pool-logs",
        max_size_mb: maxSizeMB,
        max_backups: maxBackups,
        redact
      }),
    onSuccess: async () => {
      await onDone();
      onToast("日志配置已保存");
    },
    onError: (error) => onToast(`保存日志配置失败：${errorMessage(error)}`, "danger")
  });
  const clearMutation = useMutation({
    mutationFn: api.clearPoolLogs,
    onSuccess: async () => {
      await onDone();
      onToast("账号池日志已清空");
    },
    onError: (error) => onToast(`清空日志失败：${errorMessage(error)}`, "danger")
  });
  const downloadMutation = useMutation({
    mutationFn: api.downloadPoolLogs,
    onSuccess: (blob) => {
      const href = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = href;
      anchor.download = "account-pool.log";
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(href);
    },
    onError: (error) => onToast(`下载日志失败：${errorMessage(error)}`, "danger")
  });

  return (
    <Card>
      <CardHeader>
        <CardTitle>账号池日志</CardTitle>
        <CardDescription>JSONL 诊断日志，默认脱敏并按大小轮转。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-5 gap-3 max-[1100px]:grid-cols-2 max-[560px]:grid-cols-1">
          <ToggleRow label="启用日志" checked={enabled} onChange={setEnabled} />
          <Field label="日志等级">
            <Select value={level} onChange={(event) => setLevel(event.target.value)}>
              <option value="debug">debug</option>
              <option value="info">info</option>
              <option value="warn">warn</option>
              <option value="error">error</option>
            </Select>
          </Field>
          <Field label="单文件 MB">
            <Input type="number" min={1} max={1024} value={maxSizeMB} onChange={(event) => setMaxSizeMB(Number(event.target.value) || 50)} />
          </Field>
          <Field label="保留文件">
            <Input type="number" min={0} max={30} value={maxBackups} onChange={(event) => setMaxBackups(Number(event.target.value) || 3)} />
          </Field>
          <ToggleRow label="脱敏" checked={redact} onChange={setRedact} />
        </div>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="text-sm text-muted-foreground">目录：{config.dir || "acc-pool-logs"}</div>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={() => saveMutation.mutate()} disabled={saveMutation.isPending}>
              {saveMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <FileText className="h-4 w-4" />}
              保存配置
            </Button>
            <Button variant="outline" onClick={() => downloadMutation.mutate()} disabled={downloadMutation.isPending}>
              {downloadMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Download className="h-4 w-4" />}
              下载日志
            </Button>
            <Button variant="outline" onClick={() => clearMutation.mutate()} disabled={clearMutation.isPending}>
              {clearMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Eraser className="h-4 w-4" />}
              清空
            </Button>
          </div>
        </div>
        <div className="max-h-80 overflow-auto rounded-lg border bg-muted/20">
          {logs.length ? (
            <div className="grid divide-y">
              {logs.slice(0, 80).map((line, index) => (
                <LogLineView key={`${line.entry?.ts || index}-${line.line.slice(0, 12)}`} line={line} />
              ))}
            </div>
          ) : (
            <div className="p-4 text-sm text-muted-foreground">暂无日志</div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

function LogLineView({ line }: { line: AccountPoolLogLine }) {
  const entry = line.entry;
  if (!entry) {
    return <pre className="whitespace-pre-wrap break-all px-3 py-2 text-xs text-muted-foreground">{line.line}</pre>;
  }
  return (
    <div className="grid gap-1 px-3 py-2 text-xs">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={entry.level === "error" ? "danger" : entry.level === "warn" ? "warning" : "info"}>{entry.level}</Badge>
        <span className="font-medium">{entry.event}</span>
        <span className="text-muted-foreground">{formatTime(entry.ts)}</span>
        {entry.status_code ? <span className="tabular-nums text-muted-foreground">HTTP {entry.status_code}</span> : null}
      </div>
      <div className="break-words text-muted-foreground">
        {entry.requested_model || entry.model || "-"} · {entry.decision || "-"} {entry.reason ? `· ${entry.reason}` : ""} {entry.error ? `· ${entry.error}` : ""}
      </div>
    </div>
  );
}

function RoutingEventsPanel({ events }: { events: RoutingEvent[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>最近调度事件</CardTitle>
        <CardDescription>包含选号、本地拒绝、上游错误和成功事件。</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="table-scroll">
          <table className="data-table min-w-[920px]">
            <thead>
              <tr>
                <th>时间</th>
                <th>决策</th>
                <th>模型</th>
                <th>状态</th>
                <th>容量</th>
                <th>原因</th>
              </tr>
            </thead>
            <tbody>
              {events.length ? (
                events.slice(0, 40).map((event, index) => (
                  <tr key={`${event.id || index}-${event.created_at}`}>
                    <td>{formatTime(event.created_at)}</td>
                    <td>
                      <Badge tone={event.decision === "success" ? "success" : event.decision === "rejected" ? "warning" : event.decision === "upstream_error" ? "danger" : "info"}>
                        {event.decision}
                      </Badge>
                    </td>
                    <td className="max-w-64 truncate" title={event.requested_model || event.model}>
                      {event.requested_model || event.model || "-"}
                    </td>
                    <td>{event.status_code || "-"}</td>
                    <td>
                      {event.capacity_used ?? 0}/{event.capacity_limit ?? 0}
                    </td>
                    <td className="max-w-80 break-words">{event.reason || event.error || "-"}</td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={6} className="text-center text-muted-foreground">
                    暂无调度事件
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );
}

function ToggleRow({
  label,
  checked,
  onChange
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex min-h-12 items-center justify-between gap-3 rounded-lg border bg-muted/25 px-3 py-2 text-sm">
      <span className="font-medium">{label}</span>
      <input type="checkbox" className="h-4 w-4" checked={checked} onChange={(event) => onChange(event.target.checked)} />
    </label>
  );
}

function ToggleBox({
  label,
  checked,
  onChange
}: {
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="flex min-h-11 items-center gap-2 rounded-lg border bg-card px-3 py-2 text-sm">
      <input type="checkbox" className="h-4 w-4" checked={checked} onChange={(event) => onChange(event.target.checked)} />
      <span>{label}</span>
    </label>
  );
}

function CompactStat({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-lg border bg-muted/25 p-3">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 break-words text-lg font-semibold leading-none">{value}</div>
    </div>
  );
}

function NumberField({ label, value, onChange }: { label: string; value: number; onChange: (value: number) => void }) {
  return (
    <Field label={label}>
      <Input
        type="number"
        inputMode="numeric"
        min={0}
        value={Number.isFinite(value) ? value : 0}
        onChange={(event) => onChange(nonNegativeNumber(event.target.value))}
      />
    </Field>
  );
}

function PercentInput({ label, value, onChange }: { label: string; value: number; onChange: (value: number) => void }) {
  return (
    <Field label={label}>
      <Input
        type="number"
        inputMode="decimal"
        min={0}
        max={100}
        value={Math.round((Number.isFinite(value) ? value : 0) * 100)}
        onChange={(event) => onChange(Math.max(0, Math.min(100, nonNegativeNumber(event.target.value))) / 100)}
      />
    </Field>
  );
}

function AccountTestBadge({ account }: { account: ClaudeCodeAccount }) {
  const status = (account.test_status || "").toLowerCase();
  if (status === "ok" || status === "healthy" || status === "success") {
    return <Badge tone="success">测试通过</Badge>;
  }
  if (status === "failed" || status === "error" || (account.consecutive_failures || 0) > 0) {
    return <Badge tone="danger">测试异常</Badge>;
  }
  return <Badge tone="neutral">未测试</Badge>;
}

function nonNegativeNumber(value: string) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return 0;
  }
  return Math.round(parsed);
}

function formatRatioPercent(value?: number) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "0%";
  }
  const normalized = value > 1 ? value : value * 100;
  return `${Math.round(normalized)}%`;
}

function realTotalTokens(value?: {
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_creation_tokens?: number;
}) {
  if (!value) {
    return 0;
  }
  return (value.input_tokens || 0) + (value.output_tokens || 0) + (value.cache_read_tokens || 0) + (value.cache_creation_tokens || 0);
}

function formatTokenCompact(tokens: number) {
  if (!Number.isFinite(tokens) || tokens <= 0) {
    return "0";
  }
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 0 }).format(tokens);
}

function formatTokenLarge(tokens: number) {
  if (!Number.isFinite(tokens) || tokens <= 0) {
    return "0M";
  }
  const millions = tokens / 1_000_000;
  if (millions >= 1000) {
    return `${formatShortNumber(millions / 1000)}B`;
  }
  return `${formatShortNumber(millions)}M`;
}

function formatShortNumber(value: number) {
  if (value >= 100) {
    return `${Math.round(value)}`;
  }
  if (value >= 10) {
    return value.toFixed(1).replace(/\.0$/, "");
  }
  return value.toFixed(2).replace(/\.00$/, "").replace(/0$/, "");
}

function compactModelName(model: string) {
  return model.replace(/^claude-/, "").replace(/-\d{8}$/, "");
}

function modelStatusText(status: string) {
  switch (String(status || "").toLowerCase()) {
    case "healthy":
      return "正常";
    case "rate_limited":
      return "限速";
    case "overloaded":
      return "过载";
    case "unhealthy":
      return "异常";
    default:
      return "未知";
  }
}

function modelStatusTone(status: string): "success" | "warning" | "danger" | "neutral" {
  switch (String(status || "").toLowerCase()) {
    case "healthy":
      return "success";
    case "rate_limited":
    case "overloaded":
      return "warning";
    case "unhealthy":
      return "danger";
    default:
      return "neutral";
  }
}

function headersToText(headers: Record<string, string>) {
  return Object.entries(headers)
    .map(([key, value]) => `${key}: ${value}`)
    .join("\n");
}

function textToHeaders(text: string) {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const trimmed = line.trim();
    if (!trimmed) {
      continue;
    }
    const idx = trimmed.indexOf(":");
    if (idx <= 0) {
      continue;
    }
    const key = trimmed.slice(0, idx).trim();
    const value = trimmed.slice(idx + 1).trim();
    if (key && value) {
      out[key] = value;
    }
  }
  return out;
}

function parseLineList(text: string) {
  return text
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean)
    .filter((item, index, items) => items.findIndex((candidate) => candidate.toLowerCase() === item.toLowerCase()) === index);
}

function defaultPoolEffectiveConfig(): ClaudeCodePoolEffectiveConfig {
  return {
    enabled: true,
    pure_mode: true,
    cloak: {
      mode: "auto",
      strict_mode: false,
      sensitive_words: []
    },
    usage: {
      clean_input_tokens: false,
      system_prompt_overhead_tokens: 1909,
      profile_fingerprint: ""
    },
    log: {
      enabled: true,
      level: "info",
      dir: "acc-pool-logs",
      max_size_mb: 50,
      max_backups: 3,
      redact: true
    },
    virtual_cache: {
      enabled: false,
      mode: "natural",
      hit_rate: 0.95,
      target_cache_reuse_ratio: 0.9,
      min_cache_tokens: 0,
      max_cache_tokens: 0,
      uncached_input_tokens: 0,
      context_shrink_reset_ratio: 0.7,
      min_creation_tokens: 0,
      max_creation_tokens: 0
    },
    routing: {
      per_account_rpm: 6,
      per_account_concurrency: 1,
      max_switches: 2,
      switch_delay_ms: 1000,
      rate_limit_cooldown_ms: 300000,
      rate_limit_max_cooldown_ms: 7200000,
      overload_cooldown_ms: 120000,
      overload_max_cooldown_ms: 1800000,
      same_account_retry_429: 0,
      same_account_retry_529: 1,
      same_account_retry_delay_ms: 3000,
      cache_affinity_enabled: true,
      cache_affinity_auto: true,
      cache_affinity_auto_profile: "cost",
      account_capacity_profile: "custom",
      cache_affinity_min_cache_tokens: 4096,
      cache_affinity_lanes: 1,
      cache_affinity_max_lanes: 2,
      cache_affinity_wait_ms: 250,
      cache_affinity_ttl_ms: 300000
    }
  };
}

function toRawPoolConfig(config: ClaudeCodePoolEffectiveConfig): ClaudeCodePoolRawConfig {
  return {
    enabled: config.enabled,
    pure_mode: config.pure_mode,
    cloak: {
      mode: config.cloak.mode,
      "strict-mode": config.cloak.strict_mode,
      "sensitive-words": config.cloak.sensitive_words
    },
    usage: {
      clean_input_tokens: config.usage.clean_input_tokens,
      system_prompt_overhead_tokens: config.usage.system_prompt_overhead_tokens
    },
    log: {
      enabled: config.log.enabled,
      level: config.log.level,
      dir: config.log.dir,
      max_size_mb: config.log.max_size_mb,
      max_backups: config.log.max_backups,
      redact: config.log.redact
    },
    virtual_cache: {
      enabled: config.virtual_cache.enabled,
      mode: config.virtual_cache.mode,
      "hit-rate": config.virtual_cache.hit_rate,
      "target-cache-reuse-ratio": config.virtual_cache.target_cache_reuse_ratio,
      "min-cache-tokens": config.virtual_cache.min_cache_tokens,
      "max-cache-tokens": config.virtual_cache.max_cache_tokens,
      "uncached-input-tokens": config.virtual_cache.uncached_input_tokens,
      "context-shrink-reset-ratio": config.virtual_cache.context_shrink_reset_ratio,
      "min-creation-tokens": config.virtual_cache.min_creation_tokens,
      "max-creation-tokens": config.virtual_cache.max_creation_tokens
    },
    routing: {
      "per-account-rpm": config.routing.per_account_rpm,
      "per-account-concurrency": config.routing.per_account_concurrency,
      "max-switches": config.routing.max_switches,
      "switch-delay-ms": config.routing.switch_delay_ms,
      "rate-limit-cooldown-ms": config.routing.rate_limit_cooldown_ms,
      "rate-limit-max-cooldown-ms": config.routing.rate_limit_max_cooldown_ms,
      "overload-cooldown-ms": config.routing.overload_cooldown_ms,
      "overload-max-cooldown-ms": config.routing.overload_max_cooldown_ms,
      "same-account-retry-429": config.routing.same_account_retry_429,
      "same-account-retry-529": config.routing.same_account_retry_529,
      "same-account-retry-delay-ms": config.routing.same_account_retry_delay_ms,
      "cache-affinity-enabled": config.routing.cache_affinity_enabled,
      "cache-affinity-auto": config.routing.cache_affinity_auto,
      "cache-affinity-auto-profile": config.routing.cache_affinity_auto_profile,
      "account-capacity-profile": config.routing.account_capacity_profile,
      "cache-affinity-min-cache-tokens": config.routing.cache_affinity_min_cache_tokens,
      "cache-affinity-lanes": config.routing.cache_affinity_lanes,
      "cache-affinity-max-lanes": config.routing.cache_affinity_max_lanes,
      "cache-affinity-wait-ms": config.routing.cache_affinity_wait_ms,
      "cache-affinity-ttl-ms": config.routing.cache_affinity_ttl_ms
    }
  };
}

function parseSensitiveWords(value: string) {
  return value
    .split(/[\n,]/)
    .map((item) => item.trim())
    .filter(Boolean)
    .filter((item, index, items) => items.findIndex((candidate) => candidate.toLowerCase() === item.toLowerCase()) === index);
}

function ProxyView({
  proxies,
  loading,
  onEdit,
  onToast,
  onDone
}: {
  proxies: ProxyResource[];
  loading: boolean;
  onEdit: (proxy: ProxyResource) => void;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
  onDone: () => Promise<void>;
}) {
  const [selectedIDs, setSelectedIDs] = useState<string[]>([]);
  const selectedSet = useMemo(() => new Set(selectedIDs), [selectedIDs]);
  const selectedCount = selectedIDs.length;
  useEffect(() => {
    setSelectedIDs((current) => current.filter((id) => proxies.some((proxy) => proxy.id === id)));
  }, [proxies]);
  const setProxySelected = (id: string, selected: boolean) => {
    setSelectedIDs((current) => {
      if (selected) {
        return current.includes(id) ? current : [...current, id];
      }
      return current.filter((item) => item !== id);
    });
  };
  const setAllSelected = (ids: string[], selected: boolean) => {
    setSelectedIDs((current) => {
      const next = new Set(current);
      for (const id of ids) {
        if (selected) {
          next.add(id);
        } else {
          next.delete(id);
        }
      }
      return Array.from(next);
    });
  };
  const clearSelection = () => setSelectedIDs([]);
  const proxyMutation = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) => api.updateProxy(id, { enabled }),
    onSuccess: async () => {
      await onDone();
      onToast("代理状态已更新");
    },
    onError: (error) => onToast(`更新失败：${errorMessage(error)}`, "danger")
  });
  const testMutation = useMutation({
    mutationFn: api.testProxy,
    onSuccess: async (data) => {
      await onDone();
      onToast(data.warning ? `测试完成但异常：${data.warning}` : "测试完成");
    },
    onError: (error) => onToast(`测试失败：${errorMessage(error)}`, "danger")
  });
  const unbindMutation = useMutation({
    mutationFn: api.unbindProxy,
    onSuccess: async () => {
      await onDone();
      onToast("代理已解绑");
    },
    onError: (error) => onToast(`解绑失败：${errorMessage(error)}`, "danger")
  });
  const deleteMutation = useMutation({
    mutationFn: api.deleteProxy,
    onSuccess: async () => {
      await onDone();
      onToast("代理已删除");
    },
    onError: (error) => onToast(`删除失败：${errorMessage(error)}`, "danger")
  });
  const batchMutation = useMutation({
    mutationFn: ({ action, ids }: { action: ProxyBatchAction; ids: string[] }) => api.batchProxies(action, ids),
    onSuccess: async (data) => {
      await onDone();
      if (data.failed > 0) {
        const firstError = data.errors?.[0]?.message;
        onToast(`批量操作完成：成功 ${data.ok}，失败 ${data.failed}${firstError ? `，首个错误：${firstError}` : ""}`, "danger");
      } else {
        onToast(`批量操作完成：成功 ${data.ok}`);
      }
      if (data.action === "delete") {
        clearSelection();
      }
    },
    onError: (error) => onToast(`批量操作失败：${errorMessage(error)}`, "danger")
  });

  const runBatch = (action: ProxyBatchAction) => {
    if (selectedCount === 0 || batchMutation.isPending) {
      return;
    }
    if (action === "delete" && !window.confirm(`确认删除选中的 ${selectedCount} 个代理？已绑定账号会自动解绑。`)) {
      return;
    }
    batchMutation.mutate({ action, ids: selectedIDs });
  };

  const columns = useMemo<ColumnDef<ProxyResource>[]>(
    () => [
      {
        id: "select",
        header: ({ table }) => {
          const pageIDs = table.getRowModel().rows.map((row) => row.original.id);
          const checked = pageIDs.length > 0 && pageIDs.every((id) => selectedSet.has(id));
          const indeterminate = pageIDs.some((id) => selectedSet.has(id)) && !checked;
          return (
            <input
              type="checkbox"
              aria-label="选择本组全部代理"
              checked={checked}
              ref={(node) => {
                if (node) {
                  node.indeterminate = indeterminate;
                }
              }}
              onChange={(event) => setAllSelected(pageIDs, event.target.checked)}
              className="h-4 w-4 rounded border-border"
            />
          );
        },
        cell: ({ row }) => (
          <input
            type="checkbox"
            aria-label={`选择代理 ${row.original.name}`}
            checked={selectedSet.has(row.original.id)}
            onChange={(event) => setProxySelected(row.original.id, event.target.checked)}
            className="h-4 w-4 rounded border-border"
          />
        )
      },
      {
        header: "名称",
        cell: ({ row }) => (
          <div className="max-w-64">
            <div className="break-words font-medium">{row.original.name}</div>
            <div className="break-all text-xs text-muted-foreground">{row.original.id}</div>
          </div>
        )
      },
      {
        header: "代理 URL",
        cell: ({ row }) => <span className="block max-w-72 break-all">{row.original.proxy_url_preview || row.original.proxy_url}</span>
      },
      {
        header: "出口 IP",
        accessorFn: (row) => row.exit_ip || "-"
      },
      {
        header: "状态",
        cell: ({ row }) => <HealthBadge status={row.original.health_status} enabled={row.original.enabled} />
      },
      {
        header: "延迟",
        cell: ({ row }) => `${row.original.latency_ms || 0} ms`
      },
      {
        header: "失败",
        accessorKey: "consecutive_failures"
      },
      {
        header: "绑定账号",
        cell: ({ row }) => row.original.bound_account_email || <span className="text-muted-foreground">空闲</span>
      },
      {
        header: "最后测试",
        cell: ({ row }) => formatTime(row.original.last_checked_at)
      },
      {
        header: "错误",
        cell: ({ row }) => <span className="block max-w-72 break-words text-muted-foreground">{row.original.last_error || "-"}</span>
      },
      {
        header: "操作",
        cell: ({ row }) => {
          const proxy = row.original;
          return (
            <div className="flex flex-wrap gap-2">
              <Button variant="outline" size="sm" onClick={() => testMutation.mutate(proxy.id)}>
                <Play className="h-3.5 w-3.5" />
                测试
              </Button>
              <Button variant="outline" size="sm" onClick={() => onEdit(proxy)}>
                <SlidersHorizontal className="h-3.5 w-3.5" />
                编辑
              </Button>
              <Button variant="outline" size="sm" onClick={() => proxyMutation.mutate({ id: proxy.id, enabled: !proxy.enabled })}>
                {proxy.enabled ? "禁用" : "启用"}
              </Button>
              <Button variant="outline" size="sm" onClick={() => unbindMutation.mutate(proxy.id)}>
                <Unlink className="h-3.5 w-3.5" />
                解绑
              </Button>
              <Button
                variant="destructive"
                size="sm"
                onClick={() => {
                  if (window.confirm("确认删除这个代理资源？已绑定账号会自动解绑。")) {
                    deleteMutation.mutate(proxy.id);
                  }
                }}
              >
                <Trash2 className="h-3.5 w-3.5" />
                删除
              </Button>
            </div>
          );
        }
      }
    ],
    [deleteMutation, onEdit, proxyMutation, selectedSet, testMutation, unbindMutation]
  );

  const grouped = useMemo(() => groupProxies(proxies), [proxies]);

  if (loading) {
    return <LoadingPanel />;
  }

  return (
    <div className="grid gap-5">
      <ProxyBatchToolbar
        selectedCount={selectedCount}
        pending={batchMutation.isPending}
        onRun={runBatch}
        onClear={clearSelection}
      />
      {grouped.map((group) => (
        <ProxyGroupCard
          key={group.key}
          title={group.title}
          description={group.description}
          items={group.items}
          columns={columns}
        />
      ))}
      {proxies.length === 0 ? <EmptyState title="暂无代理" description="新增代理后，健康检查 worker 会按配置周期自动测试。" /> : null}
    </div>
  );
}

function ProxyGroupCard({
  title,
  description,
  items,
  columns
}: {
  title: string;
  description: string;
  items: ProxyResource[];
  columns: ColumnDef<ProxyResource>[];
}) {
  const table = useReactTable({ data: items, columns, getCoreRowModel: getCoreRowModel() });
  if (items.length === 0) {
    return null;
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle>
          {title} <span className="text-sm font-normal text-muted-foreground">({items.length})</span>
        </CardTitle>
        <CardDescription>{description}</CardDescription>
      </CardHeader>
      <CardContent>
        <DataTable table={table} empty="暂无代理" />
      </CardContent>
    </Card>
  );
}

function ProxyBatchToolbar({
  selectedCount,
  pending,
  onRun,
  onClear
}: {
  selectedCount: number;
  pending: boolean;
  onRun: (action: ProxyBatchAction) => void;
  onClear: () => void;
}) {
  return (
    <Card>
      <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
        <div className="grid gap-1">
          <div className="text-sm font-medium">批量操作</div>
          <div className="text-xs text-muted-foreground">已选择 {selectedCount} 个代理</div>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => onRun("test")} disabled={selectedCount === 0 || pending}>
            {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
            批量测试
          </Button>
          <Button variant="outline" size="sm" onClick={() => onRun("enable")} disabled={selectedCount === 0 || pending}>
            启用
          </Button>
          <Button variant="outline" size="sm" onClick={() => onRun("disable")} disabled={selectedCount === 0 || pending}>
            禁用
          </Button>
          <Button variant="outline" size="sm" onClick={() => onRun("unbind")} disabled={selectedCount === 0 || pending}>
            <Unlink className="h-3.5 w-3.5" />
            解绑
          </Button>
          <Button variant="destructive" size="sm" onClick={() => onRun("delete")} disabled={selectedCount === 0 || pending}>
            <Trash2 className="h-3.5 w-3.5" />
            删除
          </Button>
          <Button variant="ghost" size="sm" onClick={onClear} disabled={selectedCount === 0 || pending}>
            清空选择
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function DataTable<T>({ table, empty }: { table: Table<T>; empty: string }) {
  return (
    <div className="table-scroll">
      <table className="data-table">
        <thead>
          {table.getHeaderGroups().map((headerGroup) => (
            <tr key={headerGroup.id}>
              {headerGroup.headers.map((header) => (
                <th key={header.id}>
                  {header.isPlaceholder ? null : flexRender(header.column.columnDef.header, header.getContext())}
                </th>
              ))}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.length ? (
            table.getRowModel().rows.map((row) => (
              <tr key={row.id}>
                {row.getVisibleCells().map((cell) => (
                  <td key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</td>
                ))}
              </tr>
            ))
          ) : (
            <tr>
              <td colSpan={table.getAllColumns().length} className="text-center text-muted-foreground">
                {empty}
              </td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  );
}

function ResourceModal({
  modal,
  available,
  onClose,
  onRefresh,
  onDone,
  onToast
}: {
  modal: ModalState;
  available: ProxyResource[];
  onClose: () => void;
  onRefresh: () => Promise<void>;
  onDone: (message: string) => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  return (
    <Dialog open={modal !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className={modal?.type === "oauth" ? "max-w-5xl" : modal?.type === "test-account" ? "max-w-4xl p-0" : undefined}>
        {modal?.type === "oauth" ? (
          <OAuthForm available={available} onDone={onDone} onToast={onToast} />
        ) : modal?.type === "proxy" ? (
          <ProxyForm proxy={modal.proxy} onDone={onDone} onToast={onToast} />
        ) : modal?.type === "import" ? (
          <ImportForm onDone={onDone} onToast={onToast} />
        ) : modal?.type === "bind" ? (
          <BindProxyForm account={modal.account} available={available} onDone={onDone} onToast={onToast} />
        ) : modal?.type === "test-account" ? (
          <AccountTestForm account={modal.account} models={modal.models} onRefresh={onRefresh} onClose={onClose} />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function AccountTestForm({
  account,
  models,
  onRefresh,
  onClose
}: {
  account: ClaudeCodeAccount;
  models: ClaudeCodeModel[];
  onRefresh: () => Promise<void>;
  onClose: () => void;
}) {
  const enabledModels = useMemo(() => models.filter((model) => model.enabled), [models]);
  const defaultModel = enabledModels[0]?.alias || enabledModels[0]?.name || "claude-3-5-haiku-latest";
  const [model, setModel] = useState(defaultModel);
  const [message, setMessage] = useState("hi");
  const [result, setResult] = useState<AccountTestResultState>({
    status: "idle",
    lines: ["选择模型后发送测试消息，响应会显示在这里。"]
  });

  useEffect(() => {
    setModel(defaultModel);
    setMessage("hi");
    setResult({
      status: "idle",
      lines: ["选择模型后发送测试消息，响应会显示在这里。"]
    });
  }, [account.id, defaultModel]);

  const mutation = useMutation({
    mutationFn: () => api.testAccount(account.id, { model, message }),
    onMutate: () => {
      setResult({
        status: "running",
        lines: [
          "连接 API 中",
          `开始测试账号：${account.email || account.auth_id}`,
          "账号类型：oauth",
          `使用模型：${model}`,
          `发送测试消息："${message}"`
        ]
      });
    },
    onSuccess: async (data) => {
      setResult({
        status: data.warning ? "warning" : "success",
        lines: [
          data.warning ? "测试完成但异常" : "已连接到 API",
          `开始测试账号：${account.email || account.auth_id}`,
          "账号类型：oauth",
          `使用模型：${model}`,
          `发送测试消息："${message}"`,
          data.warning ? `异常：${data.warning}` : "响应：",
          data.reply || "测试成功，但接口未返回回复文本。"
        ]
      });
      await onRefresh();
    },
    onError: async (error) => {
      setResult({
        status: "error",
        lines: [
          "测试失败",
          `开始测试账号：${account.email || account.auth_id}`,
          "账号类型：oauth",
          `使用模型：${model}`,
          `发送测试消息："${message}"`,
          `错误：${errorMessage(error)}`
        ]
      });
      await onRefresh();
    }
  });

  return (
    <div className="grid max-h-[calc(100dvh-2rem)] overflow-hidden">
      <DialogHeader className="border-b px-6 py-5 pr-14">
        <DialogTitle className="text-2xl">测试账号连接</DialogTitle>
      </DialogHeader>
      <form
        className="grid gap-5 overflow-y-auto px-6 py-5"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.mutate();
        }}
      >
        <div className="flex items-center justify-between gap-4 rounded-lg border bg-muted/25 p-4 max-[640px]:items-start">
          <div className="flex min-w-0 items-center gap-4">
            <div className="grid h-14 w-14 shrink-0 place-items-center rounded-lg bg-teal-600 text-white">
              <Play className="h-7 w-7" />
            </div>
            <div className="min-w-0">
              <div className="break-words text-lg font-semibold leading-6">{account.email || account.auth_id}</div>
              <div className="mt-1 flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
                <Badge tone="neutral">OAUTH</Badge>
                <span className="break-all">账号</span>
                {account.proxy ? <span className="break-all">固定出口：{proxyDisplay(account.proxy)}</span> : <span>未绑定代理</span>}
              </div>
            </div>
          </div>
          <Badge tone={account.enabled ? "success" : "danger"}>{account.enabled ? "active" : "disabled"}</Badge>
        </div>
        <Field label="测试模型">
          <Select value={model} onChange={(event) => setModel(event.target.value)}>
            {enabledModels.length ? (
              enabledModels.map((item) => {
                const value = item.alias || item.name;
                return (
                  <option key={item.id} value={value}>
                    {value}
                    {item.alias && item.alias !== item.name ? ` -> ${item.name}` : ""}
                  </option>
                );
              })
            ) : (
              <option value="claude-3-5-haiku-latest">claude-3-5-haiku-latest</option>
            )}
          </Select>
        </Field>
        <Field label="测试消息">
          <Textarea value={message} onChange={(event) => setMessage(event.target.value)} className="min-h-20" />
        </Field>
        <AccountTestConsole result={result} model={model} message={message} />
      </form>
      <div className="flex flex-wrap justify-end gap-3 border-t bg-card px-6 py-4">
        <Button type="button" variant="secondary" onClick={onClose}>
          关闭
        </Button>
        <Button
          type="button"
          onClick={() => mutation.mutate()}
          disabled={mutation.isPending || !model.trim() || !message.trim()}
          className="min-w-36 bg-teal-600 text-white hover:bg-teal-700"
        >
          {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
          {mutation.isPending ? "测试中..." : "开始测试"}
        </Button>
      </div>
    </div>
  );
}

type AccountTestResultState = {
  status: "idle" | "running" | "success" | "warning" | "error";
  lines: string[];
};

function AccountTestConsole({
  result,
  model,
  message
}: {
  result: AccountTestResultState;
  model: string;
  message: string;
}) {
  const statusTone =
    result.status === "success" ? "text-emerald-400" : result.status === "error" ? "text-red-400" : result.status === "warning" ? "text-amber-300" : "text-sky-300";
  return (
    <div className="grid gap-3 rounded-lg border border-slate-700 bg-slate-950 p-4 font-mono text-sm leading-7 text-slate-300 shadow-inner">
      <div className={cn("flex items-center gap-2", statusTone)}>
        {result.status === "running" ? <Loader2 className="h-4 w-4 animate-spin" /> : <RefreshCw className="h-4 w-4" />}
        <span>{result.status === "idle" ? "等待测试" : result.lines[0]}</span>
      </div>
      <div className="grid gap-0.5">
        {result.status === "idle" ? (
          <>
            <ConsoleLine label="使用模型" value={model || "-"} tone="cyan" />
            <ConsoleLine label="测试消息" value={`"${message || "hi"}"`} />
            <ConsoleLine label="响应" value="测试完成后会显示在这里。" tone="muted" />
          </>
        ) : (
          <div className="max-h-72 overflow-y-auto pr-1">
            {result.lines.slice(1).map((line, index) => {
              const [label, ...valueParts] = line.split("：");
              const value = valueParts.join("：");
              if (valueParts.length === 0) {
                return (
                  <div key={`${line}-${index}`} className={cn("whitespace-pre-wrap break-words", statusTone)}>
                    {line}
                  </div>
                );
              }
              return <ConsoleLine key={`${line}-${index}`} label={label} value={value} tone={consoleLineTone(label)} />;
            })}
          </div>
        )}
      </div>
    </div>
  );
}

function ConsoleLine({ label, value, tone = "default" }: { label: string; value: string; tone?: "default" | "cyan" | "green" | "amber" | "red" | "muted" }) {
  const toneClass =
    tone === "cyan"
      ? "text-cyan-300"
      : tone === "green"
        ? "text-emerald-300"
        : tone === "amber"
          ? "text-amber-300"
          : tone === "red"
            ? "text-red-300"
            : tone === "muted"
              ? "text-slate-400"
              : "text-slate-300";
  return (
    <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-2">
      <span className="text-slate-400">{label}：</span>
      <span className={cn("min-w-0 whitespace-pre-wrap break-words", toneClass)}>{value}</span>
    </div>
  );
}

function consoleLineTone(label: string): "default" | "cyan" | "green" | "amber" | "red" | "muted" {
  if (label.includes("已连接") || label.includes("响应")) {
    return "green";
  }
  if (label.includes("模型")) {
    return "cyan";
  }
  if (label.includes("异常")) {
    return "amber";
  }
  if (label.includes("错误")) {
    return "red";
  }
  if (label.includes("账号类型") || label.includes("发送测试消息")) {
    return "muted";
  }
  return "default";
}

function OAuthForm({
  available,
  onDone,
  onToast
}: {
  available: ProxyResource[];
  onDone: (message: string) => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [mode, setMode] = useState("");
  const [proxyID, setProxyID] = useState("");
  const [bindSameProxy, setBindSameProxy] = useState(true);
  const [selectorOpen, setSelectorOpen] = useState(false);
  const [authURL, setAuthURL] = useState("");
  const [authState, setAuthState] = useState("");
  const [callbackURL, setCallbackURL] = useState("");
  const [flowStatus, setFlowStatus] = useState("未生成授权链接");
  const selectedProxy = available.find((proxy) => proxy.id === proxyID);
  const effectiveMode = proxyID ? "id" : mode === "id" ? "" : mode;
  const proxyHint =
    effectiveMode === "auto"
      ? "后端会自动选择一个空闲且健康或未知的代理；若保持绑定选项，账号后续也会绑定同一个代理。"
      : effectiveMode === "id" && selectedProxy
      ? `本次登录和 token 换取会走 ${selectedProxy.name}，授权成功后可绑定到该账号。`
      : effectiveMode === "direct"
      ? "本次登录强制直连；后续账号可保持未绑定，或授权后手动绑定代理。"
      : "本次登录使用服务全局网络配置；如果没有全局代理，就是直连。";

  const generateMutation = useMutation({
    mutationFn: async () => {
      const params = new URLSearchParams({ pool: "claude-code", is_webui: "true" });
      if (effectiveMode) {
        params.set("login_proxy", effectiveMode);
      }
      if (proxyID) {
        params.set("proxy_resource_id", proxyID);
      }
      if (!bindSameProxy) {
        params.set("bind_proxy_resource_id", "none");
      }
      return api.authURL(params);
    },
    onSuccess: (data) => {
      setAuthURL(data.url);
      setAuthState(data.state);
      setCallbackURL("");
      setFlowStatus("授权链接已生成。授权后复制页面返回的 code#state，或完整回调 URL。");
    },
    onError: (error) => onToast(`生成授权链接失败：${errorMessage(error)}`, "danger")
  });
  const callbackMutation = useMutation({
    mutationFn: async () => {
      if (!callbackAlreadyReachedServer(callbackURL)) {
        await api.submitOAuthCallback(callbackURL, authState);
      }
      setFlowStatus("正在等待后端换取 token 并加入账号池。");
      await waitForOAuthComplete(authState);
    },
    onSuccess: async () => {
      await onDone("OAuth 认证完成，账号已加入 Claude Code 账号池。");
    },
    onError: (error) => {
      setFlowStatus("认证未完成，请检查 code#state 或回调 URL 后重试。");
      onToast(`提交回调失败：${errorMessage(error)}`, "danger");
    }
  });

  useEffect(() => {
    if (!authState || callbackMutation.isPending) {
      return;
    }
    let stopped = false;
    const poll = async () => {
      try {
        const status = await api.authStatus(authState);
        if (stopped) {
          return;
        }
        if (status.status === "ok") {
          setFlowStatus("OAuth 认证完成，账号已加入 Claude Code 账号池。");
          await onDone("OAuth 认证完成，账号已加入 Claude Code 账号池。");
          return;
        }
        if (status.status === "error") {
          setFlowStatus(status.error || "OAuth 认证失败");
          return;
        }
        window.setTimeout(poll, 1_500);
      } catch {
        if (!stopped) {
          window.setTimeout(poll, 3_000);
        }
      }
    };
    const timer = window.setTimeout(poll, 1_500);
    return () => {
      stopped = true;
      window.clearTimeout(timer);
    };
  }, [authState, callbackMutation.isPending, onDone]);

  const copyAuthURL = async () => {
    if (!authURL) {
      return;
    }
    try {
      await navigator.clipboard.writeText(authURL);
      onToast("授权链接已复制");
    } catch {
      onToast("复制失败，请手动选择链接复制。", "danger");
    }
  };

  const openAuthURL = () => {
    if (!authURL) {
      return;
    }
    window.open(authURL, "_blank", "noopener,noreferrer");
  };

  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <KeyRound className="h-5 w-5 text-primary" />
          Anthropic OAuth
        </DialogTitle>
        <DialogDescription>
          通过 OAuth 流程登录 Anthropic Claude，认证成功后自动保存并加入 Claude Code 账号池。
        </DialogDescription>
      </DialogHeader>
      <form
        className="grid gap-5"
        onSubmit={(event) => {
          event.preventDefault();
          generateMutation.mutate();
        }}
      >
        <div className="grid grid-cols-2 gap-4 max-[640px]:grid-cols-1">
          <Field label="登录出口">
            <Select
              value={mode}
              onChange={(event) => {
                setMode(event.target.value);
                if (event.target.value !== "id") {
                  setProxyID("");
                }
              }}
            >
              <option value="">直连或全局配置</option>
              <option value="direct">强制直连</option>
              <option value="auto">自动选择空闲代理</option>
              <option value="id">指定代理</option>
            </Select>
          </Field>
          <Field label="指定代理">
            <ProxySelectorButton
              selectedProxy={selectedProxy}
              availableCount={available.length}
              onOpen={() => setSelectorOpen(true)}
              onClear={() => {
                setProxyID("");
                if (mode === "id") {
                  setMode("");
                }
              }}
            />
          </Field>
        </div>
        <div className="rounded-lg border bg-muted/35 px-3 py-2 text-sm leading-6 text-muted-foreground">
          {proxyHint}
        </div>
        <label className="flex items-start gap-2 text-sm">
          <input
            type="checkbox"
            className="mt-1 h-4 w-4"
            checked={bindSameProxy}
            onChange={(event) => setBindSameProxy(event.target.checked)}
          />
          <span>授权成功后绑定同一个代理。直连登录时可先不绑定，之后在账号卡片中手动绑定。</span>
        </label>

        <section className="rounded-lg border border-dashed bg-card p-4">
          <div className="grid gap-2">
            <Label>授权链接</Label>
            {authURL ? (
              <div className="break-all rounded-lg bg-muted px-3 py-3 text-sm font-semibold leading-6 text-foreground">
                {authURL}
              </div>
            ) : (
              <div className="rounded-lg bg-muted px-3 py-3 text-sm text-muted-foreground">
                点击“开始 Anthropic 登录”后生成授权链接。
              </div>
            )}
          </div>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button type="submit" disabled={generateMutation.isPending}>
              {generateMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <KeyRound className="h-4 w-4" />}
              {authURL ? "重新生成链接" : "开始 Anthropic 登录"}
            </Button>
            <Button type="button" variant="outline" onClick={copyAuthURL} disabled={!authURL}>
              <Copy className="h-4 w-4" />
              复制链接
            </Button>
            <Button type="button" variant="outline" onClick={openAuthURL} disabled={!authURL}>
              <ExternalLink className="h-4 w-4" />
              打开链接
            </Button>
          </div>
        </section>

        <div className="grid gap-3">
          <Field label="回调 URL 或 code#state">
            <Input
              value={callbackURL}
              onChange={(event) => setCallbackURL(event.target.value)}
              placeholder="7B1...#H0qq... 或 https://platform.claude.com/oauth/code/callback?code=...&state=..."
              disabled={!authURL || callbackMutation.isPending}
            />
          </Field>
          <p className="text-sm leading-6 text-muted-foreground">
            优先粘贴授权页显示的 code#state；如果浏览器地址栏有完整回调 URL，也可以直接粘贴。
          </p>
          <div className="flex flex-wrap items-center gap-3">
            <Button
              type="button"
              variant="outline"
              disabled={!authURL || !callbackURL.trim() || callbackMutation.isPending}
              onClick={() => callbackMutation.mutate()}
            >
              {callbackMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
              提交认证结果
            </Button>
            <span className="max-w-full break-words rounded-lg border bg-card px-3 py-1.5 text-sm leading-6 text-muted-foreground">
              {flowStatus}
            </span>
          </div>
          {authState ? <p className="break-all text-xs text-muted-foreground">当前 OAuth state：{authState}</p> : null}
        </div>
      </form>
      <ProxySelectorDialog
        open={selectorOpen}
        title="选择登录代理"
        description="只展示未绑定的代理。选择后，OAuth 授权和 token 换取都会走这个出口。"
        available={available}
        selectedID={proxyID}
        onOpenChange={setSelectorOpen}
        onSelect={(proxy) => {
          setProxyID(proxy.id);
          setMode("id");
          setSelectorOpen(false);
        }}
      />
    </>
  );
}

async function waitForOAuthComplete(state: string) {
  if (!state) {
    return;
  }
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    await sleep(1_200);
    const status = await api.authStatus(state);
    if (status.status === "ok") {
      return;
    }
    if (status.status === "error") {
      throw new Error(status.error || "OAuth 认证失败");
    }
  }
  throw new Error("OAuth 回调已提交，但后端仍在处理中，请稍后刷新账号池。");
}

function callbackAlreadyReachedServer(rawURL: string) {
  try {
    const parsed = new URL(rawURL.trim());
    return parsed.pathname === "/anthropic/callback" && (parsed.hostname === "127.0.0.1" || parsed.hostname === "localhost");
  } catch {
    return false;
  }
}

function sleep(ms: number) {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function ProxySelectorButton({
  selectedProxy,
  availableCount,
  onOpen,
  onClear
}: {
  selectedProxy?: ProxyResource;
  availableCount: number;
  onOpen: () => void;
  onClear: () => void;
}) {
  return (
    <div className="grid gap-2">
      <div className="flex flex-wrap gap-2">
        <Button type="button" variant="outline" className="min-w-44 justify-start" onClick={onOpen} disabled={availableCount === 0}>
          <Search className="h-4 w-4" />
          {selectedProxy ? "更换代理" : availableCount > 0 ? "选择代理" : "暂无空闲代理"}
        </Button>
        {selectedProxy ? (
          <Button type="button" variant="ghost" onClick={onClear}>
            <Link2Off className="h-4 w-4" />
            清除
          </Button>
        ) : null}
      </div>
      <div className="min-h-11 rounded-lg border bg-card px-3 py-2 text-sm leading-6">
        {selectedProxy ? (
          <div className="grid gap-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="font-medium">{selectedProxy.name}</span>
              <HealthBadge status={selectedProxy.health_status} enabled={selectedProxy.enabled} />
              <span className="text-xs text-muted-foreground">{selectedProxy.latency_ms || 0} ms</span>
            </div>
            <div className="break-all text-xs text-muted-foreground">{proxyDisplay(selectedProxy)}</div>
          </div>
        ) : (
          <span className="text-muted-foreground">不指定代理</span>
        )}
      </div>
    </div>
  );
}

function ProxySelectorDialog({
  open,
  title,
  description,
  available,
  selectedID,
  onOpenChange,
  onSelect
}: {
  open: boolean;
  title: string;
  description: string;
  available: ProxyResource[];
  selectedID?: string;
  onOpenChange: (open: boolean) => void;
  onSelect: (proxy: ProxyResource) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        <ProxyChoiceTable available={available} selectedID={selectedID || ""} onSelect={onSelect} />
      </DialogContent>
    </Dialog>
  );
}

function ProxyChoiceTable({
  available,
  selectedID,
  onSelect,
  compact = false
}: {
  available: ProxyResource[];
  selectedID: string;
  onSelect: (proxy: ProxyResource) => void;
  compact?: boolean;
}) {
  const [query, setQuery] = useState("");
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) {
      return available;
    }
    return available.filter((proxy) =>
      [proxy.name, proxy.exit_ip, proxy.proxy_url_preview, proxy.proxy_url, proxy.note, ...(proxy.tags || [])]
        .filter(Boolean)
        .some((value) => String(value).toLowerCase().includes(needle))
    );
  }, [available, query]);
  return (
    <div className="grid gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="text-sm text-muted-foreground">空闲代理 {available.length} 个</div>
        <div className="relative w-full max-w-sm">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="搜索名称、出口 IP、标签"
            className="pl-9"
          />
        </div>
      </div>
      {filtered.length ? (
        <div className="table-scroll">
          <table className={cn("data-table", compact ? "min-w-[720px]" : "min-w-[820px]")}>
            <thead>
              <tr>
                <th>代理</th>
                <th>出口</th>
                <th>健康</th>
                <th>延迟</th>
                {!compact ? <th>最后测试</th> : null}
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((proxy) => {
                const selected = proxy.id === selectedID;
                return (
                  <tr key={proxy.id} className={selected ? "bg-primary/5" : undefined}>
                    <td>
                      <div className="max-w-64">
                        <div className="break-words font-medium">{proxy.name}</div>
                        <div className="break-all text-xs text-muted-foreground">{proxy.proxy_url_preview || proxy.proxy_url}</div>
                      </div>
                    </td>
                    <td className="break-words">{proxy.exit_ip || "-"}</td>
                    <td>
                      <HealthBadge status={proxy.health_status} enabled={proxy.enabled} />
                    </td>
                    <td>{proxy.latency_ms || 0} ms</td>
                    {!compact ? <td>{formatTime(proxy.last_checked_at)}</td> : null}
                    <td>
                      <Button type="button" size="sm" variant={selected ? "secondary" : "outline"} onClick={() => onSelect(proxy)}>
                        {selected ? <Check className="h-3.5 w-3.5" /> : <Network className="h-3.5 w-3.5" />}
                        {selected ? "已选择" : "选择"}
                      </Button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      ) : (
        <EmptyState title={available.length ? "没有匹配的代理" : "暂无空闲代理"} description="只会显示启用、未绑定、健康或未知的代理。" />
      )}
    </div>
  );
}

function ProxyForm({
  proxy,
  onDone,
  onToast
}: {
  proxy?: ProxyResource;
  onDone: (message: string) => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [payload, setPayload] = useState<ProxyPayload>({
    name: proxy?.name || "",
    proxy_url: proxy?.proxy_url || "",
    exit_ip: proxy?.exit_ip || "",
    enabled: proxy?.enabled ?? true,
    tags: proxy?.tags || [],
    note: proxy?.note || ""
  });
  const mutation = useMutation({
    mutationFn: () => (proxy ? api.updateProxy(proxy.id, payload) : api.createProxy(payload)),
    onSuccess: () => onDone(proxy ? "代理已保存" : "代理已新增"),
    onError: (error) => onToast(`${proxy ? "保存" : "新增"}失败：${errorMessage(error)}`, "danger")
  });
  const set = <K extends keyof ProxyPayload>(key: K, value: ProxyPayload[K]) => setPayload((prev) => ({ ...prev, [key]: value }));

  return (
    <>
      <DialogHeader>
        <DialogTitle>{proxy ? "编辑代理" : "新增代理"}</DialogTitle>
        <DialogDescription>代理 URL 支持 http、https、socks5、socks5h。</DialogDescription>
      </DialogHeader>
      <form
        className="grid gap-4"
        onSubmit={(event: FormEvent) => {
          event.preventDefault();
          mutation.mutate();
        }}
      >
        <div className="grid grid-cols-2 gap-4 max-[640px]:grid-cols-1">
          <Field label="名称">
            <Input value={payload.name || ""} onChange={(event) => set("name", event.target.value)} placeholder="美国静态出口 01" />
          </Field>
          <Field label="出口 IP 备注">
            <Input value={payload.exit_ip || ""} onChange={(event) => set("exit_ip", event.target.value)} placeholder="203.0.113.10" />
          </Field>
          <Field label="代理 URL" className="col-span-2 max-[640px]:col-span-1">
            <Input
              value={payload.proxy_url || ""}
              onChange={(event) => set("proxy_url", event.target.value)}
              placeholder="socks5://user:pass@host:1080"
              required
            />
          </Field>
          <Field label="标签">
            <Input
              value={(payload.tags || []).join(", ")}
              onChange={(event) =>
                set(
                  "tags",
                  event.target.value
                    .split(",")
                    .map((item) => item.trim())
                    .filter(Boolean)
                )
              }
              placeholder="us, max20"
            />
          </Field>
          <Field label="状态">
            <Select value={String(payload.enabled !== false)} onChange={(event) => set("enabled", event.target.value === "true")}>
              <option value="true">启用</option>
              <option value="false">禁用</option>
            </Select>
          </Field>
          <Field label="备注" className="col-span-2 max-[640px]:col-span-1">
            <Textarea value={payload.note || ""} onChange={(event) => set("note", event.target.value)} />
          </Field>
        </div>
        <div className="flex justify-end">
          <Button type="submit" disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
            保存
          </Button>
        </div>
      </form>
    </>
  );
}

function ImportForm({
  onDone,
  onToast
}: {
  onDone: (message: string) => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [text, setText] = useState("");
  const mutation = useMutation({
    mutationFn: () => api.importProxies(text),
    onSuccess: (data) => onDone(`导入完成：新增 ${data.created || 0}，跳过 ${data.skipped || 0}`),
    onError: (error) => onToast(`导入失败：${errorMessage(error)}`, "danger")
  });
  return (
    <>
      <DialogHeader>
        <DialogTitle>批量导入代理</DialogTitle>
        <DialogDescription>每行一个代理 URL，或使用 name|proxy-url|exit-ip|tag1,tag2|note。</DialogDescription>
      </DialogHeader>
      <form
        className="grid gap-4"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.mutate();
        }}
      >
        <Field label="导入内容">
          <Textarea value={text} onChange={(event) => setText(event.target.value)} placeholder="socks5://user:pass@host:1080" required />
        </Field>
        <div className="flex justify-end">
          <Button type="submit" disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CopyPlus className="h-4 w-4" />}
            导入
          </Button>
        </div>
      </form>
    </>
  );
}

function BindProxyForm({
  account,
  available,
  onDone,
  onToast
}: {
  account: ClaudeCodeAccount;
  available: ProxyResource[];
  onDone: (message: string) => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [proxyID, setProxyID] = useState(available[0]?.id || "");
  const selectedProxy = available.find((proxy) => proxy.id === proxyID);
  const mutation = useMutation({
    mutationFn: () => api.bindAccountProxy(account.id, proxyID),
    onSuccess: () => onDone("绑定已更新"),
    onError: (error) => onToast(`绑定失败：${errorMessage(error)}`, "danger")
  });
  return (
    <>
      <DialogHeader>
        <DialogTitle>绑定代理</DialogTitle>
        <DialogDescription>只展示启用、空闲、健康或未知的代理；已绑定代理不可选。</DialogDescription>
      </DialogHeader>
      <form
        className="grid gap-4"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.mutate();
        }}
      >
        <Field label="账号">
          <Input value={account.email || account.auth_id} readOnly />
        </Field>
        <div className="grid gap-2">
          <Label>选择代理</Label>
          <ProxyChoiceTable available={available} selectedID={proxyID} onSelect={(proxy) => setProxyID(proxy.id)} compact />
          {selectedProxy ? (
            <div className="rounded-lg border bg-muted/35 px-3 py-2 text-sm leading-6 text-muted-foreground">
              将绑定到：<span className="font-medium text-foreground">{selectedProxy.name}</span> · {proxyDisplay(selectedProxy)}
            </div>
          ) : null}
        </div>
        <div className="flex justify-end">
          <Button type="submit" disabled={mutation.isPending || !proxyID}>
            {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Network className="h-4 w-4" />}
            绑定
          </Button>
        </div>
      </form>
    </>
  );
}

function Field({ label, className, htmlFor, children }: { label: string; className?: string; htmlFor?: string; children: ReactNode }) {
  return (
    <div className={cn("grid gap-2", className)}>
      <Label htmlFor={htmlFor}>{label}</Label>
      {children}
    </div>
  );
}

function AccountStatusBadge({ account, runtime }: Pick<AccountRow, "account" | "runtime">) {
  if (!account.enabled) {
    return <Badge tone="danger">已禁用</Badge>;
  }
  const status = runtime?.status || "active";
  if (status === "active") {
    return <Badge tone="success">可用</Badge>;
  }
  if (status === "disabled") {
    return <Badge tone="danger">已禁用</Badge>;
  }
  return <Badge tone="warning">{status}</Badge>;
}

function HealthBadge({ status, enabled }: { status: string; enabled: boolean }) {
  const text = healthText(status, enabled);
  if (text === "健康") {
    return <Badge tone="success">健康</Badge>;
  }
  if (text === "异常" || text === "已禁用") {
    return <Badge tone="danger">{text}</Badge>;
  }
  return <Badge tone="warning">未知</Badge>;
}

function QuotaSummary({ quota }: { quota?: AccountQuota }) {
  if (!quota?.checked_at) {
    return <span className="text-muted-foreground">未检测</span>;
  }
  if (quota.status === "error") {
    return <span className="block max-w-56 break-words text-red-700">{quota.last_error || "异常"}</span>;
  }
  const primary = quota.windows?.[0];
  if (!primary) {
    return <span className="text-muted-foreground">无数据</span>;
  }
  return (
    <div className="grid min-w-40 gap-1">
      <div className="flex items-center justify-between gap-2 text-sm">
        <span>{primary.name}</span>
        <span className="tabular-nums">{formatPercent(primary.remain_percent)} 可用</span>
      </div>
      <Progress value={primary.remain_percent} />
      <span className="text-xs text-muted-foreground">{formatTime(quota.checked_at)}</span>
    </div>
  );
}

function QuotaPanel({ quota, onRefresh, refreshing }: { quota?: AccountQuota; onRefresh: () => void; refreshing: boolean }) {
  const windows = quota?.windows || [];
  return (
    <div className="grid gap-3 rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2 text-sm font-medium">
          <Database className="h-4 w-4 text-muted-foreground" />
          额度
          {quota?.status === "error" ? <Badge tone="danger">异常</Badge> : quota?.checked_at ? <Badge tone="info">已检测</Badge> : <Badge tone="warning">未检测</Badge>}
        </div>
        <Button variant="outline" size="sm" onClick={onRefresh} disabled={refreshing}>
          <RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} />
          刷新额度
        </Button>
      </div>
      {quota?.status === "error" ? (
        <div className="break-words rounded-md border border-red-200 bg-red-50 px-3 py-2 text-xs leading-5 text-red-800">
          {quota.last_error || "额度检测失败"}
        </div>
      ) : windows.length ? (
        <div className="grid gap-2">
          {windows.map((window) => (
            <div key={window.key || window.name} className="grid gap-1.5">
              <div className="flex items-center justify-between gap-3 text-xs">
                <span className="font-medium">{window.name || window.key}</span>
                <span className="tabular-nums text-muted-foreground">
                  剩余 {formatPercent(window.remain_percent)}
                  {window.resets_at ? ` · 重置 ${formatTime(window.resets_at)}` : ""}
                </span>
              </div>
              <Progress value={window.remain_percent} />
              {window.monthly_limit !== undefined || window.used_credits !== undefined ? (
                <div className="text-xs text-muted-foreground">
                  已用 {formatNumber(window.used_credits)} / {formatNumber(window.monthly_limit)}
                </div>
              ) : null}
            </div>
          ))}
        </div>
      ) : (
        <div className="text-xs leading-5 text-muted-foreground">还没有额度快照。</div>
      )}
      {quota?.checked_at ? <div className="text-xs text-muted-foreground">最近刷新 {formatTime(quota.checked_at)}</div> : null}
    </div>
  );
}

function LoadingPanel() {
  return (
    <Card>
      <CardContent className="flex min-h-48 items-center justify-center gap-2 p-8 text-muted-foreground">
        <Loader2 className="h-5 w-5 animate-spin" />
        正在加载
      </CardContent>
    </Card>
  );
}

function EmptyState({ title, description }: { title: string; description: string }) {
  return (
    <div className="grid place-items-center rounded-lg border border-dashed bg-muted/30 p-10 text-center">
      <div className="grid max-w-md gap-2">
        <strong>{title}</strong>
        <p className="text-sm leading-6 text-muted-foreground">{description}</p>
      </div>
    </div>
  );
}

function groupProxies(proxies: ProxyResource[]) {
  const groups = [
    { key: "healthy", title: "健康代理", description: "可优先绑定到账号池。", items: [] as ProxyResource[] },
    { key: "unknown", title: "未知代理", description: "未完成检测或连续失败未达阈值。", items: [] as ProxyResource[] },
    { key: "unhealthy", title: "异常代理", description: "不进入账号绑定选择器，已绑定账号会提示风险。", items: [] as ProxyResource[] },
    { key: "disabled", title: "已禁用代理", description: "不会参与健康检查和账号选择。", items: [] as ProxyResource[] }
  ];
  for (const proxy of proxies) {
    const text = healthText(proxy.health_status, proxy.enabled);
    if (text === "健康") {
      groups[0].items.push(proxy);
    } else if (text === "异常") {
      groups[2].items.push(proxy);
    } else if (text === "已禁用") {
      groups[3].items.push(proxy);
    } else {
      groups[1].items.push(proxy);
    }
  }
  return groups;
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

function shortText(value?: string, length = 12) {
  const text = (value || "").trim();
  if (!text) {
    return "-";
  }
  if (text.length <= length) {
    return text;
  }
  return `${text.slice(0, length)}...`;
}

function snapshotStatusText(status?: string) {
  switch ((status || "").trim()) {
    case "promoted":
      return "已标记";
    case "fetched":
      return "已拉取";
    case "failed":
      return "异常";
    default:
      return status || "未知";
  }
}

function formatPercent(value?: number) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "-";
  }
  return `${Math.round(value * 10) / 10}%`;
}

function formatNumber(value?: number) {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return "-";
  }
  return new Intl.NumberFormat("zh-CN", { maximumFractionDigits: 2 }).format(value);
}

function formatMinute(value?: string) {
  if (!value) {
    return "-";
  }
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "-";
  }
  return date.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false
  });
}

function availabilityStatusForCount(requestCount: number, successCount: number) {
  if (!requestCount) {
    return "none";
  }
  const rate = (successCount * 100) / requestCount;
  if (rate >= 90) {
    return "healthy";
  }
  if (rate >= 10) {
    return "degraded";
  }
  return "unhealthy";
}

function availabilityTone(status: string): {
  label: string;
  badgeTone: "success" | "warning" | "danger" | "neutral";
  barClass: string;
  textClass: string;
} {
  if (status === "healthy") {
    return {
      label: "健康",
      badgeTone: "success",
      barClass: "bg-emerald-500",
      textClass: "text-emerald-700"
    };
  }
  if (status === "degraded") {
    return {
      label: "波动",
      badgeTone: "warning",
      barClass: "bg-amber-500",
      textClass: "text-amber-700"
    };
  }
  if (status === "unhealthy") {
    return {
      label: "异常",
      badgeTone: "danger",
      barClass: "bg-red-500",
      textClass: "text-red-700"
    };
  }
  return {
    label: "暂无请求",
    badgeTone: "neutral",
    barClass: "bg-muted",
    textClass: "text-muted-foreground"
  };
}

function parseClaudeCodeIdentity(raw?: string) {
  const value = (raw || "").trim();
  if (!value) {
    return { deviceId: "", accountUUID: "" };
  }
  const match = value.match(/^user_([a-fA-F0-9]{64})_account_([0-9a-fA-F-]*)_session_([0-9a-fA-F-]+)$/);
  if (!match) {
    return { deviceId: value, accountUUID: "" };
  }
  return {
    deviceId: match[1],
    accountUUID: match[2] || ""
  };
}

export default App;
