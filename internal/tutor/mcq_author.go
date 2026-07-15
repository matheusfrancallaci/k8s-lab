package tutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Autoria SOB DEMANDA de questões de múltipla escolha — o análogo de
// AuthorExamBatch (labs) para MC. Diferente do caminho de ingestão (que exige
// doc colado), aqui o insumo é o CURRÍCULO + RAG da certificação. Separa
// AUTORIA de SERVING: engorda o banco em batch, exige grounding, injeta
// distratores dos confusionSets (o erro que o aluno de fato confunde), remove
// quase-duplicatas por embeddings e registra proveniência/prontidão antes de
// publicar. Nenhuma questão "de fato" chega ao aluno sem passar por esses gates.
// ─────────────────────────────────────────────────────────────────────────────

const mcqContractVersion = "1"

// MCQReport descreve o resultado de uma rodada de autoria de MC.
type MCQReport struct {
	Requested  int      `json:"requested"`
	Ready      int      `json:"ready"`
	Rejected   int      `json:"rejected"`
	Duplicates int      `json:"duplicates"`
	Cert       string   `json:"cert"`
	Topic      string   `json:"topic"`
	Grounded   bool     `json:"grounded"`
	Judged     int      `json:"judged"`   // confirmadas por juiz de correção / self-consistency
	Verified   bool     `json:"verified"` // distratores provados por execução (modo comando)
	UsedModel  string   `json:"used_model,omitempty"`
	JudgeModel string   `json:"judge_model,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
	Failures   []string `json:"failures,omitempty"`
}

// AuthorMCQBatch gera até count questões de múltipla escolha nível prova para
// cert/topic, ancoradas no currículo/RAG. `existing` é o banco atual (curado +
// gerado) usado para dedup semântico. Retorna só o que passou em todos os gates.
func AuthorMCQBatch(cert, topic string, count, level int, existing []models.Question) ([]models.Question, MCQReport, error) {
	cert = CanonicalCert(strings.TrimSpace(cert))
	if cert == "" {
		cert = "CKA"
	}
	if name, _ := RegisterCert(cert); name != "" {
		cert = name
	}
	if count < 1 {
		count = 5
	}
	if count > 10 {
		count = 10
	}
	if level < 1 || level > 3 {
		level = 2
	}
	topic = resolveMCQTopic(cert, topic)
	report := MCQReport{Requested: count, Cert: cert, Topic: topic}

	if ok, _ := LLMStatus(); !ok {
		return nil, report, fmt.Errorf("IA local (Ollama) indisponível: a autoria de questões precisa do modelo de geração")
	}
	// A: autora-se com o modelo MAIS FORTE disponível (custo amortizado); o
	// serving continua local. B: o juiz de correção usa modelo forte/independente.
	report.UsedModel = authoringModel()
	if mcqJudgeEnabled() {
		report.JudgeModel = authoringJudgeModel()
	}

	// Grounding: sem evidência oficial não geramos questão — o maior risco do
	// produto é servir conteúdo técnico inventado como fato.
	WarmRAG(cert, topic)
	ground := mcqGroundContext(cert, topic)
	if strings.TrimSpace(ground.text) == "" {
		return nil, report, fmt.Errorf("sem evidência oficial suficiente para %s / %q — cole a URL da doc oficial ou escolha um tópico do currículo", cert, topic)
	}
	report.Grounded = true

	hints := confusionHintsForContext(ground.text, topic)
	if len(hints) > 0 {
		report.Reasons = append(report.Reasons, "distratores ancorados nos pares de confusão reais: "+strings.Join(hints, ", "))
	}

	raw, err := llmMCQFromEvidence(cert, topic, ground.text, hints, count)
	if err != nil {
		return nil, report, fmt.Errorf("geração falhou: %w", err)
	}

	// dedup: embeddings do banco existente (mesma cert, MC) + do que já foi
	// aceito nesta rodada.
	dedup := newMCQDedup(existing, cert)

	var out []models.Question
	for _, cand := range raw {
		if len(out) >= count {
			break
		}
		q, reason := finalizeMCQCandidate(cand, cert, topic, level, ground)
		if reason != "" {
			report.Rejected++
			report.Failures = append(report.Failures, reason)
			quizRejected.Add(1)
			continue
		}
		// B: juiz de correção — o grounding só prova que a resposta APARECE na
		// fonte; aqui provamos que ela É a resposta (ou rejeitamos a incoerência).
		if pass, judged, jreason := verifyMCQCorrectness(q, ground.text); !pass {
			report.Rejected++
			report.Failures = append(report.Failures, jreason)
			quizRejected.Add(1)
			continue
		} else if judged {
			markMCQJudged(&q)
			report.Judged++
		}
		if dedup.isDuplicate(q) {
			report.Duplicates++
			continue
		}
		dedup.remember(q)
		quizAccepted.Add(1)
		out = append(out, q)
	}

	if len(out) == 0 {
		return nil, report, fmt.Errorf("nenhuma questão passou nos gates (grounding/dedup): %d reprovada(s), %d duplicada(s)", report.Rejected, report.Duplicates)
	}
	if err := persist(out); err != nil {
		return nil, report, err
	}
	_ = RecordMCQCatalog(out)
	report.Ready = len(out)
	return out, report, nil
}

// resolveMCQTopic mapeia o pedido para um tópico concreto do currículo/gerador.
func resolveMCQTopic(cert, topic string) string {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return ""
	}
	if t := curriculumTopicInMessage(cert, topic); t != "" {
		return t
	}
	if t := exactTopicForRequest(cert, topic); t != "" {
		return t
	}
	if t := detectTopic(topic); t != "" {
		return t
	}
	return topic
}

// mcqGround agrega o material confiável usado como fonte da questão.
type mcqGround struct {
	text      string
	sourceURL string
}

func mcqGroundContext(cert, topic string) mcqGround {
	var b strings.Builder
	sourceURL := ""
	if ev := EvidenceContext(cert, topic, topic, 4); ev != "" {
		b.WriteString(ev)
		b.WriteString("\n")
	}
	if rag, chunks := RAGContext(cert, topic, topic, 5); rag != "" {
		b.WriteString(rag)
		b.WriteString("\n")
		for _, c := range chunks {
			if c.URL != "" && sourceURL == "" {
				sourceURL = c.URL
			}
		}
	}
	if sourceURL == "" {
		if cur, ok := CurriculumFor(cert); ok {
			needle := strings.ToLower(topic)
			for _, d := range cur {
				if len(d.URLs) == 0 {
					continue
				}
				if topic == "" || strings.Contains(strings.ToLower(d.Domain), needle) || domainMatchesTopic(strings.ToLower(d.Domain), needle) {
					sourceURL = d.URLs[0]
					break
				}
			}
		}
	}
	return mcqGround{text: sanitizeRetrievedText(b.String()), sourceURL: sourceURL}
}

// confusionHintsForContext coleta, dos confusionSets, os termos cujos pares
// aparecem no material — são os distratores que o aluno de fato confunde na
// prova (não flags aleatórias). Slice 2 do plano.
func confusionHintsForContext(context, topic string) []string {
	hay := strings.ToLower(context + " " + topic)
	seen := map[string]bool{}
	var out []string
	for _, set := range confusionSets {
		matched := false
		for _, term := range set {
			if strings.Contains(hay, strings.ToLower(term)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for _, term := range set {
			if !seen[strings.ToLower(term)] {
				seen[strings.ToLower(term)] = true
				out = append(out, term)
			}
		}
	}
	sort.Strings(out)
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

func llmMCQFromEvidence(cert, topic, evidence string, hints []string, n int) ([]mcqCandidate, error) {
	if len(evidence) > 6000 {
		evidence = evidence[:6000]
	}
	topicLine := "os domínios da certificação"
	if topic != "" {
		topicLine = "o tópico: " + topic
	}
	hintLine := ""
	if len(hints) > 0 {
		hintLine = fmt.Sprintf("\n- Prefira distratores PLAUSÍVEIS destes conceitos que o aluno costuma confundir: %s. Nunca use letras soltas nem números como alternativa.", strings.Join(hints, ", "))
	}
	prompt := fmt.Sprintf(`Você é um elaborador de questões para a certificação %s.
A partir EXCLUSIVAMENTE das evidências oficiais abaixo, crie %d questões de múltipla escolha nível prova em português do Brasil sobre %s.

Regras:
- Cada questão testa compreensão real (cenário/pegadinha), não decoreba literal.
- Os %d enunciados devem ser DISTINTOS entre si (conceitos diferentes do tópico); nunca repita a mesma pergunta com respostas diferentes.
- NÃO copie o exemplo abaixo: ele é só de formato.
- "options": exatamente 4 strings completas e plausíveis. A correta deve estar sustentada pelas evidências.%s
- "answer": índice numérico (0-3) da alternativa correta.
- "explanation": por que a correta está certa e por que a mais tentadora está errada (2-3 frases). A explicação NÃO pode contradizer a alternativa marcada como correta.

Exemplo do formato EXATO:
{"questions":[{"question":"Qual componente decide em qual nó um pod novo roda?","options":["kube-scheduler","kubelet","kube-proxy","etcd"],"answer":0,"explanation":"O kube-scheduler faz o bind do pod ao nó; o kubelet apenas executa o que já foi agendado."}]}

Responda SOMENTE JSON válido nesse formato.

EVIDÊNCIAS OFICIAIS:
%s`, cert, n, topicLine, n, hintLine, evidence)

	raw, err := llmGenerateContract(prompt, "quiz", 120*time.Second, tokensGen, authoringModel())
	if err != nil {
		log.Printf("[tutor/mcq] geração falhou: %v", err)
		return nil, err
	}
	return parseMCQContract(raw)
}

// mcqCandidate é uma questão crua do modelo, antes dos gates.
type mcqCandidate struct {
	Question    string
	Options     []string
	Answer      int
	Explanation string
}

func parseMCQContract(raw string) ([]mcqCandidate, error) {
	var parsed struct {
		Questions []struct {
			Question    string      `json:"question"`
			Options     []any       `json:"options"`
			Answer      json.Number `json:"answer"`
			Explanation string      `json:"explanation"`
		} `json:"questions"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("JSON inválido do modelo: %w", err)
	}
	var out []mcqCandidate
	for _, q := range parsed.Questions {
		ans, aerr := q.Answer.Int64()
		if aerr != nil {
			continue
		}
		opts := make([]string, 0, len(q.Options))
		for _, o := range q.Options {
			if s, ok := o.(string); ok && len(strings.TrimSpace(s)) > 1 {
				opts = append(opts, strings.TrimSpace(s))
			}
		}
		out = append(out, mcqCandidate{
			Question: strings.TrimSpace(q.Question), Options: opts,
			Answer: int(ans), Explanation: strings.TrimSpace(q.Explanation),
		})
	}
	return out, nil
}

