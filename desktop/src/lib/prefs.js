// 本地偏好（localStorage）：记住上次用的模型/强度/权限做新对话默认，
// 以及用户配置的自定义 Claude 模型 ID（参考 GCMS Pilot 的 prefs.customClaudeIds）。
const KEY = 'gcrm.pilot.prefs';

export function loadPrefs() {
  const def = { model: '', effort: '', perm: 'plan', customClaudeIds: [] };
  try {
    return { ...def, ...JSON.parse(localStorage.getItem(KEY) || '{}') };
  } catch {
    return def;
  }
}

export function savePrefs(p) {
  try {
    localStorage.setItem(KEY, JSON.stringify(p));
  } catch {
    /* localStorage 不可用时忽略 */
  }
}
