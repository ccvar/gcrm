<script>
  import { invoke, Channel } from '@tauri-apps/api/core';
  import { openUrl } from '@tauri-apps/plugin-opener';
  import { getCurrentWindow } from '@tauri-apps/api/window';
  import { mdRender, mdClick } from './md.js';
  import ModelChip from './ModelChip.svelte';
  import { loadPrefs, savePrefs } from './prefs.js';

  function startDrag(e) {
    if (e.button !== 0) return;
    if (e.target.closest('button, a, input, textarea, select, [role="button"], [data-no-drag]')) return;
    getCurrentWindow().startDragging().catch(() => {});
  }

  let {
    convId = $bindable(null), // 当前会话 id，null = 新对话
    connected = false,
    customModels = [], // 设置里配置的自定义模型 ID
    onchanged = () => {}, // 会话增改后通知父组件刷新侧栏
  } = $props();

  let conv = $state(null); // 当前会话完整对象
  let running = $state(false);
  let runConvId = $state(null); // 进行中回合归属的会话 id（首轮由 Started 事件回填）
  let liveText = $state('');
  let liveTools = $state([]);
  let pendingUser = $state(''); // 首轮乐观显示的用户气泡（conv 尚未载入时）
  let err = $state('');
  let draft = $state('');
  let taskType = $state('free');
  // 新对话默认沿用上次选的模型/强度/权限（存 localStorage）
  const _p = loadPrefs();
  let permMode = $state(_p.perm || 'plan');
  let model = $state(_p.model || '');
  let effort = $state(_p.effort || '');

  const TASK_CARDS = [
    { id: 'prospect', label: '找客户', desc: '联网找潜在客户，确认后导入 CRM' },
    { id: 'focus', label: '今日作战', desc: '找出今天最该打的客户与话术' },
    { id: 'review', label: '复盘归因', desc: '分析赢单丢单，沉淀 playbook' },
    { id: 'free', label: '自由对话', desc: '任意销售问题' },
  ];
  const PERM = [
    { id: 'plan', label: '只读', hint: '仅分析，不改数据' },
    { id: 'full', label: '可写', hint: '可调接口建客户/记录' },
  ];

  // 找客户模式要写回 CRM，默认切到可写；其余默认只读
  $effect(() => {
    if (taskType === 'prospect') permMode = 'full';
  });

  // convId 变化时加载会话（进行中的回合不清，避免切走再切回把流式状态清掉）
  $effect(() => {
    const id = convId;
    if (!id) {
      conv = null;
      if (!running) { liveText = ''; liveTools = []; err = ''; }
      return;
    }
    invoke('get_convo', { id }).then((c) => {
      // 若期间又切走了，丢弃这次结果
      if (convId !== id) return;
      conv = c;
      if (c && !running) { taskType = c.task_type; permMode = c.perm_mode; model = c.model; effort = c.effort || ''; }
    });
  });

  let messages = $derived(conv ? conv.messages.filter((m) => m.text || m.tools?.length) : []);
  // 有会话消息、或正在跑本会话的回合 → 显示对话视图；否则显示欢迎页
  let showHero = $derived(!convId && !running && messages.length === 0);
  // 本会话是否正有回合在跑（切到别的会话时不显示它的流式气泡）
  let liveHere = $derived(running && (runConvId === convId || (runConvId === null && !convId)));

  async function send() {
    const text = draft.trim();
    if (!text || running) return;
    draft = '';
    running = true;
    err = '';
    liveText = '';
    liveTools = [];
    pendingUser = text;
    const startedFromNew = !convId;
    runConvId = convId; // 续轮已知；首轮等 Started 回填
    // 记住这次选的模型/强度/权限，作为下次新对话默认（不动自定义模型列表）
    savePrefs({ ...loadPrefs(), model, effort, perm: permMode });

    let closed = false;
    const ch = new Channel();
    ch.onmessage = (msg) => {
      if (closed) return;
      if (msg.type === 'started') {
        runConvId = msg.conv_id;
        if (startedFromNew && !convId) convId = msg.conv_id; // 回填，使首轮也能取消
      } else if (msg.type === 'delta') {
        liveText += msg.text;
      } else if (msg.type === 'tool') {
        liveTools = [...liveTools, { label: msg.label, detail: msg.detail }];
      } else if (msg.type === 'done') {
        closed = true;
      }
    };

    const turnId = startedFromNew ? null : convId;
    try {
      const out = await invoke('send_chat', {
        convId: turnId,
        text,
        brain: 'claude',
        model: model || null,
        permMode,
        taskType,
        effort: effort || null,
        onEvent: ch,
      });
      // 仅当用户仍停留在这条会话时才把结果贴上视图（避免切走后被旧回合覆盖）
      if (convId === out.conv.id || (startedFromNew && (convId === null || convId === out.conv.id))) {
        conv = out.conv;
        convId = out.conv.id;
      }
      onchanged();
    } catch (e) {
      err = String(e);
    } finally {
      running = false;
      runConvId = null;
      liveText = '';
      liveTools = [];
      pendingUser = '';
    }
  }

  async function stop() {
    const id = runConvId || convId;
    if (id) { try { await invoke('cancel_chat', { id }); } catch { /* */ } }
  }

  function onKey(e) {
    if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
      e.preventDefault();
      send();
    }
  }

  async function copyText(text, ev) {
    try {
      await navigator.clipboard.writeText(text);
      const b = ev.currentTarget, old = b.textContent;
      b.textContent = '已复制 ✓';
      setTimeout(() => (b.textContent = old), 1200);
    } catch { /* */ }
  }
