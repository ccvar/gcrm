<script>
  import { onMount } from 'svelte';
  import { invoke } from '@tauri-apps/api/core';
  import { listen } from '@tauri-apps/api/event';
  import { getCurrentWindow } from '@tauri-apps/api/window';
  import { open as openDialog } from '@tauri-apps/plugin-dialog';
  import { check as checkUpdate } from '@tauri-apps/plugin-updater';
  import { relaunch } from '@tauri-apps/plugin-process';
  import Chat from './lib/Chat.svelte';
  import Queue from './lib/Queue.svelte';
  import Settings from './lib/Settings.svelte';
  import { loadPrefs, savePrefs } from './lib/prefs.js';

  let view = $state('chat'); // chat | queue
  let convId = $state(null);
  let convos = $state([]);
  let setup = $state({ server: '', has_key: false, skill_dir: '', key_prefix: '' });
  let brains = $state([]);
  let showSettings = $state(false);
  let prefs = $state(loadPrefs());

  // 顶栏融合：折叠侧栏、拖拽宽度、搜索
  let railCollapsed = $state(false);
  let railWidth = $state(loadRailWidth());
  let showSearch = $state(false);
  let searchQuery = $state('');
  function loadRailWidth() {
    const n = parseInt(localStorage.getItem('gcrm.pilot.railW') || '244', 10);
    return isNaN(n) ? 244 : Math.min(400, Math.max(190, n));
  }
  function startResize(e) {
    e.preventDefault();
    const startX = e.clientX, startW = railWidth;
    const onMove = (ev) => { railWidth = Math.max(190, Math.min(400, startW + ev.clientX - startX)); };
    const onUp = () => {
      window.removeEventListener('mousemove', onMove);
      window.removeEventListener('mouseup', onUp);
      try { localStorage.setItem('gcrm.pilot.railW', String(railWidth)); } catch { /* */ }
    };
    window.addEventListener('mousemove', onMove);
    window.addEventListener('mouseup', onUp);
  }
  let searchResults = $derived(
    searchQuery.trim()
      ? convos.filter((c) => c.title.toLowerCase().includes(searchQuery.trim().toLowerCase()))
      : convos
  );
  function pickSearch(id) { showSearch = false; searchQuery = ''; openConv(id); }

  // 拖动窗口：Overlay 标题栏下靠 JS 主动 startDragging（data-tauri-drag-region 单独不够可靠）；
  // 落在按钮/输入等交互元素上则不拖，交给它们自己。
  function startDrag(e) {
    if (e.button !== 0) return;
    const t = e.target;
    if (t.closest('button, a, input, textarea, select, [role="button"], [data-no-drag]')) return;
    getCurrentWindow().startDragging().catch(() => {});
  }

  let connected = $derived(!!(setup.server && setup.has_key));
  let claude = $derived(brains.find((b) => b.id === 'claude'));
  let brainReady = $derived(!!(claude && claude.found && claude.logged_in));

  // 更新
  let updAvail = $state('');
  let updBusy = $state(false);
  let updMsg = $state('');

  async function refreshConvos() {
    try { convos = await invoke('list_convos'); } catch { /* */ }
  }
  async function refreshSetup() {
    try { setup = await invoke('get_setup'); } catch { /* */ }
  }
  async function refreshBrains() {
    try { brains = await invoke('detect_brains'); } catch { /* */ }
  }

  function newChat() { convId = null; view = 'chat'; }
  function openConv(id) { convId = id; view = 'chat'; }

  async function delConv(id, ev) {
    ev.stopPropagation();
    try { await invoke('delete_convo', { id }); } catch { /* */ }
    if (convId === id) convId = null;
    refreshConvos();
  }

  function relTime(ts) {
    const d = new Date(ts * 1000), now = new Date();
    const days = Math.floor((now.setHours(0,0,0,0) - new Date(ts*1000).setHours(0,0,0,0)) / 86400000);
    if (days <= 0) return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' });
    if (days === 1) return '昨天';
    if (days < 7) return `${days} 天前`;
    return `${d.getMonth() + 1}/${d.getDate()}`;
  }

  // 会话按任务类型分组（对应 GCMS 的按站点分组），每组可折叠
  const TASK_META = {
    prospect: '找客户', focus: '今日作战', review: '复盘归因', free: '自由对话',
  };
  function loadCollapsed() {
    try { return new Set(JSON.parse(localStorage.getItem('gcrm.pilot.collapsed') || '[]')); } catch { return new Set(); }
  }
  let collapsed = $state(loadCollapsed());
  function toggleGroup(key) {
    const s = new Set(collapsed);
    s.has(key) ? s.delete(key) : s.add(key);
    collapsed = s;
    try { localStorage.setItem('gcrm.pilot.collapsed', JSON.stringify([...s])); } catch { /* */ }
  }
  let grouped = $derived.by(() => {
    const map = new Map();
    for (const c of convos) {
      const key = TASK_META[c.task_type] ? c.task_type : 'free';
      if (!map.has(key)) map.set(key, { key, label: TASK_META[key], items: [], recent: 0 });
      const g = map.get(key);
      g.items.push(c);
      if (c.updated_at > g.recent) g.recent = c.updated_at;
    }
    const groups = [...map.values()];
    for (const g of groups) g.items.sort((a, b) => b.updated_at - a.updated_at);
    groups.sort((a, b) => b.recent - a.recent); // 最近活动的组在上（同 GCMS）
    return groups;
  });

  async function silentCheckUpdate() {
    try {
      const upd = await checkUpdate();
      updAvail = upd ? upd.version : '';
      if (upd) { try { await upd.close(); } catch { /* */ } }
    } catch { /* */ }
  }
  async function doUpdate() {
    if (updBusy) return;
    updBusy = true; updMsg = '下载中…';
    try {
      const upd = await checkUpdate();
      if (!upd) { updAvail = ''; return; }
      let total = 0, got = 0;
      await upd.downloadAndInstall((ev) => {
        if (ev.event === 'Started') total = ev.data.contentLength ?? 0;
        else if (ev.event === 'Progress') { got += ev.data.chunkLength; if (total) updMsg = `下载 ${Math.round(got/total*100)}%`; }
        else if (ev.event === 'Finished') updMsg = '安装中…';
      });
      await relaunch();
    } catch (e) { updMsg = '更新失败'; } finally { updBusy = false; }
  }

  async function importPack() {
    try {
      const path = await openDialog({ filters: [{ name: '技能包', extensions: ['zip'] }] });
      if (!path) return;
      const out = await invoke('import_pack', { zipPath: path, key: null });
      if (out.status === 'needs_key') {
        // 抽屉里提示粘贴密钥
        pendingZip = path;
        pendingBase = out.api_base;
      } else {
        await refreshSetup();
        pendingZip = null;
        pendingBase = '';
      }
    } catch (e) { alert('导入失败：' + e); }
  }
  let pendingZip = $state(null);
  let pendingBase = $state('');

  onMount(async () => {
    await Promise.all([refreshConvos(), refreshSetup(), refreshBrains()]);
    await listen('pilot://refresh', () => { view = 'queue'; });
    silentCheckUpdate();
    setInterval(silentCheckUpdate, 6 * 60 * 60 * 1000);
  });
