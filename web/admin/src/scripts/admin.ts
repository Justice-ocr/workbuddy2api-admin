declare global {
  interface Window {
    lucide?: { createIcons: (options?: unknown) => void };
    spine?: { SpinePlayer: new (id: string, options: Record<string, unknown>) => unknown };
  }
}

type Data = Record<string, any>;
type Account = Data & { uid: string; nickname?: string; realm?: string; credits?: number; disabled?: boolean; cooling?: boolean; cool_remaining_sec?: number; in_flight?: number };

const $ = <T extends HTMLElement>(id: string) => document.getElementById(id) as T;
const root = document.documentElement;
const settingsKey = "wb-firefly-settings";
let settings = { theme: "system", hue: 165, wallpaper: true, pio: true, motion: true };
let token = "";
let generation = 0;
let accounts: Account[] = [];
let models: Data[] = [];
let lastActivity = Date.now();
let usagePage = 1;
let oauthID = "";
let oauthAttempt = 0;
let oauthTimer = 0;
let deleteTarget: { uid: string; confirmation: string } | null = null;
let toastTimer = 0;

try { settings = { ...settings, ...JSON.parse(localStorage.getItem(settingsKey) || "{}") }; } catch {}

const icons = () => window.lucide?.createIcons({ attrs: { "aria-hidden": "true" } });
const node = <K extends keyof HTMLElementTagNameMap>(tag: K, text?: string, className?: string) => {
  const element = document.createElement(tag);
  if (text !== undefined) element.textContent = text;
  if (className) element.className = className;
  return element;
};
const badge = (text: string, className = "") => node("span", text, `badge ${className}`.trim());
const formatNumber = (value: number) => Number(value || 0).toLocaleString("zh-CN");
const formatCompact = (value: number) => new Intl.NumberFormat("zh-CN", { notation: "compact", maximumFractionDigits: 2 }).format(value || 0);
const formatDate = (value: string) => new Date(value).toLocaleString("zh-CN", { timeZone: "Asia/Shanghai", hour12: false });
const uptime = (seconds: number) => `${Math.floor(seconds / 86400)} 天 ${Math.floor(seconds / 3600) % 24} 小时`;

function iconButton(name: string, label: string, action: () => void | Promise<void>) {
  const button = node("button", undefined, "icon-button");
  button.type = "button";
  button.title = label;
  button.setAttribute("aria-label", label);
  const icon = node("i");
  icon.dataset.lucide = name;
  button.append(icon);
  button.addEventListener("click", action);
  return button;
}

function toast(message: string) {
  $("toast").textContent = message;
  $("toast").hidden = false;
  clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => { $("toast").hidden = true; }, 4500);
}

