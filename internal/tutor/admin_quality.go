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
	Telemetry          TutorTelemetryReport  `json:"telemetry"`
	Feedback           FeedbackSummary       `json:"feedback"`
	GroundingScore     int                   `json:"grounding_score"`
	SourceCoverage     int                   `json:"source_coverage"`
	RefusalCorrectness int                   `json:"refusal_correctness"`
	RegressionScore    int                   `json:"historical_regression_score"`
	Topics             []string              `json:"topics"`
	Recommendations    []string              `json:"recommendations"`
	DeploymentBlockers []string              `json:"deployment_blockers"`
}

func BuildAdminQualityReport() AdminQualityReport {
	golden := RunGoldenEval()
	quality := PromptQualityReport()
	obs := LabObservability()
	rep := AdminQualityReport{
		GeneratedAt:        time.Now().UTC().Format(time.RFC3339),
		GoldenScore:        golden.Score,
		GoldenPassed:       golden.Passed,
		GoldenTotal:        golden.Total,
		PromptQuality:      quality,
		LabObservability:   obs,
		RAG:                RAGStatus(),
		Telemetry:          TutorTelemetry(),
		Feedback:           TutorFeedbackSummary(),
		GroundingScore:     golden.GroundingScore,
		SourceCoverage:     golden.SourceCoverage,
		RefusalCorrectness: golden.RefusalCorrectness,
		RegressionScore:    golden.RegressionScore,
		Topics:             Topics(),
	}
	if golden.Score < 80 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "golden eval abaixo de 80")
	}
	if golden.GroundingTotal > 0 && golden.GroundingScore < 80 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "eval de grounding abaixo de 80")
	}
	if golden.SourceCoverage < 80 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "cobertura de fonte oficial abaixo de 80")
	}
	if golden.RefusalCorrectness < 80 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "recusa correta abaixo de 80")
	}
	if metric := rep.Telemetry.Stages["llm.contract.lab-spec"]; metric.Count > 5 && metric.Failures*100/metric.Count > 5 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "taxa de JSON invalido de lab-spec acima de 5%")
	}
	if metric := rep.Telemetry.Stages["llm.stream"]; metric.Count > 5 && metric.P95MS > 15000 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "latencia p95 do streaming acima de 15s")
	}
	if golden.RegressionTotal > 0 && golden.RegressionScore < 75 {
		rep.DeploymentBlockers = append(rep.DeploymentBlockers, "reexecucao de prompts historicos abaixo de 75")
	}
	if obs.Attempts >= 5 && obs.SuccessRate < .60 {
		rep.Recommendations = append(rep.Recommendations, "investigar labs com baixa taxa de conclusao; desempenho do aluno nao bloqueia deploy")
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
		"telemetria de latencia carregada",
		"eval de respostas grounded executado",
	)
	if admin.GoldenScore >= 80 {
		rep.Checks = append(rep.Checks, "golden eval >= 80")
	}
	if admin.PromptQuality.Total == 0 || admin.PromptQuality.AvgRegression >= 75 {
		rep.Checks = append(rep.Checks, "prompt regression gate aprovado")
	}
	if admin.GroundingScore >= 80 {
		rep.Checks = append(rep.Checks, "grounding/refusal gate aprovado")
	}
	return rep
}
