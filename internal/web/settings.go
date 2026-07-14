package web

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strings"
)

func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, "")
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, newKeySecret string) {
	v := s.view(r, "设置")
	v.Path = "/settings"
	v.AIBaseURL, _ = s.st.Setting("ai.base_url")
	v.AIModel, _ = s.st.Setting("ai.model")
	key, _ := s.st.Setting("ai.api_key")
	v.AIKeySet = key != ""
	v.AIConfigured = v.AIBaseURL != "" && v.AIModel != ""
	v.Keys, _ = s.st.AutomationKeys()
	v.NewKeySecret = newKeySecret
	s.render(w, r, "settings", v)
}

// ---------- AI 模型（通道 B）配置 ----------

func (s *Server) settingsAISave(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimSpace(r.FormValue("base_url"))
	model := strings.TrimSpace(r.FormValue("model"))
	apiKey := strings.TrimSpace(r.FormValue("api_key"))
	if err := s.st.SetSetting("ai.base_url", base); err != nil {
		log.Printf("web: 存设置失败: %v", err)
		setFlash(w, "保存失败")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	_ = s.st.SetSetting("ai.model", model)
	// API Key 留空表示不修改（避免每次保存都要重填）
	if apiKey != "" {
		_ = s.st.SetSetting("ai.api_key", apiKey)
	}
	if r.FormValue("clear_key") == "1" {
		_ = s.st.SetSetting("ai.api_key", "")
	}
	setFlash(w, "AI 模型配置已保存")
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// ---------- 自动化密钥（通道 A） ----------

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) keyCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = "未命名密钥"
	}
	scopes := "read,write"
	if r.FormValue("scopes") == "read" {
		scopes = "read"
	}
	token := "ccrm_" + randToken(20)
	prefix := token[:13]
	if _, err := s.st.CreateAutomationKey(name, prefix, hashToken(token), scopes); err != nil {
		log.Printf("web: 建密钥失败: %v", err)
		setFlash(w, "创建失败")
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	// 明文只展示这一次，直接渲染（不走重定向，避免明文进 cookie）
	s.renderSettings(w, r, token)
}

func (s *Server) keyDisable(w http.ResponseWriter, r *http.Request) {
	if err := s.st.DisableAutomationKey(pathID(r)); err != nil {
		setFlash(w, "操作失败")
	} else {
		setFlash(w, "密钥已停用")
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
