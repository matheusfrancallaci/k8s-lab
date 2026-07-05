package repository

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type LabSession struct {
	ID        string
	Questions []string
	Index     int
	CreatedAt time.Time
}

type LabSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*LabSession
}

func NewLabSessionStore() *LabSessionStore {
	return &LabSessionStore{sessions: make(map[string]*LabSession)}
}

func (s *LabSessionStore) Create(questions []string) *LabSession {
	b := make([]byte, 10)
	rand.Read(b)
	id := hex.EncodeToString(b)

	sess := &LabSession{ID: id, Questions: questions, Index: 0, CreatedAt: time.Now()}
	s.mu.Lock()
	s.sessions[id] = sess
	s.mu.Unlock()
	return sess
}

func (s *LabSessionStore) Get(id string) (*LabSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

// Advance moves the session to the next question.
// Returns (newIndex 0-based, total, nextQuestionID, done).
func (s *LabSessionStore) Advance(id string) (index, total int, nextID string, done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok {
		return 0, 0, "", true
	}
	sess.Index++
	total = len(sess.Questions)
	if sess.Index >= total {
		return sess.Index, total, "", true
	}
	return sess.Index, total, sess.Questions[sess.Index], false
}
