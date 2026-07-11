package tutor

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"estudo-app/internal/models"
)

type AnswerabilityReport struct {
	Answerable      bool
	Confidence      int
	Reason          string
	Sources         []string
	Blocked         []string
	Evidence        string
	RAG             string
	Context         string
	Cert            string
	Topic           string
	CheckedAt       string
	OfficialSource  bool
	EvidenceScore   int
	RAGScore        int
	TopicRecognized bool
}

func CheckAnswerability(msg, activeCert string) AnswerabilityReport {
	started := time.Now()
	defer func() { recordTutorLatency("grounding.answerability", time.Since(started), 0, false) }()
	msg = strings.TrimSpace(msg)
	cert := inferCertFromMessage(msg, activeCert)
	if cert == "" {
		cert = "CKA"
	}
	topic := exactTopicForRequest(cert, msg)
	if topic == "" {
		topic = detectTopic(msg)
	}

	enriched, sources, blocked := EnrichSource(msg)
	evidence := EvidenceContext(cert, topic, msg+" "+enriched, 4)
	rag, _ := RAGContext(cert, topic, msg+" "+enriched, 4)

	conf := 0
	var reasons []string
	if len(sources) > 0 {
		conf += 45
		reasons = append(reasons, "fonte oficial recuperada")
	}
	if evidence != "" {
		conf += 25
		reasons = append(reasons, "evidencia curricular encontrada")
	}
	if rag != "" {
		conf += 20
		reasons = append(reasons, "chunks RAG recuperados")
	}
	if topic != "" {
		conf += 20
		reasons = append(reasons, "topico exato reconhecido")
	}
	if _, ok := CurriculumFor(cert); ok {
		conf += 10
	}
	if conf > 100 {
		conf = 100
	}

	// Technical answers fail closed: a recognized topic alone is never enough.
	// Volatile requests (versions, releases and current defaults) require a live
	// trusted source in addition to internal curriculum/RAG evidence.
	volatile := regexp.MustCompile(`(?i)\b(vers[aã]o|release|atual|latest|hoje|202[0-9]|default)\b`).MatchString(msg)
	answerable := conf >= 45 && (len(sources) > 0 || (evidence != "" && rag != ""))
	if identifiers := unsupportedProductIdentifiers(msg, enriched); len(identifiers) > 0 {
		answerable = false
		reasons = append(reasons, "identificador sem evidencia oficial: "+strings.Join(identifiers, ", "))
	}
	if volatile && len(sources) == 0 {
		answerable = false
		reasons = append(reasons, "pergunta volatil sem fonte oficial atual")
	}
	if !technicalQuestion(msg) {
		answerable = true
	}
	reason := strings.Join(reasons, "; ")
	if reason == "" {
		reason = "sem fonte, evidencia ou template confiavel para esse pedido"
	}
	return AnswerabilityReport{
		Answerable:      answerable,
		Confidence:      conf,
		Reason:          reason,
		Sources:         sources,
		Blocked:         blocked,
		Evidence:        evidence,
		RAG:             rag,
		Context:         enriched,
		Cert:            cert,
		Topic:           topic,
		CheckedAt:       time.Now().UTC().Format("2006-01-02"),
		OfficialSource:  len(sources) > 0,
		EvidenceScore:   evidenceConfidence(cert, topic, msg+" "+enriched),
		RAGScore:        bestRAGRelevance(cert, topic, msg+" "+enriched),
		TopicRecognized: topic != "",
	}
}

