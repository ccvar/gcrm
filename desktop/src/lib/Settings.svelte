<script>
  // 设置抽屉（从页面右侧滑出）：连接 CRM + 本地大脑 + 自定义模型，一处配齐。
  import { invoke } from '@tauri-apps/api/core';
  import { fly, fade } from 'svelte/transition';

  let {
    setup = { server: '', has_key: false, skill_dir: '', key_prefix: '' },
    brains = [],
    prefs = $bindable(),
    pendingZip = null,     // 导入技能包时「需要密钥」的待处理 zip
    pendingBase = '',
    onrefreshbrains = () => {},
    onrefreshsetup = () => {},
    onimport = () => {},   // 触发选文件导入
    onsave = () => {},     // 保存 prefs（自定义模型）
    onclose = () => {},
  } = $props();

  // ---- 连接 CRM ----
  let server = $state(setup.server || 'http://localhost:8090');
  let connKey = $state('');
  let connErr = $state('');
  let connBusy = $state('');
  let needsKey = $derived(!!pendingZip);
  $effect(() => { if (pendingBase) server = pendingBase; });

  async function saveConn() {
    connBusy = 'save'; connErr = '';
    try {
      if (needsKey) {
        const out = await invoke('import_pack', { zipPath: pendingZip, key: connKey.trim() });
        if (out.status === 'needs_key') { connErr = '密钥不正确'; return; }
      } else {
        await invoke('save_setup', { server: server.trim(), key: connKey.trim() });
      }
      connKey = '';
      await onrefreshsetup();
    } catch (e) { connErr = String(e); } finally { connBusy = ''; }
  }
  async function disconnect() {
    connBusy = 'disc';
    try { await invoke('clear_setup'); await onrefreshsetup(); } catch (e) { connErr = String(e); } finally { connBusy = ''; }
  }

  // ---- 本地大脑 ----
  const BRAIN_META = {
    claude: { name: 'Claude Code', install: 'npm i -g @anthropic-ai/claude-code' },
    codex: { name: 'Codex CLI', install: 'npm i -g @openai/codex' },
  };
  let detecting = $state(false);
  let draft = $state('');
  function row(id) { return brains.find((b) => b.id === id) || { id, found: false, logged_in: null, version: '', detail: '' }; }
  async function redetect() { detecting = true; try { await onrefreshbrains(); } finally { detecting = false; } }
  async function authorize(id) { try { await invoke('open_brain_login', { brain: id }); } catch (e) { alert(String(e)); } }

  // ---- 自定义模型 ----
  function addCustom() {
    const v = draft.trim();
    if (!v) return;
    if (!prefs.customClaudeIds.includes(v)) { prefs.customClaudeIds = [...prefs.customClaudeIds, v]; onsave(); }
    draft = '';
  }
  function removeCustom(id) { prefs.customClaudeIds = prefs.customClaudeIds.filter((x) => x !== id); onsave(); }
</script>

<div class="scrim" role="button" tabindex="-1" transition:fade={{ duration: 150 }}
  onclick={onclose} onkeydown={(e) => e.key === 'Escape' && onclose()}></div>

