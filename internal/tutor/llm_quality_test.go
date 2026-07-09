package tutor

import (
	"strings"
	"testing"
)

func TestShuffleOptionsPreservesCorrectAndSpreads(t *testing.T) {
	opts := []string{"kube-scheduler", "kubelet", "kube-proxy", "etcd"}
	positions := map[int]bool{}
	for i := 0; i < 200; i++ {
		shuffled, ans := shuffleOptions(opts, 0)
		if shuffled[ans] != "kube-scheduler" {
			t.Fatalf("resposta correta se perdeu no shuffle: idx=%d opts=%v", ans, shuffled)
		}
		if len(shuffled) != 4 {
			t.Fatalf("shuffle mudou o numero de opcoes: %v", shuffled)
		}
		positions[ans] = true
	}
	// Anti-viés: em 200 embaralhadas a correta precisa ter visitado as 4 posições.
	if len(positions) < 4 {
		t.Fatalf("correta nao circulou pelas posicoes (vicio de posicao persiste): %v", positions)
	}
}

func TestGroundedInSourceAcceptsExtractedRejectsInvented(t *testing.T) {
	source := `O kube-scheduler avalia os nós elegíveis e faz o bind do pod ao nó
escolhido, respeitando requests, taints e afinidade. O kubelet executa os pods
agendados em cada nó e reporta o status ao control plane.`

	// Resposta extraída do material: ancorada.
	if !groundedInSource("Qual componente faz o bind do pod ao nó?", "kube-scheduler", source) {
		t.Fatal("resposta presente no material foi rejeitada")
	}
	// Comparação inventada (vocabulário do modelo, não da fonte) — o caso real
	// visto em gen-20260704: 'Core Concepts aborda X, enquanto Fundamentals...'.
	invented := "Kubernetes Core Concepts é mais focado em aplicativos cloud-native, enquanto Kubernetes Fundamentals é mais focado em sistemas de produção"
	if groundedInSource("Qual é a principal diferença entre Core Concepts e Fundamentals?", invented, source) {
		t.Fatal("resposta inventada (sem ancora no material) foi aceita")
	}
}

func TestFinalizeExplanationFallsBackWhenGutted(t *testing.T) {
	// Modelo respondeu SÓ com o comando: o guard redige e sobra nada útil.
	out := finalizeExplanation("Rode kubectl expose deploy titan-cache --port=80", "", "Service titan-cache-svc responde no DNS interno")
	if strings.Contains(out, "kubectl expose") {
		t.Fatalf("comando de solucao sobreviveu: %q", out)
	}
	if !strings.Contains(out, "titan-cache-svc") {
		t.Fatalf("fallback deveria citar o goal para orientar: %q", out)
	}
	// Prosa boa passa intacta.
	prose := "O Service existe mas o selector nao casa com os labels do pod. Compare os labels com kubectl describe e ajuste o selector."
	if got := finalizeExplanation(prose, "", "goal"); got != prose {
		t.Fatalf("explicacao legitima foi alterada:\n%q\n%q", prose, got)
	}
}
