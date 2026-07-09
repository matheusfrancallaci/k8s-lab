package tutor

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"estudo-app/internal/models"
)

var k8sVersionHintRe = regexp.MustCompile(`(?i)\bkubernetes\s*(v?1\.[0-9]+(?:\.[0-9]+)?)\b`)

type LabPreflightReport struct {
	Checks []string
	Risks  []string
	Errors []string
}

func BuildLabPlan(q models.Question, request string, spec models.LabSpec) models.LabPlan {
	report := LabPreflight(q)
	return models.LabPlan{
		ExactTopic:      q.Topic,
		Cert:            string(q.Cert),
		CheckedAt:       time.Now().UTC().Format("2006-01-02"),
		SourceVersion:   sourceVersion(q, request),
		Namespace:       spec.Namespace,
		Resources:       labResources(q),
		Validations:     labValidationCommands(q),
		Risks:           append([]string{}, report.Risks...),
		PreflightChecks: append([]string{}, report.Checks...),
		Sources:         append([]models.LabSource{}, spec.Sources...),
	}
}

func LabPreflight(q models.Question) LabPreflightReport {
	var rep LabPreflightReport
	check := func(s string) { rep.Checks = append(rep.Checks, s) }
	risk := func(s string) { rep.Risks = append(rep.Risks, s) }
	fail := func(s string) { rep.Errors = append(rep.Errors, s) }

	if q.Type != models.Lab {
		check("nao e lab")
		return rep
	}
	if strings.TrimSpace(q.Question) == "" {
		fail("enunciado vazio")
	} else if strings.Contains(strings.ToLower(q.Question), "lab maker") {
		fail("label interno exposto no enunciado")
	} else if questionHasReadyCommand(q.Question) {
		fail("enunciado principal contem comando pronto")
	} else {
		check("enunciado em modo desafio")
	}

	goals := 0
	validations := labValidationCommands(q)
	for _, g := range q.Goals {
		if strings.TrimSpace(g.Description) != "" {
			goals++
		}
	}
	if goals == 0 {
		fail("sem goals")
	} else {
		check(fmt.Sprintf("%d goal(s) declarados", goals))
	}
	if len(validations) == 0 {
		fail("sem validacao automatica")
	} else {
		check(fmt.Sprintf("%d validador(es) automaticos", len(validations)))
	}

	if q.AnswerCommand == "" {
		risk("sem solucao reproduzivel")
	} else {
		check("solucao reproduzivel declarada")
	}
	ns := InferLabNamespace(q)
	if ns == "" {
		fail("namespace de validacao ausente")
	} else {
		check("namespace alinhado: " + ns)
	}
	for _, cmd := range append(append([]string{}, setupCommands(q)...), validations...) {
		if reason := BlockedLabCommandReason(cmd); reason != "" {
			fail("comando bloqueado pelo guardrail: " + reason)
		}
	}
	if len(q.Teardown) == 0 {
		risk("sem limpeza automatica")
	} else {
		check("cleanup declarado")
	}
	if q.DocURL == "" && (q.LabSpec == nil || len(q.LabSpec.Sources) == 0) {
		risk("fonte oficial ausente")
	} else {
		check("fonte/evidencia rastreavel")
	}
	return rep
}

// questionHasReadyCommand detecta um comando pronto DENTRO de crases no
// enunciado (o que o modo desafio proíbe). Checa o conteúdo de cada span
// `...` — e não "palavra-comando E crase soltas em qualquer lugar", que dava
// falso positivo quando o tópico era um comando (bash/java/terraform/kubectl)
// e a redação do HideLabSpoilers deixava uma crase de `comando apropriado`.
func questionHasReadyCommand(text string) bool {
	for _, m := range inlineCommandRe.FindAllStringSubmatch(text, -1) {
		if len(m) == 2 && commandLikeRe.MatchString(m[1]) {
			return true
		}
	}
	return false
}

func LabDeliveryPreflight(q models.Question) error {
	rep := LabPreflight(q)
	if len(rep.Errors) > 0 {
		return fmt.Errorf("preflight do lab falhou: %s", strings.Join(rep.Errors, "; "))
	}
	return nil
}

func setupCommands(q models.Question) []string {
	var out []string
	for _, s := range q.Setup {
		if strings.TrimSpace(s.Command) != "" {
			out = append(out, s.Command)
		}
	}
	return out
}

func labValidationCommands(q models.Question) []string {
	var out []string
	if q.Validation != nil && strings.TrimSpace(q.Validation.Command) != "" {
		out = append(out, q.Validation.Command)
	}
	for _, g := range q.Goals {
		if g.Validation != nil && strings.TrimSpace(g.Validation.Command) != "" {
			out = append(out, g.Validation.Command)
		}
	}
	return out
}

func labResources(q models.Question) []string {
	text := strings.ToLower(strings.Join([]string{
		q.Topic,
		q.Question,
		q.AnswerCommand,
		setupText(q),
		validationText(q),
		strings.Join(q.Teardown, " "),
	}, " "))
	resourceTerms := []string{
		"namespace", "pod", "deployment", "replicaset", "service", "ingress", "configmap", "secret",
		"pvc", "pv", "job", "cronjob", "hpa", "networkpolicy", "serviceaccount", "role", "rolebinding",
		"helm chart", "dockerfile", "terraform", "localstack", "s3", "sqs", "iam", "java", "bash",
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range resourceTerms {
		if strings.Contains(text, r) && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out
}

func sourceVersion(q models.Question, request string) string {
	text := q.DocURL + " " + q.DocSection + " " + q.Question + " " + request
	if m := k8sVersionHintRe.FindStringSubmatch(text); len(m) > 1 {
		return "Kubernetes " + strings.TrimPrefix(m[1], "v")
	}
	switch {
	case strings.Contains(q.DocURL, "kubernetes.io"):
		return "Kubernetes docs, verificado " + time.Now().UTC().Format("2006-01-02")
	case strings.Contains(q.DocURL, "developer.hashicorp.com"):
		return "HashiCorp Terraform docs, verificado " + time.Now().UTC().Format("2006-01-02")
	case strings.Contains(q.DocURL, "docs.aws.amazon.com"):
		return "AWS docs, verificado " + time.Now().UTC().Format("2006-01-02")
	case strings.Contains(q.DocURL, "learn.microsoft.com"):
		return "Microsoft Learn, verificado " + time.Now().UTC().Format("2006-01-02")
	case strings.Contains(q.DocURL, "helm.sh"):
		return "Helm docs, verificado " + time.Now().UTC().Format("2006-01-02")
	case strings.Contains(q.DocURL, "docs.docker.com"):
		return "Docker docs, verificado " + time.Now().UTC().Format("2006-01-02")
	default:
		return "fontes internas/RAG, verificado " + time.Now().UTC().Format("2006-01-02")
	}
}
