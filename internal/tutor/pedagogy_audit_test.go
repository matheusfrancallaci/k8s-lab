package tutor

import "testing"

func TestPedagogyAuditRewardsDiagnosticMethod(t *testing.T) {
	plan := TutorOrchestration{Intent: "diagnose", Strategy: "hypothesis-evidence-test"}
	reply := "Sintoma: os Pods estao Pending. Hipotese: faltam recursos. Verifique os eventos e teste a capacidade do node. O que voce encontrou? Em seguida, compare requests e allocatable."
	a := AuditTeachingResponse(reply, plan)
	if a.Score < 85 || !a.HasCheckpoint || !a.HasNextStep || a.SpoilerRisk {
		t.Fatalf("auditoria pedagogica inesperada: %+v", a)
	}
}

func TestPedagogyAuditFlagsPracticeSpoiler(t *testing.T) {
	plan := TutorOrchestration{Intent: "practice", Strategy: "guided-discovery"}
	a := AuditTeachingResponse("Aqui esta a solucao completa: copie e cole este comando como resposta final.", plan)
	if !a.SpoilerRisk || a.Score >= 80 {
		t.Fatalf("spoiler nao penalizado: %+v", a)
	}
}
