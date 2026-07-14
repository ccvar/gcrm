<script>
  import { onMount } from 'svelte';
  import { invoke } from '@tauri-apps/api/core';
  import { listen } from '@tauri-apps/api/event';
  import {
    isPermissionGranted,
    requestPermission,
    sendNotification,
  } from '@tauri-apps/plugin-notification';
  import Brain from './lib/Brain.svelte';

  let view = $state('loading'); // loading | setup | main
  let tab = $state('work'); // work | brain
  let server = $state('http://localhost:8090');
  let apiKey = $state('');
  let setupErr = $state('');
  let saving = $state(false);

  let tasks = $state([]);
  let keyName = $state('');
  let err = $state('');
  let loading = $state(false);
  let lastRefresh = $state('');
  let notified = false; // 每次启动只提醒一次，避免骚扰

  const DAY = 86400;
  function startOfToday() {
    const d = new Date();
    d.setHours(0, 0, 0, 0);
    return Math.floor(d.getTime() / 1000);
  }

  let buckets = $derived.by(() => {
    const st = startOfToday();
    const et = st + DAY;
    const b = { overdue: [], today: [], later: [] };
    for (const t of tasks) {
      if (t.due_at && t.due_at < st) b.overdue.push(t);
      else if (t.due_at >= st && t.due_at < et) b.today.push(t);
      else b.later.push(t);
    }
    return b;
  });

  function fmtDate(ts) {
    if (!ts) return '未排期';
    const d = new Date(ts * 1000);
    return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
  }

  async function call(method, path, body = null) {
    const r = await invoke('api', { method, path, body });
    let data = {};
    try {
      data = r.body ? JSON.parse(r.body) : {};
    } catch {
      throw new Error(`响应不是 JSON（HTTP ${r.status}）`);
    }
    if (r.status === 401) throw new Error(data.error || '密钥无效或已停用');
    if (r.status >= 400) throw new Error(data.error || `HTTP ${r.status}`);
    return data;
  }

  async function refresh() {
    loading = true;
    err = '';
    try {
      const data = await call('GET', '/api/v1/tasks');
      tasks = data.tasks || [];
      lastRefresh = new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
      await maybeNotify();
    } catch (e) {
      err = e.message || String(e);
    } finally {
      loading = false;
    }
  }

  async function maybeNotify() {
    if (notified) return;
    const n = buckets.overdue.length + buckets.today.length;
    if (n === 0) return;
    notified = true;
    try {
      let ok = await isPermissionGranted();
      if (!ok) ok = (await requestPermission()) === 'granted';
      if (ok) {
        sendNotification({
          title: 'CRM Pilot · 今日行动',
          body: `${buckets.overdue.length} 条逾期、${buckets.today.length} 条今日到期的跟进等你处理`,
        });
      }
    } catch {
      /* 通知失败不影响主流程 */
    }
  }

  async function done(t) {
    try {
      await call('POST', `/api/v1/tasks/${t.id}/done`);
      tasks = tasks.filter((x) => x.id !== t.id);
    } catch (e) {
      err = e.message || String(e);
    }
  }

  async function copyDraft(t, ev) {
    try {
      await navigator.clipboard.writeText(t.ai_draft);
      const btn = ev.currentTarget;
      const old = btn.textContent;
      btn.textContent = '已复制 ✓';
      setTimeout(() => (btn.textContent = old), 1500);
    } catch {
      err = '复制失败，请手动选择文本';
    }
  }

  async function saveSetup() {
    saving = true;
    setupErr = '';
    try {
      await invoke('save_setup', { server, key: apiKey });
      const ping = await call('GET', '/api/v1/ping');
      keyName = ping.key || '';
      apiKey = ''; // 明文用完即弃，密钥已在钥匙串
      view = 'main';
      await refresh();
    } catch (e) {
      setupErr = e.message || String(e);
    } finally {
      saving = false;
    }
  }

  async function disconnect() {
    await invoke('clear_setup');
    tasks = [];
    keyName = '';
    view = 'setup';
  }

  onMount(async () => {
    const s = await invoke('get_setup');
    if (s.server) server = s.server;
    if (s.has_key) {
      view = 'main';
      try {
        const ping = await call('GET', '/api/v1/ping');
        keyName = ping.key || '';
      } catch (e) {
        err = e.message || String(e);
      }
      await refresh();
    } else {
      view = 'setup';
    }
    // 工作时段每 5 分钟拉一次，到期任务弹系统通知
    setInterval(() => {
      if (view === 'main') refresh();
    }, 5 * 60 * 1000);
    // 托盘菜单「立即刷新」
    await listen('pilot://refresh', () => {
      if (view === 'main') {
        tab = 'work';
        refresh();
      }
    });
  });
