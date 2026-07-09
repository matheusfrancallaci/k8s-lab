package tutor

import (
	"strings"
	"testing"
)

func TestRedactSolutionCommandsHidesMutatingCommands(t *testing.T) {
	cases := []string{
		"Você precisa rodar kubectl expose deploy titan-cache --port=80 para criar o Service.",
		"O Service não existe. Tente `kubectl create service clusterip titan-cache --tcp=80:80`.",
		"Falta aplicar o manifest:\n```\nkubectl apply -f service.yaml\n```\nDepois valide.",
		"Rode terraform apply -auto-approve no workspace.",
		"Execute ansible-playbook site.yml para convergir.",
		"Basta um kubectl scale deploy/titan-cache --replicas=3.",
	}
	for _, in := range cases {
		got := RedactSolutionCommands(in, "")
		if commandLikeRe.MatchString(got) && mutatingCmdRe.MatchString(got) {
			t.Fatalf("comando mutante sobreviveu à redação:\nentrada: %q\nsaída:   %q", in, got)
		}
		if got == "" {
			t.Fatalf("redação esvaziou a explicação: %q", in)
		}
	}
}

func TestRedactSolutionCommandsKeepsDiagnostics(t *testing.T) {
	// Mandar o aluno investigar é o núcleo da tutoria — não pode ser redigido.
	diag := []string{
		"Rode kubectl get pods -n lab-x e veja se o Pod está Running.",
		"Use kubectl describe svc titan-cache-svc para conferir o selector.",
		"Confira kubectl logs deploy/titan-cache antes de concluir.",
	}
	for _, in := range diag {
		if got := RedactSolutionCommands(in, ""); got != in {
			t.Fatalf("comando de diagnóstico foi redigido:\nentrada: %q\nsaída:   %q", in, got)
		}
	}
}

func TestRedactSolutionCommandsHidesAnswerCommand(t *testing.T) {
	// Labs de Bash/Java têm gabarito que não casa com os verbos mutantes.
	answer := "javac Solucao.java && java Solucao"
	in := "Quase lá! Só falta compilar: javac Solucao.java && java Solucao"
	got := RedactSolutionCommands(in, answer)
	if got == in {
		t.Fatalf("gabarito literal nao foi redigido: %q", got)
	}
	if want := "o comando apropriado"; !strings.Contains(got, want) {
		t.Fatalf("esperava placeholder %q em %q", want, got)
	}
}

func TestRedactSolutionCommandsEmptyInput(t *testing.T) {
	if got := RedactSolutionCommands("", "kubectl apply -f x.yaml"); got != "" {
		t.Fatalf("entrada vazia deveria continuar vazia, veio %q", got)
	}
}
