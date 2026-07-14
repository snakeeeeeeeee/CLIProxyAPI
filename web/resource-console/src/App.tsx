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
  ArrowRightLeft,
  ArrowRight,
  Cable,
  Check,
  CheckCircle2,
  CircleHelp,
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
  LayoutGrid,
  Link2Off,
  List,
  Loader2,
  LogIn,
  LogOut,
  MoreHorizontal,
  Network,
  Play,
  Plus,
  RefreshCw,
  Search,
  Settings2,
  ShieldAlert,
  Trash2,
  Unlink,
  UserRoundCog,
  UsersRound,
  X
} from "lucide-react";
import { FormEvent, ReactNode, RefObject, UIEvent, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  AccountRow,
  AccountAvailabilitySummary,
  AccountBatchAction,
  AccountCapacity,
  AccountModelStatus,
  AccountQuota,
  QuotaWindowState,
  ClaudeCodeAccount,
  ClaudeCodeProfileResponse,
  ClaudeCodeProfileSnapshot,
  ClaudeCodeModel,
  ClaudeCodeModelPayload,
  ClaudeCodeAccountPool,
  ClaudeCodePoolAPIKey,
  ModelPriceVersion,
  UsageWindow,
  ClaudeCodePoolConfigResponse,
  ClaudeCodePoolEffectiveConfig,
  ClaudeCodePoolRawConfig,
  ClaudeCodePoolStats,
  AccountPoolLogEffectiveConfig,
  AccountPoolLogLine,
  AccountPoolLogRawConfig,
  AccountPoolDiagnostics,
  ProxyBatchAction,
  ProxyPayload,
  ProxyResource,
  RoutingEffectiveConfig,
  RoutingEvent,
  UsageSummary,
  UsageCalibrationResponse,
  api,
  getManagementKey,
  isManagementAuthError,
  managementEventsURL,
  setManagementKey
} from "./api";
import { AppShell, type ConsoleSection } from "./components/AppShell";
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
import { formatTime, formatUSD, healthText, proxyDisplay, successRate } from "./format";
import { cn } from "./lib/utils";
import { OverviewPage } from "./pages/OverviewPage";
import { PoolsPage } from "./pages/PoolsPage";
import { APIKeysPage } from "./pages/APIKeysPage";
import { ModelPricingPage } from "./pages/ModelPricingPage";
import { PoolDetailPage, type PoolDetailTab } from "./pages/PoolDetailPage";
import { PoolEventsPage } from "./pages/PoolEventsPage";
import { PoolStrategyPage } from "./pages/PoolStrategyPage";

type View = "overview" | "pools" | "pool" | "api-keys" | "proxies" | "models" | "settings";
type Route = View | "login";
const routingEventsPageSize = 20;
type ModalState =
  | { type: "oauth"; poolID: string }
  | { type: "proxy"; proxy?: ProxyResource }
  | { type: "import" }
  | { type: "bind"; account: ClaudeCodeAccount }
  | { type: "test-account"; account: ClaudeCodeAccount; models: ClaudeCodeModel[] }
  | null;

interface ToastState {
  message: string;
  tone: "default" | "danger";
}

interface RouteLocation {
  route: Route;
  poolID: string;
  poolTab: PoolDetailTab;
}