async function api(path: string, body?: Data) {
  const stamp = generation;
  const response = await fetch(`/api/${path}`, {
    method: body === undefined ? "GET" : "POST",
    cache: "no-store",
    credentials: "omit",
    redirect: "error",
    signal: AbortSignal.timeout(145000),
    headers: { Authorization: `Bearer ${token}`, "X-Admin-Request": "1", "Content-Type": "application/json" },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const data = await response.json().catch(() => ({ error: "服务响应无效" }));
  if (stamp !== generation) throw new Error("管理台已锁定");
  if (!response.ok) {
    const error = new Error(data.error || "请求失败") as Error & { status?: number };
    error.status = response.status;
    if (response.status === 401 && !$("authDialog").hasAttribute("open")) lock();
    throw error;
  }
  return data;
}

async function busy(button: HTMLButtonElement, action: () => Promise<void>) {
  button.disabled = true;
  try { await action(); } catch (error) { toast(error instanceof Error ? error.message : "请求失败"); }
  finally { button.disabled = false; }
}

function applySettings() {
  const theme = settings.theme === "system" ? (matchMedia("(prefers-color-scheme:dark)").matches ? "dark" : "light") : settings.theme;
  root.dataset.theme = theme;
  root.style.setProperty("--hue", String(settings.hue));
  document.body.classList.toggle("no-wallpaper", !settings.wallpaper);
  document.body.classList.toggle("reduce-motion", !settings.motion);
  $("spineModel").hidden = !settings.pio;
  $("hueValue").textContent = String(settings.hue);
  $("hue").setAttribute("value", String(settings.hue));
  ($("hue") as HTMLInputElement).value = String(settings.hue);
  ($("wallpaperSwitch") as HTMLInputElement).checked = settings.wallpaper;
  ($("pioSwitch") as HTMLInputElement).checked = settings.pio;
  ($("motionSwitch") as HTMLInputElement).checked = settings.motion;
  document.querySelectorAll<HTMLButtonElement>("[data-theme-mode]").forEach(button => button.classList.toggle("active", button.dataset.themeMode === settings.theme));
}

function saveSettings() {
  localStorage.setItem(settingsKey, JSON.stringify(settings));
  applySettings();
}

function cancelOAuth() {
  oauthAttempt++;
  clearTimeout(oauthTimer);
  const id = oauthID;
  oauthID = "";
  if (id && token) api("login/cancel", { id }).catch(() => {});
}

function lock() {
  cancelOAuth();
  for (const id of ["loginDialog", "deleteDialog", "appearanceDialog", "usageDetailDialog"] as const) {
    const dialog = $(id) as HTMLDialogElement;
    if (dialog.open) dialog.close();
  }
  deleteTarget = null;
  token = "";
  generation++;
  accounts = [];
  models = [];
  $("connection").textContent = "未认证";
  $("authState").textContent = "待认证";
  $("serviceState").textContent = "等待认证";
  $("serviceDot").classList.add("gray");
  ["accountRows", "usageRows", "taskRows", "modelRows", "logRows", "recentUsageRows"].forEach(id => $(id).replaceChildren());
  ($("newKey") as HTMLInputElement).value = "";
  ($("repeatKey") as HTMLInputElement).value = "";
  const authDialog = $("authDialog") as HTMLDialogElement;
  if (!authDialog.open) authDialog.showModal();
  ($("adminToken") as HTMLInputElement).value = "";
  ($("adminToken") as HTMLInputElement).focus();
}

function showView(name: string) {
  if (!document.getElementById(`view-${name}`)) name = "overview";
  document.querySelectorAll<HTMLElement>(".view").forEach(view => view.classList.toggle("active", view.id === `view-${name}`));
  document.querySelectorAll<HTMLButtonElement>("[data-view]").forEach(link => link.classList.toggle("active", link.dataset.view === name));
  $("sidebar").classList.remove("open");
  history.replaceState(null, "", `#${name}`);
  if (!token) return;
  const loaders: Record<string, () => Promise<void>> = { overview: refreshDashboard, accounts: refreshOverview, usage: refreshUsage, tasks: refreshOverview, models: refreshModels, system: refreshOverview };
  loaders[name]?.().catch(error => toast(error.message));
}

function renderAccounts() {
  const query = ($("accountSearch") as HTMLInputElement).value.toLowerCase();
  const realm = ($("accountRealm") as HTMLSelectElement).value;
  const status = ($("accountFilter") as HTMLSelectElement).value;
  const list = accounts.filter(account => {
    const state = account.disabled ? "disabled" : account.cooling ? "cooling" : "healthy";
    return (realm === "all" || account.realm === realm) && (status === "all" || state === status) && `${account.uid} ${account.nickname || ""}`.toLowerCase().includes(query);
  });
  $("resultCount").textContent = `${list.length} 个账号`;
  $("accountEmpty").hidden = list.length > 0;
  $("accountRows").replaceChildren();
  for (const account of list) {
    const row = node("tr");
    const identity = node("td");
    identity.append(node("strong", account.nickname || "未命名账号"), node("small", account.uid));
    const realmCell = node("td");
    realmCell.append(badge(account.realm === "global" ? "国际版" : "国内版", account.realm === "global" ? "success" : ""));
    const statusCell = node("td");
    statusCell.append(badge(account.disabled ? "已停用" : account.cooling ? "冷却中" : "可用", account.disabled ? "" : account.cooling ? "warning" : "success"));
    if (account.cooling) statusCell.title = `剩余 ${account.cool_remaining_sec || 0} 秒`;
    const actions = node("td");
    const refresh = iconButton("refresh-cw", "刷新账号", () => busy(refresh, async () => {
      const result = await api("accounts/action", { uid: account.uid, action: "refresh" });
      toast(result.balance_updated ? "凭据和余额已刷新" : "凭据已刷新，余额暂不可用");
      await refreshOverview();
    }));
    const toggle = iconButton(account.disabled ? "play" : "pause", account.disabled ? "恢复账号" : "停用账号", () => busy(toggle, async () => {
      await api("accounts/action", { uid: account.uid, action: account.disabled ? "enable" : "disable" });
      toast(account.disabled ? "账号已恢复" : "账号已停用");
      await refreshOverview();
    }));
    const remove = iconButton("trash-2", "删除账号", () => prepareDelete(account));
    remove.classList.add("danger");
    remove.disabled = !account.disabled || Number(account.in_flight || 0) > 0;
    actions.append(refresh, toggle, remove);
    row.append(identity, realmCell, statusCell, node("td", formatNumber(account.credits || 0)), node("td", String(account.in_flight || 0)), actions);
    $("accountRows").append(row);
  }
  icons();
}

async function refreshOverview() {
  const data = await api("overview");
  accounts = data.accounts || [];
  const globalCount = accounts.filter(account => account.realm === "global").length;
  const cnCount = accounts.length - globalCount;
  $("metricTotal").textContent = String(data.total);
  $("metricRealm").textContent = `国际版 ${globalCount} · 国内版 ${cnCount}`;
  $("sideTotal").textContent = String(data.total);
  $("sideHealthy").textContent = String(data.healthy);
  $("sideUptime").textContent = uptime(data.uptime_seconds || 0);
  $("legendHealthy").textContent = String(data.healthy);
  $("legendCooling").textContent = String(data.cooling);
  $("legendDisabled").textContent = String(data.disabled);
  const percent = data.total ? Math.round(data.healthy / data.total * 100) : 0;
  $("healthPercent").textContent = `${percent}%`;
  $("healthRing").style.background = `conic-gradient(var(--primary) 0 ${percent}%, #d2aa5c ${percent}% ${Math.min(100, percent + (data.cooling / Math.max(1, data.total) * 100))}%, #9aa6a0 0)`;
  $("globalState").textContent = data.global_enabled ? "可用" : "未启用";
  $("globalDot").classList.toggle("gray", !data.global_enabled);
  $("keyState").textContent = data.api_key_configured ? "已配置" : "未配置";
  $("keyDot").classList.toggle("gray", !data.api_key_configured);
  $("keyStatus").textContent = data.api_key_configured ? "已配置" : "未配置";
  $("keyStatus").className = `badge ${data.api_key_configured ? "success" : "warning"}`;
  ($("saveKey") as HTMLButtonElement).disabled = !data.api_key_editable;
  $("keyMessage").textContent = data.api_key_editable ? "" : "当前密钥由环境变量管理，面板不可修改";
  $("serviceState").textContent = "服务运行正常";
  $("serviceDot").classList.remove("gray");
  $("authState").textContent = "已认证";
  const selected = ($("taskAccount") as HTMLSelectElement).value;
  $("taskAccount").replaceChildren();
  for (const account of accounts) {
    const option = node("option", `${account.nickname || account.uid} · ${account.realm === "global" ? "国际版" : "国内版"}`);
    option.setAttribute("value", account.uid);
    $("taskAccount").append(option);
  }
  if (accounts.some(account => account.uid === selected)) ($("taskAccount") as HTMLSelectElement).value = selected;
  renderAccounts();
}

function renderUsageRows(target: HTMLElement, rows: Data[], compact = false) {
  target.replaceChildren();
  for (const record of rows) {
    const row = node("tr");
    const tokens = (record.input_tokens ?? 0) + (record.output_tokens ?? 0);
    const success = record.status >= 200 && record.status < 300 && !record.interrupted;
    if (compact) {
      const result = node("td"); result.append(badge(record.interrupted ? "中断" : String(record.status), success ? "success" : "warning"));
      row.append(node("td", formatDate(record.time)), node("td", record.model), node("td", record.uid || "--"), result, node("td", tokens ? formatNumber(tokens) : "未知"), node("td", `${record.duration_ms}ms`));
    } else {
      row.append(node("td", formatDate(record.time)), node("td", `${record.model} / ${record.uid || "--"}`), node("td", `${record.mode} / ${record.status}${record.interrupted ? " 中断" : ""}`), node("td", `${record.input_tokens ?? "--"} / ${record.output_tokens ?? "--"}`), node("td", record.credit == null ? "--" : String(record.credit)), node("td", `${record.ttfb_ms || "--"} / ${record.duration_ms} ms`));
      row.classList.add("clickable-row");
      row.tabIndex = 0;
      row.title = "查看请求详情";
      const open = () => showUsageDetail(record);
      row.addEventListener("click", open);
      row.addEventListener("keydown", event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); open(); } });
    }
    target.append(row);
  }
}

