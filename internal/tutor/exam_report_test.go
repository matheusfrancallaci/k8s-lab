package tutor

import "testing"

func TestBuildExamReportWeightsByOfficialDomains(t *testing.T) {
	answers := []ExamAnswer{
		{Topic: "Troubleshooting", OK: true},
		{Topic: "Troubleshooting", OK: true},
		{Topic: "Autoscaling", OK: false}, // casa com Workloads via ponte hpa/autoscal
		{Topic: "Storage", OK: true},
	}
	rep := BuildExamReport("CKA", answers)

	if rep.Cert != "CKA" || rep.PassCut != 66 {
		t.Fatalf("cert/corte inesperados: %+v", rep)
	}
	if rep.Total != 4 || rep.OK != 3 || rep.RawPct != 75 {
		t.Fatalf("contagem bruta inesperada: %+v", rep)
	}
	if len(rep.Domains) == 0 {
		t.Fatalf("domínios cobertos deveriam aparecer: %+v", rep)
	}
	for _, d := range rep.Domains {
		if d.Weight == 0 {
			t.Fatalf("domínio oficial sem peso: %+v", d)
		}
	}
	if rep.CoveredWeightPct <= 0 || rep.CoveredWeightPct > 100 {
		t.Fatalf("cobertura de peso fora do intervalo: %+v", rep)
	}
	if len(rep.Unseen) == 0 {
		t.Fatalf("um simulado de 4 questões não cobre a CKA inteira — deveria listar domínios não vistos")
	}
	if rep.WeightedPct < 0 || rep.WeightedPct > 100 {
		t.Fatalf("projeção ponderada fora do intervalo: %+v", rep)
	}
}

func TestBuildExamReportPassProjection(t *testing.T) {
	all := []ExamAnswer{
		{Topic: "Troubleshooting", OK: true},
		{Topic: "Storage", OK: true},
		{Topic: "Autoscaling", OK: true},
	}
	rep := BuildExamReport("CKA", all)
	if rep.WeightedPct != 100 || !rep.Passed {
		t.Fatalf("100%% de acerto deveria projetar aprovação: %+v", rep)
	}

	none := []ExamAnswer{
		{Topic: "Troubleshooting", OK: false},
		{Topic: "Storage", OK: false},
	}
	rep = BuildExamReport("CKA", none)
	if rep.WeightedPct != 0 || rep.Passed {
		t.Fatalf("0%% de acerto não pode projetar aprovação: %+v", rep)
	}
}

func TestBuildExamReportWithoutCurriculumFallsBackToRaw(t *testing.T) {
	answers := []ExamAnswer{
		{Topic: "Tópico Inédito", Cert: "MinhaCert", OK: true},
		{Topic: "Outro Tópico", Cert: "MinhaCert", OK: false},
	}
	rep := BuildExamReport("", answers)
	if rep.Cert != "MinhaCert" {
		t.Fatalf("cert deveria vir da maioria das respostas: %+v", rep)
	}
	if rep.WeightedPct != rep.RawPct || rep.RawPct != 50 {
		t.Fatalf("sem currículo a projeção degrada para o bruto: %+v", rep)
	}
	if len(rep.Extra) != 2 {
		t.Fatalf("tópicos fora do currículo deveriam aparecer como extra: %+v", rep.Extra)
	}
}
