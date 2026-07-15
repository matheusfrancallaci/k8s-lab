package tutor

import (
	"sort"
	"strings"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Dedup semântico de questões (Fatia 5) — o qKey do repositório só barra texto
// idêntico; gerações repetidas produzem paráfrases ("Qual componente agenda
// pods?" vs "Que componente decide o nó de um pod?") que passavam batido. Aqui
// comparamos por (1) conjunto de tokens normalizados e (2) proximidade de
// embeddings (o mesmo sinal denso do RAG), barrando quase-duplicatas antes de
// publicar. Usa localEmbedding: determinístico, custo zero e suficiente para
// near-dup de textos curtos.
// ─────────────────────────────────────────────────────────────────────────────

// Calibrado com dados: paráfrases do mesmo enunciado ficam ~0.90 e questões
// genuinamente distintas (mesmo tópico) ≤0.33 — 0.88 separa com folga.
const mcqDupThreshold = 0.88

type mcqDedup struct {
	stems map[string]bool // enunciados já vistos (chave por tokens normalizados)
	vecs  [][]float64     // embeddings do enunciado para near-dup por paráfrase
}

// newMCQDedup indexa as questões de múltipla escolha existentes da mesma cert.
func newMCQDedup(existing []models.Question, cert string) *mcqDedup {
	d := &mcqDedup{stems: map[string]bool{}}
	for _, q := range existing {
		if q.Type != models.MultipleChoice || !strings.EqualFold(string(q.Cert), cert) {
			continue
		}
		d.remember(q)
	}
	return d
}

// mcqDedupText é a identidade da questão: o ENUNCIADO. Deliberadamente NÃO inclui
// a resposta — modelos fracos reemitem o mesmo enunciado com respostas
// diferentes (às vezes contraditórias); tratar cada par como único deixaria
// passar questões incoerentes. Mesmo enunciado = mesma questão, fica só a 1ª.
func mcqDedupText(q models.Question) string {
	return q.Question
}

func mcqNormKey(text string) string {
	toks := ragTokens(text)
	sort.Strings(toks)
	return strings.Join(toks, " ")
}

func (d *mcqDedup) remember(q models.Question) {
	text := mcqDedupText(q)
	d.stems[mcqNormKey(text)] = true
	d.vecs = append(d.vecs, localEmbedding(text))
}

func (d *mcqDedup) isDuplicate(q models.Question) bool {
	text := mcqDedupText(q)
	if key := mcqNormKey(text); key != "" && d.stems[key] {
		return true
	}
	v := localEmbedding(text)
	for _, e := range d.vecs {
		if cosineDense(v, e) >= mcqDupThreshold {
			return true
		}
	}
	return false
}