</script>

{#snippet taskCard(t)}
  <div class="task">
    <div class="task-main">
      <div class="task-title">
        <b>{t.customer_name}</b> — {t.title}
        {#if t.source === 'ai'}<span class="badge badge-ai">AI</span>{/if}
        <span class="muted small">{fmtDate(t.due_at)}</span>
      </div>
      {#if t.detail}<div class="muted small">{t.detail}</div>{/if}
      {#if t.ai_draft}
        <details class="draft">
          <summary>跟进草稿</summary>
          <pre>{t.ai_draft}</pre>
          <button class="btn btn-sm" onclick={(ev) => copyDraft(t, ev)}>复制草稿</button>
        </details>
      {/if}
    </div>
    <button class="btn btn-sm" onclick={() => done(t)}>完成</button>
  </div>
{/snippet}

{#if view === 'loading'}
  <div class="center-box muted">加载中…</div>
{:else if view === 'setup'}
  <div class="center-box">
    <div class="card auth-card">
      <h1>CRM<span class="dot">·</span>Pilot</h1>
      <p class="muted">连接你的 CCVAR CRM。密钥只存入系统钥匙串，不落盘。</p>
      <label>服务器地址
        <input type="url" bind:value={server} placeholder="http://localhost:8090" />
      </label>
      <label>自动化密钥（在 CRM「设置 → 自动化密钥」创建）
        <input type="password" bind:value={apiKey} placeholder="ccrm_…" />
      </label>
      {#if setupErr}<p class="err small">{setupErr}</p>{/if}
      <button class="btn btn-primary btn-block" onclick={saveSetup} disabled={saving}>
        {saving ? '连接中…' : '测试并保存'}
      </button>
    </div>
  </div>
{:else}
  <header class="topbar" data-tauri-drag-region>
    <span class="brand">CRM<span class="dot">·</span>Pilot</span>
    <nav class="tabs">
      <button class="tab" class:active={tab === 'work'} onclick={() => (tab = 'work')}>工作台</button>
      <button class="tab" class:active={tab === 'brain'} onclick={() => (tab = 'brain')}>分析</button>
    </nav>
    <span class="spacer"></span>
    {#if tab === 'work'}
      {#if lastRefresh}<span class="muted small">更新于 {lastRefresh}</span>{/if}
      <button class="btn btn-sm" onclick={refresh} disabled={loading}>{loading ? '刷新中…' : '刷新'}</button>
    {/if}
    <button class="btn btn-sm btn-ghost" onclick={disconnect}>断开</button>
  </header>
  <main class="content">
    {#if err && tab === 'work'}<div class="flash flash-err">{err}</div>{/if}

    <!-- Brain 常驻挂载：{#if} 切换会销毁组件，把进行中的分析流和停止按钮一起弄丢 -->
    <div class:hidden={tab !== 'brain'}>
      <Brain />
    </div>

    <div class:hidden={tab !== 'work'}>

    {#if buckets.overdue.length}
      <section class="card">
        <h2 class="danger-text">已逾期（{buckets.overdue.length}）</h2>
        {#each buckets.overdue as t (t.id)}{@render taskCard(t)}{/each}
      </section>
    {/if}

    <section class="card">
      <h2>今日到期{#if buckets.today.length}（{buckets.today.length}）{/if}</h2>
      {#if buckets.today.length}
        {#each buckets.today as t (t.id)}{@render taskCard(t)}{/each}
      {:else}
        <p class="muted">今天没有到期任务 ✓</p>
      {/if}
    </section>

    {#if buckets.later.length}
      <section class="card">
        <h2 class="muted">之后 / 未排期</h2>
        {#each buckets.later as t (t.id)}{@render taskCard(t)}{/each}
      </section>
    {/if}

    </div>
  </main>
{/if}
