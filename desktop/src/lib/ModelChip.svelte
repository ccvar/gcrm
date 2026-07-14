<script>
  // 模型 + 思考强度合一的 chip（参考 GCMS Pilot 的 ModelFx）：
  // 触发器显示「图标 模型名 强度徽章 ∨」，点开上方浮层选模型和强度。
  let {
    model = $bindable(''),
    effort = $bindable(''),
    lock = false, // 运行中锁模型（强度仍可调，下轮生效）
    customModels = [], // 用户在设置里配置的自定义 Claude 模型 ID
  } = $props();

  const PRESETS = [
    { value: '', label: 'Sonnet', sub: '默认 · 均衡' },
    { value: 'opus', label: 'Opus', sub: '最强 · 更慢' },
    { value: 'haiku', label: 'Haiku', sub: '最快最省' },
  ];
  const MODELS = $derived([
    ...PRESETS,
    ...customModels.map((id) => ({ value: id, label: id, sub: '自定义' })),
  ]);
  const EFFORTS = [
    { value: '', label: '标准' },
    { value: 'low', label: '低' },
    { value: 'medium', label: '中' },
    { value: 'high', label: '高' },
  ];

  let open = $state(false);
  let root = $state();
  const cur = $derived(MODELS.find((m) => m.value === model) || MODELS[0]);
  const effLabel = $derived(EFFORTS.find((e) => e.value === effort)?.label || '');

  function pickModel(v) {
    if (lock) return;
    model = v;
  }
  function pickEffort(v) { effort = v; }

  function onDoc(e) { if (root && !root.contains(e.target)) open = false; }
  function onKey(e) { if (e.key === 'Escape') open = false; }
  $effect(() => {
    if (!open) return;
    document.addEventListener('mousedown', onDoc, true);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onDoc, true);
      document.removeEventListener('keydown', onKey);
    };
  });
</script>

<div class="fx" bind:this={root}>
  <button type="button" class="trig" class:open onclick={() => (open = !open)} data-no-drag title="模型与思考强度">
    <svg class="bi" viewBox="0 0 24 24"><path d="M12 3l2.1 5.4L19.5 10l-5.4 2.1L12 17.5l-2.1-5.4L4.5 10l5.4-1.6L12 3Z"/></svg>
    <span class="lab">{cur.label}</span>
    {#if effLabel && effort}<span class="eff">{effLabel}</span>{/if}
    <svg class="chev" viewBox="0 0 12 12"><path d="M3 4.5 6 7.5 9 4.5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>
  </button>

  {#if open}
    <div class="menu" data-no-drag>
      <div class="sec">模型{#if lock}<span class="sub">本轮进行中不可换</span>{/if}</div>
      {#each MODELS as m (m.value)}
        <button type="button" class="opt" class:on={m.value === model} disabled={lock} onclick={() => pickModel(m.value)}>
          <span class="otext"><b>{m.label}</b><small>{m.sub}</small></span>
          {#if m.value === model}<span class="ck">✓</span>{/if}
        </button>
      {/each}
      <div class="div"></div>
      <div class="sec">思考强度<span class="sub">越高越缜密、越慢</span></div>
      <div class="segs">
        {#each EFFORTS as e (e.value)}
          <button type="button" class="seg" class:on={e.value === effort} onclick={() => pickEffort(e.value)}>{e.label}</button>
        {/each}
      </div>
    </div>
  {/if}
</div>

<style>
  .fx { position: relative; display: inline-flex; }
  .trig {
    display: inline-flex; align-items: center; gap: 5px; height: 26px; box-sizing: border-box;
    padding: 0 7px; border: 1px solid var(--line); border-radius: 8px;
    background: var(--surface); font: inherit; font-size: 12px; color: var(--ink-soft);
    cursor: pointer; white-space: nowrap;
  }
  .trig:hover, .trig.open { border-color: var(--accent-soft); color: var(--accent); }
  .bi { width: 13px; height: 13px; flex: none; fill: var(--accent); }
  .lab { font-weight: 500; }
  .eff { font-size: 10.5px; padding: 0 6px; line-height: 15px; border-radius: 999px; background: var(--accent-wash); color: var(--accent); }
  .chev { width: 10px; height: 10px; opacity: .55; flex: none; }

  .menu {
    position: absolute; bottom: calc(100% + 6px); left: 0; z-index: 60; width: 220px;
    display: flex; flex-direction: column; padding: 6px;
    background: var(--surface); border: 1px solid var(--line); border-radius: 12px; box-shadow: var(--shadow);
  }
  .sec { display: flex; justify-content: space-between; align-items: center; font-size: 10.5px; font-weight: 600; letter-spacing: .04em; color: var(--faint); padding: 4px 8px 5px; }
  .sub { font-weight: 500; letter-spacing: 0; color: var(--muted); }
  .opt { width: 100%; display: flex; align-items: center; gap: 8px; padding: 6px 8px; border: 0; border-radius: 8px; background: transparent; text-align: left; cursor: pointer; font: inherit; color: var(--ink-soft); }
  .opt:hover:not(:disabled) { background: var(--line-soft); }
  .opt.on { color: var(--accent); }
  .opt:disabled { opacity: .5; cursor: default; }
  .otext { flex: 1; display: flex; flex-direction: column; line-height: 1.25; }
  .otext b { font-weight: 600; font-size: 13px; }
  .otext small { color: var(--muted); font-size: 11px; }
  .ck { color: var(--accent); }
  .div { height: 1px; background: var(--line-soft); margin: 5px 4px; }
  .segs { display: flex; gap: 4px; padding: 2px 4px 4px; }
  .seg { flex: 1; padding: .3rem 0; border: 1px solid var(--line); border-radius: 7px; background: transparent; font: inherit; font-size: 12px; color: var(--muted); cursor: pointer; }
  .seg:hover { border-color: var(--accent-soft); }
  .seg.on { border-color: var(--accent); background: var(--accent-wash); color: var(--accent); }
</style>
