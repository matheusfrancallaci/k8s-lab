package tutor

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"estudo-app/internal/models"
)

func TestPrepareLabForDeliveryKeepsLowQualityContentAvailable(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("K8S_LAB_VERIFY_GENERATED", "0")

	q := models.Question{
		ID:          "generated-incomplete-lab",
		Cert:        models.CKA,
		Topic:       "Workloads",
		Type:        models.Lab,
		Source:      models.SourceGenerated,
		Question:    "Investigue por que o deployment nao alcanca o estado desejado.",
		Hint:        "Comece comparando o estado desejado com o estado atual.",
		Explanation: "O estado observado indica o proximo passo do diagnostico.",
	}

	got := PrepareLabForDelivery(q)
	if !strings.Contains(got.Question, "Investigue") || got.Hint == "" || got.Explanation == "" {
		t.Fatalf("conteudo pedagogico nao pode ser removido por um gate: %+v", got)
	}
	if got.LabSpec == nil || got.LabSpec.Readiness.State != "degraded" {
		t.Fatalf("falha de qualidade deve virar aviso interno, nao bloqueio: %+v", got.LabSpec)
	}
	if !strings.Contains(got.LabSpec.Readiness.Failure, "quality gate") {
		t.Fatalf("motivo do gate deve permanecer observavel: %+v", got.LabSpec.Readiness)
	}

	entries := LabCatalog()
	if len(entries) != 1 || entries[0].ID != q.ID || entries[0].Readiness.State != "degraded" {
		t.Fatalf("catalogo deve registrar degradacao sem retirar conteudo: %+v", entries)
	}
}

func TestPrepareLabForDeliveryNeverRunsExecutableVerification(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("K8S_LAB_VERIFY_GENERATED", "1")

	qs := generateQuestions("Workloads", "CKA", 2, 1)
	if len(qs) != 1 {
		t.Fatal("template de teste nao gerou lab")
	}
	q := qs[0]
	q.Source = models.SourceGenerated

	previous := executableKubernetesLabVerifier
	called := 0
	executableKubernetesLabVerifier = func(models.Question) error {
		called++
		return nil
	}
	t.Cleanup(func() { executableKubernetesLabVerifier = previous })

	got := PrepareLabForDelivery(q)
	if got.Question == "" {
		t.Fatal("prepare deve devolver o conteudo")
	}
	if called != 0 {
		t.Fatalf("GET/setup nao pode repetir verificacao executavel; chamadas=%d", called)
	}
}

func TestGenerateRejectsContentWhenExecutableVerificationFails(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("K8S_LAB_VERIFY_GENERATED", "1")
	t.Setenv("RAG_DATA_DIR", filepath.Join(t.TempDir(), "rag"))

	canonical := CanonicalCert("CKA")
	ragMu.Lock()
	previousIndex, hadIndex := ragIndexes[canonical]
	ragIndexes[canonical] = &ragIndex{Cert: canonical, Hydrated: true}
	ragMu.Unlock()
	t.Cleanup(func() {
		ragMu.Lock()
		defer ragMu.Unlock()
		if hadIndex {
			ragIndexes[canonical] = previousIndex
		} else {
			delete(ragIndexes, canonical)
		}
	})

	previousVerifier := executableKubernetesLabVerifier
	called := 0
	executableKubernetesLabVerifier = func(models.Question) error {
		called++
		return errors.New("cluster de verificacao indisponivel")
	}
	t.Cleanup(func() { executableKubernetesLabVerifier = previousVerifier })

	qs, err := Generate("Workloads", "CKA", 2, 1)
	if err == nil || !strings.Contains(err.Error(), "validacao falhou") {
		t.Fatalf("falha executavel precisa cancelar a publicacao: labs=%d err=%v", len(qs), err)
	}
	if called != 1 || len(qs) != 0 {
		t.Fatalf("verificacao deveria ser tentada uma vez: called=%d labs=%d", called, len(qs))
	}
	files, globErr := filepath.Glob(filepath.Join("questions-custom", "gen-*.yaml"))
	if globErr != nil || len(files) != 0 {
		t.Fatalf("lab reprovado nao pode ser persistido: files=%v err=%v", files, globErr)
	}
	entries := LabCatalog()
	if len(entries) != 1 || entries[0].Readiness.State != "rejected" {
		t.Fatalf("catalogo interno deve preservar a falha sem publicar o lab: %+v", entries)
	}
}

func TestSmartLabPathRunsExecutableVerificationBeforePublishing(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("K8S_LAB_VERIFY_GENERATED", "1")
	previousVerifier := executableKubernetesLabVerifier
	called := 0
	executableKubernetesLabVerifier = func(q models.Question) error {
		called++
		if q.Source != models.SourceGenerated {
			t.Fatalf("verificador recebeu lab sem proveniencia gerada: %+v", q)
		}
		return nil
	}
	t.Cleanup(func() { executableKubernetesLabVerifier = previousVerifier })

	qs, _, err := GenerateSmartLabs("Crie 1 lab pratico sobre NodePort", "CKA", 2, 1)
	if err != nil || len(qs) != 1 {
		t.Fatalf("smart lab deveria publicar depois da verificacao: labs=%d err=%v", len(qs), err)
	}
	if called != 1 || qs[0].LabSpec == nil || qs[0].LabSpec.Readiness.State != "ready" {
		t.Fatalf("verificacao executavel nao foi aplicada: called=%d lab=%+v", called, qs[0].LabSpec)
	}
}

func TestPrepareLabForDeliveryDoesNotDisableSetupGuardrail(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("K8S_LAB_VERIFY_GENERATED", "0")

	q := models.Question{
		ID:            "unsafe-setup-visible-lab",
		Cert:          models.CKA,
		Topic:         "Troubleshooting",
		Type:          models.Lab,
		Source:        models.SourceGenerated,
		Question:      "Analise o ambiente e corrija a configuracao do workload com seguranca.",
		AnswerCommand: "kubectl get pods",
		Setup: []models.SetupStep{{
			Description: "Preparar ambiente",
			Command:     "sudo rm -rf /",
		}},
		Goals: []models.Goal{{
			Description: "Confirmar os pods",
			Validation: &models.Validation{
				Command:          "kubectl get pods",
				ExpectedContains: "Running",
			},
		}},
		Teardown: []string{"kubectl delete pod demo --ignore-not-found"},
	}

	got := PrepareLabForDelivery(q)
	if got.Question == "" || got.LabSpec == nil || got.LabSpec.Readiness.State != "degraded" {
		t.Fatalf("enunciado deve continuar acessivel com aviso interno: %+v", got)
	}
	if reason := BlockedLabCommandReason(got.Setup[0].Command); reason == "" {
		t.Fatal("entrega nao pode desativar o guardrail aplicado na execucao do setup")
	}
}
