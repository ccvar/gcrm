// Package web 承载 HTTP 层：页面渲染、会话/CSRF、自动化 API。
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm.ccvar.com/internal/store"
)

type Server struct {
	st       *store.Store
	tmpl     *template.Template
	assets   http.Handler
	baseURL  string
	assetVer string
}

func New(st *store.Store, templatesFS, assetsFS fs.FS, baseURL string) (*Server, error) {
	funcs := template.FuncMap{
		"fmtTime":      fmtTime,
		"fmtDate":      fmtDate,
		"money":        money,
		"stageLabel":   stageLabel,
		"intentLabel":  intentLabel,
		"channelLabel": channelLabel,
		// taskRow 给子模板同时带上行数据和 CSRF（子模板里 $ 不再是页面视图）
		"taskRow": func(csrf string, t store.TaskRow) map[string]any {
			return map[string]any{"T": t, "CSRF": csrf}
		},
	}
	tmpl, err := template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	assetsSub, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return nil, err
	}
	return &Server{
		st:       st,
		tmpl:     tmpl,
		assets:   http.StripPrefix("/assets/", http.FileServer(http.FS(assetsSub))),
		baseURL:  baseURL,
		assetVer: strconv.FormatInt(time.Now().Unix(), 10),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /assets/", s.assets)

	mux.HandleFunc("GET /setup", s.setupForm)
	mux.HandleFunc("POST /setup", s.setupPost)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.logout)

	mux.HandleFunc("GET /{$}", s.auth(s.dashboard))
	mux.HandleFunc("GET /customers", s.auth(s.customersPage))
	mux.HandleFunc("POST /customers", s.auth(s.customerCreate))
	mux.HandleFunc("GET /customers/{id}", s.auth(s.customerDetail))
	mux.HandleFunc("POST /customers/{id}/profile", s.auth(s.customerProfileSave))
	mux.HandleFunc("POST /customers/{id}/stage", s.auth(s.customerStageSave))
	mux.HandleFunc("POST /customers/{id}/interactions", s.auth(s.interactionCreate))
	mux.HandleFunc("POST /customers/{id}/tasks", s.auth(s.taskCreate))
	mux.HandleFunc("POST /customers/{id}/deals", s.auth(s.dealCreate))
	mux.HandleFunc("POST /tasks/{id}/done", s.auth(s.taskDone))
	mux.HandleFunc("POST /deals/{id}/close", s.auth(s.dealClose))

	mux.HandleFunc("GET /reviews", s.auth(s.reviewsPage))
	mux.HandleFunc("POST /reviews/{id}/run", s.auth(s.reviewRun))

	mux.HandleFunc("GET /settings", s.auth(s.settingsPage))
	mux.HandleFunc("POST /settings/ai", s.auth(s.settingsAISave))
	mux.HandleFunc("POST /settings/keys", s.auth(s.keyCreate))
	mux.HandleFunc("POST /settings/keys/{id}/disable", s.auth(s.keyDisable))
	mux.HandleFunc("GET /settings/skillpack.zip", s.auth(s.skillPackDownload))

	// 通道 A：自动化接口（Bearer ccrm_ 密钥）
	mux.HandleFunc("GET /api/v1/ping", s.apiAuth(s.apiPing))
	mux.HandleFunc("GET /api/v1/customers", s.apiAuth(s.apiCustomers))
	mux.HandleFunc("POST /api/v1/customers", s.apiAuth(s.apiCustomerCreate))
	mux.HandleFunc("GET /api/v1/customers/{id}", s.apiAuth(s.apiCustomerDetail))
	mux.HandleFunc("POST /api/v1/customers/{id}/interactions", s.apiAuth(s.apiInteractionCreate))
	mux.HandleFunc("GET /api/v1/tasks", s.apiAuth(s.apiTasks))
	mux.HandleFunc("POST /api/v1/tasks/{id}/done", s.apiAuth(s.apiTaskDone))
	mux.HandleFunc("GET /api/v1/deals", s.apiAuth(s.apiDeals))
	return corsAPI(mux)
}

// corsAPI 给 /api/v1 加 CORS —— Pilot 桌面端（tauri://）与浏览器端工具都要跨源调用。
// 页面路由不受影响（不加任何 CORS 头）。
func corsAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- 视图模型 ----------

