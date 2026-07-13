package tutor

import (
	"fmt"
	"math/rand/v2"
	"strings"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Lab composto estilo prova — realismo de certificação pela ESTRUTURA, não
// pelo LLM: questão de CKA real é cenário multi-restrição com crédito parcial.
// Compõe 2-4 tarefas de tópicos distintos (templates determinísticos, nomes
// randomizados) numa única questão com goals ponderáveis por tarefa.
// ─────────────────────────────────────────────────────────────────────────────

// GenerateExamComposite monta uma questão estilo prova para a cert: cenário +
// N tarefas independentes, cada uma com seus validadores (crédito parcial por
// goal). Persiste e (quando habilitado) passa pela verificação executável.
func GenerateExamComposite(cert string, parts int) (models.Question, error) {
	if parts < 2 {
		parts = 3
	}
	if parts > 4 {
		parts = 4
	}
	var pool []string
	for _, t := range fallbackTopicsForCert(cert, "") {
		if _, ok := templates[t]; ok {
			pool = append(pool, t)
		}
	}
	if len(pool) < 2 {
		return models.Question{}, fmt.Errorf("cert %s nao tem templates suficientes para um lab composto", cert)
	}
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if len(pool) > parts {
		pool = pool[:parts]
	}

	var subs []models.Question
	for _, topic := range pool {
		if qs := generateQuestions(topic, cert, 2, 1); len(qs) == 1 {
			subs = append(subs, qs[0])
		}
	}
	if len(subs) < 2 {
		return models.Question{}, fmt.Errorf("nao consegui compor tarefas suficientes para %s", cert)
	}

	comp := models.Question{
		ID:         newID(),
		Cert:       models.Cert(cert),
		Topic:      "Estilo Prova",
		Difficulty: models.Hard,
		Type:       models.Lab,
		Source:     models.SourceGenerated,
	}
	var enunciado strings.Builder
	var answers []string
	var topics []string
	fmt.Fprintf(&enunciado, "**Cenário de prova (%s)** — você assumiu um cluster do time e precisa executar %d tarefas independentes. Cada tarefa pontua separadamente (crédito parcial), como na prova real.\n", cert, len(subs))
	for i, s := range subs {
		topics = append(topics, s.Topic)
		fmt.Fprintf(&enunciado, "\n**Tarefa %d · %s** — %s\n", i+1, s.Topic, strings.TrimSpace(s.Question))
		comp.Setup = append(comp.Setup, s.Setup...)
		comp.Teardown = append(comp.Teardown, s.Teardown...)
		goals := s.Goals
		if len(goals) == 0 && s.Validation != nil {
			goals = []models.Goal{{Description: "Validar o resultado", Validation: s.Validation}}
		}
		for _, g := range goals {
			g.Description = fmt.Sprintf("[Tarefa %d] %s", i+1, g.Description)
			comp.Goals = append(comp.Goals, g)
		}
		if strings.TrimSpace(s.AnswerCommand) != "" {
			answers = append(answers, fmt.Sprintf("# Tarefa %d — %s\n%s", i+1, s.Topic, s.AnswerCommand))
		}
	}
	comp.Question = enunciado.String()
	comp.AnswerCommand = strings.Join(answers, "\n\n")
	comp.Explanation = fmt.Sprintf("Lab composto estilo prova cobrindo %s. Na certificação real as questões encadeiam restrições de tópicos diferentes — treine alternar de contexto sem perder o namespace e o nome exato de cada recurso.", strings.Join(topics, ", "))

	comp = FinalizeLab(comp, "lab estilo prova")
	if err := persist([]models.Question{comp}); err != nil {
		return models.Question{}, err
	}
	return comp, nil
}
