package tutor

import (
	"path/filepath"
	"strings"
	"testing"

	"estudo-app/internal/models"
)

func TestGenerateExamCompositeBuildsMultiTaskLab(t *testing.T) {
	t.Setenv("QUESTIONS_CUSTOM_DIR", t.TempDir())
	q, err := GenerateExamComposite("CKA", 3)
	if err != nil {
		t.Fatalf("composto deveria gerar: %v", err)
	}
	if q.Type != models.Lab || q.Source != models.SourceGenerated {
		t.Fatalf("composto deve ser lab gerado: %+v", q.Type)
	}
	if len(q.Goals) < 2 {
		t.Fatalf("estilo prova exige multiplos goals (crédito parcial), veio %d", len(q.Goals))
	}
	if !strings.Contains(q.Question, "Tarefa 2") {
		t.Fatalf("enunciado deveria ter tarefas numeradas: %.200s", q.Question)
	}
	for _, g := range q.Goals {
		if g.Validation == nil || strings.TrimSpace(g.Validation.Command) == "" {
			t.Fatalf("todo goal do composto precisa de validador: %+v", g)
		}
	}
	if files, _ := filepath.Glob(filepath.Join(CustomQuestionsDir(), "gen-*.yaml")); len(files) == 0 {
		t.Fatalf("composto deveria persistir em questions-custom")
	}
}

func TestConfusionDistractorsComeFromTheRightSet(t *testing.T) {
	got := confusionDistractors("--dry-run", "kubectl apply --dry-run=client", 3)
	if len(got) == 0 {
		t.Fatalf("--dry-run pertence a um conjunto de confusão")
	}
	for _, d := range got {
		if strings.EqualFold(d, "--dry-run") {
			t.Fatalf("distrator não pode ser a própria resposta")
		}
	}
	if got := confusionDistractors("NodePort", "tipo de service", 3); len(got) == 0 || containsFold(got, "NodePort") {
		t.Fatalf("NodePort deveria puxar ClusterIP/LoadBalancer, veio %v", got)
	}
	if got := confusionDistractors("--xyz-inexistente", "cmd", 3); len(got) != 0 {
		t.Fatalf("termo fora dos conjuntos não gera distratores de confusão")
	}
}

// Lab gerado com gabarito PROVADO quebrado (rejected + executable) não chega
// ao aluno; pendente/degradado continua disponível.
func TestDeliveryBlockReasonOnlyBlocksExecutablyRejected(t *testing.T) {
	t.Setenv("QUESTIONS_CUSTOM_DIR", t.TempDir())
	qs := generateQuestions("Workloads", "CKA", 2, 1)
	q := FinalizeLab(qs[0], "")
	q.Source = models.SourceGenerated // persist() carimba isso no fluxo real

	if reason := DeliveryBlockReason(q); reason != "" {
		t.Fatalf("lab recém-gerado (não verificado) deve ser entregue: %q", reason)
	}
	markLabDegraded(&q, "aviso qualquer")
	if reason := DeliveryBlockReason(q); reason != "" {
		t.Fatalf("degradado continua disponível: %q", reason)
	}
	markLabVerified(&q, true, errFake("solucao nao aplicou"))
	if reason := DeliveryBlockReason(q); reason == "" {
		t.Fatalf("rejeitado pela execução NÃO pode chegar ao aluno")
	}
	markLabVerified(&q, true, nil)
	if reason := DeliveryBlockReason(q); reason != "" {
		t.Fatalf("verificado ready deve ser entregue: %q", reason)
	}
}

type errFake string

func (e errFake) Error() string { return string(e) }
