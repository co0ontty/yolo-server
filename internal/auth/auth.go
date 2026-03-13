package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

// Session 存储认证会话信息
type Session struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// AuthManager 管理认证会话
type AuthManager struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	username string
	password string
	enabled  bool
}

var manager *AuthManager
var once sync.Once

// GetAuthManager 获取单例认证管理器
func GetAuthManager() *AuthManager {
	once.Do(func() {
		username := os.Getenv("WEB_AUTH_USER")
		if username == "" {
			username = "admin"
		}
		password := os.Getenv("WEB_AUTH_PASSWORD")
		enabled := os.Getenv("WEB_AUTH_ENABLED") == "true" && password != ""

		manager = &AuthManager{
			sessions: make(map[string]*Session),
			username: username,
			password: password,
			enabled:  enabled,
		}
		log.Printf("认证管理器初始化完成 (启用：%v, 用户：%s)", enabled, username)
	})
	return manager
}

// IsEnabled 返回是否启用了认证
func (a *AuthManager) IsEnabled() bool {
	return a.enabled
}

// Login 验证用户名密码并创建会话
func (a *AuthManager) Login(username, password string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if username != a.username || password != a.password {
		return "", errors.New("unauthorized")
	}

	// 生成随机 token
	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}

	hash := sha256.Sum256(randomBytes)
	token := base64.URLEncoding.EncodeToString(hash[:])

	now := time.Now()
	session := &Session{
		Token:     token,
		Username:  username,
		CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour), // 24 小时过期
	}

	a.sessions[token] = session
	log.Printf("用户 %s 登录成功", username)

	return token, nil
}

// Validate 验证 token 是否有效
func (a *AuthManager) Validate(token string) (*Session, bool) {
	if !a.enabled {
		return &Session{Username: "anonymous"}, true
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	session, exists := a.sessions[token]
	if !exists {
		return nil, false
	}

	if time.Now().After(session.ExpiresAt) {
		// token 已过期，删除会话
		a.mu.RUnlock()
		a.mu.Lock()
		delete(a.sessions, token)
		a.mu.Unlock()
		a.mu.RLock()
		return nil, false
	}

	return session, true
}

// Logout 删除会话
func (a *AuthManager) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, token)
	log.Printf("用户已登出")
}

// CleanExpiredSessions 定期清理过期的会话
func (a *AuthManager) CleanExpiredSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	for token, session := range a.sessions {
		if now.After(session.ExpiresAt) {
			delete(a.sessions, token)
		}
	}
}

// LoginRequest 登录请求
type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token,omitempty"`
	Message string `json:"message,omitempty"`
}

// LogoutResponse 登出响应
type LogoutResponse struct {
	Success bool `json:"success"`
}

// CheckSessionResponse 检查会话状态响应
type CheckSessionResponse struct {
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
}

// Middleware 返回认证中间件
func (a *AuthManager) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled {
			next(w, r)
			return
		}

		// 从 Authorization header 获取 token
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			// 尝试从 cookie 获取 token
			cookie, err := r.Cookie("auth_token")
			if err != nil {
				http.Error(w, "未授权", http.StatusUnauthorized)
				return
			}
			authHeader = "Bearer " + cookie.Value
		}

		var token string
		if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
			token = authHeader[7:]
		}

		if token == "" {
			http.Error(w, "未授权", http.StatusUnauthorized)
			return
		}

		_, valid := a.Validate(token)
		if !valid {
			http.Error(w, "未授权", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}

// LoginHandler 处理登录请求
func (a *AuthManager) LoginHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(LoginResponse{
			Success: false,
			Message: "无效的请求",
		})
		return
	}

	if !a.enabled {
		// 未启用认证，直接返回成功
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(LoginResponse{
			Success: true,
			Message: "认证未启用",
		})
		return
	}

	token, err := a.Login(req.Username, req.Password)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(LoginResponse{
			Success: false,
			Message: "用户名或密码错误",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LoginResponse{
		Success: true,
		Token:   token,
	})
}

// LogoutHandler 处理登出请求
func (a *AuthManager) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	authHeader := r.Header.Get("Authorization")
	var token string
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		token = authHeader[7:]
	}

	if token != "" {
		a.Logout(token)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LogoutResponse{
		Success: true,
	})
}

// CheckSessionHandler 检查会话状态
func (a *AuthManager) CheckSessionHandler(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	var token string
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		token = authHeader[7:]
	}

	response := CheckSessionResponse{
		Authenticated: false,
	}

	if token != "" {
		if session, valid := a.Validate(token); valid {
			response.Authenticated = true
			response.Username = session.Username
		}
	}

	if !a.enabled {
		response.Authenticated = true
		response.Username = "anonymous"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
