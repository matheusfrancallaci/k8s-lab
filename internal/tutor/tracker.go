// Package tutor implementa um tutor adaptativo 100% local (zero API):
// observa o desempenho do usuário (goals, hints, terminal), mantém um modelo
// de habilidade estatístico por tópico e gera labs personalizados por template.
package tutor

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Modelo de habilidade — EWMA de acerto por (cert, tópico)
// ─────────────────────────────────────────────────────────────────────────────

const ewmaAlpha = 0.3

// TopicSkill é o estado de habilidade do usuário em um tópico.
type TopicSkill struct {
	Cert        string    `json:"cert"`
	Topic       string    `json:"topic"`
	Score       float64   `json:"score"` // EWMA de acerto ∈ [0,1]
	Attempts    int       `json:"attempts"`
	Failures    int       `json:"failures"`
	FailStreak  int       `json:"fail_streak"`
	Hints       int       `json:"hints"`
	Solutions   int       `json:"solutions"`
	TermErrors  int       `json:"term_errors"`
	TotalSecs   int       `json:"total_secs"`
	Completed   int       `json:"completed"`
	LastAttempt time.Time `json:"last_attempt"`
}

type trackerState struct {
	Skills map[string]*TopicSkill `json:"skills"` // key: cert|topic
}

var (
	mu    sync.Mutex
	state = trackerState{Skills: map[string]*TopicSkill{}}

	// contexto do terminal: última questão aberta
	activeCert  string
	activeTopic string

	saveTimer *time.Timer
)

func dataFile() string { return filepath.Join("data", "tutor.json") }

// Load carrega o histórico de habilidade do disco (chame no boot).
func Load() {
	mu.Lock()
	defer mu.Unlock()
	b, err := os.ReadFile(dataFile())
	if err != nil {
		return // primeiro uso
	}
	var s trackerState
	if json.Unmarshal(b, &s) == nil && s.Skills != nil {
		state = s
	}
}

// scheduleSave persiste com debounce (caller deve segurar mu).
func scheduleSave() {
	if saveTimer != nil {
		saveTimer.Stop()
	}
	saveTimer = time.AfterFunc(2*time.Second, func() {
		mu.Lock()
		b, err := json.MarshalIndent(state, "", "  ")
		mu.Unlock()
		if err != nil {
			return
		}
		if err := os.MkdirAll("data", 0o755); err != nil {
			return
		}
		if err := os.WriteFile(dataFile(), b, 0o644); err != nil {
			log.Printf("[tutor] falha ao salvar estado: %v", err)
		}
	})
}

func skillFor(cert, topic string) *TopicSkill {
	key := cert + "|" + topic
	s, ok := state.Skills[key]
	if !ok {
		s = &TopicSkill{Cert: cert, Topic: topic, Score: 0.5} // prior neutro
		state.Skills[key] = s
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// Eventos
// ─────────────────────────────────────────────────────────────────────────────

// SetActiveQuestion registra a questão aberta — dá contexto aos eventos do terminal.
func SetActiveQuestion(q models.Question) {
	mu.Lock()
	defer mu.Unlock()
	activeCert = string(q.Cert)
	activeTopic = q.Topic
}

// RecordGoal registra o resultado de um CHECK de goal.
func RecordGoal(q models.Question, success bool) {
	mu.Lock()
	defer mu.Unlock()
	s := skillFor(string(q.Cert), q.Topic)
	s.Attempts++
	s.LastAttempt = time.Now()
	v := 0.0
	if success {
		v = 1.0
		s.FailStreak = 0
	} else {
		s.Failures++
		s.FailStreak++
	}
	s.Score = s.Score*(1-ewmaAlpha) + v*ewmaAlpha
	scheduleSave()
}

// RecordHint registra a abertura da aba HINT.
func RecordHint(q models.Question) {
	mu.Lock()
	defer mu.Unlock()
	skillFor(string(q.Cert), q.Topic).Hints++
	scheduleSave()
}

// RecordSolution registra a abertura da aba SOLUTION.
func RecordSolution(q models.Question) {
	mu.Lock()
	defer mu.Unlock()
	skillFor(string(q.Cert), q.Topic).Solutions++
	scheduleSave()
}

// RecordDone registra a conclusão de uma questão e o tempo gasto.
func RecordDone(q models.Question, seconds int) {
	mu.Lock()
	defer mu.Unlock()
	s := skillFor(string(q.Cert), q.Topic)
	s.Completed++
	s.TotalSecs += seconds
	scheduleSave()
}

// RecordTermError registra um erro de comando visto no terminal do lab,
// atribuído ao tópico da questão ativa.
func RecordTermError() {
	mu.Lock()
	defer mu.Unlock()
	if activeTopic == "" {
		return
	}
	skillFor(activeCert, activeTopic).TermErrors++
	scheduleSave()
}

// Stats devolve uma cópia dos skills para o dashboard (ordenação fica na UI).
func Stats() []TopicSkill {
	mu.Lock()
	defer mu.Unlock()
	out := make([]TopicSkill, 0, len(state.Skills))
	for _, s := range state.Skills {
		out = append(out, *s)
	}
	return out
}
