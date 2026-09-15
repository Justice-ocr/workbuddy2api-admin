(() => {
  'use strict';
  const $ = id => document.getElementById(id);
  const systemTheme = window.matchMedia('(prefers-color-scheme: dark)');
  let themeMode = 'system', accent = 'mint';
  try {
    const saved = JSON.parse(localStorage.getItem('wb-admin-appearance') || '{}');
    if (['light', 'dark', 'system'].includes(saved.mode)) themeMode = saved.mode;
    if (['mint', 'blue', 'rose'].includes(saved.accent)) accent = saved.accent;
  } catch {}
  function applyAppearance() {
    document.documentElement.dataset.theme = themeMode === 'system' ? (systemTheme.matches ? 'dark' : 'light') : themeMode;
    document.documentElement.dataset.accent = accent;
    document.querySelectorAll('[data-theme-mode]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.themeMode === themeMode)));
    document.querySelectorAll('[data-color]').forEach(b => b.setAttribute('aria-pressed', String(b.dataset.color === accent)));
  }
  function saveAppearance() {
    applyAppearance();
    // Store appearance preferences only; credentials remain in memory.
    try { localStorage.setItem('wb-admin-appearance', JSON.stringify({ mode: themeMode, accent })); } catch {}
  }
  systemTheme.addEventListener('change', applyAppearance);
  document.querySelectorAll('[data-theme-mode]').forEach(b => b.addEventListener('click', () => { themeMode = b.dataset.themeMode; saveAppearance(); }));
  document.querySelectorAll('[data-color]').forEach(b => b.addEventListener('click', () => { accent = b.dataset.color; saveAppearance(); }));
  $('appearance').addEventListener('click', () => $('appearanceDialog').showModal());
  applyAppearance();
  let token = '', accounts = [], models = [], view = 'accounts', generation = 0;
  let oauthID = null, oauthTimer = null, toastTimer = null, deleteTarget = null;
  let lastActivity = Date.now();
  let usagePage = 1;
  let oauthAttempt = 0;
  let usageAttempt = 0;
  $('usageDate').value = new Date(Date.now() + 8 * 3600000).toISOString().slice(0, 10);
  const icons = () => window.lucide.createIcons({ attrs: { 'aria-hidden': 'true' } });
  const node = (tag, text, cls) => {
    const el = document.createElement(tag);
    if (text !== undefined) el.textContent = text;
    if (cls) el.className = cls;
    return el;
  };
  const icon = name => { const el = node('i'); el.dataset.lucide = name; return el; };
  const badge = (text, cls = '') => node('span', text, `badge ${cls}`);
  function iconButton(name, label, action) {
    const b = node('button', undefined, 'icon-button');
    b.type = 'button'; b.title = label; b.setAttribute('aria-label', label);
    b.append(icon(name)); b.addEventListener('click', action); return b;
  }
  function toast(text) {
    $('toast').textContent = text; $('toast').hidden = false;
    clearTimeout(toastTimer); toastTimer = setTimeout(() => { $('toast').hidden = true; }, 5000);
  }
  async function api(path, body) {
    const stamp = generation;
    const res = await fetch(`/api/${path}`, {
      method: body === undefined ? 'GET' : 'POST', cache: 'no-store',
      credentials: 'omit', redirect: 'error', signal: AbortSignal.timeout(145000),
      headers: { Authorization: `Bearer ${token}`, 'X-Admin-Request': '1', 'Content-Type': 'application/json' },
      ...(body === undefined ? {} : { body: JSON.stringify(body) })
    });
    const data = await res.json().catch(() => ({ error: '服务响应无效' }));
    if (stamp !== generation) throw new Error('管理台已锁定');
    if (!res.ok) {
      const e = new Error(data.error || '请求失败'); e.status = res.status;
      if (res.status === 401 && !$('authDialog').open) lock();
      throw e;
    }
    return data;
  }
  async function busy(button, action) {
    button.disabled = true;
    try { await action(); } catch (e) { toast(e.message || '请求失败'); }
    finally { button.disabled = false; }
  }
  function lock() {
    cancelOAuth();
    token = ''; generation++; accounts = []; models = [];
    $('accountRows').replaceChildren(); $('modelRows').replaceChildren(); $('logRows').replaceChildren();
    $('usageRows').replaceChildren(); $('taskRows').replaceChildren();
    $('taskAccount').replaceChildren();
    $('usageSummary').textContent = ''; $('taskMessage').textContent = '';
    $('deleteAccount').textContent = ''; $('deleteUID').value = ''; deleteTarget = null;
    $('authLink').removeAttribute('href');
    $('newKey').value = ''; $('repeatKey').value = ''; $('connection').textContent = '未认证';
    $('deleteDialog').close(); $('loginDialog').close();
    $('appearanceDialog').close();
    if (!$('authDialog').open) $('authDialog').showModal();
    $('adminToken').value = ''; $('adminToken').focus();
  }
  $('authDialog').addEventListener('cancel', e => e.preventDefault());
  $('authForm').addEventListener('submit', async e => {
    e.preventDefault(); $('authError').textContent = ''; $('signIn').disabled = true;
    token = $('adminToken').value; $('adminToken').value = '';
    try {
      await refreshOverview();
      $('authDialog').close(); $('connection').textContent = '管理会话已认证';
      lastActivity = Date.now(); await loadView();
    } catch (err) {
      token = ''; $('authError').textContent = err.message;
    } finally { $('signIn').disabled = false; }
  });
  $('logout').addEventListener('click', lock);
  document.addEventListener('pointerdown', () => { lastActivity = Date.now(); });
  document.addEventListener('keydown', () => { lastActivity = Date.now(); });
  setInterval(() => { if (token && Date.now() - lastActivity > 30 * 60 * 1000) lock(); }, 15000);

  function renderAccounts() {
    const q = $('accountSearch').value.toLowerCase();
    const filter = $('accountFilter').value;
    const list = accounts.filter(a => {
      const status = a.disabled ? 'disabled' : a.cooling ? 'cooling' : 'healthy';
      return (filter === 'all' || status === filter) && `${a.uid} ${a.nickname || ''}`.toLowerCase().includes(q);
    });
    $('resultCount').textContent = `${list.length} 个账号`;
    $('accountEmpty').hidden = list.length > 0;
    $('accountEmpty').querySelector('h2').textContent = accounts.length ? '没有匹配的账号' : '暂无账号';
    $('accountRows').replaceChildren();
    for (const a of list) {
      const tr = node('tr'), identity = node('td'), content = node('div', undefined, 'account-cell');
      const name = a.nickname || '未命名账号';
      content.append(node('span', Array.from(name)[0], 'avatar'));
      const label = node('div');
      label.append(node('div', name, 'account-name'), node('code', a.uid, 'uid'));
      content.append(label); identity.append(content); tr.append(identity);
      const realm = node('td'); realm.append(badge(a.realm === 'global' ? '国际版' : '国内版')); tr.append(realm);
      const status = node('td');
      status.append(badge(a.disabled ? '已停用' : a.cooling ? '冷却中' : '可用', a.disabled ? '' : a.cooling ? 'warning' : 'success'));
      status.title = a.disabled ? '账号已停用' : a.cooling ? `剩余 ${a.cool_remaining_sec || 0} 秒` : '正常调度';
      tr.append(status, node('td', Number(a.credits || 0).toLocaleString('zh-CN'), 'credits'), node('td', String(a.in_flight || 0)));
      const actions = node('td'), bar = node('div', undefined, 'row-actions');
      const refresh = iconButton('refresh-cw', `刷新账号 ${name}`, () => busy(refresh, async () => {
        const result = await api('accounts/action', { uid: a.uid, action: 'refresh' });
        toast(result.balance_updated ? '凭据和余额已刷新' : '凭据已刷新，余额暂时不可用');
        await refreshOverview();
      }));
      const toggle = iconButton(a.disabled ? 'play' : 'pause', `${a.disabled ? '恢复' : '停用'}账号 ${name}`, () => busy(toggle, async () => {
        await api('accounts/action', { uid: a.uid, action: a.disabled ? 'enable' : 'disable' });
        toast(a.disabled ? '账号已恢复' : '账号已停用'); await refreshOverview();
      }));
      const remove = iconButton('trash-2', `删除账号 ${name}`, () => busy(remove, () => prepareDelete(a)));
      remove.classList.add('danger'); remove.disabled = !a.disabled || a.in_flight > 0;
      if (remove.disabled) remove.title = '先停用账号并等待在途请求结束';
      bar.append(refresh, toggle, remove); actions.append(bar); tr.append(actions); $('accountRows').append(tr);
    }
    icons();
  }
  async function refreshOverview() {
    const x = await api('overview'); accounts = x.accounts || [];
    const selected = $('taskAccount').value;
    $('taskAccount').replaceChildren();
    accounts.forEach(a => { const o = node('option', `${a.nickname || a.uid} · ${a.realm}`); o.value = a.uid; $('taskAccount').append(o); });
    if (accounts.some(a => a.uid === selected)) $('taskAccount').value = selected;
    for (const key of ['total', 'healthy', 'cooling', 'disabled']) $(key).textContent = x[key];
    $('navCount').textContent = x.total; renderAccounts();
    $('updatedAt').textContent = `最近更新 ${new Date().toLocaleTimeString('zh-CN', { hour12: false })}`;
    $('globalDot').classList.toggle('gray', !x.global_enabled);
    $('uptime').textContent = `运行 ${Math.floor(x.uptime_seconds / 3600)} 小时 ${Math.floor(x.uptime_seconds / 60) % 60} 分钟`;
    $('keyStatus').textContent = x.api_key_configured ? '已配置' : '未配置';
    $('keyStatus').className = `badge ${x.api_key_configured ? 'success' : 'warning'}`;
    $('saveKey').disabled = !x.api_key_editable;
    if (!x.api_key_editable) $('keyMessage').textContent = '当前密钥由环境变量管理';
  }
  $('accountSearch').addEventListener('input', renderAccounts);
  $('accountFilter').addEventListener('change', renderAccounts);
  $('refreshAccounts').addEventListener('click', e => busy(e.currentTarget, refreshOverview));
  async function prepareDelete(a) {
    const result = await api('accounts/action', { uid: a.uid, action: 'delete-confirm' });
    deleteTarget = { uid: a.uid, confirmation: result.confirmation };
    $('deleteAccount').textContent = `${a.nickname || '未命名账号'} / ${a.uid}`;
    $('deleteUID').value = ''; $('deleteError').textContent = ''; $('confirmDelete').disabled = true;
    $('deleteDialog').showModal(); $('deleteUID').focus();
  }
  $('deleteUID').addEventListener('input', () => {
    $('confirmDelete').disabled = !deleteTarget || $('deleteUID').value !== deleteTarget.uid;
  });
  $('deleteForm').addEventListener('submit', async e => {
    e.preventDefault();
    if (!deleteTarget || $('deleteUID').value !== deleteTarget.uid) return;
    $('confirmDelete').disabled = true;
    try {
      await api('accounts/action', { ...deleteTarget, action: 'delete' });
      $('deleteDialog').close(); deleteTarget = null; toast('账号和凭据已删除'); await refreshOverview();
    } catch (err) { $('deleteError').textContent = `${err.message}，请关闭后重新确认`; }
  });
  function cancelOAuth() {
    oauthAttempt++;
    clearTimeout(oauthTimer);
    const id = oauthID; oauthID = null;
    if (id && token) api('login/cancel', { id }).catch(() => {});
  }
  async function beginOAuth() {
    cancelOAuth(); $('authLink').hidden = true; $('retryLogin').hidden = true;
    $('loginStatus').textContent = '正在获取授权入口';
    if (!$('loginDialog').open) $('loginDialog').showModal();
    const mark = generation;
    const attempt = oauthAttempt;
    try {
      const realm = $('loginRealm').value;
      const x = await api('login/start', { realm });
      if (attempt !== oauthAttempt) { api('login/cancel', {id:x.id}).catch(()=>{}); return; }
      const u = new URL(x.url);
      const hosts = realm === 'cn' ? ['copilot.tencent.com', 'www.codebuddy.cn'] : ['www.workbuddy.ai'];
      if (u.protocol !== 'https:' || !hosts.includes(u.host) || u.username || u.password) throw new Error('授权域名校验失败');
      oauthID = x.id;
      if (!$('loginDialog').open || mark !== generation) { cancelOAuth(); return; }
      $('authLink').href = u.href; $('authLink').hidden = false;
      $('loginStatus').textContent = '等待浏览器授权'; pollOAuth(x.id, Date.now() + 900000, 0);
    } catch (e) {
      if (attempt !== oauthAttempt || mark !== generation) return;
      $('loginStatus').textContent = e.message; $('retryLogin').hidden = false;
    }
  }
  function pollOAuth(id, expires, failures) {
    oauthTimer = setTimeout(async () => {
      if (oauthID !== id) return;
      if (Date.now() > expires) { cancelOAuth(); $('loginStatus').textContent = '授权已过期'; $('retryLogin').hidden = false; return; }
      try {
        const result = await api('login/poll', { id });
        if (oauthID !== id) return;
        if (result.done) {
          oauthID = null; $('loginDialog').close(); toast('账号已添加'); await refreshOverview(); return;
        }
        $('loginStatus').textContent = '等待浏览器授权'; pollOAuth(id, expires, 0);
      } catch (e) {
        if (oauthID !== id) return;
        $('loginStatus').textContent = e.message;
        if ([409, 410, 400].includes(e.status) || failures >= 4) {
          cancelOAuth(); $('retryLogin').hidden = false; $('authLink').hidden = true;
        } else { pollOAuth(id, expires, failures + 1); }
      }
    }, 3000);
  }
  $('addAccount').addEventListener('click', () => busy($('addAccount'), beginOAuth));
  $('retryLogin').addEventListener('click', () => busy($('retryLogin'), beginOAuth));
  $('loginRealm').addEventListener('change', () => busy($('addAccount'), beginOAuth));
  $('loginDialog').addEventListener('close', cancelOAuth);
  document.querySelectorAll('.close-dialog').forEach(b => b.addEventListener('click', () => $(b.dataset.dialog).close()));

  function renderModels() {
    const q = $('modelSearch').value.toLowerCase(), realm = $('realmFilter').value;
    const list = models.filter(m => m.id.toLowerCase().includes(q) && (realm === 'all' || m.id.startsWith(`${realm}:`)));
    $('modelRows').replaceChildren(); $('modelEmpty').hidden = list.length > 0;
    for (const m of list) {
      const tr = node('tr'), realmCell = node('td');
      realmCell.append(badge(m.id.startsWith('global:') ? '国际版' : '国内版'));
      tr.append(node('td', m.id, 'model-id'), realmCell, node('td', m.context_length ? Number(m.context_length).toLocaleString() : '--'), node('td', m.owned_by || 'workbuddy'));
      const td = node('td');
      td.append(iconButton('copy', `复制 ${m.id}`, async () => {
        try { await navigator.clipboard.writeText(m.id); toast('模型标识已复制'); } catch { toast('当前浏览器不允许复制'); }
      }));
      tr.append(td); $('modelRows').append(tr);
    }
    icons();
  }
  async function refreshModels() { models = (await api('models')).data || []; renderModels(); }
  $('refreshModels').addEventListener('click', e => busy(e.currentTarget, refreshModels));
  $('modelSearch').addEventListener('input', renderModels); $('realmFilter').addEventListener('change', renderModels);
  async function refreshLogs() {
    const list = (await api('logs')).entries || []; $('logRows').replaceChildren(); $('logEmpty').hidden = list.length > 0;
    for (const item of [...list].reverse()) {
      const tr = node('tr'); tr.append(node('td', new Date(item.time).toLocaleString('zh-CN', { hour12: false })), node('td', item.action), node('td', item.result)); $('logRows').append(tr);
    }
  }
  $('refreshLogs').addEventListener('click', e => busy(e.currentTarget, refreshLogs));
  async function loadView() {
    if (!token) return;
    if (view === 'usage') { await refreshUsage(); return; }
    if (view === 'tasks') { await refreshOverview(); return; }
    if (view === 'models') await refreshModels();
    else if (view === 'logs') await refreshLogs();
    else await refreshOverview();
  }
  document.querySelectorAll('[data-view]').forEach(button => button.addEventListener('click', async () => {
    view = button.dataset.view;
    document.querySelectorAll('.view').forEach(el => { el.hidden = el.id !== `view-${view}`; });
    document.querySelectorAll('[data-view]').forEach(el => {
      el.classList.toggle('active', el === button); if (el === button) el.setAttribute('aria-current', 'page'); else el.removeAttribute('aria-current');
    });
    $('breadcrumb').textContent = ({ accounts: '账号管理', models: '模型目录', logs: '运行日志', settings: '接口密钥', usage:'使用日志',tasks:'积分任务' })[view];
    try { await loadView(); } catch (e) { toast(e.message); }
  }));
  $('keyForm').addEventListener('submit', async e => {
    e.preventDefault(); $('keyMessage').textContent = '';
    const key = $('newKey').value;
    if (key !== $('repeatKey').value) { $('keyMessage').textContent = '两次输入的密钥不一致'; return; }
    $('saveKey').disabled = true;
    try {
      await api('api-key', { key, confirm: $('keyConfirm').checked });
      $('keyForm').reset(); $('keyMessage').textContent = '原配置已备份，新 API Key 已生效';
      toast('API 密钥已更新'); await refreshOverview();
    } catch (err) { $('keyMessage').textContent = err.message; }
    finally { $('newKey').value = ''; $('repeatKey').value = ''; $('saveKey').disabled = false; }
  });
  async function refreshUsage() {
    const attempt = ++usageAttempt;
    const q = new URLSearchParams({ page:String(usagePage),date:$('usageDate').value,uid:$('usageUID').value.trim(),model:$('usageModel').value,result:$('usageResult').value });
    const x = await api(`usage?${q}`);
    if (attempt !== usageAttempt) return;
    $('usageSummary').textContent = `${x.total} 次请求 · ${x.tokens} 已知 Token · ${x.credit.toFixed(3)} 已知积分 · ${x.unknown_credit} 次积分未知${x.persisted?'':' · 警告：持久化失败'}`;
    $('usageRows').replaceChildren();
    x.rows.forEach(r => {
      const tr=node('tr');
      tr.append(node('td',new Date(r.time).toLocaleString('zh-CN',{timeZone:'Asia/Shanghai'})),
        node('td',`${r.model} / ${r.uid || '--'}`),node('td',`${r.mode} / ${r.status}${r.interrupted?' 中断':''}`),
        node('td',`${r.input_tokens ?? '--'} / ${r.output_tokens ?? '--'}`),node('td',r.credit ?? '--'),
        node('td',`${r.ttfb_ms || '--'} / ${r.duration_ms} ms`));
      $('usageRows').append(tr);
    });
    $('usagePage').textContent=`${usagePage} / ${Math.max(1,Math.ceil(x.total/50))}`;
    $('usagePrev').disabled=usagePage<=1;$('usageNext').disabled=usagePage*50>=x.total;
  }
  $('refreshUsage').addEventListener('click',e=>busy(e.currentTarget,async()=>{usagePage=1;await refreshUsage();}));
  $('usagePrev').addEventListener('click',async()=>{if(usagePage<=1)return;usagePage--;try{await refreshUsage();}catch(e){usagePage++;toast(e.message);}});
  $('usageNext').addEventListener('click',async()=>{usagePage++;try{await refreshUsage();}catch(e){usagePage--;toast(e.message);}});
  $('refreshTasks').addEventListener('click',e=>busy(e.currentTarget,async()=>{
    const uid = $('taskAccount').value;
    if (!uid) { toast('请先添加账号'); return; }
    $('taskRows').replaceChildren();$('taskMessage').textContent='正在查询';
    try {
      const x=await api('tasks',{uid});
      if ($('taskAccount').value !== uid) return;
      $('taskMessage').textContent=`查询于 ${new Date(x.queried_at).toLocaleString()} · 当前任务快照，当日完成归属尚未验证`;
      const status={not_accepted:'未接受',accepted:'进行中',completed:'已完成',claimed:'已领取'};
      x.tasks.forEach(t=>{const tr=node('tr');tr.append(node('td',t.name||t.task_code),node('td',status[t.accept_status]||'未知'),node('td',`${t.progress.current} / ${t.progress.target}`));$('taskRows').append(tr);});
    } catch(err){$('taskMessage').textContent=err.message;}
  }));
  $('taskAccount').addEventListener('change',()=>{$('taskRows').replaceChildren();$('taskMessage').textContent='';});
  icons(); lock();
})();
