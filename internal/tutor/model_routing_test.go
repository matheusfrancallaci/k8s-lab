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
