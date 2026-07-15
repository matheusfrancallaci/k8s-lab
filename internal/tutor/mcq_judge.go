package tutor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Juiz de CORREÇÃO de questões (Fatia B do plano A+B). O grounding lexical prova
// que a resposta APARECE na fonte, não que ela É a resposta — um modelo fraco
// gera questão auto-contraditória (marca X e a explicação diz o contrário) que
// passa no grounding. Este gate ataca isso de duas formas, sempre respeitando a
// regra "não julgar com o mesmo modelo fraco que gerou é eco":
//
//   - COM gateway remoto: um modelo forte julga se a alternativa marcada é a
//     única correta segundo a evidência e se a explicação não a contradiz.
//   - SEM gateway: SELF-CONSISTENCY — o modelo local RESOLVE a questão K vezes;
//     se a maioria discorda da marcada (ou não há maioria), a questão é
//     ambígua/incoerente e é rejeitada. Custo baixo (saída curta) e pega a
//     contradição sem depender de um juiz mais forte.
//
// Roda na AUTORIA (batch, amortizado por todos os alunos), nunca no serving.
// ─────────────────────────────────────────────────────────────────────────────

// authoringModel devolve o modelo MAIS FORTE disponível para AUTORAR (custo
// amortizado): tier frontier do gateway remoto quando configurado, senão o
// melhor modelo local de geração. Autoria é onde a qualidade importa e o custo
// se dilui — insistir no modelo pequeno aqui é economizar no lugar errado.
func authoringModel() string {
	if _, ok := remoteLLM(); ok {
		if m := strings.TrimSpace(os.Getenv("LLM_FRONTIER_MODEL")); m != "" {
			return m
		}
		return remoteModelFor("gen", "")
	}
	return genModel()
}

// authoringJudgeModel é o modelo que JULGA a correção. Prefere um modelo
// distinto (LLM_JUDGE_MODEL) para não ser eco; senão o forte remoto.
func authoringJudgeModel() string {
	if m := strings.TrimSpace(os.Getenv("LLM_JUDGE_MODEL")); m != "" {
		return m
	}
	if _, ok := remoteLLM(); ok {
		return remoteModelFor("frontier", "")
	}
	return genModel()
}

func mcqJudgeEnabled() bool {
	v := strings.TrimSpace(os.Getenv("MCQ_JUDGE"))
	return v == "" || v == "1" || strings.EqualFold(v, "true")
}

const selfConsistencyRounds = 3

// verifyMCQCorrectness confere se a alternativa marcada é de fato a correta.
// Retorna: pass (entra ou não), verified (um juiz/consistência REALMENTE
// confirmou, habilitando o selo "judged") e o motivo da rejeição.
func verifyMCQCorrectness(q models.Question, evidence string) (pass, verified bool, reason string) {
	if !mcqJudgeEnabled() || q.Answer < 0 || q.Answer >= len(q.Options) {
		return true, false, ""
	}
	if _, ok := remoteLLM(); ok {
		return judgeMCQRemote(q, evidence)
	}
	return selfConsistentMCQ(q, evidence)
}

