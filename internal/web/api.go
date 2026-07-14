package web

// 通道 A：自动化接口。外部 AI 工具（Claude Code / Codex 等）持 ccrm_ 密钥调用，
// CRM 不在此处调用任何 AI API —— 与 GCMS 的自动化接口哲学一致。

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"crm.ccvar.com/internal/store"
)

type apiHandler func(w http.ResponseWriter, r *http.Request, key *store.AutomationKey)

func (s *Server) apiAuth(next apiHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !strings.HasPrefix(raw, "ccrm_") {
			jsonErr(w, http.StatusUnauthorized, "缺少有效的 Bearer 密钥（ccrm_ 前缀）")
			return
		}
		key, err := s.st.AutomationKeyByHash(hashToken(raw))
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		if key == nil {
			jsonErr(w, http.StatusUnauthorized, "密钥无效或已停用")
			return
		}
		if r.Method != http.MethodGet && !strings.Contains(key.Scopes, "write") {
			jsonErr(w, http.StatusForbidden, "该密钥为只读权限")
			return
		}
		_ = s.st.TouchAutomationKey(key.ID)
		next(w, r, key)
	}
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) apiPing(w http.ResponseWriter, _ *http.Request, key *store.AutomationKey) {
	jsonOK(w, map[string]any{"ok": true, "key": key.Name, "scopes": key.Scopes})
}

type apiCustomer struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Company      string `json:"company,omitempty"`
	Phone        string `json:"phone,omitempty"`
	Wechat       string `json:"wechat,omitempty"`
	Email        string `json:"email,omitempty"`
	Source       string `json:"source,omitempty"`
	Stage        string `json:"stage"`
	Intent       string `json:"intent"`
	IntentReason string `json:"intent_reason,omitempty"`
	Notes        string `json:"notes,omitempty"`
	UpdatedAt    int64  `json:"updated_at"`
}

func toAPICustomer(c *store.Customer) apiCustomer {
	return apiCustomer{
		ID: c.ID, Name: c.Name, Company: c.Company, Phone: c.Phone, Wechat: c.Wechat,
		Email: c.Email, Source: c.Source, Stage: c.Stage, Intent: c.Intent,
		IntentReason: c.IntentReason, Notes: c.Notes, UpdatedAt: c.UpdatedAt,
	}
}

func (s *Server) apiCustomers(w http.ResponseWriter, r *http.Request, _ *store.AutomationKey) {
	list, err := s.st.Customers(strings.TrimSpace(r.URL.Query().Get("q")), 500)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]apiCustomer, 0, len(list))
	for i := range list {
		out = append(out, toAPICustomer(&list[i]))
	}
	jsonOK(w, map[string]any{"customers": out})
}

func (s *Server) apiCustomerCreate(w http.ResponseWriter, r *http.Request, _ *store.AutomationKey) {
	var in apiCustomer
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		jsonErr(w, http.StatusBadRequest, "请求体需要 JSON，且 name 必填")
		return
	}
	id, err := s.st.CreateCustomer(&store.Customer{
		Name: strings.TrimSpace(in.Name), Company: in.Company, Phone: in.Phone,
		Wechat: in.Wechat, Email: in.Email, Source: in.Source, Notes: in.Notes,
	})
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "创建失败")
		return
	}
	c, _ := s.st.Customer(id)
	jsonOK(w, toAPICustomer(c))
}

func (s *Server) apiCustomerDetail(w http.ResponseWriter, r *http.Request, _ *store.AutomationKey) {
	c, err := s.st.Customer(pathID(r))
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	if c == nil {
		jsonErr(w, http.StatusNotFound, "客户不存在")
		return
	}
	its, _ := s.st.Interactions(c.ID, 100)
	tasks, _ := s.st.TasksForCustomer(c.ID)
	deals, _ := s.st.DealsForCustomer(c.ID)
	jsonOK(w, map[string]any{
		"customer": toAPICustomer(c), "interactions": its, "tasks": tasks, "deals": deals,
	})
}

