package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"yolo-server/internal/auth"
	"yolo-server/internal/model"
	"yolo-server/internal/store"
)

type Handler struct {
	// 会话存储
	store *store.SessionStore

	// 客户端连接管理
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client

	// CLI 工作器连接
	cliWorkers map[*Client]bool
	cliRRIndex int // 轮询索引
}

type Client struct {
	conn   *websocket.Conn
	send   chan []byte
	userID string
}

func NewHandler(filePath string) *Handler {
	return &Handler{
		store:      store.NewSessionStore(filePath),
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		cliWorkers: make(map[*Client]bool),
	}
}

// broadcastCLIStatus 向所有前端客户端广播 CLI 工作器连接状态
func (h *Handler) broadcastCLIStatus() {
	status := map[string]interface{}{
		"connected": len(h.cliWorkers) > 0,
		"count":     len(h.cliWorkers),
	}
	statusJSON, _ := json.Marshal(status)
	msg := model.Message{
		Type:    "cli_status",
		Content: statusJSON,
		Time:    time.Now(),
	}
	data, _ := json.Marshal(msg)
	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			close(client.send)
			delete(h.clients, client)
		}
	}
}

// broadcastSessions 向所有前端客户端广播会话列表
func (h *Handler) broadcastSessions() {
	sessions, err := h.store.GetAllSessions()
	if err != nil {
		log.Printf("获取会话列表失败: %v", err)
		return
	}
	sessionsJSON, _ := json.Marshal(sessions)
	msg := model.Message{
		Type:    "sessions",
		Content: sessionsJSON,
		Time:    time.Now(),
	}
	data, _ := json.Marshal(msg)
	for client := range h.clients {
		select {
		case client.send <- data:
		default:
			close(client.send)
			delete(h.clients, client)
		}
	}
}

// sendToFrontends 向所有前端客户端发送消息
func (h *Handler) sendToFrontends(message []byte) {
	for client := range h.clients {
		select {
		case client.send <- message:
		default:
			close(client.send)
			delete(h.clients, client)
		}
	}
}

// pickCLIWorker 轮询选择一个 CLI 工作器
func (h *Handler) pickCLIWorker() *Client {
	if len(h.cliWorkers) == 0 {
		return nil
	}
	workers := make([]*Client, 0, len(h.cliWorkers))
	for c := range h.cliWorkers {
		workers = append(workers, c)
	}
	h.cliRRIndex = h.cliRRIndex % len(workers)
	picked := workers[h.cliRRIndex]
	h.cliRRIndex++
	return picked
}

