package tutor

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"estudo-app/internal/models"
)

type RAGChunk struct {
	ID              string    `json:"id"`
	Cert            string    `json:"cert"`
	Domain          string    `json:"domain"`
	Weight          int       `json:"weight"`
	Title           string    `json:"title"`
	URL             string    `json:"url"`
	Text            string    `json:"text"`
	Hydrated        bool      `json:"hydrated"`
	BuiltAt         time.Time `json:"built_at"`
	Embedding       []float64 `json:"embedding,omitempty"`
	EmbeddingModel  string    `json:"embedding_model,omitempty"`
	SourceType      string    `json:"source_type,omitempty"`
	CollectedAt     time.Time `json:"collected_at,omitempty"`
	DocumentVersion string    `json:"document_version,omitempty"`
	tokens          []string
}

type RAGHit struct {
	Chunk     RAGChunk
	Score     float64
	Relevance int
}

type ragIndex struct {
	Cert     string     `json:"cert"`
	BuiltAt  time.Time  `json:"built_at"`
	Hydrated bool       `json:"hydrated"`
	Chunks   []RAGChunk `json:"chunks"`
}

type RAGStatusInfo struct {
	Certs          int    `json:"certs"`
	Chunks         int    `json:"chunks"`
	Embeddings     int    `json:"embeddings"`
	EmbeddingModel string `json:"embedding_model,omitempty"`
	Mode           string `json:"mode"`
}

var (
	ragMu      sync.Mutex
	ragIndexes = map[string]*ragIndex{}
)

func ragDir() string {
	if dir := strings.TrimSpace(os.Getenv("RAG_DATA_DIR")); dir != "" {
		return dir
	}
	return filepath.Join("data", "rag")
}

func ragPath(cert string) string {
	return filepath.Join(ragDir(), SanitizeID(cert)+".json")
}

func RAGStatus() RAGStatusInfo {
	ragMu.Lock()
	defer ragMu.Unlock()
	chunks := 0
	embeddings := 0
	model := ""
	certs := map[string]bool{}
	loaded := map[string]bool{}
	for cert, idx := range ragIndexes {
		key := SanitizeID(cert)
		certs[key] = true
		loaded[key] = true
		chunks += len(idx.Chunks)
		for _, c := range idx.Chunks {
			if len(c.Embedding) > 0 {
				embeddings++
				if model == "" {
					model = c.EmbeddingModel
				}
			}
		}
	}
	if entries, err := os.ReadDir(ragDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			key := strings.TrimSuffix(e.Name(), ".json")
			certs[key] = true
			if loaded[key] {
				continue
			}
			if idx := loadRAGIndex(key); idx != nil {
				chunks += len(idx.Chunks)
				for _, c := range idx.Chunks {
					if len(c.Embedding) > 0 {
						embeddings++
						if model == "" {
							model = c.EmbeddingModel
						}
					}
				}
			}
		}
	}
	return RAGStatusInfo{Certs: len(certs), Chunks: chunks, Embeddings: embeddings, EmbeddingModel: model, Mode: "persistent-embeddings+chunks"}
}

func WarmRAG(cert, query string) {
	_, _ = RAGContext(cert, "", query, 3)
}

func RAGContext(cert, topic, query string, max int) (string, []models.LabChunk) {
	hits := RAGSearchFiltered(cert, topic, strings.TrimSpace(topic+" "+query), max, true)
	if len(hits) == 0 {
		return "", nil
	}
	var lines []string
	var refs []models.LabChunk
	for _, h := range hits {
		excerpt := compactText(h.Chunk.Text, 520)
		lines = append(lines, fmt.Sprintf("- chunk %s | %s | relevancia %d | fonte %s\n  %s",
			h.Chunk.ID, h.Chunk.Domain, h.Relevance, h.Chunk.URL, excerpt))
		refs = append(refs, labChunkFromHit(h))
	}
	return strings.Join(lines, "\n"), refs
}

