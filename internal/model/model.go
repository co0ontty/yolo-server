package model

import (
	"encoding/json"
	"time"
)

// Message 表示前端与后端之间传输的消息
type Message struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
	Time    time.Time       `json:"time"`
	Session string          `json:"session,omitempty"`
}

// Session 表示一个工作会话
type Session struct {
	ID          string    `json:"id"`
	Directory   string    `json:"directory"`
	Permission  string    `json:"permission"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ClaudeToken string    `json:"claude_token,omitempty"`
}

// ChatRequest 表示前端发送的聊天请求
type ChatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// StreamResponse 表示 CLI 发送的流式响应
type StreamResponse struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
}

// MessageComplete 表示消息完成
type MessageComplete struct {
	SessionID string `json:"session_id"`
}
