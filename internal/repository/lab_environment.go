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

// LabEnvironment is the server-side lease for one disposable Kubernetes
// tenant cluster. Expiration is absolute and never moves when the user reloads
// a page or advances to another question.
type LabEnvironment struct {
	ID          string    `json:"id"`
	Owner       string    `json:"owner"`
	Lease       string    `json:"lease"`
	Namespace   string    `json:"namespace"`
	ClusterName string    `json:"cluster_name"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	ReadyAt     time.Time `json:"ready_at,omitempty"`
}

type LabEnvironmentStore struct {
	mu      sync.RWMutex
	byOwner map[string]*LabEnvironment
	path    string
	now     func() time.Time
}

func NewLabEnvironmentStore() *LabEnvironmentStore {
	path := os.Getenv("LAB_ENVIRONMENTS_PATH")
	if path == "" {
		path = filepath.Join("data", "lab-environments.json")
	}
	s := &LabEnvironmentStore{byOwner: make(map[string]*LabEnvironment), path: path, now: time.Now}
	s.load()
	return s
}

func (s *LabEnvironmentStore) load() {
	var items []*LabEnvironment
	if persistence.Enabled() && persistence.List("lab_environment", &items) == nil {
		s.loadItems(items)
		return
	}
	b, err := os.ReadFile(s.path)
	if err == nil && json.Unmarshal(b, &items) == nil {
		s.loadItems(items)
	}
}

func (s *LabEnvironmentStore) loadItems(items []*LabEnvironment) {
	for _, env := range items {
		if env == nil || env.ID == "" || env.Owner == "" || env.Namespace == "" {
			continue
		}
		if current := s.byOwner[env.Owner]; current == nil || env.CreatedAt.After(current.CreatedAt) {
			s.byOwner[env.Owner] = env
		}
	}
}

func (s *LabEnvironmentStore) saveLocked() {
	items := make([]*LabEnvironment, 0, len(s.byOwner))
	for _, env := range s.byOwner {
		items = append(items, env)
		if persistence.Enabled() {
			_ = persistence.Put("lab_environment", env.ID, env)
		}
	}
	if os.MkdirAll(filepath.Dir(s.path), 0o755) != nil {
		return
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

func (s *LabEnvironmentStore) Start(owner, lease, namespace string, ttl time.Duration) (current LabEnvironment, replaced *LabEnvironment) {
	if ttl <= 0 {
		ttl = LabSessionTTL()
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if old := s.byOwner[owner]; old != nil && old.Lease == lease && now.Before(old.ExpiresAt) {
		return *cloneLabEnvironment(old), nil
	}
	if old := s.byOwner[owner]; old != nil {
		replaced = cloneLabEnvironment(old)
		delete(s.byOwner, owner)
		if persistence.Enabled() {
			_ = persistence.Delete("lab_environment", old.ID)
		}
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	env := &LabEnvironment{
		ID: hex.EncodeToString(b), Owner: owner, Lease: lease, Namespace: namespace,
		ClusterName: "student", CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
	s.byOwner[owner] = env
	s.saveLocked()
	return *cloneLabEnvironment(env), replaced
}

func (s *LabEnvironmentStore) Active(owner string) (*LabEnvironment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	env := s.byOwner[owner]
	if env == nil || !s.now().Before(env.ExpiresAt) {
		return nil, false
	}
	return cloneLabEnvironment(env), true
}

// Current returns the lease even after it expires. Cleanup uses it to retain a
// durable retry record until the backing namespace has actually been deleted.
func (s *LabEnvironmentStore) Current(owner, id string) (*LabEnvironment, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	env := s.byOwner[owner]
	if env == nil || (id != "" && env.ID != id) {
		return nil, false
	}
	return cloneLabEnvironment(env), true
}

func (s *LabEnvironmentStore) MarkReady(owner, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	env := s.byOwner[owner]
	if env == nil || env.ID != id || !s.now().Before(env.ExpiresAt) {
		return false
	}
	env.ReadyAt = s.now().UTC()
	s.saveLocked()
	return true
}

func (s *LabEnvironmentStore) End(owner, id string) (*LabEnvironment, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	env := s.byOwner[owner]
	if env == nil || (id != "" && env.ID != id) {
		return nil, false
	}
	delete(s.byOwner, owner)
	if persistence.Enabled() {
		_ = persistence.Delete("lab_environment", env.ID)
	}
	s.saveLocked()
	return cloneLabEnvironment(env), true
}

func (s *LabEnvironmentStore) Expired() []*LabEnvironment {
	now := s.now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	var expired []*LabEnvironment
	for _, env := range s.byOwner {
		if now.Before(env.ExpiresAt) {
			continue
		}
		expired = append(expired, cloneLabEnvironment(env))
	}
	return expired
}

func cloneLabEnvironment(in *LabEnvironment) *LabEnvironment {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
