package handler

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
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

func (h *Handler) Run() {
	for {
		select {
		case client := <-h.register:
			if client.userID == "cli" {
				h.cliWorkers[client] = true
				log.Println("CLI 工作器连接成功")
			} else {
				h.clients[client] = true
				log.Println("前端客户端连接成功")
				// 发送当前会话列表
				if sessions, err := h.store.GetAllSessions(); err == nil {
					msg := model.Message{
						Type:     "sessions",
						Content:  sessions,
						Time:     time.Now(),
					}
					data, _ := json.Marshal(msg)
					client.send <- data
				}
			}
		case client := <-h.unregister:
			if client.userID == "cli" {
				delete(h.cliWorkers, client)
				log.Println("CLI 工作器断开连接")
			} else {
				delete(h.clients, client)
				log.Println("前端客户端断开连接")
			}
			close(client.send)
		case message := <-h.broadcast:
			var msg model.Message
			if err := json.Unmarshal(message, &msg); err != nil {
				log.Printf("无法解析消息: %v", err)
				continue
			}

			switch msg.Type {
			case "chat":
				if len(h.cliWorkers) == 0 {
					log.Println("没有可用的 CLI 工作器")
					// 发送错误消息给前端
					errMsg := model.Message{
						Type:    "stream",
						Content: []byte(`{"error":"没有可用的 CLI 工作器"}`),
						Time:    time.Now(),
					}
					errData, _ := json.Marshal(errMsg)
					for client := range h.clients {
						select {
						case client.send <- errData:
						default:
							close(client.send)
							delete(h.clients, client)
						}
					}
					continue
				}

				// 转发消息给 CLI 工作器
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
				for client := range h.clients {
					select {
					case client.send <- message:
					default:
						close(client.send)
						delete(h.clients, client)
					}
				}
			case "sessions":
				// 发送会话列表给前端
				for client := range h.clients {
					select {
					case client.send <- message:
					default:
						close(client.send)
						delete(h.clients, client)
					}
				}
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

	client.conn.SetReadLimit(512)
	client.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	client.conn.SetPongHandler(func(string) error { client.conn.SetReadDeadline(time.Now().Add(60 * time.Second)); return nil })

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
	ticker := time.NewTicker(60 * time.Second)
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
