// Package tutor implementa um tutor adaptativo 100% local (zero API):
// observa o desempenho do usuário (goals, hints, terminal), mantém um modelo
// de habilidade estatístico por tópico e gera labs personalizados por template.
//
// O estado é POR USUÁRIO (Profile), keyed por um id de perfil vindo do cookie.
// Sem perfil definido, tudo cai no perfil "default" (comportamento single-user).
package tutor

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// Retenção medida: resultado em questão REAPRESENTADA pelo spaced
	// repetition (a revisão estava vencida quando o aluno respondeu).
	// É a métrica "reter de verdade" do docs/game-change.md — sem ela o
	// loop de reforço é fé, não medida.
	RetentionHits   int `json:"retention_hits,omitempty"`
	RetentionMisses int `json:"retention_misses,omitempty"`
}

type ReviewItem struct {
	Cert         string    `json:"cert"`
	Topic        string    `json:"topic"`
	QuestionID   string    `json:"question_id"`
	Reason       string    `json:"reason"`
	Due          time.Time `json:"due"`
	IntervalDays int       `json:"interval_days"`
	Failures     int       `json:"failures"`
	LastSeen     time.Time `json:"last_seen"`
	Ready        bool      `json:"ready,omitempty"`
}

type DomainMastery struct {
	Cert       string   `json:"cert"`
	Domain     string   `json:"domain"`
	Weight     int      `json:"weight"`
	Score      float64  `json:"score"`
	Attempts   int      `json:"attempts"`
	DueReviews int      `json:"due_reviews"`
	Sources    []string `json:"sources"`
}

// ProfileSnapshot é a fotografia diária do perfil — alimenta a tendência
// (sparkline) do painel: o aluno vê o loop funcionando, não só o estado de hoje.
// Valores CUMULATIVOS no fim daquele dia (o último evento do dia sobrescreve).
type ProfileSnapshot struct {
	Date            string  `json:"date"` // YYYY-MM-DD
	AvgScore        float64 `json:"avg_score"`
	Attempts        int     `json:"attempts"`
	RetentionHits   int     `json:"retention_hits"`
	RetentionMisses int     `json:"retention_misses"`
	DueReviews      int     `json:"due_reviews"`
}

// StudyGoal é o objetivo declarado do aluno (onboarding): vira contagem
// regressiva no painel e âncora do plano. Sem objetivo, o painel pede um.
type StudyGoal struct {
	Cert     string `json:"cert,omitempty"`
	ExamDate string `json:"exam_date,omitempty"` // YYYY-MM-DD
	Level    string `json:"level,omitempty"`     // iniciante|intermediario|avancado
	SetAt    string `json:"set_at,omitempty"`
}

// StreakState é o streak no SERVIDOR — localStorage se perde por browser/
// dispositivo e mentia a jornada.
type StreakState struct {
	Count   int    `json:"count"`
	LastDay string `json:"last_day,omitempty"` // YYYY-MM-DD
}

// QuestionOutcome é o estado HONESTO de uma questão/lab: avançar nunca
// bloqueia, mas o que aconteceu fica registrado — aprovado, tentou, pulou,
// abriu dica/solução, falha de ambiente.
type QuestionOutcome struct {
	Cert           string    `json:"cert,omitempty"`
	Topic          string    `json:"topic,omitempty"`
	Approved       bool      `json:"approved,omitempty"`
	Attempts       int       `json:"attempts,omitempty"`
	Skips          int       `json:"skips,omitempty"`
	HintOpened     bool      `json:"hint_opened,omitempty"`
	SolutionOpened bool      `json:"solution_opened,omitempty"`
	EnvFailures    int       `json:"env_failures,omitempty"`
	Seconds        int       `json:"seconds,omitempty"`
	PassedGoals    []int     `json:"passed_goals,omitempty"` // goals de lab já verdes
	LastAt         time.Time `json:"last_at,omitempty"`
}

// State deriva o rótulo honesto para a UI (precedência do que importa contar).
func (o *QuestionOutcome) State() string {
	switch {
	case o == nil:
		return ""
	case o.Approved && o.SolutionOpened:
		return "aprovado_com_solucao"
	case o.Approved:
		return "aprovado"
	case o.SolutionOpened:
		return "solucao"
	case o.HintOpened:
		return "dica"
	case o.Attempts > 0:
		return "tentou"
	case o.EnvFailures > 0:
		return "falha_ambiente"
	case o.Skips > 0:
		return "pulou"
	default:
		return "aberto"
	}
}

