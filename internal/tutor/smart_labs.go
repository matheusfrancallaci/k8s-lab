package tutor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"estudo-app/internal/models"
)

var (
	certCodeRe = regexp.MustCompile(`(?i)\b(?:AZ|AI|DP|SC|MS|PL|MB)-\d{3}\b|\b(?:CLF|SAA|DVA|SOA|SAP|MLS|DOP|DEA|ANS|SCS)-C\d{2}\b|\b(?:CKA|CKAD|CKS)\b`)
	labAskRe   = regexp.MustCompile(`(?i)\b(lab|laborat|exerc[ií]cio|pr[aá]tica|hands.?on|simula[çc][aã]o)\b`)
)

type SmartLabReport struct {
	Cert    string
	Topic   string
	Reason  string
	Ingest  IngestReport
	UsedLLM bool
}

func isBroadLabRequest(msg string) bool {
	l := strings.ToLower(msg)
	if !labAskRe.MatchString(l) {
		if !regexp.MustCompile(`(?i)\b(quest|pergunta)\w*`).MatchString(l) {
			return false
		}
	}
	return regexp.MustCompile(`(?i)\b(cri|ger|mont|faz|fa[cç]a|quero|preciso|prepara|estudar|treinar)\w*`).MatchString(l) ||
		certCodeRe.MatchString(msg) ||
		detectTopic(msg) != ""
}

// certNamedInMessage devolve a certificação citada EXPLICITAMENTE na mensagem
// (registrada ou código conhecido), sem fallback para a cert ativa — quem
// precisa distinguir "citou" de "herdou o chip" usa isto.
func certNamedInMessage(msg string) (string, bool) {
	if regexp.MustCompile(`(?i)\bCAPA\b`).MatchString(msg) {
		return "CAPA", true
	}
	for _, c := range AllCerts() {
		if regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(c) + `\b`).MatchString(msg) {
			return c, true
		}
	}
	if m := certCodeRe.FindString(msg); m != "" {
		return strings.ToUpper(strings.ReplaceAll(m, " ", "-")), true
	}
	return "", false
}

func inferCertFromMessage(msg, active string) string {
	if c, ok := certNamedInMessage(msg); ok {
		return c
	}
	if active != "" {
		return active
	}
	return "CKA"
}

func GenerateSmartLabs(msg, activeCert string, level, count int) ([]models.Question, SmartLabReport, error) {
	if count < 1 {
		count = 5
	}
	if count > 8 {
		count = 8
	}
	cert := inferCertFromMessage(msg, activeCert)
	userTopic := exactTopicForRequest(cert, msg)
	if userTopic == "" {
		userTopic = detectTopic(msg)
	}
	if isAWSFocus(cert, msg) {
		cert = "AWS"
		if userTopic == "" || !strings.HasPrefix(userTopic, "AWS ") {
			userTopic = exactTopicForRequest(cert, msg)
		}
		if count < len(certTemplateTopics["AWS"]) && userTopic == "" {
			count = len(certTemplateTopics["AWS"])
		}
	}
	if name, _ := RegisterCert(cert); name != "" {
		cert = name
	}
	if err := LabRequestPreflight(msg, cert); err != nil {
		return nil, SmartLabReport{Cert: cert, Topic: userTopic}, err
	}
	WarmRAG(cert, msg)

	enriched, sources, blocked := EnrichSource(msg)
	rep := SmartLabReport{Cert: cert, Topic: userTopic, Ingest: IngestReport{Sources: sources}}
	for _, b := range blocked {
		rep.Ingest.Notes = append(rep.Ingest.Notes, "URL ignorada por seguranca: "+b)
	}

	var topics []string
	if rep.Topic != "" {
		topics = []string{rep.Topic}
		rep.Reason = "topico exato detectado por regras da certificacao antes de consultar o LLM"
	} else if isAWSFocus(cert, msg) && rep.Topic == "" {
		topics = fallbackTopicsForCert(cert, msg)
		rep.Reason = "pedido generico de AWS mapeado para fundamentos iniciais: compute, networking, IAM, storage e messaging"
	} else {
		var reason string
		var usedLLM bool
		topics, reason, usedLLM = planLabTopics(enriched, cert, count)
		rep.Reason, rep.UsedLLM = reason, usedLLM
	}
	if len(topics) == 0 && rep.Topic != "" {
		topics = []string{rep.Topic}
	}
	if len(topics) == 0 && specificLabSubject(msg) != "" {
		return nil, rep, fmt.Errorf("nao encontrei template/evidencia suficiente para esse pedido especifico; nao vou trocar por um lab generico")
	}
	if len(topics) == 0 {
		topics = fallbackTopicsForCert(cert, msg)
	}
	if len(topics) == 0 {
		topics = certTemplateTopics["CKA"]
	}

	var qs []models.Question
	for i := 0; i < count; i++ {
		topic := topics[i%len(topics)]
		qs = append(qs, generateQuestions(topic, cert, level, 1)...)
	}
	if len(qs) == 0 {
		return nil, rep, fmt.Errorf("nao consegui mapear esse pedido para um lab seguro")
	}
	qs = FinalizeLabs(qs, msg)
	for _, q := range qs {
		if err := LabQualityGate(q); err != nil {
			return nil, rep, err
		}
		if err := LabRequestAdherence(q, msg); err != nil {
			return nil, rep, err
		}
	}
	if err := persist(qs); err != nil {
		return nil, rep, err
	}
	if err := RecordLabCatalog(qs); err != nil {
		return nil, rep, err
	}
	rep.Ingest.Labs = len(qs)
	for _, q := range qs {
		rep.Ingest.Generated = append(rep.Ingest.Generated, q.ID)
	}
	if rep.Topic == "" {
		rep.Topic = topics[0]
	}
	return qs, rep, nil
}

