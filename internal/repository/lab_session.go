package repository

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"estudo-app/internal/persistence"
)

const labSessionTTL = 24 * time.Hour

type LabSession struct {
	ID        string    `json:"id"`
	Owner     string    `json:"owner"`
	Questions []string  `json:"questions"`
	Index     int       `json:"index"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type LabSessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*LabSession
	path     string
}

func NewLabSessionStore() *LabSessionStore {
	path := os.Getenv("LAB_SESSIONS_PATH")
	if path == "" {
		path = filepath.Join("data", "lab-sessions.json")
	}
	s := &LabSessionStore{sessions: make(map[string]*LabSession), path: path}
	s.load()
	return s
}

func (s *LabSessionStore) load() {
	if persistence.Enabled() {
		var items []*LabSession
		if persistence.List("lab_session", &items) == nil && len(items) > 0 {
			s.loadItems(items)
			return
		}
	}
	b, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var items []*LabSession
	if json.Unmarshal(b, &items) != nil {
		return
	}
	s.loadItems(items)
}

func (s *LabSessionStore) loadItems(items []*LabSession) {
	now := time.Now()
	for _, sess := range items {
		if sess != nil && sess.ID != "" && sess.Owner != "" && now.Sub(sess.UpdatedAt) <= labSessionTTL {
			s.sessions[sess.ID] = sess
		}
	}
}

func (s *LabSessionStore) saveLocked() {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	items := make([]*LabSession, 0, len(s.sessions))
	for _, sess := range s.sessions {
		items = append(items, sess)
		if persistence.Enabled() {
			_ = persistence.Put("lab_session", sess.ID, sess)
		}
	}
	b, err := json.Marshal(items)
	if err != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.path)
	}
}

func (s *LabSessionStore) Create(owner string, questions []string) *LabSession {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	now := time.Now().UTC()
	sess := &LabSession{ID: hex.EncodeToString(b), Owner: owner, Questions: append([]string(nil), questions...), CreatedAt: now, UpdatedAt: now}
	s.mu.Lock()
	s.pruneLocked(now)
	s.sessions[sess.ID] = sess
	s.saveLocked()
	s.mu.Unlock()
	return cloneLabSession(sess)
}

func (s *LabSessionStore) Get(owner, id string) (*LabSession, bool) {
	s.mu.RLock()
	sess, ok := s.sessions[id]
	if !ok || sess.Owner != owner || time.Since(sess.UpdatedAt) > labSessionTTL {
		s.mu.RUnlock()
		return nil, false
	}
	out := cloneLabSession(sess)
	s.mu.RUnlock()
	return out, true
}

func (s *LabSessionStore) LatestActive(owner string) (*LabSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var latest *LabSession
	for _, sess := range s.sessions {
		if sess.Owner != owner || sess.Index >= len(sess.Questions) || time.Since(sess.UpdatedAt) > labSessionTTL {
			continue
		}
		if latest == nil || sess.UpdatedAt.After(latest.UpdatedAt) {
			latest = sess
		}
	}
	if latest == nil {
		return nil, false
	}
	return cloneLabSession(latest), true
}

func (s *LabSessionStore) Advance(owner, id string) (index, total int, nextID string, done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[id]
	if !ok || sess.Owner != owner || time.Since(sess.UpdatedAt) > labSessionTTL {
		return 0, 0, "", true
	}
	sess.Index++
	sess.UpdatedAt = time.Now().UTC()
	total = len(sess.Questions)
	s.saveLocked()
	if sess.Index >= total {
		return sess.Index, total, "", true
	}
	return sess.Index, total, sess.Questions[sess.Index], false
}

func (s *LabSessionStore) pruneLocked(now time.Time) {
	for id, sess := range s.sessions {
		if now.Sub(sess.UpdatedAt) > labSessionTTL {
			delete(s.sessions, id)
		}
	}
}

func cloneLabSession(in *LabSession) *LabSession {
	out := *in
	out.Questions = append([]string(nil), in.Questions...)
	return &out
}
