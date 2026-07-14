package ai

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"crm.ccvar.com/internal/store"
)

// Worker 消费 ai_jobs 队列。main 里一个 goroutine ticker 驱动。
type Worker struct {
	St *store.Store
}

func (w *Worker) Run(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for range t.C {
		for {
			job, err := w.St.ClaimAIJob()
			if err != nil {
				log.Printf("ai: 取任务失败: %v", err)
				break
			}
			if job == nil {
				break
			}
			result, err := w.run(job)
			if err != nil {
				log.Printf("ai: 任务 #%d(%s) 第 %d 次失败: %v", job.ID, job.Kind, job.Attempts, err)
				_ = w.St.FailAIJob(job.ID, job.Attempts, err.Error())
				continue
			}
			_ = w.St.FinishAIJob(job.ID, result)
		}
	}
}

func (w *Worker) config() Config {
	base, _ := w.St.Setting("ai.base_url")
	key, _ := w.St.Setting("ai.api_key")
	model, _ := w.St.Setting("ai.model")
	return Config{BaseURL: base, APIKey: key, Model: model}
}

func (w *Worker) run(job *store.AIJob) (string, error) {
	switch job.Kind {
	case "interaction_extract":
		return w.runInteractionExtract(job.Payload)
	case "deal_review":
		return w.runDealReview(job.Payload)
	default:
		return "", fmt.Errorf("未知任务类型 %q", job.Kind)
	}
}

// ---------- interaction_extract：从一次沟通中提取意向/异议/下一步 ----------

type extractPayload struct {
	InteractionID int64 `json:"interaction_id"`
}

type extractResult struct {
	Summary      string   `json:"summary"`
	Intent       string   `json:"intent"`
	IntentReason string   `json:"intent_reason"`
	Objections   []string `json:"objections"`
	NextAction   struct {
		Title   string `json:"title"`
		DueDays int    `json:"due_days"`
		Draft   string `json:"draft"`
	} `json:"next_action"`
}

const extractSystem = `你是一名资深销售教练，嵌在 CRM 里分析销售与客户的沟通记录。
你的判断会直接生成跟进任务，务必基于记录本身，不要臆造。只输出 JSON，不要任何其他文字。`

func (w *Worker) runInteractionExtract(payload string) (string, error) {
	var p extractPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return "", fmt.Errorf("payload 解析失败: %w", err)
	}
	it, err := w.St.Interaction(p.InteractionID)
	if err != nil || it == nil {
		return "", fmt.Errorf("交互 #%d 不存在: %v", p.InteractionID, err)
	}
	cust, err := w.St.Customer(it.CustomerID)
	if err != nil || cust == nil {
		return "", fmt.Errorf("客户 #%d 不存在: %v", it.CustomerID, err)
	}
	history, _ := w.St.Interactions(it.CustomerID, 6)

	var b strings.Builder
	fmt.Fprintf(&b, "客户：%s", cust.Name)
	if cust.Company != "" {
		fmt.Fprintf(&b, "（%s）", cust.Company)
	}
	fmt.Fprintf(&b, "，当前阶段：%s，当前意向：%s\n", cust.Stage, cust.Intent)
	if cust.Notes != "" {
		fmt.Fprintf(&b, "备注：%s\n", cust.Notes)
	}
	if len(history) > 1 {
		b.WriteString("\n近期沟通（从早到晚）：\n")
		for i := len(history) - 1; i >= 0; i-- {
			h := history[i]
			if h.ID == it.ID {
				continue
			}
			fmt.Fprintf(&b, "- [%s/%s] %s\n", h.Channel, h.Direction, truncate(h.Content, 300))
		}
	}
	fmt.Fprintf(&b, "\n本次沟通（%s，%s）：\n%s\n", it.Channel, directionLabel(it.Direction), it.Content)
	b.WriteString(`
请输出 JSON：
{
  "summary": "一句话概括本次沟通的核心内容",
  "intent": "low|medium|high",
  "intent_reason": "判断意向的依据，一句话",
  "objections": ["客户提出的异议，没有则为空数组"],
  "next_action": {
    "title": "下一步跟进动作，一句话；若确无必要跟进则留空字符串",
    "due_days": 2,
    "draft": "可直接发给客户的跟进消息草稿，口吻自然，不超过 150 字；无需跟进则留空"
  }
}`)

	raw, err := Chat(w.config(), extractSystem, b.String())
	if err != nil {
		return "", err
	}
	var res extractResult
	cleaned := StripJSON(raw)
	if err := json.Unmarshal([]byte(cleaned), &res); err != nil {
		return "", fmt.Errorf("模型输出不是有效 JSON: %s", truncate(cleaned, 200))
	}

	if err := w.St.SetInteractionAI(it.ID, res.Summary, cleaned); err != nil {
		return "", err
	}
	switch res.Intent {
	case "low", "medium", "high":
		_ = w.St.UpdateCustomerIntent(cust.ID, res.Intent, res.IntentReason)
	}
	if t := strings.TrimSpace(res.NextAction.Title); t != "" {
		detail := "由 AI 根据沟通记录生成"
		if len(res.Objections) > 0 {
			detail += "；待处理异议：" + strings.Join(res.Objections, "；")
		}
		due := time.Now().AddDate(0, 0, clampDays(res.NextAction.DueDays)).Unix()
		_, _ = w.St.CreateTask(&store.Task{
			CustomerID: cust.ID,
			Title:      t,
			Detail:     detail,
			AIDraft:    res.NextAction.Draft,
			Source:     "ai",
			DueAt:      due,
		})
	}
	return res.Summary, nil
}