function usageQuery(page = usagePage, dashboard = false) {
  if (dashboard) return new URLSearchParams({ page: "1", page_size: "50" });
  return new URLSearchParams({
    page: String(page), page_size: ($<HTMLSelectElement>("usagePageSize")).value,
    from: ($<HTMLInputElement>("usageFrom")).value, to: ($<HTMLInputElement>("usageTo")).value,
    uid: ($<HTMLInputElement>("usageUID")).value.trim(), model: ($<HTMLInputElement>("usageModel")).value.trim(),
    result: ($<HTMLSelectElement>("usageResult")).value,
  });
}

async function loadUsage(page = usagePage, dashboard = false) {
  return api(`usage?${usageQuery(page, dashboard)}`);
}

function renderUsageChart(rows: Data[], granularity: string) {
  const chart = $("usageChart");
  chart.replaceChildren();
  $("trendUnit").textContent = granularity === "hour" ? "按小时" : "按日";
  const max = Math.max(1, ...rows.map(item => Number(item.requests || 0)));
  for (const item of rows) {
    const column = node("div", undefined, "bar-column");
    const bars = node("div", undefined, "bar-stack");
    bars.style.height = `${Math.max(5, Number(item.requests || 0) / max * 100)}%`;
    const success = node("span", undefined, "bar-success"); success.style.flex = String(item.success || 0);
    const failed = node("span", undefined, "bar-error"); failed.style.flex = String(item.errors || 0);
    bars.append(success, failed);
    const label = String(item.key || "");
    column.title = `${label} · ${item.requests || 0} 次 · ${formatNumber(item.tokens || 0)} Token`;
    column.append(node("strong", String(item.requests || 0)), bars, node("small", granularity === "hour" ? label.slice(11, 16) : label.slice(5)));
    chart.append(column);
  }
  if (!rows.length) chart.append(node("p", "当前范围暂无趋势数据", "empty compact"));
}