</script>

<div class="chat">
  <!-- 顶部拖拽条：主区也能拖动窗口（Overlay 标题栏下） -->
  <div class="drag-strip" data-tauri-drag-region onmousedown={startDrag}></div>
  <div class="stream">
    {#if showHero}
      <div class="hero">
        <h1>想让它帮你找客户 / 搞定客户？</h1>
        <p class="muted">选个方向，像聊天一样说清楚。跑在你本机的 Claude Code 上{#if !connected}（未连接 CRM：能找客户、给建议，但导入/改数据需先连接）{/if}。</p>
        <div class="cards">
          {#each TASK_CARDS as c (c.id)}
            <button class="card" class:on={taskType === c.id} onclick={() => (taskType = c.id)}>
              <b>{c.label}</b><span class="muted">{c.desc}</span>
            </button>
          {/each}
        </div>
        {#if taskType === 'prospect'}
          <p class="hint">找客户模式：AI 会联网检索潜在客户、列成清单，<b>你确认后</b>才写入 CRM。{#if !connected}当前未连接，只能列清单——连接后可一键导入。{/if}</p>
        {/if}
      </div>
    {:else}
      {#each messages as m (m.ts + '-' + m.role + '-' + (m.text ? m.text.length : 0))}
        {#if m.role === 'user'}
          <div class="msg user"><div class="ubody">{m.text}</div></div>
        {:else}
          <div class="msg assistant">
            {#if m.tools?.length}
              <details class="cmds"><summary>{m.tools.length} 步操作</summary>
                {#each m.tools as t}<div class="tool"><span class="tlabel">{t.label}</span><code>{t.detail}</code></div>{/each}
              </details>
            {/if}
            {#if m.error}
              <div class="errbody">{m.text}</div>
            {:else}
              <div class="mdbody" role="presentation" onclick={(e) => mdClick(e, openUrl)}>{@html mdRender(m.text)}</div>
              {#if m.text}<button class="mini" onclick={(e) => copyText(m.text, e)}>复制</button>{/if}
            {/if}
          </div>
        {/if}
      {/each}

      {#if liveHere}
        <!-- 首轮 conv 尚未载入时用 pendingUser 补上用户气泡 -->
        {#if pendingUser && (!conv || !conv.messages.some((m) => m.role === 'user' && m.text === pendingUser))}
          <div class="msg user"><div class="ubody">{pendingUser}</div></div>
        {/if}
        <div class="msg assistant">
          {#if liveTools.length}
            <details class="cmds" open><summary>{liveTools.length} 步操作</summary>
              {#each liveTools as t}<div class="tool"><span class="tlabel">{t.label}</span><code>{t.detail}</code></div>{/each}
            </details>
          {/if}
          {#if liveText}<div class="mdbody">{@html mdRender(liveText)}</div>{/if}
          <span class="working">思考中…</span>
        </div>
      {/if}
      {#if err}<div class="msg assistant"><div class="errbody">{err}</div></div>{/if}
    {/if}
  </div>

  <div class="composer">
    <textarea bind:value={draft} onkeydown={onKey} rows="1"
      placeholder={taskType === 'prospect' ? '例如：帮我找长三角做新能源电池的中型制造企业，要采购负责人的公开联系方式' : (showHero ? '像聊天一样说清楚你的需求' : '继续问…')}></textarea>
    <div class="bar">
      <div class="bl">
        {#if !showHero}
          <span class="ro">{conv?.task_type === 'prospect' ? '找客户' : conv?.task_type === 'focus' ? '今日作战' : conv?.task_type === 'review' ? '复盘归因' : '自由对话'}</span>
        {/if}
        <div class="permchip" data-perm={permMode} data-no-drag>
          <select bind:value={permMode} title="权限档位">
            {#each PERM as p (p.id)}<option value={p.id}>{p.label}</option>{/each}
          </select>
          <svg class="pchev" viewBox="0 0 12 12"><path d="M3 4.5 6 7.5 9 4.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </div>
        <ModelChip bind:model bind:effort lock={running} {customModels} />
      </div>
      <div class="br">
        {#if running}
          <button class="send stop" onclick={stop}>■</button>
        {:else}
          <button class="send" onclick={send} disabled={!draft.trim()}>↑</button>
        {/if}
      </div>
    </div>
  </div>
</div>

<style>
  .chat { display: flex; flex-direction: column; height: 100%; position: relative; }
  .drag-strip { position: absolute; top: 0; left: 0; right: 0; height: 30px; z-index: 3; }
  .stream { flex: 1; overflow-y: auto; padding: 2.2rem 0 1.2rem; }
  .hero { max-width: 680px; margin: 8vh auto 0; padding: 0 1.5rem; }
  .hero h1 { font-family: var(--serif); font-size: 1.6rem; margin: 0 0 .5rem; }
  .cards { display: grid; grid-template-columns: repeat(2, 1fr); gap: .7rem; margin-top: 1.4rem; }
  .card { text-align: left; border: 1px solid var(--line); border-radius: 10px; padding: .8rem .9rem; background: var(--surface); cursor: pointer; transition: all .15s; }
  .card:hover { border-color: var(--accent-soft); }
  .card.on { border-color: var(--accent); background: var(--accent-wash); }
  .card b { display: block; margin-bottom: .2rem; }
  .card span { font-size: .82rem; }
  .hint { font-size: .85rem; color: var(--accent); background: var(--accent-wash); border-radius: 8px; padding: .6rem .8rem; margin-top: 1rem; }

  .msg { max-width: 760px; margin: 0 auto 1rem; padding: 0 1.5rem; }
  .msg.user { display: flex; justify-content: flex-end; }
  .ubody { background: var(--accent-wash); color: var(--ink); border-radius: 12px 12px 3px 12px; padding: .55rem .85rem; max-width: 82%; white-space: pre-wrap; }
  .cmds { margin-bottom: .5rem; font-size: .85rem; }
  .cmds summary { cursor: pointer; color: var(--muted); }
  .tool { margin: .35rem 0 .35rem .5rem; }
  .tlabel { display: inline-block; font-size: .72rem; font-weight: 600; color: var(--accent); background: var(--accent-wash); padding: .05rem .4rem; border-radius: 5px; margin-right: .4rem; }
  .tool code { font-size: .8rem; word-break: break-all; }
  .mdbody { line-height: 1.65; }
  .mdbody :global(pre) { background: var(--line-soft); border-radius: 8px; padding: .7rem .9rem; overflow-x: auto; }
  .mdbody :global(code) { font-family: var(--mono); font-size: .88em; }
  .mdbody :global(p):first-child { margin-top: 0; }
  .mdbody :global(h1), .mdbody :global(h2), .mdbody :global(h3) { font-family: var(--serif); }
  .mdbody :global(table) { border-collapse: collapse; width: 100%; font-size: .9rem; }
  .mdbody :global(th), .mdbody :global(td) { border: 1px solid var(--line); padding: .3rem .6rem; text-align: left; }
  .errbody { color: var(--danger); background: color-mix(in srgb, var(--danger) 8%, var(--surface)); border: 1px solid color-mix(in srgb, var(--danger) 30%, var(--line)); border-radius: 8px; padding: .5rem .8rem; font-size: .9rem; }
  .working { font-size: .82rem; color: var(--muted); }
  .mini { font-size: .75rem; color: var(--muted); background: none; border: 0; cursor: pointer; padding: .2rem 0; }
  .mini:hover { color: var(--accent); }

  .composer { flex: none; border-top: 1px solid var(--line); background: var(--surface); padding: .7rem 1.5rem 1rem; }
  .composer textarea { width: 100%; max-width: 760px; margin: 0 auto; display: block; resize: none; border: 1px solid var(--line); border-radius: 10px; padding: .6rem .8rem; font: inherit; background: var(--bg); color: var(--ink); max-height: 200px; }
  .composer textarea:focus { outline: none; border-color: var(--accent-soft); }
  .bar { max-width: 760px; margin: .5rem auto 0; display: flex; justify-content: space-between; align-items: center; gap: .5rem; }
  .bl { display: flex; align-items: center; gap: .4rem; flex-wrap: wrap; }
  .ro { font-size: .82rem; color: var(--muted); padding-right: .1rem; }

  /* 权限 chip：原生 select 套壳成 chip，按风险上色（可写=暖橙提示） */
  .permchip { position: relative; display: inline-flex; align-items: center; height: 26px; border: 1px solid var(--line); border-radius: 8px; background: var(--surface); color: var(--ink-soft); }
  .permchip:hover { border-color: var(--accent-soft); }
  .permchip select {
    appearance: none; -webkit-appearance: none;
    border: 0; background: transparent; font: inherit; font-size: 12px; font-weight: 500; color: inherit;
    padding: 0 20px 0 8px; height: 100%; cursor: pointer;
  }
  .permchip select:focus { outline: none; }
  .pchev { position: absolute; right: 6px; width: 10px; height: 10px; opacity: .55; pointer-events: none; }
  .permchip[data-perm="full"] { color: #c0863a; border-color: color-mix(in srgb, #c0863a 40%, var(--line)); }
  .send { width: 34px; height: 34px; border-radius: 50%; border: 0; background: var(--accent); color: #fff; font-size: 1rem; cursor: pointer; }
  .send:disabled { opacity: .4; cursor: default; }
  .send.stop { background: var(--danger); }
</style>