type View struct {
	Title    string
	Path     string
	Flash    string
	Err      string
	CSRF     string
	Authed   bool
	AssetVer string

	Stats                  store.Stats
	Overdue, Today, Later  []store.TaskRow
	Customers              []store.Customer
	Query                  string
	Customer               *store.Customer
	Interactions           []store.Interaction
	Tasks                  []store.Task
	Deals                  []store.Deal
	ClosedDeals            []store.DealRow
	AIBaseURL, AIModel     string
	AIKeySet, AIConfigured bool
	Keys                   []store.AutomationKey
	NewKeySecret           string
}

func (s *Server) view(r *http.Request, title string) *View {
	v := &View{Title: title, Path: r.URL.Path, AssetVer: s.assetVer}
	if sess := sessionFrom(r.Context()); sess != nil {
		v.Authed = true
		v.CSRF = sess.CSRF
	}
	v.Flash = takeFlash(nil, r) // 只读；清除在 render 前由 handler 完成
	return v
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, v *View) {
	if c, err := r.Cookie(flashCookie); err == nil && c.Value != "" && v.Flash != "" {
		clearFlash(w)
	}
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, v); err != nil {
		log.Printf("web: 渲染 %s 失败: %v", name, err)
		http.Error(w, "模板渲染失败", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

// ---------- 会话 / CSRF ----------

type ctxKey int

const sessKey ctxKey = 1

const sessionCookie = "crm_session"

func sessionFrom(ctx context.Context) *store.Session {
	sess, _ := ctx.Value(sessKey).(*store.Session)
	return sess
}

func (s *Server) currentSession(r *http.Request) *store.Session {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	sess, err := s.st.SessionByToken(c.Value)
	if err != nil {
		log.Printf("web: 读会话失败: %v", err)
		return nil
	}
	return sess
}

// auth 登录守卫：未初始化跳 /setup，未登录跳 /login，POST 校验 CSRF。
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if n, err := s.st.UserCount(); err == nil && n == 0 {
			http.Redirect(w, r, "/setup", http.StatusSeeOther)
			return
		}
		sess := s.currentSession(r)
		if sess == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method != http.MethodGet && r.FormValue("_csrf") != sess.CSRF {
			http.Error(w, "CSRF 校验失败，请刷新页面重试", http.StatusForbidden)
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessKey, sess)))
	}
}

func randToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err) // 系统熵源不可用属于致命错误
	}
	return hex.EncodeToString(b)
}

// ---------- flash（一次性提示，走 cookie） ----------

const flashCookie = "crm_flash"

func setFlash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: encodeFlash(msg), Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func takeFlash(_ http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil || c.Value == "" {
		return ""
	}
	return decodeFlash(c.Value)
}

func clearFlash(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func encodeFlash(s string) string { return hex.EncodeToString([]byte(s)) }

func decodeFlash(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil {
		return ""
	}
	return string(b)
}

// ---------- 模板函数 ----------

func fmtTime(t int64) string {
	if t == 0 {
		return "—"
	}
	return time.Unix(t, 0).Local().Format("2006-01-02 15:04")
}

func fmtDate(t int64) string {
	if t == 0 {
		return "—"
	}
	return time.Unix(t, 0).Local().Format("2006-01-02")
}

func money(cents int64) string {
	return fmt.Sprintf("¥%.2f", float64(cents)/100)
}

var stageLabels = map[string]string{
	"lead": "新线索", "contacted": "已联系", "negotiating": "洽谈中", "won": "已成交", "lost": "已流失",
	"open": "进行中",
}

func stageLabel(s string) string {
	if l, ok := stageLabels[s]; ok {
		return l
	}
	return s
}

var intentLabels = map[string]string{"unknown": "未知", "low": "低", "medium": "中", "high": "高"}

func intentLabel(s string) string {
	if l, ok := intentLabels[s]; ok {
		return l
	}
	return s
}

var channelLabels = map[string]string{
	"wechat": "微信", "phone": "电话", "email": "邮件", "meeting": "见面", "other": "其他",
}

func channelLabel(s string) string {
	if l, ok := channelLabels[s]; ok {
		return l
	}
	return s
}

// pathID 取路由里的 {id}。
func pathID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}
