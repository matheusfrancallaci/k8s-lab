package tutor

import (
	"fmt"
	"os"
	"strings"
	"time"

	"estudo-app/internal/models"
)

// shouldVerifyGeneratedKubernetesLabs is explicitly enabled only after the
// cluster sandbox is provisioned. Local development and unit tests stay fast,
// while production can opt in with K8S_LAB_VERIFY_GENERATED=1.
func shouldVerifyGeneratedKubernetesLabs() bool {
	if value := strings.TrimSpace(os.Getenv("K8S_LAB_VERIFY_GENERATED")); value != "" {
		return value == "1" || strings.EqualFold(value, "true")
	}
	return false
}

func isKubernetesLab(q models.Question) bool {
	text := strings.ToLower(strings.Join([]string{string(q.Cert), q.Topic, q.AnswerCommand, setupText(q), validationText(q)}, " "))
	return strings.Contains(text, "kubectl") || strings.Contains(text, "helm") || strings.Contains(text, "argocd")
}

// VerifyGeneratedKubernetesLab executes only deterministic internal templates
// in a throwaway namespace. It proves the solution satisfies every validator
// before the lab is delivered, mirroring the Terraform/Ansible contract.
func VerifyGeneratedKubernetesLab(q models.Question) error {
	if q.Source != models.SourceGenerated || !isKubernetesLab(q) {
		return nil
	}
	for _, command := range append(append(setupCommands(q), q.AnswerCommand), append(labValidationCommands(q), q.Teardown...)...) {
		if reason := BlockedLabCommandReason(command); reason != "" {
			return fmt.Errorf("lab Kubernetes reprovado pelo guardrail: %s", reason)
		}
	}
	ns := "lab-verify-" + SanitizeID(q.ID)
	if len(ns) > 63 {
		ns = ns[:63]
	}
	kubeconfig, err := os.CreateTemp("", "k8s-lab-verify-*.yaml")
	if err != nil {
		return err
	}
	kubeconfigPath := kubeconfig.Name()
	_ = kubeconfig.Close()
	defer os.Remove(kubeconfigPath)
	if _, err := sh(fmt.Sprintf(`kubectl create namespace %s >/dev/null && kubectl config view --raw > %q && KUBECONFIG=%q kubectl config set-context --current --namespace=%s >/dev/null`, ns, kubeconfigPath, kubeconfigPath, ns), 30); err != nil {
		return fmt.Errorf("namespace efemero da verificacao nao iniciou: %w", err)
	}
	base := fmt.Sprintf("export KUBECONFIG=%q; ", kubeconfigPath)
	cleanup := fmt.Sprintf(`kubectl delete namespace %s --ignore-not-found --wait=false >/dev/null 2>&1`, ns)
	defer func() { _, _ = sh(cleanup, 15) }()
	if commands := setupCommands(q); len(commands) > 0 {
		if _, err := sh(base+strings.Join(commands, "; "), 180); err != nil {
			return fmt.Errorf("setup da verificacao Kubernetes falhou: %w", err)
		}
	}
	if _, err := sh(base+q.AnswerCommand, 180); err != nil {
		return fmt.Errorf("solucao Kubernetes nao aplicou: %w", err)
	}
	for _, validation := range appendValidationObjects(q) {
		output, _ := sh(base+validation.Command, 60)
		want := validation.ExpectedContains
		if want == "" {
			want = validation.ExpectedOutput
		}
		if want != "" && !strings.Contains(output, want) {
			return fmt.Errorf("validador Kubernetes nao confirmou %q", want)
		}
	}
	return nil
}

func appendValidationObjects(q models.Question) []models.Validation {
	var out []models.Validation
	if q.Validation != nil {
		out = append(out, *q.Validation)
	}
	for _, goal := range q.Goals {
		if goal.Validation != nil {
			out = append(out, *goal.Validation)
		}
	}
	return out
}

func verifyGeneratedKubernetesLabs(qs []models.Question) error {
	if !shouldVerifyGeneratedKubernetesLabs() {
		return nil
	}
	for _, q := range qs {
		started := time.Now()
		err := VerifyGeneratedKubernetesLab(q)
		recordTutorLatency("lab.kubernetes_verify", time.Since(started), 0, err != nil)
		if err != nil {
			return err
		}
	}
	return nil
}
