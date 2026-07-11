package handlers

import (
	"strings"
	"testing"

	"estudo-app/internal/models"
)

func TestPrewarmIsDisabledWithoutCluster(t *testing.T) {
	t.Setenv("LAB_NO_CLUSTER", "1")
	prewarmStatusStore.Lock()
	prewarmStatusStore.ByContext = map[string]PrewarmStatus{}
	prewarmStatusStore.Unlock()
	PrewarmLabImages([]models.Question{{AnswerCommand: "kubectl run web --image=nginx:1.25"}})
	prewarmStatusStore.Lock()
	defer prewarmStatusStore.Unlock()
	if len(prewarmStatusStore.ByContext) != 0 {
		t.Fatal("prewarm nao pode tocar cluster em modo LAB_NO_CLUSTER")
	}
}

func TestGeneratedToolImagesAreVersionPinned(t *testing.T) {
	if isPlaceholderImage("localstack/localstack:2026.06.2") {
		t.Fatal("imagem versionada foi tratada como placeholder")
	}
	for _, tool := range toolCatalog {
		for _, step := range tool.steps {
			if step.Cmd != "" && containsMutableLatestImage(step.Cmd) {
				t.Fatalf("ferramenta %s usa imagem mutavel: %s", tool.ID, step.Cmd)
			}
		}
	}
}

func containsMutableLatestImage(s string) bool {
	return strings.Contains(strings.ToLower(s), ":latest")
}
