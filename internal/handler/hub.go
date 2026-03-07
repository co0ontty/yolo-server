package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
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
				// 发送当前会话列表
				if sessions, err := h.store.GetAllSessions(); err == nil {
					sessionsJSON, _ := json.Marshal(sessions)
					msg := model.Message{
						Type:    "sessions",
						Content: sessionsJSON,
						Time:    time.Now(),
					}
					data, _ := json.Marshal(msg)
					client.send <- data
				}
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

			case "chat":
				if len(h.cliWorkers) == 0 {
					log.Println("没有可用的 CLI 工作器")
					errContent, _ := json.Marshal(map[string]string{
						"type": "text",
						"text": "错误：没有可用的 CLI 工作器，请确保 CLI 已启动并连接。",
					})
					// 解析 chat 内容获取 session_id
					var chatReq model.ChatRequest
					json.Unmarshal(msg.Content, &chatReq)
					errMsg := model.Message{
						Type:    "stream",
						Content: errContent,
						Session: chatReq.SessionID,
						Time:    time.Now(),
					}
					errData, _ := json.Marshal(errMsg)
					h.sendToFrontends(errData)
					// 同时发送 message_complete
					completeMsg := model.Message{
						Type:    "message_complete",
						Session: chatReq.SessionID,
						Time:    time.Now(),
					}
					completeData, _ := json.Marshal(completeMsg)
					h.sendToFrontends(completeData)
					continue
				}

				// 解析 chat 内容，转换为 execute_task 发给 CLI
				var chatReq model.ChatRequest
				if err := json.Unmarshal(msg.Content, &chatReq); err != nil {
					log.Printf("解析聊天请求失败: %v", err)
					continue
				}

				taskContent, _ := json.Marshal(map[string]string{
					"session_id": chatReq.SessionID,
					"command":    chatReq.Message,
				})
				taskMsg := model.Message{
					Type:    "execute_task",
					Content: taskContent,
					Session: chatReq.SessionID,
					Time:    time.Now(),
				}
				taskData, _ := json.Marshal(taskMsg)

				for client := range h.cliWorkers {
					select {
					case client.send <- taskData:
					default:
						close(client.send)
						delete(h.cliWorkers, client)
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
				// 转发给前端
				h.sendToFrontends(message)

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
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		return
	}
	client := &Client{conn: conn, send: make(chan []byte, 256), userID: "web"}
	h.register <- client

	go h.readPump(client)
	go h.writePump(client)
}

func (h *Handler) CLIWebSocketHandler(w http.ResponseWriter, r *http.Request) {
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
