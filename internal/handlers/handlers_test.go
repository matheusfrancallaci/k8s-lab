package handlers

import "testing"

func TestContextNameValidation(t *testing.T) {
	valid := []string{"minikube", "k8s-study-lab", "arn:aws:eks:us-east-1:123:cluster/prod",
		"gke_proj_us-central1-a_cluster", "user@cluster.local"}
	for _, v := range valid {
		if !contextNameRe.MatchString(v) {
			t.Errorf("contexto válido rejeitado: %q", v)
		}
	}
	invalid := []string{"", "minikube; rm -rf /", "x && curl evil", "a b", "$(whoami)", "ctx`id`", "x|y"}
	for _, v := range invalid {
		if contextNameRe.MatchString(v) {
			t.Errorf("injeção aceita: %q", v)
		}
	}
}

func TestIsPlaceholderImageViaPrewarm(t *testing.T) {
	// imagens quebradas de propósito nos enunciados nunca podem ir ao prewarm
	re := imageFlagRe
	cmd := "kubectl run x --image=nginx:INVALID_TAG --image=nginx:1.21"
	found := re.FindAllStringSubmatch(cmd, -1)
	if len(found) != 2 {
		t.Fatalf("regex de imagem deveria achar 2, achou %d", len(found))
	}
}
