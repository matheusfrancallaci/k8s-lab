package handlers

import (
	"strings"
	"testing"
)

func TestLabNamespaceSecurityLabels(t *testing.T) {
	t.Setenv("LAB_PSA_ENFORCE", "baseline")
	labels := labNamespaceSecurityLabels()
	if labels["pod-security.kubernetes.io/enforce"] != "baseline" || labels["k8s-study-lab/user-namespace"] != "true" {
		t.Fatalf("labels de isolamento incompletas: %#v", labels)
	}
	if script := namespaceGuardrailsScript("lab-alice"); script == "" || !strings.Contains(strings.ToLower(script), "networkpolicy") {
		t.Fatalf("script de guardrails incompleto: %q", script)
	}
}