func (h *Handler) Run() {
	for {
		select {
		case client := <-h.register:
			if client.userID == "cli" {
				h.cliWorkers[client] = true
				log.Printf("CLI 工作器连接成功 (当前 %d 个)", len(h.cliWorkers))
				h.broadcastCLIStatus()
			} else {
				h.clients[client] = true
				log.Println("前端客户端连接成功")
				log.Println("DEBUG: About to get sessions from store")
				// 发送当前会话列表
				sessions, err := h.store.GetAllSessions()
				if err != nil {
					log.Printf("DEBUG: GetAllSessions error: %v", err)
				} else {
					log.Printf("DEBUG: GetAllSessions success, count=%d", len(sessions))
				}
				sessionsJSON, _ := json.Marshal(sessions)
				msg := model.Message{
					Type:    "sessions",
					Content: sessionsJSON,
					Time:    time.Now(),
				}
				data, _ := json.Marshal(msg)
				log.Printf("DEBUG: Sending initial sessions to client.send, len=%d", len(data))
				client.send <- data
				log.Println("DEBUG: Sent sessions to client.send")
				// 发送当前 CLI 状态
				status := map[string]interface{}{
					"connected": len(h.cliWorkers) > 0,
					"count":     len(h.cliWorkers),
				}
				statusJSON, _ := json.Marshal(status)
				statusMsg := model.Message{
					Type:    "cli_status",
					Content: statusJSON,
					Time:    time.Now(),
				}
				statusData, _ := json.Marshal(statusMsg)
				client.send <- statusData
			}
		case client := <-h.unregister:
			if client.userID == "cli" {
				delete(h.cliWorkers, client)
				log.Printf("CLI 工作器断开连接 (剩余 %d 个)", len(h.cliWorkers))
				close(client.send)
				h.broadcastCLIStatus()
			} else {
				delete(h.clients, client)
				log.Println("前端客户端断开连接")
				close(client.send)
			}
		case message := <-h.broadcast:
			var msg model.Message
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Printf("无法解析消息: %v", err)
				continue
			}

			switch msg.Type {
			case "create_session":
				var sessionData struct {
					Directory  string `json:"directory"`
					Permission string `json:"permission"`
				}
				if err := json.Unmarshal(msg.Content, &sessionData); err != nil {
					log.Printf("解析创建会话请求失败: %v", err)
					continue
				}
				session := model.Session{
					ID:         fmt.Sprintf("session-%d", time.Now().UnixNano()),
					Directory:  sessionData.Directory,
					Permission: sessionData.Permission,
				}
				if err := h.store.CreateSession(session); err != nil {
					log.Printf("创建会话失败: %v", err)
					continue
				}
				log.Printf("创建会话成功: %s", session.ID)
				h.broadcastSessions()

			case "delete_session":
				var reqData struct {
					SessionID string `json:"session_id"`
				}
				if err := json.Unmarshal(msg.Content, &reqData); err != nil {
					log.Printf("解析删除会话请求失败: %v", err)
					continue
				}
				if err := h.store.DeleteSession(reqData.SessionID); err != nil {
					log.Printf("删除会话失败: %v", err)
					continue
				}
				log.Printf("删除会话成功: %s", reqData.SessionID)
				h.broadcastSessions()

			case "rename_session":
				var reqData struct {
					SessionID string `json:"session_id"`
					NewName   string `json:"new_name"`
				}
				if err := json.Unmarshal(msg.Content, &reqData); err != nil {
					log.Printf("解析重命名会话请求失败: %v", err)
					continue
				}
				session, err := h.store.GetSession(reqData.SessionID)
				if err != nil {
					log.Printf("获取待重命名会话失败: %v", err)
					continue
				}
				session.Directory = strings.TrimSpace(reqData.NewName)
				if session.Directory == "" {
					log.Printf("重命名会话失败: 新名称为空")
					continue
				}
				if err := h.store.UpdateSession(reqData.SessionID, *session); err != nil {
					log.Printf("重命名会话失败: %v", err)
					continue
				}
				log.Printf("重命名会话成功: %s -> %s", reqData.SessionID, session.Directory)
				h.broadcastSessions()

			case "chat":
				var chatReq model.ChatRequest
				if err := json.Unmarshal(msg.Content, &chatReq); err != nil {
					log.Printf("解析聊天请求失败: %v", err)
					continue
				}

				if len(h.cliWorkers) == 0 {
					log.Println("没有可用的 CLI 工作器")
					_ = h.store.AddMessage(model.SessionMessage{
						SessionID:  chatReq.SessionID,
						Role:       "user",
						Content:    chatReq.Message,
						Time:       time.Now(),
						IsComplete: true,
					})
					_ = h.store.AddMessage(model.SessionMessage{
						SessionID:  chatReq.SessionID,
						Role:       "assistant",
						Content:    "错误：没有可用的 CLI 工作器，请确保 CLI 已启动并连接。",
						Time:       time.Now(),
						IsComplete: true,
					})
					h.broadcastSessions()
					errData, _ := json.Marshal(map[string]interface{}{
						"type":       "stream",
						"session_id": chatReq.SessionID,
						"content": map[string]string{
							"type": "text",
							"text": "错误：没有可用的 CLI 工作器，请确保 CLI 已启动并连接。",
						},
					})
					h.sendToFrontends(errData)
					completeData, _ := json.Marshal(map[string]interface{}{
						"type":       "message_complete",
						"session_id": chatReq.SessionID,
					})
					h.sendToFrontends(completeData)
					continue
				}

				session, err := h.store.GetSession(chatReq.SessionID)
				if err != nil {
					log.Printf("获取会话失败: %v", err)
					errData, _ := json.Marshal(map[string]interface{}{
						"type":       "stream",
						"session_id": chatReq.SessionID,
						"content": map[string]string{
							"type": "text",
							"text": "错误：会话不存在或已被删除。",
						},
					})
					h.sendToFrontends(errData)
					completeData, _ := json.Marshal(map[string]interface{}{
						"type":       "message_complete",
						"session_id": chatReq.SessionID,
					})
					h.sendToFrontends(completeData)
					continue
				}

				_ = h.store.AddMessage(model.SessionMessage{
					SessionID:  chatReq.SessionID,
					Role:       "user",
					Content:    chatReq.Message,
					Time:       time.Now(),
					IsComplete: true,
				})
				_ = h.store.AddMessage(model.SessionMessage{
					SessionID:  chatReq.SessionID,
					Role:       "assistant",
					Content:    "",
					Time:       time.Now(),
					IsComplete: false,
				})
				h.broadcastSessions()

				taskContent, _ := json.Marshal(map[string]string{
					"session_id": chatReq.SessionID,
					"command":    chatReq.Message,
					"directory":  session.Directory,
					"permission": session.Permission,
				})
				taskMsg := model.Message{
					Type:    "execute_task",
					Content: taskContent,
					Session: chatReq.SessionID,
					Time:    time.Now(),
				}
				taskData, _ := json.Marshal(taskMsg)

				// 轮询选择一个 CLI 工作器执行任务
				worker := h.pickCLIWorker()
				if worker != nil {
					select {
					case worker.send <- taskData:
					default:
						close(worker.send)
						delete(h.cliWorkers, worker)
					}
				}

			case "stop":
				// 转发 stop 给 CLI 工作器
				for client := range h.cliWorkers {
					select {
					case client.send <- message:
					default:
						close(client.send)
						delete(h.cliWorkers, client)
					}
				}

			case "stream", "message_complete":
				if msg.Type == "stream" {
					var streamData struct {
						SessionID string `json:"session_id"`
						Content   struct {
							Text string `json:"text"`
						} `json:"content"`
					}
					if err := json.Unmarshal(message, &streamData); err == nil && streamData.SessionID != "" && streamData.Content.Text != "" {
						_ = h.store.AppendToLatestAssistantMessage(streamData.SessionID, streamData.Content.Text)
					}
				} else {
					var completeData struct {
						SessionID string `json:"session_id"`
						Session   string `json:"session"`
					}
					if err := json.Unmarshal(message, &completeData); err == nil {
						sessionID := completeData.SessionID
						if sessionID == "" {
							sessionID = completeData.Session
						}
						if sessionID != "" {
							_ = h.store.CompleteLatestAssistantMessage(sessionID)
							h.broadcastSessions()
						}
					}
				}
				// 转发给前端
				h.sendToFrontends(message)

			case "permission_request":
				// 权限申请，转发给前端并等待响应
				h.sendToFrontends(message)

			case "permission_response":
				// 权限响应，转发给 CLI
				var permResp model.PermissionResponse
				if err := json.Unmarshal(msg.Content, &permResp); err != nil {
					log.Printf("解析权限响应失败：%v", err)
					continue
				}
				// 转发给 CLI 工作器
				for client := range h.cliWorkers {
					select {
					case client.send <- message:
					default:
						close(client.send)
						delete(h.cliWorkers, client)
					}
				}

			case "tool_use_request":
				// 工具调用请求，转发给前端并等待响应
				h.sendToFrontends(message)

			case "tool_use_response":
				// 工具调用响应，转发给 CLI
				for client := range h.cliWorkers {
					select {
					case client.send <- message:
					default:
						close(client.send)
						delete(h.cliWorkers, client)
					}
				}

			case "sessions":
				// 发送会话列表给前端
				h.sendToFrontends(message)
			}
		}
	}
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// 允许所有来源，在生产环境中应该根据实际情况配置
		return true
	},
}