// apiTasks 列全部待办（带客户名），Pilot 工作台以此为数据源。
func (s *Server) apiTasks(w http.ResponseWriter, _ *http.Request, _ *store.AutomationKey) {
	rows, err := s.st.OpenTaskRows()
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	type apiTask struct {
		ID           int64  `json:"id"`
		CustomerID   int64  `json:"customer_id"`
		CustomerName string `json:"customer_name"`
		Title        string `json:"title"`
		Detail       string `json:"detail,omitempty"`
		AIDraft      string `json:"ai_draft,omitempty"`
		Source       string `json:"source"`
		DueAt        int64  `json:"due_at"`
	}
	out := make([]apiTask, 0, len(rows))
	for _, t := range rows {
		out = append(out, apiTask{
			ID: t.ID, CustomerID: t.CustomerID, CustomerName: t.CustomerName,
			Title: t.Title, Detail: t.Detail, AIDraft: t.AIDraft, Source: t.Source, DueAt: t.DueAt,
		})
	}
	jsonOK(w, map[string]any{"tasks": out})
}

func (s *Server) apiTaskDone(w http.ResponseWriter, r *http.Request, _ *store.AutomationKey) {
	t, err := s.st.Task(pathID(r))
	if err != nil || t == nil {
		jsonErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	if err := s.st.CompleteTask(t.ID); err != nil {
		jsonErr(w, http.StatusInternalServerError, "操作失败")
		return
	}
	jsonOK(w, map[string]any{"ok": true, "id": t.ID})
}

// apiDeals 已关单商机 + AI 复盘（?stage=won|lost 可过滤），供通道 A 做团队级分析。
func (s *Server) apiDeals(w http.ResponseWriter, r *http.Request, _ *store.AutomationKey) {
	rows, err := s.st.ClosedDealRows(500)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	stage := r.URL.Query().Get("stage")
	type apiDeal struct {
		ID            int64  `json:"id"`
		CustomerID    int64  `json:"customer_id"`
		CustomerName  string `json:"customer_name"`
		Title         string `json:"title"`
		AmountCents   int64  `json:"amount_cents"`
		Stage         string `json:"stage"`
		ClosedAt      int64  `json:"closed_at"`
		LostReason    string `json:"lost_reason,omitempty"`
		Review        string `json:"review,omitempty"`
		ReviewLessons string `json:"review_lessons,omitempty"`
	}
	out := make([]apiDeal, 0, len(rows))
	for _, d := range rows {
		if stage != "" && d.Stage != stage {
			continue
		}
		out = append(out, apiDeal{
			ID: d.ID, CustomerID: d.CustomerID, CustomerName: d.CustomerName, Title: d.Title,
			AmountCents: d.AmountCents, Stage: d.Stage, ClosedAt: d.ClosedAt,
			LostReason: d.LostReason, Review: d.Review, ReviewLessons: d.ReviewLessons,
		})
	}
	jsonOK(w, map[string]any{"deals": out})
}

func (s *Server) apiInteractionCreate(w http.ResponseWriter, r *http.Request, _ *store.AutomationKey) {
	c, err := s.st.Customer(pathID(r))
	if err != nil || c == nil {
		jsonErr(w, http.StatusNotFound, "客户不存在")
		return
	}
	var in struct {
		Channel    string `json:"channel"`
		Direction  string `json:"direction"`
		Content    string `json:"content"`
		OccurredAt int64  `json:"occurred_at"`
		SkipAI     bool   `json:"skip_ai"` // 批量导入历史数据时可跳过 AI 分析
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Content) == "" {
		jsonErr(w, http.StatusBadRequest, "请求体需要 JSON，且 content 必填")
		return
	}
	if in.OccurredAt == 0 {
		in.OccurredAt = time.Now().Unix()
	}
	id, err := s.st.AddInteraction(&store.Interaction{
		CustomerID: c.ID, Channel: in.Channel, Direction: in.Direction,
		Content: strings.TrimSpace(in.Content), OccurredAt: in.OccurredAt,
	})
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "写入失败")
		return
	}
	queued := false
	if !in.SkipAI && s.aiConfigured() {
		payload, _ := json.Marshal(map[string]int64{"interaction_id": id})
		if _, err := s.st.EnqueueAIJob("interaction_extract", string(payload)); err == nil {
			queued = true
		}
	}
	jsonOK(w, map[string]any{"id": id, "ai_queued": queued})
}
