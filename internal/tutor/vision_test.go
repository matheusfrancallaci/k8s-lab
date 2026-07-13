package tutor

import (
	"context"
	"testing"
)

func TestVisionFailsClosedWithoutRemoteProvider(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	_, err := AnalyzeImageAttachment(context.Background(), "data:image/png;base64,AAAA", "image/png")
	if err == nil {
		t.Fatal("vision nao pode fingir analise sem modelo configurado")
	}
}

func TestVisionRejectsInvalidAttachment(t *testing.T) {
	if _, err := AnalyzeImageAttachment(context.Background(), "texto", "text/plain"); err == nil {
		t.Fatal("anexo invalido aceito como imagem")
	}
}
