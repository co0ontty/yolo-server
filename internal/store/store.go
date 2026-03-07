package store

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"yolo-server/internal/model"
)

type SessionStore struct {
	filePath string
	mu       sync.RWMutex
	sessions []model.Session
}

func NewSessionStore(filePath string) *SessionStore {
	store := &SessionStore{
		filePath: filePath,
	}
	store.load()
	return store
}

func (s *SessionStore) load() {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.Open(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.sessions = []model.Session{}
			s.save()
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

func (s *SessionStore) save() {
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions, nil
}

func (s *SessionStore) GetSession(id string) (*model.Session, error) {
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
	s.mu.Lock()
	defer s.mu.Unlock()

	session.CreatedAt = time.Now()
	session.UpdatedAt = time.Now()

	s.sessions = append(s.sessions, session)

	s.save()

	return nil
}

func (s *SessionStore) UpdateSession(id string, session model.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.sessions {
		if existing.ID == id {
			s.sessions[i] = session
			s.sessions[i].UpdatedAt = time.Now()
			s.save()
			return nil
		}
	}

	return fmt.Errorf("会话不存在: %s", id)
}

func (s *SessionStore) DeleteSession(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, session := range s.sessions {
		if session.ID == id {
			s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
			s.save()
			return nil
		}
	}

	return fmt.Errorf("会话不存在: %s", id)
}