func isAWSFocus(cert, msg string) bool {
	l := strings.ToLower(cert + " " + msg)
	return strings.EqualFold(cert, "AWS") ||
		strings.Contains(l, "aws") ||
		strings.Contains(l, "amazon web services") ||
		strings.Contains(l, "ec2") ||
		strings.Contains(l, "vpc") ||
		strings.Contains(l, "iam") ||
		strings.Contains(l, "s3") ||
		strings.Contains(l, "sqs") ||
		strings.Contains(l, "simple queue") ||
		strings.Contains(l, "eks") ||
		strings.Contains(l, "lambda") ||
		strings.Contains(l, "dynamodb") ||
		strings.Contains(l, "cloudwatch") ||
		regexp.MustCompile(`(?i)\b(?:CLF|SAA|DVA|SOA|SAP|MLS|DOP|DEA|ANS|SCS)-C\d{2}\b`).MatchString(msg)
}

func exactTopicForRequest(cert, msg string) string {
	l := strings.ToLower(cert + " " + msg)
	switch {
	case isAWSFocus(cert, msg) && regexp.MustCompile(`(?i)\bsqs\b|simple queue|fila|mensager`).MatchString(l):
		return "AWS Messaging"
	case isAWSFocus(cert, msg) && regexp.MustCompile(`(?i)\bs3\b|bucket|object storage|storage|armazen`).MatchString(l):
		return "AWS Storage"
	case isAWSFocus(cert, msg) && regexp.MustCompile(`(?i)\biam\b|identity|policy|usuario|usu[aÃ¡]rio|permiss`).MatchString(l):
		return "AWS IAM"
	case isAWSFocus(cert, msg) && regexp.MustCompile(`(?i)\bvpc\b|subnet|security group|network`).MatchString(l):
		return "AWS Networking"
	case isAWSFocus(cert, msg) && regexp.MustCompile(`(?i)\bec2\b|eks\b|compute|inst[aÃ¢]ncia|workload`).MatchString(l):
		return "AWS Compute"
	case regexp.MustCompile(`(?i)\bhpa\b|horizontal.?pod.?autoscal|autoscal|auto.?scal`).MatchString(l):
		return "Autoscaling"
	case regexp.MustCompile(`(?i)\breplica.?set\b|\breplicaset\b|\bscale.?set\b`).MatchString(l):
		return "ReplicaSet"
	case regexp.MustCompile(`(?i)\blinux\b|chmod|permiss(?:ao|[ãa]o)|logs?|grep|awk|sed`).MatchString(l):
		return "Linux"
	case regexp.MustCompile(`(?i)\bbash\b|shell script|script bash|argumentos?|csv`).MatchString(l):
		return "Bash"
	case regexp.MustCompile(`(?i)\bjava\b|javac|jvm|spring|classe|public static void main`).MatchString(l):
		return "Java"
	case regexp.MustCompile(`(?i)\bhelm\b|helm chart|values\.yaml|chart\.yaml`).MatchString(l):
		return "Helm"
	case regexp.MustCompile(`(?i)\bdockerfile\b|\bdocker\b|containerfile|container image`).MatchString(l):
		return "Docker"
	case regexp.MustCompile(`(?i)\brollout\b|rollback|rolling update`).MatchString(l):
		return "Workloads"
	case regexp.MustCompile(`(?i)\btaint\b|toleration|node.?affinity|node.?selector|scheduling|agend`).MatchString(l):
		return "Scheduling"
	case regexp.MustCompile(`(?i)\bdns\b|coredns|clusterip|nodeport|loadbalancer|service\b|ingress|gateway api`).MatchString(l):
		return "Services"
	case regexp.MustCompile(`(?i)\bpvc\b|\bpv\b|persistent|volume|storageclass`).MatchString(l):
		return "Storage"
	case regexp.MustCompile(`(?i)\bconfigmap\b|secret|env|vari[aÃ¡]vel`).MatchString(l):
		return "Configuration"
	case regexp.MustCompile(`(?i)\bprobe\b|liveness|readiness|startup|init.?container`).MatchString(l):
		return "Application Design"
	case regexp.MustCompile(`(?i)\brbac\b|network.?policy|security.?context|pod security|serviceaccount`).MatchString(l):
		return "Security"
	case regexp.MustCompile(`(?i)troubleshoot|debug|incidente|diagn[oÃ³]stic|quebrad|erro`).MatchString(l):
		return "Troubleshooting"
	case regexp.MustCompile(`(?i)\bpod\b|namespace|quota`).MatchString(l):
		return "Core Concepts"
	}
	return ""
}

