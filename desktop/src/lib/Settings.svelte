<script>
  // 设置面板（参考 GCMS Pilot 的「连接与模型」）：本地大脑状态 + 授权 + 自定义模型配置 + 关于。
  import { invoke } from '@tauri-apps/api/core';

  let { brains = [], prefs = $bindable(), onrefreshbrains = () => {}, onsave = () => {}, onclose = () => {} } = $props();

  const BRAIN_META = {
    claude: { name: 'Claude Code', install: 'npm i -g @anthropic-ai/claude-code' },
    codex: { name: 'Codex CLI', install: 'npm i -g @openai/codex' },
  };
  let detecting = $state(false);
  let customOpen = $state(true);
  let draft = $state('');

  function row(id) { return brains.find((b) => b.id === id) || { id, found: false, logged_in: null, version: '', detail: '' }; }

  async function redetect() {
    detecting = true;
    try { await onrefreshbrains(); } finally { detecting = false; }
  }
  async function authorize(id) {
    try { await invoke('open_brain_login', { brain: id }); } catch (e) { alert(String(e)); }
  }
  function addCustom() {
    const v = draft.trim();
    if (!v) return;
    if (!prefs.customClaudeIds.includes(v)) {
      prefs.customClaudeIds = [...prefs.customClaudeIds, v];
      onsave();
    }
    draft = '';
  }
  function removeCustom(id) {
    prefs.customClaudeIds = prefs.customClaudeIds.filter((x) => x !== id);
    onsave();
  }
</script>

<div class="mask" role="button" tabindex="-1" onclick={onclose} onkeydown={(e) => e.key === 'Escape' && onclose()}>
  <div class="sheet" role="dialog" tabindex="-1" onclick={(e) => e.stopPropagation()}>
    <div class="sheet-head"><h2>设置</h2><button class="x" onclick={onclose}>×</button></div>

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
          <input class="tin" bind:value={draft} placeholder="如 claude-opus-4-8"
            spellcheck="false" autocapitalize="off" autocorrect="off"
            onkeydown={(e) => e.key === 'Enter' && addCustom()} />
          <button class="btn sm" onclick={addCustom} disabled={!draft.trim()}>添加</button>
        </div>
      </div>
    </div>

    <div class="acts"><button class="btn" onclick={onclose}>完成</button></div>
  </div>
</div>

<style>
  .mask { position: fixed; inset: 0; background: rgba(0,0,0,.35); display: flex; align-items: flex-start; justify-content: center; z-index: 100; }
  .sheet { width: 440px; max-width: 92vw; margin-top: 9vh; max-height: 80vh; overflow-y: auto; background: var(--surface); border: 1px solid var(--line); border-radius: 12px; box-shadow: var(--shadow); padding: 1.2rem 1.4rem 1.4rem; }
  .sheet-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: .6rem; }
  .sheet-head h2 { margin: 0; font-family: var(--serif); font-size: 1.25rem; }
  .x { border: 0; background: none; font-size: 1.4rem; line-height: 1; color: var(--muted); cursor: pointer; }
  .sec { border-top: 1px solid var(--line-soft); padding: .9rem 0; }
  .sec:first-of-type { border-top: 0; }
  .sec-h { display: flex; align-items: center; gap: .5rem; font-size: .78rem; font-weight: 600; letter-spacing: .06em; color: var(--faint); }
  .sec-h span:first-child { margin-right: auto; }
  .n { font-size: .72rem; color: var(--accent); background: var(--accent-wash); padding: 0 .4rem; border-radius: 999px; }
  .link { border: 0; background: none; font: inherit; font-size: .8rem; color: var(--accent); cursor: pointer; }
  .hint { font-size: .82rem; color: var(--muted); margin: .5rem 0 .7rem; line-height: 1.5; }
  .hint.mono { font-family: var(--mono); font-size: .78rem; background: var(--line-soft); border-radius: 6px; padding: .35rem .5rem; }
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
  .btn.sm { font-size: .82rem; padding: .35rem .7rem; }
  .btn:disabled { opacity: .5; cursor: default; }
  .acts { display: flex; justify-content: flex-end; margin-top: .6rem; }
</style>
