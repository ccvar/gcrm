<script>
  import { onMount } from 'svelte';
  import { invoke, Channel } from '@tauri-apps/api/core';

  let brains = $state([]);
  let detecting = $state(false);
  let running = $state(false);
  let output = $state('');
  let runErr = $state('');
  let kind = $state('today_focus');
  let custom = $state('');

  let claude = $derived(brains.find((b) => b.id === 'claude'));
  let ready = $derived(!!(claude && claude.found && claude.logged_in));

  const PRESETS = [
    { id: 'today_focus', label: '今日作战重点', desc: '最该打的客户 + 开场话术 + 被遗漏的高意向' },
    { id: 'lost_review', label: '赢单/丢单归因月报', desc: '胜率概览 + 丢单共性 + 可复制动作' },
    { id: 'custom', label: '自定义分析', desc: '用你的问题跑全量数据' },
  ];

  async function detect() {
    detecting = true;
    try {
      brains = await invoke('detect_brains');
    } catch (e) {
      runErr = String(e);
    } finally {
      detecting = false;
    }
  }

  async function login(brain) {
    try {
      await invoke('open_brain_login', { brain });
    } catch (e) {
      runErr = String(e);
    }
  }

  async function run() {
    if (running) return;
    running = true;
    output = '';
    runErr = '';
    const ch = new Channel();
    let closed = false; // Done 之后丢弃迟到的 Delta（取消时管道里可能还有残余）
    ch.onmessage = (msg) => {
      if (closed) return;
      if (msg.type === 'delta') output += msg.text;
      else if (msg.type === 'done') {
        closed = true;
        running = false;
        if (!msg.ok && msg.error) runErr = msg.error;
      }
    };
    try {
      const text = await invoke('run_analysis', {
        kind,
        custom: kind === 'custom' ? custom : null,
        onEvent: ch,
      });
      // CLI 可能只发 result 兜底文本、没有增量事件——用返回值补上
      if (text && !output) output = text;
    } catch (e) {
      runErr = runErr || String(e);
    } finally {
      running = false;
    }
  }

  async function stop() {
    try {
      await invoke('cancel_analysis');
    } catch {
      /* 没有进行中的分析 */
    }
  }

  async function copyOut(ev) {
    try {
      await navigator.clipboard.writeText(output);
      const btn = ev.currentTarget;
      const old = btn.textContent;
      btn.textContent = '已复制 ✓';
      setTimeout(() => (btn.textContent = old), 1500);
    } catch {
      runErr = '复制失败，请手动选择文本';
    }
  }

  onMount(detect);
</script>

<section class="card">
  <h2>本地大脑</h2>
  <p class="muted small">
    深度分析跑在你本机已登录的 Claude Code CLI 上（用你自己的订阅，零 API 计费）。
    数据由 Pilot 拉好喂给模型，密钥不会进入子进程。
  </p>
  {#each brains as b (b.id)}
    <div class="brain-row">
      <span class="brain-name">{b.id === 'claude' ? 'Claude Code' : 'Codex CLI'}</span>
      {#if !b.found}
        <span class="muted small">未检测到</span>
      {:else if b.logged_in === false}
        <span class="warn small">已安装（{b.version}）· 未登录</span>
        <button class="btn btn-sm" onclick={() => login(b.id)}>去授权</button>
      {:else if b.logged_in}
        <span class="ok small">✓ {b.version}{#if b.detail}&nbsp;· {b.detail}{/if}</span>
        {#if b.id === 'codex'}<span class="muted small">（暂未接入分析）</span>{/if}
      {:else}
        <span class="muted small">{b.version} · 状态未知</span>
      {/if}
    </div>
  {/each}
  <button class="btn btn-sm" onclick={detect} disabled={detecting}>
    {detecting ? '检测中…' : '重新检测'}
  </button>
</section>

<section class="card">
  <h2>深度分析</h2>
  <div class="presets">
    {#each PRESETS as p (p.id)}
      <label class="preset" class:active={kind === p.id}>
        <input type="radio" bind:group={kind} value={p.id} />
        <b>{p.label}</b>
        <span class="muted small">{p.desc}</span>
      </label>
    {/each}
  </div>
  {#if kind === 'custom'}
    <label>你的问题
      <textarea rows="3" bind:value={custom} placeholder="例如：哪些客户最近 30 天有互动但没有商机？帮我按优先级排个名单。"></textarea>
    </label>
  {/if}
  <div class="run-row">
    {#if running}
      <button class="btn btn-danger" onclick={stop}>停止</button>
      <span class="muted small">分析中…（跑在本机 claude 上，可能要几分钟）</span>
    {:else}
      <button class="btn btn-primary" onclick={run} disabled={!ready}>开始分析</button>
      {#if !ready}<span class="muted small">需要已登录的 Claude Code CLI</span>{/if}
    {/if}
  </div>
  {#if runErr}<div class="flash flash-err">{runErr}</div>{/if}
  {#if output}
    <div class="report">
      <pre>{output}</pre>
      <button class="btn btn-sm" onclick={copyOut}>复制报告</button>
    </div>
  {/if}
</section>

<style>
  .brain-row {
    display: flex; align-items: center; gap: .7rem;
    padding: .45rem 0; border-top: 1px solid var(--line-soft);
  }
  .brain-row:first-of-type { border-top: 0; }
  .brain-name { font-weight: 600; min-width: 100px; }
  .ok { color: var(--ok); }
  .warn { color: var(--danger); }
  .presets { display: grid; grid-template-columns: repeat(auto-fit, minmax(180px, 1fr)); gap: .6rem; margin-bottom: .8rem; }
  .preset {
    display: block; border: 1px solid var(--line); border-radius: 8px;
    padding: .6rem .8rem; cursor: pointer; margin: 0;
  }
  .preset.active { border-color: var(--accent); background: var(--accent-wash); }
  .preset input { display: none; }
  .preset b { display: block; font-size: .92rem; }
  .run-row { display: flex; align-items: center; gap: .8rem; margin: .6rem 0; }
  .report { margin-top: .8rem; }
  .report pre { max-height: 420px; overflow-y: auto; }
  textarea {
    display: block; width: 100%; margin-top: .3rem;
    font: inherit; color: var(--ink);
    background: var(--bg); border: 1px solid var(--line); border-radius: 8px;
    padding: .45rem .65rem; resize: vertical;
  }
  textarea:focus { outline: none; border-color: var(--accent-soft); }
</style>
