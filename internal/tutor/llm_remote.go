package tutor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// remoteLLMConfig enables any OpenAI-compatible provider. It is opt-in so the
// existing Ollama and deterministic fallbacks keep working without credentials.
type remoteLLMConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

func remoteLLM() (remoteLLMConfig, bool) {
	c := remoteLLMConfig{
		BaseURL: strings.TrimRight(envOr("LLM_API_BASE", "https://api.openai.com/v1"), "/"),
		APIKey:  strings.TrimSpace(os.Getenv("LLM_API_KEY")),
		Model:   strings.TrimSpace(os.Getenv("LLM_MODEL")),
	}
	return c, c.APIKey != "" && c.Model != ""
}

func remoteModelFor(role, requested string) string {
	if requested != "" && !strings.Contains(requested, ":") {
		return requested
	}
	if m := strings.TrimSpace(os.Getenv("LLM_" + strings.ToUpper(role) + "_MODEL")); m != "" {
		return m
	}
	c, _ := remoteLLM()
	return c.Model
}

func remoteGenerate(prompt string, wantJSON bool, timeout time.Duration, maxTokens int, model string) (string, error) {
	started := time.Now()
	failed := true
	defer func() { recordTutorLatency("llm.remote.generate", time.Since(started), 0, failed) }()
	c, ok := remoteLLM()
	if !ok {
		return "", fmt.Errorf("provedor remoto nao configurado")
	}
	body := map[string]any{
		"model": remoteModelFor("gen", model), "messages": []map[string]string{{"role": "user", "content": prompt}},
		"max_completion_tokens": maxTokens,
	}
	if wantJSON {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("provedor remoto HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("resposta remota vazia")
	}
	failed = false
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

func remoteStream(prompt string, timeout time.Duration, maxTokens int, model string, onChunk func(string)) error {
	started := time.Now()
	failed := true
	defer func() { recordTutorLatency("llm.remote.stream", time.Since(started), 0, failed) }()
	c, ok := remoteLLM()
	if !ok {
		return fmt.Errorf("provedor remoto nao configurado")
	}
	body := map[string]any{"model": remoteModelFor("chat", model), "messages": []map[string]string{{"role": "user", "content": prompt}}, "max_completion_tokens": maxTokens, "stream": true}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("provedor remoto HTTP %d", resp.StatusCode)
	}
	s := bufio.NewScanner(resp.Body)
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for s.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(s.Text(), "data:"))
		if line == "" || line == "[DONE]" {
			continue
		}
		var event struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && len(event.Choices) > 0 && event.Choices[0].Delta.Content != "" && onChunk != nil {
			onChunk(event.Choices[0].Delta.Content)
		}
	}
	err = s.Err()
	failed = err != nil
	return err
}