// RAGSearchFiltered applies certification and topic metadata before ranking.
// When a topic filter is too narrow it falls back to the cert index, preserving
// useful answers while making the normal path less prone to cross-topic chunks.
func RAGSearchFiltered(cert, topic, query string, max int, hydrate bool) []RAGHit {
	hits := RAGSearch(cert, query, max*3, hydrate)
	if strings.TrimSpace(topic) == "" {
		if len(hits) > max {
			return hits[:max]
		}
		return hits
	}
	filtered := make([]RAGHit, 0, len(hits))
	needle := normalizeEvidenceText(topic)
	for _, hit := range hits {
		if strings.Contains(normalizeEvidenceText(hit.Chunk.Domain+" "+hit.Chunk.Title), needle) || domainMatchesTopic(normalizeEvidenceText(hit.Chunk.Domain), needle) {
			filtered = append(filtered, hit)
		}
	}
	if len(filtered) == 0 {
		filtered = hits
	}
	if len(filtered) > max {
		filtered = filtered[:max]
	}
	return filtered
}

func RAGSearch(cert, query string, max int, hydrate bool) []RAGHit {
	started := time.Now()
	defer func() { recordTutorLatency("rag.search", time.Since(started), 0, false) }()
	if max < 1 {
		max = 3
	}
	idx := ragIndexForCert(cert, hydrate)
	if idx == nil || len(idx.Chunks) == 0 {
		return nil
	}
	qTokens := ragTokens(query)
	if len(qTokens) == 0 {
		return nil
	}
	idf := ragIDF(idx.Chunks)
	qVec, qNorm := ragVector(qTokens, idf)
	if qNorm == 0 {
		return nil
	}
	qEmb := ragQueryEmbedding(query, idx)
	queryNorm := normalizeEvidenceText(query)
	var hits []RAGHit
	for _, c := range idx.Chunks {
		if c.SourceType == "" {
			c.SourceType = "official-curriculum"
		}
		if c.CollectedAt.IsZero() {
			c.CollectedAt = c.BuiltAt
		}
		if len(c.tokens) == 0 {
			c.tokens = ragTokens(c.Text + " " + c.Domain + " " + c.Title)
		}
		cVec, cNorm := ragVector(c.tokens, idf)
		if cNorm == 0 {
			continue
		}
		dot := 0.0
		for term, qw := range qVec {
			dot += qw * cVec[term]
		}
		lexicalScore := dot / (qNorm * cNorm)
		semanticScore := 0.0
		if len(qEmb) > 0 && len(c.Embedding) == len(qEmb) {
			semanticScore = cosineDense(qEmb, c.Embedding)
		}
		score := lexicalScore
		if semanticScore > 0 {
			score = semanticScore*0.65 + lexicalScore*0.35
		}
		if domainMatchesTopic(normalizeEvidenceText(c.Domain), queryNorm) {
			score += 0.12
		}
		if strings.Contains(queryNorm, normalizeEvidenceText(c.Domain)) {
			score += 0.08
		}
		if c.Hydrated {
			score += 0.04
		}
		if c.Weight > 0 {
			score += math.Min(float64(c.Weight), 40) / 1000
		}
		if score <= 0 {
			continue
		}
		rel := int(math.Round(score * 100))
		if rel > 100 {
			rel = 100
		}
		hits = append(hits, RAGHit{Chunk: c, Score: score, Relevance: rel})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score == hits[j].Score {
			return hits[i].Chunk.Weight > hits[j].Chunk.Weight
		}
		return hits[i].Score > hits[j].Score
	})
	if len(hits) > max {
		hits = hits[:max]
	}
	return hits
}

func ragIndexForCert(cert string, hydrate bool) *ragIndex {
	if cert == "" {
		cert = "CKA"
	}
	canonical := CanonicalCert(cert)
	ragMu.Lock()
	if idx := ragIndexes[canonical]; idx != nil && (!hydrate || idx.Hydrated) {
		ragMu.Unlock()
		return idx
	}
	ragMu.Unlock()

	idx := loadRAGIndex(canonical)
	if idx == nil {
		idx = &ragIndex{Cert: canonical, BuiltAt: time.Now(), Chunks: syntheticRAGChunks(canonical)}
	}
	if hydrate && !idx.Hydrated {
		if hydrated := buildHydratedRAGIndex(canonical); hydrated != nil && len(hydrated.Chunks) > 0 {
			idx = hydrated
			saveRAGIndex(idx)
		}
	}
	for i := range idx.Chunks {
		idx.Chunks[i].tokens = ragTokens(idx.Chunks[i].Text + " " + idx.Chunks[i].Domain + " " + idx.Chunks[i].Title)
	}
	if ensureRAGEmbeddings(idx) {
		saveRAGIndex(idx)
	}
	ragMu.Lock()
	ragIndexes[canonical] = idx
	ragMu.Unlock()
	return idx
}

