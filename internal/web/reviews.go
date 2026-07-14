package web

import (
	"encoding/json"
	"log"
	"net/http"
)

// 复盘页：已关单商机 + AI 归因。转化 playbook 从这里长出来。
func (s *Server) reviewsPage(w http.ResponseWriter, r *http.Request) {
	v := s.view(r, "复盘")
	var err error
	v.ClosedDeals, err = s.st.ClosedDealRows(100)
	if err != nil {
		log.Printf("web: 读复盘失败: %v", err)
	}
	v.AIConfigured = s.aiConfigured()
	s.render(w, r, "reviews", v)
}

// 手动（重新）触发某单复盘 —— 关单时 AI 未配置、或想用新模型重跑时用。
func (s *Server) reviewRun(w http.ResponseWriter, r *http.Request) {
	id := pathID(r)
	deal, err := s.st.Deal(id)
	if err != nil || deal == nil {
		http.NotFound(w, r)
		return
	}
	if deal.Stage != "won" && deal.Stage != "lost" {
		setFlash(w, "商机尚未关单，无法复盘")
		http.Redirect(w, r, "/reviews", http.StatusSeeOther)
		return
	}
	if !s.aiConfigured() {
		setFlash(w, "AI 模型未配置，请先到设置页开启")
		http.Redirect(w, r, "/reviews", http.StatusSeeOther)
		return
	}
	payload, _ := json.Marshal(map[string]int64{"deal_id": id})
	if _, err := s.st.EnqueueAIJob("deal_review", string(payload)); err != nil {
		setFlash(w, "任务入队失败")
	} else {
		setFlash(w, "复盘任务已入队，稍后刷新查看")
	}
	http.Redirect(w, r, "/reviews", http.StatusSeeOther)
}
