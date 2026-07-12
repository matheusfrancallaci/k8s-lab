package tutor

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestConversationLifecycleAndUserIsolation(t *testing.T) {
	t.Setenv("TUTOR_CONVERSATIONS_PATH", filepath.Join(t.TempDir(), "conversations.json"))
	c, err := CreateConversation("alice", "CKA", "deep")
	if err != nil || c.Mode != "deep" {
		t.Fatalf("criacao inesperada: %+v %v", c, err)
	}
	if _, err = AppendConversationMessage("alice", c.ID, "user", "Como funciona HPA?", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = AppendConversationMessage("alice", c.ID, "assistant", "HPA ajusta replicas [S1].", []string{"https://kubernetes.io/docs/"}); err != nil {
		t.Fatal(err)
	}
	got, ok := GetConversation("alice", c.ID)
	if !ok || len(got.Messages) != 2 || got.Title == "Nova conversa" {
		t.Fatalf("conversa nao persistida: %+v", got)
	}
	if _, ok := GetConversation("bob", c.ID); ok {
		t.Fatal("conversa vazou entre usuarios")
	}
	if err := RenameConversation("alice", c.ID, "Autoscaling", "diagnostic"); err != nil {
		t.Fatal(err)
	}
	got, _ = GetConversation("alice", c.ID)
	if got.Title != "Autoscaling" || got.Mode != "diagnostic" {
		t.Fatalf("preferencias nao persistidas: %+v", got)
	}
	if err := DeleteConversation("alice", c.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := GetConversation("alice", c.ID); ok {
		t.Fatal("conversa deveria ter sido excluida")
	}
}

func TestConversationContextIsBoundedAndLabeled(t *testing.T) {
	c := Conversation{}
	for i := 0; i < 15; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		c.Messages = append(c.Messages, ConversationMessage{Role: role, Content: strings.Repeat("x", 1400)})
	}
	ctx := ConversationContext(c, "")
	if !strings.Contains(ctx, "Aluno:") || !strings.Contains(ctx, "Tutor:") {
		t.Fatalf("contexto sem papeis: %q", ctx)
	}
	if len(ctx) > 12200 {
		t.Fatalf("contexto sem limite: %d", len(ctx))
	}
}

func TestGroundedPromptSupportsResponseModesAndHistory(t *testing.T) {
	prompt, _ := BuildGroundedChatPromptWithContext("o que e um Pod?", "Aluno: explique simples", "diagnostic")
	for _, want := range []string{"sintomas, hipoteses", "HISTORICO RECENTE", "explique simples"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt nao contem %q", want)
		}
	}
}
