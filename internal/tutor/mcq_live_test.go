package tutor

import (
	"os"
	"strings"
	"testing"

	"estudo-app/internal/models"
)

// TestLiveMCQConcept exercita o caminho REAL (IA local) de AuthorMCQBatch.
// Guardado por RUN_LIVE_OLLAMA=1 para nunca rodar no CI; redireciona todas as
// escritas para um tempdir. Rode com:
//
//	RUN_LIVE_OLLAMA=1 go test ./internal/tutor -run TestLiveMCQConcept -v
func TestLiveMCQConcept(t *testing.T) {
	if os.Getenv("RUN_LIVE_OLLAMA") != "1" {
		t.Skip("defina RUN_LIVE_OLLAMA=1 para o teste live com Ollama")
	}
	tmp := t.TempDir()
	t.Setenv("QUESTIONS_CUSTOM_DIR", tmp)
	t.Setenv("RAG_DATA_DIR", tmp)
	if ok, model := LLMStatus(); !ok {
		t.Skipf("Ollama indisponível: %s", model)
	} else {
		t.Logf("modelo ativo: %s", model)
	}

	cert, topic := "CKA", "Services"
	qs, rep, err := AuthorMCQBatch(cert, topic, 3, 2, nil)
	if err != nil {
		t.Fatalf("AuthorMCQBatch falhou: %v (report=%+v)", err, rep)
	}
	t.Logf("report: requested=%d ready=%d rejected=%d duplicates=%d grounded=%v model=%s",
		rep.Requested, rep.Ready, rep.Rejected, rep.Duplicates, rep.Grounded, rep.UsedModel)
	for _, r := range rep.Reasons {
		t.Logf("  motivo: %s", r)
	}
	for _, f := range rep.Failures {
		t.Logf("  reprovada: %s", f)
	}
	if len(qs) == 0 {
		t.Fatal("nenhuma questão gerada")
	}
	for i, q := range qs {
		if q.Type != models.MultipleChoice {
			t.Errorf("q%d: tipo %q != multiple_choice", i, q.Type)
		}
		if len(q.Options) != 4 || q.Answer < 0 || q.Answer >= 4 {
			t.Errorf("q%d: opções/resposta inválidas (%d opções, answer=%d)", i, len(q.Options), q.Answer)
		}
		if q.Readiness == nil || q.Readiness.State != "grounded" {
			t.Errorf("q%d: prontidão inesperada %+v", i, q.Readiness)
		}
		var b strings.Builder
		b.WriteString("\n──────────────────────────────────────────\n")
		b.WriteString("Q" + itoa(i+1) + " [" + string(q.Cert) + " · " + q.Topic + " · " + string(q.Difficulty) + "]\n")
		b.WriteString(q.Question + "\n")
		for j, o := range q.Options {
			mark := "  "
			if j == q.Answer {
				mark = "✓ "
			}
			b.WriteString(mark + string(rune('A'+j)) + ") " + o + "\n")
		}
		b.WriteString("→ " + strings.SplitN(q.Explanation, "\n", 2)[0] + "\n")
		if q.DocURL != "" {
			b.WriteString("fonte: " + q.DocURL + "\n")
		}
		t.Log(b.String())
	}
}

// TestLiveMCQCommand exercita o caminho DETERMINÍSTICO (offline, sem Ollama) de
// questões de comando: correta = AnswerCommand do template, distratores mutados.
// A verificação executável dos distratores só roda com cluster
// (K8S_LAB_VERIFY_GENERATED=1); aqui só mostramos a geração.
//
//	RUN_LIVE_MCQ=1 go test ./internal/tutor -run TestLiveMCQCommand -v
func TestLiveMCQCommand(t *testing.T) {
	if os.Getenv("RUN_LIVE_MCQ") != "1" {
		t.Skip("defina RUN_LIVE_MCQ=1 para ver a geração de questões de comando")
	}
	tmp := t.TempDir()
	t.Setenv("QUESTIONS_CUSTOM_DIR", tmp)

	qs, rep, err := AuthorCommandMCQBatch("CKA", "", 4, 2, nil)
	if err != nil {
		t.Fatalf("AuthorCommandMCQBatch falhou: %v (%+v)", err, rep)
	}
	t.Logf("report: ready=%d rejected=%d duplicates=%d verified=%v", rep.Ready, rep.Rejected, rep.Duplicates, rep.Verified)
	for i, q := range qs {
		var b strings.Builder
		b.WriteString("\n──────────────────────────────────────────\n")
		b.WriteString("Q" + itoa(i+1) + " [" + string(q.Cert) + " · " + q.Topic + " · prontidão=" + q.Readiness.State + "]\n")
		b.WriteString(q.Question + "\n")
		for j, o := range q.Options {
			mark := "  "
			if j == q.Answer {
				mark = "✓ "
			}
			b.WriteString(mark + string(rune('A'+j)) + ") " + o + "\n")
		}
		b.WriteString("validador do efeito: " + q.Validation.Command + "\n")
		t.Log(b.String())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
