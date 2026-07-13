package tutor

import (
	"regexp"
	"strings"
)

type PedagogyAudit struct {
	Score         int      `json:"score"`
	Strategy      string   `json:"strategy"`
	Strengths     []string `json:"strengths,omitempty"`
	Missing       []string `json:"missing,omitempty"`
	HasCheckpoint bool     `json:"has_checkpoint"`
	HasNextStep   bool     `json:"has_next_step"`
	SpoilerRisk   bool     `json:"spoiler_risk"`
}

func AuditTeachingResponse(reply string, plan TutorOrchestration) PedagogyAudit {
	l := strings.ToLower(reply)
	a := PedagogyAudit{Strategy: plan.Strategy}
	add := func(ok bool, points int, label string) {
		if ok {
			a.Score += points
			a.Strengths = append(a.Strengths, label)
		} else {
			a.Missing = append(a.Missing, label)
		}
	}
	clear := len(strings.Fields(reply)) >= 12
	add(clear, 20, "explicacao substancial")
	a.HasCheckpoint = regexp.MustCompile(`(?i)(consegue|qual seria|o que voce|tente|verifique|confirme|checkpoint|agora você|agora voce)`).MatchString(reply)
	add(a.HasCheckpoint, 20, "checkpoint ativo")
	a.HasNextStep = regexp.MustCompile(`(?i)(proximo passo|próximo passo|agora|depois|em seguida|lab|pratic)`).MatchString(reply)
	add(a.HasNextStep, 20, "proximo passo")
	strategyApplied := true
	switch plan.Strategy {
	case "hypothesis-evidence-test":
		strategyApplied = regexp.MustCompile(`(?i)hip[oó]tese|evid[eê]ncia|teste|sintoma`).MatchString(reply)
	case "tradeoff-and-counterexample":
		strategyApplied = strings.Contains(l, "trade-off") || strings.Contains(l, "tradeoff") || strings.Contains(l, "por outro lado") || strings.Contains(l, "depende")
	case "guided-discovery":
		strategyApplied = a.HasCheckpoint
	case "retrieval-practice":
		strategyApplied = regexp.MustCompile(`(?i)lembre|sem consultar|recupere|tente responder|revis`).MatchString(reply)
	}
	add(strategyApplied, 25, "estrategia aplicada")
	a.SpoilerRisk = plan.Intent == "practice" && regexp.MustCompile(`(?i)(solucao completa|solução completa|copie e cole|resposta final)`).MatchString(reply)
	add(!a.SpoilerRisk, 15, "sem spoiler indevido")
	if a.Score > 100 {
		a.Score = 100
	}
	return a
}