function renderRank(target: HTMLElement, rows: Data[]) {
  target.replaceChildren();
  const max = Math.max(1, ...rows.map(item => Number(item.requests || 0)));
  for (const [index, item] of rows.entries()) {
    const entry = node("div", undefined, "rank-entry");
    const title = node("p"); title.append(node("span", `${index + 1}. ${item.key}`), node("strong", `${item.requests} 次`));
    const track = node("i"); const fill = node("b"); fill.style.width = `${Number(item.requests || 0) / max * 100}%`; track.append(fill);
    entry.append(title, track, node("small", `${formatNumber(item.tokens || 0)} Token · 成功 ${item.success || 0}`));
    target.append(entry);
  }
  if (!rows.length) target.append(node("p", "暂无数据", "empty compact"));
}

function showUsageDetail(record: Data) {
  const fields = [
    ["请求时间", formatDate(record.time)], ["模型", record.model || "--"], ["账号 UID", record.uid || "--"], ["响应模式", record.mode || "--"],
    ["HTTP 状态", String(record.status ?? "--")], ["是否中断", record.interrupted ? "是" : "否"], ["输入 Token", String(record.input_tokens ?? "未知")],
    ["输出 Token", String(record.output_tokens ?? "未知")], ["积分消耗", String(record.credit ?? "未知")], ["首帧耗时", record.ttfb_ms ? `${record.ttfb_ms} ms` : "未知"], ["总耗时", `${record.duration_ms || 0} ms`],
  ];
  $("usageDetail").replaceChildren(...fields.map(([label, value]) => { const item = node("div"); item.append(node("dt", label), node("dd", value)); return item; }));
  ($<HTMLDialogElement>("usageDetailDialog")).showModal();
}

async function refreshUsage() {
  const data = await loadUsage();
  const summary = data.summary || {};
  $("usageTotal").textContent = formatNumber(summary.total);
  $("usageResults").textContent = `成功 ${summary.success || 0} · 失败 ${summary.error || 0}`;
  $("usageSuccessRate").textContent = `${Number(summary.success_rate || 0).toFixed(1)}%`;
  $("usageInterrupted").textContent = `中断 ${summary.interrupted || 0}`;
  $("usageTokens").textContent = formatCompact(summary.total_tokens);
  $("usageTokenSplit").textContent = `输入 ${formatCompact(summary.input_tokens)} · 输出 ${formatCompact(summary.output_tokens)}`;
  $("usageCredit").textContent = Number(summary.known_credit || 0).toFixed(3);
  $("usageUnknownCredit").textContent = `未知 ${summary.unknown_credit || 0} 次`;
  $("usageDuration").textContent = `${formatNumber(summary.avg_duration_ms)} ms`;
  $("usageTTFB").textContent = `首帧 ${formatNumber(summary.avg_ttfb_ms)} ms`;
  $("usageSummary").textContent = `${data.timezone} · 最近优先 · ${data.persisted ? "持久化正常" : "持久化异常"}`;
  renderUsageRows($("usageRows"), data.rows || []);
  $("usageEmpty").hidden = (data.rows || []).length > 0;
  renderUsageChart(data.trend || [], data.trend_granularity);
  renderRank($("modelRank"), data.by_model || []);
  renderRank($("accountRank"), data.by_account || []);
  const pageSize = Number(data.page_size || 50);
  $("usagePage").textContent = `${usagePage} / ${Math.max(1, Math.ceil(summary.total / pageSize))}`;
  ($("usagePrev") as HTMLButtonElement).disabled = usagePage <= 1;
  ($("usageNext") as HTMLButtonElement).disabled = usagePage * pageSize >= summary.total;
}