// Profile é o estado adaptativo de UM usuário (isolado dos demais).
type Profile struct {
	mu          sync.Mutex
	id          string
	Skills      map[string]*TopicSkill      `json:"skills"`
	Review      map[string]*ReviewItem      `json:"review"`
	History     []ProfileSnapshot           `json:"history,omitempty"`
	Memory      LearningMemory              `json:"memory,omitempty"`
	Goal        StudyGoal                   `json:"goal,omitempty"`
	Streak      StreakState                 `json:"streak,omitempty"`
	Activity    map[string]*QuestionOutcome `json:"activity,omitempty"` // por question ID
	activeCert  string
	activeTopic string
	activeLab   *models.Question
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

func profilesDir() string          { return filepath.Join("data", "profiles") }
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
	Skills   map[string]*TopicSkill      `json:"skills"`
	Review   map[string]*ReviewItem      `json:"review,omitempty"`
	History  []ProfileSnapshot           `json:"history,omitempty"`
	Memory   LearningMemory              `json:"memory,omitempty"`
	Goal     StudyGoal                   `json:"goal,omitempty"`
	Streak   StreakState                 `json:"streak,omitempty"`
	Activity map[string]*QuestionOutcome `json:"activity,omitempty"`
}

func loadProfile(id string) *Profile {
	p := &Profile{id: id, Skills: map[string]*TopicSkill{}, Review: map[string]*ReviewItem{}, lastAdvised: map[string]time.Time{}}
	b, err := os.ReadFile(profilePath(id))
	if err != nil && id == "default" {
		// Migração única: progresso legado morava em data/tutor.json.
		b, err = os.ReadFile(filepath.Join("data", "tutor.json"))
	}
	if err == nil {
		var s skillsDoc
		if json.Unmarshal(b, &s) == nil {
			if s.Skills != nil {
				p.Skills = s.Skills
			}
			if s.Review != nil {
				p.Review = s.Review
			}
			p.History = s.History
			p.Memory = s.Memory
			p.Goal = s.Goal
			p.Streak = s.Streak
			if s.Activity != nil {
				p.Activity = s.Activity
			}
		}
	}
	if p.Activity == nil {
		p.Activity = map[string]*QuestionOutcome{}
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
		refreshLearningMemoryLocked(p)
		b, err := json.MarshalIndent(skillsDoc{Skills: p.Skills, Review: p.Review, History: p.History, Memory: p.Memory, Goal: p.Goal, Streak: p.Streak, Activity: p.Activity}, "", "  ")
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
	cp := q
	p.activeLab = &cp
}

// ActiveQuestion devolve uma cópia do lab aberto pelo usuário. O terminal usa
// isto para gerar RBAC por lab e não depender de permissões amplas.
func ActiveQuestion(userID string) (models.Question, bool) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeLab == nil {
		return models.Question{}, false
	}
	return *p.activeLab, true
}

// RecordGoal registra o resultado de um CHECK de goal.
func RecordGoal(userID string, q models.Question, success bool) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.skillFor(string(q.Cert), q.Topic)
	s.Attempts++
	now := time.Now()
	s.LastAttempt = now
	v := 0.0
	if success {
		v = 1.0
		s.FailStreak = 0
	} else {
		s.Failures++
		s.FailStreak++
	}
	s.Score = s.Score*(1-ewmaAlpha) + v*ewmaAlpha
	// Reapresentação: se a revisão desta questão estava vencida, este resultado
	// mede retenção após o intervalo (antes do recordReview reagendar).
	if item := p.Review[reviewKey(q)]; item != nil && !item.Due.After(now) {
		if success {
			s.RetentionHits++
		} else {
			s.RetentionMisses++
		}
	}
	p.recordReview(q, success, now)
	if success {
		p.settleTopicReviewsLocked(q, now)
	}
	p.snapshotLocked(now)
	p.scheduleSave()
}

