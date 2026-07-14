<script>
  import { onMount } from 'svelte';
  import { invoke } from '@tauri-apps/api/core';
  import { listen } from '@tauri-apps/api/event';
  import { open as openDialog } from '@tauri-apps/plugin-dialog';
  import { check as checkUpdate } from '@tauri-apps/plugin-updater';
  import { relaunch } from '@tauri-apps/plugin-process';
  import Chat from './lib/Chat.svelte';
  import Queue from './lib/Queue.svelte';
  import Connect from './lib/Connect.svelte';

  let view = $state('chat'); // chat | queue
  let convId = $state(null);
  let convos = $state([]);
  let setup = $state({ server: '', has_key: false, skill_dir: '', key_prefix: '' });
  let brains = $state([]);
  let showConnect = $state(false);

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
        showConnect = true; // Connect 组件里会带 pendingZip 提示粘贴密钥
        pendingZip = path;
        pendingBase = out.api_base;
      } else {
        await refreshSetup();
        showConnect = false;
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

<main class="app">
  <aside class="rail">
    <div class="rail-head" data-tauri-drag-region>
      <button class="newchat" onclick={newChat}>＋ 新对话</button>
      <nav class="railnav">
        <button class:on={view === 'chat' && !convId} onclick={newChat}>对话</button>
        <button class:on={view === 'queue'} onclick={() => (view = 'queue')} disabled={!connected} title={connected ? '' : '需连接 CRM'}>行动队列</button>
      </nav>
    </div>

    <div class="convos">
      {#each convos as c (c.id)}
        <button class="convo" class:on={convId === c.id && view === 'chat'} onclick={() => openConv(c.id)}>
          <span class="ct">{c.title}</span>
          <span class="cw">{relTime(c.updated_at)}</span>
          <span class="cx" role="button" tabindex="-1" onclick={(e) => delConv(c.id, e)}>×</span>
        </button>
      {:else}
        <p class="empty muted">还没有对话</p>
      {/each}
    </div>

    <div class="rail-foot">
      {#if updAvail}
        <button class="updbtn" onclick={doUpdate} disabled={updBusy}>{updBusy ? updMsg : `更新到 ${updAvail}`}</button>
      {/if}
      <button class="connbar" onclick={() => (showConnect = true)}>
        <span class="dot2" class:live={connected}></span>
        <span class="cbtext">
          {#if connected}已连接 CRM{:else}未连接 · 独立模式{/if}
        </span>
        <span class="cbhint">⚙</span>
      </button>
      {#if !brainReady}
        <div class="warnbar">
          {#if !claude?.found}未检测到 Claude CLI{:else if !claude?.logged_in}Claude 未登录 · <button class="link" onclick={() => invoke('open_brain_login', { brain: 'claude' })}>去授权</button>{/if}
          <button class="link" onclick={refreshBrains}>重新检测</button>
        </div>
      {/if}
    </div>
  </aside>

  <section class="main">
    {#if view === 'queue'}
      <Queue {connected} />
    {:else}
      <Chat bind:convId {connected} onchanged={refreshConvos} />
    {/if}
  </section>

  {#if showConnect}
    <Connect
      {setup}
      {pendingZip}
      {pendingBase}
      onclose={() => { showConnect = false; pendingZip = null; }}
      onsaved={async () => { await refreshSetup(); showConnect = false; pendingZip = null; }}
      onimport={importPack}
    />
  {/if}
</main>
