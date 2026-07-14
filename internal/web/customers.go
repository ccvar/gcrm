package web

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)
import "crm.ccvar.com/internal/store"

// ---------- 首页：今日行动队列 ----------

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	v := s.view(r, "今日行动")
	var err error
	v.Stats, err = s.st.DashboardStats()
	if err != nil {
		log.Printf("web: 统计失败: %v", err)
	}
	rows, err := s.st.OpenTaskRows()
	if err != nil {
		log.Printf("web: 读任务失败: %v", err)
	}
	nowT := time.Now()
	startToday := time.Date(nowT.Year(), nowT.Month(), nowT.Day(), 0, 0, 0, 0, time.Local).Unix()
	endToday := startToday + 86400
	for _, t := range rows {
		switch {
		case t.DueAt != 0 && t.DueAt < startToday:
			v.Overdue = append(v.Overdue, t)
		case t.DueAt >= startToday && t.DueAt < endToday:
			v.Today = append(v.Today, t)
		default:
			v.Later = append(v.Later, t)
		}
	}
	if len(v.Later) > 20 {
		v.Later = v.Later[:20]
	}
	s.render(w, r, "dashboard", v)
}

// ---------- 客户列表 / 新建 ----------

func (s *Server) customersPage(w http.ResponseWriter, r *http.Request) {
	v := s.view(r, "客户")
	v.Query = strings.TrimSpace(r.URL.Query().Get("q"))
	var err error
	v.Customers, err = s.st.Customers(v.Query, 200)
	if err != nil {
		log.Printf("web: 读客户失败: %v", err)
	}
	s.render(w, r, "customers", v)
}

func (s *Server) customerCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		setFlash(w, "客户姓名不能为空")
		http.Redirect(w, r, "/customers", http.StatusSeeOther)
		return
	}
	id, err := s.st.CreateCustomer(&store.Customer{
		Name:    name,
		Company: strings.TrimSpace(r.FormValue("company")),
		Phone:   strings.TrimSpace(r.FormValue("phone")),
		Wechat:  strings.TrimSpace(r.FormValue("wechat")),
		Email:   strings.TrimSpace(r.FormValue("email")),
		Source:  strings.TrimSpace(r.FormValue("source")),
		Notes:   strings.TrimSpace(r.FormValue("notes")),
	})
	if err != nil {
		log.Printf("web: 建客户失败: %v", err)
		setFlash(w, "创建失败，请重试")
		http.Redirect(w, r, "/customers", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
}

// ---------- 客户详情 ----------

func (s *Server) customerDetail(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	cust, err := s.st.Customer(id)
	if err != nil {
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	if cust == nil {
		http.NotFound(w, r)
		return
	}
	v := s.view(r, cust.Name)
	v.Path = "/customers"
	v.Customer = cust
	v.Interactions, _ = s.st.Interactions(id, 100)
	v.Tasks, _ = s.st.TasksForCustomer(id)
	v.Deals, _ = s.st.DealsForCustomer(id)
	v.AIConfigured = s.aiConfigured()
	s.render(w, r, "customer_detail", v)
}

func (s *Server) customerProfileSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		setFlash(w, "姓名不能为空")
		http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
		return
	}
	err := s.st.UpdateCustomerProfile(id, name,
		strings.TrimSpace(r.FormValue("company")), strings.TrimSpace(r.FormValue("phone")),
		strings.TrimSpace(r.FormValue("wechat")), strings.TrimSpace(r.FormValue("email")),
		strings.TrimSpace(r.FormValue("source")), strings.TrimSpace(r.FormValue("notes")))
	if err != nil {
		log.Printf("web: 更新客户失败: %v", err)
		setFlash(w, "保存失败")
	} else {
		setFlash(w, "资料已保存")
	}
	http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
}

var validStages = map[string]bool{"lead": true, "contacted": true, "negotiating": true, "won": true, "lost": true}

func (s *Server) customerStageSave(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	stage := r.FormValue("stage")
	if !validStages[stage] {
		http.Error(w, "非法阶段", http.StatusBadRequest)
		return
	}
	if err := s.st.UpdateCustomerStage(id, stage); err != nil {
		setFlash(w, "更新失败")
	} else {
		setFlash(w, "阶段已更新为「"+stageLabel(stage)+"」")
	}
	http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
}

// ---------- 交互记录（入库后自动进 AI 队列） ----------

