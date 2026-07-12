package tutor

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

type TutorPhase struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"`
	Status  string `json:"status"`
	Gate    string `json:"gate,omitempty"`
}

type TutorOrchestration struct {
	TurnID       string       `json:"turn_id"`
	Intent       string       `json:"intent"`
	Strategy     string       `json:"strategy"`
	LearnerLevel string       `json:"learner_level"`
	TargetTopic  string       `json:"target_topic,omitempty"`
	Phases       []TutorPhase `json:"phases"`
	MaxToolCalls int          `json:"max_tool_calls"`
	MaxRevisions int          `json:"max_revisions"`
	CreatedAt    time.Time    `json:"created_at"`
}

var orchestrationState = struct {
	sync.RWMutex
	Values map[string]TutorOrchestration
}{Values: map[string]TutorOrchestration{}}

func OrchestrateTutorTurn(userID, msg, cert, mode string) TutorOrchestration {
	memory := LearningMemoryFor(userID)
	intent := classifyTutorIntent(msg)
	topic := exactTopicForRequest(cert, msg)
	if topic == "" {
		topic = detectTopic(msg)
	}
	level := inferLearnerLevel(memory)
	strategy := selectTeachingStrategy(intent, level, mode, memory)
	phases := []TutorPhase{
		{Name: "observe", Purpose: "identificar objetivo, nivel e contexto relevante", Status: "completed", Gate: "intencao reconhecida"},
		{Name: "retrieve", Purpose: "recuperar fontes oficiais e memoria pedagogica", Status: "planned", Gate: "evidencia suficiente ou recusa"},
		{Name: "act", Purpose: "usar somente ferramentas autorizadas quando necessario", Status: "planned", Gate: "somente leitura automatica; escrita exige aprovacao"},
		{Name: "teach", Purpose: "explicar no nivel do aluno sem entregar atalhos indevidos", Status: "planned", Gate: "estrategia pedagogica aplicada"},
		{Name: "verify", Purpose: "auditar claims, fontes e proximo passo", Status: "planned", Gate: "grounding >= 80%"},
	}
	plan := TutorOrchestration{TurnID: ragID(userID, msg, time.Now().UTC().Format(time.RFC3339Nano)), Intent: intent, Strategy: strategy, LearnerLevel: level, TargetTopic: topic, Phases: phases, MaxToolCalls: 3, MaxRevisions: 1, CreatedAt: time.Now().UTC()}
	orchestrationState.Lock()
	orchestrationState.Values[ragID(userID)] = plan
	orchestrationState.Unlock()
	return plan
}

func LastTutorOrchestration(userID string) TutorOrchestration {
	orchestrationState.RLock()
	defer orchestrationState.RUnlock()
	return orchestrationState.Values[ragID(userID)]
}

func (p TutorOrchestration) PromptContext() string {
	return fmt.Sprintf("PLANO PEDAGOGICO DO TURNO (metadados do backend): intencao=%s; estrategia=%s; nivel=%s; topico=%s. Nao exponha estes metadados literalmente; aplique-os na resposta.", p.Intent, p.Strategy, p.LearnerLevel, p.TargetTopic)
}

func classifyTutorIntent(msg string) string {
	l := strings.ToLower(msg)
	switch {
	case regexp.MustCompile(`lab|hands.?on|exerc[ií]cio|pratic`).MatchString(l):
		return "practice"
	case regexp.MustCompile(`erro|falh|incidente|diagn[oó]st|nao funciona|não funciona`).MatchString(l):
		return "diagnose"
	case regexp.MustCompile(`compare|diferen[cç]a|trade.?off|quando usar`).MatchString(l):
		return "compare"
	case regexp.MustCompile(`prova|exame|simulado|certifica`).MatchString(l):
		return "exam"
	case regexp.MustCompile(`revis|errei|refor[cç]`).MatchString(l):
		return "review"
	default:
		return "explain"
	}
}

func inferLearnerLevel(memory LearningMemory) string {
	if len(memory.StrongTopics) >= 3 && len(memory.CurrentGaps) == 0 {
		return "advanced"
	}
	if len(memory.StrongTopics) > 0 {
		return "intermediate"
	}
	return "beginner"
}

func selectTeachingStrategy(intent, level, mode string, memory LearningMemory) string {
	if intent == "diagnose" {
		return "hypothesis-evidence-test"
	}
	if intent == "review" || len(memory.CurrentGaps) >= 3 {
		return "retrieval-practice"
	}
	if intent == "compare" || level == "advanced" || mode == "deep" {
		return "tradeoff-and-counterexample"
	}
	if intent == "practice" {
		return "guided-discovery"
	}
	if intent == "exam" {
		return "exam-reasoning-no-spoilers"
	}
	return "scaffolded-explanation-checkpoint"
}
