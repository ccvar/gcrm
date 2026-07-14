package web

// 技能包下载（对齐 GCMS 自动化文档模式）：给 Claude Code / Codex / Cursor 等
// AI 工具的接入说明包。CRM 不在通道 A 调用任何 AI API —— AI 工具持你授权的
// ccrm_ 密钥调用自动化接口。包里不含密钥，需自行填入 .env。

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

func (s *Server) skillPackDownload(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := []struct{ name, body string }{
		{"README.md", skillPackReadme(s.baseURL)},
		{"crm-assistant/SKILL.md", skillMarkdown(s.baseURL)},
		{"crm-assistant/references/openapi.json", openAPIJSON(s.baseURL)},
		{"crm-assistant/.env.example", "CRM_BASE_URL=" + s.baseURL + "\nCRM_API_KEY=ccrm_在设置页创建后填入\n"},
	}
	for _, f := range files {
		fw, err := zw.Create(f.name)
		if err == nil {
			_, err = fw.Write([]byte(f.body))
		}
		if err != nil {
			log.Printf("web: 技能包生成失败: %v", err)
			http.Error(w, "生成失败", http.StatusInternalServerError)
			return
		}
	}
	if err := zw.Close(); err != nil {
		http.Error(w, "生成失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="crm-assistant-skillpack.zip"`)
	_, _ = w.Write(buf.Bytes())
}

func skillPackReadme(base string) string {
	return strings.Join([]string{
		"# GCRM 助手技能包",
		"",
		"这个包给 Claude Code、Codex、Cursor 等能读取文件的 AI 工具使用。",
		"让 AI 先阅读 `crm-assistant/SKILL.md`，再根据 `references/openapi.json` 调用 CRM 自动化接口。",
		"",
		"## 准备",
		"",
		"1. 在 CRM 后台「设置 → 自动化密钥」创建一把 `ccrm_` 密钥（明文只显示一次）。",
		"2. 把密钥填入 `crm-assistant/.env.example` 并改名为 `.env`。",
		"3. 服务地址当前为 `" + base + "`，如有变化同步修改。",
		"",
		"## 典型用法",
		"",
		"- 批量导入线索：整理名单后逐条 `POST /api/v1/customers`。",
		"- 导入历史沟通：`POST /api/v1/customers/{id}/interactions`，历史数据带 `\"skip_ai\": true` 避免批量触发分析。",
		"- 团队复盘：`GET /api/v1/deals?stage=lost` 拉取丢单及 AI 复盘，做月度归因报告。",
		"",
		"## 边界",
		"",
		"- 密钥只进 `.env`，不要写进任何会提交的文件。",
		"- 不要尝试修改 CRM 的管理员账号、AI 模型配置或密钥本身（接口也不提供）。",
		"",
	}, "\n")
}

func skillMarkdown(base string) string {
	return strings.Join([]string{
		"---",
		"name: gcrm-assistant",
		"description: 通过 GCRM 自动化接口做线索导入、沟通记录写入、待办查询与赢单/丢单复盘分析。",
		"---",
		"",
		"# GCRM 自动化接口",
		"",
		"基址：`" + base + "`（以 `.env` 的 `CRM_BASE_URL` 为准）",
		"鉴权：每个请求带 `Authorization: Bearer $CRM_API_KEY`（`ccrm_` 前缀，只读密钥拒绝写操作）。",
		"",
		"## 接口",
		"",
		"| 方法 | 路径 | 说明 |",
		"|---|---|---|",
		"| GET | /api/v1/ping | 验证密钥，返回密钥名与权限 |",
		"| GET | /api/v1/customers?q= | 客户列表（q 模糊匹配姓名/公司/电话/微信） |",
		"| POST | /api/v1/customers | 建客户，`name` 必填，可带 company/phone/wechat/email/source/notes |",
		"| GET | /api/v1/customers/{id} | 客户详情，含 interactions / tasks / deals |",
		"| POST | /api/v1/customers/{id}/interactions | 写沟通记录：channel(wechat/phone/email/meeting/other)、direction(in/out)、content 必填、occurred_at(unix 秒)、skip_ai |",
		"| GET | /api/v1/tasks | 全部待办任务（带客户名、AI 草稿、due_at） |",
		"| POST | /api/v1/tasks/{id}/done | 完成任务 |",
		"| GET | /api/v1/deals?stage=won\\|lost | 已关单商机 + AI 复盘与 playbook 沉淀 |",
		"",
		"## 约定",
		"",
		"- 时间一律 unix 秒；金额字段为分（amount_cents）。",
		"- 批量导入历史沟通务必 `\"skip_ai\": true`——实时沟通才需要 AI 分析，历史回填会浪费模型额度并制造过期任务。",
		"- 导入前先 `GET /api/v1/customers?q=` 查重，避免重复建档。",
		"- 错误响应统一为 `{\"error\": \"...\"}`，HTTP 状态码对应含义。",
		"",
		"## 场景一：找客户（获客）",
		"",
		"用户给理想客户画像时，用你的联网检索能力（WebSearch/WebFetch）找潜在客户，再写回 CRM：",
		"",
		"1. 只取**公开来源**：企业官网、行业名录、招投标/中标公告、招聘信息、新闻、展会名单。不要抓需要登录的社媒私密数据，不要伪造。",
		"2. 整理成 Markdown 表格给用户看：公司名｜对接人/部门｜公开联系方式｜来源链接｜为什么是潜客。",
		"3. **等用户确认导入哪些**（关键动作，必须人拍板），再逐条建客户：",
		"   `curl -sS -X POST \"$CRM_BASE_URL/api/v1/customers\" -H \"Authorization: Bearer $CRM_API_KEY\" -H 'Content-Type: application/json' -d '{\"name\":\"张三\",\"company\":\"某某新能源\",\"phone\":\"...\",\"source\":\"AI获客·行业名录\",\"notes\":\"线索依据 + 来源链接\"}'`",
		"4. 导入前对每家先 `GET /api/v1/customers?q=公司名` 查重。每导入一批向用户报告结果。",
		"",
		"## 场景二：已有名单加工",
		"",
		"用户给一份名单（表格/文本），你负责清洗去重、补全（用联网检索补公司/职位/公开联系方式）、按画像打意向优先级，",
		"再按用户确认导入。同一批里同名同公司只建一条。",
		"",
		"## 场景三：外联话术",
		"",
		"针对某个客户（先 `GET /api/v1/customers/{id}` 拉全景），写冷启动私信/邮件或多轮跟进话术。",
		"话术只**起草**给用户，由用户去发；发送不是本接口的能力，不要假装发出去。",
		"",
		"## 不允许的事",
		"",
		"- 不要伪造客户/线索/沟通记录；导入数据必须来自真实的公开来源或用户提供的材料，注明出处。",
		"- 写入类操作（建客户、记沟通、改阶段、完成任务）在执行前一定先向用户说明将要做什么并取得同意。",
		"- 不要循环高频轮询或对目标站点高频抓取；控制在合理节奏。",
		"",
	}, "\n")
}

// openAPIJSON 生成当前自动化接口的 OpenAPI 3.0 描述。
func openAPIJSON(base string) string {
	obj := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	str := map[string]any{"type": "string"}
	i64 := map[string]any{"type": "integer", "format": "int64"}
	boolean := map[string]any{"type": "boolean"}

	customer := obj(map[string]any{
		"id": i64, "name": str, "company": str, "phone": str, "wechat": str,
		"email": str, "source": str, "stage": str, "intent": str,
		"intent_reason": str, "notes": str, "updated_at": i64,
	})
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "GCRM 自动化接口",
			"version":     "v1",
			"description": "CRM 不调用 AI API；外部 AI 工具使用访问密钥调用这里的接口做线索导入、沟通写入与复盘分析。",
		},
		"servers": []any{map[string]any{"url": base}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearer": map[string]any{"type": "http", "scheme": "bearer", "description": "ccrm_ 前缀自动化密钥"},
			},
			"schemas": map[string]any{"Customer": customer},
		},
		"security": []any{map[string]any{"bearer": []any{}}},
		"paths": map[string]any{
			"/api/v1/ping": map[string]any{"get": map[string]any{
				"summary": "验证密钥", "responses": map[string]any{"200": map[string]any{"description": "ok"}},
			}},
			"/api/v1/customers": map[string]any{
				"get": map[string]any{
					"summary": "客户列表",
					"parameters": []any{map[string]any{"name": "q", "in": "query", "schema": str}},
					"responses":  map[string]any{"200": map[string]any{"description": "customers 数组"}},
				},
				"post": map[string]any{
					"summary": "建客户（name 必填）",
					"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/Customer"}}}},
					"responses": map[string]any{"200": map[string]any{"description": "创建后的客户"}},
				},
			},
			"/api/v1/customers/{id}": map[string]any{"get": map[string]any{
				"summary":    "客户详情（含 interactions/tasks/deals）",
				"parameters": []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": i64}},
				"responses":  map[string]any{"200": map[string]any{"description": "详情"}, "404": map[string]any{"description": "不存在"}},
			}},
			"/api/v1/customers/{id}/interactions": map[string]any{"post": map[string]any{
				"summary":    "写沟通记录（content 必填；历史导入带 skip_ai=true）",
				"parameters": []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": i64}},
				"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{
					"schema": obj(map[string]any{
						"channel": str, "direction": str, "content": str,
						"occurred_at": i64, "skip_ai": boolean,
					})}}},
				"responses": map[string]any{"200": map[string]any{"description": "{id, ai_queued}"}},
			}},
			"/api/v1/tasks": map[string]any{"get": map[string]any{
				"summary": "全部待办任务", "responses": map[string]any{"200": map[string]any{"description": "tasks 数组"}},
			}},
			"/api/v1/tasks/{id}/done": map[string]any{"post": map[string]any{
				"summary":    "完成任务",
				"parameters": []any{map[string]any{"name": "id", "in": "path", "required": true, "schema": i64}},
				"responses":  map[string]any{"200": map[string]any{"description": "ok"}},
			}},
			"/api/v1/deals": map[string]any{"get": map[string]any{
				"summary":    "已关单商机 + AI 复盘",
				"parameters": []any{map[string]any{"name": "stage", "in": "query", "schema": map[string]any{"type": "string", "enum": []any{"won", "lost"}}}},
				"responses":  map[string]any{"200": map[string]any{"description": "deals 数组"}},
			}},
		},
	}
	b, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b)
}
