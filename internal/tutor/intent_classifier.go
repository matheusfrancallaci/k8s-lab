package tutor

import (
	"regexp"
	"strings"
)

type TutorIntentDecision struct {
	Intent     string `json:"intent"`
	Topic      string `json:"topic,omitempty"`
	Cert       string `json:"cert"`
	Count      int    `json:"count"`
	Level      int    `json:"level"`
	Confidence int    `json:"confidence"`
}

var (
	progressIntentRe = regexp.MustCompile(`(?i)\b(desempenho|progresso|como\s+estou|estat[ií]stic(?:a|as|o|os)?|stats?|dashboard|pontos?\s+fracos?)\b`)
	labIntentRe      = regexp.MustCompile(`(?i)\b(labs?|laborat[oó]rios?|hands.?on|exerc[ií]cios?|praticar|treinar)\b`)
	examIntentRe     = regexp.MustCompile(`(?i)\b(exame|simulado|prova\s+completa)\b`)
	reviewIntentRe   = regexp.MustCompile(`(?i)\b(revisar?|refazer\s+erro|caderno\s+de\s+erros|refor[cç]o)\b`)
)

func ClassifyTutorRequest(msg, activeCert string) TutorIntentDecision {
	text := strings.TrimSpace(msg)
	cert := inferCertFromMessage(text, activeCert)
	topic := exactTopicForRequest(cert, text)
	if topic == "" {
		topic = detectTopic(text)
	}
	d := TutorIntentDecision{Intent: "explain", Topic: topic, Cert: cert, Count: detectCount(text, 5), Level: detectLevel(text), Confidence: 72}
	switch {
	case labIntentRe.MatchString(text) && (isBroadLabRequest(text) || explicitTutorCommand(text)):
		d.Intent, d.Confidence = "create_lab", 98
	case examIntentRe.MatchString(text):
		d.Intent, d.Confidence = "exam", 96
	case reviewIntentRe.MatchString(text):
		d.Intent, d.Confidence = "review", 94
	case progressIntentRe.MatchString(text):
		d.Intent, d.Confidence = "progress", 96
	case regexp.MustCompile(`(?i)\b(erro|falha|incidente|diagn[oó]stico|nao\s+funciona|não\s+funciona)\b`).MatchString(text):
		d.Intent, d.Confidence = "diagnose", 90
	case regexp.MustCompile(`(?i)\b(compare|diferen[cç]a|trade.?off|quando\s+usar)\b`).MatchString(text):
		d.Intent, d.Confidence = "compare", 90
	}
	return d
}
