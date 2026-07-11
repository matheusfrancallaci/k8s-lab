package tutor

import "testing"

func TestRemoteLLMIsOptIn(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	if _, ok := remoteLLM(); ok {
		t.Fatal("provedor remoto nao deve ativar sem chave")
	}
}

func TestRemoteModelRoles(t *testing.T) {
	t.Setenv("LLM_API_KEY", "test")
	t.Setenv("LLM_MODEL", "base")
	t.Setenv("LLM_GEN_MODEL", "strong")
	if got := remoteModelFor("gen", ""); got != "strong" {
		t.Fatalf("modelo gen = %q", got)
	}
	if got := remoteModelFor("chat", ""); got != "base" {
		t.Fatalf("modelo chat = %q", got)
	}
}
