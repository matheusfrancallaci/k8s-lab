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

func TestCoverageBridgesTopicNamingVariants(t *testing.T) {
	// Regressão: conteúdo curado REAL ficava invisível para cobertura e
	// DomainMap por flexão/nome do topic ("Minimizing..." vs domínio
	// "Minimize...", "Sync Strategies" vs "Sync e Rollback", "Fundamentos"
	// vs "IaC & Fluxo Terraform").
	cases := []struct {
		cert, topic, domainFrag string
	}{
		{"CKS", "Minimizing Microservice Vulnerabilities", "microservice"},
		{"ArgoCD", "Sync Strategies", "sync"},
		{"Terraform", "Fundamentos", "fluxo"},
		{"Terraform", "Módulos", "módulos"},
		{"Terraform", "Providers", "providers"},
	}
	for _, c := range cases {
		rep, ok := CurriculumCoverage(c.cert, []models.Question{
			{Cert: models.Cert(c.cert), Topic: c.topic, Type: models.Lab, Source: models.SourceCurated},
		})
		if !ok {
			t.Fatalf("%s deveria ter curriculo", c.cert)
		}
		matched := false
		for _, d := range rep.Domains {
			if strings.Contains(strings.ToLower(d.Domain), c.domainFrag) && d.Curated > 0 {
				matched = true
			}
		}
		if !matched {
			t.Fatalf("topic %q (%s) deveria contar no dominio contendo %q: %+v", c.topic, c.cert, c.domainFrag, rep.Domains)
		}
	}
}
