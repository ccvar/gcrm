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
