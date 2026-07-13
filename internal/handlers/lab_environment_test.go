package handlers

import (
	"strings"
	"testing"
	"time"

	"estudo-app/internal/models"
	"estudo-app/internal/repository"
)

func TestVclusterValuesUseInternalEndpointAndEphemeralStorage(t *testing.T) {
	env := repository.LabEnvironment{
		ID: "abc", Owner: "alice", Namespace: "lab-alice-abc", ClusterName: "student",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	values := vclusterValues(env)
	for _, want := range []string{
		"student.lab-alice-abc.svc", "enabled: true", "size: 1Gi", "retentionPolicy: Delete", "cpu: 100m", "memory: 256Mi",
	} {
		if !strings.Contains(values, want) {
			t.Fatalf("values do vcluster nao contem %q:\n%s", want, values)
		}
	}
}

func TestVirtualShellDoesNotUseHostServiceAccount(t *testing.T) {
	rc := virtualCloudShellRC()
	if !strings.Contains(rc, "KUBECONFIG=/tmp/.labkube") || strings.Contains(rc, "serviceaccount/token") {
		t.Fatalf("terminal nao esta isolado do kubeconfig host:\n%s", rc)
	}
}

func TestVirtualClusterPreservesOfficialNamespaces(t *testing.T) {
	t.Setenv("AZURE_MANAGED_IDENTITY", "1")
	t.Setenv("LAB_VCLUSTER_ENABLED", "1")
	q := models.Question{
		Question:      "trabalhe no namespace tools",
		AnswerCommand: "kubectl get pods -n tools",
		LabSpec: &models.LabSpec{
			Namespace: "tools",
			LabPlan:   &models.LabPlan{Namespace: "tools"},
		},
	}
	got := scopeLabForUser(q, "alice")
	if got.Question != q.Question || got.AnswerCommand != q.AnswerCommand || got.LabSpec.Namespace != "tools" || got.LabSpec.LabPlan.Namespace != "tools" {
		t.Fatalf("cluster virtual alterou o namespace oficial: %+v", got)
	}
}
