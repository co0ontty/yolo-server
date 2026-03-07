package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"

	"yolo-server/internal/handler"
)

func main() {
	// 初始化数据存储目录
	dataDir := "data"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("无法创建数据目录: %v", err)
	}

	// 初始化处理程序
	h := handler.NewHandler(filepath.Join(dataDir, "sessions.json"))

	// 启动 WebSocket 服务器
	go h.Run()

	// 设置路由
	http.HandleFunc("/ws", h.WebSocketHandler)
	http.HandleFunc("/ws/cli", h.CLIWebSocketHandler)
	http.HandleFunc("/health", h.HealthCheck)

	// 提供静态文件（前端）
	fs := http.FileServer(http.Dir("dist/web"))
	http.Handle("/", fs)

	// 提供 CLI 下载
	cliFS := http.FileServer(http.Dir("dist/cli"))
	http.Handle("/cli/", http.StripPrefix("/cli", cliFS))

	// 启动 HTTP 服务器
	port := "3100"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	log.Printf("服务器启动在 :%s", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatalf("服务器启动失败: %v", err)
	}
}
