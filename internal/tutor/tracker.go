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

// Profile é o estado adaptativo de UM usuário (isolado dos demais).
type Profile struct {
	mu          sync.Mutex
	id          string
	Skills      map[string]*TopicSkill `json:"skills"`
	Review      map[string]*ReviewItem `json:"review"`
	History     []ProfileSnapshot      `json:"history,omitempty"`
	Memory      LearningMemory         `json:"memory,omitempty"`
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
	Skills  map[string]*TopicSkill `json:"skills"`
	Review  map[string]*ReviewItem `json:"review,omitempty"`
	History []ProfileSnapshot      `json:"history,omitempty"`
	Memory  LearningMemory         `json:"memory,omitempty"`
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
		refreshLearningMemoryLocked(p)
		b, err := json.MarshalIndent(skillsDoc{Skills: p.Skills, Review: p.Review, History: p.History, Memory: p.Memory}, "", "  ")
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
	p.snapshotLocked(now)
	p.scheduleSave()
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
