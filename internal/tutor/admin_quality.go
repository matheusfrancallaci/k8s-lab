package tutor

import "time"

type AdminQualityReport struct {
	GeneratedAt        string                `json:"generated_at"`
	GoldenScore        int                   `json:"golden_score"`
	GoldenPassed       int                   `json:"golden_passed"`
	GoldenTotal        int                   `json:"golden_total"`
	PromptQuality      PromptQualitySummary  `json:"prompt_quality"`
	LabObservability   LabObservationSummary `json:"lab_observability"`
	RAG                RAGStatusInfo         `json:"rag"`
	Topics             []string              `json:"topics"`
	Recommendations    []string              `json:"recommendations"`
	DeploymentBlockers []string              `json:"deployment_blockers"`
}

func BuildAdminQualityReport() AdminQualityReport {
	golden := RunGoldenEval()
	quality := PromptQualityReport()
	obs := LabObservability()
	rep := AdminQualityReport{
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		GoldenScore:      golden.Score,
		GoldenPassed:     golden.Passed,
		GoldenTotal:      golden.Total,
		PromptQuality:    quality,
		LabObservability: obs,
		RAG:              RAGStatus(),
		Topics:           Topics(),
	}
	if golden.Score < 80 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "golden eval abaixo de 80")
	}
	if quality.Total > 0 && quality.AvgRegression < 75 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "regressao media de prompts reais abaixo de 75")
	}
	if obs.Attempts > 0 && obs.SuccessRate < .60 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "taxa de sucesso dos validadores abaixo de 60%")
	}
	if rep.RAG.Chunks == 0 {
		rep.Recommendations = append(rep.Recommendations, "aquecer RAG com fontes oficiais antes de labs novos")
	}
	if quality.NeedsAttention > 0 {
		rep.Recommendations = append(rep.Recommendations, "converter prompts fracos em casos de regressao")
	}
	if len(rep.DeploymentBlockers) == 0 {
		rep.Recommendations = append(rep.Recommendations, "deploy gate verde para qualidade de tutor/labs")
	}
	return rep
}

type DeployGateReport struct {
	GeneratedAt string   `json:"generated_at"`
	Passed      bool     `json:"passed"`
	Checks      []string `json:"checks"`
	Blockers    []string `json:"blockers"`
}

func RunDeployGate() DeployGateReport {
	admin := BuildAdminQualityReport()
	rep := DeployGateReport{
		GeneratedAt: admin.GeneratedAt,
		Passed:      len(admin.DeploymentBlockers) == 0,
		Blockers:    append([]string{}, admin.DeploymentBlockers...),
	}
	rep.Checks = append(rep.Checks,
		"golden eval executado",
		"prompt-quality carregado",
		"observabilidade de labs carregada",
		"RAG status carregado",
	)
	if admin.GoldenScore >= 80 {
		rep.Checks = append(rep.Checks, "golden eval >= 80")
	}
	if admin.PromptQuality.Total == 0 || admin.PromptQuality.AvgRegression >= 75 {
		rep.Checks = append(rep.Checks, "prompt regression gate aprovado")
	}
	return rep
}
