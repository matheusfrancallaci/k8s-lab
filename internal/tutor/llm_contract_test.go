package tutor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLLMContractsRejectIncompleteOutput(t *testing.T) {
	if err := validateLLMContract("lab-spec", `{"question":"x"}`); err == nil {
		t.Fatal("contrato de lab deveria rejeitar resposta incompleta")
	}
	if err := validateLLMContract("topic-selection", `{"topics":["Workloads"],"reason":"evidencia"}`); err != nil {
		t.Fatalf("contrato de topicos valido rejeitado: %v", err)
	}
	if err := validateLLMContract("quiz", `{"questions":[{"question":"Qual objeto?","options":["Pod","Service"],"answer":0,"explanation":"Pod executa containers."}]}`); err != nil {
		t.Fatalf("contrato de quiz valido rejeitado: %v", err)
	}
}

func TestLLMContractsRejectWrongTypesAndInvalidAnswer(t *testing.T) {
	cases := []struct {
		contract string
		raw      string
	}{
		{"lab-spec", `{"question":7,"solution":"kubectl get pods","validation":"kubectl get pods","expected":"web","hint":"workloads","explanation":"confere o pod"}`},
		{"topic-selection", `{"topics":"Workloads","reason":"evidencia"}`},
		{"topic-selection", `{"topics":[""],"reason":"evidencia"}`},
		{"quiz", `{"questions":[{"question":"Qual?","options":["A","B"],"answer":2,"explanation":"fora do intervalo"}]}`},
		{"quiz", `{"questions":[{"question":"Qual?","options":["A"],"answer":0,"explanation":"poucas opcoes"}]}`},
	}
	for _, tc := range cases {
		if err := validateLLMContract(tc.contract, tc.raw); err == nil {
			t.Errorf("contrato %s deveria rejeitar %s", tc.contract, tc.raw)
		}
	}
}

func TestLLMContractsExposeNativeJSONSchemas(t *testing.T) {
	for _, contract := range []string{"lab-spec", "topic-selection", "quiz"} {
		schema := llmContractSchema(contract)
		encoded, err := json.Marshal(schema)
		if err != nil {
			t.Fatalf("schema %s nao serializa: %v", contract, err)
		}
		var obj map[string]any
		if err := json.Unmarshal(encoded, &obj); err != nil || obj["type"] != "object" {
			t.Fatalf("schema %s deveria ser objeto JSON Schema: %s", contract, encoded)
		}
	}
	if got := llmContractSchema("desconhecido"); got != "json" {
		t.Fatalf("contrato desconhecido deveria manter fallback json, veio %#v", got)
	}
}

func TestVerifiedSourcesOnlyAcceptTrustedURLs(t *testing.T) {
	report := AnswerabilityReport{Sources: []string{"https://kubernetes.io/docs/", "https://example.invalid/not-official"}}
	sources := report.VerifiedSources()
	if len(sources) != 1 || sources[0] != "https://kubernetes.io/docs/" {
		t.Fatalf("fontes verificadas inesperadas: %#v", sources)
	}
}

func TestUnknownProductIdentifierFailsClosed(t *testing.T) {
	report := CheckAnswerability("No Kubernetes, qual e a configuracao secreta do produto XZ-999?", "CKA")
	if report.Answerable {
		t.Fatalf("produto sem evidencia nao deveria ser respondido: %+v", report)
	}
	if !strings.Contains(report.Reason, "XZ-999") {
		t.Fatalf("recusa deveria explicar o identificador sem evidencia: %s", report.Reason)
	}
}
