package tutor

import (
	"strings"
	"testing"
)

func TestOrchestratorAdaptsStrategyToIntent(t *testing.T) {
	tests := []struct{ msg, mode, intent, strategy string }{
		{"me ajude a diagnosticar este erro no cluster", "diagnostic", "diagnose", "hypothesis-evidence-test"},
		{"crie um lab hands-on de HPA", "didactic", "practice", "guided-discovery"},
		{"compare Deployment e StatefulSet com trade-offs", "deep", "compare", "tradeoff-and-counterexample"},
		{"quero revisar o que errei", "didactic", "review", "retrieval-practice"},
	}
	for _, tc := range tests {
		p := OrchestrateTutorTurn("new-student", tc.msg, "CKA", tc.mode)
		if p.Intent != tc.intent || p.Strategy != tc.strategy {
			t.Errorf("%q: %+v", tc.msg, p)
		}
		if len(p.Phases) != 5 || p.MaxToolCalls != 3 || p.MaxRevisions != 1 {
			t.Errorf("limites invalidos: %+v", p)
		}
	}
}

func TestOrchestratorUsesLearnerMemory(t *testing.T) {
	p := resetProfile(t, "orchestrator-advanced")
	p.mu.Lock()
	p.Skills["CKA|Pods"] = &TopicSkill{Cert: "CKA", Topic: "Pods", Score: .9, Attempts: 5}
	p.Skills["CKA|Services"] = &TopicSkill{Cert: "CKA", Topic: "Services", Score: .9, Attempts: 5}
	p.Skills["CKA|Workloads"] = &TopicSkill{Cert: "CKA", Topic: "Workloads", Score: .9, Attempts: 5}
	p.mu.Unlock()
	plan := OrchestrateTutorTurn("orchestrator-advanced", "explique arquitetura do scheduler", "CKA", "didactic")
	if plan.LearnerLevel != "advanced" || plan.Strategy != "tradeoff-and-counterexample" {
		t.Fatalf("memoria nao alterou estrategia: %+v", plan)
	}
}

func TestOrchestrationPromptContextDoesNotExposeSecrets(t *testing.T) {
	p := OrchestrateTutorTurn("alice", "explique HPA", "CKA", "didactic")
	ctx := p.PromptContext()
	if !strings.Contains(ctx, "PLANO PEDAGOGICO") || strings.Contains(ctx, "alice") {
		t.Fatalf("contexto inesperado: %s", ctx)
	}
}