// finalizeMCQCandidate aplica os gates determinísticos e monta a Question final.
// Devolve motivo != "" quando reprovada.
func finalizeMCQCandidate(c mcqCandidate, cert, topic string, level int, ground mcqGround) (models.Question, string) {
	if len(c.Options) != 4 {
		return models.Question{}, fmt.Sprintf("questão descartada: %d alternativas (esperado 4)", len(c.Options))
	}
	if c.Answer < 0 || c.Answer >= 4 {
		return models.Question{}, "questão descartada: índice de resposta fora do intervalo"
	}
	if len([]rune(c.Question)) < 15 {
		return models.Question{}, "questão descartada: enunciado curto demais"
	}
	if dupOptions(c.Options) {
		return models.Question{}, "questão descartada: alternativas repetidas"
	}
	correct := c.Options[c.Answer]
	// Anti-alucinação: a resposta correta precisa estar ancorada nas evidências.
	if !groundedInSource(c.Question, correct, ground.text) {
		return models.Question{}, fmt.Sprintf("questão descartada por grounding (resposta não ancorada): %.60q", c.Question)
	}
	// Anti-viés de posição: o modelo tende a pôr a correta primeiro.
	opts, answer := shuffleOptions(c.Options, c.Answer)
	q := models.Question{
		ID:          newID(),
		Cert:        models.Cert(cert),
		Topic:       mcqTopicLabel(topic),
		Difficulty:  diffFor(level),
		Type:        models.MultipleChoice,
		Source:      models.SourceGenerated,
		Question:    c.Question,
		Options:     opts,
		Answer:      answer,
		Explanation: c.Explanation + "\n\n(questão gerada pela IA local, ancorada na documentação oficial)",
		DocURL:      ground.sourceURL,
	}
	q.Readiness = groundedMCQReadiness(q, ground.sourceURL)
	return q, ""
}

