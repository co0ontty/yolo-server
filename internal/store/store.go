package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"yolo-server/internal/model"
)

type SessionStore struct {
	filePath string
	backend  string
	db       *sql.DB
	mu       sync.RWMutex
	sessions []model.Session
}

func NewSessionStore(filePath string) *SessionStore {
	store := &SessionStore{
		filePath: filePath,
	}
	store.backend = detectBackend(filePath)
	fmt.Printf("DEBUG: SessionStore backend: %s, filePath: %s\n", store.backend, store.filePath)
	if store.backend == "sqlite" {
		if err := store.initSQLite(); err != nil {
			fmt.Printf("初始化 SQLite 会话存储失败: %v\n", err)
			store.backend = "json"
			store.db = nil
			if strings.HasSuffix(strings.ToLower(store.filePath), ".db") || strings.HasSuffix(strings.ToLower(store.filePath), ".sqlite") || strings.HasSuffix(strings.ToLower(store.filePath), ".sqlite3") {
				store.filePath = filepath.Join(filepath.Dir(store.filePath), "sessions.json")
			}
		}
	}
	if store.backend == "sqlite" {
		return store
	}
	store.loadJSON()
	return store
}

func detectBackend(filePath string) string {
	backend := strings.ToLower(os.Getenv("SESSION_STORE_BACKEND"))
	if backend == "sqlite" || backend == "json" {
		return backend
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".db" || ext == ".sqlite" || ext == ".sqlite3" {
		return "sqlite"
	}
	return "json"
}

func (s *SessionStore) initSQLite() error {
	if err := os.MkdirAll(filepath.Dir(s.filePath), 0755); err != nil {
		return err
	}

	db, err := sql.Open("sqlite3", s.filePath)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)

	createTableSQL := `
	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		directory TEXT NOT NULL,
		permission TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL,
		claude_token TEXT DEFAULT ''
	);
	`
	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return err
	}

	createMessagesSQL := `
	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		session_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		time DATETIME NOT NULL,
		is_complete BOOLEAN NOT NULL DEFAULT 0,
		FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
	);
	CREATE INDEX IF NOT EXISTS idx_messages_session_time ON messages(session_id, time);
	`
	if _, err := db.Exec(createMessagesSQL); err != nil {
		db.Close()
		return err
	}

	s.db = db
	return s.migrateLegacyJSON()
}

func (s *SessionStore) migrateLegacyJSON() error {
	legacyPath := filepath.Join(filepath.Dir(s.filePath), "sessions.json")
	if legacyPath == s.filePath {
		return nil
	}
	if _, err := os.Stat(legacyPath); err != nil {
		return nil
	}

	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}

	data, err := os.ReadFile(legacyPath)
	if err != nil {
		return err
	}

	var sessions []model.Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO sessions (id, directory, permission, created_at, updated_at, claude_token)
		VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, session := range sessions {
		if session.CreatedAt.IsZero() {
			session.CreatedAt = time.Now()
		}
		if session.UpdatedAt.IsZero() {
			session.UpdatedAt = session.CreatedAt
		}
		if _, err := stmt.Exec(session.ID, session.Directory, session.Permission, session.CreatedAt, session.UpdatedAt, session.ClaudeToken); err != nil {
			return err
		}
		for _, message := range session.Messages {
			if message.ID == "" {
				message.ID = fmt.Sprintf("msg-%d", time.Now().UnixNano())
			}
			if message.SessionID == "" {
				message.SessionID = session.ID
			}
			if message.Time.IsZero() {
				message.Time = session.CreatedAt
			}
			if _, err := tx.Exec(`
				INSERT OR REPLACE INTO messages (id, session_id, role, content, time, is_complete)
				VALUES (?, ?, ?, ?, ?, ?)
			`, message.ID, message.SessionID, message.Role, message.Content, message.Time, message.IsComplete); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

func (s *SessionStore) loadJSON() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.filePath), 0755); err != nil {
		fmt.Printf("无法创建会话目录: %v", err)
		return
	}

	file, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.sessions = []model.Session{}
			s.saveJSON()
			return
		}
		fmt.Printf("无法打开会话文件: %v", err)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		fmt.Printf("无法读取会话文件: %v", err)
		return
	}

	if err := json.Unmarshal(data, &s.sessions); err != nil {
		fmt.Printf("无法解析会话数据: %v", err)
		s.sessions = []model.Session{}
		return
	}
}

