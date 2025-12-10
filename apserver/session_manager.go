package apserver

import (
	"sync"
)

// SessionManager manages AirPlay RTSP sessions
type SessionManager struct {
	sessions map[string]*SessionData
	mu       sync.RWMutex
	handler  func(sessionID string, fmtp []int, aesiv, aeskey []byte) error
}

// NewSessionManager creates a new session manager
func NewSessionManager(handler func(sessionID string, fmtp []int, aesiv, aeskey []byte) error) *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*SessionData),
		handler:  handler,
	}
}

// CreateSession creates a new session
func (sm *SessionManager) CreateSession(id string) *SessionData {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	session := &SessionData{
		ID: id,
	}
	sm.sessions[id] = session
	return session
}

// GetSession gets a session by ID
func (sm *SessionManager) GetSession(id string) (*SessionData, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	session, ok := sm.sessions[id]
	return session, ok
}

// RemoveSession removes a session
func (sm *SessionManager) RemoveSession(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, id)
}

// HandleANNOUNCE handles ANNOUNCE message and calls the handler
func (sm *SessionManager) HandleANNOUNCE(sessionID string, fmtp []int, aesiv, aeskey []byte) error {
	if sm.handler != nil {
		return sm.handler(sessionID, fmtp, aesiv, aeskey)
	}
	return nil
}

