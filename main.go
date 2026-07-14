// CCVAR CRM —— AI 驱动的转化型 CRM，Go + SQLite 单二进制。
// 找客户重要，转化更重要：系统消化沟通记录，输出下一步行动。
package main

import (
	"embed"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"crm.ccvar.com/internal/ai"
	"crm.ccvar.com/internal/store"
	"crm.ccvar.com/internal/web"
)

//go:embed templates
var templatesFS embed.FS

//go:embed assets
var assetsFS embed.FS

func main() {
	dbPath := env("CRM_DB", "data/crm.db")
	if dir := filepath.Dir(dbPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("打开数据库失败: %v", err)
	}
	defer st.Close()

	baseURL := env("BASE_URL", "http://localhost:8090")
	srv, err := web.New(st, templatesFS, assetsFS, baseURL)
	if err != nil {
		log.Fatalf("初始化 web 失败: %v", err)
	}

	// AI 队列消费者（通道 B）：交互提取、草稿生成
	worker := &ai.Worker{St: st}
	go worker.Run(4 * time.Second)

	// 过期会话清理
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for range t.C {
			_ = st.PurgeExpiredSessions()
		}
	}()

	addr := env("CRM_ADDR", ":8090")
	log.Printf("CCVAR CRM 启动: http://localhost%s （数据库 %s）", addr, dbPath)
	if err := http.ListenAndServe(addr, srv.Handler()); err != nil {
		log.Fatal(err)
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