async function exportUsage() {
  const stamp = generation;
  const response = await fetch(`/api/usage/export?${usageQuery(1, false)}`, { cache: "no-store", credentials: "omit", redirect: "error", headers: { Authorization: `Bearer ${token}`, "X-Admin-Request": "1" } });
  if (stamp !== generation) throw new Error("管理台已锁定");
  if (!response.ok) { const data = await response.json().catch(() => ({})); throw new Error(data.error || "导出失败"); }
  const link = document.createElement("a");
  link.href = URL.createObjectURL(await response.blob());
  link.download = `workbuddy-usage-${($<HTMLInputElement>("usageFrom")).value}-${($<HTMLInputElement>("usageTo")).value}.csv`;
  link.click();
  setTimeout(() => URL.revokeObjectURL(link.href), 1000);
}

async function refreshLogs() {
  const data = await api("logs");
  const entries = (data.entries || []).slice().reverse();
  $("logRows").replaceChildren();
  for (const entry of entries.slice(0, 12)) {
    const row = node("tr");
    row.append(node("td", formatDate(entry.time)), node("td", entry.action), node("td", entry.result));
    $("logRows").append(row);
  }
  $("logEmpty").hidden = entries.length > 0;
  $("sideEvents").replaceChildren();
  for (const entry of entries.slice(0, 3)) {
    const paragraph = node("p");
    paragraph.append(node("time", new Date(entry.time).toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit", hour12: false })), document.createTextNode(` ${entry.action} · ${entry.result}`));
    $("sideEvents").append(paragraph);
  }
  if (!entries.length) $("sideEvents").append(node("p", "暂无运行事件"));
}

async function refreshDashboard() {
  await refreshOverview();
  const [usage] = await Promise.all([loadUsage(1, true), refreshLogs()]);
  const rows = usage.rows || [];
  const input = usage.summary?.input_tokens || 0;
  const output = usage.summary?.output_tokens || 0;
  $("metricRequests").textContent = formatNumber(usage.total);
  $("sideRequests").textContent = formatNumber(usage.total);
  $("metricTokens").textContent = formatCompact(usage.tokens);
  $("metricTokenDetail").textContent = `输入 ${formatCompact(input)} · 输出 ${formatCompact(output)}`;
  $("metricCredit").textContent = Number(usage.credit || 0).toFixed(3);
  $("metricUnknown").textContent = `积分未知 ${usage.unknown_credit} 次`;
  $("persistState").textContent = usage.persisted ? "正常" : "异常";
  $("persistDot").classList.toggle("gray", !usage.persisted);
  renderUsageRows($("recentUsageRows"), rows.slice(0, 6), true);
  $("recentUsageEmpty").hidden = rows.length > 0;
}

function renderModels() {
  const query = ($("modelSearch") as HTMLInputElement).value.toLowerCase();
  const realm = ($("realmFilter") as HTMLSelectElement).value;
  const list = models.filter(model => String(model.id).toLowerCase().includes(query) && (realm === "all" || (realm === "global" ? String(model.id).startsWith("global:") : !String(model.id).startsWith("global:"))));
  $("modelRows").replaceChildren();
  $("modelEmpty").hidden = list.length > 0;
  for (const model of list) {
    const article = node("article");
    const isGlobal = String(model.id).startsWith("global:");
    article.append(node("span", isGlobal ? "GLOBAL" : "CN", `realm ${isGlobal ? "global" : "cn"}`), node("h2", String(model.id).replace(/^global:/, "")), node("code", model.id));
    article.append(iconButton("copy", `复制 ${model.id}`, async () => { try { await navigator.clipboard.writeText(model.id); toast("模型标识已复制"); } catch { toast("当前浏览器不允许复制"); } }));
    $("modelRows").append(article);
  }
  icons();
}

async function refreshModels() {
  const data = await api("models");
  models = data.data || [];
  renderModels();
}

async function beginOAuth() {
  cancelOAuth();
  const dialog = $("loginDialog") as HTMLDialogElement;
  if (!dialog.open) dialog.showModal();
  $("authLink").hidden = true;
  $("retryLogin").hidden = true;
  $("loginStatus").textContent = "正在获取授权入口";
  const attempt = oauthAttempt;
  const realm = ($("loginRealm") as HTMLSelectElement).value;
  try {
    const data = await api("login/start", { realm });
    if (attempt !== oauthAttempt) { api("login/cancel", { id: data.id }).catch(() => {}); return; }
    const url = new URL(data.url);
    const allowed = realm === "global" ? ["www.workbuddy.ai"] : ["copilot.tencent.com", "www.codebuddy.cn"];
    if (url.protocol !== "https:" || !allowed.includes(url.host) || url.username || url.password) throw new Error("授权域名校验失败");
    oauthID = data.id;
    ($("authLink") as HTMLAnchorElement).href = url.href;
    $("authLink").hidden = false;
    $("loginStatus").textContent = "等待浏览器授权";
    pollOAuth(data.id, Date.now() + 900000, 0);
  } catch (error) {
    $("loginStatus").textContent = error instanceof Error ? error.message : "授权入口不可用";
    $("retryLogin").hidden = false;
  }
}

function pollOAuth(id: string, expires: number, failures: number) {
  oauthTimer = window.setTimeout(async () => {
    if (oauthID !== id) return;
    if (Date.now() > expires) { cancelOAuth(); $("loginStatus").textContent = "授权已过期"; $("retryLogin").hidden = false; return; }
    try {
      const data = await api("login/poll", { id });
      if (oauthID !== id) return;
      if (data.done) {
        oauthID = "";
        ($("loginDialog") as HTMLDialogElement).close();
        toast(`账号 ${data.nickname || data.uid} 已添加`);
        await refreshDashboard();
        return;
      }
      $("loginStatus").textContent = "等待浏览器授权";
      pollOAuth(id, expires, 0);
    } catch (error) {
      const status = (error as Error & { status?: number }).status;
      $("loginStatus").textContent = error instanceof Error ? error.message : "查询失败";
      if ([400, 409, 410].includes(status || 0) || failures >= 4) { cancelOAuth(); $("retryLogin").hidden = false; $("authLink").hidden = true; }
      else pollOAuth(id, expires, failures + 1);
    }
  }, 3000);
}

async function prepareDelete(account: Account) {
  const data = await api("accounts/action", { uid: account.uid, action: "delete-confirm" });
  deleteTarget = { uid: account.uid, confirmation: data.confirmation };
  $("deleteAccount").textContent = `${account.nickname || "未命名账号"} / ${account.uid}`;
  ($("deleteUID") as HTMLInputElement).value = "";
  $("deleteError").textContent = "";
  ($("confirmDelete") as HTMLButtonElement).disabled = true;
  ($("deleteDialog") as HTMLDialogElement).showModal();
}

function initSpine() {
  if (!window.spine?.SpinePlayer || !settings.pio || innerWidth <= 600) return;
  try {
    new window.spine.SpinePlayer("spinePlayer", { skeleton: "/pio/models/spine/firefly/1310.json", atlas: "/pio/models/spine/firefly/1310.atlas", animation: "idle", backgroundColor: "#00000000", showControls: false, alpha: true, success: () => { const message = $("pioMessage"); message.hidden = false; setTimeout(() => { message.hidden = true; }, 3000); }, error: () => { $("spineModel").hidden = true; } });
  } catch { $("spineModel").hidden = true; }
}

document.querySelectorAll<HTMLButtonElement>("[data-view]").forEach(link => link.addEventListener("click", () => showView(link.dataset.view || "overview")));
document.querySelectorAll<HTMLButtonElement>(".close-dialog").forEach(button => button.addEventListener("click", () => ($(button.dataset.dialog || "") as HTMLDialogElement).close()));
$("mobileMenu").addEventListener("click", () => $("sidebar").classList.toggle("open"));
$("lockAdmin").addEventListener("click", lock);
document.addEventListener("pointerdown", () => { lastActivity = Date.now(); });
document.addEventListener("keydown", () => { lastActivity = Date.now(); });
setInterval(() => { if (token && Date.now() - lastActivity > 30 * 60 * 1000) lock(); }, 15000);

const authDialog = $("authDialog") as HTMLDialogElement;
authDialog.addEventListener("cancel", event => event.preventDefault());
$("authForm").addEventListener("submit", async event => {
  event.preventDefault();
  const button = $("signIn") as HTMLButtonElement;
  button.disabled = true;
  $("authError").textContent = "";
  token = ($("adminToken") as HTMLInputElement).value;
  ($("adminToken") as HTMLInputElement).value = "";
  try {
    await refreshDashboard();
    authDialog.close();
    $("connection").textContent = "已认证";
    lastActivity = Date.now();
    showView(location.hash.slice(1) || "overview");
  } catch (error) {
    token = "";
    $("authError").textContent = error instanceof Error ? error.message : "认证失败";
  } finally { button.disabled = false; }
});

$("accountSearch").addEventListener("input", renderAccounts);
$("accountRealm").addEventListener("change", renderAccounts);
$("accountFilter").addEventListener("change", renderAccounts);
$("modelSearch").addEventListener("input", renderModels);
$("realmFilter").addEventListener("change", renderModels);
$("refreshOverview").addEventListener("click", event => busy(event.currentTarget as HTMLButtonElement, refreshDashboard));
$("refreshAccounts").addEventListener("click", event => busy(event.currentTarget as HTMLButtonElement, refreshOverview));
$("refreshLogs").addEventListener("click", event => busy(event.currentTarget as HTMLButtonElement, refreshLogs));
$("refreshModels").addEventListener("click", event => busy(event.currentTarget as HTMLButtonElement, refreshModels));
$("refreshUsage").addEventListener("click", event => busy(event.currentTarget as HTMLButtonElement, async () => { usagePage = 1; await refreshUsage(); }));
$("exportUsage").addEventListener("click", event => busy(event.currentTarget as HTMLButtonElement, exportUsage));
$("usagePageSize").addEventListener("change", async () => { usagePage = 1; try { await refreshUsage(); } catch (error) { toast((error as Error).message); } });
$("usagePrev").addEventListener("click", async () => { if (usagePage <= 1) return; usagePage--; try { await refreshUsage(); } catch (error) { usagePage++; toast((error as Error).message); } });
$("usageNext").addEventListener("click", async () => { usagePage++; try { await refreshUsage(); } catch (error) { usagePage--; toast((error as Error).message); } });
$("addAccount").addEventListener("click", () => beginOAuth());
$("retryLogin").addEventListener("click", () => beginOAuth());
$("loginRealm").addEventListener("change", () => beginOAuth());
$("loginDialog").addEventListener("close", cancelOAuth);

$("deleteUID").addEventListener("input", () => { ($("confirmDelete") as HTMLButtonElement).disabled = !deleteTarget || ($("deleteUID") as HTMLInputElement).value !== deleteTarget.uid; });
$("deleteForm").addEventListener("submit", async event => {
  event.preventDefault();
  if (!deleteTarget || ($("deleteUID") as HTMLInputElement).value !== deleteTarget.uid) return;
  ($("confirmDelete") as HTMLButtonElement).disabled = true;
  try { await api("accounts/action", { ...deleteTarget, action: "delete" }); ($("deleteDialog") as HTMLDialogElement).close(); deleteTarget = null; toast("账号和凭据已删除"); await refreshDashboard(); }
  catch (error) { $("deleteError").textContent = `${(error as Error).message}，请关闭后重新确认`; }
});

$("refreshTasks").addEventListener("click", event => busy(event.currentTarget as HTMLButtonElement, async () => {
  const uid = ($("taskAccount") as HTMLSelectElement).value;
  if (!uid) { toast("请先添加账号"); return; }
  $("taskRows").replaceChildren();
  $("taskMessage").textContent = "正在查询";
  const data = await api("tasks", { uid });
  $("taskMessage").textContent = `${data.business_date}（${data.timezone}）· ${data.observation_persisted ? "本地当日观测已保存" : "仅当前快照，观测未持久化"} · 上游未提供完成时间`;
  const labels: Record<string, string> = { not_accepted: "未接受", accepted: "进行中", completed: "已完成", claimed: "已领取" };
  for (const task of data.tasks || []) {
    const status = labels[task.accept_status] || "未知";
    const daily = task.daily_state === "observed_complete" ? "今日已观测完成" : task.daily_state === "observed_incomplete" ? "今日尚未观测完成" : "当日状态未知";
    const article = node("article", undefined, "task-card");
    const observed = task.daily_state === "observed_complete";
    const icon = node("i"); icon.dataset.lucide = observed ? "calendar-check-2" : "list-checks";
    const detail = node("div"); detail.append(node("span", task.name || task.task_code), node("strong", `${task.progress?.current ?? "--"} / ${task.progress?.target ?? "--"}`), node("small", `${daily} · 当前 ${status}`));
    article.append(icon, detail, badge(observed ? "今日完成" : status, observed ? "success" : "warning"));
    $("taskRows").append(article);
  }
  icons();
}));
$("taskAccount").addEventListener("change", () => { $("taskRows").replaceChildren(); $("taskMessage").textContent = "尚未查询"; });

$("keyForm").addEventListener("submit", async event => {
  event.preventDefault();
  const key = ($("newKey") as HTMLInputElement).value;
  if (key !== ($("repeatKey") as HTMLInputElement).value) { $("keyMessage").textContent = "两次输入的密钥不一致"; return; }
  const button = $("saveKey") as HTMLButtonElement;
  await busy(button, async () => {
    await api("api-key", { key, confirm: ($("keyConfirm") as HTMLInputElement).checked });
    ($("keyForm") as HTMLFormElement).reset();
    $("keyMessage").textContent = "原配置已备份，新 API Key 已生效";
    toast("API 密钥已更新");
    await refreshOverview();
  });
  ($("newKey") as HTMLInputElement).value = "";
  ($("repeatKey") as HTMLInputElement).value = "";
});

$("appearanceToggle").addEventListener("click", () => ($("appearanceDialog") as HTMLDialogElement).showModal());
document.querySelectorAll<HTMLButtonElement>("[data-theme-mode]").forEach(button => button.addEventListener("click", () => { settings.theme = button.dataset.themeMode || "system"; saveSettings(); }));
$("hue").addEventListener("input", event => { settings.hue = Number((event.target as HTMLInputElement).value); saveSettings(); });
$("wallpaperSwitch").addEventListener("change", event => { settings.wallpaper = (event.target as HTMLInputElement).checked; saveSettings(); });
$("pioSwitch").addEventListener("change", event => { settings.pio = (event.target as HTMLInputElement).checked; saveSettings(); });
$("motionSwitch").addEventListener("change", event => { settings.motion = (event.target as HTMLInputElement).checked; saveSettings(); });
matchMedia("(prefers-color-scheme:dark)").addEventListener("change", applySettings);

const musicPanel = $("musicPanel");
const audio = $("audio") as HTMLAudioElement;
const progress = $("musicProgress") as HTMLInputElement;
function updatePlayIcon() {
  const icon = node("i");
  icon.dataset.lucide = audio.paused ? "play" : "pause";
  $("playMusic").replaceChildren(icon);
  $("playMusic").setAttribute("aria-label", audio.paused ? "播放" : "暂停");
  document.querySelector(".track")?.classList.toggle("playing", !audio.paused);
  icons();
}
$("musicToggle").addEventListener("click", () => { musicPanel.hidden = !musicPanel.hidden; });
$("closeMusic").addEventListener("click", () => { musicPanel.hidden = true; });
$("playMusic").addEventListener("click", async () => { try { if (audio.paused) await audio.play(); else audio.pause(); } catch { toast("浏览器暂时无法播放音乐"); } updatePlayIcon(); });
$("muteMusic").addEventListener("click", () => { audio.muted = !audio.muted; const icon = node("i"); icon.dataset.lucide = audio.muted ? "volume-x" : "volume-2"; $("muteMusic").replaceChildren(icon); icons(); });
audio.addEventListener("loadedmetadata", () => { $("duration").textContent = formatTime(audio.duration); });
audio.addEventListener("timeupdate", () => { progress.value = audio.duration ? String(audio.currentTime / audio.duration * 100) : "0"; $("currentTime").textContent = formatTime(audio.currentTime); });
audio.addEventListener("ended", updatePlayIcon);
progress.addEventListener("input", () => { if (audio.duration) audio.currentTime = Number(progress.value) / 100 * audio.duration; });
function formatTime(value: number) { return Number.isFinite(value) ? `${Math.floor(value / 60)}:${String(Math.floor(value % 60)).padStart(2, "0")}` : "0:00"; }

$("hidePio").addEventListener("click", () => { settings.pio = false; saveSettings(); });
$("spineModel").addEventListener("click", event => { if ((event.target as Element).closest("button")) return; const message = $("pioMessage"); const messages = ["今天也要保持账号池健康哦", "国际版和国内版都要照顾好", "记得检查积分任务进度"]; message.textContent = messages[Math.floor(Math.random() * messages.length)]; message.hidden = false; setTimeout(() => { message.hidden = true; }, 2800); });

const utc8Today = new Date(Date.now() + 8 * 3600000).toISOString().slice(0, 10);
($<HTMLInputElement>("usageFrom")).value = utc8Today;
($<HTMLInputElement>("usageTo")).value = utc8Today;
applySettings();
showView(location.hash.slice(1) || "overview");
icons();
addEventListener("load", initSpine, { once: true });
lock();

export {};
