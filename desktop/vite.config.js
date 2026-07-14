import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// Tauri 开发约定：固定端口、失败即退，不清屏以保留 Rust 报错
export default defineConfig({
  plugins: [svelte()],
  clearScreen: false,
  server: { port: 1430, strictPort: true },
  build: { target: 'es2021' },
});
