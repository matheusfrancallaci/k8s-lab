package tutor

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"estudo-app/internal/models"
)

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// ─────────────────────────────────────────────────────────────────────────────
// LLM local (Ollama) — enhancer OPCIONAL, custo zero.
// Sem Ollama rodando, tudo cai nos caminhos heurísticos normalmente.
// Config: OLLAMA_URL (default http://localhost:11434), OLLAMA_MODEL (auto).
// ─────────────────────────────────────────────────────────────────────────────

func ollamaURL() string {
	return envOr("OLLAMA_URL", "http://localhost:11434")
}

// modelos preferidos, em ordem, quando OLLAMA_MODEL não é definido
var preferredModels = []string{"llama3.2", "qwen2.5", "gemma2", "mistral", "phi3"}

var (
	llmMu        sync.Mutex
	llmChecked   time.Time
	llmAvailable bool
	llmModel     string
)

// LLMStatus informa disponibilidade e modelo ativo (cache de 30s).
func LLMStatus() (bool, string) {
	llmMu.Lock()
	defer llmMu.Unlock()
	if time.Since(llmChecked) < 30*time.Second {
		return llmAvailable, llmModel
	}
	llmChecked = time.Now()
	llmAvailable, llmModel = probeOllama()
	return llmAvailable, llmModel
}

func probeOllama() (bool, string) {
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(ollamaURL() + "/api/tags")
	if err != nil {
		return false, ""
	}
	defer resp.Body.Close()
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || len(body.Models) == 0 {
		return false, ""
	}
	if m := envOr("OLLAMA_MODEL", ""); m != "" {
		return true, m
	}
	// escolhe o primeiro instalado que case com a lista de preferência
	for _, pref := range preferredModels {
		for _, m := range body.Models {
			if strings.HasPrefix(m.Name, pref) {
				return true, m.Name
			}
		}
	}
	return true, body.Models[0].Name
}

// llmGenerate chama o Ollama de forma síncrona (stream=false).
func llmGenerate(prompt string, wantJSON bool, timeout time.Duration) (string, error) {
	ok, model := LLMStatus()
	if !ok {
		return "", fmt.Errorf("ollama indisponível")
	}
	payload := map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": false,
		"options": map[string]any{
			"temperature": 0.3,
			"num_predict": 1200,
		},
	}
	if wantJSON {
		payload["format"] = "json"
	}
	b, _ := json.Marshal(payload)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(ollamaURL()+"/api/generate", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Response), nil
}

