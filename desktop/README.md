# GCRM Pilot

CCVAR CRM 桌面客户端（Tauri 2 + Svelte 5），支持 **macOS 与 Windows**。
架构承自 GCMS Pilot：密钥进系统钥匙串、Rust 侧代理请求、前端永远见不到明文。

## 定位

销售每天泡在里面的工作台（GCMS Pilot 是低频批量驾驶舱，这是关键差异）：

- **行动队列**：逾期 / 今日到期 / 之后，三桶待办，一键完成
- **AI 跟进草稿**：展开即读，一键复制去发送
- **系统级提醒**：启动或刷新时发现到期任务，弹系统通知（网页版给不了的转化抓手）
- **托盘常驻**：关窗隐藏到托盘，后台每 5 分钟自动刷新；托盘菜单可直接刷新/退出
- **本地大脑**（分析页签）：检测本机 claude / codex CLI 与登录态，未登录一键「去授权」
  （打开终端跑官方登录命令）。深度分析（今日作战重点 / 赢单丢单归因月报 / 自定义问题）
  跑在你自己的 Claude Code 订阅上，零 API 计费；数据由 Pilot 从 CRM 拉好喂给模型，
  **密钥不进子进程**（比 GCMS 的注入 env 更进一步）；流式输出，可随时停止
  （停止时对整个进程组 SIGKILL，不留孤儿进程）

## 安全模型（承自 GCMS Pilot）

- `ccrm_` 密钥只写入 **macOS 钥匙串 / Windows 凭据管理器**（`keyring` crate），
  绝不落盘、绝不进 WebView——前端只知道「有没有密钥」
- 所有请求由 Rust 侧发出并注入 `Authorization`，且只允许 `/api/v1/` 路径
- 服务器地址存 `pilot.json`（应用配置目录），不含敏感信息

## 开发

```bash
npm install
npm run tauri dev   # 需先启动 CRM 服务端（默认 http://localhost:8090）
```

依赖：Rust ≥ 1.77、Node ≥ 20。

## 打包

```bash
npm run tauri build
```

- macOS 产物：`src-tauri/target/release/bundle/`（`.app` / `.dmg`，ad-hoc 签名）
- Windows 产物：`.exe`（NSIS 安装器），需在 Windows 机器或 CI 上构建

## 尚未接入（对外分发前的前置，同 GCMS Pilot 的冻结方案）

- 自动更新（tauri updater + 独立 release 仓库与 ed25519 签名密钥）
- 代码签名 / 公证（Apple Developer 证书 + notarytool；Windows 代码签名证书）
- Codex CLI 的分析接入（已检测登录态，执行通道未接）