const (
	localEmbeddingModel = "local-hash-v1"
	localEmbeddingDims  = 128
)

func ensureRAGEmbeddings(idx *ragIndex) bool {
	if idx == nil {
		return false
	}
	remoteModel, remoteOK := ollamaEmbeddingModel()
	changed := false
	remoteFailed := false
	for i := range idx.Chunks {
		c := &idx.Chunks[i]
		if len(c.Embedding) > 0 && strings.TrimSpace(c.EmbeddingModel) != "" {
			if !(c.EmbeddingModel == localEmbeddingModel && remoteOK && !remoteFailed) {
				continue
			}
		}
		text := c.Domain + " " + c.Title + " " + c.Text
		var emb []float64
		model := localEmbeddingModel
		if remoteOK && !remoteFailed {
			if got, err := ollamaEmbeddingForModel(text, remoteModel); err == nil && len(got) > 0 {
				emb = got
				model = remoteModel
			} else {
				remoteFailed = true
			}
		}
		if len(emb) == 0 {
			emb = localEmbedding(text)
		}
		c.Embedding = emb
		c.EmbeddingModel = model
		changed = true
	}
	return changed
}

func ragQueryEmbedding(query string, idx *ragIndex) []float64 {
	if idx == nil {
		return nil
	}
	model := ""
	for _, c := range idx.Chunks {
		if len(c.Embedding) > 0 && c.EmbeddingModel != "" {
			model = c.EmbeddingModel
			break
		}
	}
	if model == "" {
		return nil
	}
	if model == localEmbeddingModel {
		return localEmbedding(query)
	}
	if emb, err := ollamaEmbeddingForModel(query, model); err == nil && len(emb) > 0 {
		return emb
	}
	return nil
}

