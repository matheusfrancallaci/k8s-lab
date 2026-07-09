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
//
// Revisões vencidas (spaced repetition) vêm PRIMEIRO: são a seta "reforçar no
// momento certo" do loop, e o dado já existe no caderno — antes o Advise era
// cego a ele e o aluno só via revisão se pedisse. Um tópico com revisão vencida
// não recebe também o nudge de skill (evita cobrar o mesmo tópico duas vezes).
func Advise(userID string) []Recommendation {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()

	var recs []Recommendation
	now := time.Now()

	reviewRecs, reviewed := p.dueReviewRecsLocked(now)

	for key, s := range p.Skills {
		if t, ok := p.lastAdvised[key]; ok && now.Sub(t) < adviseCooldown {
			continue
		}
		if reviewed[s.Cert+"|"+s.Topic] {
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
	// Revisões vencidas na frente de tudo (o sort acima é só entre nudges de skill).
	return append(reviewRecs, recs...)
}

// dueReviewRecsLocked transforma revisões vencidas em recomendações (caller
// segura p.mu). Uma por tópico, mais vencida primeiro, no máx. 3 — o replay vem
// "sem mão na roda" (Level 1) para valer como recuperação de memória de prova.
func (p *Profile) dueReviewRecsLocked(now time.Time) ([]Recommendation, map[string]bool) {
	type due struct {
		cert, topic string
		overdue     time.Duration
		failures    int
	}
	worst := map[string]due{}
	for _, item := range p.Review {
		if item == nil || item.Due.After(now) {
			continue
		}
		key := item.Cert + "|" + item.Topic
		d := due{cert: item.Cert, topic: item.Topic, overdue: now.Sub(item.Due), failures: item.Failures}
		if cur, ok := worst[key]; !ok || d.overdue > cur.overdue {
			worst[key] = d
		}
	}
	if len(worst) == 0 {
		return nil, nil
	}
	ordered := make([]due, 0, len(worst))
	for _, d := range worst {
		ordered = append(ordered, d)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].overdue > ordered[j].overdue })
	if len(ordered) > 3 {
		ordered = ordered[:3]
	}
	reviewed := map[string]bool{}
	recs := make([]Recommendation, 0, len(ordered))
	for _, d := range ordered {
		reviewed[d.cert+"|"+d.topic] = true
		recs = append(recs, Recommendation{
			Cert: d.cert, Topic: d.topic, Level: 1,
			Reason: reviewReason(d.topic, d.overdue, d.failures),
		})
	}
	return recs, reviewed
}

func reviewReason(topic string, overdue time.Duration, failures int) string {
	when := "hoje"
	if days := int(overdue.Hours() / 24); days >= 1 {
		when = fmt.Sprintf("ha %d dia(s)", days)
	}
	if failures > 0 {
		return fmt.Sprintf("revisao de %s venceu %s — voce ja tropecou aqui; refaz sem dica para fixar", topic, when)
	}
	return fmt.Sprintf("revisao de %s venceu %s — reforco espacado para nao esquecer antes da prova", topic, when)
}

// MarkAdvised inicia o cooldown de uma recomendação (aceita ou dispensada).
func MarkAdvised(userID, cert, topic string) {
	p := profileFor(userID)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAdvised[cert+"|"+topic] = time.Now()
}