// settleTopicReviewsLocked reagenda revisões VENCIDAS do mesmo cert+tópico
// quando o aluno acerta qualquer questão dele (caller segura p.mu). Sem isto,
// um item de revisão apontando para uma questão antiga (id de lab gerado que
// já nem existe) segurava o tópico e o nag de revisão PARA SEMPRE — treinar o
// tópico por outros labs nunca limpava. A unidade pedagógica é o tópico.
func (p *Profile) settleTopicReviewsLocked(q models.Question, now time.Time) {
	self := reviewKey(q)
	for key, item := range p.Review {
		if item == nil || key == self || item.Due.After(now) {
			continue
		}
		if !strings.EqualFold(item.Cert, string(q.Cert)) || !strings.EqualFold(item.Topic, q.Topic) {
			continue
		}
		item.LastSeen = now
		if item.IntervalDays < 1 {
			item.IntervalDays = 1
		} else {
			item.IntervalDays *= 2
		}
		if item.IntervalDays > 21 {
			item.IntervalDays = 21
		}
		item.Due = now.Add(time.Duration(item.IntervalDays) * 24 * time.Hour)
		item.Reason = "consolidado por acerto recente no tópico"
	}
}

// snapshotLocked atualiza a fotografia do DIA (caller segura p.mu). O último
// evento do dia vence — a série guarda o estado de fechamento de cada dia.
func (p *Profile) snapshotLocked(now time.Time) {
	var sum float64
	var n, attempts, hits, misses int
	for _, s := range p.Skills {
		if s == nil || s.Attempts == 0 {
			continue
		}
		sum += s.Score
		n++
		attempts += s.Attempts
		hits += s.RetentionHits
		misses += s.RetentionMisses
	}
	due := 0
	for _, item := range p.Review {
		if item != nil && !item.Due.After(now) {
			due++
		}
	}
	snap := ProfileSnapshot{
		Date:            now.Format("2006-01-02"),
		Attempts:        attempts,
		RetentionHits:   hits,
		RetentionMisses: misses,
		DueReviews:      due,
	}
	if n > 0 {
		snap.AvgScore = sum / float64(n)
	}
	if k := len(p.History); k > 0 && p.History[k-1].Date == snap.Date {
		p.History[k-1] = snap
	} else {
		p.History = append(p.History, snap)
	}
	if len(p.History) > 90 {
		p.History = p.History[len(p.History)-90:]
	}
}

// History devolve a série diária do perfil para o painel de tendência.
func History(userID string) []ProfileSnapshot {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ProfileSnapshot, len(p.History))
	copy(out, p.History)
	return out
}

func reviewKey(q models.Question) string {
	id := strings.TrimSpace(q.ID)
	if id == "" {
		id = string(q.Cert) + "|" + q.Topic
	}
	return string(q.Cert) + "|" + q.Topic + "|" + id
}

func (p *Profile) recordReview(q models.Question, success bool, now time.Time) {
	if p.Review == nil {
		p.Review = map[string]*ReviewItem{}
	}
	key := reviewKey(q)
	item := p.Review[key]
	if item == nil && success {
		return
	}
	if item == nil {
		item = &ReviewItem{
			Cert:         string(q.Cert),
			Topic:        q.Topic,
			QuestionID:   q.ID,
			IntervalDays: 1,
		}
		p.Review[key] = item
	}
	item.LastSeen = now
	if success {
		if item.Failures == 0 {
			item.Reason = "reforco espacacado apos acerto"
		} else {
			item.Reason = "erro anterior corrigido; revisar para consolidar"
		}
		if item.IntervalDays < 1 {
			item.IntervalDays = 1
		} else {
			item.IntervalDays *= 2
		}
		if item.IntervalDays > 21 {
			item.IntervalDays = 21
		}
		item.Due = now.Add(time.Duration(item.IntervalDays) * 24 * time.Hour)
		return
	}
	item.Failures++
	item.IntervalDays = 1
	item.Due = now
	item.Reason = "falha em validador; revisar com lab guiado"
}