func (h *Handler) WebSocketHandler(w http.ResponseWriter, r *http.Request) {
	// 认证检查
	authManager := auth.GetAuthManager()
	var userID string
	if authManager.IsEnabled() {
		token := r.URL.Query().Get("token")
		if token == "" {
			// 尝试从 Authorization header 获取
			authHeader := r.Header.Get("Authorization")
			if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				token = authHeader[7:]
			}
		}
		if token == "" {
			// 尝试从 cookie 获取
			cookie, err := r.Cookie("auth_token")
			if err == nil {
				token = cookie.Value
			}
		}
		if token != "" {
			// 验证 token，支持 URL 参数中的 token 参数（用于 WebSocket）
			if session, valid := authManager.Validate(token); !valid {
				log.Println("WebSocket 认证失败：无效的 token")
				http.Error(w, "未授权", http.StatusUnauthorized)
				return
			} else {
				userID = session.Username
			}
		} else {
			log.Println("WebSocket 连接未提供认证 token")
			http.Error(w, "未授权", http.StatusUnauthorized)
			return
		}
	} else {
		userID = "web"
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}

	client := &Client{conn: conn, send: make(chan []byte, 256), userID: userID}
	h.register <- client

	go h.readPump(client)
	go h.writePump(client)
}

func (h *Handler) CLIWebSocketHandler(w http.ResponseWriter, r *http.Request) {
	// 认证检查（支持 CLI 专用 token）
	authManager := auth.GetAuthManager()
	if authManager.IsEnabled() {
		token := r.URL.Query().Get("token")
		if token != "" {
			// 尝试用 CLI token 验证
			if cliToken, valid := authManager.ValidateCLIToken(token, r.RemoteAddr); valid {
				log.Printf("CLI 工作器认证成功：%s (最后连接：%s)", cliToken.Name, cliToken.LastAddress)
			} else {
				log.Println("CLI 工作器认证失败：无效的 token")
				http.Error(w, "未授权", http.StatusUnauthorized)
				return
			}
		} else {
			// 如果没有提供 token，记录警告但允许连接（向后兼容）
			log.Println("CLI 工作器连接未提供认证 token（允许连接）")
		}
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	client := &Client{conn: conn, send: make(chan []byte, 256), userID: "cli"}
	h.register <- client

	go h.readPump(client)
	go h.writePump(client)
}

func (h *Handler) readPump(client *Client) {
	defer func() {
		h.unregister <- client
		client.conn.Close()
	}()

	client.conn.SetReadLimit(1024 * 1024) // 1MB
	client.conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(120 * time.Second)); return nil })

	for {
		_, message, err := client.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("错误: %v", err)
			}
			break
		}
		h.broadcast <- message
	}
}

