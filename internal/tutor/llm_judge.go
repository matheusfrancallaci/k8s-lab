package tutor

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Juiz amostral — um modelo FORTE (gateway remoto) audita uma amostra do
// conteúdo gerado pelo modelo local. Nunca está no caminho do aluno (async,
// best-effort) e nunca julga com o próprio modelo que gerou: sem gateway
// remoto configurado, não roda. Reprovação marca o lab como degradado no
// catálogo e vira sinal no painel admin — humano decide o descarte.
// ─────────────────────────────────────────────────────────────────────────────

var (
	judgePassed  atomic.Int64
	judgeFlagged atomic.Int64
)

// JudgeStats devolve aprovados/sinalizados pelo juiz amostral desde o boot.
func JudgeStats() (passed, flagged int64) {
	return judgePassed.Load(), judgeFlagged.Load()
}

// judgeSamplePct: LLM_JUDGE_SAMPLE (0-100, default 10). 0 desliga.
func judgeSamplePct() int {
	if v := strings.TrimSpace(os.Getenv("LLM_JUDGE_SAMPLE")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 100 {
			return n
		}
	}
	return 10
}

func maybeJudgeLabs(qs []models.Question) {
	if _, ok := remoteLLM(); !ok {
		return // juiz precisa ser mais forte que o gerador; local julgando local é eco
	}
	pct := judgeSamplePct()
	if pct <= 0 {
		return
	}
	for i := range qs {
		if qs[i].Type != models.Lab || rand.IntN(100) >= pct {
			continue
		}
		q := qs[i]
		go func() {
			if err := judgeLab(q); err != nil {
				log.Printf("[tutor/judge] %s: %v", q.ID, err)
			}
		}()
	}
}

func judgeLab(q models.Question) error {
	var validations []string
	for _, v := range appendValidationObjects(q) {
		validations = append(validations, v.Command)
	}
	prompt := fmt.Sprintf(`Você é um examinador sênior de certificações Kubernetes/Cloud. Avalie se esta questão de lab está em NÍVEL DE PROVA (%s):

ENUNCIADO: %s
GABARITO: %s
VALIDADORES: %s

Critérios: enunciado sem ambiguidade, gabarito coerente com o pedido, validadores que provam o objetivo, dificuldade compatível com a certificação.
Responda APENAS o JSON: {"verdict":"aprovado"|"reprovado","nivel":1-5,"problema":"vazio se aprovado; senão o defeito em 1 frase"}`,
		q.Cert, compactText(q.Question, 900), compactText(q.AnswerCommand, 400), compactText(strings.Join(validations, " | "), 400))

	raw, err := llmGenerateContract(prompt, "judge", 90*time.Second, tokensChat, "")
	if err != nil {
		return err
	}
	var verdict struct {
		Verdict  string `json:"verdict"`
		Nivel    int    `json:"nivel"`
		Problema string `json:"problema"`
	}
	if err := json.Unmarshal([]byte(raw), &verdict); err != nil {
		return err
	}
	if strings.EqualFold(verdict.Verdict, "aprovado") {
		judgePassed.Add(1)
		return nil
	}
	judgeFlagged.Add(1)
	markLabDegraded(&q, "juiz LLM (amostral): "+compactText(verdict.Problema, 160))
	return RecordLabCatalog([]models.Question{q})
}
