<script>
  import { invoke } from '@tauri-apps/api/core';

  let { setup, pendingZip = null, pendingBase = '', onclose, onsaved, onimport } = $props();

  let server = $state(setup.server || 'http://localhost:8090');
  let key = $state('');
  let err = $state('');
  let busy = $state(false);

  // 从技能包导入触发的“需要密钥”态
  let needsKey = $derived(!!pendingZip);
  $effect(() => { if (pendingBase) server = pendingBase; });

  async function save() {
    busy = true; err = '';
    try {
      if (needsKey) {
        const out = await invoke('import_pack', { zipPath: pendingZip, key: key.trim() });
        if (out.status === 'needs_key') { err = '密钥不正确'; return; }
      } else {
        await invoke('save_setup', { server: server.trim(), key: key.trim() });
      }
      onsaved();
    } catch (e) { err = String(e); } finally { busy = false; }
  }

  async function disconnect() {
    busy = true;
    try { await invoke('clear_setup'); onsaved(); } catch (e) { err = String(e); } finally { busy = false; }
  }
</script>

<div class="mask" role="button" tabindex="-1" onclick={onclose} onkeydown={(e) => e.key === 'Escape' && onclose()}>
  <div class="sheet" role="dialog" tabindex="-1" onclick={(e) => e.stopPropagation()}>
    <h2>{needsKey ? '导入技能包 · 填入密钥' : '连接 GCRM'}</h2>
    <p class="muted small">连接后可拉取行动队列，并让对话直接读取真实客户数据。不连接也能用本地大脑聊天。</p>

    {#if needsKey}
      <p class="small">技能包来自 <code>{pendingBase}</code>，但没有内嵌密钥。请在 CRM「设置 → 自动化密钥」创建一把 <code>ccrm_</code> 密钥粘贴进来：</p>
      <label>密钥<input type="password" bind:value={key} placeholder="ccrm_…" autocomplete="off" /></label>
    {:else}
      <label>服务器地址<input type="url" bind:value={server} placeholder="http://localhost:8090" /></label>
      <label>自动化密钥（CRM 设置页创建）{#if setup.has_key}<span class="ok small"> 已设置 {setup.key_prefix}…（留空不改）</span>{/if}
        <input type="password" bind:value={key} placeholder={setup.has_key ? '••••••••' : 'ccrm_…'} autocomplete="off" />
      </label>
    {/if}

    {#if err}<div class="err small">{err}</div>{/if}

    <div class="acts">
      <button class="btn primary" onclick={save} disabled={busy}>{busy ? '处理中…' : '保存连接'}</button>
      {#if !needsKey}
        <button class="btn" onclick={onimport} disabled={busy}>导入技能包…</button>
        {#if setup.has_key}<button class="btn danger" onclick={disconnect} disabled={busy}>断开</button>{/if}
      {/if}
      <button class="btn ghost" onclick={onclose}>取消</button>
    </div>
  </div>
</div>

<style>
  .mask { position: fixed; inset: 0; background: rgba(0,0,0,.35); display: flex; align-items: center; justify-content: center; z-index: 100; }
  .sheet { width: 420px; max-width: 92vw; background: var(--surface); border: 1px solid var(--line); border-radius: 12px; box-shadow: var(--shadow); padding: 1.4rem 1.6rem; }
  .sheet h2 { margin: 0 0 .3rem; font-family: var(--serif); font-size: 1.2rem; }
  label { display: block; font-size: .86rem; color: var(--ink-soft); font-weight: 500; margin: .9rem 0 0; }
  input { display: block; width: 100%; margin-top: .3rem; font: inherit; background: var(--bg); border: 1px solid var(--line); border-radius: 8px; padding: .45rem .65rem; color: var(--ink); }
  input:focus { outline: none; border-color: var(--accent-soft); }
  .ok { color: var(--ok); }
  .err { color: var(--danger); margin-top: .7rem; }
  .acts { display: flex; gap: .5rem; margin-top: 1.3rem; flex-wrap: wrap; }
  .btn { font: inherit; font-size: .88rem; color: var(--ink-soft); background: var(--surface); border: 1px solid var(--line); border-radius: 8px; padding: .4rem .9rem; cursor: pointer; }
  .btn:hover { border-color: var(--accent-soft); color: var(--accent); }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
  .btn.danger { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 35%, var(--line)); }
  .btn.ghost { border-color: transparent; color: var(--muted); }
  .btn:disabled { opacity: .5; }
</style>
