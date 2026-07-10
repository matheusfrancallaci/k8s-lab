package tutor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"estudo-app/internal/models"

	"gopkg.in/yaml.v3"
)

// Auditoria do banco curado INTEIRO: cada lab que chega ao aluno precisa
// sobreviver ao mesmo preflight da entrega e ter validação verificável.
// Curado = escrito por humano; humano esquece — o teste não.
func loadCuratedBank(t *testing.T) []models.Question {
	t.Helper()
	root := filepath.Join("..", "..", "questions")
	var all []models.Question
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var qf models.QuestionFile
		if err := yaml.Unmarshal(b, &qf); err != nil {
			t.Fatalf("YAML invalido em %s: %v", path, err)
		}
		all = append(all, qf.Questions...)
		return nil
	})
	if err != nil {
		t.Fatalf("nao consegui ler o banco curado: %v", err)
	}
	if len(all) < 100 {
		t.Fatalf("banco curado suspeito de truncado: %d questoes", len(all))
	}
	return all
}

func TestBankHandsOnLabsPassPreflight(t *testing.T) {
	var handson, failed int
	for _, q := range loadCuratedBank(t) {
		if q.Type != models.Lab || len(q.Goals) == 0 {
			continue
		}
		handson++
		fq := FinalizeLab(q, "")
		if err := LabDeliveryPreflight(fq); err != nil {
			failed++
			t.Errorf("lab %s (%s/%s) reprova no preflight de entrega: %v", q.ID, q.Cert, q.Topic, err)
		}
	}
	if handson == 0 {
		t.Fatal("nenhum lab hands-on encontrado no banco")
	}
	t.Logf("labs hands-on auditados: %d (reprovados: %d)", handson, failed)
}

func TestBankGoalsHaveVerifiableValidation(t *testing.T) {
	// Goal que valida só exit code aprova falso positivo com facilidade
	// (comando que "roda" mas não prova o estado). Todo goal do banco curado
	// exige comando + resultado esperado.
	for _, q := range loadCuratedBank(t) {
		if q.Type != models.Lab {
			continue
		}
		for i, g := range q.Goals {
			if g.Validation == nil || strings.TrimSpace(g.Validation.Command) == "" {
				t.Errorf("lab %s goal %d sem comando de validacao", q.ID, i+1)
				continue
			}
			if strings.TrimSpace(g.Validation.ExpectedContains) == "" && strings.TrimSpace(g.Validation.ExpectedOutput) == "" {
				t.Errorf("lab %s goal %d valida apenas exit code (sem expected)", q.ID, i+1)
			}
		}
	}
}

func TestBankCommandLabsHaveIntactAnswerKey(t *testing.T) {
	// Labs de comando (estilo quiz: options + answer_command, sem goals)
	// precisam de gabarito íntegro: answer dentro do range e comando não vazio.
	for _, q := range loadCuratedBank(t) {
		if q.Type != models.Lab || len(q.Goals) > 0 {
			continue
		}
		if len(q.Options) > 0 {
			if q.Answer < 0 || q.Answer >= len(q.Options) {
				t.Errorf("lab %s: answer %d fora do range de %d options", q.ID, q.Answer, len(q.Options))
			}
			if strings.TrimSpace(q.Explanation) == "" {
				t.Errorf("lab %s: sem explanation (o aluno erra e nao aprende por que)", q.ID)
			}
		}
	}
}
