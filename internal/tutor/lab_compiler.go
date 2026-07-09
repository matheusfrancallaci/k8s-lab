package tutor

import (
	"regexp"
	"strings"

	"estudo-app/internal/models"
)

var (
	kubectlNSFlagRe     = regexp.MustCompile(`(?i)(?:^|\s)(?:-n|--namespace)\s+([a-z0-9]([-a-z0-9]*[a-z0-9])?)\b`)
	kubectlNSFlagEqRe   = regexp.MustCompile(`(?i)(?:^|\s)--namespace=([a-z0-9]([-a-z0-9]*[a-z0-9])?)\b`)
	kubectlCreateNSRe   = regexp.MustCompile(`(?i)\bkubectl\s+(?:create\s+)?(?:ns|namespace)\s+([a-z0-9]([-a-z0-9]*[a-z0-9])?)\b`)
	kubectlCmdStartRe   = regexp.MustCompile(`(?i)(^|[;&|]\s*)kubectl\s+`)
	kubectlHasNSRe      = regexp.MustCompile(`(?i)(?:^|\s)(?:-n\s+|--namespace(?:=|\s+))`)
	kubectlNamespacedRe = regexp.MustCompile(`(?i)\bkubectl\s+(?:get|describe|create|run|expose|apply|delete|label|annotate|scale|rollout\s+status|wait|logs|exec|set\s+resources|patch)\s+([a-z0-9./-]+)`)
	kubectlClusterResRe = regexp.MustCompile(`(?i)^(?:ns|namespace|namespaces|node|nodes|pv|persistentvolume|persistentvolumes|storageclass|storageclasses|clusterrole|clusterroles|clusterrolebinding|clusterrolebindings|crd|crds|customresourcedefinition|customresourcedefinitions)(?:/|$)`)
	validationShellRe   = regexp.MustCompile(`(?i)\b(echo\s+OK\s+\|\|\s+echo\s+FAIL|&&\s+echo\s+OK|\|\|\s+echo\s+FAIL)\b`)
)

// CompileLab is the deterministic final compiler pass for generated labs. It
// keeps setup, solution, validation, teardown, RBAC planning and UI metadata in
// one contract so labs do not drift across namespaces or expose internal labels.
func CompileLab(q models.Question, request string) models.Question {
	if q.Type != models.Lab {
		return q
	}
	ns := InferLabNamespace(q)
	if ns != "" && ns != "default" {
		q = alignLabNamespace(q, ns)
	}
	q = hardenValidations(q)
	return q
}

func InferLabNamespace(q models.Question) string {
	text := strings.Join([]string{
		q.AnswerCommand,
		validationText(q),
		compileSetupText(q),
		strings.Join(q.Teardown, "\n"),
	}, "\n")
	candidates := extractNamespaces(text)
	for _, ns := range candidates {
		if ns != "" {
			return ns
		}
	}
	return "default"
}

func extractNamespaces(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ns string) {
		ns = strings.Trim(strings.ToLower(ns), "`'\".,;:()[]{}")
		if ns == "" || ns == "all" || ns == "true" || seen[ns] {
			return
		}
		seen[ns] = true
		out = append(out, ns)
	}
	for _, re := range []*regexp.Regexp{kubectlNSFlagRe, kubectlNSFlagEqRe, kubectlCreateNSRe} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				add(m[1])
			}
		}
	}
	return out
}

func compileSetupText(q models.Question) string {
	var b strings.Builder
	for _, s := range q.Setup {
		b.WriteString(s.Command)
		b.WriteByte('\n')
	}
	return b.String()
}

func validationText(q models.Question) string {
	var b strings.Builder
	if q.Validation != nil {
		b.WriteString(q.Validation.Command)
		b.WriteByte('\n')
	}
	for _, g := range q.Goals {
		if g.Validation != nil {
			b.WriteString(g.Validation.Command)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func alignLabNamespace(q models.Question, ns string) models.Question {
	q.AnswerCommand = addNamespaceToKubectl(q.AnswerCommand, ns)
	if q.Validation != nil {
		q.Validation.Command = addNamespaceToKubectl(q.Validation.Command, ns)
	}
	for i := range q.Setup {
		q.Setup[i].Command = addNamespaceToKubectl(q.Setup[i].Command, ns)
	}
	for i := range q.Goals {
		if q.Goals[i].Validation != nil {
			q.Goals[i].Validation.Command = addNamespaceToKubectl(q.Goals[i].Validation.Command, ns)
		}
	}
	for i := range q.Teardown {
		q.Teardown[i] = addNamespaceToKubectl(q.Teardown[i], ns)
	}
	return q
}

func addNamespaceToKubectl(cmd, ns string) string {
	if strings.TrimSpace(cmd) == "" || ns == "" || ns == "default" {
		return cmd
	}
	parts := splitShellSegments(cmd)
	for i, p := range parts {
		parts[i] = addNamespaceToSegment(p, ns)
	}
	return strings.Join(parts, "")
}

func splitShellSegments(cmd string) []string {
	var out []string
	start := 0
	for i, r := range cmd {
		if r == ';' || r == '|' || r == '&' {
			if start < i {
				out = append(out, cmd[start:i])
			}
			out = append(out, string(r))
			start = i + 1
		}
	}
	if start < len(cmd) {
		out = append(out, cmd[start:])
	}
	return out
}

func addNamespaceToSegment(segment, ns string) string {
	if !strings.Contains(strings.ToLower(segment), "kubectl") || kubectlHasNSRe.MatchString(segment) {
		return segment
	}
	m := kubectlNamespacedRe.FindStringSubmatch(segment)
	if len(m) < 2 {
		return segment
	}
	res := strings.TrimPrefix(strings.ToLower(m[1]), "resource/")
	if kubectlClusterResRe.MatchString(res) {
		return segment
	}
	return kubectlCmdStartRe.ReplaceAllString(segment, "${1}kubectl -n "+ns+" ")
}

func hardenValidations(q models.Question) models.Question {
	if q.Validation != nil {
		q.Validation.Command = makeValidationDeterministic(q.Validation.Command)
	}
	for i := range q.Goals {
		if q.Goals[i].Validation != nil {
			q.Goals[i].Validation.Command = makeValidationDeterministic(q.Goals[i].Validation.Command)
		}
	}
	return q
}

func makeValidationDeterministic(cmd string) string {
	if strings.TrimSpace(cmd) == "" || validationShellRe.MatchString(cmd) {
		return cmd
	}
	if strings.Contains(cmd, "jsonpath") || strings.Contains(cmd, "grep -q") || strings.Contains(cmd, "test ") {
		return cmd
	}
	return cmd
}
