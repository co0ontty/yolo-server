package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Session 存储 Web 认证会话信息（24 小时过期）
type Session struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CLIToken 存储 CLI 专用 token（长期有效）
type CLIToken struct {
	Token       string    `json:"token"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	IsActive    bool      `json:"is_active"`
	LastAddress string    `json:"last_address"`
}

// AuthManager 管理认证会话和 CLI token
type AuthManager struct {
	mu       sync.RWMutex
	db       *sql.DB
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

		dataDir := os.Getenv("DATA_DIR")
		if dataDir == "" {
			dataDir = "data"
		}

		dbPath := filepath.Join(dataDir, "auth.db")
		if envPath := os.Getenv("AUTH_DB_PATH"); envPath != "" {
			dbPath = envPath
		}

		if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
			log.Fatalf("无法创建认证数据库目录：%v", err)
		}

		db, err := sql.Open("sqlite3", dbPath)
		if err != nil {
			log.Fatalf("无法打开数据库：%v", err)
		}
		db.SetMaxOpenConns(1)

    createTableSQL := `
        CREATE TABLE IF NOT EXISTS sessions (
            token TEXT PRIMARY KEY,
            username TEXT NOT NULL,
            created_at DATETIME NOT NULL,
            expires_at DATETIME NOT NULL
        );
        CREATE TABLE IF NOT EXISTS cli_tokens (
            token TEXT PRIMARY KEY,
            name TEXT NOT NULL,
            created_at DATETIME NOT NULL,
            last_used_at DATETIME NOT NULL,
            is_active BOOLEAN NOT NULL DEFAULT false,
            last_address TEXT
        );
        `
		_, err = db.Exec(createTableSQL)
		if err != nil {
			log.Fatalf("无法创建表：%v", err)
		}

		db.Exec("DELETE FROM sessions WHERE expires_at < ?", time.Now())

		// 初始化一个默认的 CLI token（如果不存在）
		var count int
		err = db.QueryRow("SELECT COUNT(*) FROM cli_tokens").Scan(&count)
		if err == nil && count == 0 {
			defaultToken := generateCLIToken()
			_, _ = db.Exec(
				"INSERT INTO cli_tokens (token, name, created_at, last_used_at, is_active) VALUES (?, ?, ?, ?, ?)",
				defaultToken, "Default CLI", time.Now(), time.Now(), false,
			)
			log.Printf("生成默认 CLI Token: %s (请妥善保管)", defaultToken)
		}

		manager = &AuthManager{
			db:       db,
			username: username,
			password: password,
			enabled:  enabled,
		}
		log.Printf("认证管理器初始化完成 (启用：%v, 用户：%s, 数据库：%s)", enabled, username, dbPath)
	})
	return manager
}

// generateCLIToken 生成 CLI 专用 token（32 位随机字符串）
func generateCLIToken() string {
	randomBytes := make([]byte, 24)
	rand.Read(randomBytes)
	return "cli_" + base64.URLEncoding.EncodeToString(randomBytes)
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

	randomBytes := make([]byte, 32)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}

	hash := sha256.Sum256(randomBytes)
	token := base64.URLEncoding.EncodeToString(hash[:])

	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	_, err := a.db.Exec(
		"INSERT INTO sessions (token, username, created_at, expires_at) VALUES (?, ?, ?, ?)",
		token, username, now, expiresAt,
	)
	if err != nil {
		log.Printf("存储 session 失败：%v", err)
		return "", err
	}

	log.Printf("用户 %s 登录成功，token=%s", username, token[:min(20, len(token))])
	return token, nil
}

// Validate 验证 token 是否有效
func (a *AuthManager) Validate(token string) (*Session, bool) {
	if !a.enabled {
		return &Session{Username: "anonymous"}, true
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	var session Session
	err := a.db.QueryRow(
		"SELECT token, username, created_at, expires_at FROM sessions WHERE token = ?",
		token,
	).Scan(&session.Token, &session.Username, &session.CreatedAt, &session.ExpiresAt)

	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		log.Printf("查询 session 失败：%v", err)
		return nil, false
	}

	if time.Now().After(session.ExpiresAt) {
		a.db.Exec("DELETE FROM sessions WHERE token = ?", token)
		return nil, false
	}

	return &session, true
}

// ValidateCLIToken 验证 CLI token 是否有效
func (a *AuthManager) ValidateCLIToken(token, remoteAddr string) (*CLIToken, bool) {
	if !a.enabled {
		return &CLIToken{Name: "anonymous"}, true
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	var cliToken CLIToken
    err := a.db.QueryRow(
        "SELECT token, name, created_at, last_used_at, is_active, IFNULL(last_address, '') FROM cli_tokens WHERE token = ?",
        token,
    ).Scan(&cliToken.Token, &cliToken.Name, &cliToken.CreatedAt, &cliToken.LastUsedAt, &cliToken.IsActive, &cliToken.LastAddress)

	if err == sql.ErrNoRows {
		return nil, false
	}
	if err != nil {
		log.Printf("查询 CLI token 失败：%v", err)
		return nil, false
	}

	// 更新最后使用时间和地址
	a.db.Exec("UPDATE cli_tokens SET last_used_at = ?, last_address = ? WHERE token = ?", time.Now(), remoteAddr, token)
	cliToken.LastUsedAt = time.Now()
	cliToken.LastAddress = remoteAddr

	return &cliToken, true
}

// CreateCLIToken 创建新的 CLI token
func (a *AuthManager) CreateCLIToken(name string) (string, error) {
	if !a.enabled {
		return "", errors.New("authentication not enabled")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	token := generateCLIToken()
	now := time.Now()

	_, err := a.db.Exec(
		"INSERT INTO cli_tokens (token, name, created_at, last_used_at, is_active) VALUES (?, ?, ?, ?, ?)",
		token, name, now, now, false,
	)
	if err != nil {
		log.Printf("存储 CLI token 失败：%v", err)
		return "", err
	}

	log.Printf("创建新的 CLI Token: %s (名称：%s)", token, name)
	return token, nil
}

// ListCLITokens 获取所有 CLI token 列表
func (a *AuthManager) ListCLITokens() ([]CLIToken, error) {
	if !a.enabled {
		return []CLIToken{}, nil
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

    rows, err := a.db.Query("SELECT token, name, created_at, last_used_at, is_active, IFNULL(last_address, '') FROM cli_tokens ORDER BY created_at DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tokens []CLIToken
	for rows.Next() {
		var t CLIToken
		err := rows.Scan(&t.Token, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.IsActive, &t.LastAddress)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, t)
	}
	return tokens, nil
}

// DeleteCLIToken 删除指定的 CLI token
func (a *AuthManager) DeleteCLIToken(token string) error {
	if !a.enabled {
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	_, err := a.db.Exec("DELETE FROM cli_tokens WHERE token = ?", token)
	if err != nil {
		log.Printf("删除 CLI token 失败：%v", err)
		return err
	}

	log.Printf("删除 CLI Token: %s", token)
	return nil
}

// Logout 删除会话
func (a *AuthManager) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.db.Exec("DELETE FROM sessions WHERE token = ?", token)
	log.Printf("用户已登出")
}

// CleanExpiredSessions 定期清理过期的会话
func (a *AuthManager) CleanExpiredSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.db.Exec("DELETE FROM sessions WHERE expires_at < ?", time.Now())
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
func (a *AuthManager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled {
			next.ServeHTTP(w, r)
			return
		}

		token := extractBearerToken(r)
		if token == "" {
			cookie, err := r.Cookie("auth_token")
			if err == nil {
				token = cookie.Value
			}
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

		next.ServeHTTP(w, r)
	})
}

// extractBearerToken 从请求中提取 Bearer token
func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
		return authHeader[7:]
	}
	return ""
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

	token := extractBearerToken(r)
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
	token := extractBearerToken(r)

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

// GetTokenResponse 获取 token 响应
type GetTokenResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token,omitempty"`
	Message string `json:"message,omitempty"`
}