func (s *SessionStore) saveJSON() {
	data, err := json.MarshalIndent(s.sessions, "", "  ")
	if err != nil {
		fmt.Printf("无法序列化会话数据: %v", err)
		return
	}

	if err := os.WriteFile(s.filePath, data, 0644); err != nil {
		fmt.Printf("无法保存会话数据: %v", err)
	}
}

func (s *SessionStore) GetAllSessions() ([]model.Session, error) {
	fmt.Printf("DEBUG: GetAllSessions called, backend=%s\n", s.backend)
	if s.backend == "sqlite" {
		fmt.Println("DEBUG: Querying SQLite database")
		rows, err := s.db.Query(`
			SELECT id, directory, permission, created_at, updated_at, claude_token
			FROM sessions
			ORDER BY updated_at DESC, created_at DESC
		`)
		if err != nil {
			return nil, err
		}
		defer rows.Close()

		sessions := make([]model.Session, 0)
		for rows.Next() {
			var session model.Session
			if err := rows.Scan(&session.ID, &session.Directory, &session.Permission, &session.CreatedAt, &session.UpdatedAt, &session.ClaudeToken); err != nil {
				return nil, err
			}
			messages, err := s.getMessagesBySessionSQLite(session.ID)
			if err != nil {
				return nil, err
			}
			session.Messages = messages
			sessions = append(sessions, session)
		}
		return sessions, rows.Err()
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	sessions := make([]model.Session, len(s.sessions))
	copy(sessions, s.sessions)
	return sessions, nil
}

func (s *SessionStore) GetSession(id string) (*model.Session, error) {
	if s.backend == "sqlite" {
		var session model.Session
		err := s.db.QueryRow(`
			SELECT id, directory, permission, created_at, updated_at, claude_token
			FROM sessions WHERE id = ?
		`, id).Scan(&session.ID, &session.Directory, &session.Permission, &session.CreatedAt, &session.UpdatedAt, &session.ClaudeToken)
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("会话不存在: %s", id)
		}
		if err != nil {
			return nil, err
		}
		messages, err := s.getMessagesBySessionSQLite(session.ID)
		if err != nil {
			return nil, err
		}
		session.Messages = messages
		return &session, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, session := range s.sessions {
		if session.ID == id {
			return &session, nil
		}
	}
	return nil, fmt.Errorf("会话不存在: %s", id)
}

func (s *SessionStore) CreateSession(session model.Session) error {
	if s.backend == "sqlite" {
		now := time.Now()
		session.CreatedAt = now
		session.UpdatedAt = now
		if session.Permission == "" {
			session.Permission = "default"
		}
		_, err := s.db.Exec(`
			INSERT INTO sessions (id, directory, permission, created_at, updated_at, claude_token)
			VALUES (?, ?, ?, ?, ?, ?)
		`, session.ID, session.Directory, session.Permission, session.CreatedAt, session.UpdatedAt, session.ClaudeToken)
		if err != nil {
			return err
		}
		for _, message := range session.Messages {
			if err := s.AddMessage(message); err != nil {
				return err
			}
		}
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session.CreatedAt = time.Now()
	session.UpdatedAt = time.Now()
	if session.Permission == "" {
		session.Permission = "default"
	}

	s.sessions = append(s.sessions, session)

	s.saveJSON()

	return nil
}

func (s *SessionStore) UpdateSession(id string, session model.Session) error {
	if s.backend == "sqlite" {
		session.UpdatedAt = time.Now()
		result, err := s.db.Exec(`
			UPDATE sessions
			SET directory = ?, permission = ?, updated_at = ?, claude_token = ?
			WHERE id = ?
		`, session.Directory, session.Permission, session.UpdatedAt, session.ClaudeToken, id)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("会话不存在: %s", id)
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.sessions {
		if existing.ID == id {
			s.sessions[i] = session
			s.sessions[i].UpdatedAt = time.Now()
			s.saveJSON()
			return nil
		}
	}

	return fmt.Errorf("会话不存在: %s", id)
}

func (s *SessionStore) DeleteSession(id string) error {
	if s.backend == "sqlite" {
		result, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return fmt.Errorf("会话不存在: %s", id)
		}
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i, session := range s.sessions {
		if session.ID == id {
			s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
			s.saveJSON()
			return nil
		}
	}

	return fmt.Errorf("会话不存在: %s", id)
}

func (s *SessionStore) AddMessage(message model.SessionMessage) error {
	if message.ID == "" {
		message.ID = fmt.Sprintf("msg-%d", time.Now().UnixNano())
	}
	if message.Time.IsZero() {
		message.Time = time.Now()
	}

	if s.backend == "sqlite" {
		_, err := s.db.Exec(`
			INSERT INTO messages (id, session_id, role, content, time, is_complete)
			VALUES (?, ?, ?, ?, ?, ?)
		`, message.ID, message.SessionID, message.Role, message.Content, message.Time, message.IsComplete)
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, time.Now(), message.SessionID)
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i, session := range s.sessions {
		if session.ID == message.SessionID {
			s.sessions[i].Messages = append(s.sessions[i].Messages, message)
			s.sessions[i].UpdatedAt = time.Now()
			s.saveJSON()
			return nil
		}
	}
	return fmt.Errorf("会话不存在: %s", message.SessionID)
}

func (s *SessionStore) AppendToLatestAssistantMessage(sessionID, text string) error {
	if s.backend == "sqlite" {
		var id, content string
		err := s.db.QueryRow(`
			SELECT id, content FROM messages
			WHERE session_id = ? AND role = 'assistant' AND is_complete = 0
			ORDER BY time DESC, id DESC LIMIT 1
		`, sessionID).Scan(&id, &content)
		if err == sql.ErrNoRows {
			return s.AddMessage(model.SessionMessage{
				SessionID:  sessionID,
				Role:       "assistant",
				Content:    text,
				Time:       time.Now(),
				IsComplete: false,
			})
		}
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`UPDATE messages SET content = ? WHERE id = ?`, content+text, id)
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, time.Now(), sessionID)
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i, session := range s.sessions {
		if session.ID != sessionID {
			continue
		}
		for j := len(session.Messages) - 1; j >= 0; j-- {
			if session.Messages[j].Role == "assistant" && !session.Messages[j].IsComplete {
				s.sessions[i].Messages[j].Content += text
				s.sessions[i].UpdatedAt = time.Now()
				s.saveJSON()
				return nil
			}
		}
		message := model.SessionMessage{
			ID:         fmt.Sprintf("msg-%d", time.Now().UnixNano()),
			SessionID:  sessionID,
			Role:       "assistant",
			Content:    text,
			Time:       time.Now(),
			IsComplete: false,
		}
		s.sessions[i].Messages = append(s.sessions[i].Messages, message)
		s.sessions[i].UpdatedAt = time.Now()
		s.saveJSON()
		return nil
	}
	return fmt.Errorf("会话不存在: %s", sessionID)
}

func (s *SessionStore) CompleteLatestAssistantMessage(sessionID string) error {
	if s.backend == "sqlite" {
		result, err := s.db.Exec(`
			UPDATE messages SET is_complete = 1
			WHERE id = (
				SELECT id FROM messages
				WHERE session_id = ? AND role = 'assistant' AND is_complete = 0
				ORDER BY time DESC, id DESC LIMIT 1
			)
		`, sessionID)
		if err != nil {
			return err
		}
		_, err = result.RowsAffected()
		if err != nil {
			return err
		}
		_, err = s.db.Exec(`UPDATE sessions SET updated_at = ? WHERE id = ?`, time.Now(), sessionID)
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i, session := range s.sessions {
		if session.ID != sessionID {
			continue
		}
		for j := len(session.Messages) - 1; j >= 0; j-- {
			if session.Messages[j].Role == "assistant" && !session.Messages[j].IsComplete {
				s.sessions[i].Messages[j].IsComplete = true
				s.sessions[i].UpdatedAt = time.Now()
				s.saveJSON()
				return nil
			}
		}
		return nil
	}
	return fmt.Errorf("会话不存在: %s", sessionID)
}

func (s *SessionStore) getMessagesBySessionSQLite(sessionID string) ([]model.SessionMessage, error) {
	rows, err := s.db.Query(`
		SELECT id, session_id, role, content, time, is_complete
		FROM messages WHERE session_id = ?
		ORDER BY time ASC, id ASC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	messages := make([]model.SessionMessage, 0)
	for rows.Next() {
		var message model.SessionMessage
		if err := rows.Scan(&message.ID, &message.SessionID, &message.Role, &message.Content, &message.Time, &message.IsComplete); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}
