package tutor

import "testing"

func TestLLMContractsRejectIncompleteOutput(t *testing.T) {
	if err := validateLLMContract("lab-spec", `{"question":"x"}`); err == nil {
		t.Fatal("contrato de lab deveria rejeitar resposta incompleta")
	}
	if err := validateLLMContract("topic-selection", `{"topics":["Workloads"],"reason":"evidencia"}`); err != nil {
		t.Fatalf("contrato de topicos valido rejeitado: %v", err)
	}
	if err := validateLLMContract("quiz", `{"questions":[{"question":"x"}]}`); err != nil {
		t.Fatalf("contrato de quiz valido rejeitado: %v", err)
	}
}

func TestVerifiedSourcesOnlyAcceptTrustedURLs(t *testing.T) {
	report := AnswerabilityReport{Sources: []string{"https://kubernetes.io/docs/", "https://example.invalid/not-official"}}
	sources := report.VerifiedSources()
	if len(sources) != 1 || sources[0] != "https://kubernetes.io/docs/" {
		t.Fatalf("fontes verificadas inesperadas: %#v", sources)
	}
}