func mcqTopicLabel(topic string) string {
	if strings.TrimSpace(topic) == "" {
		return "Estilo Prova"
	}
	return topic
}

func dupOptions(opts []string) bool {
	seen := map[string]bool{}
	for _, o := range opts {
		k := strings.ToLower(strings.TrimSpace(o))
		if seen[k] {
			return true
		}
		seen[k] = true
	}
	return false
}

// ─── Prontidão e catálogo (Fatia 4) ──────────────────────────────────────────

func mcqContentDigest(q models.Question) string {
	payload := strings.Join(append([]string{
		q.ID, string(q.Cert), q.Topic, q.Question, fmt.Sprint(q.Answer), q.Explanation,
	}, q.Options...), "\x00")
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func groundedMCQReadiness(q models.Question, sourceURL string) *models.QuestionReadiness {
	return &models.QuestionReadiness{
		State:         "grounded",
		Version:       mcqContractVersion,
		ContentDigest: mcqContentDigest(q),
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
		Grounded:      true,
		SourceURL:     sourceURL,
		Warnings:      []string{"distratores ainda não provados errados por execução"},
	}
}

// markMCQVerified promove uma questão de comando para "verified" quando os
// distratores foram executados e nenhum satisfez o validador do efeito.
func markMCQVerified(q *models.Question, err error) {
	if q == nil {
		return
	}
	if q.Readiness == nil {
		q.Readiness = &models.QuestionReadiness{Version: mcqContractVersion, ContentDigest: mcqContentDigest(*q)}
	}
	r := q.Readiness
	r.Executable = true
	r.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		r.State = "rejected"
		r.Failure = err.Error()
		r.Warnings = []string{"verificação executável reprovou a questão"}
		return
	}
	r.State = "verified"
	r.VerifiedAt = r.CheckedAt
	r.Failure = ""
	r.Warnings = nil
}

