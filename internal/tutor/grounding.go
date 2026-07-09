package tutor

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"estudo-app/internal/models"
)

type AnswerabilityReport struct {
	Answerable bool
	Confidence int
	Reason     string
	Sources    []string
	Blocked    []string
	Evidence   string
	RAG        string
	Context    string
	Cert       string
	Topic      string
	CheckedAt  string
}

func CheckAnswerability(msg, activeCert string) AnswerabilityReport {
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

	answerable := conf >= 45
	if !technicalQuestion(msg) {
		answerable = true
	}
	reason := strings.Join(reasons, "; ")
	if reason == "" {
		reason = "sem fonte, evidencia ou template confiavel para esse pedido"
	}
	return AnswerabilityReport{
		Answerable: answerable,
		Confidence: conf,
		Reason:     reason,
		Sources:    sources,
		Blocked:    blocked,
		Evidence:   evidence,
		RAG:        rag,
		Context:    enriched,
		Cert:       cert,
		Topic:      topic,
		CheckedAt:  time.Now().UTC().Format("2006-01-02"),
	}
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
		ctx := report.Context
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
	if len(report.Sources) > 0 {
		context += "\n\nFONTES RECUPERADAS:\n- " + strings.Join(report.Sources, "\n- ")
	}
	context += fmt.Sprintf("\n\nFUNDAMENTACAO: confianca %d/100; verificado em %s; motivo: %s.", report.Confidence, report.CheckedAt, report.Reason)

	prompt := fmt.Sprintf(`Voce e o Tutor do k8s-lab: um mentor especialista em infraestrutura, cloud, IaC e programacao. Responda em portugues do Brasil, direto e didatico, em NO MAXIMO 6 frases.

REGRAS ANTI-ALUCINACAO:
- Use somente fatos sustentados pelo contexto, evidencias, RAG ou conhecimento tecnico basico e estavel.
- Se faltar evidencia para uma parte da pergunta, diga exatamente o que nao foi encontrado.
- Quando usar inferencia, marque como "Inferencia:".
- Termine com "Fontes:" listando URLs usadas; se nao houver URL recuperada, escreva "Fontes: base interna/RAG" ou "Fontes: nao encontrei fonte confiavel suficiente".

ESCOPO: Kubernetes, AKS/Azure, containers, cloud (Azure/AWS/GCP), Terraform/IaC, Linux, redes, DevOps, CI/CD, GitOps/ArgoCD, Helm e programacao. So recuse se fugir totalmente de tecnologia.%s

Pergunta do aluno: %s`, context, strings.TrimSpace(msg))
	return prompt, report
}

func technicalQuestion(msg string) bool {
	return regexp.MustCompile(`(?i)\b(k8s|kubernetes|kubectl|pod|deployment|replicaset|service|ingress|helm|docker|container|linux|bash|shell|java|terraform|iac|ansible|aws|azure|gcp|aks|eks|cloud|devops|ci/cd|gitops|argocd|prometheus|grafana|cilium|network|dns|rbac|iam|s3|sqs|vpc|ec2|hpa|autoscal|replica|scale)\b`).MatchString(msg)
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
	return fmt.Errorf("nao encontrei fonte/template confiavel para criar um lab especificamente sobre %q; prefiro falhar explicitamente em vez de gerar um lab generico", subject)
}
