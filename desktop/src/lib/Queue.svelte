<script>
  import { onMount } from 'svelte';
  import { invoke } from '@tauri-apps/api/core';
  import {
    isPermissionGranted, requestPermission, sendNotification,
  } from '@tauri-apps/plugin-notification';

  let { connected = false } = $props();
  let tasks = $state([]);
  let loading = $state(false);
  let err = $state('');
  let lastRefresh = $state('');
  let notified = false;

  const DAY = 86400;
  function startOfToday() { const d = new Date(); d.setHours(0,0,0,0); return Math.floor(d.getTime()/1000); }

  let buckets = $derived.by(() => {
    const st = startOfToday(), et = st + DAY;
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
    return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,'0')}-${String(d.getDate()).padStart(2,'0')}`;
  }

  async function call(method, path, body = null) {
    const r = await invoke('api', { method, path, body });
    let data = {};
    try { data = r.body ? JSON.parse(r.body) : {}; } catch { throw new Error(`HTTP ${r.status}`); }
    if (r.status >= 400) throw new Error(data.error || `HTTP ${r.status}`);
    return data;
  }

  async function refresh() {
    if (!connected) return;
    loading = true; err = '';
    try {
      const data = await call('GET', '/api/v1/tasks');
      tasks = data.tasks || [];
      lastRefresh = new Date().toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
      await maybeNotify();
    } catch (e) { err = String(e); } finally { loading = false; }
  }

  async function maybeNotify() {
    if (notified) return;
    const n = buckets.overdue.length + buckets.today.length;
    if (n === 0) return;
    notified = true;
    try {
      let ok = await isPermissionGranted();
      if (!ok) ok = (await requestPermission()) === 'granted';
      if (ok) sendNotification({ title: 'GCRM Pilot · 今日行动', body: `${buckets.overdue.length} 条逾期、${buckets.today.length} 条今日到期` });
    } catch { /* */ }
  }

  async function done(t) {
    try { await call('POST', `/api/v1/tasks/${t.id}/done`); tasks = tasks.filter((x) => x.id !== t.id); }
    catch (e) { err = String(e); }
  }
  async function copyDraft(t, ev) {
    try { await navigator.clipboard.writeText(t.ai_draft); const b = ev.currentTarget, o = b.textContent; b.textContent = '已复制 ✓'; setTimeout(() => (b.textContent = o), 1200); } catch { /* */ }
  }

  onMount(() => { refresh(); const i = setInterval(refresh, 5 * 60 * 1000); return () => clearInterval(i); });
</script>

<header class="head" data-tauri-drag-region>
  <div><h2>今日行动</h2></div>
  <div class="ha">
    {#if lastRefresh}<span class="muted small">更新于 {lastRefresh}</span>{/if}
    <button class="btn btn-sm" onclick={refresh} disabled={loading || !connected}>{loading ? '刷新中…' : '刷新'}</button>
  </div>
</header>

<div class="body">
  {#if !connected}
    <p class="muted">行动队列需要连接 CRM 服务端。点左下角「未连接」去连接或导入技能包。</p>
  {:else}
    {#if err}<div class="flash-err">{err}</div>{/if}
    {#snippet card(t)}
      <div class="task">
        <div class="tmain">
          <div class="tt"><b>{t.customer_name}</b> — {t.title}{#if t.source === 'ai'}<span class="badge">AI</span>{/if}<span class="muted small">{fmtDate(t.due_at)}</span></div>
          {#if t.detail}<div class="muted small">{t.detail}</div>{/if}
          {#if t.ai_draft}<details class="draft"><summary>跟进草稿</summary><pre>{t.ai_draft}</pre><button class="btn btn-sm" onclick={(e) => copyDraft(t, e)}>复制草稿</button></details>{/if}
        </div>
        <button class="btn btn-sm" onclick={() => done(t)}>完成</button>
      </div>
    {/snippet}
    {#if buckets.overdue.length}<section class="card"><h3 class="danger">已逾期（{buckets.overdue.length}）</h3>{#each buckets.overdue as t (t.id)}{@render card(t)}{/each}</section>{/if}
    <section class="card"><h3>今日到期{#if buckets.today.length}（{buckets.today.length}）{/if}</h3>{#if buckets.today.length}{#each buckets.today as t (t.id)}{@render card(t)}{/each}{:else}<p class="muted">今天没有到期任务 ✓</p>{/if}</section>
    {#if buckets.later.length}<section class="card"><h3 class="muted">之后 / 未排期</h3>{#each buckets.later as t (t.id)}{@render card(t)}{/each}</section>{/if}
  {/if}
</div>

<style>
  .head { display: flex; justify-content: space-between; align-items: center; padding: 1rem 1.5rem; border-bottom: 1px solid var(--line); }
  .head h2 { margin: 0; font-family: var(--serif); }
  .ha { display: flex; align-items: center; gap: .7rem; }
  .body { padding: 1.2rem 1.5rem; max-width: 820px; overflow-y: auto; }
  .card { background: var(--surface); border: 1px solid var(--line); border-radius: 10px; padding: 1rem 1.2rem; margin-bottom: 1rem; }
  .card h3 { margin: 0 0 .7rem; font-family: var(--serif); font-size: 1rem; }
  .danger { color: var(--danger); }
  .task { display: flex; gap: .9rem; align-items: flex-start; padding: .7rem 0; border-top: 1px solid var(--line-soft); }
  .task:first-of-type { border-top: 0; }
  .tmain { flex: 1; min-width: 0; }
  .badge { font-size: .72rem; font-weight: 600; padding: .05rem .4rem; border-radius: 20px; background: var(--accent-wash); color: var(--accent); margin: 0 .3rem; }
  .draft { margin-top: .35rem; }
  .draft summary { font-size: .83rem; color: var(--accent); cursor: pointer; }
  .flash-err { background: color-mix(in srgb, var(--danger) 8%, var(--surface)); color: var(--danger); border: 1px solid color-mix(in srgb, var(--danger) 30%, var(--line)); border-radius: 8px; padding: .5rem .8rem; margin-bottom: 1rem; }
  .btn { font: inherit; font-size: .88rem; color: var(--ink-soft); background: var(--surface); border: 1px solid var(--line); border-radius: 8px; padding: .35rem .8rem; cursor: pointer; }
  .btn:hover { border-color: var(--accent-soft); color: var(--accent); }
  .btn:disabled { opacity: .5; }
  .btn-sm { font-size: .8rem; padding: .28rem .7rem; }
</style>
