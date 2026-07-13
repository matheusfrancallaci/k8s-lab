package tutor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func AnalyzeImageAttachment(ctx context.Context, dataURL, mime string) (string, error) {
	if !strings.HasPrefix(mime, "image/") || !strings.HasPrefix(dataURL, "data:image/") {
		return "", fmt.Errorf("anexo nao e uma imagem valida")
	}
	if len(dataURL) > 2_800_000 {
		return "", fmt.Errorf("imagem excede 2 MB")
	}
	c, ok := remoteLLM()
	if !ok {
		return "", fmt.Errorf("modelo vision remoto nao configurado")
	}
	model := strings.TrimSpace(os.Getenv("LLM_VISION_MODEL"))
	if model == "" {
		model = c.Model
	}
	body := map[string]any{"model": model, "max_completion_tokens": 600, "messages": []any{map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "text", "text": "Descreva somente evidencias tecnicas visiveis nesta imagem: mensagens de erro, recursos, estados, diagrama e texto. Nao invente partes ilegíveis. Responda em portugues."},
		map[string]any{"type": "image_url", "image_url": map[string]string{"url": dataURL}},
	}}}}
	b, _ := json.Marshal(body)
	reqCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := sharedLLMHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("vision HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.NewDecoder(resp.Body).Decode(&out) != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("resposta vision vazia")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
