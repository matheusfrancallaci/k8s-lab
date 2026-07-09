package tutor

import (
	"testing"

	"estudo-app/internal/models"
)

func TestKubernetesVerificationOptInAndClassification(t *testing.T) {
	t.Setenv("K8S_LAB_VERIFY_GENERATED", "0")
	if shouldVerifyGeneratedKubernetesLabs() {
		t.Fatal("verificacao deveria respeitar opt-out")
	}
	t.Setenv("K8S_LAB_VERIFY_GENERATED", "true")
	if !shouldVerifyGeneratedKubernetesLabs() {
		t.Fatal("verificacao deveria respeitar opt-in")
	}
	q := models.Question{Cert: models.CKA, Topic: "Workloads", AnswerCommand: "kubectl create deployment api --image=nginx"}
	if !isKubernetesLab(q) {
		t.Fatal("lab kubectl deveria ser classificado como Kubernetes")
	}
	if err := VerifyGeneratedKubernetesLab(models.Question{Source: models.SourceGenerated, AnswerCommand: "kubectl delete namespace --all"}); err == nil {
		t.Fatal("comando perigoso deveria ser rejeitado antes da execucao")
	}
}