<aside class="drawer" role="dialog" tabindex="-1" transition:fly={{ x: 420, duration: 200 }} onclick={(e) => e.stopPropagation()}>
  <div class="head">
    <h2>设置</h2>
    <button class="x" onclick={onclose} title="关闭">×</button>
  </div>
  <div class="body">

    <div class="sec">
      <div class="sec-h"><span>连接 CRM</span>
        <span class="pill" class:live={setup.has_key && setup.server}>{setup.has_key && setup.server ? '已连接' : '未连接'}</span>
      </div>
      <p class="hint">连接后可拉取行动队列、让对话读写真实客户数据。不连接也能用本地大脑找客户、给建议。</p>
      {#if needsKey}
        <p class="hint">技能包来自 <code>{pendingBase}</code>，无内嵌密钥，请粘贴一把 <code>ccrm_</code> 密钥：</p>
        <label>密钥<input type="password" bind:value={connKey} placeholder="ccrm_…" autocomplete="off" /></label>
      {:else}
        <label>服务器地址<input type="url" bind:value={server} placeholder="http://localhost:8090" /></label>
        <label>自动化密钥{#if setup.has_key}<span class="ok"> 已设置 {setup.key_prefix}…（留空不改）</span>{/if}
          <input type="password" bind:value={connKey} placeholder={setup.has_key ? '••••••••' : 'ccrm_…'} autocomplete="off" />
        </label>
      {/if}
      {#if connErr}<div class="err">{connErr}</div>{/if}
      <div class="row">
        <button class="btn primary" onclick={saveConn} disabled={!!connBusy}>{connBusy === 'save' ? '处理中…' : '保存连接'}</button>
        {#if !needsKey}
          <button class="btn" onclick={onimport} disabled={!!connBusy}>导入技能包…</button>
          {#if setup.has_key}<button class="btn danger" onclick={disconnect} disabled={!!connBusy}>{connBusy === 'disc' ? '断开中…' : '断开'}</button>{/if}
        {/if}
      </div>
    </div>

    <div class="sec">
      <div class="sec-h"><span>本地大脑</span><button class="link" onclick={redetect} disabled={detecting}>{detecting ? '检测中…' : '重新检测'}</button></div>
      <p class="hint">深度对话跑在你本机已登录的 CLI 上，用你自己的订阅，零 API 计费。</p>
      {#each ['claude', 'codex'] as id (id)}
        {@const r = row(id)}
        <div class="brain">
          <div class="brow">
            <span class="bname">{BRAIN_META[id].name}</span>
            <span class="bstat">
              {#if !r.found}<span class="dot off"></span>未安装
              {:else if r.logged_in === false}<span class="dot warn"></span>未登录
              {:else if r.logged_in}<span class="dot ok"></span>{r.version || '已就绪'}{#if r.detail} · {r.detail}{/if}
              {:else}<span class="dot"></span>{r.version || '状态未知'}{/if}
            </span>
            {#if r.found && r.logged_in === false}<button class="btn sm" onclick={() => authorize(id)}>去授权 ↗</button>{/if}
          </div>
          {#if !r.found}<p class="hint mono">安装：{BRAIN_META[id].install}</p>{/if}
        </div>
      {/each}
    </div>

    <div class="sec">
      <div class="sec-h"><span>自定义模型</span>{#if prefs.customClaudeIds.length}<span class="n">{prefs.customClaudeIds.length}</span>{/if}</div>
      <p class="hint">默认可选 Sonnet / Opus / Haiku。要用别的 Claude 模型，在这里加它的完整 ID，会出现在对话框的模型选择里。</p>
      <div class="cust">
        {#each prefs.customClaudeIds as id (id)}
          <div class="chip"><span>{id}</span><button class="cx" title="删除" onclick={() => removeCustom(id)}>×</button></div>
        {/each}
        <div class="add">
          <input class="tin" bind:value={draft} placeholder="如 claude-opus-4-8" spellcheck="false" autocapitalize="off" autocorrect="off"
            onkeydown={(e) => e.key === 'Enter' && addCustom()} />
          <button class="btn sm" onclick={addCustom} disabled={!draft.trim()}>添加</button>
        </div>
      </div>
    </div>

  </div>
</aside>

<style>
  .scrim { position: fixed; inset: 0; background: rgba(0,0,0,.28); z-index: 100; }
  .drawer {
    position: fixed; top: 0; right: 0; height: 100vh; width: 420px; max-width: 92vw; z-index: 101;
    display: flex; flex-direction: column;
    background: var(--surface); border-left: 1px solid var(--line); box-shadow: -12px 0 32px rgba(30,25,15,.14);
  }
  .head { flex: none; display: flex; align-items: center; justify-content: space-between; padding: 1.2rem 1.4rem .6rem; }
  .head h2 { margin: 0; font-family: var(--serif); font-size: 1.25rem; }
  .x { border: 0; background: none; font-size: 1.5rem; line-height: 1; color: var(--muted); cursor: pointer; }
  .x:hover { color: var(--ink); }
  .body { flex: 1; overflow-y: auto; padding: 0 1.4rem 1.4rem; }

  .sec { border-top: 1px solid var(--line-soft); padding: 1rem 0; }
  .sec:first-child { border-top: 0; }
  .sec-h { display: flex; align-items: center; gap: .5rem; font-size: .78rem; font-weight: 600; letter-spacing: .06em; color: var(--faint); }
  .sec-h span:first-child { margin-right: auto; }
  .pill { font-size: .72rem; font-weight: 500; letter-spacing: 0; color: var(--muted); background: var(--line-soft); padding: .05rem .5rem; border-radius: 999px; }
  .pill.live { color: var(--ok); background: color-mix(in srgb, var(--ok) 14%, var(--surface)); }
  .n { font-size: .72rem; color: var(--accent); background: var(--accent-wash); padding: 0 .4rem; border-radius: 999px; }
  .link { border: 0; background: none; font: inherit; font-size: .8rem; color: var(--accent); cursor: pointer; }
  .hint { font-size: .82rem; color: var(--muted); margin: .5rem 0 .7rem; line-height: 1.5; }
  .hint.mono { font-family: var(--mono); font-size: .78rem; background: var(--line-soft); border-radius: 6px; padding: .35rem .5rem; }

  label { display: block; font-size: .84rem; color: var(--ink-soft); font-weight: 500; margin: .7rem 0 0; }
  input:not(.tin) { display: block; width: 100%; margin-top: .3rem; font: inherit; background: var(--bg); border: 1px solid var(--line); border-radius: 8px; padding: .45rem .65rem; color: var(--ink); }
  input:focus { outline: none; border-color: var(--accent-soft); }
  .ok { color: var(--ok); font-weight: 400; }
  .err { color: var(--danger); font-size: .84rem; margin-top: .6rem; }
  .row { display: flex; gap: .5rem; margin-top: .9rem; flex-wrap: wrap; }

  .brain { padding: .35rem 0; }
  .brow { display: flex; align-items: center; gap: .6rem; }
  .bname { font-weight: 600; min-width: 96px; }
  .bstat { flex: 1; display: flex; align-items: center; gap: .4rem; font-size: .85rem; color: var(--muted); }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--faint); flex: none; }
  .dot.ok { background: var(--ok); } .dot.warn { background: #c0863a; } .dot.off { background: var(--faint); }

  .cust { display: flex; flex-direction: column; gap: .4rem; }
  .chip { display: flex; align-items: center; justify-content: space-between; background: var(--line-soft); border-radius: 7px; padding: .3rem .3rem .3rem .6rem; font-family: var(--mono); font-size: .82rem; }
  .cx { border: 0; background: none; color: var(--muted); font-size: 1rem; line-height: 1; cursor: pointer; padding: 0 .3rem; }
  .cx:hover { color: var(--danger); }
  .add { display: flex; gap: .5rem; }
  .tin { flex: 1; font: inherit; font-size: .85rem; background: var(--bg); border: 1px solid var(--line); border-radius: 8px; padding: .4rem .6rem; color: var(--ink); }
  .tin:focus { outline: none; border-color: var(--accent-soft); }

  .btn { font: inherit; font-size: .88rem; color: var(--ink-soft); background: var(--surface); border: 1px solid var(--line); border-radius: 8px; padding: .4rem .9rem; cursor: pointer; }
  .btn:hover { border-color: var(--accent-soft); color: var(--accent); }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: #fff; }
  .btn.primary:hover { background: var(--accent-soft); color: #fff; }
  .btn.danger { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 35%, var(--line)); }
  .btn.sm { font-size: .82rem; padding: .35rem .7rem; }
  .btn:disabled { opacity: .5; cursor: default; }
</style>