function routeFromHash(): RouteLocation {
  const normalized = window.location.hash.replace(/^#\/?/, "").split("?")[0].split("&")[0];
  const parts = normalized.split("/").filter(Boolean).map(decodeURIComponent);
  if (parts[0] === "pools" && parts[1]) {
    const tab = (["overview", "accounts", "api-keys", "events", "strategy"] as PoolDetailTab[]).includes(parts[2] as PoolDetailTab) ? parts[2] as PoolDetailTab : "overview";
    return { route: "pool", poolID: parts[1], poolTab: tab };
  }
  if (parts[0] === "accounts") return { route: "pool", poolID: "default", poolTab: "accounts" };
  if (["pools", "api-keys", "proxies", "models", "settings", "login"].includes(parts[0])) {
    return { route: parts[0] as Route, poolID: "", poolTab: "overview" };
  }
  return { route: "overview", poolID: "", poolTab: "overview" };
}

function routeHash(location: RouteLocation) {
  if (location.route === "overview") return "#/";
  if (location.route === "pool") return `#/pools/${encodeURIComponent(location.poolID || "default")}/${location.poolTab}`;
  return `#/${location.route}`;
}

function emitHashChange() {
  try {
    window.dispatchEvent(new HashChangeEvent("hashchange"));
  } catch {
    window.dispatchEvent(new Event("hashchange"));
  }
}

function replaceHashRoute(location: RouteLocation) {
  const hash = routeHash(location);
  window.history.replaceState(null, "", `${window.location.pathname}${window.location.search}${hash}`);
  emitHashChange();
}

function pushHashRoute(location: RouteLocation) {
  const hash = routeHash(location);
  if (window.location.hash === hash) {
    emitHashChange();
    return;
  }
  window.location.hash = hash;
}

function App() {
  const initialManagementKey = getManagementKey();
  const initialLocation = routeFromHash();
  const [location, setLocation] = useState<RouteLocation>(() => initialManagementKey.trim() ? initialLocation : { route: "login", poolID: "", poolTab: "overview" });
  const [afterLoginRoute, setAfterLoginRoute] = useState<RouteLocation>(() => initialLocation.route === "login" ? { route: "overview", poolID: "", poolTab: "overview" } : initialLocation);
  const [usageWindow, setUsageWindow] = useState<UsageWindow>("30d");
  const [routingEventsPage, setRoutingEventsPage] = useState(1);
  const [authRequired, setAuthRequired] = useState(false);
  const [managementKey, setManagementKeyState] = useState(initialManagementKey);
  const [modal, setModal] = useState<ModalState>(null);
  const [toast, setToast] = useState<ToastState | null>(null);
  const queryClient = useQueryClient();
  const route = location.route;
  const hasManagementKey = managementKey.trim().length > 0;
  const dataEnabled = hasManagementKey && !authRequired && route !== "login";
  const activeLocation = route === "login" ? afterLoginRoute : location;
  const view = activeLocation.route as View;
  const currentPoolID = view === "pool" ? (activeLocation.poolID || "default") : "";
  const poolListUsageWindow: UsageWindow = view === "overview" ? usageWindow : "all";
  const accountDataEnabled = dataEnabled && (view === "pool" || view === "settings");
  const accountListEnabled = accountDataEnabled || (dataEnabled && view === "models");

  const showToast = (message: string, tone: ToastState["tone"] = "default") => {
    setToast({ message, tone });
    window.clearTimeout((window as unknown as { __resourceToast?: number }).__resourceToast);
    (window as unknown as { __resourceToast?: number }).__resourceToast = window.setTimeout(() => setToast(null), 4200);
  };

  useEffect(() => {
    const syncRoute = () => setLocation(routeFromHash());
    syncRoute();
    window.addEventListener("hashchange", syncRoute);
    return () => window.removeEventListener("hashchange", syncRoute);
  }, []);

  useEffect(() => {
    if (!hasManagementKey) {
      if (route !== "login") {
        setAfterLoginRoute(activeLocation);
      }
      replaceHashRoute({ route: "login", poolID: "", poolTab: "overview" });
    }
  }, [hasManagementKey, route, activeLocation]);

  useEffect(() => {
    setRoutingEventsPage(1);
  }, [currentPoolID, usageWindow]);

  const configQuery = useQuery({ queryKey: ["resource-config"], queryFn: api.config, enabled: dataEnabled });
  const poolsQuery = useQuery({ queryKey: ["account-pools", poolListUsageWindow], queryFn: () => api.accountPools(poolListUsageWindow), enabled: dataEnabled });
  const accountsQuery = useQuery({ queryKey: ["accounts", currentPoolID, "30d"], queryFn: () => api.accounts(currentPoolID, "30d"), enabled: accountListEnabled });
  const apiKeysQuery = useQuery({ queryKey: ["pool-api-keys", currentPoolID, "all"], queryFn: () => api.poolAPIKeys(currentPoolID, "all"), enabled: dataEnabled });
  const proxiesQuery = useQuery({ queryKey: ["proxies"], queryFn: api.proxies, enabled: dataEnabled });
  const availableQuery = useQuery({ queryKey: ["available-proxies"], queryFn: api.availableProxies, enabled: dataEnabled });
  const poolConfigQuery = useQuery({ queryKey: ["account-pool-config"], queryFn: api.poolConfig, enabled: accountDataEnabled });
  const poolStrategyQuery = useQuery({
    queryKey: ["account-pool-strategy", currentPoolID],
    queryFn: () => api.accountPoolConfig(currentPoolID),
    enabled: dataEnabled && view === "pool" && activeLocation.poolTab === "strategy" && currentPoolID.length > 0
  });
  const poolProfileQuery = useQuery({ queryKey: ["account-pool-profile"], queryFn: api.poolProfile, enabled: dataEnabled && view === "settings" });
  const profileSnapshotsQuery = useQuery({
    queryKey: ["account-pool-profile-snapshots"],
    queryFn: api.profileSnapshots,
    enabled: dataEnabled && view === "settings"
  });
  const poolStatsQuery = useQuery({ queryKey: ["account-pool-stats", currentPoolID, usageWindow], queryFn: () => api.poolStats(currentPoolID, usageWindow), enabled: dataEnabled && (view === "overview" || view === "pool"), refetchInterval: 30_000 });
  const poolModelsQuery = useQuery({ queryKey: ["account-pool-models"], queryFn: api.poolModels, enabled: dataEnabled && (view === "pool" || view === "models" || view === "settings") });
  const modelPricesQuery = useQuery({ queryKey: ["model-prices"], queryFn: api.modelPrices, enabled: dataEnabled && (view === "models" || view === "pool") });
  const usageSummaryQuery = useQuery({ queryKey: ["account-pool-usage", currentPoolID, usageWindow], queryFn: () => api.usageSummary(currentPoolID, usageWindow), enabled: dataEnabled && (view === "overview" || view === "pool"), refetchInterval: 30_000 });
  const routingEventsQuery = useQuery({
    queryKey: ["account-pool-routing-events", currentPoolID, usageWindow, routingEventsPage],
    queryFn: () => api.routingEvents(currentPoolID, usageWindow, routingEventsPage, routingEventsPageSize),
    enabled: dataEnabled && view === "pool" && activeLocation.poolTab === "events",
    refetchInterval: 30_000
  });
  const logConfigQuery = useQuery({ queryKey: ["account-pool-log-config"], queryFn: api.poolLogConfig, enabled: dataEnabled && view === "settings" });
  const poolLogsQuery = useQuery({ queryKey: ["account-pool-logs"], queryFn: api.poolLogs, enabled: dataEnabled && view === "settings", refetchInterval: 30_000 });
  const usageCalibrationsQuery = useQuery({
    queryKey: ["account-pool-usage-calibrations"],
    queryFn: api.usageCalibrations,
    enabled: dataEnabled && view === "settings"
  });
  const diagnosticsQuery = useQuery({
    queryKey: ["account-pool-diagnostics"],
    queryFn: api.diagnostics,
    enabled: dataEnabled && view === "settings"
  });
  const authError = [
    configQuery.error,
    poolsQuery.error,
    accountsQuery.error,
    apiKeysQuery.error,
    proxiesQuery.error,
    availableQuery.error,
    poolConfigQuery.error,
    poolStrategyQuery.error,
    poolProfileQuery.error,
    profileSnapshotsQuery.error,
    poolStatsQuery.error,
    poolModelsQuery.error,
    modelPricesQuery.error,
    usageSummaryQuery.error,
    routingEventsQuery.error,
    logConfigQuery.error,
    poolLogsQuery.error,
    usageCalibrationsQuery.error,
    diagnosticsQuery.error
  ].some(isManagementAuthError);

  useEffect(() => {
    if (!authError) {
      return;
    }
    setAuthRequired(true);
    setManagementKey("");
    setManagementKeyState("");
    if (route !== "login") {
      setAfterLoginRoute(activeLocation);
      replaceHashRoute({ route: "login", poolID: "", poolTab: "overview" });
    }
  }, [authError, route, activeLocation]);

  const invalidateAll = async () => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["resource-config"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pools"] }),
      queryClient.invalidateQueries({ queryKey: ["accounts"] }),
      queryClient.invalidateQueries({ queryKey: ["pool-api-keys"] }),
      queryClient.invalidateQueries({ queryKey: ["proxies"] }),
      queryClient.invalidateQueries({ queryKey: ["available-proxies"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-config"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-strategy"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-profile"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-profile-snapshots"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-stats"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-models"] }),
      queryClient.invalidateQueries({ queryKey: ["model-prices"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-usage"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-routing-events"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-log-config"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-logs"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-diagnostics"] })
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
    const onProxyChanged = () => invalidate(["resource-config", "proxies", "available-proxies", "accounts", "account-pool-diagnostics"]);
    const onAccountChanged = () =>
      invalidate(["resource-config", "accounts", "proxies", "available-proxies", "account-pool-stats", "account-pool-usage", "account-pool-routing-events", "account-pool-logs", "account-pool-diagnostics"]);
    const onConfigChanged = () =>
      invalidate(["resource-config", "account-pool-config", "account-pool-profile", "account-pool-profile-snapshots", "account-pool-stats", "account-pool-log-config", "account-pool-diagnostics"]);
    const onStatsChanged = () => invalidate(["account-pool-stats", "account-pool-usage", "account-pool-routing-events", "account-pool-logs", "accounts"]);
    const onModelChanged = () => invalidate(["account-pool-models"]);
    const onPoolChanged = () => invalidate(["account-pools", "accounts", "pool-api-keys", "account-pool-stats", "account-pool-usage", "account-pool-strategy", "account-pool-diagnostics"]);
    const onAPIKeyChanged = () => invalidate(["account-pools", "pool-api-keys", "account-pool-usage"]);
    const onPricingChanged = () => invalidate(["model-prices", "account-pool-models", "account-pools", "accounts", "account-pool-stats", "account-pool-usage"]);
    const onSessionKeyJobChanged = () =>
      invalidate(["session-key-job", "resource-config", "accounts", "proxies", "available-proxies", "account-pool-stats"]);

    source.addEventListener("proxy_changed", onProxyChanged);
    source.addEventListener("account_changed", onAccountChanged);
    source.addEventListener("config_changed", onConfigChanged);
    source.addEventListener("stats_changed", onStatsChanged);
    source.addEventListener("model_changed", onModelChanged);
    source.addEventListener("pool_changed", onPoolChanged);
    source.addEventListener("api_key_changed", onAPIKeyChanged);
    source.addEventListener("pricing_changed", onPricingChanged);
    source.addEventListener("session_key_job_changed", onSessionKeyJobChanged);

    source.onerror = () => {
      // EventSource reconnects automatically; polling remains as a fallback.
    };

    return () => {
      source.removeEventListener("proxy_changed", onProxyChanged);
      source.removeEventListener("account_changed", onAccountChanged);
      source.removeEventListener("config_changed", onConfigChanged);
      source.removeEventListener("stats_changed", onStatsChanged);
      source.removeEventListener("model_changed", onModelChanged);
      source.removeEventListener("pool_changed", onPoolChanged);
      source.removeEventListener("api_key_changed", onAPIKeyChanged);
      source.removeEventListener("pricing_changed", onPricingChanged);
      source.removeEventListener("session_key_job_changed", onSessionKeyJobChanged);
      source.close();
    };
  }, [dataEnabled, queryClient]);

  const accountPoolLoading =
    accountDataEnabled &&
    (poolConfigQuery.isLoading || poolProfileQuery.isLoading || profileSnapshotsQuery.isLoading || poolStatsQuery.isLoading || poolModelsQuery.isLoading);
  const loading = configQuery.isLoading || poolsQuery.isLoading || accountsQuery.isLoading || apiKeysQuery.isLoading || proxiesQuery.isLoading || availableQuery.isLoading;
  const pools = poolsQuery.data?.items || [];
  const accounts = accountsQuery.data?.items || [];
  const poolAPIKeys = apiKeysQuery.data?.items || [];
  const proxies = proxiesQuery.data?.items || [];
  const available = availableQuery.data?.items || [];
  const summary = configQuery.data?.summary;
  const poolConfig = poolConfigQuery.data;
  const poolProfile = poolProfileQuery.data;
  const profileSnapshots = profileSnapshotsQuery.data?.items || [];
  const poolStats = poolStatsQuery.data?.stats;
  const poolModels = poolModelsQuery.data?.items || [];
  const modelPrices = modelPricesQuery.data?.current;
  const usageSummary = usageSummaryQuery.data?.summary;
  const routingEvents = routingEventsQuery.data?.items || [];
  const routingEventsTotal = routingEventsQuery.data?.total || 0;
  const routingEventsPageCount = Math.max(1, Math.ceil(routingEventsTotal / routingEventsPageSize));
  const logConfig = logConfigQuery.data;
  const poolLogs = poolLogsQuery.data?.items || [];
  const usageCalibrations = usageCalibrationsQuery.data;
  const diagnostics = diagnosticsQuery.data;

  useEffect(() => {
    if (routingEventsQuery.data && routingEventsPage > routingEventsPageCount) {
      setRoutingEventsPage(routingEventsPageCount);
    }
  }, [routingEventsPage, routingEventsPageCount, routingEventsQuery.data]);

  const pageInfo: Record<View, [string, string]> = {
    overview: ["总览", "全部账号池的健康、用量与成本"],
    pools: ["账号池", "账号、API Key 与会话按池隔离"],
    pool: ["账号池详情", "当前账号池的运行状态与资源"],
    "api-keys": ["API Keys", "生成和管理绑定账号池的访问凭证"],
    proxies: ["代理 IP", "维护账号登录和推理使用的固定出口"],
    models: ["模型与价格", "模型映射与版本化标准计价"],
    settings: ["系统设置", "全局调度、纯净模式、Profile 与日志"]
  };
  const [pageTitle, pageSubtitle] = pageInfo[view];

  const navigate = (nextRoute: Route, poolID = "", poolTab: PoolDetailTab = "overview") => {
    pushHashRoute({ route: nextRoute, poolID, poolTab });
  };

  const logout = async () => {
    setAfterLoginRoute(activeLocation);
    setAuthRequired(false);
    setModal(null);
    setManagementKey("");
    setManagementKeyState("");
    await queryClient.cancelQueries();
    queryClient.clear();
    replaceHashRoute({ route: "login", poolID: "", poolTab: "overview" });
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

  const currentPool = pools.find((pool) => pool.id === currentPoolID);
  const activeSection: ConsoleSection = view === "pool" ? "pools" : view;
  const notify = (message: string, danger = false) => showToast(message, danger ? "danger" : "default");
  const accountWorkspace = (forcedTab: "accounts" | "config") => (
    <AccountsView
      accounts={accounts}
      pools={pools}
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
      diagnostics={diagnostics}
      diagnosticsLoading={diagnosticsQuery.isLoading || diagnosticsQuery.isFetching}
      diagnosticsError={diagnosticsQuery.error}
      forcedTab={forcedTab}
      onBind={(account) => setModal({ type: "bind", account })}
      onTest={(account) => setModal({ type: "test-account", account, models: poolModels })}
      onToast={showToast}
      onDone={invalidateAll}
      onRefreshDiagnostics={async () => {
        const result = await diagnosticsQuery.refetch();
        if (result.error) {
          showToast(`诊断刷新失败：${errorMessage(result.error)}`, "danger");
          return;
        }
        showToast("运行诊断已刷新");
      }}
    />
  );
  const headerActions = view === "pool" && activeLocation.poolTab === "accounts" ? (
    <Button onClick={() => setModal({ type: "oauth", poolID: currentPoolID })}><KeyRound className="h-4 w-4" />新增账号</Button>
  ) : view === "proxies" ? (
    <><Button variant="outline" onClick={() => setModal({ type: "import" })}><CopyPlus className="h-4 w-4" />批量导入</Button><Button onClick={() => setModal({ type: "proxy" })}><Plus className="h-4 w-4" />新增代理</Button></>
  ) : undefined;

  return (
    <AppShell
      active={activeSection}
      title={pageTitle}
      subtitle={pageSubtitle}
      loading={loading}
      actions={headerActions}
      onNavigate={(section) => navigate(section)}
      onRefresh={refresh}
      onLogout={logout}
    >
      {view === "overview" ? (
        <OverviewPage pools={pools} stats={poolStats} window={usageWindow} loading={loading} onWindowChange={setUsageWindow} onOpenPool={(id) => navigate("pool", id)} />
      ) : view === "pools" ? (
        <PoolsPage pools={pools} onOpenPool={(id) => navigate("pool", id, "accounts")} onChanged={invalidateAll} notify={notify} />
      ) : view === "api-keys" ? (
        <APIKeysPage keys={poolAPIKeys} pools={pools} onChanged={invalidateAll} notify={notify} />
      ) : view === "pool" ? (
        <PoolDetailPage
          pool={currentPool}
          tab={activeLocation.poolTab}
          stats={poolStats}
          usage={usageSummary}
          window={usageWindow}
          onWindowChange={setUsageWindow}
          onBack={() => navigate("pools")}
          onTabChange={(tab) => navigate("pool", currentPoolID, tab)}
        >
          {activeLocation.poolTab === "accounts" ? accountWorkspace("accounts") : activeLocation.poolTab === "api-keys" ? (
            <APIKeysPage keys={poolAPIKeys} pools={pools} poolID={currentPoolID} onChanged={invalidateAll} notify={notify} />
          ) : activeLocation.poolTab === "events" ? (
            <PoolEventsPage
              events={routingEvents}
              total={routingEventsTotal}
              page={routingEventsPage}
              pageSize={routingEventsPageSize}
              loading={routingEventsQuery.isLoading || routingEventsQuery.isFetching}
              onPageChange={setRoutingEventsPage}
            />
          ) : activeLocation.poolTab === "strategy" ? (
            <PoolStrategyPage
              poolID={currentPoolID}
              config={poolStrategyQuery.data}
              loading={poolStrategyQuery.isLoading || poolStrategyQuery.isFetching}
              error={poolStrategyQuery.error}
              onChanged={invalidateAll}
              notify={notify}
            />
          ) : null}
        </PoolDetailPage>
      ) : view === "proxies" ? (
        <ProxyView proxies={proxies} loading={loading} onEdit={(proxy) => setModal({ type: "proxy", proxy })} onToast={showToast} onDone={invalidateAll} />
      ) : view === "models" ? (
        <ModelPricingPage accounts={accounts} models={poolModels} pricing={modelPrices} onChanged={invalidateAll} notify={notify} />
      ) : (
        accountWorkspace("config")
      )}

      <ResourceModal
        modal={modal}
        pools={pools}
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
    </AppShell>
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
  accounts,
  proxies,
  availableCount,
  loading
}: {
  summary?: { account_total: number; account_enabled: number; account_bound: number; proxy_total: number; proxy_healthy: number };
  accounts: AccountRow[];
  proxies: ProxyResource[];
  availableCount: number;
  loading: boolean;
}) {
  if (loading) {
    return <LoadingPanel />;
  }
  const expiringAccounts = accounts.filter(({ account }) => {
    if (!account.token_expires_at) {
      return false;
    }
    const expiresAt = new Date(account.token_expires_at).getTime();
    return Number.isFinite(expiresAt) && expiresAt > Date.now() && expiresAt - Date.now() < 24 * 60 * 60 * 1000;
  });
  const lowQuotaAccounts = accounts.filter(({ account }) =>
    (account.quota?.windows || []).some((window) => typeof window.remain_percent === "number" && window.remain_percent <= 10)
  );
  const unhealthyProxies = proxies.filter((proxy) => proxy.enabled && healthText(proxy.health_status, proxy.enabled) === "异常");
  const failedAccounts = accounts.filter(({ account }) => Boolean(account.last_error));
  const issues = [
    { label: "Token 24 小时内过期", count: expiringAccounts.length, href: "#/accounts", tone: "warning" as const },
    { label: "额度低于 10%", count: lowQuotaAccounts.length, href: "#/accounts", tone: "danger" as const },
    { label: "异常代理", count: unhealthyProxies.length, href: "#/proxies", tone: "danger" as const },
    { label: "账号最近有错误", count: failedAccounts.length, href: "#/accounts", tone: "warning" as const }
  ];
  return (
    <div className="grid gap-4">
      <section className="overview-band grid grid-cols-2 divide-x rounded-lg border bg-card max-[760px]:grid-cols-1 max-[760px]:divide-x-0 max-[760px]:divide-y">
        <ResourceOverview
          href="#/accounts"
          icon={UsersRound}
          title="Claude Code 账号池"
          status={`${summary?.account_enabled || 0} 个启用`}
          metrics={[
            ["账号", summary?.account_total || 0],
            ["已绑定代理", summary?.account_bound || 0],
            ["最近错误", failedAccounts.length]
          ]}
        />
        <ResourceOverview
          href="#/proxies"
          icon={Cable}
          title="代理 IP 池"
          status={`${summary?.proxy_healthy || 0} 个健康`}
          metrics={[
            ["代理", summary?.proxy_total || 0],
            ["空闲可用", availableCount],
            ["异常", unhealthyProxies.length]
          ]}
        />
      </section>

      <section className="rounded-lg border bg-card">
        <div className="flex items-center justify-between gap-3 border-b px-4 py-3">
          <div>
            <h2 className="text-sm font-semibold">待处理事项</h2>
            <p className="mt-0.5 text-xs text-muted-foreground">根据当前账号和代理状态汇总</p>
          </div>
          <Badge tone={issues.some((item) => item.count > 0) ? "warning" : "success"}>
            {issues.reduce((sum, item) => sum + item.count, 0)} 项
          </Badge>
        </div>
        <div className="grid grid-cols-2 max-[760px]:grid-cols-1">
          {issues.map((issue) => (
            <a key={issue.label} href={issue.href} className="issue-row flex items-center justify-between gap-3 border-b px-4 py-3 text-sm even:border-l max-[760px]:even:border-l-0">
              <span className="flex min-w-0 items-center gap-2">
                <span className={cn("h-2 w-2 shrink-0 rounded-full", issue.count === 0 ? "bg-slate-300" : issue.tone === "danger" ? "bg-red-500" : "bg-amber-500")} />
                <span className="truncate">{issue.label}</span>
              </span>
              <span className={cn("font-semibold tabular-nums", issue.count > 0 && issue.tone === "danger" ? "text-red-700" : issue.count > 0 ? "text-amber-700" : "text-muted-foreground")}>{issue.count}</span>
            </a>
          ))}
        </div>
      </section>
    </div>
  );
}

function ResourceOverview({
  href,
  icon: Icon,
  title,
  status,
  metrics
}: {
  href: string;
  icon: typeof UsersRound;
  title: string;
  status: string;
  metrics: Array<[string, number]>;
}) {
  return (
    <a href={href} className="resource-overview group grid gap-4 p-5 text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring">
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-muted text-foreground"><Icon className="h-4 w-4" /></div>
          <div className="min-w-0">
            <h2 className="truncate text-base font-semibold">{title}</h2>
            <p className="text-xs text-muted-foreground">{status}</p>
          </div>
        </div>
        <ArrowRight className="h-4 w-4 shrink-0 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-primary" />
      </div>
      <dl className="grid grid-cols-3 gap-4">
        {metrics.map(([label, value]) => (
          <div key={label} className="min-w-0">
            <dt className="text-xs text-muted-foreground">{label}</dt>
            <dd className="mt-1 text-xl font-semibold leading-none tabular-nums">{value}</dd>
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
        "flex min-h-10 items-center gap-2 rounded-lg px-3 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring max-[900px]:justify-center max-[520px]:px-2",
        active ? "bg-primary/10 font-medium text-primary" : "text-muted-foreground hover:bg-muted hover:text-foreground"
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

const ACCOUNT_PAGE_SIZE = 20;

function AccountsView({
  accounts,
  pools,
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
  diagnostics,
  diagnosticsLoading,
  diagnosticsError,
  forcedTab,
  onBind,
  onTest,
  onToast,
  onDone,
  onRefreshDiagnostics
}: {
  accounts: AccountRow[];
  pools: ClaudeCodeAccountPool[];
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
  diagnostics?: AccountPoolDiagnostics;
  diagnosticsLoading: boolean;
  diagnosticsError: unknown;
  forcedTab?: "accounts" | "metrics" | "usage" | "events" | "config";
  onBind: (account: ClaudeCodeAccount) => void;
  onTest: (account: ClaudeCodeAccount) => void;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
  onDone: () => Promise<void>;
  onRefreshDiagnostics: () => Promise<void>;
}) {
  const effectiveConfig = config?.effective || defaultPoolEffectiveConfig();
  const [selectedTab, setSelectedTab] = useState<"accounts" | "metrics" | "usage" | "events" | "config">("accounts");
  const activeTab = forcedTab || selectedTab;
  const [searchQuery, setSearchQuery] = useState("");
	  const [statusFilter, setStatusFilter] = useState<"all" | "available" | "checking" | "cooling" | "error" | "disabled">("all");
  const [viewMode, setViewMode] = useState<"cards" | "table">("cards");
  const [selectedIDs, setSelectedIDs] = useState<string[]>([]);
  const [page, setPage] = useState(1);
  const [visibleCardCount, setVisibleCardCount] = useState(ACCOUNT_PAGE_SIZE);
  const [detailRow, setDetailRow] = useState<AccountRow | null>(null);
  const [moveRow, setMoveRow] = useState<AccountRow | null>(null);
  const selectedSet = useMemo(() => new Set(selectedIDs), [selectedIDs]);
  const selectedCount = selectedIDs.length;
  const filteredAccounts = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    return accounts.filter((row) => {
      const account = row.account;
      const matchesQuery = !query || [account.email, account.auth_id, account.proxy?.name, account.proxy?.exit_ip]
        .some((value) => (value || "").toLowerCase().includes(query));
      if (!matchesQuery) {
        return false;
      }
      const runtimeStatus = row.runtime?.status || "active";
      if (statusFilter === "available") {
	        return account.effective_schedulable;
	      }
	      if (statusFilter === "checking") {
	        return account.health_status === "checking";
      }
      if (statusFilter === "cooling") {
        return Boolean(account.runtime_capacity?.account_cooling || account.runtime_capacity?.cooling_until || runtimeStatus === "cooling");
      }
      if (statusFilter === "error") {
        return Boolean(account.last_error || account.quota?.status === "error" || account.availability?.status === "unhealthy");
      }
      if (statusFilter === "disabled") {
	        return !account.schedulable || runtimeStatus === "disabled";
      }
      return true;
    });
  }, [accounts, searchQuery, statusFilter]);
  const pageCount = Math.max(1, Math.ceil(filteredAccounts.length / ACCOUNT_PAGE_SIZE));
  const currentPage = Math.min(page, pageCount);
  const pageRows = useMemo(
    () => filteredAccounts.slice((currentPage - 1) * ACCOUNT_PAGE_SIZE, currentPage * ACCOUNT_PAGE_SIZE),
    [filteredAccounts, currentPage]
  );
  const cardRows = useMemo(
    () => filteredAccounts.slice(0, visibleCardCount),
    [filteredAccounts, visibleCardCount]
  );
  const visibleRows = viewMode === "cards" ? cardRows : pageRows;
  const pageIDs = visibleRows.map((row) => row.account.id);
  const pageSelected = pageIDs.length > 0 && pageIDs.every((id) => selectedSet.has(id));
  const pagePartiallySelected = pageIDs.some((id) => selectedSet.has(id)) && !pageSelected;
  useEffect(() => {
    setSelectedIDs((current) => current.filter((id) => accounts.some((row) => row.account.id === id)));
  }, [accounts]);
  useEffect(() => {
    setPage(1);
    setVisibleCardCount(ACCOUNT_PAGE_SIZE);
  }, [searchQuery, statusFilter]);
  useEffect(() => {
    setPage(1);
    setVisibleCardCount(ACCOUNT_PAGE_SIZE);
  }, [viewMode]);
  useEffect(() => {
    setPage((current) => Math.min(current, Math.max(1, Math.ceil(filteredAccounts.length / ACCOUNT_PAGE_SIZE))));
  }, [filteredAccounts.length]);

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
	    mutationFn: ({ accountID, schedulable }: { accountID: string; schedulable: boolean }) =>
	      api.patchAccount(accountID, { schedulable } as Partial<ClaudeCodeAccount>),
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
	const moveMutation = useMutation({
		mutationFn: ({ accountID, poolID }: { accountID: string; poolID: string }) => api.moveAccount(accountID, poolID),
		onSuccess: async () => {
			setMoveRow(null);
			setDetailRow(null);
			await onDone();
			onToast("账号已移动到目标池");
		},
		onError: (error) => onToast(`移动失败：${errorMessage(error)}`, "danger")
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
	  const recheckMutation = useMutation({
	    mutationFn: api.recheckAccount,
	    onSuccess: async () => {
	      await onDone();
	      onToast("账号检查通过，已恢复调度");
	    },
	    onError: (error) => onToast(`重新检查失败：${errorMessage(error)}`, "danger")
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
    <div className="grid gap-4">
      {!forcedTab ? <SegmentedTabs
        value={activeTab}
        onChange={setSelectedTab}
        items={[
          { value: "accounts", label: "账号" },
          { value: "metrics", label: "运行指标" },
          { value: "usage", label: "用量" },
          { value: "events", label: "事件与日志" },
          { value: "config", label: "配置" }
        ]}
      /> : null}

      {activeTab === "accounts" ? (
        <div className={cn(
          "grid gap-3",
          forcedTab === "accounts" && "lg:min-h-[calc(100dvh-15rem)] lg:grid-rows-[auto_minmax(0,1fr)]"
        )}>
          <AccountWorkspaceToolbar
            query={searchQuery}
            status={statusFilter}
            viewMode={viewMode}
            total={accounts.length}
            filtered={filteredAccounts.length}
            onQueryChange={setSearchQuery}
            onStatusChange={setStatusFilter}
            onViewModeChange={setViewMode}
          />
          <AccountCardsPanel
            rows={visibleRows}
            total={filteredAccounts.length}
            page={currentPage}
            pageCount={pageCount}
            hasMoreCards={viewMode === "cards" && cardRows.length < filteredAccounts.length}
            pageSelected={pageSelected}
            pagePartiallySelected={pagePartiallySelected}
            selectedSet={selectedSet}
            selectedCount={selectedCount}
            pending={batchMutation.isPending}
            availableCount={available.length}
            viewMode={viewMode}
            onPageChange={setPage}
            onLoadMoreCards={() => setVisibleCardCount((current) => Math.min(current + ACCOUNT_PAGE_SIZE, filteredAccounts.length))}
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
			onMove={(row) => setMoveRow(row)}
	            onToggle={(account) => patchMutation.mutate({ accountID: account.id, schedulable: !account.schedulable })}
            onDelete={(account) => {
              if (window.confirm("确认删除这个账号？绑定代理会自动释放。")) {
                deleteMutation.mutate(account.id);
              }
            }}
          />
        </div>
      ) : activeTab === "metrics" ? (
        <AccountPoolMetrics stats={stats} usage={usage} config={effectiveConfig} compactUsage />
      ) : activeTab === "usage" ? (
        <div className="grid gap-4">
          <AccountUsagePanel stats={stats} usage={usage} />
          <CleanInputUsagePanel config={effectiveConfig} calibrations={calibrations} accounts={accounts} models={models} onDone={onDone} onToast={onToast} />
        </div>
      ) : activeTab === "events" ? (
        <div className="grid gap-4">
          <RoutingEventsPanel events={routingEvents} />
          <AccountPoolLogPanel config={logConfig?.effective || effectiveConfig.log} raw={logConfig?.raw} logs={logs} onDone={onDone} onToast={onToast} />
        </div>
      ) : (
        <div className="grid gap-5">
          <RuntimeDiagnosticsPanel
            diagnostics={diagnostics}
            loading={diagnosticsLoading}
            error={diagnosticsError}
            onRefresh={onRefreshDiagnostics}
          />
          <PublicAPIPanel modelsCount={models.filter((model) => model.enabled).length} />
          <ClaudeCodeProfilePanel profile={profile} />
          <ClaudeCodeProfileSnapshotsPanel snapshots={profileSnapshots} profile={profile} onDone={onDone} onToast={onToast} />
          <AccountPoolConfigPanel config={effectiveConfig} path={config?.path} onDone={onDone} onToast={onToast} />
          <RoutingProtectionPanel config={effectiveConfig} onDone={onDone} onToast={onToast} />
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
		pools={pools}
		onMove={(account) => setMoveRow({ account, runtime: detailRow?.runtime })}
        onRefreshToken={(account) => tokenMutation.mutate(account.id)}
	        tokenPending={tokenMutation.isPending}
	        onRecheck={(account) => recheckMutation.mutate(account.id)}
	        recheckPending={recheckMutation.isPending}
	        onToggle={(account) => patchMutation.mutate({ accountID: account.id, schedulable: !account.schedulable })}
        onDelete={(account) => {
          if (window.confirm("确认删除这个账号？绑定代理会自动释放。")) {
            deleteMutation.mutate(account.id);
            setDetailRow(null);
          }
        }}
      />
	  <MoveAccountDialog
		row={moveRow}
		pools={pools}
		open={moveRow !== null}
		pending={moveMutation.isPending}
		onClose={() => setMoveRow(null)}
		onMove={(poolID) => {
			if (moveRow) {
				moveMutation.mutate({ accountID: moveRow.account.id, poolID });
			}
		}}
	  />
    </div>
  );
}

function RuntimeDiagnosticsPanel({
  diagnostics,
  loading,
  error,
  onRefresh
}: {
  diagnostics?: AccountPoolDiagnostics;
  loading: boolean;
  error: unknown;
  onRefresh: () => Promise<void>;
}) {
  const status = diagnostics?.status || "attention";
  const buildCommit = diagnostics?.build.commit && diagnostics.build.commit !== "none"
    ? shortText(diagnostics.build.commit, 10)
    : "未注入";

  return (
    <Card>
      <CardHeader className="gap-3">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <CardTitle>运行一致性诊断</CardTitle>
            <CardDescription>本机构建、数据库、Profile、额度调度与账号采集状态。</CardDescription>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-2">
            {diagnostics ? <DiagnosticStatusBadge status={status} /> : null}
            <Button variant="outline" size="sm" disabled={loading} onClick={() => void onRefresh()}>
              <RefreshCw className={cn("h-3.5 w-3.5", loading && "animate-spin")} />
              刷新诊断
            </Button>
          </div>
        </div>
        {diagnostics ? (
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span>采集于 {formatTime(diagnostics.as_of)}</span>
            <span>账号 {diagnostics.summary.total}</span>
            <span className="text-emerald-700">正常 {diagnostics.summary.healthy}</span>
            <span className="text-amber-700">关注 {diagnostics.summary.attention}</span>
            <span className="text-red-700">异常 {diagnostics.summary.critical}</span>
          </div>
        ) : null}
      </CardHeader>
      <CardContent className="grid gap-4">
        {error && !diagnostics ? (
          <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-red-200 bg-red-50 px-3 py-3 text-sm text-red-800" role="alert">
            <span>诊断读取失败：{errorMessage(error)}</span>
            <Button variant="outline" size="sm" onClick={() => void onRefresh()}>重试</Button>
          </div>
        ) : null}

        {!diagnostics && loading ? (
          <div className="flex min-h-28 items-center justify-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" />
            正在读取本机诊断
          </div>
        ) : null}

        {diagnostics ? (
          <>
            <div className="grid grid-cols-4 gap-3 max-[1180px]:grid-cols-2 max-[620px]:grid-cols-1">
              <DiagnosticSummaryTile
                icon={<FileCode2 className="h-4 w-4" />}
                label="构建"
                value={`${diagnostics.build.version || "dev"} · ${buildCommit}`}
                detail={`${diagnostics.build.goos}/${diagnostics.build.goarch} · ${diagnostics.build.go_version}`}
                foot={diagnostics.build.build_date || "构建时间未知"}
              />
              <DiagnosticSummaryTile
                icon={<Database className="h-4 w-4" />}
                label="数据库实例"
                value={diagnostics.database.instance_fingerprint || "-"}
                detail={diagnostics.database.path || "-"}
              />
              <DiagnosticSummaryTile
                icon={<Network className="h-4 w-4" />}
                label="Profile / 传输"
                value={`${diagnostics.profile.revision || "-"} · ${diagnostics.profile.tls_alpn || "-"}`}
                detail={`${shortText(diagnostics.profile.fingerprint, 12)} · Headers ${diagnostics.profile.header_order_count}/${diagnostics.profile.header_count}`}
                foot={diagnostics.profile.tls_profile || "-"}
              />
              <DiagnosticSummaryTile
                icon={<Activity className="h-4 w-4" />}
                label="额度调度"
                value={diagnostics.quota.enabled ? `每 ${diagnostics.quota.interval}` : "已停用"}
                detail={`扫描 ${diagnostics.quota.scheduler_tick} · 并发 ${diagnostics.quota.concurrency}`}
                foot={`全局出口 ${diagnosticProxyModeText(diagnostics.quota.global_proxy_mode)}`}
              />
            </div>

            {diagnostics.issues.length > 0 ? (
              <div className="flex flex-wrap items-center gap-2 rounded-lg border border-amber-200 bg-amber-50 px-3 py-2">
                <ShieldAlert className="h-4 w-4 shrink-0 text-amber-700" />
                {diagnostics.issues.map((issue) => (
                  <Badge key={issue} tone={diagnosticIssueTone(issue)}>{diagnosticIssueText(issue)}</Badge>
                ))}
              </div>
            ) : (
              <div className="flex items-center gap-2 rounded-lg border border-emerald-200 bg-emerald-50 px-3 py-2 text-sm text-emerald-800">
                <CheckCircle2 className="h-4 w-4" />
                本机运行摘要未发现一致性问题
              </div>
            )}

            <div className="hidden overflow-hidden rounded-lg border min-[901px]:block">
              <table className="w-full table-fixed text-sm">
                <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
                  <tr>
                    <th className="w-[13%] px-3 py-2 font-medium">状态</th>
                    <th className="w-[16%] px-3 py-2 font-medium">账号指纹</th>
                    <th className="w-[18%] px-3 py-2 font-medium">池与出口</th>
                    <th className="w-[14%] px-3 py-2 font-medium">Token</th>
                    <th className="w-[20%] px-3 py-2 font-medium">额度采集</th>
                    <th className="w-[19%] px-3 py-2 font-medium">问题</th>
                  </tr>
                </thead>
                <tbody>
                  {diagnostics.accounts.length > 0 ? diagnostics.accounts.map((account) => (
                    <tr key={`${account.pool_id}:${account.account_fingerprint}`} className="border-t align-top">
                      <td className="px-3 py-3"><DiagnosticStatusBadge status={account.status} /></td>
                      <td className="px-3 py-3">
                        <div className="break-all font-mono text-xs">{account.account_fingerprint}</div>
                        <div className="mt-1 break-all text-xs text-muted-foreground">设备 {account.device_fingerprint || "未知"}</div>
                      </td>
                      <td className="px-3 py-3">
                        <div className="break-words font-medium">{account.pool_id}</div>
                        <div className="mt-1 break-all text-xs text-muted-foreground">{diagnosticAccountProxyText(account)}</div>
                      </td>
                      <td className="px-3 py-3 text-xs">
                        <div>{formatTime(account.token_expires_at)}</div>
                      </td>
                      <td className="px-3 py-3 text-xs">
                        <div>{account.quota_transport || "未采集"}{account.probe?.status_code ? ` · HTTP ${account.probe.status_code}` : ""}</div>
                        <div className="mt-1 text-muted-foreground">上次 {formatTime(account.last_quota_at)}</div>
                        <div className="mt-1 text-muted-foreground">下次 {formatTime(account.next_quota_at)}</div>
                      </td>
                      <td className="px-3 py-3">
                        <DiagnosticIssueList issues={account.issues} />
                      </td>
                    </tr>
                  )) : (
                    <tr><td colSpan={6} className="px-3 py-8 text-center text-muted-foreground">暂无账号诊断</td></tr>
                  )}
                </tbody>
              </table>
            </div>

            <div className="grid gap-2 min-[901px]:hidden">
              {diagnostics.accounts.length > 0 ? diagnostics.accounts.map((account) => (
                <div key={`${account.pool_id}:${account.account_fingerprint}`} className="grid gap-3 rounded-lg border px-3 py-3">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="break-all font-mono text-xs font-semibold">{account.account_fingerprint}</div>
                      <div className="mt-1 break-all text-xs text-muted-foreground">设备 {account.device_fingerprint || "未知"}</div>
                    </div>
                    <DiagnosticStatusBadge status={account.status} />
                  </div>
                  <div className="grid grid-cols-2 gap-x-4 gap-y-2 text-xs max-[520px]:grid-cols-1">
                    <DiagnosticField label="账号池" value={account.pool_id} />
                    <DiagnosticField label="出口" value={diagnosticAccountProxyText(account)} />
                    <DiagnosticField label="Token 到期" value={formatTime(account.token_expires_at)} />
                    <DiagnosticField label="额度传输" value={`${account.quota_transport || "未采集"}${account.probe?.status_code ? ` · HTTP ${account.probe.status_code}` : ""}`} />
                    <DiagnosticField label="上次额度" value={formatTime(account.last_quota_at)} />
                    <DiagnosticField label="下次额度" value={formatTime(account.next_quota_at)} />
                  </div>
                  <DiagnosticIssueList issues={account.issues} />
                </div>
              )) : <EmptyState title="暂无账号诊断" description="数据库中还没有 Claude Code 账号。" />}
            </div>
          </>
        ) : null}
      </CardContent>
    </Card>
  );
}

function DiagnosticSummaryTile({
  icon,
  label,
  value,
  detail,
  foot
}: {
  icon: ReactNode;
  label: string;
  value: string;
  detail: string;
  foot?: string;
}) {
  return (
    <div className="min-w-0 rounded-lg border bg-muted/25 p-3">
      <div className="flex items-center gap-2 text-xs font-medium text-muted-foreground">{icon}{label}</div>
      <div className="mt-2 break-all text-sm font-semibold">{value}</div>
      <div className="mt-1 break-all text-xs text-muted-foreground" title={detail}>{detail}</div>
      {foot ? <div className="mt-1 break-all text-xs text-muted-foreground" title={foot}>{foot}</div> : null}
    </div>
  );
}

function DiagnosticStatusBadge({ status }: { status?: string }) {
  const normalized = (status || "attention").toLowerCase();
  const tone = normalized === "healthy" ? "success" : normalized === "critical" ? "danger" : "warning";
  const label = normalized === "healthy" ? "正常" : normalized === "critical" ? "异常" : "需关注";
  return <Badge tone={tone} className="whitespace-nowrap">{label}</Badge>;
}

function DiagnosticIssueList({ issues }: { issues: string[] }) {
  if (issues.length === 0) {
    return <span className="text-xs text-muted-foreground">无</span>;
  }
  return (
    <div className="flex flex-wrap gap-1.5">
      {issues.map((issue) => <Badge key={issue} tone={diagnosticIssueTone(issue)}>{diagnosticIssueText(issue)}</Badge>)}
    </div>
  );
}

function DiagnosticField({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <div className="text-muted-foreground">{label}</div>
      <div className="mt-0.5 break-all font-medium">{value}</div>
    </div>
  );
}

function diagnosticAccountProxyText(account: AccountPoolDiagnostics["accounts"][number]) {
  if (account.proxy_resource_id) {
    return `${account.proxy_resource_id}${account.last_observed_exit_ip ? ` · ${account.last_observed_exit_ip}` : ""}`;
  }
  return account.last_observed_exit_ip || "未绑定";
}

function diagnosticProxyModeText(mode?: string) {
  switch ((mode || "").toLowerCase()) {
    case "proxy": return "代理";
    case "direct": return "直连";
    case "invalid": return "配置无效";
    default: return "继承";
  }
}

function diagnosticIssueTone(issue: string): "neutral" | "success" | "warning" | "danger" | "info" {
  return ["profile_transport_mismatch", "accounts_critical", "auth_missing", "token_expired", "manual_recovery", "proxy_binding_missing", "proxy_unhealthy", "quota_proxy_invalid"].includes(issue)
    ? "danger"
    : "warning";
}

function diagnosticIssueText(issue: string) {
  const labels: Record<string, string> = {
    build_commit_unknown: "构建 Commit 未注入",
    profile_revision_custom: "使用自定义 Profile",
    profile_transport_mismatch: "Profile 传输不匹配",
    no_accounts: "暂无账号",
    accounts_critical: "存在异常账号",
    accounts_attention: "存在待关注账号",
    auth_missing: "认证数据缺失",
    token_expired: "Token 已过期",
    token_expiring: "Token 即将过期",
    manual_recovery: "需要人工恢复",
    temporarily_blocked: "账号临时阻断",
    proxy_binding_missing: "代理绑定缺失",
    proxy_unhealthy: "代理异常",
    quota_never_observed: "尚未采集额度",
    quota_stale: "额度数据过期",
    quota_schedule_overdue: "额度采集逾期",
    quota_profile_mismatch: "采集 Profile 已变化",
    quota_proxy_invalid: "额度代理无效",
    quota_probe_failed: "额度采集失败"
  };
  return labels[issue] || issue;
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
        <div className="grid grid-cols-6 gap-3 max-[1280px]:grid-cols-3 max-[760px]:grid-cols-2 max-[560px]:grid-cols-1">
          <ReadOnlyTile label="Claude Code 版本" value={effective?.version || "2.1.207"} />
          <ReadOnlyTile label="Profile Revision" value={effective?.revision || "2.1.207-r3"} />
          <ReadOnlyTile label="平台" value={`${effective?.headers?.["X-Stainless-Os"] || "MacOS"} · ${effective?.headers?.["X-Stainless-Arch"] || "arm64"}`} />
          <ReadOnlyTile label="Billing" value={effective?.billing_block_enabled === false ? "关闭" : "sdk-cli · 无 CCH"} />
          <ReadOnlyTile label="Prompt" value="稳定工具无关" />
          <ReadOnlyTile label="传输" value={`${effective?.tls_profile || "node-macos-arm64-http1"} · ${effective?.tls_alpn || "http/1.1"}`} />
        </div>

        <div className="rounded-lg border bg-muted/30 p-3">
          <div className="text-xs font-medium text-muted-foreground">User-Agent</div>
          <div className="mt-1 break-all font-mono text-sm">{effective?.user_agent || "claude-cli/2.1.207 (external, sdk-cli)"}</div>
        </div>

        <div className="grid grid-cols-2 gap-3 max-[860px]:grid-cols-1">
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="text-xs font-medium text-muted-foreground">HTTP/1.1 Header 顺序</div>
            <div className="mt-1 text-sm">固定 {effective?.header_order?.length || 0} 个已验证字段位置</div>
          </div>
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="text-xs font-medium text-muted-foreground">TLS 摘要</div>
            <div className="mt-1 break-all font-mono text-xs">JA3 {effective?.tls_ja3 || "-"}</div>
            <div className="mt-1 break-all font-mono text-xs">JA4 {effective?.tls_ja4 || "-"}</div>
          </div>
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
          生产只使用经过 trace 校验的稳定工具无关 Prompt、sdk-cli billing、账号级 metadata.user_id 和官方域名 HTTP/1.1 TLS profile。Phistory 完整动态 Prompt 仅用于对比参考。
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
  const [version, setVersion] = useState(profile?.effective?.version || "2.1.207");
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
            <Input value={version} onChange={(event) => setVersion(event.target.value)} placeholder="2.1.207" />
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

        <div className="grid grid-cols-4 gap-3 max-[1100px]:grid-cols-2 max-[620px]:grid-cols-1">
          <ReadOnlyTile label="当前运行版本" value={profile?.effective?.version || "-"} />
          <ReadOnlyTile label="基线 Revision" value={profile?.effective?.revision || "-"} />
          <ReadOnlyTile label="当前来源" value={profile?.effective?.updated_from || "builtin"} />
          <ReadOnlyTile label="Profile 摘要" value={currentFingerprint} />
        </div>

        <div className="overflow-x-auto rounded-lg border">
          <table className="min-w-[1080px] text-sm">
            <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
              <tr>
                <th className="px-3 py-2 font-medium">版本</th>
                <th className="px-3 py-2 font-medium">状态</th>
                <th className="px-3 py-2 font-medium">稳定提示词</th>
                <th className="px-3 py-2 font-medium">完整动态 Prompt</th>
                <th className="px-3 py-2 font-medium">Trace Hash</th>
                <th className="px-3 py-2 font-medium">Diff</th>
                <th className="px-3 py-2 font-medium">拉取时间</th>
                <th className="px-3 py-2 text-right font-medium">操作</th>
              </tr>
            </thead>
            <tbody>
              {snapshots.length === 0 ? (
                <tr>
                  <td colSpan={8} className="px-3 py-8 text-center text-muted-foreground">
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
                      <div className="mt-1 flex flex-wrap gap-1">
                        {Object.entries(item.request_kind_summary || {}).map(([kind, count]) => (
                          <Badge key={kind} tone="neutral">{kind} {count}</Badge>
                        ))}
                      </div>
                    </td>
                    <td className="px-3 py-2">
                      <Badge tone={item.status === "promoted" ? "success" : item.status === "failed" ? "danger" : "info"}>{snapshotStatusText(item.status)}</Badge>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">
                      <div>{shortText(item.static_prompt_hash, 12)}</div>
                      <div className="mt-1 font-sans text-muted-foreground">{item.static_prompt_length || 0} 字符</div>
                    </td>
                    <td className="px-3 py-2 font-mono text-xs">
                      <div>{shortText(item.full_prompt_hash || item.prompt_hash, 12)}</div>
                      <div className="mt-1 font-sans text-muted-foreground">{item.full_prompt_length || 0} 字符</div>
                    </td>
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

type ProfileDiffTab = "prompt" | "full-prompt" | "headers" | "betas" | "report";
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
  const snapshotPrompt = snapshot.static_prompts_md || snapshotProfile?.system_prompt || "";
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
          稳定提示词
        </ProfileDiffTabButton>
        <ProfileDiffTabButton active={tab === "full-prompt"} onClick={() => setTab("full-prompt")}>
          完整动态 Prompt
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
      {tab === "full-prompt" ? (
        <pre className="max-h-[520px] overflow-auto whitespace-pre-wrap rounded-md bg-slate-950 p-3 text-xs leading-5 text-slate-50">
          {snapshot.prompt_md || "快照未包含完整动态 Prompt。"}
        </pre>
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

function AccountPoolMetrics({
  stats,
  usage,
  config,
  compactUsage = false
}: {
  stats?: ClaudeCodePoolStats;
  usage?: UsageSummary;
  config: ClaudeCodePoolEffectiveConfig;
  compactUsage?: boolean;
}) {
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

        {!compactUsage ? (
          <div className="grid grid-cols-5 gap-3 max-[1180px]:grid-cols-3 max-[760px]:grid-cols-2 max-[480px]:grid-cols-1">
            <CompactStat label="真实输入" value={formatTokenLarge(stats?.raw_input_tokens || stats?.input_tokens || 0)} />
            <CompactStat label="真实输出" value={formatTokenLarge(stats?.output_tokens || 0)} />
            <CompactStat label="缓存创建" value={formatTokenLarge(stats?.cache_creation_tokens || 0)} />
            <CompactStat label="缓存读取" value={formatTokenLarge(stats?.cache_read_tokens || 0)} />
            <CompactStat label="真实总量" value={formatTokenLarge(stats?.raw_total_tokens || realTotalTokens(stats))} />
          </div>
        ) : null}

        <div className={cn("grid gap-3", compactUsage ? "grid-cols-1" : "grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.2fr)] max-[1080px]:grid-cols-1")}>
          {!compactUsage ? <UsageBucket title="近 1h 按模型" items={usage?.by_requested_model?.length ? usage.by_requested_model : usage?.by_model || []} /> : null}
          {!compactUsage ? <UsageBucket title="近 1h 按账号" items={usage?.by_account || []} /> : null}
          <RecentRoutingErrors errors={stats?.recent_errors || []} />
        </div>
      </CardContent>
    </Card>
  );
}

function AccountUsagePanel({ stats, usage }: { stats?: ClaudeCodePoolStats; usage?: UsageSummary }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>真实 Token 用量</CardTitle>
        <CardDescription>使用 Anthropic 返回的原始 usage，不受纯净模式下游计费口径影响。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-5 gap-3 max-[980px]:grid-cols-3 max-[640px]:grid-cols-2">
          <CompactStat label="真实输入" value={formatTokenLarge(stats?.raw_input_tokens || stats?.input_tokens || 0)} />
          <CompactStat label="真实输出" value={formatTokenLarge(stats?.output_tokens || 0)} />
          <CompactStat label="缓存创建" value={formatTokenLarge(stats?.cache_creation_tokens || 0)} />
          <CompactStat label="缓存读取" value={formatTokenLarge(stats?.cache_read_tokens || 0)} />
          <CompactStat label="真实总量" value={formatTokenLarge(stats?.raw_total_tokens || realTotalTokens(stats))} />
        </div>
        <div className="grid grid-cols-2 gap-3 max-[860px]:grid-cols-1">
          <UsageBucket title="按模型" items={usage?.by_requested_model?.length ? usage.by_requested_model : usage?.by_model || []} />
          <UsageBucket title="按账号" items={usage?.by_account || []} />
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
  const [allowClientCacheTTL, setAllowClientCacheTTL] = useState(config.allow_client_cache_ttl);
  useEffect(() => {
    setEnabled(config.enabled);
    setPureMode(config.pure_mode);
    setAllowClientCacheTTL(config.allow_client_cache_ttl);
  }, [config.allow_client_cache_ttl, config.enabled, config.pure_mode]);
  const mutation = useMutation({
    mutationFn: () => api.savePoolConfig(toRawPoolConfig({ ...config, enabled, pure_mode: pureMode, allow_client_cache_ttl: allowClientCacheTTL })),
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
        <CardDescription>账号池开关、请求缓存和下游 usage 净化策略。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <ToggleRow label="启用账号池" checked={enabled} onChange={setEnabled} />
        <ToggleRow label="纯净计费模式" checked={pureMode} onChange={setPureMode} />
        <ToggleBox
          label="允许请求参数控制缓存 TTL"
          help="默认关闭：账号池发往 Anthropic 的已有缓存断点统一使用 1 小时。开启后，客户端显式传入的 ttl: 5m 或 ttl: 1h 会被保留；未指定 ttl 的断点仍使用 1 小时。"
          checked={allowClientCacheTTL}
          onChange={setAllowClientCacheTTL}
        />
        <div className="rounded-lg border bg-muted/30 px-3 py-2 text-sm leading-6 text-muted-foreground">
          只清理返回给下游的输入与缓存 usage，扣除账号池自动注入的 Claude Code 请求开销；上游请求、真实账本和 Anthropic 实际消耗保持不变。
        </div>
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

const ROUTING_HELP: Partial<Record<keyof RoutingEffectiveConfig, string>> = {
  cache_affinity_enabled: "让同一显式会话优先使用同一账号，并让长上下文缓存前缀使用稳定的备用账号，提高 Anthropic 真实缓存命中率。没有 Session ID 的请求不会建立会话绑定。",
  cache_affinity_auto: "根据账号池规模、可用账号和当前压力自动调整亲和 lanes。关闭后固定使用下方配置的亲和 lanes 与最大 lanes。",
  account_capacity_profile: "预设每账号 RPM 和基础并发。选择“自定义”时使用右侧手动值；其他档位由系统套用预设容量。OAuth 账号建议使用保守档位或较低的自定义值。",
  per_account_rpm: "单个账号每分钟最多发起的真实上游请求次数。首次请求和每次重试都会计数；Sticky 并发预留不会增加此上限。",
  per_account_concurrency: "单个账号允许同时执行的基础请求数。数值越高吞吐越大，但更容易触发上游限流或账号风险。",
  sticky_concurrency_reserve: "仅供已绑定到该账号的亲和会话使用的额外并发槽位。新会话和普通请求不能占用，也不会增加 RPM。",
  max_sessions: "单个账号最多维持的活跃显式会话数。这是调度软上限：新会话会改选其他账号，已有会话仍可继续；空闲达到活跃 TTL 后释放名额。",
  max_switches: "一次请求最多允许切换到其他账号的次数。数值过大会放大请求延迟和账号池压力；请求参数错误不会触发换号。",
  sticky_wait_ms: "已绑定会话的主账号满载时，等待并发槽位释放的最长时间。超时后才尝试备用账号或返回 429。单位：毫秒。",
  fallback_wait_ms: "普通请求或无绑定请求在账号满载时等待可用槽位的最长时间。超时后返回本地 429。单位：毫秒。",
  max_waiters_per_account: "单个账号允许排队等待并发槽位的请求数。达到上限后新请求不会继续排队。",
  max_waiters_global: "整个 Claude Code 账号池允许同时排队的请求总数，用于避免满载时积压过多请求。",
  session_affinity_ttl_ms: "会话主账号绑定的空闲有效期，命中后会续期。过期后该会话重新参与账号选择。单位：毫秒。",
  active_session_idle_ttl_ms: "活跃会话占用 MaxSessions 名额的空闲时间，默认 5 分钟。它独立于 1 小时会话亲和。单位：毫秒。",
  switch_delay_ms: "换到另一个账号前的等待时间，用于避免错误发生后立即连续冲击多个账号。单位：毫秒。",
  rate_limit_cooldown_ms: "遇到没有明确 Retry-After/reset 的 HTTP 429 时，第一次对该模型执行的本地冷却时间。单位：毫秒。",
  rate_limit_max_cooldown_ms: "HTTP 429 本地指数退避允许增长到的最长冷却时间；上游返回明确 reset 时优先采用上游时间。单位：毫秒。",
  overload_cooldown_ms: "遇到 HTTP 529 上游过载时，第一次对该模型执行的本地冷却时间。单位：毫秒。",
  overload_max_cooldown_ms: "HTTP 529 连续发生时，本地指数退避允许增长到的最长冷却时间。单位：毫秒。",
  same_account_retry_429: "收到 HTTP 429 后，在同一账号上再次尝试的次数。每次尝试都会消耗 RPM；通常建议设为 0。",
  same_account_retry_529: "收到 HTTP 529 后，在同一账号上再次尝试的次数。适合短暂上游过载，默认 1 次。",
  same_account_retry_delay_ms: "429/529 同账号重试之间的固定等待时间。单位：毫秒。",
  cache_affinity_min_cache_tokens: "只有请求中的缓存相关 Token 达到此阈值，才启用长上下文前缀亲和；较短请求仍只使用会话主绑定。",
  cache_affinity_lanes: "一个缓存前缀期望维持的稳定账号通道数。主账号不可用时会从这些 lanes 中选择备用；手动策略下直接生效。",
  cache_affinity_max_lanes: "同一缓存前缀最多可扩展到的账号通道数。自动策略会在该上限内按账号池规模和压力调整。",
  cache_affinity_wait_ms: "亲和账号暂时满载时，为保住真实缓存命中而等待该账号的时间；超时后才使用其他账号。单位：毫秒。",
  cache_affinity_ttl_ms: "缓存前缀与稳定 lanes 的空闲有效期，命中后会续期。它独立于上面的会话主绑定 TTL。单位：毫秒。"
};

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
      onToast("全局默认策略已保存");
    },
    onError: (error) => onToast(`保存失败：${errorMessage(error)}`, "danger")
  });
  return (
    <Card>
      <CardHeader>
        <CardTitle>全局默认策略</CardTitle>
        <CardDescription>未设置独立策略的账号池继承这里的调度参数。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-4">
        <div className="grid grid-cols-4 gap-3 max-[1040px]:grid-cols-2 max-[560px]:grid-cols-1">
          <ToggleBox label="启用缓存亲和" help={ROUTING_HELP.cache_affinity_enabled} checked={routing.cache_affinity_enabled} onChange={(value) => setField("cache_affinity_enabled", value)} />
          <ToggleBox label="自动策略" help={ROUTING_HELP.cache_affinity_auto} checked={routing.cache_affinity_auto} onChange={(value) => setField("cache_affinity_auto", value)} />
          <Field label="承载档位" help={ROUTING_HELP.account_capacity_profile}>
            <Select value={routing.account_capacity_profile} onChange={(event) => setField("account_capacity_profile", event.target.value)}>
              <option value="custom">自定义</option>
              <option value="conservative">保守</option>
              <option value="standard">标准</option>
              <option value="aggressive">激进</option>
            </Select>
          </Field>
          <NumberField label="每账号 RPM" help={ROUTING_HELP.per_account_rpm} value={routing.per_account_rpm} onChange={(value) => setField("per_account_rpm", value)} />
          <NumberField
            label="每账号并发"
            help={ROUTING_HELP.per_account_concurrency}
            value={routing.per_account_concurrency}
            onChange={(value) => setField("per_account_concurrency", value)}
          />
          <NumberField label="最大换号次数" help={ROUTING_HELP.max_switches} value={routing.max_switches} onChange={(value) => setField("max_switches", value)} />
        </div>
        <details className="rounded-lg border bg-muted/20">
          <summary className="cursor-pointer px-4 py-3 text-sm font-medium">高级路由设置</summary>
          <div className="grid grid-cols-4 gap-3 border-t p-4 max-[1040px]:grid-cols-2 max-[560px]:grid-cols-1">
            <NumberField label="亲和会话额外并发" help={ROUTING_HELP.sticky_concurrency_reserve} value={routing.sticky_concurrency_reserve} onChange={(value) => setField("sticky_concurrency_reserve", value)} />
            <NumberField label="活跃会话软上限" help={ROUTING_HELP.max_sessions} value={routing.max_sessions} onChange={(value) => setField("max_sessions", value)} />
            <NumberField label="Sticky 等待 ms" help={ROUTING_HELP.sticky_wait_ms} value={routing.sticky_wait_ms} onChange={(value) => setField("sticky_wait_ms", value)} />
            <NumberField label="普通等待 ms" help={ROUTING_HELP.fallback_wait_ms} value={routing.fallback_wait_ms} onChange={(value) => setField("fallback_wait_ms", value)} />
            <NumberField label="单账号等待上限" help={ROUTING_HELP.max_waiters_per_account} value={routing.max_waiters_per_account} onChange={(value) => setField("max_waiters_per_account", value)} />
            <NumberField label="全局等待上限" help={ROUTING_HELP.max_waiters_global} value={routing.max_waiters_global} onChange={(value) => setField("max_waiters_global", value)} />
	            <NumberField label="会话亲和 TTL ms" help={ROUTING_HELP.session_affinity_ttl_ms} value={routing.session_affinity_ttl_ms} onChange={(value) => setField("session_affinity_ttl_ms", value)} />
	            <NumberField label="活跃会话 TTL ms" help={ROUTING_HELP.active_session_idle_ttl_ms} value={routing.active_session_idle_ttl_ms} onChange={(value) => setField("active_session_idle_ttl_ms", value)} />
            <NumberField label="换号间隔 ms" help={ROUTING_HELP.switch_delay_ms} value={routing.switch_delay_ms} onChange={(value) => setField("switch_delay_ms", value)} />
            <NumberField label="429 初始冷却 ms" help={ROUTING_HELP.rate_limit_cooldown_ms} value={routing.rate_limit_cooldown_ms} onChange={(value) => setField("rate_limit_cooldown_ms", value)} />
            <NumberField label="429 最大冷却 ms" help={ROUTING_HELP.rate_limit_max_cooldown_ms} value={routing.rate_limit_max_cooldown_ms} onChange={(value) => setField("rate_limit_max_cooldown_ms", value)} />
            <NumberField label="529 初始冷却 ms" help={ROUTING_HELP.overload_cooldown_ms} value={routing.overload_cooldown_ms} onChange={(value) => setField("overload_cooldown_ms", value)} />
            <NumberField label="529 最大冷却 ms" help={ROUTING_HELP.overload_max_cooldown_ms} value={routing.overload_max_cooldown_ms} onChange={(value) => setField("overload_max_cooldown_ms", value)} />
            <NumberField label="429 同号重试" help={ROUTING_HELP.same_account_retry_429} value={routing.same_account_retry_429} onChange={(value) => setField("same_account_retry_429", value)} />
            <NumberField label="529 同号重试" help={ROUTING_HELP.same_account_retry_529} value={routing.same_account_retry_529} onChange={(value) => setField("same_account_retry_529", value)} />
            <NumberField label="重试间隔 ms" help={ROUTING_HELP.same_account_retry_delay_ms} value={routing.same_account_retry_delay_ms} onChange={(value) => setField("same_account_retry_delay_ms", value)} />
            <NumberField label="最小缓存 Tokens" help={ROUTING_HELP.cache_affinity_min_cache_tokens} value={routing.cache_affinity_min_cache_tokens} onChange={(value) => setField("cache_affinity_min_cache_tokens", value)} />
            <NumberField label="亲和 lanes" help={ROUTING_HELP.cache_affinity_lanes} value={routing.cache_affinity_lanes} onChange={(value) => setField("cache_affinity_lanes", value)} />
            <NumberField label="最大 lanes" help={ROUTING_HELP.cache_affinity_max_lanes} value={routing.cache_affinity_max_lanes} onChange={(value) => setField("cache_affinity_max_lanes", value)} />
            <NumberField label="亲和等待 ms" help={ROUTING_HELP.cache_affinity_wait_ms} value={routing.cache_affinity_wait_ms} onChange={(value) => setField("cache_affinity_wait_ms", value)} />
            <NumberField label="亲和 TTL ms" help={ROUTING_HELP.cache_affinity_ttl_ms} value={routing.cache_affinity_ttl_ms} onChange={(value) => setField("cache_affinity_ttl_ms", value)} />
          </div>
        </details>
        {routing.per_account_rpm > 6 || routing.per_account_concurrency > 1 || routing.sticky_concurrency_reserve > 1 ? (
          <div className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm text-amber-800">
            当前请求强度高于保守档位（6 RPM / 1 并发 / 1 亲和额外并发），请结合账号额度和实际错误率评估。
          </div>
        ) : null}
        <div className="flex justify-end">
          <Button onClick={() => mutation.mutate()} disabled={mutation.isPending}>
            {mutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <CheckCircle2 className="h-4 w-4" />}
            保存全局策略
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
  const runnableAccounts = accounts.filter((row) => row.account.effective_schedulable && row.account.has_auth_data);

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
  const containerRef = useRef<HTMLDivElement>(null);
  const activeButtonRef = useRef<HTMLButtonElement>(null);

  useLayoutEffect(() => {
    const container = containerRef.current;
    const activeButton = activeButtonRef.current;
    if (!container || !activeButton) {
      return;
    }

    const left = activeButton.offsetLeft;
    const right = left + activeButton.offsetWidth;
    if (left < container.scrollLeft) {
      container.scrollLeft = left;
    } else if (right > container.scrollLeft + container.clientWidth) {
      container.scrollLeft = right - container.clientWidth;
    }
  }, [value]);

  return (
    <div ref={containerRef} className="flex w-full flex-nowrap gap-1 overflow-x-auto rounded-lg border bg-card p-1">
      {items.map((item) => {
        const active = item.value === value;
        return (
          <button
            key={item.value}
            ref={active ? activeButtonRef : undefined}
            type="button"
            className={cn(
              "min-h-9 shrink-0 rounded-md px-4 py-1.5 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
              active ? "bg-foreground text-background shadow-sm" : "text-muted-foreground hover:bg-muted hover:text-foreground"
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

function AccountWorkspaceToolbar({
  query,
  status,
  viewMode,
  total,
  filtered,
  onQueryChange,
  onStatusChange,
  onViewModeChange
}: {
  query: string;
  status: "all" | "available" | "checking" | "cooling" | "error" | "disabled";
  viewMode: "cards" | "table";
  total: number;
  filtered: number;
  onQueryChange: (value: string) => void;
  onStatusChange: (value: "all" | "available" | "checking" | "cooling" | "error" | "disabled") => void;
  onViewModeChange: (value: "cards" | "table") => void;
}) {
  return (
    <div className="workspace-toolbar flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-card p-3">
      <div className="flex min-w-0 flex-1 flex-wrap items-center gap-2">
        <label className="relative min-w-[220px] flex-1 max-w-md">
          <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            placeholder="搜索邮箱、Auth ID、代理"
            className="pl-9 pr-9"
          />
          {query ? (
            <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => onQueryChange("")} aria-label="清空搜索">
              <X className="h-3.5 w-3.5" />
            </button>
          ) : null}
        </label>
        <Select value={status} onChange={(event) => onStatusChange(event.target.value as typeof status)} className="w-32">
          <option value="all">全部状态</option>
          <option value="available">可调度</option>
          <option value="checking">检查中</option>
          <option value="cooling">冷却中</option>
          <option value="error">有错误</option>
          <option value="disabled">暂停调度</option>
        </Select>
        <span className="whitespace-nowrap text-xs text-muted-foreground">{filtered} / {total}</span>
      </div>
      <div className="view-toggle flex rounded-lg border bg-muted/40 p-0.5" aria-label="账号显示方式">
        <button type="button" className={cn("icon-segment", viewMode === "cards" && "active")} onClick={() => onViewModeChange("cards")} title="卡片视图" aria-label="卡片视图"><LayoutGrid className="h-4 w-4" /></button>
        <button type="button" className={cn("icon-segment", viewMode === "table" && "active")} onClick={() => onViewModeChange("table")} title="表格视图" aria-label="表格视图"><List className="h-4 w-4" /></button>
      </div>
    </div>
  );
}

function AccountCardsPanel({
  rows,
  total,
  page,
  pageCount,
  hasMoreCards,
  pageSelected,
  pagePartiallySelected,
  selectedSet,
  selectedCount,
  pending,
  availableCount,
  viewMode,
  onPageChange,
  onLoadMoreCards,
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
  onMove,
  onToggle,
  onDelete
}: {
  rows: AccountRow[];
  total: number;
  page: number;
  pageCount: number;
  hasMoreCards: boolean;
  pageSelected: boolean;
  pagePartiallySelected: boolean;
  selectedSet: Set<string>;
  selectedCount: number;
  pending: boolean;
  availableCount: number;
  viewMode: "cards" | "table";
  onPageChange: (page: number) => void;
  onLoadMoreCards: () => void;
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
  onMove: (row: AccountRow) => void;
  onToggle: (account: ClaudeCodeAccount) => void;
  onDelete: (account: ClaudeCodeAccount) => void;
}) {
  const loadMoreRef = useRef<HTMLDivElement>(null);
  const start = total === 0 ? 0 : viewMode === "cards" ? 1 : (page - 1) * ACCOUNT_PAGE_SIZE + 1;
  const end = total === 0 ? 0 : start + rows.length - 1;

  useEffect(() => {
    const target = loadMoreRef.current;
    if (viewMode !== "cards" || !hasMoreCards || !target) {
      return;
    }
    let requested = false;
    const observer = new IntersectionObserver((entries) => {
      if (!requested && entries.some((entry) => entry.isIntersecting)) {
        requested = true;
        onLoadMoreCards();
      }
    }, { rootMargin: "0px" });
    observer.observe(target);
    return () => observer.disconnect();
  }, [hasMoreCards, onLoadMoreCards, rows.length, viewMode]);

  return (
    <section className="flex min-h-0 flex-col gap-3">
        {selectedCount > 0 ? <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-primary/25 bg-primary/5 px-3 py-2">
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
            <span>{viewMode === "cards" ? "选择已显示" : "选择本页"}</span>
            <span className="text-muted-foreground">已选 {selectedCount}</span>
          </label>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" size="sm" onClick={() => onRunBatch("test")} disabled={selectedCount === 0 || pending}>
              {pending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
              测试
            </Button>
            <Button variant="outline" size="sm" onClick={() => onRunBatch("enable")} disabled={selectedCount === 0 || pending}>
              参与调度
            </Button>
            <Button variant="outline" size="sm" onClick={() => onRunBatch("disable")} disabled={selectedCount === 0 || pending}>
              暂停调度
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
        </div> : null}

        {total > 0 ? (
          viewMode === "cards" ? (
          <div className="account-grid grid min-h-0 flex-1 auto-rows-max content-start grid-cols-1 gap-3 min-[640px]:grid-cols-2 min-[1180px]:grid-cols-3 min-[1500px]:grid-cols-4 min-[1840px]:grid-cols-5">
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
                  onMove={() => onMove(row)}
                  onToggle={() => onToggle(row.account)}
                  onDelete={() => onDelete(row.account)}
                />
              ))}
          </div>
          ) : (
            <AccountTable
              rows={rows}
              selectedSet={selectedSet}
              quotaPending={quotaPending}
              onSelectAccount={onSelectAccount}
              onDetails={onDetails}
              onTest={onTest}
              onBind={onBind}
              onUnbind={onUnbind}
              onReset={onReset}
              onRefreshQuota={onRefreshQuota}
              onMove={onMove}
              onToggle={onToggle}
              onDelete={onDelete}
            />
          )
        ) : (
          <EmptyState title="没有匹配的账号" description="调整搜索或筛选条件，或点击顶部新增账号。" />
        )}

        {viewMode === "cards" ? (
          <div ref={loadMoreRef} className="mt-auto flex min-h-11 flex-wrap items-center justify-between gap-3 rounded-lg border bg-card px-3 py-2 text-sm text-muted-foreground">
            <div>已显示 {rows.length} / 共 {total} 个 · 空闲代理 {availableCount}</div>
            {hasMoreCards ? <Loader2 className="h-4 w-4 animate-spin" aria-label="正在加载更多账号" /> : <span>已全部显示</span>}
          </div>
        ) : (
        <div className="mt-auto flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-card px-3 py-2 text-sm text-muted-foreground">
          <div>
            第 {start}-{end} 个 / 共 {total} 个 · 空闲代理 {availableCount}
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
        )}
    </section>
  );
}

function AccountTable({
  rows,
  selectedSet,
  quotaPending,
  onSelectAccount,
  onDetails,
  onTest,
  onBind,
  onUnbind,
  onReset,
  onRefreshQuota,
  onMove,
  onToggle,
  onDelete
}: {
  rows: AccountRow[];
  selectedSet: Set<string>;
  quotaPending: boolean;
  onSelectAccount: (id: string, selected: boolean) => void;
  onDetails: (row: AccountRow) => void;
  onTest: (account: ClaudeCodeAccount) => void;
  onBind: (account: ClaudeCodeAccount) => void;
  onUnbind: (account: ClaudeCodeAccount) => void;
  onReset: (account: ClaudeCodeAccount) => void;
  onRefreshQuota: (account: ClaudeCodeAccount) => void;
  onMove: (row: AccountRow) => void;
  onToggle: (account: ClaudeCodeAccount) => void;
  onDelete: (account: ClaudeCodeAccount) => void;
}) {
  return (
    <div className="table-scroll">
      <table className="data-table account-table min-w-[1080px]">
        <thead>
          <tr>
            <th className="w-10"></th>
            <th>账号</th>
            <th>状态</th>
            <th>额度</th>
            <th>1h 可用性</th>
            <th>负载</th>
            <th>30 天用量</th>
            <th>固定出口</th>
            <th className="w-28">操作</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => {
            const account = row.account;
            const bound = Boolean(account.proxy_resource_id || account.proxy);
            return (
              <tr key={account.id} className="cursor-pointer" onClick={() => onDetails(row)}>
                <td onClick={(event) => event.stopPropagation()}>
                  <input type="checkbox" className="h-4 w-4 rounded border-border" checked={selectedSet.has(account.id)} onChange={(event) => onSelectAccount(account.id, event.target.checked)} aria-label={`选择账号 ${account.email || account.auth_id}`} />
                </td>
                <td>
                    <div className="max-w-56">
                      <div className="truncate font-medium" title={account.email || account.auth_id}>{account.email || account.auth_id}</div>
                    </div>
                  </td>
                <td><AccountStatusBadge account={account} runtime={row.runtime} /></td>
                <td>
                  <CompactQuotaInline account={account} />
                </td>
                <td><AvailabilityTableCell availability={account.availability} /></td>
                <td><CapacityInline account={account} /></td>
                <td>
                  <div className="min-w-32 text-xs tabular-nums">
                    <div>{account.usage?.request_count || 0} 请求 · {formatTokenLarge(account.usage?.raw_total_tokens || 0)}</div>
                    <div className="mt-1 font-medium text-foreground">{formatUSD(account.usage?.estimated_cost || 0)}</div>
                  </div>
                </td>
                <td className="max-w-44 truncate font-mono text-xs" title={account.proxy ? proxyDisplay(account.proxy) : ""}>{account.proxy?.exit_ip || (account.proxy ? account.proxy.name : "未绑定")}</td>
                <td onClick={(event) => event.stopPropagation()}>
                  <div className="flex items-center gap-1">
                    <Button variant="outline" size="icon" className="h-8 w-8" onClick={() => onTest(account)} title="测试账号" aria-label="测试账号"><Play className="h-3.5 w-3.5" /></Button>
                    <OverflowMenu label="账号操作">
                      <button type="button" onClick={() => onDetails(row)}>查看详情</button>
                      <button type="button" onClick={() => (bound ? onUnbind(account) : onBind(account))}>{bound ? "解绑代理" : "绑定代理"}</button>
                      <button type="button" onClick={() => onRefreshQuota(account)} disabled={quotaPending}>刷新额度</button>
                      <button type="button" onClick={() => onReset(account)}>清除冷却</button>
                      <button type="button" onClick={() => onMove(row)}>移动账号池</button>
                      <button type="button" onClick={() => onToggle(account)}>{account.schedulable ? "暂停调度" : "参与调度"}</button>
                      <button type="button" className="danger" onClick={() => onDelete(account)}>删除账号</button>
                    </OverflowMenu>
                  </div>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

function CompactQuotaInline({ account }: { account: ClaudeCodeAccount }) {
  const quota5h = findQuotaWindowState(account, "five_hour");
  const quota7d = findQuotaWindowState(account, "seven_day");
  return (
    <div className="grid min-w-44 gap-1 whitespace-nowrap text-xs tabular-nums">
      <div className="flex items-center gap-2">
        <span title={quotaWindowStateTitle(quota5h)}><span className="text-muted-foreground">5h</span> {quotaWindowStateCompactValue(quota5h)}</span>
        <span className="text-border">/</span>
        <span title={quotaWindowStateTitle(quota7d)}><span className="text-muted-foreground">7天</span> {quotaWindowStateCompactValue(quota7d)}</span>
      </div>
      <CompactModelQuotaSummary account={account} />
    </div>
  );
}

function AvailabilityTableCell({ availability }: { availability?: AccountAvailabilitySummary }) {
  const summary = availabilityWindowSummary(availability);
  const hasData = summary.request_count > 0;
  const tone = availabilityTone(summary.status);
  return (
    <div className="grid w-44 gap-1.5">
      <div className="flex items-center justify-between gap-2 text-xs">
        <span className={cn("font-semibold tabular-nums", tone.textClass)}>{hasData ? formatPercent(summary.success_rate) : "暂无请求"}</span>
        {hasData ? <span className="text-muted-foreground">{summary.request_count} 次</span> : null}
      </div>
      <AvailabilityStrip availability={availability} compact />
    </div>
  );
}

function CapacityInline({ account }: { account: ClaudeCodeAccount }) {
  const runtime = account.runtime_capacity;
  const configured = account.capacity;
  return (
    <div className="whitespace-nowrap text-xs tabular-nums">
      <span>并发 {runtime?.in_flight ?? 0}/{runtime?.concurrency_limit || configured?.concurrency_limit || 0}</span>
      <span className="mx-1.5 text-border">·</span>
      <span>RPM {runtime?.rpm_used ?? 0}/{runtime?.rpm_limit || configured?.base_rpm || 0}</span>
    </div>
  );
}

function OverflowMenu({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState({ left: 0, top: 0 });

  useLayoutEffect(() => {
    if (!open) {
      return;
    }
    const updatePosition = () => {
      const trigger = triggerRef.current;
      const menu = menuRef.current;
      if (!trigger || !menu) {
        return;
      }
      const triggerRect = trigger.getBoundingClientRect();
      const menuRect = menu.getBoundingClientRect();
      const viewportPadding = 8;
      const left = Math.min(
        window.innerWidth - menuRect.width - viewportPadding,
        Math.max(viewportPadding, triggerRect.right - menuRect.width)
      );
      const spaceBelow = window.innerHeight - triggerRect.bottom - viewportPadding;
      const top = spaceBelow >= menuRect.height + 4
        ? triggerRect.bottom + 4
        : Math.max(viewportPadding, triggerRect.top - menuRect.height - 4);
      setPosition({ left, top });
    };
    const closeOnPointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!triggerRef.current?.contains(target) && !menuRef.current?.contains(target)) {
        setOpen(false);
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    updatePosition();
    window.addEventListener("resize", updatePosition);
    document.addEventListener("scroll", updatePosition, true);
    document.addEventListener("pointerdown", closeOnPointerDown);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("resize", updatePosition);
      document.removeEventListener("scroll", updatePosition, true);
      document.removeEventListener("pointerdown", closeOnPointerDown);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  return (
    <div className="overflow-menu">
      <button
        ref={triggerRef}
        type="button"
        title={label}
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={(event) => {
          event.stopPropagation();
          setOpen((current) => !current);
        }}
      >
        <MoreHorizontal className="h-4 w-4" />
      </button>
      {open
        ? createPortal(
            <div
              ref={menuRef}
              role="menu"
              className="overflow-menu-content"
              style={{ left: position.left, top: position.top }}
              onClick={() => setOpen(false)}
            >
              {children}
            </div>,
            document.body
          )
        : null}
    </div>
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
  onMove,
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
  onMove: () => void;
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
        "flex h-full min-w-0 cursor-pointer flex-col gap-1.5 overflow-visible rounded-lg border bg-card p-2.5 transition-colors hover:border-primary/45 hover:bg-muted/15 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
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
      <div className="flex min-w-0 items-center justify-between gap-2.5">
        <label className="flex min-w-0 flex-1 items-center gap-2" onClick={(event) => event.stopPropagation()}>
          <input
            type="checkbox"
            className="h-4 w-4 shrink-0 rounded border-border"
            checked={selected}
            aria-label={`选择账号 ${displayName}`}
            onChange={(event) => onSelectedChange(event.target.checked)}
          />
          <span className="min-w-0 truncate text-sm font-semibold leading-5" title={displayName}>{displayName}</span>
        </label>
        <AccountStatusBadge account={account} runtime={runtime} />
      </div>

      <AvailabilityPanel availability={account.availability} compact />

      <CompactQuotaGrid account={account} />
      <AccountMetricGrid account={account} />

      <div className="mt-auto flex min-w-0 items-center justify-between gap-2 border-t pt-1.5" onClick={(event) => event.stopPropagation()}>
        <div className="min-w-0 flex-1"><BoundProxyIndicator account={account} /></div>
        <div className="flex shrink-0 items-center gap-1.5">
          <Button variant="outline" size="sm" className="h-8 px-2" onClick={onTest}>
            <Play className="h-3.5 w-3.5" />
            测试
          </Button>
          <OverflowMenu label="账号操作">
            <button type="button" onClick={onDetails}>查看详情</button>
            <button type="button" onClick={bound ? onUnbind : onBind}>{bound ? "解绑代理" : "绑定代理"}</button>
            <button type="button" onClick={onRefreshQuota} disabled={quotaPending}>刷新额度</button>
            <button type="button" onClick={onReset}>清除冷却</button>
            <button type="button" onClick={onMove}>移动账号池</button>
            <button type="button" onClick={onToggle}>{account.schedulable ? "暂停调度" : "参与调度"}</button>
            <button type="button" className="danger" onClick={onDelete}>删除账号</button>
          </OverflowMenu>
        </div>
      </div>
    </article>
  );
}

function CompactQuotaGrid({ account }: { account: ClaudeCodeAccount }) {
  const windows = [
    { label: "5h", state: findQuotaWindowState(account, "five_hour") },
    { label: "7天", state: findQuotaWindowState(account, "seven_day") },
    { label: "S", state: findQuotaWindowState(account, "seven_day_sonnet") },
    { label: "O", state: findQuotaWindowState(account, "seven_day_opus") },
    { label: "F", state: findQuotaWindowState(account, "seven_day_fable") }
  ];
  return (
    <div className="grid min-w-0 grid-cols-5 divide-x border-y py-1">
      {windows.map(({ label, state }) => (
        <div key={label} className="grid min-w-0 justify-items-center px-1 text-center" title={quotaWindowStateTitle(state)}>
          <span className="text-[10px] leading-3 text-muted-foreground">{label}</span>
          <span className={cn("max-w-full truncate text-xs font-semibold leading-4 tabular-nums", quotaWindowStateTextTone(state))}>
            {state && state.confidence !== "unknown" ? quotaWindowStateCompactValue(state) : "未知"}
          </span>
        </div>
      ))}
    </div>
  );
}

function CompactModelQuotaSummary({ account }: { account: ClaudeCodeAccount }) {
  const windows = [
    { label: "S", state: findQuotaWindowState(account, "seven_day_sonnet") },
    { label: "O", state: findQuotaWindowState(account, "seven_day_opus") },
    { label: "F", state: findQuotaWindowState(account, "seven_day_fable") }
  ];
  return (
    <div className="flex min-w-0 items-center justify-between gap-2 text-[11px] tabular-nums">
      {windows.map(({ label, state }) => (
        <span key={label} className="flex min-w-0 items-center gap-1 whitespace-nowrap" title={quotaWindowStateTitle(state)}>
          <span className="text-muted-foreground">{label}</span>
          <span className={cn("font-semibold", quotaWindowStateTextTone(state))}>{quotaWindowStateCompactValue(state)}</span>
        </span>
      ))}
    </div>
  );
}

function AccountMetricGrid({ account }: { account: ClaudeCodeAccount }) {
  const runtime = account.runtime_capacity;
  const configured = account.capacity;
  const concurrencyLimit = runtime?.concurrency_limit ?? configured?.concurrency_limit ?? 0;
  const inFlight = runtime?.in_flight ?? 0;
  const metrics = [
    { label: "并发", value: concurrencyLimit > 0 ? `${inFlight}/${concurrencyLimit}` : "-" },
    { label: "RPM", value: `${runtime?.rpm_used ?? 0}/${runtime?.rpm_limit || configured?.base_rpm || 0}` },
    { label: "请求", value: formatNumber(account.usage?.request_count || 0) },
    { label: "Tokens", value: formatTokenLarge(account.usage?.raw_total_tokens || 0) },
    { label: "金额", value: formatUSD(account.usage?.estimated_cost || 0) }
  ];
  return (
    <div className="grid grid-cols-5 divide-x overflow-hidden rounded-md border bg-muted/25" title="请求、Tokens 和金额为最近 30 天原始上游用量">
      {metrics.map((metric) => (
        <div key={metric.label} className="min-w-0 px-1 py-1 text-center">
          <div className="truncate text-[10px] leading-3 text-muted-foreground">{metric.label}</div>
          <div className="truncate text-xs font-semibold leading-4 tabular-nums" title={metric.value}>{metric.value}</div>
        </div>
      ))}
    </div>
  );
}

function BoundProxyIndicator({ account }: { account: ClaudeCodeAccount }) {
  const bound = Boolean(account.proxy_resource_id || account.proxy);
  const proxyText = account.proxy?.exit_ip || account.proxy?.name || (account.proxy_resource_id ? "已绑定" : "未绑定 IP");
  return (
    <div className={cn("flex min-w-0 items-center gap-1.5 text-xs", bound ? "text-emerald-700" : "text-muted-foreground")}>
      {bound ? <Network className="h-3.5 w-3.5 shrink-0" /> : <Link2Off className="h-3.5 w-3.5 shrink-0" />}
      <span className="truncate font-medium" title={account.proxy ? proxyDisplay(account.proxy) : proxyText}>{proxyText}</span>
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
  const summary = availabilityWindowSummary(availability);
  const tone = availabilityTone(summary.status);
  const hasData = summary.request_count > 0;
  const value = hasData ? formatPercent(summary.success_rate) : "暂无请求";
  if (compact) {
    return (
      <div className={cn(
        "grid min-w-0 items-center gap-2",
        hasData ? "grid-cols-[2rem_minmax(0,1fr)_auto]" : "grid-cols-[2rem_minmax(0,1fr)]"
      )}>
        <span className="text-xs font-medium text-muted-foreground">1h</span>
        <AvailabilityStrip availability={availability} compact />
        {hasData ? <span className={cn("min-w-12 text-right text-xs font-semibold tabular-nums", tone.textClass)}>{value}</span> : null}
      </div>
    );
  }
  return (
    <div className="grid min-w-0 gap-3 overflow-hidden rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <span className="text-xs font-medium text-muted-foreground">1h 可用性</span>
          <span className={cn("text-lg font-semibold tabular-nums", tone.textClass)}>{value}</span>
        </div>
        <Badge tone={tone.badgeTone}>{tone.label}</Badge>
      </div>
      <AvailabilityStrip availability={availability} />
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
        <span>最近 1 小时 · 每格 2 分钟</span>
        <span className="tabular-nums">{hasData ? `${summary.success_count}/${summary.request_count} 成功 · 失败 ${summary.failure_count}` : "灰色表示无请求"}</span>
      </div>
    </div>
  );
}

function AvailabilityStrip({ availability, compact = false }: { availability?: AccountAvailabilitySummary; compact?: boolean }) {
  const buckets = aggregateAvailabilityBuckets(normalizeAvailabilityBuckets(availability), 2);
  return (
    <div
      className={cn("availability-strip grid min-w-0 max-w-full grid-flow-col overflow-hidden rounded-sm", compact ? "h-2.5 gap-px" : "h-5 gap-0.5")}
      style={{ gridTemplateColumns: `repeat(${buckets.length}, minmax(0, 1fr))` }}
      aria-label="最近 1 小时每两分钟请求可用性"
    >
      {buckets.map((bucket, index) => {
        const tone = availabilityTone(bucket.status);
        const title = availabilityBucketTitle(bucket);
        return (
          <span
            key={`${bucket.started_at || "empty"}-${index}`}
            className={cn("block min-w-0 rounded-[2px]", tone.barClass)}
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
  if (buckets.length >= 60) {
    return buckets.slice(-60);
  }
  const padding = Array.from({ length: 60 - buckets.length }, () => ({
    started_at: "",
    request_count: 0,
    success_count: 0,
    success_rate: 0,
    status: "none"
  }));
  return [...padding, ...buckets];
}

function availabilityWindowSummary(availability?: AccountAvailabilitySummary): AccountAvailabilitySummary {
  const buckets = normalizeAvailabilityBuckets(availability);
  const requestCount = buckets.reduce((sum, bucket) => sum + (bucket.request_count || 0), 0);
  const successCount = buckets.reduce((sum, bucket) => sum + (bucket.success_count || 0), 0);
  return {
    window_minutes: 60,
    request_count: requestCount,
    success_count: successCount,
    failure_count: Math.max(0, requestCount - successCount),
    success_rate: requestCount > 0 ? (successCount * 100) / requestCount : 0,
    status: availabilityStatusForCount(requestCount, successCount),
    buckets
  };
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
	pools,
	onMove,
	  onRefreshToken,
	  tokenPending,
	  onRecheck,
	  recheckPending,
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
  pools: ClaudeCodeAccountPool[];
  onMove: (account: ClaudeCodeAccount) => void;
  onRefreshToken: (account: ClaudeCodeAccount) => void;
	  tokenPending: boolean;
	  onRecheck: (account: ClaudeCodeAccount) => void;
	  recheckPending: boolean;
  onToggle: (account: ClaudeCodeAccount) => void;
  onDelete: (account: ClaudeCodeAccount) => void;
}) {
  const account = row?.account;
  const runtime = row?.runtime;
  const bound = Boolean(account?.proxy_resource_id || account?.proxy);
  const identity = parseClaudeCodeIdentity(account?.cloak_user_id);
  const availabilitySummary = availabilityWindowSummary(account?.availability);
  const quota5h = account ? findQuotaWindowState(account, "five_hour") : undefined;
  const quota7d = account ? findQuotaWindowState(account, "seven_day") : undefined;
	const pool = pools.find((item) => item.id === account?.pool_id);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="drawer-panel grid-rows-[auto_minmax(0,1fr)] gap-0 overflow-hidden rounded-none border-y-0 border-r-0 p-0">
        {account ? (
          <>
            <DialogHeader className="border-b px-5 py-4 pr-12">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="grid min-w-0 gap-1.5">
                  <DialogTitle>账号详情</DialogTitle>
                  <DialogDescription className="break-all">{account.email || account.auth_id}</DialogDescription>
                  <div className="flex flex-wrap items-center gap-1.5">
                    <AccountStatusBadge account={account} runtime={runtime} />
					<Badge tone="neutral">{pool?.name || account.pool_id}</Badge>
                    <QuotaBandBadge account={account} />
                    <AccountTestBadge account={account} />
                    {account.runtime_capacity?.account_cooling ? <Badge tone="danger">账号冷却</Badge> : null}
                    {(account.runtime_capacity?.model_cooling_count || 0) > 0 ? <Badge tone="warning">{account.runtime_capacity?.model_cooling_count} 个模型冷却</Badge> : null}
                    {bound ? <Badge tone="success">已绑定代理</Badge> : <Badge tone="warning">未绑定代理</Badge>}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button variant="outline" size="sm" onClick={() => onTest(account)}>
                    <Play className="h-3.5 w-3.5" />
                    测试
                  </Button>
                  <Button variant="outline" size="icon" className="h-8 w-8" onClick={() => onRefreshToken(account)} disabled={tokenPending} title="刷新 Token" aria-label="刷新 Token">
                    <RefreshCw className={cn("h-3.5 w-3.5", tokenPending && "animate-spin")} />
                  </Button>
                  <OverflowMenu label="更多账号操作">
                    <button type="button" onClick={() => (bound ? onUnbind(account) : onBind(account))}>{bound ? "解绑代理" : "绑定代理"}</button>
                    <button type="button" onClick={() => onReset(account)}>清除冷却</button>
	                    <button type="button" onClick={() => onRecheck(account)} disabled={recheckPending}>重新检查并恢复</button>
	                    <button type="button" onClick={() => onMove(account)}>移动账号池</button>
	                    <button type="button" onClick={() => onToggle(account)}>{account.schedulable ? "暂停调度" : "参与调度"}</button>
                    <button type="button" className="danger" onClick={() => onDelete(account)}>删除账号</button>
                  </OverflowMenu>
                </div>
              </div>
            </DialogHeader>
            <div className="grid content-start gap-4 overflow-y-auto px-5 py-4 pb-6">
	              {account.blocked_reason ? (
	                <section className={cn("grid gap-1 rounded-lg border px-3 py-2.5", account.health_status === "manual_recovery" ? "border-red-200 bg-red-50 text-red-800" : "border-amber-200 bg-amber-50 text-amber-800")}>
	                  <div className="text-xs font-semibold">{account.health_status === "manual_recovery" ? "账号需要处理" : "账号暂不可调度"}</div>
	                  <div className="text-xs leading-5">{account.blocked_reason}{account.blocked_until ? ` · 预计 ${formatTime(account.blocked_until)} 恢复` : ""}</div>
	                </section>
	              ) : null}
	              <section className="grid grid-cols-6 divide-x rounded-lg border bg-card max-[900px]:grid-cols-3 max-[620px]:grid-cols-2 max-[900px]:divide-x-0">
                <DetailMetric label="1h 可用性" value={availabilitySummary.request_count > 0 ? formatPercent(availabilitySummary.success_rate) : "暂无请求"} />
                <DetailMetric label="5h 额度" value={quotaWindowStateCompactValue(quota5h)} />
                <DetailMetric label="7天额度" value={quotaWindowStateCompactValue(quota7d)} />
                <DetailMetric label="并发 / RPM" value={`${account.runtime_capacity?.in_flight || 0}/${account.runtime_capacity?.concurrency_limit || account.capacity?.concurrency_limit || 0} · ${account.runtime_capacity?.rpm_used || 0}/${account.runtime_capacity?.rpm_limit || account.capacity?.base_rpm || 0}`} />
	                <DetailMetric label="活跃会话" value={`${account.runtime_capacity?.active_sessions || 0}/${account.runtime_capacity?.max_sessions || account.capacity?.max_sessions || 0}`} />
	                <DetailMetric label="亲和额外并发" value={`${account.runtime_capacity?.reserve_used || 0}/${account.runtime_capacity?.sticky_concurrency_reserve || account.capacity?.sticky_concurrency_reserve || 0}`} />
              </section>

              <div className="grid grid-cols-[minmax(0,1.05fr)_minmax(0,0.95fr)] items-start gap-3 max-[700px]:grid-cols-1">
                <AvailabilityPanel availability={account.availability} />
	                <DenseQuotaPanel account={account} onRefresh={() => onRefreshQuota(account)} refreshing={quotaPending} />
              </div>

              {account.model_statuses ? <ModelStatusStrip statuses={account.model_statuses} /> : null}

			  <AccountUsageBreakdown usage={account.usage} />

              <ProxyDetailRow account={account} />

              {account.last_error || runtime?.last_error ? (
                <section className="grid gap-1.5 rounded-lg border border-red-200 bg-red-50 px-3 py-2.5">
                  <div className="text-xs font-semibold text-red-800">最近错误</div>
                  <div className="break-words text-xs leading-5 text-red-700">{account.last_error || runtime?.last_error}</div>
                </section>
              ) : null}

              <details className="identity-details rounded-lg border bg-card">
                <summary className="cursor-pointer px-3 py-2.5 text-sm font-medium">身份与时间信息</summary>
                <dl className="grid grid-cols-2 gap-x-5 gap-y-3 border-t px-3 py-3 text-xs max-[560px]:grid-cols-1">
                  <DetailField label="Auth ID" value={account.auth_id} />
                  <DetailField label="Device ID" value={identity.deviceId || "-"} />
                  <DetailField label="Account UUID" value={identity.accountUUID || "-"} />
                  <DetailField label="Session ID" value="请求时按会话生成" />
                  <DetailField label="亲和绑定" value={`${account.affinity_bindings || 0}`} />
                  <DetailField label="运行成功率" value={successRate(runtime)} />
                  <DetailField label="连续失败" value={`${account.consecutive_failures || 0}`} />
                  <DetailField label="最近测试" value={formatTime(account.last_test_at)} />
	                  <DetailField label="Token 过期" value={formatTime(account.token_expires_at)} />
	                  <DetailField label="最近健康检查" value={formatTime(account.last_health_check_at)} />
	                  <DetailField label="下次健康检查" value={formatTime(account.next_health_check_at)} />
                  <DetailField label="更新时间" value={formatTime(account.updated_at)} />
                </dl>
              </details>
            </div>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function DetailMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0 px-3 py-3 max-[620px]:border-b max-[620px]:odd:border-r">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-1 truncate text-base font-semibold tabular-nums" title={value}>{value}</div>
    </div>
  );
}

function DetailField({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="mt-1 break-all font-medium leading-5">{value}</dd>
    </div>
  );
}

function AccountUsageBreakdown({ usage }: { usage?: UsageSummary["by_account"][number] }) {
	return (
		<section className="grid gap-3 rounded-lg border bg-card p-3">
			<div className="flex flex-wrap items-center justify-between gap-2">
				<div>
					<div className="text-sm font-medium">最近 30 天用量</div>
					<div className="mt-0.5 text-[11px] text-muted-foreground">成本使用原始上游 usage，包含重试和换号产生的真实 attempts</div>
				</div>
				<Badge tone={(usage?.unpriced_request_count || 0) > 0 ? "warning" : "success"}>
					计价覆盖 {formatPercent(usage?.pricing_coverage ?? 100)}
				</Badge>
			</div>
			<div className="grid grid-cols-4 gap-px overflow-hidden rounded-md border bg-border max-[620px]:grid-cols-2">
				<UsageDetailMetric label="请求 / Attempts" value={`${usage?.request_count || 0} / ${usage?.attempt_count || 0}`} />
				<UsageDetailMetric label="输入" value={formatTokenLarge(usage?.input_tokens || 0)} />
				<UsageDetailMetric label="输出" value={formatTokenLarge(usage?.output_tokens || 0)} />
				<UsageDetailMetric label="缓存读取" value={formatTokenLarge(usage?.cache_read_tokens || 0)} />
				<UsageDetailMetric label="缓存写入 5m" value={formatTokenLarge(usage?.cache_creation_5m_tokens || 0)} />
				<UsageDetailMetric label="缓存写入 1h" value={formatTokenLarge(usage?.cache_creation_1h_tokens || 0)} />
				<UsageDetailMetric label="原始 Tokens" value={formatTokenLarge(usage?.raw_total_tokens || 0)} />
				<UsageDetailMetric label="估算成本" value={formatUSD(usage?.estimated_cost || 0)} />
			</div>
			{(usage?.unpriced_request_count || 0) > 0 ? <div className="text-xs text-amber-700">{usage?.unpriced_request_count} 个请求尚无匹配价格，Tokens 已统计但未计入成本。</div> : null}
		</section>
	);
}

function UsageDetailMetric({ label, value }: { label: string; value: string }) {
	return <div className="min-w-0 bg-card px-3 py-2.5"><div className="text-[11px] text-muted-foreground">{label}</div><div className="mt-1 truncate text-sm font-semibold tabular-nums" title={value}>{value}</div></div>;
}

function MoveAccountDialog({ row, pools, open, pending, onClose, onMove }: {
	row: AccountRow | null;
	pools: ClaudeCodeAccountPool[];
	open: boolean;
	pending: boolean;
	onClose: () => void;
	onMove: (poolID: string) => void;
}) {
	const targets = pools.filter((pool) => !pool.archived_at && pool.id !== row?.account.pool_id);
	const [poolID, setPoolID] = useState("");
	useEffect(() => {
		if (open) {
			setPoolID(targets[0]?.id || "");
		}
	}, [open, row?.account.id, targets[0]?.id]);
	return (
		<Dialog open={open} onOpenChange={(next) => !next && onClose()}>
			<DialogContent>
				<DialogHeader>
					<DialogTitle>移动账号</DialogTitle>
					<DialogDescription>移动会清除旧池中的亲和绑定和活跃会话；历史用量仍归原账号池。</DialogDescription>
				</DialogHeader>
				{targets.length > 0 ? (
					<form className="grid gap-4" onSubmit={(event) => { event.preventDefault(); if (poolID) onMove(poolID); }}>
						<div className="grid gap-2">
							<Label htmlFor="move-account-pool">目标账号池</Label>
							<Select id="move-account-pool" value={poolID} onChange={(event) => setPoolID(event.target.value)}>
								{targets.map((pool) => <option key={pool.id} value={pool.id}>{pool.name}</option>)}
							</Select>
						</div>
						<div className="flex justify-end gap-2"><Button type="button" variant="outline" onClick={onClose}>取消</Button><Button type="submit" disabled={!poolID || pending}>{pending ? <Loader2 className="h-4 w-4 animate-spin" /> : <ArrowRightLeft className="h-4 w-4" />}移动</Button></div>
					</form>
				) : <div className="rounded-md border bg-muted px-3 py-8 text-center text-sm text-muted-foreground">没有可移动到的其他账号池</div>}
			</DialogContent>
		</Dialog>
	);
}

function DenseQuotaPanel({ account, onRefresh, refreshing }: { account: ClaudeCodeAccount; onRefresh: () => void; refreshing: boolean }) {
  const quota = account.quota;
  const windows = accountQuotaWindowStates(account);
  return (
    <section className="grid gap-2">
      <div className="flex items-center justify-between gap-2">
        <div className="min-w-0">
          <span className="text-sm font-medium">额度</span>
          <div className="mt-0.5 text-[11px] text-muted-foreground">
            {typeof account.headroom === "number" ? `Headroom ${formatPercent(account.headroom * 100)}` : "尚无可用 Headroom"}
            {account.quota_band && account.quota_band !== "unknown" ? ` · ${quotaBandText(account.quota_band)}` : ""}
          </div>
        </div>
        <Button variant="ghost" size="icon" className="h-7 w-7" onClick={onRefresh} disabled={refreshing} title="刷新额度" aria-label="刷新额度">
          <RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} />
        </Button>
      </div>
      {quota?.status === "error" ? (
        <div className="text-xs leading-5 text-red-700">额度刷新失败，以下保留最后一次采集结果</div>
      ) : null}
      <div className="divide-y overflow-hidden rounded-md border bg-card">
        {windows.map((window) => {
          const progressValue = quotaWindowStateProgress(window);
          return (
            <div key={window.key} className="grid gap-1.5 px-3 py-2.5">
              <div className="flex min-w-0 items-center justify-between gap-3">
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <span className="truncate text-xs font-medium" title={window.name}>{window.name}</span>
                  <Badge className="px-1.5 py-0 text-[10px]" tone={quotaWindowConfidenceTone(window)}>{quotaWindowConfidenceText(window)}</Badge>
                  {window.freshness === "stale" ? <Badge className="px-1.5 py-0 text-[10px]" tone="warning">数据过期</Badge> : window.freshness === "fresh" ? <Badge className="px-1.5 py-0 text-[10px]" tone="success">新鲜</Badge> : null}
                </div>
                <span className={cn("shrink-0 text-xs font-semibold tabular-nums", quotaWindowStateTextTone(window))}>{quotaWindowStateDetailValue(window)}</span>
              </div>
              {typeof progressValue === "number" ? <Progress value={progressValue} /> : <div className="h-2 rounded-full bg-muted" />}
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] leading-4 text-muted-foreground">
                <span>{quotaSourceText(window.source)}</span>
                <span>{window.observed_at ? `采集 ${formatTime(window.observed_at)}` : "尚未采集"}</span>
                {window.status ? <span>{quotaWindowStatusText(window.status)}</span> : null}
                {window.resets_at ? <span>重置 {formatTime(window.resets_at)}</span> : null}
              </div>
            </div>
          );
        })}
      </div>
    </section>
  );
}

function ProxyDetailRow({ account }: { account: ClaudeCodeAccount }) {
  return (
    <section className="flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-lg border bg-card px-3 py-2.5">
      <div className="min-w-0">
        <div className="text-xs text-muted-foreground">绑定代理</div>
        <div className="mt-1 truncate text-sm font-medium" title={account.proxy ? proxyDisplay(account.proxy) : "未绑定代理"}>{account.proxy ? account.proxy.name : "未绑定代理"}</div>
      </div>
      {account.proxy ? (
        <div className="flex min-w-0 items-center gap-2 text-xs">
          <HealthBadge status={account.proxy.health_status} enabled={account.proxy.enabled} />
          <span className="truncate font-mono text-muted-foreground">{account.proxy.exit_ip || proxyDisplay(account.proxy)}</span>
          <span className="text-muted-foreground">{account.proxy.latency_ms || 0} ms</span>
        </div>
      ) : <Badge tone="warning">未绑定</Badge>}
    </section>
  );
}

function findQuotaWindow(quota: AccountQuota | undefined, candidates: string[]) {
  const windows = quota?.windows || [];
  const normalized = candidates.map((item) => item.toLowerCase());
  return windows.find((item) => normalized.includes(String(item.key || "").toLowerCase()) || normalized.includes(String(item.name || "").toLowerCase()));
}

const quotaWindowStateSpecs = [
  { key: "five_hour", name: "5 小时", candidates: ["five_hour", "5 小时", "5小时"], shared: false },
  { key: "seven_day", name: "7 天", candidates: ["seven_day", "7 天", "7天"], shared: false },
  { key: "seven_day_sonnet", name: "Sonnet 周额度", candidates: ["seven_day_sonnet"], shared: true },
  { key: "seven_day_opus", name: "Opus 周额度", candidates: ["seven_day_opus"], shared: true },
  { key: "seven_day_fable", name: "Fable 周额度", candidates: ["seven_day_fable", "seven_day_overage_included", "model_7d_oi"], shared: true }
] as const;

function unknownQuotaWindowState(key: string, name: string): QuotaWindowState {
  return { key, name, confidence: "unknown", freshness: "unknown", utilization_known: false, exhausted: false };
}

function accountQuotaWindowStates(account: ClaudeCodeAccount): QuotaWindowState[] {
  const provided = account.quota_window_states || [];
  if (provided.length > 0) {
    return quotaWindowStateSpecs.map((spec) => provided.find((item) => item.key === spec.key) || unknownQuotaWindowState(spec.key, spec.name));
  }

  const shared = findQuotaWindow(account.quota, ["seven_day", "7 天", "7天"]);
  return quotaWindowStateSpecs.map((spec) => {
    const direct = findQuotaWindow(account.quota, [...spec.candidates]);
    const window = direct || (spec.shared ? shared : undefined);
    if (!window) {
      return unknownQuotaWindowState(spec.key, spec.name);
    }
    const observedAt = window.updated_at || account.quota?.checked_at;
    const observedTime = observedAt ? Date.parse(observedAt) : Number.NaN;
    const resetTime = window.resets_at ? Date.parse(window.resets_at) : Number.NaN;
    const stale = (Number.isFinite(observedTime) && Date.now() - observedTime > 15 * 60 * 1000) || (Number.isFinite(resetTime) && resetTime <= Date.now());
    const known = quotaWindowUtilizationKnown(window);
    return {
      key: spec.key,
      name: spec.name,
      confidence: direct ? (known ? "exact" : "observed") : "shared",
      freshness: observedAt ? (stale ? "stale" : "fresh") : "unknown",
      source: window.source || account.quota_source,
      observed_at: observedAt,
      shared_from: direct ? undefined : "seven_day",
      utilization_known: known,
      used_percent: known ? window.used_percent : undefined,
      remain_percent: known ? window.remain_percent : undefined,
      resets_at: window.resets_at,
      status: window.status,
      remaining: window.remaining,
      exhausted: quotaWindowExplicitlyExhausted(window) || (known && window.used_percent >= 100)
    };
  });
}

function findQuotaWindowState(account: ClaudeCodeAccount, key: string) {
  return accountQuotaWindowStates(account).find((item) => item.key === key);
}

function quotaWindowStateProgress(state?: QuotaWindowState) {
  if (!state || state.freshness !== "fresh" || !state.utilization_known || typeof state.remain_percent !== "number") {
    return undefined;
  }
  return state.remain_percent;
}

function quotaWindowStateCompactValue(state?: QuotaWindowState) {
  if (!state || state.confidence === "unknown") return "未知";
  if (state.freshness === "stale") return "过期";
  if (state.freshness !== "fresh") return "未知";
  if (state.exhausted) return "已耗尽";
  if (state.confidence === "shared") return "共享";
  if (state.utilization_known && typeof state.remain_percent === "number") return formatPercent(state.remain_percent);
  return "已观察";
}

function quotaWindowStateDetailValue(state: QuotaWindowState) {
  if (state.confidence === "unknown") return "未知";
  if (state.freshness === "stale") {
    return state.utilization_known && typeof state.remain_percent === "number" ? `上次 ${formatPercent(state.remain_percent)}` : "历史状态";
  }
  if (state.freshness !== "fresh") return "采集时间未知";
  if (state.exhausted) return "已耗尽";
  if (state.utilization_known && typeof state.remain_percent === "number") return `${formatPercent(state.remain_percent)} 可用`;
  return "已观察";
}

function quotaWindowConfidenceText(state: QuotaWindowState) {
  switch (state.confidence) {
    case "exact": return "精确百分比";
    case "shared": return "共享 7 天";
    case "observed": return "仅观察";
    default: return "未知";
  }
}

function quotaWindowConfidenceTone(state: QuotaWindowState): "success" | "info" | "neutral" {
  if (state.confidence === "exact") return "success";
  if (state.confidence === "shared") return "info";
  return "neutral";
}

function quotaWindowStateTitle(state?: QuotaWindowState) {
  if (!state) return "暂无额度数据";
  const details = [state.name, quotaWindowConfidenceText(state)];
  details.push(state.freshness === "fresh" ? "数据新鲜" : state.freshness === "stale" ? "数据过期" : "采集时间未知");
  if (state.source) details.push(quotaSourceText(state.source));
  if (state.observed_at) details.push(`采集 ${formatTime(state.observed_at)}`);
  if (state.resets_at) details.push(`重置 ${formatTime(state.resets_at)}`);
  return details.join(" · ");
}

function quotaWindowStateTextTone(state?: QuotaWindowState) {
  if (!state || state.confidence === "unknown" || state.freshness === "unknown") return "text-muted-foreground";
  if (state.freshness === "stale") return "text-amber-700";
  if (state.exhausted || (state.utilization_known && (state.remain_percent ?? 100) <= 0)) return "text-red-700";
  if (state.utilization_known && (state.remain_percent ?? 100) <= 15) return "text-amber-700";
  return "text-foreground";
}

function quotaSourceText(source?: string) {
  if (source === "response_headers") return "推理响应 Header";
  if (source === "oauth_usage") return "OAuth usage";
  if (source === "mixed") return "OAuth usage + 响应 Header";
  return "未知来源";
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
  const [accountID, setAccountID] = useState("");
  const runnableAccounts = accounts.filter((row) => row.account.effective_schedulable && row.account.has_auth_data);
  useEffect(() => {
    if (accountID && !runnableAccounts.some((row) => row.account.id === accountID)) {
      setAccountID("");
    }
  }, [accountID, runnableAccounts]);
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
  return (
    <Card>
      <CardHeader>
        <CardTitle>纯净模式计费校准</CardTitle>
        <CardDescription>校准账号池自动注入的 Claude Code 开销，用于清理下游 input、缓存创建和缓存读取；真实 usage 始终保留。</CardDescription>
      </CardHeader>
      <CardContent className="grid gap-5">
        <div className="grid gap-3">
          <div className="grid grid-cols-2 gap-3 max-[640px]:grid-cols-1">
            <CompactStat label="默认估算扣减" value={`${calibrations?.default_overhead || config.usage.system_prompt_overhead_tokens} tokens`} />
            <CompactStat label="纯净模式状态" value={config.pure_mode ? "已开启" : "未开启"} />
          </div>
          <div className="rounded-lg border bg-muted/30 p-3">
            <div className="text-xs font-medium text-muted-foreground">Profile Fingerprint</div>
            <div className="mt-1 break-all font-mono text-xs">{fingerprint || "unknown"}</div>
          </div>
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
                <th>容量 / RPM</th>
                <th>亲和</th>
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
                      {event.in_flight ?? event.capacity_used ?? 0}/{event.concurrency_limit ?? event.capacity_limit ?? 0} · {event.rpm_used ?? 0}/{event.rpm_limit ?? 0}
                    </td>
                    <td>{event.affinity_mode ? `${event.affinity_mode}${event.backup_lane ? " · 备用" : event.primary_hit ? " · 主账号" : ""}` : "-"}</td>
                    <td className="max-w-80 break-words">{event.reason || event.error || "-"}</td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={7} className="text-center text-muted-foreground">
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
  help,
  checked,
  onChange
}: {
  label: string;
  help?: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  const inputID = useId();
  return (
    <div className="flex min-h-11 items-center gap-2 rounded-lg border bg-card px-3 py-2 text-sm">
      <input id={inputID} type="checkbox" className="h-4 w-4" checked={checked} onChange={(event) => onChange(event.target.checked)} />
      <label htmlFor={inputID} className="cursor-pointer">{label}</label>
      {help ? <HelpTip label={label} content={help} /> : null}
    </div>
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

function NumberField({ label, help, value, onChange }: { label: string; help?: string; value: number; onChange: (value: number) => void }) {
  return (
    <Field label={label} help={help}>
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

function defaultPoolEffectiveConfig(): ClaudeCodePoolEffectiveConfig {
  return {
    enabled: true,
    pure_mode: true,
    allow_client_cache_ttl: false,
    cloak: {
      mode: "auto",
      strict_mode: false,
      sensitive_words: []
    },
    usage: {
      clean_input_tokens: true,
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
    routing: {
      per_account_rpm: 6,
      per_account_concurrency: 1,
      sticky_concurrency_reserve: 1,
      max_sessions: 30,
      sticky_wait_ms: 2000,
      fallback_wait_ms: 500,
	      max_waiters_per_account: 5,
      max_waiters_global: 200,
	      session_affinity_ttl_ms: 3600000,
	      active_session_idle_ttl_ms: 300000,
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
    allow_client_cache_ttl: config.allow_client_cache_ttl,
    cloak: {
      mode: config.cloak.mode,
      "strict-mode": config.cloak.strict_mode,
      "sensitive-words": config.cloak.sensitive_words
    },
    usage: {
      clean_input_tokens: config.pure_mode,
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
    routing: {
      "per-account-rpm": config.routing.per_account_rpm,
      "per-account-concurrency": config.routing.per_account_concurrency,
      "sticky-concurrency-reserve": config.routing.sticky_concurrency_reserve,
      "max-sessions": config.routing.max_sessions,
      "sticky-wait-ms": config.routing.sticky_wait_ms,
      "fallback-wait-ms": config.routing.fallback_wait_ms,
      "max-waiters-per-account": config.routing.max_waiters_per_account,
      "max-waiters-global": config.routing.max_waiters_global,
	      "session-affinity-ttl-ms": config.routing.session_affinity_ttl_ms,
	      "active-session-idle-ttl-ms": config.routing.active_session_idle_ttl_ms,
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
  const [searchQuery, setSearchQuery] = useState("");
  const [filter, setFilter] = useState<"all" | "healthy" | "unhealthy" | "available" | "bound" | "reserved" | "disabled">("all");
  const [testResults, setTestResults] = useState<Record<string, { tone: "success" | "danger"; message: string }>>({});
  const selectedSet = useMemo(() => new Set(selectedIDs), [selectedIDs]);
  const selectedCount = selectedIDs.length;
  const filteredProxies = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    return proxies.filter((proxy) => {
      const matchesQuery = !query || [proxy.name, proxy.exit_ip, proxy.bound_account_email, proxy.proxy_url_preview]
        .some((value) => (value || "").toLowerCase().includes(query));
      if (!matchesQuery) {
        return false;
      }
      if (filter === "healthy") return proxy.enabled && proxy.health_status === "healthy";
      if (filter === "unhealthy") return proxy.enabled && proxy.health_status === "unhealthy";
      if (filter === "available") return proxy.enabled && proxy.health_status === "healthy" && !proxy.bound_account_id && !proxy.reserved;
      if (filter === "bound") return Boolean(proxy.bound_account_id);
      if (filter === "reserved") return proxy.reserved;
      if (filter === "disabled") return !proxy.enabled;
      return true;
    });
  }, [filter, proxies, searchQuery]);
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
    onSuccess: async (data, id) => {
      setTestResults((current) => ({
        ...current,
        [id]: { tone: data.warning ? "danger" : "success", message: data.warning || "测试通过" }
      }));
      await onDone();
    },
    onError: (error, id) => setTestResults((current) => ({ ...current, [id]: { tone: "danger", message: errorMessage(error) } }))
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
        header: "出口 IP",
        cell: ({ row }) => (
          <div className="max-w-52">
            <div className="font-mono text-xs">{row.original.exit_ip || "-"}</div>
            <div className="mt-1 truncate text-xs text-muted-foreground" title={row.original.proxy_url_preview || row.original.proxy_url}>{row.original.proxy_url_preview || row.original.proxy_url}</div>
          </div>
        )
      },
      {
        header: "状态",
        cell: ({ row }) => (
          <div className="flex flex-wrap gap-1.5">
            <HealthBadge status={row.original.health_status} enabled={row.original.enabled} />
            {row.original.reserved ? <Badge tone="warning">登录预留</Badge> : null}
          </div>
        )
      },
      {
        header: "失败",
        accessorKey: "consecutive_failures"
      },
      {
        header: "绑定账号",
        cell: ({ row }) =>
          row.original.bound_account_email ||
          (row.original.reserved ? (
            <span className="text-amber-700" title={row.original.reserved_until ? `预留至 ${formatTime(row.original.reserved_until)}` : undefined}>登录预留</span>
          ) : (
            <span className="text-muted-foreground">空闲</span>
          ))
      },
      {
        header: "最后测试",
        cell: ({ row }) => formatTime(row.original.last_checked_at)
      },
      {
        header: "最近结果",
        cell: ({ row }) => {
          const result = testResults[row.original.id];
          return (
            <span className={cn("block max-w-72 truncate text-xs", result?.tone === "success" ? "text-emerald-700" : result?.tone === "danger" ? "text-red-700" : "text-muted-foreground")} title={result?.message || row.original.last_error || ""}>
              {result?.message || row.original.last_error || "-"}
            </span>
          );
        }
      },
      {
        header: "操作",
        cell: ({ row }) => {
          const proxy = row.original;
          return (
            <div className="flex items-center gap-1">
              <Button variant="outline" size="icon" className="h-8 w-8" onClick={() => testMutation.mutate(proxy.id)} disabled={testMutation.isPending && testMutation.variables === proxy.id} title="测试代理" aria-label="测试代理">
                {testMutation.isPending && testMutation.variables === proxy.id ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Play className="h-3.5 w-3.5" />}
              </Button>
              <OverflowMenu label="代理操作">
                <button type="button" onClick={() => onEdit(proxy)}>编辑代理</button>
                <button type="button" onClick={() => proxyMutation.mutate({ id: proxy.id, enabled: !proxy.enabled })}>{proxy.enabled ? "禁用代理" : "启用代理"}</button>
                <button type="button" onClick={() => unbindMutation.mutate(proxy.id)} disabled={!proxy.bound_account_id}>解绑账号</button>
                <button type="button" className="danger" onClick={() => { if (window.confirm("确认删除这个代理资源？已绑定账号会自动解绑。")) deleteMutation.mutate(proxy.id); }}>删除代理</button>
              </OverflowMenu>
            </div>
          );
        }
      }
    ],
    [deleteMutation, onEdit, proxyMutation, selectedSet, testMutation, testResults, unbindMutation]
  );
  const table = useReactTable({ data: filteredProxies, columns, getCoreRowModel: getCoreRowModel() });

  if (loading) {
    return <LoadingPanel />;
  }

  return (
    <div className="grid gap-3">
      <div className="workspace-toolbar grid gap-3 rounded-lg border bg-card p-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <label className="relative min-w-[240px] flex-1 max-w-lg">
            <Search className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
            <Input value={searchQuery} onChange={(event) => setSearchQuery(event.target.value)} placeholder="搜索名称、出口 IP、绑定账号" className="pl-9 pr-9" />
            {searchQuery ? <button type="button" className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-1 text-muted-foreground hover:bg-muted" onClick={() => setSearchQuery("")} aria-label="清空搜索"><X className="h-3.5 w-3.5" /></button> : null}
          </label>
          <span className="text-xs text-muted-foreground">{filteredProxies.length} / {proxies.length} 个代理</span>
        </div>
        <div className="filter-tabs flex gap-1 overflow-x-auto pb-0.5">
          {([
            ["all", "全部"], ["healthy", "健康"], ["unhealthy", "异常"], ["available", "可用"], ["bound", "已绑定"], ["reserved", "登录预留"], ["disabled", "已禁用"]
          ] as const).map(([value, label]) => (
            <button key={value} type="button" className={cn(filter === value && "active")} onClick={() => setFilter(value)}>{label}</button>
          ))}
        </div>
      </div>

      {selectedCount > 0 ? <ProxyBatchToolbar selectedCount={selectedCount} pending={batchMutation.isPending} onRun={runBatch} onClear={clearSelection} /> : null}

      {proxies.length > 0 ? <DataTable table={table} empty="没有匹配的代理" /> : <EmptyState title="暂无代理" description="新增代理后，健康检查会按配置周期自动测试。" />}
    </div>
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
    <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-primary/25 bg-primary/5 px-3 py-2">
        <div className="text-sm font-medium">已选择 {selectedCount} 个代理</div>
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
    </div>
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
  pools,
  available,
  onClose,
  onRefresh,
  onDone,
  onToast
}: {
  modal: ModalState;
  pools: ClaudeCodeAccountPool[];
  available: ProxyResource[];
  onClose: () => void;
  onRefresh: () => Promise<void>;
  onDone: (message: string) => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  return (
    <Dialog open={modal !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className={modal?.type === "oauth" ? "max-w-5xl" : modal?.type === "test-account" ? "max-w-4xl p-0" : modal?.type === "proxy" ? "drawer-panel max-w-[560px] rounded-none border-y-0 border-r-0" : undefined}>
        {modal?.type === "oauth" ? (
          <AddAccountForm poolID={modal.poolID} poolName={pools.find((pool) => pool.id === modal.poolID)?.name || modal.poolID} available={available} onDone={onDone} onToast={onToast} />
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

function AddAccountForm({
  poolID,
  poolName,
  available,
  onDone,
  onToast
}: {
  poolID: string;
  poolName: string;
  available: ProxyResource[];
  onDone: (message: string) => Promise<void>;
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const [tab, setTab] = useState<"oauth" | "session-key">("oauth");
  return (
    <div className="grid min-w-0 gap-5">
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <UserRoundCog className="h-5 w-5 text-primary" />
          新增 Claude Code 账号
        </DialogTitle>
        <DialogDescription>选择浏览器 OAuth，或使用现有网页会话批量完成 OAuth 授权。</DialogDescription>
      </DialogHeader>
      <div className="flex min-w-0 flex-wrap items-center justify-between gap-2 rounded-md border bg-muted/40 px-3 py-2 text-sm">
        <span className="text-muted-foreground">目标账号池</span>
        <span className="min-w-0 text-right"><strong>{poolName}</strong><code className="ml-2 break-all text-xs text-muted-foreground">{poolID}</code></span>
      </div>
      <div className="grid grid-cols-2 rounded-lg bg-muted p-1" role="tablist" aria-label="账号登录方式">
        <button
          type="button"
          role="tab"
          aria-selected={tab === "oauth"}
          className={cn("min-h-10 rounded-md px-3 text-sm font-medium", tab === "oauth" ? "bg-card shadow-sm" : "text-muted-foreground hover:text-foreground")}
          onClick={() => setTab("oauth")}
        >
          OAuth 登录
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={tab === "session-key"}
          className={cn("min-h-10 rounded-md px-3 text-sm font-medium", tab === "session-key" ? "bg-card shadow-sm" : "text-muted-foreground hover:text-foreground")}
          onClick={() => setTab("session-key")}
        >
          SessionKey 批量
        </button>
      </div>
      {tab === "oauth" ? (
        <OAuthForm poolID={poolID} available={available} onDone={onDone} onToast={onToast} />
      ) : (
        <SessionKeyBatchForm poolID={poolID} available={available} onToast={onToast} />
      )}
    </div>
  );
}

function SessionKeyBatchForm({
  poolID,
  available,
  onToast
}: {
  poolID: string;
  available: ProxyResource[];
  onToast: (message: string, tone?: ToastState["tone"]) => void;
}) {
  const queryClient = useQueryClient();
  const previousJobStatus = useRef<string | undefined>(undefined);
  const [input, setInput] = useState("");
  const [concurrency, setConcurrency] = useState(2);
  const jobQuery = useQuery({
    queryKey: ["session-key-job"],
    queryFn: api.currentSessionKeyJob,
    refetchInterval: (query) => {
      const status = query.state.data?.job?.status;
      return status === "queued" || status === "running" || status === "cancelling" ? 2_000 : false;
    }
  });
  const job = jobQuery.data?.job || null;
  const active = job?.status === "queued" || job?.status === "running" || job?.status === "cancelling";
  const healthyAvailable = available.filter((proxy) => proxy.enabled && proxy.health_status === "healthy" && !proxy.reserved).length;
  const parsedKeys = input.split(/\r?\n/).map((line) => line.trim()).filter(Boolean);

  const startMutation = useMutation({
    mutationFn: () => api.createSessionKeyJob(parsedKeys, concurrency, poolID),
    onSuccess: async (data) => {
      setInput("");
      queryClient.setQueryData(["session-key-job"], data);
      await queryClient.invalidateQueries({ queryKey: ["available-proxies"] });
      onToast("SessionKey 批量登录任务已开始");
    },
    onError: (error) => onToast(`启动批量登录失败：${errorMessage(error)}`, "danger")
  });
  const cancelMutation = useMutation({
    mutationFn: () => (job ? api.cancelSessionKeyJob(job.id) : Promise.reject(new Error("没有运行中的任务"))),
    onSuccess: (data) => {
      queryClient.setQueryData(["session-key-job"], data);
      onToast("已停止领取待登录条目");
    },
    onError: (error) => onToast(`取消任务失败：${errorMessage(error)}`, "danger")
  });

  useEffect(() => {
    const previous = previousJobStatus.current;
    previousJobStatus.current = job?.status;
    if (!job || (job.status !== "completed" && job.status !== "cancelled") || (previous !== "queued" && previous !== "running" && previous !== "cancelling")) {
      return;
    }
    onToast(`批量登录完成：新增 ${job.succeeded}，更新 ${job.updated}，失败 ${job.failed + job.no_proxy}。`);
    void Promise.all([
      queryClient.invalidateQueries({ queryKey: ["accounts"] }),
      queryClient.invalidateQueries({ queryKey: ["proxies"] }),
      queryClient.invalidateQueries({ queryKey: ["available-proxies"] }),
      queryClient.invalidateQueries({ queryKey: ["account-pool-stats"] })
    ]);
  }, [job?.id, job?.status]);

  const completed = job ? Math.max(0, job.total - job.queued - job.running) : 0;
  const progress = job && job.total > 0 ? (completed / job.total) * 100 : 0;
  const inputError = parsedKeys.length > 100 ? "每批最多 100 条" : parsedKeys.some((key) => key.length > 4096) ? "单条最长 4096 字符" : "";

  return (
    <div className="grid min-w-0 gap-5">
      <div className="grid grid-cols-[minmax(0,1fr)_10rem] gap-4 max-[640px]:grid-cols-1">
        <Field label="SessionKey（一行一个）">
          <Textarea
            value={input}
            onChange={(event) => setInput(event.target.value)}
            rows={7}
            maxLength={409700}
            spellCheck={false}
            autoComplete="off"
            placeholder={"sk-ant-sid...\nsk-ant-sid..."}
            disabled={active || startMutation.isPending}
            className="min-h-44 resize-y font-mono text-xs"
          />
        </Field>
        <div className="grid content-start gap-4">
          <Field label="登录并发">
            <Input
              type="number"
              min={1}
              max={5}
              value={concurrency}
              onChange={(event) => setConcurrency(Math.max(1, Math.min(5, Number(event.target.value) || 1)))}
              disabled={active}
            />
          </Field>
          <div className="rounded-lg border bg-muted/30 px-3 py-3">
            <div className="text-xs text-muted-foreground">健康空闲代理</div>
            <div className="mt-1 text-2xl font-semibold">{healthyAvailable}</div>
          </div>
        </div>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="text-sm text-muted-foreground">
          {inputError ? <span className="text-destructive">{inputError}</span> : `已输入 ${parsedKeys.length} 条`}
        </div>
        <div className="flex flex-wrap gap-2">
          {active ? (
            <Button type="button" variant="destructive" onClick={() => cancelMutation.mutate()} disabled={cancelMutation.isPending || job?.status === "cancelling"}>
              {cancelMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <ShieldAlert className="h-4 w-4" />}
              停止任务
            </Button>
          ) : null}
          <Button
            type="button"
            onClick={() => startMutation.mutate()}
            disabled={active || startMutation.isPending || parsedKeys.length === 0 || Boolean(inputError)}
          >
            {startMutation.isPending ? <Loader2 className="h-4 w-4 animate-spin" /> : <Play className="h-4 w-4" />}
            开始批量登录
          </Button>
        </div>
      </div>

      {job ? (
        <section className="grid min-w-0 gap-4 border-t pt-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="flex items-center gap-2 font-semibold">
                {active ? <Loader2 className="h-4 w-4 animate-spin text-primary" /> : <CheckCircle2 className="h-4 w-4 text-emerald-600" />}
                批量任务
                <SessionKeyJobBadge status={job.status} />
              </div>
              <div className="mt-1 font-mono text-xs text-muted-foreground">{job.id}</div>
            </div>
            <div className="text-sm text-muted-foreground">{completed} / {job.total}</div>
          </div>
          <Progress value={progress} />
          <div className="grid grid-cols-5 gap-2 max-[768px]:grid-cols-3 max-[480px]:grid-cols-2">
            <JobMetric label="新增" value={job.succeeded} tone="text-emerald-700" />
            <JobMetric label="更新" value={job.updated} tone="text-blue-700" />
            <JobMetric label="失败" value={job.failed} tone="text-red-700" />
            <JobMetric label="无代理" value={job.no_proxy} tone="text-amber-700" />
            <JobMetric label="运行中" value={job.running + job.queued} tone="text-foreground" />
          </div>
          <div className="max-h-72 overflow-auto rounded-lg border">
            <table className="w-full min-w-[720px] border-collapse text-sm">
              <thead className="sticky top-0 bg-muted text-left text-xs text-muted-foreground">
                <tr>
                  <th className="px-3 py-2">序号</th>
                  <th className="px-3 py-2">指纹</th>
                  <th className="px-3 py-2">代理</th>
                  <th className="px-3 py-2">账号</th>
                  <th className="px-3 py-2">状态</th>
                  <th className="px-3 py-2">结果</th>
                </tr>
              </thead>
              <tbody>
                {job.items.map((item) => (
                  <tr key={`${job.id}-${item.index}`} className="border-t align-top">
                    <td className="px-3 py-2 tabular-nums">{item.index}</td>
                    <td className="px-3 py-2 font-mono text-xs">{item.fingerprint}</td>
                    <td className="max-w-44 break-words px-3 py-2">{item.proxy_name || item.proxy_exit_ip || "-"}</td>
                    <td className="max-w-52 break-all px-3 py-2">{item.account_email || "-"}</td>
                    <td className="px-3 py-2"><SessionKeyJobBadge status={item.status} /></td>
                    <td className="max-w-64 break-words px-3 py-2 text-xs text-muted-foreground">{item.error_message || "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : jobQuery.isLoading ? (
        <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />读取任务状态</div>
      ) : null}
    </div>
  );
}

function JobMetric({ label, value, tone }: { label: string; value: number; tone: string }) {
  return (
    <div className="rounded-lg border bg-card px-3 py-2">
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className={cn("mt-1 text-lg font-semibold tabular-nums", tone)}>{value}</div>
    </div>
  );
}

function SessionKeyJobBadge({ status }: { status: string }) {
  const labels: Record<string, string> = {
    queued: "排队", running: "运行中", cancelling: "正在停止", completed: "已完成", cancelled: "已取消",
    success: "新增", updated: "更新", failed: "失败", no_proxy: "无代理", duplicate_input: "重复输入",
    duplicate_account: "重复账号", invalid_format: "格式无效"
  };
  const tone = status === "success" || status === "updated" || status === "completed"
    ? "success"
    : status === "failed" || status === "invalid_format"
      ? "danger"
      : status === "running" || status === "queued" || status === "cancelling"
        ? "warning"
        : "neutral";
  return <Badge tone={tone}>{labels[status] || status}</Badge>;
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
  poolID,
  available,
  onDone,
  onToast
}: {
  poolID: string;
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
      params.set("pool_id", poolID || "default");
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

function Field({
  label,
  help,
  className,
  htmlFor,
  children
}: {
  label: string;
  help?: string;
  className?: string;
  htmlFor?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("grid gap-2", className)}>
      <div className="flex min-w-0 items-center gap-1.5">
        <Label htmlFor={htmlFor}>{label}</Label>
        {help ? <HelpTip label={label} content={help} /> : null}
      </div>
      {children}
    </div>
  );
}

function HelpTip({ label, content }: { label: string; content: string }) {
  const tooltipID = useId();
  const buttonRef = useRef<HTMLButtonElement>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const [mode, setMode] = useState<"closed" | "hover" | "pinned">("closed");
  const [position, setPosition] = useState({ left: 12, top: 12 });
  const open = mode !== "closed";

  useLayoutEffect(() => {
    if (!open) {
      return;
    }
    const updatePosition = () => {
      const button = buttonRef.current;
      const tooltip = tooltipRef.current;
      if (!button || !tooltip) {
        return;
      }
      const buttonRect = button.getBoundingClientRect();
      const width = tooltip.offsetWidth;
      const height = tooltip.offsetHeight;
      const left = Math.min(Math.max(12, buttonRect.left + buttonRect.width / 2 - width / 2), window.innerWidth - width - 12);
      const below = buttonRect.bottom + 8;
      const top = below + height <= window.innerHeight - 12 ? below : Math.max(12, buttonRect.top - height - 8);
      setPosition({ left, top });
    };
    updatePosition();
    window.addEventListener("resize", updatePosition);
    window.addEventListener("scroll", updatePosition, true);
    return () => {
      window.removeEventListener("resize", updatePosition);
      window.removeEventListener("scroll", updatePosition, true);
    };
  }, [open]);

  useEffect(() => {
    if (mode !== "pinned") {
      return;
    }
    const closeOnOutsidePress = (event: Event) => {
      const target = event.target as Node;
      if (!buttonRef.current?.contains(target) && !tooltipRef.current?.contains(target)) {
        setMode("closed");
      }
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setMode("closed");
        buttonRef.current?.focus();
      }
    };
    document.addEventListener("pointerdown", closeOnOutsidePress);
    document.addEventListener("keydown", closeOnEscape);
    return () => {
      document.removeEventListener("pointerdown", closeOnOutsidePress);
      document.removeEventListener("keydown", closeOnEscape);
    };
  }, [mode]);

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`查看“${label}”说明`}
        aria-describedby={open ? tooltipID : undefined}
        aria-expanded={open}
        onMouseEnter={() => setMode((current) => current === "closed" ? "hover" : current)}
        onMouseLeave={() => setMode((current) => current === "hover" ? "closed" : current)}
        onFocus={() => setMode((current) => current === "closed" ? "hover" : current)}
        onBlur={() => setMode((current) => current === "hover" ? "closed" : current)}
        onClick={() => setMode((current) => current === "pinned" ? "closed" : "pinned")}
      >
        <CircleHelp className="h-4 w-4" aria-hidden="true" />
      </button>
      {open ? createPortal(
        <div
          ref={tooltipRef}
          id={tooltipID}
          role="tooltip"
          className="fixed z-[100] w-[min(19rem,calc(100vw-1.5rem))] rounded-md border border-slate-700 bg-slate-900 px-3 py-2.5 text-left text-xs font-normal leading-5 text-white shadow-lg"
          style={position}
        >
          <div className="mb-0.5 font-medium text-white">{label}</div>
          <div className="text-slate-200">{content}</div>
        </div>,
        document.body
      ) : null}
    </>
  );
}

function AccountStatusBadge({ account, runtime }: Pick<AccountRow, "account" | "runtime">) {
  if (!account.schedulable) {
    return <Badge tone="neutral">暂停调度</Badge>;
  }
  if (account.health_status === "checking") {
    return <Badge tone="warning">检查中</Badge>;
  }
  if (account.health_status === "manual_recovery") {
    return <Badge tone="danger">需要处理</Badge>;
  }
  if (account.health_status === "temporarily_blocked") {
    return <Badge tone="warning">临时冷却</Badge>;
  }
  const status = runtime?.status || "active";
  if (status === "active") {
    return <Badge tone={account.effective_schedulable ? "success" : "warning"}>{account.effective_schedulable ? "可调度" : "暂不可用"}</Badge>;
  }
  if (status === "disabled") {
    return <Badge tone="neutral">暂停调度</Badge>;
  }
  return <Badge tone="warning">{status}</Badge>;
}

function QuotaBandBadge({ account }: { account: ClaudeCodeAccount }) {
  if (account.quota_freshness !== "fresh") {
    return null;
  }
  if (account.shared_quota_band === "exhausted") {
    return <Badge tone="danger">共享额度耗尽</Badge>;
  }
  if (account.shared_quota_band === "drain_only") {
    return <Badge tone="neutral">共享额度偏低</Badge>;
  }
  if (account.quota_band === "exhausted") {
    return <Badge tone="warning">模型额度受限</Badge>;
  }
  if (account.quota_band === "drain_only") {
    return <Badge tone="neutral">额度偏低</Badge>;
  }
  if (account.quota_band === "degraded") {
    return <Badge tone="neutral">额度偏低</Badge>;
  }
  return null;
}

function quotaBandText(band?: string) {
  switch (band) {
    case "normal": return "额度正常";
    case "degraded": return "额度偏低";
    case "drain_only": return "额度偏低";
    case "exhausted": return "额度耗尽";
    default: return "额度未知";
  }
}

function quotaWindowStatusText(status?: string) {
  switch (String(status || "").toLowerCase()) {
    case "allowed": return "可用";
    case "rejected": return "已拒绝";
    case "exhausted": return "已耗尽";
    default: return status || "";
  }
}

function quotaWindowUtilizationKnown(window?: AccountQuota["windows"][number]) {
  if (!window) {
    return false;
  }
  if (typeof window.utilization_known === "boolean") {
    return window.utilization_known;
  }
  if (window.source === "oauth_usage") {
    return true;
  }
  return window.used_percent > 0 || (window.remain_percent > 0 && window.remain_percent < 100);
}

function quotaWindowExplicitlyExhausted(window?: AccountQuota["windows"][number]) {
  if (!window) {
    return false;
  }
  const status = String(window.status || "").toLowerCase();
  return status === "rejected" || status === "exhausted" || (typeof window.remaining === "number" && window.remaining <= 0);
}

function quotaWindowValueText(window?: AccountQuota["windows"][number], missing = "未知") {
  if (!window) {
    return missing;
  }
  if (quotaWindowExplicitlyExhausted(window)) {
    return "已耗尽";
  }
  if (quotaWindowUtilizationKnown(window)) {
    return formatPercent(window.remain_percent);
  }
  return "已观察";
}

function quotaWindowTitle(label: string, window?: AccountQuota["windows"][number], shared?: AccountQuota["windows"][number]) {
  if (!window) {
    return shared ? `${label} 未返回独立额度，使用共享 7 天窗口` : `${label} 暂无额度数据`;
  }
  const details = [`${label} ${quotaWindowValueText(window)}`];
  if (!quotaWindowUtilizationKnown(window)) {
    details.push("上游未返回可计算的百分比");
  }
  if (window.status) {
    details.push(quotaWindowStatusText(window.status));
  }
  if (window.resets_at) {
    details.push(`重置 ${formatTime(window.resets_at)}`);
  }
  return details.filter(Boolean).join(" · ");
}

function quotaWindowTextTone(window?: AccountQuota["windows"][number]) {
  if (quotaWindowExplicitlyExhausted(window) || (quotaWindowUtilizationKnown(window) && (window?.remain_percent ?? 100) <= 0)) {
    return "text-red-700";
  }
  if (!quotaWindowUtilizationKnown(window)) {
    return "text-muted-foreground";
  }
  if ((window?.remain_percent ?? 100) <= 15) {
    return "text-amber-700";
  }
  return "text-foreground";
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
