package repository

import "testing"

import "time"

func TestLabSessionOwnershipAndPersistence(t *testing.T) {
	path := t.TempDir() + "/sessions.json"
	t.Setenv("LAB_SESSIONS_PATH", path)
	s := NewLabSessionStore()
	created := s.Create("alice", []string{"q1", "q2"})
	if created.Owner != "alice" {
		t.Fatalf("owner ausente: %+v", created)
	}
	if _, ok := s.Get("bob", created.ID); ok {
		t.Fatal("sessao vazou para outro usuario")
	}
	if _, _, _, done := s.Advance("bob", created.ID); !done {
		t.Fatal("outro usuario conseguiu avancar sessao")
	}
	reloaded := NewLabSessionStore()
	got, ok := reloaded.Get("alice", created.ID)
	if !ok || len(got.Questions) != 2 {
		t.Fatalf("sessao nao sobreviveu ao restart: %+v", got)
	}
}

func TestLabSessionHasFixedOneHourLease(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("LAB_SESSIONS_PATH", t.TempDir()+"/sessions.json")
	t.Setenv("LAB_SESSION_TTL", "1h")
	s := NewLabSessionStore()
	created := s.Create("alice", []string{"q1", "q2"})
	if got := created.ExpiresAt.Sub(created.CreatedAt); got != time.Hour {
		t.Fatalf("duracao do lab = %s, esperado 1h", got)
	}
	before := created.ExpiresAt
	s.Advance("alice", created.ID)
	got, ok := s.Get("alice", created.ID)
	if !ok || !got.ExpiresAt.Equal(before) {
		t.Fatalf("avancar ou recarregar estendeu a hora: antes=%s depois=%v", before, got)
	}
}
