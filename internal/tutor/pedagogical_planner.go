package tutor

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type LearningMemory struct {
	Summary       string    `json:"summary"`
	StrongTopics  []string  `json:"strong_topics,omitempty"`
	CurrentGaps   []string  `json:"current_gaps,omitempty"`
	PreferredMode string    `json:"preferred_mode"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type TutorDecision struct {
	Cert            string   `json:"cert"`
	TargetTopic     string   `json:"target_topic"`
	LikelyGap       string   `json:"likely_gap"`
	Evidence        []string `json:"evidence"`
	Prerequisites   []string `json:"prerequisites,omitempty"`
	Strategy        string   `json:"strategy"`
	Activity        string   `json:"activity"`
	SuccessCriteria []string `json:"success_criteria"`
	Confidence      int      `json:"confidence"`
	Explanation     string   `json:"explanation"`
}

var competencyPrerequisites = map[string][]string{
	"Autoscaling":        {"Workloads", "Configuration"},
	"Services":           {"Core Concepts", "Workloads"},
	"Storage":            {"Core Concepts"},
	"Security":           {"Core Concepts", "Services"},
	"Scheduling":         {"Core Concepts", "Workloads"},
	"GitOps":             {"Workloads", "Services", "Configuration"},
	"AWS Networking":     {"AWS IAM"},
	"AWS Compute":        {"AWS IAM", "AWS Networking"},
	"AWS Messaging":      {"AWS IAM"},
	"Application Design": {"Workloads", "Configuration"},
}

func refreshLearningMemoryLocked(p *Profile) {
	type ranked struct {
		topic    string
		score    float64
		attempts int
		failures int
	}
	var all []ranked
	for _, skill := range p.Skills {
		all = append(all, ranked{skill.Topic, skill.Score, skill.Attempts, skill.Failures})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].score == all[j].score {
			return all[i].attempts > all[j].attempts
		}
		return all[i].score < all[j].score
	})
	p.Memory.CurrentGaps = nil
	p.Memory.StrongTopics = nil
	for _, item := range all {
		if item.attempts > 0 && item.score < .65 && len(p.Memory.CurrentGaps) < 4 {
			p.Memory.CurrentGaps = append(p.Memory.CurrentGaps, item.topic)
		}
	}
	for i := len(all) - 1; i >= 0 && len(p.Memory.StrongTopics) < 4; i-- {
		if all[i].attempts >= 3 && all[i].score >= .80 {
			p.Memory.StrongTopics = append(p.Memory.StrongTopics, all[i].topic)
		}
	}
	p.Memory.PreferredMode = "guided-lab"
	p.Memory.Summary = fmt.Sprintf("lacunas: %s; pontos fortes: %s", strings.Join(p.Memory.CurrentGaps, ", "), strings.Join(p.Memory.StrongTopics, ", "))
	p.Memory.UpdatedAt = time.Now().UTC()
}

func LearningMemoryFor(userID string) LearningMemory {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	refreshLearningMemoryLocked(p)
	return p.Memory
}

func BuildTutorDecision(userID, msg, cert string) TutorDecision {
	if cert == "" {
		cert = "CKA"
	}
	topic := exactTopicForRequest(cert, msg)
	if topic == "" {
		topic = detectTopic(msg)
	}
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	refreshLearningMemoryLocked(p)
	if topic == "" && len(p.Memory.CurrentGaps) > 0 {
		topic = p.Memory.CurrentGaps[0]
	}
	if topic == "" {
		topic = "Core Concepts"
	}
	d := TutorDecision{Cert: cert, TargetTopic: topic, Prerequisites: append([]string{}, competencyPrerequisites[topic]...), Confidence: 65}
	key := cert + "|" + topic
	if skill := p.Skills[key]; skill != nil {
		d.Confidence = 90
		d.Evidence = append(d.Evidence, fmt.Sprintf("%d tentativas, %.0f%% de dominio, %d falhas, %d dicas", skill.Attempts, skill.Score*100, skill.Failures, skill.Hints))
		switch {
		case skill.FailStreak >= 2:
			d.LikelyGap = "modelo mental ou pre-requisito ainda instavel"
			d.Strategy, d.Activity = "diagnostico-socratico", "pergunta curta seguida de lab guiado"
		case skill.Hints > skill.Completed:
			d.LikelyGap = "dependencia excessiva de dicas"
			d.Strategy, d.Activity = "fading-hints", "lab com dicas progressivas ocultas"
		case skill.Score >= .80 && skill.Attempts >= 3:
			d.LikelyGap = "dominio comprovado; validar retencao e transferencia"
			d.Strategy, d.Activity = "desafio-transferencia", "incidente sem roteiro"
		default:
			d.LikelyGap = "habilidade ainda sem evidencia suficiente"
			d.Strategy, d.Activity = "pratica-deliberada", "lab exato com validacao automatica"
		}
	} else {
		d.LikelyGap = "topico ainda nao diagnosticado"
		d.Strategy, d.Activity = "diagnostico-inicial", "microdesafio antes da explicacao"
		d.Evidence = append(d.Evidence, "sem tentativas registradas neste topico")
	}
	d.SuccessCriteria = []string{"concluir sem abrir a solucao", "todos os validadores automaticos aprovados", "explicar por que a correcao funciona"}
	d.Explanation = "Decisao deterministica baseada no historico, nos pre-requisitos e no pedido atual; a LLM pode redigir, mas nao altera estes gates."
	return d
}
