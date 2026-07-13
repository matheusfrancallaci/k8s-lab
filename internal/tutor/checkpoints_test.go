package tutor

import "testing"

func resetCheckpointsForTest() {
	checkpoints.Lock()
	checkpoints.Values = map[string]TutorCheckpoint{}
	checkpoints.Unlock()
}

func TestCheckpointEvaluatesAndReleasesPractice(t *testing.T) {
	resetCheckpointsForTest()
	plan := TutorOrchestration{TurnID: "turn", Intent: "explain", TargetTopic: "Autoscaling"}
	cp, ok := RegisterTutorCheckpoint("alice", "conv", plan)
	if !ok || cp.Status != "awaiting" {
		t.Fatalf("checkpoint nao criado: %+v", cp)
	}
	ev, ok := EvaluateTutorCheckpoint("alice", "conv", "O HPA precisa de metricas, requests de CPU e um target configurado.")
	if !ok || ev.Outcome != "release" || ev.Score < 70 || ev.NextPrompt == "" {
		t.Fatalf("pratica nao liberada: %+v", ev)
	}
	if _, ok := ActiveTutorCheckpoint("alice", "conv"); ok {
		t.Fatal("checkpoint aprovado continuou pendente")
	}
}

func TestCheckpointRemediatesWeakAnswerAndStopsAfterThreeAttempts(t *testing.T) {
	resetCheckpointsForTest()
	plan := TutorOrchestration{TurnID: "turn", Intent: "diagnose", TargetTopic: "Incidente"}
	RegisterTutorCheckpoint("bob", "conv", plan)
	for i := 0; i < 2; i++ {
		ev, ok := EvaluateTutorCheckpoint("bob", "conv", "nao sei")
		if !ok || ev.Outcome != "remediate" {
			t.Fatalf("tentativa %d: %+v", i, ev)
		}
	}
	ev, _ := EvaluateTutorCheckpoint("bob", "conv", "ainda nao sei")
	if ev.Outcome != "remediate" {
		t.Fatalf("terceira tentativa deveria remediar: %+v", ev)
	}
	if _, ok := ActiveTutorCheckpoint("bob", "conv"); ok {
		t.Fatal("checkpoint deveria encerrar apos tres tentativas")
	}
}

func TestCheckpointIsIsolatedByConversation(t *testing.T) {
	resetCheckpointsForTest()
	RegisterTutorCheckpoint("alice", "conv-a", TutorOrchestration{TurnID: "1", Intent: "explain", TargetTopic: "Storage"})
	if _, ok := EvaluateTutorCheckpoint("alice", "conv-b", "PVC PV StorageClass"); ok {
		t.Fatal("checkpoint vazou entre conversas")
	}
}

func TestExplicitLabCommandSupersedesPendingCheckpoint(t *testing.T) {
	resetCheckpointsForTest()
	RegisterTutorCheckpoint("carol", "conv-lab", TutorOrchestration{TurnID: "1", Intent: "explain", TargetTopic: "Core Concepts"})
	if _, ok := EvaluateTutorCheckpoint("carol", "conv-lab", "Criar lab de pod estaticos"); ok {
		t.Fatal("ordem explicita deve substituir o checkpoint, nao ser avaliada como resposta")
	}
	if _, ok := ActiveTutorCheckpoint("carol", "conv-lab"); ok {
		t.Fatal("checkpoint substituido continuou interceptando mensagens")
	}
}