// ---------- deal_review：关单后回溯 timeline，做赢单/丢单归因 ----------

type reviewPayload struct {
	DealID int64 `json:"deal_id"`
}

type reviewResult struct {
	Review  string   `json:"review"`
	Lessons []string `json:"lessons"`
}

const reviewSystem = `你是一名销售总监，负责关单复盘。基于完整沟通时间线做归因：
赢单要找出可复制的关键动作；丢单要定位流失环节和未化解的异议。
结论要具体、可执行，不要空话。只输出 JSON，不要任何其他文字。`

func (w *Worker) runDealReview(payload string) (string, error) {
	var p reviewPayload
	if err := json.Unmarshal([]byte(payload), &p); err != nil {
		return "", fmt.Errorf("payload 解析失败: %w", err)
	}
	deal, err := w.St.Deal(p.DealID)
	if err != nil || deal == nil {
		return "", fmt.Errorf("商机 #%d 不存在: %v", p.DealID, err)
	}
	cust, err := w.St.Customer(deal.CustomerID)
	if err != nil || cust == nil {
		return "", fmt.Errorf("客户 #%d 不存在: %v", deal.CustomerID, err)
	}
	history, _ := w.St.Interactions(deal.CustomerID, 30)

	var b strings.Builder
	outcome := "赢单（已成交）"
	if deal.Stage == "lost" {
		outcome = "丢单"
		if deal.LostReason != "" {
			outcome += "（销售填写的原因：" + deal.LostReason + "）"
		}
	}
	fmt.Fprintf(&b, "商机：%s，金额 %.2f 元，结果：%s\n", deal.Title, float64(deal.AmountCents)/100, outcome)
	fmt.Fprintf(&b, "客户：%s", cust.Name)
	if cust.Company != "" {
		fmt.Fprintf(&b, "（%s）", cust.Company)
	}
	fmt.Fprintf(&b, "，来源：%s\n", cust.Source)
	if len(history) > 0 {
		b.WriteString("\n完整沟通时间线（从早到晚）：\n")
		for i := len(history) - 1; i >= 0; i-- {
			h := history[i]
			fmt.Fprintf(&b, "- [%s/%s] %s\n", h.Channel, directionLabel(h.Direction), truncate(h.Content, 400))
		}
	} else {
		b.WriteString("\n（无沟通记录，请在复盘中指出过程数据缺失本身就是问题）\n")
	}
	b.WriteString(`
请输出 JSON：
{
  "review": "复盘正文，200 字以内：关键节点、归因、当时本可以怎么做",
  "lessons": ["可沉淀到团队 playbook 的经验，每条一句话，1-3 条"]
}`)

	raw, err := Chat(w.config(), reviewSystem, b.String())
	if err != nil {
		return "", err
	}
	var res reviewResult
	cleaned := StripJSON(raw)
	if err := json.Unmarshal([]byte(cleaned), &res); err != nil {
		return "", fmt.Errorf("模型输出不是有效 JSON: %s", truncate(cleaned, 200))
	}
	if err := w.St.SetDealReview(deal.ID, strings.TrimSpace(res.Review), strings.Join(res.Lessons, "\n")); err != nil {
		return "", err
	}
	return res.Review, nil
}

func clampDays(d int) int {
	if d < 0 {
		return 0
	}
	if d > 30 {
		return 30
	}
	return d
}

func directionLabel(d string) string {
	if d == "in" {
		return "客户发来"
	}
	return "我方发出"
}
