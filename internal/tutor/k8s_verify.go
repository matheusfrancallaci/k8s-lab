package tutor

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"estudo-app/internal/models"
)

var executableKubernetesLabVerifier = VerifyGeneratedKubernetesLab

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
	// A validator must not already pass before the student's solution. This
	// catches labs whose setup accidentally gives away the finished state.
	for _, validation := range appendValidationObjects(q) {
		output, err := sh(base+validation.Command, 60)
		want := validation.ExpectedContains
		if want == "" {
			want = validation.ExpectedOutput
		}
		if err == nil && (want == "" || strings.Contains(output, want)) {
			return fmt.Errorf("validador ja aprovava antes da solucao")
		}
	}
	if _, err := sh(base+q.AnswerCommand, 180); err != nil {
		// kubectl scale/patch logo após o create pode correr na frente do
		// controller — uma segunda tentativa elimina a corrida sem mascarar
		// solução realmente quebrada.
		time.Sleep(3 * time.Second)
		if _, err2 := sh(base+q.AnswerCommand, 180); err2 != nil {
			return fmt.Errorf("solucao Kubernetes nao aplicou: %w", err2)
		}
	}
	// Validadores com espera de convergência: rollout leva segundos e validar
	// imediatamente reprovava lab legítimo (visto na autoria real: "nao
	// confirmou 5" com as réplicas ainda subindo). Backoff até ~75s.
	for _, validation := range appendValidationObjects(q) {
		want := validation.ExpectedContains
		if want == "" {
			want = validation.ExpectedOutput
		}
		var lastErr error
		confirmed := false
		for attempt := 0; attempt < 6 && !confirmed; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(attempt) * 5 * time.Second)
			}
			output, validationErr := sh(base+validation.Command, 60)
			switch {
			case validationErr != nil:
				lastErr = fmt.Errorf("validador Kubernetes falhou: %w", validationErr)
			case want != "" && !strings.Contains(output, want):
				lastErr = fmt.Errorf("validador Kubernetes nao confirmou %q", want)
			default:
				confirmed = true
			}
		}
		if !confirmed {
			return lastErr
		}
	}
	if len(q.Teardown) > 0 {
		if _, err := sh(base+strings.Join(q.Teardown, "; "), 120); err != nil {
			return fmt.Errorf("teardown Kubernetes falhou: %w", err)
		}
	}
	if _, err := sh(fmt.Sprintf(`kubectl delete namespace %s --ignore-not-found --wait=true --timeout=90s >/dev/null`, ns), 100); err != nil {
		return fmt.Errorf("sandbox da verificacao nao foi limpo: %w", err)
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
	var verificationErrors []error
	for i := range qs {
		if !isKubernetesLab(qs[i]) {
			continue
		}
		// Um gate estatico degradado pode incluir comando inseguro. O conteudo
		// continua disponivel, mas nunca e elegivel a execucao automatica.
		if qs[i].LabSpec != nil && qs[i].LabSpec.Readiness.State == "degraded" {
			continue
		}
		started := time.Now()
		err := executableKubernetesLabVerifier(qs[i])
		recordTutorLatency("lab.kubernetes_verify", time.Since(started), 0, err != nil)
		markLabVerified(&qs[i], true, err)
		if err != nil {
			verificationErrors = append(verificationErrors, fmt.Errorf("lab %s: %w", qs[i].ID, err))
		}
	}
	if err := RecordLabCatalog(qs); err != nil {
		verificationErrors = append(verificationErrors, fmt.Errorf("catalogo de labs: %w", err))
	}
	return errors.Join(verificationErrors...)
}
