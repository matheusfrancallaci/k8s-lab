package tutor

import (
	"path/filepath"
	"testing"
)

func TestIsEnvironmentFailureSeparatesInfraFromStudent(t *testing.T) {
	env := []string{
		"The connection to the server 10.0.0.1:6443 was refused - did you specify the right host or port?",
		"Unable to connect to the server: dial tcp: i/o timeout",
		"error: current-context is not set",
		"sh: kubectl: command not found",
		"Error from server (ServiceUnavailable): the server is currently unable to handle the request",
	}
	for _, out := range env {
		if !IsEnvironmentFailure(out) {
			t.Fatalf("deveria classificar como falha de ambiente: %q", out)
		}
	}
	student := []string{
		`Error from server (NotFound): pods "web" not found`,
		`Error from server (Forbidden): pods is forbidden: User "aluno" cannot list resource`,
		"NAME READY STATUS RESTARTS\nweb 0/1 CrashLoopBackOff 5",
		"FAIL",
	}
	for _, out := range student {
		if IsEnvironmentFailure(out) {
			t.Fatalf("erro do aluno não pode virar falha de ambiente: %q", out)
		}
	}
}

// Falha de ambiente não polui a taxa de sucesso nem o skill do aluno; falha do
// mesmo validador para 3+ alunos distintos vira suspeita de lab quebrado.
func TestEnvFailuresAndSuspectGoalsInObservability(t *testing.T) {
	t.Setenv("LAB_OBSERVABILITY_PATH", filepath.Join(t.TempDir(), "labs.json"))
	resetLabObservabilityForTest()
	q := generateQuestions("Autoscaling", "CKA", 2, 1)[0]

	RecordLabValidation("alice", q, 0, "kubectl get hpa", false, "Unable to connect to the server: dial tcp: i/o timeout")
	got := LabObservability()
	if got.Attempts != 0 || got.Failures != 0 || got.EnvFailures != 1 {
		t.Fatalf("falha de ambiente não entra em attempts/failures: %+v", got)
	}

	for _, user := range []string{"alice", "bob", "carol"} {
		RecordLabValidation(user, q, 0, "kubectl get hpa -n apps", false, "FAIL")
	}
	got = LabObservability()
	if got.Attempts != 3 || got.Failures != 3 {
		t.Fatalf("falhas de aluno deveriam contar normalmente: %+v", got)
	}
	if len(got.SuspectGoals) != 1 || got.SuspectGoals[0].DistinctUsers != 3 {
		t.Fatalf("3 alunos distintos no mesmo validador deveriam disparar suspeita: %+v", got.SuspectGoals)
	}

	// O mesmo aluno repetindo a falha não infla a contagem de usuários distintos.
	RecordLabValidation("alice", q, 0, "kubectl get hpa -n apps", false, "FAIL")
	got = LabObservability()
	if got.SuspectGoals[0].DistinctUsers != 3 {
		t.Fatalf("repetição do mesmo aluno não é usuário novo: %+v", got.SuspectGoals)
	}
}

// A série diária do perfil sustenta a tendência do painel.
func TestProfileHistorySnapshots(t *testing.T) {
	user := "unit-history"
	p := resetProfile(t, user)
	p.mu.Lock()
	p.History = nil
	p.mu.Unlock()
	q := generateQuestions("Autoscaling", "CKA", 2, 1)[0]

	RecordGoal(user, q, true)
	RecordGoal(user, q, false)

	hist := History(user)
	if len(hist) != 1 {
		t.Fatalf("eventos do mesmo dia colapsam em 1 snapshot, veio %d", len(hist))
	}
	snap := hist[0]
	if snap.Attempts != 2 {
		t.Fatalf("snapshot deveria acumular tentativas do perfil: %+v", snap)
	}
	if snap.AvgScore <= 0 || snap.AvgScore >= 1 {
		t.Fatalf("EWMA médio deveria estar entre 0 e 1: %+v", snap)
	}
	if snap.Date == "" {
		t.Fatalf("snapshot precisa de data: %+v", snap)
	}
}