func judgeMCQRemote(q models.Question, evidence string) (bool, bool, string) {
	if len(evidence) > 4000 {
		evidence = evidence[:4000]
	}
	prompt := fmt.Sprintf(`Você é um examinador sênior de certificações Kubernetes/Cloud. Avalie a QUESTÃO abaixo usando SOMENTE a evidência.

ENUNCIADO: %s
ALTERNATIVAS:
%s
MARCADA COMO CORRETA: %s
EXPLICAÇÃO DADA: %s

EVIDÊNCIA OFICIAL:
%s

Aprove SOMENTE se as três forem verdadeiras:
1) exatamente UMA alternativa é correta segundo a evidência;
2) essa alternativa é a MARCADA;
3) a explicação NÃO contradiz a alternativa marcada.
Responda APENAS JSON: {"verdict":"aprovado"|"reprovado","nivel":1-5,"problema":"vazio se aprovado; senão o defeito em 1 frase"}`,
		compactText(q.Question, 600), optionsBlock(q), compactText(q.Options[q.Answer], 200), compactText(q.Explanation, 300), evidence)

	raw, err := llmGenerateContract(prompt, "judge", 90*time.Second, tokensChat, authoringJudgeModel())
	if err != nil {
		// Juiz indisponível não deve bloquear (grounding já passou); só não sela.
		return true, false, ""
	}
	var v struct {
		Verdict  string `json:"verdict"`
		Problema string `json:"problema"`
	}
	if json.Unmarshal([]byte(raw), &v) != nil {
		return true, false, ""
	}
	if strings.EqualFold(v.Verdict, "aprovado") {
		return true, true, ""
	}
	return false, true, "juiz de correção reprovou: " + compactText(v.Problema, 140)
}

// selfConsistentMCQ pede ao modelo local que RESOLVA a questão K vezes e compara
// com a alternativa marcada.
func selfConsistentMCQ(q models.Question, evidence string) (bool, bool, string) {
	if ok, _ := LLMStatus(); !ok {
		return true, false, "" // sem modelo, não dá para verificar; grounding decide
	}
	if len(evidence) > 3500 {
		evidence = evidence[:3500]
	}
	var votes []int
	for i := 0; i < selfConsistencyRounds; i++ {
		if a, ok := solveMCQLocally(q, evidence); ok {
			votes = append(votes, a)
		}
	}
	if len(votes) < 2 {
		return true, false, "" // não conseguiu resolver o bastante para confirmar
	}
	val, count := majorityVote(votes)
	if count*2 <= len(votes) {
		return false, true, "sem maioria ao resolver a questão (ambígua para o próprio modelo)"
	}
	if val != q.Answer {
		return false, true, "ao resolver a questão, o modelo discorda da alternativa marcada como correta"
	}
	return true, true, ""
}

func solveMCQLocally(q models.Question, evidence string) (int, bool) {
	prompt := fmt.Sprintf(`Baseado SOMENTE na evidência, escolha a única alternativa correta.

PERGUNTA: %s
ALTERNATIVAS:
%s
EVIDÊNCIA:
%s

Responda APENAS JSON com o índice (0-%d) da correta: {"answer": N}`,
		compactText(q.Question, 600), optionsBlock(q), evidence, len(q.Options)-1)
	raw, err := llmGenerateFormatted(prompt, "json", 60*time.Second, 120, genModel())
	if err != nil {
		return 0, false
	}
	var out struct {
		Answer json.Number `json:"answer"`
	}
	if json.Unmarshal([]byte(raw), &out) != nil {
		return 0, false
	}
	n, err := out.Answer.Int64()
	if err != nil || n < 0 || int(n) >= len(q.Options) {
		return 0, false
	}
	return int(n), true
}

// majorityVote devolve o valor mais frequente e sua contagem.
func majorityVote(votes []int) (int, int) {
	counts := map[int]int{}
	best, bestN := votes[0], 0
	for _, v := range votes {
		counts[v]++
		if counts[v] > bestN {
			best, bestN = v, counts[v]
		}
	}
	return best, bestN
}

func optionsBlock(q models.Question) string {
	var b strings.Builder
	for i, o := range q.Options {
		fmt.Fprintf(&b, "%d) %s\n", i, o)
	}
	return strings.TrimRight(b.String(), "\n")
}

// markMCQJudged promove uma questão conceitual cujo juiz/consistência confirmou
// a resposta. Distintо de "verified" (execução dos distratores) e mais forte que
// "grounded" (só ancoragem lexical).
func markMCQJudged(q *models.Question) {
	if q == nil || q.Readiness == nil {
		return
	}
	r := q.Readiness
	r.State = "judged"
	r.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	r.Warnings = []string{"resposta confirmada por juiz independente; distratores não provados por execução"}
}
