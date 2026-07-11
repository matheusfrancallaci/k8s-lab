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
	schema := llmContractSchema(contract)
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := llmGenerateFormatted(prompt, schema, timeout, maxTokens, model)
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

func llmContractSchema(contract string) any {
	stringField := map[string]any{"type": "string", "minLength": 1}
	schemas := map[string]any{
		"lab-spec": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"question", "solution", "validation", "expected", "hint", "explanation"},
			"properties": map[string]any{
				"question": stringField, "solution": stringField, "validation": stringField,
				"expected": stringField, "hint": stringField, "explanation": stringField,
			},
		},
		"topic-selection": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"topics", "reason"},
			"properties": map[string]any{
				"topics": map[string]any{"type": "array", "minItems": 1, "items": stringField},
				"reason": stringField,
			},
		},
		"curriculum": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"domains"},
			"properties": map[string]any{"domains": map[string]any{
				"type": "array", "items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"domain", "weight"},
					"properties": map[string]any{
						"domain": stringField,
						"weight": map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
					},
				},
			}},
		},
		"quiz": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"questions"},
			"properties": map[string]any{"questions": map[string]any{
				"type": "array", "minItems": 1, "items": map[string]any{
					"type": "object", "additionalProperties": false,
					"required": []string{"question", "options", "answer", "explanation"},
					"properties": map[string]any{
						"question":    stringField,
						"options":     map[string]any{"type": "array", "minItems": 2, "items": stringField},
						"answer":      map[string]any{"type": "integer", "minimum": 0},
						"explanation": stringField,
					},
				},
			}},
		},
	}
	if schema, ok := schemas[contract]; ok {
		return schema
	}
	return "json"
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
	if contract == "curriculum" {
		// domains vazio é resposta VÁLIDA ("o material não é guia de exame") —
		// a recusa explícita faz parte do contrato, como no grounding.
		if _, ok := obj["domains"].([]any); !ok {
			return fmt.Errorf("contrato curriculum requer campo domains (array)")
		}
		return nil
	}
	if contract == "quiz" {
		items, ok := obj["questions"].([]any)
		if !ok || len(items) == 0 {
			return fmt.Errorf("contrato quiz requer questions nao vazio")
		}
		for i, item := range items {
			q, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("contrato quiz: questions[%d] requer objeto", i)
			}
			if err := requireNonEmptyStrings(q, "question", "explanation"); err != nil {
				return fmt.Errorf("contrato quiz: questions[%d]: %w", i, err)
			}
			options, ok := q["options"].([]any)
			if !ok || len(options) < 2 {
				return fmt.Errorf("contrato quiz: questions[%d] requer ao menos 2 options", i)
			}
			for j, option := range options {
				if strings.TrimSpace(fmt.Sprint(option)) == "" {
					return fmt.Errorf("contrato quiz: questions[%d].options[%d] vazio", i, j)
				}
			}
			answer, ok := numberAsInt(q["answer"])
			if !ok || answer < 0 || answer >= len(options) {
				return fmt.Errorf("contrato quiz: questions[%d].answer fora do intervalo", i)
			}
		}
		return nil
	}
	for _, key := range required[contract] {
		v, ok := obj[key]
		if !ok {
			return fmt.Errorf("contrato %s sem campo %s", contract, key)
		}
		if key == "topics" {
			values, ok := v.([]any)
			if !ok || len(values) == 0 {
				return fmt.Errorf("contrato %s requer topics nao vazio", contract)
			}
			for i, topic := range values {
				if strings.TrimSpace(fmt.Sprint(topic)) == "" {
					return fmt.Errorf("contrato %s tem topics[%d] vazio", contract, i)
				}
			}
			continue
		}
		if _, ok := v.(string); !ok || strings.TrimSpace(fmt.Sprint(v)) == "" {
			return fmt.Errorf("contrato %s tem campo vazio: %s", contract, key)
		}
	}
	return nil
}

func requireNonEmptyStrings(obj map[string]any, keys ...string) error {
	for _, key := range keys {
		value, ok := obj[key].(string)
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("campo %s deve ser texto nao vazio", key)
		}
	}
	return nil
}

func numberAsInt(value any) (int, bool) {
	n, ok := value.(float64)
	if !ok || n != float64(int(n)) {
		return 0, false
	}
	return int(n), true
}
