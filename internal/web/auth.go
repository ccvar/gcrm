package web

import (
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionTTL = 14 * 24 * time.Hour

// ---------- 首次初始化 ----------

func (s *Server) setupForm(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.st.UserCount(); n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, "setup", s.view(r, "初始化"))
}

func (s *Server) setupPost(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.st.UserCount(); n > 0 {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	v := s.view(r, "初始化")
	if username == "" || len(password) < 8 {
		v.Err = "用户名不能为空，密码至少 8 位"
		s.render(w, r, "setup", v)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "密码处理失败", http.StatusInternalServerError)
		return
	}
	if err := s.st.CreateUser(username, string(hash)); err != nil {
		log.Printf("web: 创建管理员失败: %v", err)
		v.Err = "创建失败，请重试"
		s.render(w, r, "setup", v)
		return
	}
	s.startSession(w, username)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// ---------- 登录 / 退出 ----------

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if n, _ := s.st.UserCount(); n == 0 {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	if s.currentSession(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", s.view(r, "登录"))
}

func (s *Server) loginPost(w http.ResponseWriter, r *http.Request) {
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	id, hash, ok, err := s.st.UserByName(username)
	if err != nil {
		http.Error(w, "服务器错误", http.StatusInternalServerError)
		return
	}
	if !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		time.Sleep(600 * time.Millisecond) // 拖慢爆破
		v := s.view(r, "登录")
		v.Err = "用户名或密码不正确"
		s.render(w, r, "login", v)
		return
	}
	_ = id
	s.startSession(w, username)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) startSession(w http.ResponseWriter, username string) {
	id, _, ok, err := s.st.UserByName(username)
	if err != nil || !ok {
		return
	}
	token := randToken(32)
	csrf := randToken(16)
	if err := s.st.CreateSession(token, id, csrf, sessionTTL); err != nil {
		log.Printf("web: 建会话失败: %v", err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int(sessionTTL.Seconds()),
		Secure: strings.HasPrefix(s.baseURL, "https://"),
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		// 退出对 CSRF 宽松处理：删除自己的会话无副作用放大风险
		_ = s.st.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