func (s *Server) interactionCreate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	content := strings.TrimSpace(r.FormValue("content"))
	if content == "" {
		setFlash(w, "沟通内容不能为空")
		http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
		return
	}
	var occurred int64
	if raw := r.FormValue("occurred_at"); raw != "" {
		if t, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local); err == nil {
			occurred = t.Unix()
		}
	}
	itID, err := s.st.AddInteraction(&store.Interaction{
		CustomerID: id,
		Channel:    r.FormValue("channel"),
		Direction:  r.FormValue("direction"),
		Content:    content,
		OccurredAt: occurred,
	})
	if err != nil {
		log.Printf("web: 记录交互失败: %v", err)
		setFlash(w, "记录失败，请重试")
		http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
		return
	}
	if s.aiConfigured() {
		payload, _ := json.Marshal(map[string]int64{"interaction_id": itID})
		if _, err := s.st.EnqueueAIJob("interaction_extract", string(payload)); err == nil {
			setFlash(w, "已记录，AI 正在分析并生成跟进建议（稍后刷新查看）")
		} else {
			setFlash(w, "已记录，但 AI 任务入队失败")
		}
	} else {
		setFlash(w, "已记录（AI 模型未配置，跳过分析，可在设置页开启）")
	}
	http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
}

func (s *Server) aiConfigured() bool {
	base, _ := s.st.Setting("ai.base_url")
	model, _ := s.st.Setting("ai.model")
	return base != "" && model != ""
}

// ---------- 任务 ----------

func (s *Server) taskCreate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		setFlash(w, "任务标题不能为空")
		http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
		return
	}
	var due int64
	if raw := r.FormValue("due"); raw != "" {
		if t, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
			due = t.Unix()
		}
	}
	_, err := s.st.CreateTask(&store.Task{
		CustomerID: id,
		Title:      title,
		Detail:     strings.TrimSpace(r.FormValue("detail")),
		DueAt:      due,
		Source:     "manual",
	})
	if err != nil {
		setFlash(w, "创建失败")
	} else {
		setFlash(w, "任务已创建")
	}
	http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
}

func (s *Server) taskDone(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	t, err := s.st.Task(id)
	if err != nil || t == nil {
		http.NotFound(w, r)
		return
	}
	if err := s.st.CompleteTask(id); err != nil {
		setFlash(w, "操作失败")
	} else {
		setFlash(w, "已完成：" + t.Title)
	}
	// 从哪来回哪去：首页或客户页
	back := r.FormValue("back")
	if back == "" || !strings.HasPrefix(back, "/") {
		back = fmt.Sprintf("/customers/%d", t.CustomerID)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ---------- 商机 ----------

func (s *Server) dealCreate(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		setFlash(w, "商机名称不能为空")
		http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
		return
	}
	var cents int64
	if raw := strings.TrimSpace(r.FormValue("amount")); raw != "" {
		if f, err := strconv.ParseFloat(raw, 64); err == nil && f >= 0 {
			cents = int64(math.Round(f * 100))
		}
	}
	var expected int64
	if raw := r.FormValue("expected"); raw != "" {
		if t, err := time.ParseInLocation("2006-01-02", raw, time.Local); err == nil {
			expected = t.Unix()
		}
	}
	_, err := s.st.CreateDeal(&store.Deal{CustomerID: id, Title: title, AmountCents: cents, ExpectedClose: expected})
	if err != nil {
		setFlash(w, "创建失败")
	} else {
		setFlash(w, "商机已创建")
	}
	http.Redirect(w, r, fmt.Sprintf("/customers/%d", id), http.StatusSeeOther)
}

func (s *Server) dealClose(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	stage := r.FormValue("stage") // won / lost
	if stage != "won" && stage != "lost" {
		http.Error(w, "非法状态", http.StatusBadRequest)
		return
	}
	custID, _ := strconv.ParseInt(r.FormValue("customer_id"), 10, 64)
	if err := s.st.CloseDeal(id, stage, strings.TrimSpace(r.FormValue("lost_reason"))); err != nil {
		setFlash(w, "操作失败")
		http.Redirect(w, r, fmt.Sprintf("/customers/%d", custID), http.StatusSeeOther)
		return
	}
	msg := "已标记为丢单"
	if stage == "won" {
		msg = "恭喜成交 🎉"
	}
	// 关单即复盘：AI 回溯 timeline 做归因，沉淀 playbook
	if s.aiConfigured() {
		payload, _ := json.Marshal(map[string]int64{"deal_id": id})
		if _, err := s.st.EnqueueAIJob("deal_review", string(payload)); err == nil {
			msg += "，AI 复盘生成中（稍后在「复盘」页查看）"
		}
	}
	setFlash(w, msg)
	http.Redirect(w, r, fmt.Sprintf("/customers/%d", custID), http.StatusSeeOther)
}