// outcomeForLocked devolve/cria o outcome da questão (caller segura p.mu).
func (p *Profile) outcomeForLocked(q models.Question) *QuestionOutcome {
	if p.Activity == nil {
		p.Activity = map[string]*QuestionOutcome{}
	}
	id := strings.TrimSpace(q.ID)
	if id == "" {
		id = string(q.Cert) + "|" + q.Topic
	}
	o := p.Activity[id]
	if o == nil {
		o = &QuestionOutcome{Cert: string(q.Cert), Topic: q.Topic}
		p.Activity[id] = o
		// Cap: jornada honesta não precisa de histórico infinito por questão —
		// descarta os outcomes mais antigos além de 800.
		if len(p.Activity) > 800 {
			oldestID, oldest := "", time.Now()
			for k, v := range p.Activity {
				if v != nil && v.LastAt.Before(oldest) {
					oldestID, oldest = k, v.LastAt
				}
			}
			delete(p.Activity, oldestID)
		}
	}
	o.LastAt = time.Now()
	return o
}

// touchStreakLocked marca atividade de estudo do dia (caller segura p.mu).
func (p *Profile) touchStreakLocked(now time.Time) {
	today := now.Format("2006-01-02")
	if p.Streak.LastDay == today {
		return
	}
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	if p.Streak.LastDay == yesterday {
		p.Streak.Count++
	} else {
		p.Streak.Count = 1
	}
	p.Streak.LastDay = today
}

// RecordHint registra a abertura da aba HINT.
func RecordHint(userID string, q models.Question) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.skillFor(string(q.Cert), q.Topic).Hints++
	p.outcomeForLocked(q).HintOpened = true
	p.scheduleSave()
}

// RecordSolution registra a abertura da aba SOLUTION.
func RecordSolution(userID string, q models.Question) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.skillFor(string(q.Cert), q.Topic).Solutions++
	p.outcomeForLocked(q).SolutionOpened = true
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
	o := p.outcomeForLocked(q)
	o.Seconds += seconds
	o.Approved = true
	p.scheduleSave()
}

// RecordAttempt registra a tentativa na jornada honesta e decide aprovação:
// goalIdx >= 0 é um goal de lab (aprova quando TODOS os goals da questão já
// passaram); goalIdx < 0 é questão de resposta única (quiz/validação própria):
// sucesso aprova direto. Também marca o streak do dia.
func RecordAttempt(userID string, q models.Question, goalIdx int, success bool) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	o := p.outcomeForLocked(q)
	o.Attempts++
	if success {
		if goalIdx < 0 || len(q.Goals) == 0 {
			o.Approved = true
		} else {
			seen := false
			for _, g := range o.PassedGoals {
				if g == goalIdx {
					seen = true
					break
				}
			}
			if !seen {
				o.PassedGoals = append(o.PassedGoals, goalIdx)
			}
			if len(o.PassedGoals) >= len(q.Goals) {
				o.Approved = true
			}
		}
	}
	p.touchStreakLocked(time.Now())
	p.scheduleSave()
}

// RecordEnvFailure registra falha de AMBIENTE na questão — não conta contra o
// aluno (skill intocado), mas a jornada honesta guarda que aconteceu.
func RecordEnvFailure(userID string, q models.Question) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.outcomeForLocked(q).EnvFailures++
	p.scheduleSave()
}

// RecordSkip registra que o aluno AVANÇOU sem aprovar a questão. Avançar nunca
// bloqueia; mentir que concluiu é que não pode.
func RecordSkip(userID string, q models.Question) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	o := p.outcomeForLocked(q)
	if o.Approved {
		return
	}
	o.Skips++
	p.scheduleSave()
}

// SetStudyGoal persiste o objetivo do aluno (onboarding). examDate em
// YYYY-MM-DD; valores vazios limpam o campo correspondente.
func SetStudyGoal(userID, cert, examDate, level string) error {
	cert = CanonicalCert(strings.TrimSpace(cert))
	examDate = strings.TrimSpace(examDate)
	if examDate != "" {
		if _, err := time.Parse("2006-01-02", examDate); err != nil {
			return fmt.Errorf("data da prova inválida (use AAAA-MM-DD)")
		}
	}
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.Goal = StudyGoal{Cert: cert, ExamDate: examDate, Level: strings.TrimSpace(level), SetAt: time.Now().Format("2006-01-02")}
	p.scheduleSave()
	return nil
}

// Journey resume a jornada persistida para o painel: objetivo (com dias
// restantes), streak e contagem honesta de outcomes.
type JourneySummary struct {
	Goal         StudyGoal      `json:"goal"`
	DaysToExam   int            `json:"days_to_exam"` // -1 = sem data
	Streak       StreakState    `json:"streak"`
	OutcomeCount map[string]int `json:"outcomes"`
}

