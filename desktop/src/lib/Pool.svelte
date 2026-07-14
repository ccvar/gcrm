<script>
  import { onMount } from 'svelte';
  import { invoke } from '@tauri-apps/api/core';
  import { getCurrentWindow } from '@tauri-apps/api/window';
  import { openUrl } from '@tauri-apps/plugin-opener';

  // AI 产出的 source_url 不可信：仅放行 http(s)，且走系统浏览器（不在 webview 里导航/执行）
  function safeOpen(url) {
    if (/^https?:\/\//i.test(url || '')) openUrl(url).catch(() => {});
  }
  const isHttp = (u) => /^https?:\/\//i.test(u || '');

  let { connected = false, onchanged = () => {} } = $props();

  function startDrag(e) {
    if (e.button !== 0) return;
    if (e.target.closest('button, a, input, select, [role="button"]')) return;
    getCurrentWindow().startDragging().catch(() => {});
  }

  let leads = $state([]);
  let busy = $state('');
  let err = $state('');

  const STATUS = { new: '新线索', contacted: '已联系', pushed: '已入库' };

  let buckets = $derived.by(() => {
    const b = { active: [], pushed: [] };
    for (const l of leads) (l.status === 'pushed' ? b.pushed : b.active).push(l);
    return b;
  });
  let unpushed = $derived(leads.filter((l) => l.status !== 'pushed'));

  async function load() {
    try { leads = await invoke('list_leads'); } catch (e) { err = String(e); }
  }
  async function push(l) {
    busy = l.id; err = '';
    try { await invoke('push_lead', { id: l.id }); await load(); onchanged(); }
    catch (e) { err = String(e); } finally { busy = ''; }
  }
  async function pushAll() {
    if (!connected) return;
    busy = 'all'; err = '';
    try {
      for (const l of unpushed) { try { await invoke('push_lead', { id: l.id }); } catch (e) { err = String(e); } }
      await load(); onchanged();
    } finally { busy = ''; }
  }
  async function mark(l, status) {
    try { await invoke('update_lead_status', { id: l.id, status }); await load(); } catch (e) { err = String(e); }
  }
  async function del(l) {
    try { await invoke('delete_lead', { id: l.id }); await load(); } catch (e) { err = String(e); }
  }

  onMount(load);
</script>

<header class="head" data-tauri-drag-region onmousedown={startDrag}>
  <div><h2>客户池</h2><span class="muted small">找到的线索先入本地池，连接 GCRM 后可推入建成客户</span></div>
  <div class="ha">
    <button class="btn btn-sm" onclick={load}>刷新</button>
    {#if unpushed.length}
      <button class="btn btn-sm btn-primary" onclick={pushAll} disabled={!connected || busy === 'all'} title={connected ? '' : '需先连接 GCRM'}>
        {busy === 'all' ? '推入中…' : `推入全部（${unpushed.length}）`}
      </button>
    {/if}
  </div>
</header>

<div class="body">
  {#if err}<div class="flash-err">{err}</div>{/if}
  {#if leads.length === 0}
    <p class="muted">客户池是空的。去<b>找客户</b>对话里让 AI 联网找一批潜在客户，然后点「全部入池」，它们就会出现在这里。</p>
  {:else}
    {#snippet card(l)}
      <div class="lead" class:pushed={l.status === 'pushed'}>
        <div class="lmain">
          <div class="ltop">
            <b>{l.company}</b>
            <span class="badge s-{l.status}">{STATUS[l.status] || l.status}</span>
          </div>
          {#if l.contact}<div class="lc muted small">对接：{l.contact}</div>{/if}
          <div class="lc small">
            {#if l.phone}<span>📞 {l.phone}</span>{/if}
            {#if l.wechat}<span>💬 {l.wechat}</span>{/if}
            {#if l.email}<span>✉ {l.email}</span>{/if}
          </div>
          {#if l.reason}<div class="lc muted small">{l.reason}</div>{/if}
          {#if l.source || l.source_url}<div class="lc muted small">来源：{l.source}{#if isHttp(l.source_url)} · <button class="srclink" onclick={() => safeOpen(l.source_url)}>链接 ↗</button>{/if}</div>{/if}
        </div>
        <div class="lacts">
          {#if l.status !== 'pushed'}
            <button class="btn btn-sm btn-primary" onclick={() => push(l)} disabled={!connected || !!busy} title={connected ? '' : '需先连接 GCRM'}>{busy === l.id ? '推入中…' : '推入 GCRM'}</button>
            {#if l.status === 'new'}<button class="btn btn-sm" onclick={() => mark(l, 'contacted')}>标记已联系</button>{/if}
          {/if}
          <button class="btn btn-sm btn-x" onclick={() => del(l)} title="从池中删除">删除</button>
        </div>
      </div>
    {/snippet}

    {#if buckets.active.length}
      <section class="grp"><h3>待处理（{buckets.active.length}）</h3>{#each buckets.active as l (l.id)}{@render card(l)}{/each}</section>
    {/if}
    {#if buckets.pushed.length}
      <section class="grp"><h3 class="muted">已入库（{buckets.pushed.length}）</h3>{#each buckets.pushed as l (l.id)}{@render card(l)}{/each}</section>
    {/if}
  {/if}
</div>

<style>
  .head { display: flex; justify-content: space-between; align-items: flex-start; padding: 2rem 1.5rem .9rem; border-bottom: 1px solid var(--line); gap: 1rem; }
  .head h2 { margin: 0; font-family: var(--serif); }
  .head .small { display: block; margin-top: .15rem; }
  .ha { display: flex; align-items: center; gap: .6rem; flex: none; }
  .body { padding: 1.2rem 1.5rem; max-width: 860px; overflow-y: auto; }
  .grp { margin-bottom: 1.3rem; }
  .grp h3 { font-family: var(--serif); font-size: 1rem; margin: 0 0 .7rem; }
  .lead { display: flex; gap: .9rem; align-items: flex-start; justify-content: space-between; background: var(--surface); border: 1px solid var(--line); border-radius: 10px; padding: .8rem 1rem; margin-bottom: .7rem; }
  .lead.pushed { opacity: .72; }
  .lmain { flex: 1; min-width: 0; }
  .ltop { display: flex; align-items: center; gap: .5rem; }
  .ltop b { font-size: 1rem; }
  .lc { margin-top: .2rem; display: flex; flex-wrap: wrap; gap: .8rem; }
  .badge { font-size: .72rem; font-weight: 600; padding: .05rem .5rem; border-radius: 20px; }
  .s-new { background: var(--accent-wash); color: var(--accent); }
  .s-contacted { background: #faf1de; color: #96690f; }
  .s-pushed { background: color-mix(in srgb, var(--ok) 14%, var(--surface)); color: var(--ok); }
  .lacts { display: flex; flex-direction: column; gap: .4rem; flex: none; align-items: flex-end; }
  .flash-err { background: color-mix(in srgb, var(--danger) 8%, var(--surface)); color: var(--danger); border: 1px solid color-mix(in srgb, var(--danger) 30%, var(--line)); border-radius: 8px; padding: .5rem .8rem; margin-bottom: 1rem; }
  .btn { font: inherit; font-size: .88rem; color: var(--ink-soft); background: var(--surface); border: 1px solid var(--line); border-radius: 8px; padding: .35rem .8rem; cursor: pointer; white-space: nowrap; }
  .btn:hover { border-color: var(--accent-soft); color: var(--accent); }
  .btn:disabled { opacity: .5; cursor: default; }
  .btn-sm { font-size: .8rem; padding: .28rem .7rem; }
  .btn-primary { background: var(--accent); border-color: var(--accent); color: #fff; }
  .btn-primary:hover { background: var(--accent-soft); color: #fff; }
  .btn-x { color: var(--muted); }
  .btn-x:hover { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 35%, var(--line)); }
  .srclink { font: inherit; font-size: inherit; color: var(--accent); background: none; border: 0; padding: 0; cursor: pointer; text-decoration: underline; }
</style>
