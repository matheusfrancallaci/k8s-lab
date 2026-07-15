package tutor

import "testing"

func TestRouteConversationModelUsesLocalWithoutRemote(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("OLLAMA_CHAT_MODEL", "qwen-local")
	r := RouteConversationModel("explique pods", "short")
	if r.Tier != "local" || r.Model != "qwen-local" {
		t.Fatalf("rota inesperada: %+v", r)
	}
}

func TestRouteConversationModelEscalatesComplexDiagnostic(t *testing.T) {
	t.Setenv("LLM_API_KEY", "test")
	t.Setenv("LLM_MODEL", "balanced")
	t.Setenv("LLM_FAST_MODEL", "fast")
	t.Setenv("LLM_FRONTIER_MODEL", "frontier")
	r := RouteConversationModel("compare trade-offs de seguranca e diagnostique incidente multi-cluster em producao", "diagnostic")
	if r.Tier != "frontier" || r.Model != "frontier" || r.Score < 5 {
		t.Fatalf("rota nao escalou: %+v", r)
	}
}

func TestAgentInspectionRequiresExplicitReadOnlyIntent(t *testing.T) {
	if WantsClusterInspection("explique o que e um cluster", "diagnostic") {
		t.Fatal("explicacao nao deve executar ferramenta")
	}
	if !WantsClusterInspection("diagnostique meu cluster em modo somente leitura", "diagnostic") {
		t.Fatal("diagnostico explicito deveria planejar leitura")
	}
}

func TestModelCanaryAndRollbackSignal(t *testing.T) {
	t.Setenv("LLM_API_KEY", "test")
	t.Setenv("LLM_MODEL", "stable")
	t.Setenv("LLM_CANDIDATE_MODEL", "candidate")
	t.Setenv("LLM_CANARY_PERCENT", "100")
	r := RouteConversationModel("diagnostique este incidente", "diagnostic")
	if r.Tier != "canary" || r.Model != "candidate" {
		t.Fatalf("canary nao selecionado: %+v", r)
	}
	modelExperiments.Lock()
	modelExperiments.Values = map[string]ModelExperimentStat{}
	modelExperiments.Unlock()
	for i := 0; i < 10; i++ {
		RecordModelOutcome(r, GroundingAudit{Coverage: 60, Passed: i < 5})
	}
	rep := ModelExperiments()
	if !rep.RollbackRecommended || rep.Models["candidate"].Requests != 10 {
		t.Fatalf("rollback nao detectado: %+v", rep)
	}
}

func TestSolLimitAppliesOnlyToSol(t *testing.T) {
	t.Setenv("TUTOR_PREMIUM_USAGE_PATH", t.TempDir()+"/premium.json")
	t.Setenv("LLM_API_KEY", "test-key")
	t.Setenv("LLM_MODEL", "gpt-5.6-sol")
	t.Setenv("LLM_FRONTIER_MODEL", "gpt-5.6-sol")
	t.Setenv("LLM_FAST_MODEL", "gpt-5.6-luna")
	t.Setenv("LLM_PREMIUM_QUESTION_LIMIT", "10")
	prompt := "compare trade-offs de seguranca e diagnostique incidente multi-cluster em producao"
	for i := 0; i < 10; i++ {
		route, usage, reserved := ReserveConversationRoute("alice", prompt, "auto")
		if !reserved || route.Model != "gpt-5.6-sol" || usage.Used != i+1 {
			t.Fatalf("reserva Sol %d incorreta: route=%+v usage=%+v reserved=%v", i+1, route, usage, reserved)
		}
	}
	route, usage, reserved := ReserveConversationRoute("alice", prompt, "auto")
	if reserved || route.Model != "gpt-5.6-luna" || usage.Remaining != 0 {
		t.Fatalf("apos 10 deve cair no modelo ilimitado: route=%+v usage=%+v", route, usage)
	}
	fastRoute, _, fastReserved := ReserveConversationRoute("bob", "explique pods", "short")
	if fastReserved || fastRoute.Model != "gpt-5.6-luna" {
		t.Fatalf("modelo que nao e Sol nao pode consumir cota: %+v", fastRoute)
	}
}
