package tutor

import (
	"regexp"
	"strings"
)

// O prompt do LLM pede para não entregar o comando da resposta, mas prompt não é
// contrato: modelos pequenos (qwen2.5:1.5b / qwen2.5-coder:3b) desobedecem com
// frequência. Toda saída de modelo que chega ao aluno DURANTE o lab passa por
// aqui antes de virar conteúdo.
//
// A regra não é "esconder todo comando": `kubectl get/describe/logs` é
// diagnóstico e é justamente o que um bom tutor manda o aluno rodar. O que não
// pode vazar é o comando que RESOLVE o lab — os verbos que mutam o cluster.
var (
	mutatingCmdRe = regexp.MustCompile("(?i)\\b(kubectl\\s+(create|apply|expose|run|set|scale|edit|patch|delete|annotate|label|autoscale|rollout|cordon|drain|taint)|terraform\\s+(apply|destroy|import)|ansible-playbook)\\b[^\\n`]*")
	fencedBlockRe = regexp.MustCompile("(?s)```.*?```")

	// Placeholder alinhado com o que o HideLabSpoilers já usa no enunciado.
	commandPlaceholder = "o comando apropriado"
)

// RedactSolutionCommands remove de um texto gerado por LLM qualquer comando
// capaz de resolver o lab no lugar do aluno. answerCommand, quando conhecido, é
// removido literalmente mesmo que não case com os verbos mutantes (o gabarito de
// um lab de Bash/Java é um comando qualquer).
func RedactSolutionCommands(text, answerCommand string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}

	// Um bloco de código no meio de uma explicação em prosa é quase sempre o
	// gabarito copiado; some com o bloco inteiro em vez de tentar remendá-lo.
	text = fencedBlockRe.ReplaceAllStringFunc(text, func(block string) string {
		if mutatingCmdRe.MatchString(block) || containsCommand(block, answerCommand) {
			return commandPlaceholder
		}
		return block
	})

	if ac := strings.TrimSpace(answerCommand); ac != "" {
		text = strings.ReplaceAll(text, ac, commandPlaceholder)
	}

	text = mutatingCmdRe.ReplaceAllString(text, commandPlaceholder)

	// A redação pode deixar crases órfãs (`o comando apropriado`) e espaços
	// duplicados onde o comando foi extraído do meio da frase.
	text = strings.ReplaceAll(text, "`"+commandPlaceholder+"`", commandPlaceholder)
	return strings.TrimSpace(collapseSpacesRe.ReplaceAllString(text, " "))
}

var collapseSpacesRe = regexp.MustCompile(`[ \t]{2,}`)

func containsCommand(text, answerCommand string) bool {
	ac := strings.TrimSpace(answerCommand)
	return ac != "" && strings.Contains(text, ac)
}
