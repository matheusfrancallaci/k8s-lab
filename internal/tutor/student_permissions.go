package tutor

import (
	"fmt"
	"regexp"
	"strings"

	"estudo-app/internal/models"
)

var clusterWriteRe = regexp.MustCompile(`(?i)\bkubectl\s+(?:create|apply|replace|patch|edit|delete|label|annotate|taint|cordon|uncordon|drain)\b[^\n;]*(?:\bnodes?\b|\bclusterroles?\b|\bclusterrolebindings?\b|\bcustomresourcedefinitions?\b|\bcrds?\b|\bstorageclasses?\b|\bpersistentvolumes?\b|\bapiservices?\b|\bmutatingwebhookconfigurations?\b|\bvalidatingwebhookconfigurations?\b)`)

var clusterScopedKinds = regexp.MustCompile(`(?im)^\s*kind:\s*(?:Node|ClusterRole|ClusterRoleBinding|CustomResourceDefinition|StorageClass|PersistentVolume|APIService|MutatingWebhookConfiguration|ValidatingWebhookConfiguration)\s*$`)

// StudentPermissionGate rejects labs whose solution requires cluster-scoped
// writes that the isolated learner account intentionally does not receive. It
// is better to explain that a lab cannot run here than let the learner hit a
// Forbidden halfway through the exercise.
func StudentPermissionGate(q models.Question) error {
	if q.Type != models.Lab || !isKubernetesLab(q) {
		return nil
	}
	text := strings.Join(append(append(setupCommands(q), q.AnswerCommand), append(labValidationCommands(q), q.Teardown...)...), "\n")
	if clusterWriteRe.MatchString(text) || clusterScopedKinds.MatchString(text) {
		return fmt.Errorf("o lab exige escrita em recurso global do cluster, mas o ambiente do aluno oferece somente escrita isolada por namespace")
	}
	return nil
}

func validateGeneratedLabs(qs []models.Question) error {
	for _, q := range qs {
		if err := LabQualityGate(q); err != nil {
			return err
		}
		if err := StudentPermissionGate(q); err != nil {
			return err
		}
	}
	if err := verifyGeneratedKubernetesLabs(qs); err != nil {
		return fmt.Errorf("o lab nao passou na verificacao executavel: %w", err)
	}
	return nil
}