// GetTokenHandler 获取当前用户的 token
func (a *AuthManager) GetTokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	token := extractBearerToken(r)

	if !a.enabled {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(GetTokenResponse{
			Success: true,
			Token:   "",
			Message: "认证未启用，无需 token",
		})
		return
	}

	if token == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(GetTokenResponse{
			Success: false,
			Message: "未授权",
		})
		return
	}

	_, valid := a.Validate(token)
	if !valid {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(GetTokenResponse{
			Success: false,
			Message: "token 无效或已过期",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(GetTokenResponse{
		Success: true,
		Token:   token,
		Message: "获取成功",
	})
}

// ListCLITokensHandler 获取所有 CLI token 列表（需要认证）
func (a *AuthManager) ListCLITokensHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	if !a.enabled {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "认证未启用",
		})
		return
	}

	tokens, err := a.ListCLITokens()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"tokens":  tokens,
	})
}

// CreateCLITokenHandler 创建新的 CLI token（需要认证）
func (a *AuthManager) CreateCLITokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	if !a.enabled {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "认证未启用",
		})
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "无效的请求",
		})
		return
	}

	if req.Name == "" {
		req.Name = fmt.Sprintf("CLI-%d", time.Now().Unix())
	}

	token, err := a.CreateCLIToken(req.Name)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"token":   token,
		"name":    req.Name,
	})
}

// DeleteCLITokenHandler 删除指定的 CLI token（需要认证）
func (a *AuthManager) DeleteCLITokenHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		http.Error(w, "方法不允许", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "无效的请求",
		})
		return
	}

	if err := a.DeleteCLIToken(req.Token); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
	})
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
