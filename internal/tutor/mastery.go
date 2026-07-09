package tutor

import (
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mastery gate — a trilha não libera o próximo tópico enquanto o atual não for
// dominado. Fecha a seta "não avançar sem skill ≥ limiar" do docs/game-change.
//
// "Dominado" liga as DUAS setas do loop: exige acerto consistente (score/EWMA),
// amostra suficiente (não dá pra dominar com 1 tentativa de sorte) E nenhuma
// revisão vencida — se o spaced-repetition ainda cobra o tópico, ele não foi
// retido, logo não está dominado. Sem esse último termo, "passar" e "reter"
// ficariam desacoplados.
// ─────────────────────────────────────────────────────────────────────────────

const (
	masteryBar         = 0.75 // EWMA de acerto mínimo para dominar um tópico
	minMasteryAttempts = 3    // amostra mínima; abaixo disso, nunca "dominado"
)

// MasteryStatus é o estado de um tópico dentro de uma trilha gated.
type MasteryStatus string

const (
	Mastered   MasteryStatus = "mastered" // domínio comprovado — liberado, serve de revisão
	CurrentGap MasteryStatus = "current"  // a fronteira: onde o aluno deve trabalhar agora
	Locked     MasteryStatus = "locked"   // ainda travado; depende de dominar os anteriores
)

// TopicMastery é o veredito de um tópico para a UI da trilha.
type TopicMastery struct {
	Topic      string        `json:"topic"`
	Score      float64       `json:"score"`
	Attempts   int           `json:"attempts"`
	DueReviews int           `json:"due_reviews"`
	Status     MasteryStatus `json:"status"`
}

// topicMasteredLocked decide domínio a partir do skill e das revisões vencidas.
// O caller deve segurar p.mu (lê p.Skills/p.Review diretamente para não
// re-travar o mutex não-reentrante via Stats/ReviewQueue).
func (p *Profile) topicMasteredLocked(cert, topic string, now time.Time) (TopicMastery, bool) {
	tm := TopicMastery{Topic: topic}
	if s := p.Skills[cert+"|"+topic]; s != nil {
		tm.Score = s.Score
		tm.Attempts = s.Attempts
	}
	for _, item := range p.Review {
		if item == nil || item.Due.After(now) {
			continue
		}
		if strings.EqualFold(item.Cert, cert) && strings.EqualFold(item.Topic, topic) {
			tm.DueReviews++
		}
	}
	mastered := tm.Attempts >= minMasteryAttempts && tm.Score >= masteryBar && tm.DueReviews == 0
	return tm, mastered
}

// MasteryPath classifica os tópicos de uma trilha em dominado/fronteira/travado.
// A fronteira é o PRIMEIRO tópico não dominado: nele o aluno trabalha; tudo
// depois fica travado até ele cair. Tópicos anteriores dominados seguem
// liberados (viram revisão). Determinístico, sem LLM.
func MasteryPath(userID, cert string, topics []string) []TopicMastery {
	cert = CanonicalCert(cert)
	if cert == "" {
		cert = "CKA"
	}
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()

	out := make([]TopicMastery, 0, len(topics))
	frontierSet := false
	for _, topic := range topics {
		tm, mastered := p.topicMasteredLocked(cert, topic, now)
		switch {
		case mastered:
			tm.Status = Mastered
		case !frontierSet:
			tm.Status = CurrentGap
			frontierSet = true
		default:
			tm.Status = Locked
		}
		out = append(out, tm)
	}
	return out
}

// MasteryPathForCert monta a visão de mastery da certificação para o dashboard,
// usando a mesma sequência de tópicos que a trilha usaria (fonte única).
func MasteryPathForCert(userID, cert string) []TopicMastery {
	cert = CanonicalCert(cert)
	if cert == "" {
		cert = "CKA"
	}
	topics := fallbackTopicsForCert(cert, "")
	if len(topics) == 0 {
		return nil
	}
	return MasteryPath(userID, cert, topics)
}

// unlockedTopics devolve os tópicos que o aluno pode praticar agora: os já
// dominados (revisão) e a fronteira atual. Os travados ficam de fora da geração
// — é o gate de fato, não só um rótulo na UI.
func unlockedTopics(mastery []TopicMastery) []string {
	var out []string
	for _, tm := range mastery {
		if tm.Status == Locked {
			break
		}
		out = append(out, tm.Topic)
	}
	return out
}
