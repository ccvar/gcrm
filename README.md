# CCVAR CRM

AI 驱动的转化型 CRM —— Go + SQLite 单二进制，工程范式与视觉系统承自 [cms.ccvar.com](../cms.ccvar.com)。

核心理念：**找客户重要，转化更重要**。销售不是数据录入员——系统消化原始沟通记录，
反过来输出行动建议（意向判定、异议识别、跟进草稿），销售负责执行和纠偏。

## 运行

```bash
go run .
# 打开 http://localhost:8090 ，首次访问会引导创建管理员
```

环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `CRM_DB` | `data/crm.db` | SQLite 路径（后期可切 MySQL，SQL 已按方言无关子集约束） |
| `CRM_ADDR` | `:8090` | 监听地址 |
| `BASE_URL` | `http://localhost:8090` | 对外地址（https 时会话 cookie 加 Secure） |

## AI 双通道

- **通道 B（内嵌）**：后台「设置」页配置 OpenAI 兼容端点（DeepSeek / Kimi / 通义 / Claude 等）。
  配置后每条沟通记录异步走 `ai_jobs` 队列：提取摘要 + 意向评级（附依据）+ 异议清单，
  并自动生成带话术草稿的跟进任务，出现在首页「今日行动」。AI 起草，人来发送。
- **通道 A（外部 AI 工具，GCMS 模式）**：「设置」页创建 `ccrm_` 自动化密钥，
  Claude Code / Codex 等工具经 `/api/v1/*` 做批量导入、复盘分析。CRM 本身不在此通道调用 AI。

### 自动化接口（v0）

```
GET  /api/v1/ping
GET  /api/v1/customers?q=
POST /api/v1/customers                      {"name": "...", "company": "...", ...}
GET  /api/v1/customers/{id}                 （含 interactions / tasks / deals）
POST /api/v1/customers/{id}/interactions    {"channel","direction","content","occurred_at","skip_ai"}
```

鉴权：`Authorization: Bearer ccrm_…`；只读密钥拒绝写操作；密钥库中只存 SHA-256。

## 结构

```
main.go            embed 模板/资源，启动 HTTP + AI worker + 会话清理
internal/store/    数据层（全部 SQL 在此；migration 带版本号）
internal/web/      handler：行动队列 / 客户 timeline / 设置 / 自动化 API
internal/ai/       通道 B：OpenAI 兼容客户端 + interaction_extract 提取任务
templates/         Go html/template（admin 骨架承自 CMS）
assets/            设计令牌移植自 CMS（强调色换墨绿），零外部字体
```

## 路线图

1. ✅ v0 骨架：登录、客户/交互/任务/商机、AI 提取闭环、自动化密钥
2. ✅ 技能包下载（设置页 → `crm-assistant-skillpack.zip`，SKILL.md + OpenAPI）
3. ✅ 赢单/丢单复盘：关单自动触发 `deal_review`，「复盘」页沉淀 playbook，
   `GET /api/v1/deals` 供通道 A 做团队级归因
4. ✅ 桌面客户端 **CRM Pilot**（[desktop/](desktop/)，Tauri 2 + Svelte 5，macOS + Windows）：
   行动队列工作台 + 系统级到期通知；密钥进系统钥匙串，Rust 侧代理 API
5. ✅ CRM Pilot 进阶：托盘常驻（关窗隐藏）、本地大脑（驱动本机 Claude Code CLI
   做今日作战重点 / 赢单丢单归因 / 自定义分析，零 API 计费）
6. GitHub Actions：CI（push/PR）、`v*` tag 发服务端全平台二进制、
   `pilot-v*` tag 发桌面端（macOS dmg + Windows NSIS）
7. 待做：Pilot 自动更新与签名公证、Codex 分析接入、MySQL / 云数据库适配
