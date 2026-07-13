package tutor

import "testing"

func TestStructuredIntentDecisionAvoidsSubstringCollisions(t *testing.T) {
	d := ClassifyTutorRequest("Criar 7 labs de pods estaticos nivel 2", "CKA")
	if d.Intent != "create_lab" || d.Topic != "Static Pods" || d.Count != 7 || d.Level != 2 || d.Confidence < 90 {
		t.Fatalf("decisao estruturada incorreta: %+v", d)
	}
	if got := ClassifyTutorRequest("mostre minhas estatisticas", "CKA"); got.Intent != "progress" {
		t.Fatalf("estatisticas deveria ser progresso: %+v", got)
	}
}
