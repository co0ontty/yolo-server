package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"yolo-server/internal/auth"
	"yolo-server/internal/handler"
)

func main() {
	// 初始化数据存储目录
	dataDir := os.Getenv("DATA_DIR")
	if dataDir == "" {
		dataDir = "data"
	}
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("无法创建数据目录：%v", err)
	}

	// 初始化认证管理器
	authManager := auth.GetAuthManager()

	// 定期清理过期会话（每 10 分钟）
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			authManager.CleanExpiredSessions()
		}
	}()

	sessionStorePath := os.Getenv("SESSION_STORE_PATH")
	if sessionStorePath == "" {
		sessionStorePath = filepath.Join(dataDir, "sessions.db")
	}

	// 初始化处理程序
	h := handler.NewHandler(sessionStorePath)

	// 启动 WebSocket 服务器
	go h.Run()

	// 设置路由
	http.HandleFunc("/ws", h.WebSocketHandler)
	http.HandleFunc("/ws/cli", h.CLIWebSocketHandler)
	http.HandleFunc("/health", h.HealthCheck)

	// 认证相关 API
	http.HandleFunc("/api/login", authManager.LoginHandler)
	http.HandleFunc("/api/logout", authManager.LogoutHandler)
	http.HandleFunc("/api/check-session", authManager.CheckSessionHandler)
	http.HandleFunc("/api/get-token", authManager.Middleware(authManager.GetTokenHandler))
	http.HandleFunc("/api/list-dirs", authManager.Middleware(h.ListDirsHandler))

	// CLI Token 管理 API
	http.HandleFunc("/api/cli-tokens", authManager.Middleware(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			authManager.ListCLITokensHandler(w, r)
		case http.MethodPost:
			authManager.CreateCLITokenHandler(w, r)
		case http.MethodDelete:
			authManager.DeleteCLITokenHandler(w, r)
		default:
			http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		}
	}))

	// 提供静态文件（前端）- 需要认证
	http.Handle("/", authManager.Middleware(func(w http.ResponseWriter, r *http.Request) {
		fs := http.FileServer(http.Dir("dist/web"))
		fs.ServeHTTP(w, r)
	}))

	// 提供 CLI 下载和安装脚本 - 不需要认证，方便远程安装
	cliFS := http.StripPrefix("/cli/", http.FileServer(http.Dir("dist/cli")))
	http.Handle("/cli/", cliFS)

	// 启动 HTTP 服务器
	port := "3100"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	log.Printf("服务器启动在 :%s (认证启用：%v)", port, authManager.IsEnabled())
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("服务器启动失败：%v", err)
	}
}
