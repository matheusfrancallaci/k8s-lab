package tutor

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// O GC só toca conteúdo GERADO (gen-*.yaml) e respeita TTL + cap.
func TestPruneGeneratedQuestionsRespectsTTLAndCap(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("QUESTIONS_CUSTOM_DIR", dir)

	write := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("questions: []"), 0o644); err != nil {
			t.Fatal(err)
		}
		mod := time.Now().Add(-age)
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	write("gen-velho.yaml", 30*24*time.Hour)
	write("gen-novo.yaml", time.Hour)
	write("gen-medio.yaml", 2*24*time.Hour)
	write("curado-manual.yaml", 90*24*time.Hour) // não é gen-*: intocável

	if removed := PruneGeneratedQuestions(14*24*time.Hour, 0); removed != 1 {
		t.Fatalf("TTL de 14d deveria remover só o velho, removeu %d", removed)
	}
	if removed := PruneGeneratedQuestions(0, 1); removed != 1 {
		t.Fatalf("cap de 1 deveria remover o excedente (médio), removeu %d", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "gen-novo.yaml")); err != nil {
		t.Fatalf("o mais novo nunca pode ser removido: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "curado-manual.yaml")); err != nil {
		t.Fatalf("arquivo fora do padrão gen-* é intocável: %v", err)
	}
}

// O currículo aprendido persiste e passa a valer no CurriculumFor — a cert
// vira primeira classe sem código novo.
func TestLearnedCurriculumPersistsAndServesCurriculumFor(t *testing.T) {
	t.Setenv("LEARNED_CURRICULA_PATH", filepath.Join(t.TempDir(), "curricula.json"))
	resetLearnedCurriculaForTest()

	learnedCurMu.Lock()
	st := ensureLearnedLocked()
	st["MinhaCert"] = []CurriculumDomain{
		{Domain: "Fundamentos", Weight: 60, URLs: []string{"https://kubernetes.io/docs/"}},
		{Domain: "Operacao", Weight: 40},
	}
	saveLearnedLocked(st)
	learnedCurMu.Unlock()

	cur, ok := CurriculumFor("minhacert")
	if !ok || len(cur) != 2 || cur[0].Weight != 60 {
		t.Fatalf("currículo aprendido deveria valer no CurriculumFor: %v %v", cur, ok)
	}

	// recarrega do disco (novo processo)
	resetLearnedCurriculaForTest()
	learnedCurMu.Lock()
	learnedCurLoaded = false
	learnedCurMu.Unlock()
	if cur, ok := CurriculumFor("MinhaCert"); !ok || len(cur) != 2 {
		t.Fatalf("currículo aprendido deveria sobreviver a restart: %v %v", cur, ok)
	}

	// embutido tem precedência
	if cur, ok := CurriculumFor("CKA"); !ok || cur[0].Domain != "Troubleshooting" {
		t.Fatalf("embutido deveria ter precedência: %v", cur)
	}
}
