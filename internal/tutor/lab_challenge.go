package tutor

import (
	"fmt"
	"regexp"
	"strings"

	"estudo-app/internal/models"
)

var (
	commandLikeRe       = regexp.MustCompile(`(?i)\b(kubectl|terraform|ansible-playbook|awslocal|javac|java|bash|chmod|grep|awk|sed|cat|mkdir|printf|echo)\b`)
	numberedStepRe      = regexp.MustCompile(`^\s*(?:\d+[.)]|[-*])\s+`)
	inlineCommandRe     = regexp.MustCompile("`([^`]+)`")
	// O modelo de geração responde em PT ou EN conforme o prompt/documento, então
	// os cabeçalhos de gabarito precisam ser reconhecidos nos dois idiomas.
	spoilerHeadingRe = regexp.MustCompile(`(?i)\b(dica|hint|passo a passo|step[\s-]?by[\s-]?step|steps|comando completo|full command|solu[cç][aã]o|solution|gabarito|resposta|answer)\s*:`)
	workspaceHintRe     = regexp.MustCompile(`(?i)\b(cd\s+\$TFLAB|workspace|diret[oó]rio)\b`)
	fullCommandPrefixRe = regexp.MustCompile(`(?i)^\s*(comando completo|use|execute|rode)\s*:\s*`)
)

// HideLabSpoilers keeps labs in challenge mode: the main task says what must be
// built, while concrete commands stay in Hint/Solution.
func HideLabSpoilers(q models.Question) models.Question {
	if q.Type != models.Lab {
		return q
	}
	q.Question = challengeText(q)
	q.Hint = compactHint(q.Hint, q)
	for i := range q.Goals {
		q.Goals[i].Hint = compactHint(q.Goals[i].Hint, q)
	}
	return q
}

func challengeText(q models.Question) string {
	raw := strings.TrimSpace(q.Question)
	if raw == "" {
		raw = labObjective(q)
	}
	var out []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		low := strings.ToLower(line)
		if spoilerHeadingRe.MatchString(line) {
			break
		}
		if numberedStepRe.MatchString(line) && commandLikeRe.MatchString(line) {
			continue
		}
		if strings.Contains(low, "comando completo") || strings.Contains(low, "gabarito") {
			continue
		}
		if strings.Contains(line, "`") && commandLikeRe.MatchString(line) && !workspaceHintRe.MatchString(line) {
			line = inlineCommandRe.ReplaceAllStringFunc(line, func(s string) string {
				m := inlineCommandRe.FindStringSubmatch(s)
				if len(m) == 2 && commandLikeRe.MatchString(m[1]) {
					return "`comando apropriado`"
				}
				return s
			})
		}
		out = append(out, line)
	}
	text := strings.TrimSpace(strings.Join(out, "\n"))
	if text == "" {
		text = fallbackChallenge(q)
	}
	if q.Topic != "" && !strings.Contains(strings.ToLower(text), strings.ToLower(q.Topic)) {
		text = fmt.Sprintf("%s\n\nFoco do treino: **%s**.", text, q.Topic)
	}
	if !strings.Contains(strings.ToLower(text), "aba hint") {
		text += "\n\nResolva pelo terminal. Use a aba HINT se travar; o gabarito completo fica na aba SOLUTION."
	}
	return text
}

func fallbackChallenge(q models.Question) string {
	obj := labObjective(q)
	if q.Topic != "" {
		return fmt.Sprintf("%s em um cenário prático de %s.", obj, q.Topic)
	}
	return obj
}

func compactHint(h string, q models.Question) string {
	h = strings.TrimSpace(h)
	if h == "" {
		return h
	}
	low := strings.ToLower(h)
	if strings.Contains(low, "comando completo") || strings.Contains(low, "gabarito") {
		return "Pense no recurso esperado e compare com os goals. Se precisar do comando pronto, abra SOLUTION."
	}
	if q.AnswerCommand != "" && strings.Contains(h, q.AnswerCommand) {
		return "Quebre a tarefa em passos pequenos: criar recurso, ajustar configuracao e validar os goals."
	}
	if fullCommandPrefixRe.MatchString(h) && commandLikeRe.MatchString(h) {
		return fullCommandPrefixRe.ReplaceAllString(h, "")
	}
	return h
}
