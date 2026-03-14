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
	ID          string           `json:"id"`
	Directory   string           `json:"directory"`
	Permission  string           `json:"permission"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	ClaudeToken string           `json:"claude_token,omitempty"`
	Messages    []SessionMessage `json:"messages,omitempty"`
}

type SessionMessage struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	Time       time.Time `json:"time"`
	IsComplete bool      `json:"is_complete"`
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

// PermissionRequest 表示权限申请请求
type PermissionRequest struct {
	SessionID   string `json:"session_id"`
	RequestID   string `json:"request_id"`
	Type        string `json:"type"` // file_edit, command_run, bash, etc.
	Description string `json:"description"`
	Details     string `json:"details,omitempty"`
}

// PermissionResponse 表示权限申请响应
type PermissionResponse struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Granted   bool   `json:"granted"`
}

// ToolUseRequest 表示工具调用请求
type ToolUseRequest struct {
	SessionID   string `json:"session_id"`
	RequestID   string `json:"request_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Parameters  string `json:"parameters,omitempty"`
}

// ToolUseResponse 表示工具调用响应
type ToolUseResponse struct {
	SessionID string `json:"session_id"`
	RequestID string `json:"request_id"`
	Approved  bool   `json:"approved"`
}
