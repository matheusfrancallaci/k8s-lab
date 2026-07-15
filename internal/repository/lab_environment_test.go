package repository

import (
	"testing"
	"time"
)

func TestLabEnvironmentIsolationReplacementAndExpiry(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("LAB_ENVIRONMENTS_PATH", t.TempDir()+"/environments.json")
	s := NewLabEnvironmentStore()
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	first, old := s.Start("alice", "session:a", "lab-alice-a", time.Hour)
	if old != nil || first.Owner != "alice" || first.Namespace != "lab-alice-a" {
		t.Fatalf("primeiro ambiente invalido: %+v old=%+v", first, old)
	}
	same, old := s.Start("alice", "session:a", "lab-alice-ignored", time.Hour)
	if old != nil || same.ID != first.ID || !same.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("reload renovou ou trocou o ambiente: first=%+v same=%+v", first, same)
	}
	second, old := s.Start("alice", "session:b", "lab-alice-b", time.Hour)
	if old == nil || old.ID != first.ID || second.ID == first.ID {
		t.Fatalf("nova sessao nao substituiu o cluster: second=%+v old=%+v", second, old)
	}

	now = now.Add(time.Hour + time.Second)
	if _, ok := s.Active("alice"); ok {
		t.Fatal("ambiente expirado ainda esta ativo")
	}
	expired := s.Expired()
	if len(expired) != 1 || expired[0].ID != second.ID {
		t.Fatalf("GC nao recebeu ambiente expirado: %+v", expired)
	}
	if _, ok := s.Current("alice", second.ID); !ok {
		t.Fatal("GC apagou o lease antes da confirmacao de limpeza")
	}
	if _, ok := s.End("alice", second.ID); !ok {
		t.Fatal("nao foi possivel confirmar a limpeza do lease")
	}
}
