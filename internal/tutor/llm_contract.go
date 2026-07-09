package tutor

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// llmGenerateContract keeps the model behind a small, explicit contract. Ollama's
// JSON mode is useful, but it is not a schema guarantee, so invalid output never
// reaches the rest of the tutor as trusted data.
func llmGenerateContract(prompt, contract string, timeout time.Duration, maxTokens int, model string) (string, error) {
	started := time.Now()
	failed := true
	defer func() { recordTutorLatency("llm.contract."+contract, time.Since(started), 0, failed) }()
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := llmGenerate(prompt, true, timeout, maxTokens, model)
		if err != nil {
			return "", err
		}
		if err := validateLLMContract(contract, raw); err == nil {
			failed = false
			return raw, nil
		}
		prompt += "\n\nA resposta anterior nao respeitou o contrato " + contract + ". Retorne apenas JSON valido, sem texto extra."
	}
	return "", fmt.Errorf("modelo nao respeitou o contrato estruturado %q", contract)
}

func validateLLMContract(contract, raw string) error {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return fmt.Errorf("JSON invalido: %w", err)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("contrato %s requer objeto JSON", contract)
	}
	required := map[string][]string{
		"lab-spec":        {"question", "solution", "validation", "expected", "hint", "explanation"},
		"topic-selection": {"topics", "reason"},
	}
	if contract == "quiz" {
		items, ok := obj["questions"].([]any)
		if !ok || len(items) == 0 {
			return fmt.Errorf("contrato quiz requer questions nao vazio")
		}
		return nil
	}
	for _, key := range required[contract] {
		v, ok := obj[key]
		if !ok {
			return fmt.Errorf("contrato %s sem campo %s", contract, key)
		}
		if key == "topics" {
			if values, ok := v.([]any); !ok || len(values) == 0 {
				return fmt.Errorf("contrato %s requer topics nao vazio", contract)
			}
			continue
		}
		if strings.TrimSpace(fmt.Sprint(v)) == "" {
			return fmt.Errorf("contrato %s tem campo vazio: %s", contract, key)
		}
	}
	return nil
}
