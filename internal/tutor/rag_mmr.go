package tutor

import (
	"math"
	"os"
	"strconv"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Fatia D — qualidade de recuperação. O gerador (labs, quiz, grounding) é tão
// bom quanto a evidência que o RAG entrega. Dois problemas atacados aqui:
//
//  1. REDUNDÂNCIA: o top-K costuma vir dominado por chunks quase idênticos da
//     mesma seção — o modelo vê a mesma frase 4x e perde cobertura. MMR
//     (Maximal Marginal Relevance) reordena o top-K penalizando quem repete o
//     que já foi selecionado, maximizando cobertura sem perder relevância.
//  2. COBERTURA: a hidratação lia só a 1ª URL de cada domínio. Ler algumas mais
//     (limitado) amplia o material oficial disponível.
//
// Ambos têm kill-switch por env para poder desligar em produção sem redeploy.
// ─────────────────────────────────────────────────────────────────────────────

func ragMMREnabled() bool {
	v := strings.TrimSpace(os.Getenv("RAG_MMR"))
	return v == "" || v == "1" || strings.EqualFold(v, "true")
}

// ragMaxURLsPerDomain limita quantas URLs oficiais hidratar por domínio.
func ragMaxURLsPerDomain() int {
	if v := strings.TrimSpace(os.Getenv("RAG_MAX_URLS_PER_DOMAIN")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 5 {
			return n
		}
	}
	return 2
}

const mmrLambda = 0.7 // 0.7*relevância − 0.3*redundância

// mmrRerank seleciona max hits de `hits` (já ordenados por score desc) maximizando
// relevância e diversidade. Determinístico. Se há hits <= max, só trunca.
func mmrRerank(hits []RAGHit, max int) []RAGHit {
	if max <= 0 {
		return nil
	}
	if len(hits) <= max {
		return hits
	}
	selected := make([]RAGHit, 0, max)
	remaining := append([]RAGHit(nil), hits...)
	for len(selected) < max && len(remaining) > 0 {
		bestIdx, bestScore := 0, math.Inf(-1)
		for i, h := range remaining {
			penalty := 0.0
			for _, s := range selected {
				if sim := chunkSimilarity(h.Chunk, s.Chunk); sim > penalty {
					penalty = sim
				}
			}
			score := mmrLambda*h.Score - (1-mmrLambda)*penalty
			if score > bestScore {
				bestScore, bestIdx = score, i
			}
		}
		selected = append(selected, remaining[bestIdx])
		remaining = append(remaining[:bestIdx], remaining[bestIdx+1:]...)
	}
	return selected
}

// chunkSimilarity mede quão parecidos dois chunks são: cosseno dos embeddings
// densos quando disponíveis (mesmo sinal do RAG), senão Jaccard de tokens.
func chunkSimilarity(a, b RAGChunk) float64 {
	if len(a.Embedding) > 0 && len(a.Embedding) == len(b.Embedding) {
		if s := cosineDense(a.Embedding, b.Embedding); s > 0 {
			return s
		}
	}
	return tokenJaccard(chunkTokens(a), chunkTokens(b))
}

func chunkTokens(c RAGChunk) []string {
	if len(c.tokens) > 0 {
		return c.tokens
	}
	return ragTokens(c.Text + " " + c.Domain + " " + c.Title)
}

func tokenJaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	sa := make(map[string]bool, len(a))
	for _, t := range a {
		sa[t] = true
	}
	inter := 0
	sb := make(map[string]bool, len(b))
	for _, t := range b {
		if !sb[t] {
			sb[t] = true
			if sa[t] {
				inter++
			}
		}
	}
	union := len(sa) + len(sb) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