type QuestionCatalogEntry struct {
	ID        string                   `json:"id"`
	Cert      string                   `json:"cert"`
	Topic     string                   `json:"topic"`
	UpdatedAt string                   `json:"updated_at"`
	Readiness models.QuestionReadiness `json:"readiness"`
}

func mcqCatalogPath() string { return filepath.Join("data", "quiz", "catalog.json") }

// RecordMCQCatalog persiste a prontidão das questões geradas — separa "existe"
// de "foi provada", espelhando o catálogo de labs.
func RecordMCQCatalog(qs []models.Question) error {
	labCatalogMu.Lock()
	defer labCatalogMu.Unlock()
	entries := map[string]QuestionCatalogEntry{}
	if b, err := os.ReadFile(mcqCatalogPath()); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	for _, q := range qs {
		if q.Type != models.MultipleChoice || q.Readiness == nil {
			continue
		}
		entries[q.ID] = QuestionCatalogEntry{
			ID: q.ID, Cert: string(q.Cert), Topic: q.Topic,
			UpdatedAt: time.Now().UTC().Format(time.RFC3339), Readiness: *q.Readiness,
		}
	}
	if err := os.MkdirAll(filepath.Dir(mcqCatalogPath()), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(mcqCatalogPath(), b, 0o644)
}

// MCQCatalog devolve as entradas de prontidão das questões, mais recentes antes.
func MCQCatalog() []QuestionCatalogEntry {
	labCatalogMu.Lock()
	defer labCatalogMu.Unlock()
	entries := map[string]QuestionCatalogEntry{}
	b, err := os.ReadFile(mcqCatalogPath())
	if err != nil || json.Unmarshal(b, &entries) != nil {
		return nil
	}
	out := make([]QuestionCatalogEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}
