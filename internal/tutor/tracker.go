// Package tutor implementa um tutor adaptativo 100% local (zero API):
// observa o desempenho do usuário (goals, hints, terminal), mantém um modelo
// de habilidade estatístico por tópico e gera labs personalizados por template.
//
// O estado é POR USUÁRIO (Profile), keyed por um id de perfil vindo do cookie.
// Sem perfil definido, tudo cai no perfil "default" (comportamento single-user).
package tutor

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// Profile é o estado adaptativo de UM usuário (isolado dos demais).
type Profile struct {
	mu          sync.Mutex
	id          string
	Skills      map[string]*TopicSkill `json:"skills"`
	activeCert  string
	activeTopic string
	lastAdvised map[string]time.Time // cooldown de recomendações
	saveTimer   *time.Timer
}

var (
	profilesMu sync.Mutex
	profiles   = map[string]*Profile{}
)

var idRe = regexp.MustCompile(`[^a-z0-9_-]+`)

// SanitizeID normaliza um nome de perfil para um slug seguro de arquivo.
// Fonte única da regra — handlers usam isto para derivar o id do cookie.
func SanitizeID(raw string) string {
	s := idRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(raw)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
	}
	if s == "" {
		return "default"
	}
	return s
}

func profilesDir() string       { return filepath.Join("data", "profiles") }
func profilePath(id string) string { return filepath.Join(profilesDir(), id+".json") }

func profileFor(userID string) *Profile {
	id := SanitizeID(userID)
	profilesMu.Lock()
	defer profilesMu.Unlock()
	if p := profiles[id]; p != nil {
		return p
	}
	p := loadProfile(id)
	profiles[id] = p
	return p
}

type skillsDoc struct {
	Skills map[string]*TopicSkill `json:"skills"`
}

func loadProfile(id string) *Profile {
	p := &Profile{id: id, Skills: map[string]*TopicSkill{}, lastAdvised: map[string]time.Time{}}
	b, err := os.ReadFile(profilePath(id))
	if err != nil && id == "default" {
		// Migração única: progresso legado morava em data/tutor.json.
		b, err = os.ReadFile(filepath.Join("data", "tutor.json"))
	}
	if err == nil {
		var s skillsDoc
		if json.Unmarshal(b, &s) == nil && s.Skills != nil {
			p.Skills = s.Skills
		}
	}
	return p
}

// scheduleSave persiste com debounce (caller deve segurar p.mu).
func (p *Profile) scheduleSave() {
	if p.saveTimer != nil {
		p.saveTimer.Stop()
	}
	p.saveTimer = time.AfterFunc(2*time.Second, func() {
		p.mu.Lock()
		b, err := json.MarshalIndent(skillsDoc{Skills: p.Skills}, "", "  ")
		p.mu.Unlock()
		if err != nil {
			return
		}
		if err := os.MkdirAll(profilesDir(), 0o755); err != nil {
			return
		}
		if err := os.WriteFile(profilePath(p.id), b, 0o644); err != nil {
			log.Printf("[tutor] falha ao salvar perfil %s: %v", p.id, err)
		}
	})
}

// skillFor devolve/cria o skill do tópico (caller deve segurar p.mu).
func (p *Profile) skillFor(cert, topic string) *TopicSkill {
	key := cert + "|" + topic
	s, ok := p.Skills[key]
	if !ok {
		s = &TopicSkill{Cert: cert, Topic: topic, Score: 0.5} // prior neutro
		p.Skills[key] = s
	}
	return s
}

// ─────────────────────────────────────────────────────────────────────────────
// Eventos — API pública, sempre por usuário (userID vem do cookie de perfil).
// ─────────────────────────────────────────────────────────────────────────────

// SetActiveQuestion registra a questão aberta — dá contexto aos eventos do terminal.
func SetActiveQuestion(userID string, q models.Question) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeCert = string(q.Cert)
	p.activeTopic = q.Topic
}

// RecordGoal registra o resultado de um CHECK de goal.
func RecordGoal(userID string, q models.Question, success bool) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.skillFor(string(q.Cert), q.Topic)
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
	p.scheduleSave()
}

// RecordHint registra a abertura da aba HINT.
func RecordHint(userID string, q models.Question) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.skillFor(string(q.Cert), q.Topic).Hints++
	p.scheduleSave()
}

// RecordSolution registra a abertura da aba SOLUTION.
func RecordSolution(userID string, q models.Question) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.skillFor(string(q.Cert), q.Topic).Solutions++
	p.scheduleSave()
}

// RecordDone registra a conclusão de uma questão e o tempo gasto.
func RecordDone(userID string, q models.Question, seconds int) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.skillFor(string(q.Cert), q.Topic)
	s.Completed++
	s.TotalSecs += seconds
	p.scheduleSave()
}

// RecordTermError registra um erro de comando visto no terminal do lab,
// atribuído ao tópico da questão ativa do usuário.
func RecordTermError(userID string) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeTopic == "" {
		return
	}
	p.skillFor(p.activeCert, p.activeTopic).TermErrors++
	p.scheduleSave()
}

// Stats devolve uma cópia dos skills do usuário para o dashboard.
func Stats(userID string) []TopicSkill {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]TopicSkill, 0, len(p.Skills))
	for _, s := range p.Skills {
		out = append(out, *s)
	}
	return out
}
