package tutor

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// O loop de qualidade do docs/game-change.md: 👎 do aluno vira fixture durável
// de regressão — cada reclamação passa a rodar em todo golden eval.
func TestNegativeFeedbackPromotesRegressionFixture(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROMPT_QUALITY_PATH", filepath.Join(dir, "quality.json"))
	t.Setenv("REGRESSION_FIXTURES_PATH", filepath.Join(dir, "fixtures.json"))
	t.Setenv("TUTOR_FEEDBACK_PATH", filepath.Join(dir, "feedback.jsonl"))
	resetPromptQualityForTest()

	prompt := "criar questao da CKA de HPA nivel 3"
	qs := generateQuestions("Autoscaling", "CKA", 3, 1)
	RecordPromptQuality("alice", prompt, "CKA", ChatResult{
		Reply:     "lab criado",
		Action:    sessionAction("s1", qs[0].ID, len(qs), qs),
		Questions: qs,
	})

	if err := RecordTutorFeedback("alice", "msg-1", "unhelpful", "CKA", "Autoscaling", prompt); err != nil {
		t.Fatalf("feedback deveria ser registrado: %v", err)
	}

	fixtures := loadRegressionFixtures()
	if len(fixtures) != 1 {
		t.Fatalf("👎 deveria promover o caso a fixture de regressão, veio %d", len(fixtures))
	}
	if !strings.EqualFold(fixtures[0].Prompt, prompt) {
		t.Fatalf("fixture deveria guardar o prompt avaliado: %+v", fixtures[0])
	}
	if !containsFold(fixtures[0].Risks, "feedback negativo") {
		t.Fatalf("fixture deveria carregar o risco de feedback negativo: %+v", fixtures[0].Risks)
	}
	if fixtures[0].UserHash != "" {
		t.Fatalf("fixture nunca guarda o hash do usuário: %+v", fixtures[0])
	}

	sum := TutorFeedbackSummary()
	if sum.Negative != 1 || sum.PromotedToEval != 1 {
		t.Fatalf("summary deveria contar o 👎 promovido: %+v", sum)
	}
}

// Feedback positivo e 👎 sem caso rastreado (conversa livre) não geram fixture.
func TestFeedbackWithoutTrackedPromptDoesNotPromote(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PROMPT_QUALITY_PATH", filepath.Join(dir, "quality.json"))
	t.Setenv("REGRESSION_FIXTURES_PATH", filepath.Join(dir, "fixtures.json"))
	t.Setenv("TUTOR_FEEDBACK_PATH", filepath.Join(dir, "feedback.jsonl"))
	resetPromptQualityForTest()

	if err := RecordTutorFeedback("bob", "msg-2", "unhelpful", "CKA", "", "o que é um pod?"); err != nil {
		t.Fatalf("feedback deveria ser registrado: %v", err)
	}
	if err := RecordTutorFeedback("bob", "msg-3", "helpful", "CKA", "", "criar questao da CKA de HPA nivel 3"); err != nil {
		t.Fatalf("feedback deveria ser registrado: %v", err)
	}
	if fixtures := loadRegressionFixtures(); len(fixtures) != 0 {
		t.Fatalf("sem caso rastreado (ou com 👍) não deve haver fixture: %+v", fixtures)
	}
	sum := TutorFeedbackSummary()
	if sum.Total != 2 || sum.Positive != 1 || sum.Negative != 1 || sum.PromotedToEval != 0 {
		t.Fatalf("summary inesperado: %+v", sum)
	}
}

// A mesma assinatura de falha deve reaproveitar a explicação (menos latência,
// menos custo de gateway); identificadores voláteis não quebram o hit.
func TestFailureSignatureNormalizesVolatileTokens(t *testing.T) {
	a := failureSignature("q", "goal", "kubectl get pods", "pod web-7f9cd4 not ready, restarts 3")
	b := failureSignature("q", "goal", "kubectl get pods", "pod web-8a1bc2 not ready, restarts 7")
	if a != b {
		t.Fatalf("sufixo de pod/contagens são voláteis — assinaturas deveriam coincidir")
	}
	c := failureSignature("q", "goal", "kubectl get pods", "deployment web has 0/2 replicas available")
	if a == c {
		t.Fatalf("saídas de tipos diferentes não podem colidir")
	}
}

func TestExplainCacheStoresAndExpires(t *testing.T) {
	explainCacheMu.Lock()
	explainCache = map[string]explainEntry{}
	explainCacheMu.Unlock()

	storeExplanation("sig-1", "explicação didática")
	if text, ok := cachedExplanation("sig-1"); !ok || text != "explicação didática" {
		t.Fatalf("cache deveria devolver a explicação guardada, veio %q %v", text, ok)
	}
	explainCacheMu.Lock()
	explainCache["sig-1"] = explainEntry{text: "velha", expires: time.Now().Add(-time.Minute)}
	explainCacheMu.Unlock()
	if _, ok := cachedExplanation("sig-1"); ok {
		t.Fatalf("entrada expirada não pode ser servida")
	}
}
