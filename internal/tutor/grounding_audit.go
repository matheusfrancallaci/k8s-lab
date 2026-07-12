package tutor

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

type groundingStreamGuard struct {
	report    AnswerabilityReport
	emit      func(string)
	buffer    strings.Builder
	published strings.Builder
}

func newGroundingStreamGuard(report AnswerabilityReport, emit func(string)) *groundingStreamGuard {
	if emit == nil {
		emit = func(string) {}
	}
	return &groundingStreamGuard{report: report, emit: emit}
}

func (g *groundingStreamGuard) Write(chunk string) {
	g.buffer.WriteString(chunk)
	text := g.buffer.String()
	start := 0
	for i, r := range text {
		if r != '.' && r != '!' && r != '?' && r != '\n' {
			continue
		}
		end := i + len(string(r))
		g.publish(text[start:end])
		start = end
	}
	if start > 0 {
		g.buffer.Reset()
		g.buffer.WriteString(text[start:])
	}
}

func (g *groundingStreamGuard) publish(sentence string) {
	trimmed := strings.TrimSpace(sentence)
	if trimmed == "" {
		g.published.WriteString(sentence)
		g.emit(sentence)
		return
	}
	audit := AuditGroundedReply(trimmed, g.report)
	if technicalQuestion(trimmed) && !audit.Passed {
		replacement := " [afirmacao tecnica omitida: fonte nao comprovada] "
		g.published.WriteString(replacement)
		g.emit(replacement)
		return
	}
	g.published.WriteString(sentence)
	g.emit(sentence)
}

type groundedCacheEntry struct {
	Reply   string
	Expires time.Time
}

var groundedReplyCache = struct {
	sync.Mutex
	Entries map[string]groundedCacheEntry
}{Entries: map[string]groundedCacheEntry{}}

func groundedReplyKey(msg, model string, report AnswerabilityReport) string {
	// SEM CheckedAt na chave: é um timestamp por request, então o cache nunca
	// acertava (medido em produção: 12s na repetição da MESMA pergunta). A
	// identidade da resposta é pergunta+modelo+estado do RAG+fontes.
	return ragID(strings.ToLower(strings.TrimSpace(msg)), model, report.RAG, strings.Join(report.VerifiedSources(), "|"))
}

func cachedGroundedReply(key string) (string, bool) {
	groundedReplyCache.Lock()
	defer groundedReplyCache.Unlock()
	e, ok := groundedReplyCache.Entries[key]
	if !ok || time.Now().After(e.Expires) {
		delete(groundedReplyCache.Entries, key)
		return "", false
	}
	return e.Reply, true
}

func storeGroundedReply(key, reply string) {
	groundedReplyCache.Lock()
	defer groundedReplyCache.Unlock()
	if len(groundedReplyCache.Entries) >= 256 {
		for k := range groundedReplyCache.Entries {
			delete(groundedReplyCache.Entries, k)
			break
		}
	}
	groundedReplyCache.Entries[key] = groundedCacheEntry{Reply: reply, Expires: time.Now().Add(30 * time.Minute)}
}

func (g *groundingStreamGuard) Close() {
	if g.buffer.Len() > 0 {
		g.publish(g.buffer.String())
	}
}

var (
	citationRe        = regexp.MustCompile(`\[S([1-9][0-9]*)\]`)
	urlInReplyRe      = regexp.MustCompile(`https?://[^\s)\]>]+`)
	promptInjectionRe = regexp.MustCompile(`(?i)(ignore|desconsidere|forget).{0,30}(instru|prompt|regra|system)|system\s*prompt|developer\s*message|execute.{0,20}(comando|command)|revele.{0,20}prompt`)
)

type GroundingAudit struct {
	Claims       int      `json:"claims"`
	CitedClaims  int      `json:"cited_claims"`
	Coverage     int      `json:"coverage"`
	InvalidRefs  []string `json:"invalid_refs,omitempty"`
	InventedURLs []string `json:"invented_urls,omitempty"`
	Passed       bool     `json:"passed"`
}

func sanitizeRetrievedText(text string) string {
	lines := strings.Split(text, "\n")
	out := lines[:0]
	for _, line := range lines {
		if promptInjectionRe.MatchString(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func AuditGroundedReply(reply string, report AnswerabilityReport) GroundingAudit {
	audit := GroundingAudit{}
	allowed := map[string]bool{}
	for _, source := range report.VerifiedSources() {
		allowed[source] = true
	}
	for _, raw := range urlInReplyRe.FindAllString(reply, -1) {
		u := strings.TrimRight(raw, ".,;:")
		if !allowed[u] {
			audit.InventedURLs = append(audit.InventedURLs, u)
		}
	}
	maxSource := len(report.VerifiedSources())
	// Corpus de evidência para ancoragem: o modelo local responde em PT sobre
	// fontes em EN — identificadores técnicos (replicaset, kubelet, pod)
	// sobrevivem à tradução. Exigir [Sn] em CADA frase mutilava respostas
	// corretas com fontes verificadas anexas (falso positivo pego em validação
	// ao vivo 2026-07-12): citação inline OU ancoragem no corpus contam.
	corpus := strings.ToLower(report.Evidence + " " + report.Context)
	for _, sentence := range regexp.MustCompile(`[.!?\n]+`).Split(reply, -1) {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" || !technicalQuestion(sentence) {
			continue
		}
		audit.Claims++
		refs := citationRe.FindAllStringSubmatch(sentence, -1)
		valid := false
		for _, ref := range refs {
			var n int
			_, _ = fmt.Sscanf(ref[1], "%d", &n)
			if n >= 1 && n <= maxSource {
				valid = true
			} else {
				audit.InvalidRefs = append(audit.InvalidRefs, ref[0])
			}
		}
		if !valid && corpus != "" {
			for _, tok := range contentTokens(sentence) {
				if strings.Contains(corpus, tok) {
					valid = true
					break
				}
			}
		}
		if valid {
			audit.CitedClaims++
		}
	}
	if audit.Claims == 0 {
		audit.Coverage = 100
	} else {
		audit.Coverage = audit.CitedClaims * 100 / audit.Claims
	}
	audit.Passed = audit.Coverage >= 80 && len(audit.InvalidRefs) == 0 && len(audit.InventedURLs) == 0
	return audit
}

func FinalizeGroundedReply(reply string, report AnswerabilityReport) string {
	audit := AuditGroundedReply(reply, report)
	recordTutorLatency("grounding.claim_audit", 0, 0, !audit.Passed)
	if !audit.Passed && technicalQuestion(reply) {
		return report.AppendVerifiedSources("Nao publiquei a resposta gerada porque as afirmacoes tecnicas nao atingiram o contrato de citacao por fonte. Posso responder novamente quando houver evidencia suficiente.")
	}
	return report.AppendVerifiedSources(reply)
}