// Product-like identifiers are a common hallucination trap: generic words such
// as "secret" can retrieve Kubernetes documentation even when the actual
// product named by the student does not exist in any recovered source.
func unsupportedProductIdentifiers(msg, evidence string) []string {
	re := regexp.MustCompile(`\b[A-Z][A-Z0-9]*-[0-9][A-Z0-9-]*\b`)
	known := map[string]bool{"CKA": true, "CKAD": true, "CKS": true, "KCNA": true, "AZ-104": true, "AZ-500": true}
	evidence = strings.ToUpper(evidence)
	seen := map[string]bool{}
	var out []string
	for _, id := range re.FindAllString(strings.ToUpper(msg), -1) {
		// EnrichSource preserves the original question at the beginning of the
		// context, so one occurrence is merely the user's claim. A second
		// occurrence is required before recovered evidence can support it.
		if known[id] || seen[id] || strings.Count(evidence, id) > 1 {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func evidenceConfidence(cert, topic, text string) int {
	best := 0
	for _, evidence := range evidenceForText(cert, topic, text, "", 4) {
		if evidence.Confidence > best {
			best = evidence.Confidence
		}
	}
	return best
}

func bestRAGRelevance(cert, topic, text string) int {
	best := 0
	for _, hit := range RAGSearch(cert, topic+" "+text, 3, false) {
		if hit.Relevance > best {
			best = hit.Relevance
		}
	}
	return best
}

func (r AnswerabilityReport) VerifiedSources() []string {
	seen := map[string]bool{}
	var out []string
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u != "" && isTrustedURL(u) && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	for _, u := range r.Sources {
		add(u)
	}
	for _, hit := range RAGSearch(r.Cert, r.Topic+" "+r.RAG, 3, false) {
		add(hit.Chunk.URL)
	}
	return out
}

func (r AnswerabilityReport) AppendVerifiedSources(reply string) string {
	sources := r.VerifiedSources()
	if len(sources) == 0 {
		return strings.TrimSpace(reply) + "\n\nFontes verificadas: nao encontrei uma URL oficial suficiente."
	}
	return strings.TrimSpace(reply) + "\n\nFontes verificadas:\n- " + strings.Join(sources, "\n- ")
}

func (r AnswerabilityReport) Refusal() string {
	msg := "Nao encontrei evidencia suficiente em fontes confiaveis para responder sem risco de alucinar."
	if r.Reason != "" {
		msg += " Motivo: " + r.Reason + "."
	}
	if len(r.Blocked) > 0 {
		msg += " Tambem ignorei URL(s) fora da lista confiavel."
	}
	msg += " Me mande uma fonte oficial ou reformule com produto/versao/topico mais especifico que eu pesquiso de novo."
	return msg
}

func BuildGroundedChatPrompt(msg string) (string, AnswerabilityReport) {
	if len(msg) > 1500 {
		msg = msg[:1500]
	}
	report := CheckAnswerability(msg, "CKA")
	context := ""
	if report.Context != "" && len(report.Sources) > 0 {
		ctx := sanitizeRetrievedText(report.Context)
		if len(ctx) > 4500 {
			ctx = ctx[:4500]
		}
		context += "\n\nCONTEXTO PESQUISADO EM FONTES CONFIAVEIS:\n" + ctx
	}
	if report.Evidence != "" {
		context += "\n\nEVIDENCIAS CURRICULARES RECUPERADAS:\n" + report.Evidence
	}
	if report.RAG != "" {
		context += "\n\nCHUNKS VETORIAIS RECUPERADOS:\n" + report.RAG
	}
	if len(report.VerifiedSources()) > 0 {
		context += "\n\nFONTES RECUPERADAS (IDs permitidos):"
		for i, source := range report.VerifiedSources() {
			context += fmt.Sprintf("\n[S%d] %s", i+1, source)
		}
	}
	context += fmt.Sprintf("\n\nFUNDAMENTACAO: confianca %d/100; verificado em %s; motivo: %s.", report.Confidence, report.CheckedAt, report.Reason)

	prompt := fmt.Sprintf(`Voce e o Tutor do k8s-lab: um mentor especialista em infraestrutura, cloud, IaC e programacao. Responda em portugues do Brasil, direto e didatico, em NO MAXIMO 6 frases.

REGRAS ANTI-ALUCINACAO:
- Use somente fatos sustentados pelo contexto, evidencias, RAG ou conhecimento tecnico basico e estavel.
- Se faltar evidencia para uma parte da pergunta, diga exatamente o que nao foi encontrado.
- Toda frase com afirmacao tecnica deve terminar com um ou mais IDs de fonte, por exemplo [S1] ou [S1][S2].
- Use apenas IDs listados em FONTES RECUPERADAS. Se nenhum ID sustentar uma afirmacao, omita a afirmacao.
- Quando usar inferencia, marque como "Inferencia:" e ainda cite a evidencia que sustenta a inferencia.
- Nao invente nem escreva URLs; o backend acrescenta somente fontes verificadas.
- Termine com "Fontes: IDs citados acima"; URLs sao controladas pelo backend.

ESCOPO: Kubernetes, AKS/Azure, containers, cloud (Azure/AWS/GCP), Terraform/IaC, Linux, redes, DevOps, CI/CD, GitOps/ArgoCD, Helm e programacao. So recuse se fugir totalmente de tecnologia.%s

Pergunta do aluno: %s`, context, strings.TrimSpace(msg))
	return prompt, report
}

func technicalQuestion(msg string) bool {
	return regexp.MustCompile(`(?i)\b(k8s|kubernetes|kubectl|pods?|deployments?|replicasets?|statefulsets?|daemonsets?|services?|ingress|configmaps?|secrets?|jobs?|cronjobs?|namespaces?|nodes?|pvc|pv|persistentvolumes?|probes?|liveness|readiness|quotas?|helm|docker|containers?|linux|bash|shell|java|terraform|iac|ansible|aws|azure|gcp|aks|eks|cloud|devops|ci/cd|gitops|argocd|prometheus|grafana|cilium|network|dns|rbac|iam|s3|sqs|vpc|ec2|hpa|autoscal|replica|scale)\b`).MatchString(msg)
}

func specificLabSubject(msg string) string {
	if !labAskRe.MatchString(msg) && !regexp.MustCompile(`(?i)\b(quest|pergunta|praticar|treinar)\w*`).MatchString(msg) {
		return ""
	}
	l := strings.ToLower(msg)
	re := regexp.MustCompile(`(?i)(?:sobre|topico|t[oó]pico|tema|assunto|voltado para|para|de)\s+(.+)$`)
	m := re.FindStringSubmatch(l)
	if len(m) < 2 {
		return ""
	}
	subject := m[1]
	subject = regexp.MustCompile(`(?i)\b(nivel|nível)\s+\d+\b.*$`).ReplaceAllString(subject, "")
	subject = regexp.MustCompile(`(?i)\b(com|usando|utilizando|que|onde|e ele|e depois)\b.*$`).ReplaceAllString(subject, "")
	terms := meaningfulSubjectTerms(subject)
	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " ")
}

func meaningfulSubjectTerms(text string) []string {
	text = normalizeEvidenceText(text)
	stop := map[string]bool{
		"um": true, "uma": true, "uns": true, "umas": true, "de": true, "do": true, "da": true, "dos": true, "das": true,
		"para": true, "por": true, "sobre": true, "topico": true, "tema": true, "assunto": true, "lab": true, "labs": true,
		"laboratorio": true, "exercicio": true, "questao": true, "pergunta": true, "crie": true, "criar": true, "gere": true,
		"gerar": true, "k8s": true, "kubernetes": true, "infra": true, "cloud": true, "certificacao": true, "cert": true,
		"aws": true, "azure": true, "gcp": true, "cka": true, "ckad": true, "cks": true, "az": true,
	}
	var out []string
	seen := map[string]bool{}
	for _, t := range strings.Fields(text) {
		t = strings.Trim(t, ".,;:!?()[]{}")
		if len(t) < 3 || stop[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

func LabRequestAdherence(q models.Question, request string) error {
	subject := specificLabSubject(request)
	if subject == "" {
		return nil
	}
	cert := routeCertForLabRequest(string(q.Cert), request, q.Topic)
	exact := exactTopicForRequest(cert, request)
	if exact == "" {
		exact = detectTopic(request)
	}
	if exact != "" {
		if q.Topic == exact {
			return nil
		}
		for _, allowed := range fallbackTopicsForCert(cert, request) {
			if q.Topic == allowed && strings.Contains(strings.ToLower(subject), strings.ToLower(allowed)) {
				return nil
			}
		}
		return fmt.Errorf("pedido pediu %q, mas o lab gerado ficou em %q", exact, q.Topic)
	}

	bag := strings.ToLower(q.Topic + " " + q.Question + " " + q.AnswerCommand + " " + q.DocURL + " " + q.DocSection)
	if q.LabSpec != nil {
		bag += " " + strings.ToLower(q.LabSpec.Objective+" "+q.LabSpec.Scenario)
		for _, e := range q.LabSpec.Evidence {
			bag += " " + strings.ToLower(e.Domain+" "+strings.Join(e.MatchedTerms, " "))
		}
		for _, c := range q.LabSpec.Chunks {
			bag += " " + strings.ToLower(c.Domain+" "+c.Title+" "+c.Excerpt)
		}
	}
	for _, term := range meaningfulSubjectTerms(subject) {
		if strings.Contains(bag, term) {
			return nil
		}
	}
	return fmt.Errorf("nao encontrei template/evidencia suficiente para criar lab especificamente sobre %q sem cair em conteudo generico", subject)
}

func LabRequestPreflight(msg, activeCert string) error {
	subject := specificLabSubject(msg)
	if subject == "" {
		return nil
	}
	cert := inferCertFromMessage(msg, activeCert)
	if exactTopicForRequest(cert, msg) != "" || detectTopic(msg) != "" {
		return nil
	}
	report := CheckAnswerability(msg, cert)
	if report.Confidence >= 45 && len(report.Sources) > 0 {
		return nil
	}
	// Recusa ACIONÁVEL: dizer só "não sei" deixa o aluno num beco (bug real com
	// KCNA). O caminho existe — colar a URL oficial faz o tutor aprender o
	// currículo sozinho e a cert virar primeira classe.
	return fmt.Errorf("nao encontrei fonte/template confiavel para %q. Se for uma certificacao, **cole a URL da pagina oficial do exame** (ex.: training.linuxfoundation.org) que eu leio, aprendo o curriculo e monto os labs — prefiro pedir a fonte a inventar um lab generico", subject)
}
