package tutor

type GroundingRegressionCase struct {
	Name           string `json:"name"`
	Prompt         string `json:"prompt"`
	WantAnswerable bool   `json:"want_answerable"`
	WantSources    bool   `json:"want_sources"`
}

// These fixtures are intentionally versioned with the code. Real weak prompts
// are promoted here after review, so a production incident cannot silently drop
// out of the quality dataset as it ages.
var groundingRegressionFixtures = []GroundingRegressionCase{
	{Name: "HPA official grounding", Prompt: "Como funciona o HPA no Kubernetes?", WantAnswerable: true, WantSources: true},
	{Name: "Unknown technical request refuses", Prompt: "No Kubernetes, qual e a configuracao secreta do produto XZ-999?", WantAnswerable: false, WantSources: false},
	{Name: "Volatile request requires live source", Prompt: "Qual e a versao atual recomendada do Kubernetes hoje?", WantAnswerable: false, WantSources: false},
}

func RunGroundingRegressionEval() []GoldenEvalCaseResult {
	out := make([]GoldenEvalCaseResult, 0, len(groundingRegressionFixtures))
	for _, fixture := range groundingRegressionFixtures {
		report := CheckAnswerability(fixture.Prompt, "CKA")
		result := GoldenEvalCaseResult{Name: "Grounding: " + fixture.Name, Prompt: fixture.Prompt, Cert: report.Cert, Topic: report.Topic}
		result.Checks = append(result.Checks, "prompt anti-alucinacao construido")
		answerOK := report.Answerable == fixture.WantAnswerable
		sources := report.VerifiedSources()
		sourcesOK := !fixture.WantSources || len(sources) > 0
		if answerOK {
			result.Checks = append(result.Checks, "decisao de resposta correta")
		} else {
			result.Warnings = append(result.Warnings, "decisao de resposta divergente")
		}
		if sourcesOK {
			result.Checks = append(result.Checks, "fontes verificadas quando exigidas")
		} else {
			result.Warnings = append(result.Warnings, "faltaram fontes verificadas")
		}
		result.Passed = answerOK && sourcesOK
		if result.Passed {
			result.Score = 100
		} else {
			result.Score = 50
		}
		result.Sources = sources
		out = append(out, result)
	}
	return out
}
