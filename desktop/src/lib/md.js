// Markdown 渲染管线（承自 GCMS Pilot）：marked(GFM) → DOMPurify 消毒 → {@html}。
import { marked } from 'marked';
import DOMPurify from 'dompurify';

marked.setOptions({ gfm: true, breaks: true });

export function mdRender(text) {
  if (!text) return '';
  return DOMPurify.sanitize(marked.parse(text));
}

// 链接点击代理：所有 <a> 拦下走系统浏览器（对话里不该原地导航）。
export function mdClick(ev, openExternal) {
  const a = ev.target.closest('a');
  if (!a) return;
  ev.preventDefault();
  const href = a.getAttribute('href') || '';
  if (/^https?:\/\//i.test(href) || href.startsWith('mailto:')) openExternal(href);
}

// 从找客户回复里抽出 <<<LEADS>>>...<<<END>>> 的结构化线索，并返回剥掉该段的正文。
// 容错：模型偶发漏写 <<<END>>>，也要把从 <<<LEADS>>> 起的整段藏掉，别把裸 JSON 渲染给用户。
export function extractLeads(text) {
  if (!text) return { leads: [], clean: text };
  const start = text.indexOf('<<<LEADS>>>');
  let leads = [];
  if (start >= 0) {
    const withEnd = text.match(/<<<LEADS>>>([\s\S]*?)<<<END>>>/);
    const src = withEnd ? withEnd[1] : text.slice(start + '<<<LEADS>>>'.length);
    const s = src.indexOf('['), e = src.lastIndexOf(']');
    if (s >= 0 && e > s) {
      try {
        const arr = JSON.parse(src.slice(s, e + 1));
        if (Array.isArray(arr)) leads = arr.filter((x) => x && x.company);
      } catch { /* 解析失败当作没有 */ }
    }
  }
  // 有 END 就替换整块；没 END（残缺）就从 <<<LEADS>>> 截到末尾
  let clean = text.replace(/<<<LEADS>>>[\s\S]*?<<<END>>>/g, '').trim();
  const dangling = clean.indexOf('<<<LEADS>>>');
  if (dangling >= 0) clean = clean.slice(0, dangling).trim();
  return { leads, clean };
}

// 流式中途把尚未闭合的 <<<LEADS>>> 段从展示里藏掉，别让用户看到半截 JSON。
export function stripLeadsLive(text) {
  if (!text) return text;
  const i = text.indexOf('<<<LEADS>>>');
  return i < 0 ? text : text.slice(0, i).trimEnd();
}