func planLabTopics(context, cert string, count int) ([]string, string, bool) {
	ok, _ := LLMStatus()
	if !ok {
		return nil, "", false
	}
	available := Topics()
	sort.Strings(available)
	if len(context) > 5000 {
		context = context[:5000]
	}
	evidence := EvidenceContext(cert, "", context, 4)
	if evidence == "" {
		evidence = "sem evidencias oficiais fortes detectadas; use somente os topicos permitidos e prefira fallback seguro"
	}
	if rag, _ := RAGContext(cert, "", context, 4); rag != "" {
		evidence += "\n\nChunks vetoriais recuperados:\n" + rag
	}
	prompt := fmt.Sprintf(`Voce e um planejador de labs para um app hospedado em AKS. Mapeie o pedido do aluno para topicos Kubernetes seguros que podem rodar no cluster ja existente.

Certificacao/foco: %s
Quantidade desejada: %d
Topicos permitidos: %s
Evidencias oficiais recuperadas:
%s

Regras:
- Escolha apenas topicos da lista permitida.
- Use as evidencias para evitar alucinacao de topico ou escopo.
- Prefira AKS/Kubernetes pratico, mesmo quando a certificacao for Azure, AWS, DevOps ou generica.
- Nao invente ferramentas externas nem recursos de nuvem que exijam credenciais.
- Se a evidencia apontar para AWS, prefira topicos AWS que rodam via LocalStack.
- Se a evidencia apontar para ArgoCD/CAPA, prefira GitOps.
- Responda somente JSON: {"topics":["Workloads"],"reason":"..."}

Pedido e contexto:
%s`, cert, count, strings.Join(available, ", "), evidence, context)

	raw, err := llmGenerateContract(prompt, "topic-selection", 60*time.Second, 500, routerModel())
	if err != nil {
		return nil, "", false
	}
	var out struct {
		Topics []string `json:"topics"`
		Reason string   `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, "", false
	}
	allowed := map[string]bool{}
	for _, t := range available {
		allowed[t] = true
	}
	var topics []string
	seen := map[string]bool{}
	for _, t := range out.Topics {
		t = strings.TrimSpace(t)
		if allowed[t] && !seen[t] {
			topics = append(topics, t)
			seen[t] = true
		}
	}
	return topics, strings.TrimSpace(out.Reason), len(topics) > 0
}

func fallbackTopicsForCert(cert, msg string) []string {
	if topics := certTemplateTopics[cert]; len(topics) > 0 {
		return topics
	}
	l := strings.ToLower(cert + " " + msg)
	switch {
	case strings.Contains(l, "hpa") || strings.Contains(l, "autoscal"):
		return []string{"Autoscaling", "Workloads"}
	case strings.Contains(l, "argocd") || strings.Contains(l, "argo cd") || strings.Contains(l, "gitops"):
		return []string{"GitOps", "Workloads"}
	case isAWSFocus(cert, msg):
		return []string{"AWS Compute", "AWS Networking", "AWS IAM", "AWS Storage", "AWS Messaging"}
	case strings.Contains(l, "az-500") || strings.Contains(l, "sc-") || strings.Contains(l, "security"):
		return []string{"Security", "Configuration", "Services"}
	case strings.Contains(l, "az-104") || strings.Contains(l, "administrator") || strings.Contains(l, "aks"):
		return []string{"Core Concepts", "Services", "Storage", "Workloads", "Autoscaling"}
	case strings.Contains(l, "az-400") || strings.Contains(l, "devops"):
		return []string{"GitOps", "Workloads", "Configuration", "Services", "Security"}
	case strings.Contains(l, "az-204") || strings.Contains(l, "developer"):
		return []string{"Application Design", "Configuration", "Workloads", "Services"}
	case strings.Contains(l, "terraform"):
		return []string{"Core Concepts", "Storage", "Services"}
	case strings.Contains(l, "ansible"):
		return []string{"Configuration", "Workloads"}
	case strings.Contains(l, "java"):
		return []string{"Java"}
	case strings.Contains(l, "bash"):
		return []string{"Bash", "Linux"}
	case strings.Contains(l, "linux"):
		return []string{"Linux", "Bash"}
	case strings.Contains(l, "replicaset") || strings.Contains(l, "replica set") || strings.Contains(l, "scaleset") || strings.Contains(l, "scale set"):
		return []string{"ReplicaSet"}
	default:
		return []string{"Core Concepts", "Workloads", "Services"}
	}
}