func (h *Handler) writePump(client *Client) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		client.conn.Close()
	}()

	for {
		select {
		case message, ok := <-client.send:
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				client.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := client.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			if err := w.Close(); err != nil {
				return
			}
		case <-ticker.C:
			client.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := client.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (h *Handler) ListDirsHandler(w http.ResponseWriter, r *http.Request) {
	basePath := strings.TrimSpace(r.URL.Query().Get("path"))
	if basePath == "" {
		basePath = "/"
	}

	lookupDir := basePath
	prefix := ""
	if !strings.HasSuffix(basePath, string(os.PathSeparator)) {
		lookupDir = filepath.Dir(basePath)
		if lookupDir == "." {
			lookupDir = "/"
		}
		prefix = filepath.Base(basePath)
	}

	entries, err := os.ReadDir(lookupDir)
	if err != nil {
		status := http.StatusBadRequest
		if os.IsPermission(err) {
			status = http.StatusForbidden
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"dirs":    []map[string]string{},
			"message": err.Error(),
		})
		return
	}

	dirs := make([]map[string]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}
		fullPath := filepath.Join(lookupDir, name)
		dirs = append(dirs, map[string]string{
			"name": name,
			"path": fullPath,
		})
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i]["name"]) < strings.ToLower(dirs[j]["name"])
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"dirs": dirs,
	})
}
