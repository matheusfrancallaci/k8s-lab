package tutor

import (
	"fmt"
	"sort"
	"time"
)

// Recommendation é uma sugestão do tutor ao usuário.
type Recommendation struct {
	Cert   string `json:"cert"`
	Topic  string `json:"topic"`
	Level  int    `json:"level"` // 1-3: nível de ajuda do lab sugerido
	Reason string `json:"reason"`
}

const adviseCooldown = 10 * time.Minute

// Advise avalia o modelo de habilidade do usuário e devolve recomendações
// ativas, da mais urgente para a menos. Determinístico, sem LLM.
func Advise(userID string) []Recommendation {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()

	var recs []Recommendation
	now := time.Now()

	for key, s := range p.Skills {
		if t, ok := p.lastAdvised[key]; ok && now.Sub(t) < adviseCooldown {
			continue
		}

		var r *Recommendation
		switch {
		// Travou agora: 2+ falhas seguidas → ajuda máxima
		case s.FailStreak >= 2:
			r = &Recommendation{
				Cert: s.Cert, Topic: s.Topic, Level: 3,
				Reason: fmt.Sprintf("%d falhas seguidas em %s — que tal um lab guiado passo a passo?", s.FailStreak, s.Topic),
			}
		// Dificuldade persistente: score baixo com amostra razoável
		case s.Score < 0.5 && s.Attempts >= 4:
			r = &Recommendation{
				Cert: s.Cert, Topic: s.Topic, Level: 3,
				Reason: fmt.Sprintf("taxa de acerto em %s está em %.0f%% — um lab com explicação detalhada pode destravar", s.Topic, s.Score*100),
			}
		// Dependência de ajuda: muitos hints/solutions em relação às tentativas
		case s.Attempts >= 3 && (s.Hints+s.Solutions) > s.Attempts:
			r = &Recommendation{
				Cert: s.Cert, Topic: s.Topic, Level: 2,
				Reason: fmt.Sprintf("você tem consultado muitas dicas em %s — um lab intermediário reforça a memória", s.Topic),
			}
		// Erros de digitação/sintaxe recorrentes no terminal
		case s.TermErrors >= 5 && s.Attempts >= 2:
			r = &Recommendation{
				Cert: s.Cert, Topic: s.Topic, Level: 2,
				Reason: fmt.Sprintf("vários erros de comando em %s — pratique com hints de sintaxe", s.Topic),
			}
		}

		if r != nil {
			recs = append(recs, *r)
		}
	}

	// Mais urgente primeiro: nível desc, depois pior score
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Level != recs[j].Level {
			return recs[i].Level > recs[j].Level
		}
		si := p.Skills[recs[i].Cert+"|"+recs[i].Topic]
		sj := p.Skills[recs[j].Cert+"|"+recs[j].Topic]
		return si.Score < sj.Score
	})
	return recs
}

// MarkAdvised inicia o cooldown de uma recomendação (aceita ou dispensada).
func MarkAdvised(userID, cert, topic string) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAdvised[cert+"|"+topic] = time.Now()
}
