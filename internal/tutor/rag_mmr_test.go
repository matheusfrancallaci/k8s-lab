package tutor

import "testing"

func TestMMRRerankDropsRedundant(t *testing.T) {
	// A e B são quase idênticos (mesmo embedding); C é diverso. Com max=2, o MMR
	// deve preferir A + C (cobertura), não A + B (redundância).
	hits := []RAGHit{
		{Chunk: RAGChunk{ID: "A", Embedding: []float64{1, 0, 0}}, Score: 0.90},
		{Chunk: RAGChunk{ID: "B", Embedding: []float64{1, 0, 0}}, Score: 0.85},
		{Chunk: RAGChunk{ID: "C", Embedding: []float64{0, 1, 0}}, Score: 0.80},
	}
	got := mmrRerank(hits, 2)
	if len(got) != 2 {
		t.Fatalf("esperava 2 hits, veio %d", len(got))
	}
	if got[0].Chunk.ID != "A" {
		t.Errorf("primeiro deveria ser o mais relevante (A), veio %s", got[0].Chunk.ID)
	}
	if got[1].Chunk.ID != "C" {
		t.Errorf("segundo deveria ser o diverso (C), veio %s — MMR não descartou a redundância", got[1].Chunk.ID)
	}
}

func TestMMRRerankPassthrough(t *testing.T) {
	hits := []RAGHit{
		{Chunk: RAGChunk{ID: "A", Embedding: []float64{1, 0}}, Score: 0.9},
		{Chunk: RAGChunk{ID: "B", Embedding: []float64{0, 1}}, Score: 0.8},
	}
	if got := mmrRerank(hits, 3); len(got) != 2 {
		t.Errorf("max >= len deveria devolver tudo, veio %d", len(got))
	}
	if got := mmrRerank(hits, 0); got != nil {
		t.Errorf("max 0 deveria devolver nil, veio %v", got)
	}
}

func TestChunkSimilarityFallbackTokens(t *testing.T) {
	// Sem embeddings, cai no Jaccard de tokens.
	a := RAGChunk{tokens: []string{"pod", "scheduler", "node"}}
	b := RAGChunk{tokens: []string{"pod", "scheduler", "node"}}
	c := RAGChunk{tokens: []string{"service", "ingress", "dns"}}
	if s := chunkSimilarity(a, b); s < 0.99 {
		t.Errorf("chunks idênticos deveriam ter similaridade ~1, veio %.3f", s)
	}
	if s := chunkSimilarity(a, c); s > 0.01 {
		t.Errorf("chunks disjuntos deveriam ter similaridade ~0, veio %.3f", s)
	}
}

func TestTokenJaccard(t *testing.T) {
	if s := tokenJaccard([]string{"a", "b"}, []string{"a", "b"}); s != 1 {
		t.Errorf("conjuntos iguais: quer 1, veio %.3f", s)
	}
	if s := tokenJaccard([]string{"a", "b"}, []string{"b", "c"}); s < 0.33 || s > 0.34 {
		t.Errorf("interseção 1 de união 3: quer ~0.333, veio %.3f", s)
	}
	if s := tokenJaccard(nil, []string{"a"}); s != 0 {
		t.Errorf("vazio: quer 0, veio %.3f", s)
	}
}