func localEmbedding(text string) []float64 {
	vec := make([]float64, localEmbeddingDims)
	tokens := ragTokens(text)
	if len(tokens) == 0 {
		tokens = []string{normalizeEvidenceText(text)}
	}
	for _, tok := range tokens {
		sum := sha1.Sum([]byte(tok))
		bucket := int(sum[0]) % len(vec)
		sign := 1.0
		if sum[1]&1 == 1 {
			sign = -1
		}
		weight := 1.0
		if len(tok) > 6 {
			weight = 1.25
		}
		vec[bucket] += sign * weight
	}
	norm := 0.0
	for _, v := range vec {
		norm += v * v
	}
	if norm == 0 {
		return vec
	}
	norm = math.Sqrt(norm)
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

func cosineDense(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	dot, an, bn := 0.0, 0.0, 0.0
	for i := range a {
		dot += a[i] * b[i]
		an += a[i] * a[i]
		bn += b[i] * b[i]
	}
	if an == 0 || bn == 0 {
		return 0
	}
	return dot / (math.Sqrt(an) * math.Sqrt(bn))
}

func loadRAGIndex(cert string) *ragIndex {
	b, err := os.ReadFile(ragPath(cert))
	if err != nil {
		return nil
	}
	var idx ragIndex
	if json.Unmarshal(b, &idx) != nil || len(idx.Chunks) == 0 {
		return nil
	}
	return &idx
}

func saveRAGIndex(idx *ragIndex) {
	if idx == nil {
		return
	}
	if err := os.MkdirAll(ragDir(), 0o755); err != nil {
		return
	}
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(ragPath(idx.Cert), b, 0o644)
}

func buildHydratedRAGIndex(cert string) *ragIndex {
	cur, ok := CurriculumFor(cert)
	if !ok {
		return nil
	}
	idx := &ragIndex{Cert: cert, BuiltAt: time.Now(), Hydrated: true}
	for _, d := range cur {
		for i, u := range d.URLs {
			if i > 0 {
				break
			}
			content, err := fetchURL(u)
			if err != nil || len(strings.TrimSpace(content)) < 120 {
				continue
			}
			chunks := chunkDocument(cert, d, u, content, true)
			idx.Chunks = append(idx.Chunks, chunks...)
			if len(idx.Chunks) >= 90 {
				return idx
			}
		}
	}
	if len(idx.Chunks) == 0 {
		return nil
	}
	return idx
}

func syntheticRAGChunks(cert string) []RAGChunk {
	cur, ok := CurriculumFor(cert)
	if !ok {
		return nil
	}
	var chunks []RAGChunk
	for _, d := range cur {
		text := d.Domain + ". " + strings.Join(evidenceTerms(d.Domain), " ")
		for _, u := range d.URLs {
			text += ". Fonte oficial: " + sourceTitle(u) + " " + u
		}
		u := ""
		if len(d.URLs) > 0 {
			u = d.URLs[0]
		}
		chunks = append(chunks, RAGChunk{
			ID:          ragID(cert, d.Domain, u, 0),
			Cert:        cert,
			Domain:      d.Domain,
			Weight:      d.Weight,
			Title:       d.Domain,
			URL:         u,
			Text:        text,
			BuiltAt:     time.Now(),
			CollectedAt: time.Now(),
			SourceType:  "official-curriculum",
		})
	}
	return chunks
}

func chunkDocument(cert string, d CurriculumDomain, u, content string, hydrated bool) []RAGChunk {
	content = stripSourceMarkers(content)
	paras := regexp.MustCompile(`\n\s*\n+`).Split(content, -1)
	var chunks []RAGChunk
	var buf strings.Builder
	flush := func() {
		txt := strings.TrimSpace(buf.String())
		buf.Reset()
		if len(txt) < 120 {
			return
		}
		if len(txt) > 1300 {
			txt = txt[:1300]
		}
		n := len(chunks)
		chunks = append(chunks, RAGChunk{
			ID:          ragID(cert, d.Domain, u, n),
			Cert:        cert,
			Domain:      d.Domain,
			Weight:      d.Weight,
			Title:       sourceTitle(u),
			URL:         u,
			Text:        txt,
			CollectedAt: time.Now(),
			SourceType:  "official-document",
			Hydrated:    hydrated,
			BuiltAt:     time.Now(),
		})
	}
	for _, p := range paras {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if buf.Len()+len(p) > 950 {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(p)
		if len(chunks) >= 16 {
			break
		}
	}
	flush()
	return chunks
}

func labChunkFromHit(h RAGHit) models.LabChunk {
	return models.LabChunk{
		ID:        h.Chunk.ID,
		Domain:    h.Chunk.Domain,
		Title:     h.Chunk.Title,
		URL:       h.Chunk.URL,
		Excerpt:   compactText(h.Chunk.Text, 180),
		Relevance: h.Relevance,
	}
}

func ragID(parts ...any) string {
	h := sha1.New()
	for _, p := range parts {
		fmt.Fprint(h, p, "|")
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func ragIDF(chunks []RAGChunk) map[string]float64 {
	df := map[string]int{}
	for _, c := range chunks {
		seen := map[string]bool{}
		toks := c.tokens
		if len(toks) == 0 {
			toks = ragTokens(c.Text + " " + c.Domain + " " + c.Title)
		}
		for _, t := range toks {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	n := float64(len(chunks))
	idf := map[string]float64{}
	for t, d := range df {
		idf[t] = math.Log(1 + (n+1)/float64(d+1))
	}
	return idf
}

func ragVector(tokens []string, idf map[string]float64) (map[string]float64, float64) {
	counts := map[string]float64{}
	for _, t := range tokens {
		counts[t]++
	}
	norm := 0.0
	for t, c := range counts {
		termIDF := idf[t]
		if termIDF == 0 {
			termIDF = 0.1
		}
		w := (1 + math.Log(c)) * termIDF
		counts[t] = w
		norm += w * w
	}
	return counts, math.Sqrt(norm)
}

var ragStop = map[string]bool{
	"com": true, "para": true, "uma": true, "que": true, "por": true, "dos": true, "das": true,
	"the": true, "and": true, "for": true, "with": true, "from": true, "this": true, "that": true,
	"kubernetes": true, "docs": true, "documentation": true, "official": true,
}

func ragTokens(s string) []string {
	s = normalizeEvidenceText(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	var out []string
	for _, f := range fields {
		if len(f) < 2 || ragStop[f] {
			continue
		}
		out = append(out, f)
	}
	return out
}

func stripSourceMarkers(text string) string {
	for {
		i := strings.Index(text, srcMarker)
		if i < 0 {
			break
		}
		j := strings.Index(text[i:], "@@\n")
		if j < 0 {
			break
		}
		text = text[:i] + text[i+j+3:]
	}
	return text
}
