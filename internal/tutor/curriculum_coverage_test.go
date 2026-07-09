package tutor

import (
	"strings"
	"testing"

	"estudo-app/internal/models"
)

func TestCurriculumCoverageCountsAndGaps(t *testing.T) {
	qs := []models.Question{
		{Cert: "CKA", Topic: "Workloads", Type: models.Lab, Source: models.SourceCurated},
		{Cert: "CKA", Topic: "Workloads", Type: models.MultipleChoice, Source: models.SourceCurated},
		{Cert: "CKA", Topic: "Services", Type: models.MultipleChoice, Source: models.SourceGenerated},
		{Cert: "CKAD", Topic: "Workloads", Type: models.Lab, Source: models.SourceCurated}, // outra cert: fora
	}
	rep, ok := CurriculumCoverage("CKA", qs)
	if !ok {
		t.Fatal("CKA tem curriculo embutido, coverage deveria existir")
	}
	var workloads, services *DomainCoverage
	for i := range rep.Domains {
		d := strings.ToLower(rep.Domains[i].Domain)
		if strings.Contains(d, "workload") {
			workloads = &rep.Domains[i]
		}
		if strings.Contains(d, "services") || strings.Contains(d, "networking") {
			services = &rep.Domains[i]
		}
	}
	if workloads == nil || workloads.Curated != 2 || workloads.Labs != 1 {
		t.Fatalf("Workloads deveria ter 2 curadas (1 lab): %+v", workloads)
	}
	if services == nil || services.Curated != 0 || services.Generated == 0 {
		t.Fatalf("Services deveria aparecer como gap com 1 gerada: %+v", services)
	}
	found := false
	for _, g := range rep.GapDomains {
		if strings.Contains(strings.ToLower(g), "services") || strings.Contains(strings.ToLower(g), "networking") {
			found = true
		}
	}
	if !found {
		t.Fatalf("dominio sem curado deveria estar em GapDomains: %v", rep.GapDomains)
	}
	if rep.CuratedPct <= 0 || rep.CuratedPct >= 100 {
		t.Fatalf("cobertura parcial deveria ficar entre 0 e 100, veio %d", rep.CuratedPct)
	}
	if _, ok := CurriculumCoverage("cert-inexistente", qs); ok {
		t.Fatal("cert sem curriculo nao deveria produzir relatorio")
	}
}
