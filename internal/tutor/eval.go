package tutor

import (
	"strings"

	"estudo-app/internal/models"
)

type GoldenEvalReport struct {
	Total              int                    `json:"total"`
	Passed             int                    `json:"passed"`
	Score              int                    `json:"score"`
	RegressionTotal    int                    `json:"regression_total"`
	RegressionPassed   int                    `json:"regression_passed"`
	RegressionScore    int                    `json:"regression_score"`
	Cases              []GoldenEvalCaseResult `json:"cases"`
	RegressionCases    []GoldenEvalCaseResult `json:"regression_cases,omitempty"`
	Quality            PromptQualitySummary   `json:"quality"`
	GroundingTotal     int                    `json:"grounding_total"`
	GroundingPassed    int                    `json:"grounding_passed"`
	GroundingScore     int                    `json:"grounding_score"`
	SourceCoverage     int                    `json:"source_coverage"`
	RefusalCorrectness int                    `json:"refusal_correctness"`
	GroundingCases     []GoldenEvalCaseResult `json:"grounding_cases,omitempty"`
}

type GoldenEvalCaseResult struct {
	Name         string   `json:"name"`
	Prompt       string   `json:"prompt"`
	Cert         string   `json:"cert"`
	Topic        string   `json:"topic"`
	LabID        string   `json:"lab_id,omitempty"`
	Quality      int      `json:"quality,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Sources      []string `json:"sources,omitempty"`
	Chunks       []string `json:"chunks,omitempty"`
	Checks       []string `json:"checks"`
	Warnings     []string `json:"warnings,omitempty"`
	Passed       bool     `json:"passed"`
	Score        int      `json:"score"`
}

type goldenPrompt struct {
	Name       string
	Prompt     string
	ActiveCert string
	WantCert   string
	WantTopic  string
	WantTerms  []string
	WantDeps   []string
	Level      int
}

var goldenPrompts = []goldenPrompt{
	{
		Name:       "CKA HPA",
		Prompt:     "criar questao da CKA de HPA nivel 3",
		ActiveCert: "CKA",
		WantCert:   "CKA",
		WantTopic:  "Autoscaling",
		WantTerms:  []string{"hpa", "metrics-server"},
		WantDeps:   []string{"metrics-server"},
		Level:      3,
	},
	{
		Name:       "AWS SQS",
		Prompt:     "crie um lab de AWS para SQS",
		ActiveCert: "CKA",
		WantCert:   "AWS",
		WantTopic:  "AWS Messaging",
		WantTerms:  []string{"sqs", "localstack", "awslocal"},
		WantDeps:   []string{"localstack"},
		Level:      2,
	},
	{
		Name:       "CAPA ArgoCD Sync",
		Prompt:     "crie um lab da CAPA sobre ArgoCD sync",
		ActiveCert: "CAPA",
		WantCert:   "CAPA",
		WantTopic:  "GitOps",
		WantTerms:  []string{"argocd", "application", "sync"},
		WantDeps:   []string{"argocd"},
		Level:      2,
	},
	{
		Name:       "Terraform Variables Outputs",
		Prompt:     "crie um lab Terraform de variaveis e outputs",
		ActiveCert: "Terraform",
		WantCert:   "Terraform",
		WantTopic:  "Linguagem: recursos, variaveis, outputs",
		WantTerms:  []string{"terraform", "variable", "output"},
		Level:      2,
	},
}

func RunGoldenEval() GoldenEvalReport {
	report := GoldenEvalReport{Total: len(goldenPrompts)}
	for _, g := range goldenPrompts {
		c := runGoldenPrompt(g)
		if c.Passed {
			report.Passed++
		}
		report.Cases = append(report.Cases, c)
	}
	if report.Total > 0 {
		report.Score = int(float64(report.Passed) / float64(report.Total) * 100)
	}
	report.Quality = PromptQualityReport()
	for _, h := range HistoricalRegressionPrompts(8) {
		c := runHistoricalRegressionPrompt(h)
		report.RegressionTotal++
		if c.Passed {
			report.RegressionPassed++
		}
		report.RegressionCases = append(report.RegressionCases, c)
	}
	if report.RegressionTotal > 0 {
		report.RegressionScore = int(float64(report.RegressionPassed) / float64(report.RegressionTotal) * 100)
	}
	report.GroundingCases = RunGroundingRegressionEval()
	report.GroundingTotal = len(report.GroundingCases)
	sourceRequired, sourcePassed, refusalRequired, refusalPassed := 0, 0, 0, 0
	for i, c := range report.GroundingCases {
		if c.Passed {
			report.GroundingPassed++
		}
		fixture := groundingRegressionFixtures[i]
		if fixture.WantSources {
			sourceRequired++
			if len(c.Sources) > 0 {
				sourcePassed++
			}
		}
		if !fixture.WantAnswerable {
			refusalRequired++
			if c.Passed {
				refusalPassed++
			}
		}
	}
	if report.GroundingTotal > 0 {
		report.GroundingScore = report.GroundingPassed * 100 / report.GroundingTotal
	}
	if sourceRequired > 0 {
		report.SourceCoverage = sourcePassed * 100 / sourceRequired
	} else {
		report.SourceCoverage = 100
	}
	if refusalRequired > 0 {
		report.RefusalCorrectness = refusalPassed * 100 / refusalRequired
	} else {
		report.RefusalCorrectness = 100
	}
	return report
}

func runHistoricalRegressionPrompt(h PromptQualityCase) GoldenEvalCaseResult {
	res := GoldenEvalCaseResult{Name: "Historico: " + compactText(h.Prompt, 44), Prompt: h.Prompt}
	totalChecks, okChecks := 0, 0
	check := func(ok bool, msg string) {
		totalChecks++
		if ok {
			okChecks++
			res.Checks = append(res.Checks, "ok: "+msg)
			return
		}
		res.Warnings = append(res.Warnings, "falhou: "+msg)
	}

	if strings.EqualFold(h.Cert, "Terraform") {
		runTerraformGolden(goldenPrompt{
			Name:       res.Name,
			Prompt:     h.Prompt,
			ActiveCert: h.ActiveCert,
			WantCert:   "Terraform",
			WantTopic:  h.Topic,
			Level:      2,
		}, &res, check)
		return finishGoldenResult(res, totalChecks, okChecks)
	}

	cert := routeCertForLabRequest(h.ActiveCert, h.Prompt, h.Topic)
	topic := exactTopicForRequest(cert, h.Prompt)
	if topic == "" {
		topic = detectTopic(h.Prompt)
	}
	if topic == "" {
		fallback := fallbackTopicsForCert(cert, h.Prompt)
		if len(fallback) > 0 {
			topic = fallback[0]
		}
	}
	res.Cert, res.Topic = cert, topic
	check(strings.EqualFold(cert, h.Cert), "certificacao historica permanece "+h.Cert)
	check(strings.EqualFold(topic, h.Topic), "topico historico permanece "+h.Topic)
	if _, ok := templates[topic]; !ok {
		check(false, "topico historico suportado pelo gerador")
		return finishGoldenResult(res, totalChecks, okChecks)
	}

	qs := generateQuestions(topic, cert, 2, 1)
	check(len(qs) == 1, "gerador reexecuta prompt historico")
	if len(qs) == 0 {
		return finishGoldenResult(res, totalChecks, okChecks)
	}
	q := FinalizeLab(qs[0], h.Prompt)
	res.LabID = q.ID
	res.Quality = labQualityScore(q)
	res.Dependencies = labDependencyNames(q)
	res.Sources = labSourceURLs(q)
	res.Chunks = labChunkTitles(q)

	baseline := h.LastQuality
	if baseline == 0 {
		baseline = int(h.AvgQuality + 0.5)
	}
	check(LabQualityGate(q) == nil, "quality gate ainda aprovado")
	check(res.Quality >= minimumLabQuality, "score minimo do Lab Agent")
	if baseline > 0 {
		check(res.Quality+10 >= baseline, "score nao regrediu mais de 10 pontos")
	}
	for _, dep := range limitedStrings(h.Dependencies, 3) {
		check(containsFold(res.Dependencies, dep), "dependencia historica "+dep+" preservada")
	}
	return finishGoldenResult(res, totalChecks, okChecks)
}

func runGoldenPrompt(g goldenPrompt) GoldenEvalCaseResult {
	res := GoldenEvalCaseResult{Name: g.Name, Prompt: g.Prompt}
	totalChecks, okChecks := 0, 0
	check := func(ok bool, msg string) {
		totalChecks++
		if ok {
			okChecks++
			res.Checks = append(res.Checks, "ok: "+msg)
			return
		}
		res.Warnings = append(res.Warnings, "falhou: "+msg)
	}

	cert := inferCertFromMessage(g.Prompt, g.ActiveCert)
	if isAWSFocus(cert, g.Prompt) {
		cert = "AWS"
	}
	if strings.EqualFold(g.WantCert, "Terraform") {
		runTerraformGolden(g, &res, check)
		return finishGoldenResult(res, totalChecks, okChecks)
	}
	topic := exactTopicForRequest(cert, g.Prompt)
	if topic == "" {
		topic = detectTopic(g.Prompt)
	}
	if topic == "" {
		fallback := fallbackTopicsForCert(cert, g.Prompt)
		if len(fallback) > 0 {
			topic = fallback[0]
		}
	}

	res.Cert, res.Topic = cert, topic
	check(strings.EqualFold(cert, g.WantCert), "certificacao roteada para "+g.WantCert)
	check(strings.EqualFold(topic, g.WantTopic), "topico exato "+g.WantTopic)

	qs := generateQuestions(topic, cert, g.Level, 1)
	check(len(qs) == 1, "gerador retornou um lab")
	if len(qs) == 0 {
		return finishGoldenResult(res, totalChecks, okChecks)
	}
	q := FinalizeLab(qs[0], g.Prompt)
	res.LabID = q.ID
	res.Quality = labQualityScore(q)
	res.Dependencies = labDependencyNames(q)
	res.Sources = labSourceURLs(q)
	res.Chunks = labChunkTitles(q)

	check(q.Type == models.Lab, "resultado e lab hands-on")
	check(len(q.Goals) > 0 || q.Validation != nil, "lab tem validador automatico")
	check(LabQualityGate(q) == nil, "quality gate aprovado")
	check(res.Quality >= minimumLabQuality, "score minimo do Lab Agent")
	check(len(res.Sources) > 0, "fontes oficiais anexadas")
	check(len(res.Chunks) > 0, "chunks RAG anexados")
	for _, dep := range g.WantDeps {
		check(containsFold(res.Dependencies, dep), "dependencia "+dep+" declarada")
	}
	text := strings.ToLower(q.Question + " " + q.AnswerCommand + " " + q.Explanation + " " + strings.Join(res.Dependencies, " "))
	for _, term := range g.WantTerms {
		check(strings.Contains(text, strings.ToLower(term)), "termo essencial "+term)
	}
	return finishGoldenResult(res, totalChecks, okChecks)
}

func runTerraformGolden(g goldenPrompt, res *GoldenEvalCaseResult, check func(bool, string)) {
	fam := familyForMessage(g.Prompt, g.ActiveCert)
	res.Cert = fam.cert
	res.Topic = g.WantTopic
	check(strings.EqualFold(fam.cert, g.WantCert), "familia autonoma roteada para Terraform")
	check(fam.name == "terraform", "familia terraform selecionada")
	cur, ok := CurriculumFor("Terraform")
	check(ok && len(cur) > 0, "curriculo oficial Terraform carregado")
	hits := RAGSearch("Terraform", g.Prompt, 2, false)
	check(len(hits) > 0, "RAG recupera chunks Terraform")
	if len(hits) > 0 {
		for _, h := range hits {
			res.Chunks = append(res.Chunks, h.Chunk.Title)
			if h.Chunk.URL != "" {
				res.Sources = append(res.Sources, h.Chunk.URL)
			}
		}
	}
	hcl := `variable "nome" { type = string }
output "nome" { value = var.nome }`
	check(safeHCL(hcl), "guardrail aceita HCL seguro de variaveis/outputs")
	check(safeValidation(`terraform -chdir="$TFDIR" output -raw nome 2>/dev/null | grep -q . && echo OK || echo FAIL`), "validacao Terraform segura")
}

func finishGoldenResult(res GoldenEvalCaseResult, total, ok int) GoldenEvalCaseResult {
	if total > 0 {
		res.Score = int(float64(ok) / float64(total) * 100)
	}
	res.Passed = total > 0 && ok == total
	return res
}

func labQualityScore(q models.Question) int {
	if q.LabSpec == nil {
		return 0
	}
	return q.LabSpec.Quality.Score
}

func labDependencyNames(q models.Question) []string {
	if q.LabSpec == nil {
		return nil
	}
	var out []string
	for _, d := range q.LabSpec.Dependencies {
		if d.Name != "" {
			out = append(out, strings.ToLower(d.Name))
		}
	}
	return out
}

func labSourceURLs(q models.Question) []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	add(q.DocURL)
	if q.LabSpec != nil {
		for _, s := range q.LabSpec.Sources {
			add(s.URL)
		}
	}
	return out
}

func labChunkTitles(q models.Question) []string {
	if q.LabSpec == nil {
		return nil
	}
	var out []string
	for _, c := range q.LabSpec.Chunks {
		label := c.Title
		if label == "" {
			label = c.Domain
		}
		if label != "" {
			out = append(out, label)
		}
	}
	return out
}

func containsFold(items []string, want string) bool {
	want = strings.ToLower(want)
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), want) {
			return true
		}
	}
	return false
}