func Journey(userID string) JourneySummary {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	js := JourneySummary{Goal: p.Goal, Streak: p.Streak, DaysToExam: -1, OutcomeCount: map[string]int{}}
	if p.Goal.ExamDate != "" {
		if d, err := time.Parse("2006-01-02", p.Goal.ExamDate); err == nil {
			js.DaysToExam = int(time.Until(d).Hours() / 24)
		}
	}
	for _, o := range p.Activity {
		if o != nil {
			js.OutcomeCount[o.State()]++
		}
	}
	return js
}

// OutcomesFor devolve o estado honesto de um conjunto de questões (fim de
// sessão): quantas aprovadas, puladas, com solução aberta etc.
func OutcomesFor(userID string, questionIDs []string) map[string]int {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]int{}
	for _, id := range questionIDs {
		o := p.Activity[strings.TrimSpace(id)]
		if o == nil {
			out["aberto"]++
			continue
		}
		out[o.State()]++
	}
	return out
}

// RecordTermError registra um erro de comando visto no terminal do lab,
// atribuído ao tópico da questão ativa do usuário.
func RecordTermError(userID string) {
	RecordTermErrorText(userID, "")
}

// RecordTermErrorText registra erro de terminal e também alimenta a
// observabilidade por lab quando há uma questão ativa.
func RecordTermErrorText(userID, output string) {
	p := profileFor(userID)
	p.mu.Lock()
	if p.activeTopic == "" {
		p.mu.Unlock()
		return
	}
	activeTopic := p.activeTopic
	activeCert := p.activeCert
	var q models.Question
	hasLab := false
	if p.activeLab != nil {
		q = *p.activeLab
		hasLab = true
	}
	p.skillFor(activeCert, activeTopic).TermErrors++
	p.scheduleSave()
	p.mu.Unlock()
	if hasLab {
		recordLabTerminalError(userID, q, output)
	}
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

func ReviewQueue(userID string) []ReviewItem {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]ReviewItem, 0, len(p.Review))
	for _, item := range p.Review {
		if item == nil {
			continue
		}
		cp := *item
		cp.Ready = !cp.Due.After(now)
		out = append(out, cp)
	}
	sortReview(out)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func DomainMap(userID, cert string) []DomainMastery {
	cert = CanonicalCert(cert)
	if cert == "" {
		cert = "CKA"
	}
	cur, ok := CurriculumFor(cert)
	if !ok {
		return nil
	}
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	out := make([]DomainMastery, 0, len(cur))
	for _, d := range cur {
		domainNorm := normalizeEvidenceText(d.Domain)
		var scoreSum float64
		var attempts int
		for _, s := range p.Skills {
			if s == nil || !strings.EqualFold(s.Cert, cert) {
				continue
			}
			topicNorm := normalizeEvidenceText(s.Topic)
			if strings.Contains(domainNorm, topicNorm) || strings.Contains(topicNorm, domainNorm) || domainMatchesTopic(domainNorm, topicNorm) {
				scoreSum += s.Score * float64(s.Attempts)
				attempts += s.Attempts
			}
		}
		due := 0
		for _, item := range p.Review {
			if item == nil || !strings.EqualFold(item.Cert, cert) || item.Due.After(now) {
				continue
			}
			topicNorm := normalizeEvidenceText(item.Topic)
			if strings.Contains(domainNorm, topicNorm) || strings.Contains(topicNorm, domainNorm) || domainMatchesTopic(domainNorm, topicNorm) {
				due++
			}
		}
		score := 0.0
		if attempts > 0 {
			score = scoreSum / float64(attempts)
		}
		srcs := append([]string{}, d.URLs...)
		if len(srcs) > 2 {
			srcs = srcs[:2]
		}
		out = append(out, DomainMastery{
			Cert:       cert,
			Domain:     d.Domain,
			Weight:     d.Weight,
			Score:      score,
			Attempts:   attempts,
			DueReviews: due,
			Sources:    srcs,
		})
	}
	return out
}

func sortReview(items []ReviewItem) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Ready != items[j].Ready {
			return items[i].Ready
		}
		if !items[i].Due.Equal(items[j].Due) {
			return items[i].Due.Before(items[j].Due)
		}
		return items[i].Failures > items[j].Failures
	})
}