</script>

<main class="app" class:rail-collapsed={railCollapsed}>
  <!-- 融合标题栏：覆盖侧栏顶部供拖拽窗口；折叠/搜索按钮浮在交通灯右侧 -->
  <div class="titlebar" data-tauri-drag-region aria-hidden="true" onmousedown={startDrag} style="width:{railCollapsed ? 150 : railWidth}px"></div>
  <div class="win-tools">
    <button class="wt" onclick={() => (railCollapsed = !railCollapsed)} title={railCollapsed ? '展开侧栏' : '折叠侧栏'}>{@render icoSidebar()}</button>
    <button class="wt" onclick={() => (showSearch = true)} title="搜索会话">{@render icoSearch()}</button>
  </div>

  <aside class="rail" style="width:{railWidth}px">
    <div class="rail-head" data-tauri-drag-region>
      <button class="navitem primary" onclick={newChat}>
        {@render icoPencil()}<span>新对话</span>
      </button>
      <button class="navitem" class:on={view === 'queue'} onclick={() => connected && (view = 'queue')} disabled={!connected} title={connected ? '' : '需连接 CRM'}>
        {@render icoQueue()}<span>行动队列</span>
      </button>
    </div>

    <div class="convos">
      {#each grouped as g (g.key)}
        <button class="grp" onclick={() => toggleGroup(g.key)}>
          <span class="chev" class:col={collapsed.has(g.key)}>{@render icoChevron()}</span>
          <span class="grp-ico g-{g.key}">{@render groupIcon(g.key)}</span>
          <span class="grp-name">{g.label}</span>
          <span class="grp-n">{g.items.length}</span>
        </button>
        {#if !collapsed.has(g.key)}
          {#each g.items as c (c.id)}
            <button class="convo" class:on={convId === c.id && view === 'chat'} onclick={() => openConv(c.id)}>
              <span class="cdot g-{c.task_type}"></span>
              <span class="ct">{c.title}</span>
              <span class="cw">{relTime(c.updated_at)}</span>
              <span class="cx" role="button" tabindex="-1" onclick={(e) => delConv(c.id, e)}>×</span>
            </button>
          {/each}
        {/if}
      {:else}
        <p class="empty muted">还没有对话</p>
      {/each}
    </div>

    <div class="rail-foot">
      {#if updAvail}
        <button class="updbtn" onclick={doUpdate} disabled={updBusy}>{updBusy ? updMsg : `更新到 ${updAvail}`}</button>
      {/if}
      <button class="connbar" onclick={() => (showSettings = true)} title="设置">
        <span class="dot2" class:live={connected}></span>
        <span class="cbtext">
          {#if connected}已连接 CRM{:else}未连接 · 独立模式{/if}
        </span>
        <span class="cbhint">{@render icoGear()}</span>
      </button>
      {#if !brainReady}
        <button class="warnbar" onclick={() => (showSettings = true)}>
          {#if !claude?.found}未检测到 Claude CLI · 去设置{:else if !claude?.logged_in}Claude 未登录 · 去授权{/if}
        </button>
      {/if}
    </div>
    <!-- 右缘拖拽把手：调侧栏宽度 -->
    <div class="rail-resize" role="separator" aria-orientation="vertical" onmousedown={startResize}></div>
  </aside>

  <section class="main">
    {#if view === 'queue'}
      <Queue {connected} />
    {:else}
      <Chat bind:convId {connected} customModels={prefs.customClaudeIds} onchanged={refreshConvos} />
    {/if}
  </section>

  {#if showSettings}
    <Settings
      {setup}
      {brains}
      bind:prefs
      {pendingZip}
      {pendingBase}
      onrefreshbrains={refreshBrains}
      onrefreshsetup={async () => { await refreshSetup(); pendingZip = null; pendingBase = ''; }}
      onimport={importPack}
      onsave={() => savePrefs(prefs)}
      onclose={() => { showSettings = false; pendingZip = null; pendingBase = ''; }}
    />
  {/if}

  {#if showSearch}
    <div class="mask" role="button" tabindex="-1" onclick={() => (showSearch = false)} onkeydown={(e) => e.key === 'Escape' && (showSearch = false)}>
      <div class="search-box" role="dialog" tabindex="-1" onclick={(e) => e.stopPropagation()}>
        <div class="search-in">
          {@render icoSearch()}
          <!-- svelte-ignore a11y_autofocus -->
          <input type="search" bind:value={searchQuery} placeholder="搜索会话标题…" autofocus
            onkeydown={(e) => { if (e.key === 'Enter' && searchResults[0]) pickSearch(searchResults[0].id); }} />
        </div>
        <div class="search-list">
          {#each searchResults.slice(0, 30) as c (c.id)}
            <button class="sr" onclick={() => pickSearch(c.id)}>
              <span class="cdot g-{c.task_type}"></span>
              <span class="ct">{c.title}</span>
              <span class="cw">{relTime(c.updated_at)}</span>
            </button>
          {:else}
            <p class="empty muted">没有匹配的会话</p>
          {/each}
        </div>
      </div>
    </div>
  {/if}
</main>

{#snippet icoGear()}<svg viewBox="0 0 24 24" class="ico"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>{/snippet}
{#snippet icoSidebar()}<svg viewBox="0 0 24 24" class="ico"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/></svg>{/snippet}
{#snippet icoSearch()}<svg viewBox="0 0 24 24" class="ico"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/></svg>{/snippet}
{#snippet icoPencil()}<svg viewBox="0 0 24 24" class="ico"><path d="M12 20h9"/><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"/></svg>{/snippet}
{#snippet icoQueue()}<svg viewBox="0 0 24 24" class="ico"><path d="M9 6h11"/><path d="M9 12h11"/><path d="M9 18h11"/><path d="M4.5 6h.01"/><path d="M4.5 12h.01"/><path d="M4.5 18h.01"/></svg>{/snippet}
{#snippet icoChevron()}<svg viewBox="0 0 24 24" class="ico"><path d="m9 18 6-6-6-6"/></svg>{/snippet}
{#snippet groupIcon(key)}
  {#if key === 'prospect'}<svg viewBox="0 0 24 24" class="ico"><circle cx="11" cy="11" r="7"/><path d="m21 21-4.3-4.3"/></svg>
  {:else if key === 'focus'}<svg viewBox="0 0 24 24" class="ico"><circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="3"/></svg>
  {:else if key === 'review'}<svg viewBox="0 0 24 24" class="ico"><path d="M3 3v18h18"/><path d="m7 14 4-4 3 3 5-6"/></svg>
  {:else}<svg viewBox="0 0 24 24" class="ico"><path d="M21 15a2 2 0 0 1-2 2H8l-5 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z"/></svg>{/if}
{/snippet}