// llmStreamGenerate executa streaming incremental do Ollama quando disponível.
func llmStreamGenerate(prompt string, wantJSON bool, timeout time.Duration, onChunk func(string)) error {
	ok, model := LLMStatus()
	if !ok {
		return fmt.Errorf("ollama indisponível")
	}
	payload := map[string]any{
		"model":  model,
		"prompt": prompt,
		"stream": true,
		"options": map[string]any{
			"temperature": 0.3,
			"num_predict": 1200,
		},
	}
	if wantJSON {
		payload["format"] = "json"
	}
	b, _ := json.Marshal(payload)

	client := &http.Client{Timeout: timeout}
	resp, err := client.Post(ollamaURL()+"/api/generate", "application/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var out strings.Builder
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var evt struct {
			Response string `json:"response"`
		}
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Response == "" {
			continue
		}
		out.WriteString(evt.Response)
		if onChunk != nil {
			onChunk(evt.Response)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
}

// StreamLLMReply responde conversa livre com streaming incremental para a UI.
func StreamLLMReply(msg string, onChunk func(string)) error {
	if len(msg) > 1500 {
		msg = msg[:1500]
	}
	prompt := fmt.Sprintf(`Você é o Tutor do k8s-lab: um mentor especialista em Kubernetes, infraestrutura, cloud e programação. Responda em português do Brasil, direto e didático, em NO MÁXIMO 6 frases.

REGRA ABSOLUTA: só responda sobre Kubernetes, containers, cloud (Azure/AWS/GCP), Linux, redes, DevOps e programação. Se a pergunta fugir desses temas, recuse educadamente em 1 frase e sugira voltar aos estudos.

Pergunta do aluno: %s`, strings.TrimSpace(msg))
	return llmStreamGenerate(prompt, false, 60*time.Second, onChunk)
}

// ─────────────────────────────────────────────────────────────────────────────
// Uso 1 — quiz de verdade a partir da documentação (substitui o cloze quando
// disponível; validação estrita do JSON: qualquer defeito → descarta e o
// chamador completa com as heurísticas).
// ─────────────────────────────────────────────────────────────────────────────

func llmQuizFromDoc(text, cert, topic string, n int) []models.Question {
	if n <= 0 {
		return nil
	}
	// remove marcadores de fonte — são metadados nossos, não material de estudo
	for {
		i := strings.Index(text, srcMarker)
		if i < 0 {
			break
		}
		j := strings.Index(text[i:], "@@\n")
		if j < 0 {
			break
		}
		text = text[:i] + text[i+j+3:]
	}
	if len(text) > 6000 {
		text = text[:6000] // contexto de modelos pequenos
	}
	prompt := fmt.Sprintf(`Você é um elaborador de questões para a certificação Kubernetes %s.
A partir EXCLUSIVAMENTE do material abaixo, crie %d questões de múltipla escolha em português do Brasil.

Regras:
- Cada questão testa compreensão real do material (não decoreba literal).
- "options": exatamente 4 strings de texto completas e plausíveis (NUNCA números, NUNCA letras soltas).
- "answer": índice numérico (0-3) da alternativa correta.
- "explanation": por que a correta está certa (2-3 frases).

Exemplo do formato EXATO esperado:
{"questions":[{"question":"Qual componente decide em qual nó um pod novo será executado?","options":["kube-scheduler","kubelet","kube-proxy","etcd"],"answer":0,"explanation":"O kube-scheduler avalia os nós elegíveis e faz o bind do pod ao nó escolhido. O kubelet apenas executa o que já foi agendado."}]}

Responda SOMENTE com JSON válido nesse formato.

MATERIAL:
%s`, cert, n, text)

	raw, err := llmGenerate(prompt, true, 120*time.Second)
	if err != nil {
		log.Printf("[tutor/llm] geração falhou: %v", err)
		return nil
	}
	// Options como []any: um item malformado descarta SÓ aquela questão,
	// nunca o batch inteiro (modelos 3B erram formato com frequência).
	var parsed struct {
		Questions []struct {
			Question    string      `json:"question"`
			Options     []any       `json:"options"`
			Answer      json.Number `json:"answer"` // às vezes vem "0" como string
			Explanation string      `json:"explanation"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		log.Printf("[tutor/llm] JSON inválido do modelo: %v — resposta: %.200s", err, raw)
		return nil
	}

	var out []models.Question
	for _, q := range parsed.Questions {
		if len(out) >= n {
			break
		}
		ans64, aerr := q.Answer.Int64()
		opts := make([]string, 0, 4)
		for _, o := range q.Options {
			if s, ok := o.(string); ok && len(strings.TrimSpace(s)) > 1 {
				opts = append(opts, s)
			}
		}
		// validação estrita por questão
		if aerr != nil || len(opts) != 4 || ans64 < 0 || ans64 > 3 ||
			len(strings.TrimSpace(q.Question)) < 15 {
			log.Printf("[tutor/llm] questão descartada na validação (options=%d, answer=%s)", len(opts), q.Answer)
			continue
		}
		out = append(out, models.Question{
			ID:   newID(),
			Cert: models.Cert(cert), Topic: topic,
			Type: models.MultipleChoice, Difficulty: models.Mid,
			Question:    strings.TrimSpace(q.Question),
			Options:     opts,
			Answer:      int(ans64),
			Explanation: strings.TrimSpace(q.Explanation) + "\n\n(gerada pela IA local a partir da sua documentação)",
		})
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Uso 2 — explicar em tempo real por que um goal falhou (tutoria de verdade).
// ─────────────────────────────────────────────────────────────────────────────

// LLMExplainFailure gera uma explicação curta e didática do erro do usuário.
func LLMExplainFailure(questionText, goalDesc, valCmd, output string) (string, error) {
	if len(output) > 800 {
		output = output[:800]
	}
	prompt := fmt.Sprintf(`Você é um tutor de Kubernetes paciente. Um aluno está fazendo este exercício:

EXERCÍCIO: %s
OBJETIVO QUE FALHOU: %s
COMANDO DE VALIDAÇÃO: %s
SAÍDA ATUAL: %s

Em português do Brasil, explique em NO MÁXIMO 4 frases:
1. O que a saída indica sobre o estado atual do cluster.
2. Qual é a causa mais provável.
3. Um empurrão na direção certa (SEM dar o comando completo da resposta).

Seja direto e encorajador. Sem markdown, sem listas.`,
		strings.TrimSpace(questionText), goalDesc, valCmd, strings.TrimSpace(output))

	return llmGenerate(prompt, false, 45*time.Second)
}
