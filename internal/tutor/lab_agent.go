package tutor

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"estudo-app/internal/models"
)

const minimumLabQuality = 70

// FinalizeLabs enriches lab questions with a declarative spec and a quality gate.
func FinalizeLabs(qs []models.Question, request string) []models.Question {
	for i := range qs {
		qs[i] = FinalizeLab(qs[i], request)
	}
	return qs
}

// FinalizeLab is the last deterministic pass before a generated lab is stored.
// It gives the UI and the tutor a stable contract to explain, validate and reset.
func FinalizeLab(q models.Question, request string) models.Question {
	if q.Type != models.Lab {
		return q
	}
	q = HideLabSpoilers(q)
	q = CompileLab(q, request)
	if q.LabSpec == nil {
		spec := BuildLabSpec(q, request)
		q.LabSpec = &spec
		return q
	}
	spec := *q.LabSpec
	if strings.TrimSpace(spec.Objective) == "" {
		spec.Objective = labObjective(q)
	}
	if strings.TrimSpace(spec.Scenario) == "" {
		spec.Scenario = labScenario(q, request)
	}
	if strings.TrimSpace(spec.Namespace) == "" {
		spec.Namespace = InferLabNamespace(q)
	}
	if strings.TrimSpace(spec.ValidationMode) == "" {
		spec.ValidationMode = "compiled"
	}
	if len(spec.Skills) == 0 {
		spec.Skills = labSkills(q)
	}
	if spec.EstimatedMinutes == 0 {
		spec.EstimatedMinutes = estimatedMinutes(q)
	}
	if len(spec.Dependencies) == 0 {
		spec.Dependencies = labDependencies(q)
	}
	if len(spec.Evidence) == 0 || spec.EvidenceScore == 0 {
		spec.Evidence = labEvidence(q, request)
		spec.EvidenceScore = labEvidenceScore(spec.Evidence)
	}
	if len(spec.Chunks) == 0 {
		spec.Chunks = labChunks(q, request)
	}
	if len(spec.Sources) == 0 {
		spec.Sources = labSources(q, spec.Evidence, spec.Chunks)
	}
	if len(spec.Plan) == 0 {
		spec.Plan = labPlan(q)
	}
	if len(spec.SuccessCriteria) == 0 {
		spec.SuccessCriteria = labSuccessCriteria(q)
	}
	if len(spec.Safety) == 0 {
		spec.Safety = labSafety(q)
	}
	plan := BuildLabPlan(q, request, spec)
	spec.LabPlan = &plan
	spec.Quality = scoreLab(q, spec)
	q.LabSpec = &spec
	return q
}

func LabQualityGate(q models.Question) error {
	if q.Type != models.Lab {
		return nil
	}
	q = FinalizeLab(q, "")
	if q.LabSpec == nil {
		return fmt.Errorf("lab sem especificacao declarativa")
	}
	if q.LabSpec.Quality.Score < minimumLabQuality {
		return fmt.Errorf("lab abaixo do quality gate (%d/%d): %s",
			q.LabSpec.Quality.Score, minimumLabQuality, strings.Join(q.LabSpec.Quality.Warnings, "; "))
	}
	if err := LabDeliveryPreflight(q); err != nil {
		return err
	}
	return nil
}

func BlockedLabCommandReason(cmd string) string {
	l := strings.ToLower(strings.TrimSpace(cmd))
	dangerous := []struct {
		re     *regexp.Regexp
		reason string
	}{
		{regexp.MustCompile(`rm\s+-rf\s+/(?:\s|$)`), "recusa rm -rf no filesystem raiz"},
		{regexp.MustCompile(`\b(?:shutdown|reboot|halt|poweroff)\b`), "recusa desligamento do host"},
		{regexp.MustCompile(`\b(?:mkfs|fdisk|parted|dd)\b`), "recusa comandos de disco"},
		{regexp.MustCompile(`curl\b[^|;&]+[|]\s*(?:sudo\s+)?(?:sh|bash)`), "recusa pipe remoto para shell"},
		{regexp.MustCompile(`wget\b[^|;&]+[|]\s*(?:sudo\s+)?(?:sh|bash)`), "recusa pipe remoto para shell"},
		{regexp.MustCompile(`\bsudo\b`), "recusa sudo em setup de lab"},
		{regexp.MustCompile(`az\s+\w+\s+delete\b`), "recusa delete de recurso cloud"},
		{regexp.MustCompile(`kubectl\s+delete\s+(?:ns|namespace)\s+--all\b`), "recusa apagar todos os namespaces"},
		{regexp.MustCompile(`kubectl\s+delete\s+all\s+--all(?:\s+-a)?\s*(?:$|-A|--all-namespaces)`), "recusa apagar tudo no cluster"},
	}
	for _, d := range dangerous {
		if d.re.MatchString(l) {
			return d.reason
		}
	}
	return ""
}

func BuildLabSpec(q models.Question, request string) models.LabSpec {
	evidence := labEvidence(q, request)
	chunks := labChunks(q, request)
	spec := models.LabSpec{
		Objective:        labObjective(q),
		Scenario:         labScenario(q, request),
		Namespace:        InferLabNamespace(q),
		ValidationMode:   "compiled",
		Skills:           labSkills(q),
		EstimatedMinutes: estimatedMinutes(q),
		Dependencies:     labDependencies(q),
		Sources:          labSources(q, evidence, chunks),
		Evidence:         evidence,
		EvidenceScore:    labEvidenceScore(evidence),
		Chunks:           chunks,
		Plan:             labPlan(q),
		SuccessCriteria:  labSuccessCriteria(q),
		Safety:           labSafety(q),
	}
	plan := BuildLabPlan(q, request, spec)
	spec.LabPlan = &plan
	spec.Quality = scoreLab(q, spec)
	return spec
}

func labObjective(q models.Question) string {
	topic := strings.TrimSpace(q.Topic)
	if topic == "" {
		topic = "Lab"
	}
	switch {
	case strings.Contains(strings.ToLower(q.Question), "hpa"):
		return "Criar e validar autoscaling com HorizontalPodAutoscaler"
	case strings.Contains(strings.ToLower(q.Question), "argocd"):
		return "Declarar uma aplicacao GitOps e validar reconciliacao no ArgoCD"
	case strings.Contains(strings.ToLower(q.Topic), "aws"):
		return "Praticar API AWS em ambiente isolado com LocalStack no cluster"
	default:
		return "Praticar " + topic + " com comandos reais e validacao automatica"
	}
}

func labScenario(q models.Question, request string) string {
	if request = strings.TrimSpace(request); request != "" {
		return "Pedido do aluno: " + compactText(request, 180)
	}
	if q.Cert != "" {
		return fmt.Sprintf("Cenario hands-on para %s em %s.", q.Cert, q.Topic)
	}
	return "Cenario hands-on executado no cluster ativo."
}

func labSkills(q models.Question) []string {
	base := []string{}
	add := func(v string) {
		for _, x := range base {
			if strings.EqualFold(x, v) {
				return
			}
		}
		base = append(base, v)
	}
	add(string(q.Cert))
	add(q.Topic)
	l := strings.ToLower(q.Topic + " " + q.Question)
	switch {
	case strings.Contains(l, "hpa") || strings.Contains(l, "autoscal"):
		add("metrics-server")
		add("autoscaling")
	case strings.Contains(l, "service") || strings.Contains(l, "dns"):
		add("service discovery")
	case strings.Contains(l, "argocd") || strings.Contains(l, "gitops"):
		add("gitops")
	case strings.Contains(l, "aws") || strings.Contains(l, "s3") || strings.Contains(l, "sqs") || strings.Contains(l, "iam"):
		add("aws api")
		add("localstack")
	case strings.Contains(l, "networkpolicy") || strings.Contains(l, "rbac") || strings.Contains(l, "security"):
		add("security")
	}
	out := []string{}
	for _, v := range base {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func estimatedMinutes(q models.Question) int {
	min := 8 + len(q.Goals)*3
	if len(q.Setup) > 0 {
		min += 3
	}
	if strings.EqualFold(string(q.Difficulty), string(models.Hard)) {
		min += 5
	}
	if min < 10 {
		min = 10
	}
	if min > 35 {
		min = 35
	}
	return min
}

func labDependencies(q models.Question) []models.LabDependency {
	text := strings.ToLower(q.Topic + " " + q.Question + " " + setupText(q))
	var deps []models.LabDependency
	add := func(dep models.LabDependency) {
		for _, d := range deps {
			if strings.EqualFold(d.Name, dep.Name) {
				return
			}
		}
		deps = append(deps, dep)
	}
	if strings.Contains(text, "metrics-server") {
		add(models.LabDependency{
			Name:          "metrics-server",
			Kind:          "kubernetes addon",
			InstallAction: "auto setup",
			StatusCommand: "kubectl get deploy metrics-server -n kube-system",
			Required:      true,
		})
	}
	if strings.Contains(text, "localstack") || strings.Contains(strings.ToLower(q.Topic), "aws") {
		add(models.LabDependency{
			Name:          "localstack",
			Kind:          "aws emulator",
			InstallAction: "auto setup",
			StatusCommand: "kubectl get deploy localstack -n tools",
			Required:      true,
		})
	}
	if strings.Contains(text, "argocd") || strings.Contains(text, "argo cd") || strings.Contains(strings.ToLower(q.Topic), "gitops") {
		add(models.LabDependency{
			Name:          "argocd",
			Kind:          "gitops controller",
			InstallAction: "auto setup",
			StatusCommand: "kubectl get deploy argocd-server -n argocd",
			Required:      true,
		})
	}
	if len(deps) == 0 {
		add(models.LabDependency{
			Name:          "cluster ativo",
			Kind:          "kubernetes",
			InstallAction: "preflight",
			StatusCommand: "kubectl cluster-info",
			Required:      true,
		})
	}
	return deps
}

func labSources(q models.Question, evidence []models.LabEvidence, chunks []models.LabChunk) []models.LabSource {
	var out []models.LabSource
	add := func(s models.LabSource) {
		if strings.TrimSpace(s.URL) == "" {
			return
		}
		for _, x := range out {
			if x.URL == s.URL {
				return
			}
		}
		out = append(out, s)
	}
	if q.DocURL != "" {
		title := sourceTitle(q.DocURL)
		if q.DocSection != "" {
			title = q.DocSection
		}
		add(models.LabSource{Title: title, URL: q.DocURL, Section: q.DocSection})
	}
	for _, e := range evidence {
		for _, s := range e.Sources {
			add(s)
		}
	}
	for _, c := range chunks {
		add(models.LabSource{Title: c.Title, URL: c.URL, Section: c.Domain})
	}
	for _, s := range curriculumSourcesFor(q) {
		add(s)
	}
	return out
}

func labChunks(q models.Question, request string) []models.LabChunk {
	hits := RAGSearch(string(q.Cert), strings.Join([]string{
		request,
		string(q.Cert),
		q.Topic,
		q.Question,
		q.DocSection,
		q.DocURL,
		q.AnswerCommand,
		setupText(q),
	}, " "), 3, false)
	if len(hits) == 0 {
		return nil
	}
	out := make([]models.LabChunk, 0, len(hits))
	for _, h := range hits {
		out = append(out, labChunkFromHit(h))
	}
	return out
}

func labEvidence(q models.Question, request string) []models.LabEvidence {
	return evidenceForText(string(q.Cert), q.Topic, strings.Join([]string{
		request,
		string(q.Cert),
		q.Topic,
		q.Question,
		q.DocSection,
		q.DocURL,
		q.AnswerCommand,
		setupText(q),
	}, " "), q.DocURL, 3)
}

func evidenceForText(cert, topic, text, preferredURL string, max int) []models.LabEvidence {
	cur, ok := CurriculumFor(cert)
	if !ok {
		if preferredURL == "" {
			return nil
		}
		return []models.LabEvidence{{
			Domain:       fallbackEvidenceDomain(topic, cert),
			Confidence:   65,
			MatchedTerms: compactTerms([]string{topic, cert}, 4),
			Sources:      []models.LabSource{{Title: sourceTitle(preferredURL), URL: preferredURL, Section: topic}},
		}}
	}
	if max < 1 {
		max = 3
	}
	query := normalizeEvidenceText(topic + " " + text)
	var ranked []models.LabEvidence
	for _, d := range cur {
		conf, terms := scoreEvidenceDomain(d, query, preferredURL)
		if conf == 0 {
			continue
		}
		ev := models.LabEvidence{
			Domain:       d.Domain,
			Weight:       d.Weight,
			Confidence:   conf,
			MatchedTerms: terms,
			Sources:      evidenceSources(d, max),
		}
		ranked = append(ranked, ev)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Confidence == ranked[j].Confidence {
			return ranked[i].Weight > ranked[j].Weight
		}
		return ranked[i].Confidence > ranked[j].Confidence
	})
	if len(ranked) > max {
		ranked = ranked[:max]
	}
	return ranked
}

func scoreEvidenceDomain(d CurriculumDomain, query, preferredURL string) (int, []string) {
	domain := normalizeEvidenceText(d.Domain)
	score := 0
	var terms []string
	addTerm := func(v string) {
		v = strings.TrimSpace(strings.ToLower(v))
		if len(v) < 3 {
			return
		}
		for _, x := range terms {
			if x == v {
				return
			}
		}
		terms = append(terms, v)
	}
	if strings.Contains(query, domain) {
		score += 35
		addTerm(d.Domain)
	}
	if domainMatchesTopic(domain, query) {
		score += 45
		addTerm(d.Domain)
	}
	for _, term := range evidenceTerms(d.Domain) {
		if strings.Contains(query, term) {
			score += 10
			addTerm(term)
		}
	}
	for _, u := range d.URLs {
		if preferredURL != "" && strings.TrimRight(u, "/") == strings.TrimRight(preferredURL, "/") {
			score += 45
			addTerm("doc_url")
		}
		for _, term := range evidenceTerms(sourceTitle(u)) {
			if strings.Contains(query, term) {
				score += 8
				addTerm(term)
			}
		}
	}
	if score > 100 {
		score = 100
	}
	if score < 35 {
		return 0, nil
	}
	return score, compactTerms(terms, 8)
}

func evidenceTerms(s string) []string {
	s = normalizeEvidenceText(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r < 'a' || r > 'z'
	})
	stop := map[string]bool{
		"and": true, "the": true, "for": true, "with": true, "from": true, "into": true,
		"docs": true, "concepts": true, "tasks": true, "latest": true, "user": true,
	}
	var out []string
	for _, f := range fields {
		if len(f) < 3 || stop[f] {
			continue
		}
		out = append(out, f)
	}
	return compactTerms(out, 12)
}

func evidenceSources(d CurriculumDomain, max int) []models.LabSource {
	if max < 1 {
		max = 1
	}
	var out []models.LabSource
	for _, u := range d.URLs {
		out = append(out, models.LabSource{Title: d.Domain, URL: u, Section: d.Domain})
		if len(out) >= max {
			break
		}
	}
	return out
}

func labEvidenceScore(evidence []models.LabEvidence) int {
	best := 0
	for _, e := range evidence {
		if e.Confidence > best {
			best = e.Confidence
		}
	}
	return best
}

func fallbackEvidenceDomain(topic, cert string) string {
	switch {
	case strings.TrimSpace(topic) != "":
		return topic
	case strings.TrimSpace(cert) != "":
		return cert
	default:
		return "Lab customizado"
	}
}

func compactTerms(in []string, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(strings.ToLower(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out
}

func normalizeEvidenceText(s string) string {
	s = strings.ToLower(s)
	repl := strings.NewReplacer(
		"-", " ",
		"_", " ",
		"/", " ",
		".", " ",
		":", " ",
		"&", " ",
	)
	return strings.Join(strings.Fields(repl.Replace(s)), " ")
}

func EvidenceContext(cert, topic, text string, max int) string {
	evs := evidenceForText(cert, topic, text, "", max)
	if len(evs) == 0 {
		return ""
	}
	var lines []string
	for _, e := range evs {
		var urls []string
		for _, s := range e.Sources {
			if s.URL != "" {
				urls = append(urls, s.URL)
			}
		}
		lines = append(lines, fmt.Sprintf("- %s (peso %d, confianca %d): termos [%s]; fontes: %s",
			e.Domain, e.Weight, e.Confidence, strings.Join(e.MatchedTerms, ", "), strings.Join(urls, ", ")))
	}
	return strings.Join(lines, "\n")
}

func curriculumSourcesFor(q models.Question) []models.LabSource {
	cur, ok := CurriculumFor(string(q.Cert))
	if !ok {
		return nil
	}
	topic := strings.ToLower(q.Topic + " " + q.Question)
	var out []models.LabSource
	for _, d := range cur {
		domain := strings.ToLower(d.Domain)
		if strings.Contains(topic, domain) || domainMatchesTopic(domain, topic) {
			for _, u := range d.URLs {
				out = append(out, models.LabSource{Title: d.Domain, URL: u, Section: d.Domain})
				if len(out) >= 2 {
					return out
				}
			}
		}
	}
	return out
}

func domainMatchesTopic(domain, topic string) bool {
	pairs := map[string][]string{
		"workloads":       {"workload", "deployment", "hpa", "autoscal", "scheduling"},
		"services":        {"service", "dns", "network", "ingress"},
		"networking":      {"service", "dns", "network", "vpc"},
		"storage":         {"storage", "pvc", "s3"},
		"security":        {"security", "iam", "rbac", "policy"},
		"troubleshooting": {"troubleshoot", "incident", "debug"},
		"gitops":          {"gitops", "argocd"},
		"compute":         {"compute", "ec2", "eks"},
		"messaging":       {"sqs", "messag", "queue"},
	}
	for key, vals := range pairs {
		if strings.Contains(domain, key) {
			for _, v := range vals {
				if strings.Contains(topic, v) {
					return true
				}
			}
		}
	}
	return false
}

func labPlan(q models.Question) []string {
	var plan []string
	if len(q.Setup) > 0 {
		for _, s := range q.Setup {
			plan = append(plan, "Preparar: "+compactText(s.Description, 90))
		}
	} else {
		plan = append(plan, "Confirmar o cluster ativo e o contexto correto")
	}
	for _, g := range q.Goals {
		plan = append(plan, "Validar: "+compactText(g.Description, 100))
		if len(plan) >= 5 {
			break
		}
	}
	if q.AnswerCommand != "" && len(plan) < 6 {
		plan = append(plan, "Executar a solucao e comparar com os validadores")
	}
	if len(q.Teardown) > 0 {
		plan = append(plan, "Limpar recursos criados pelo lab")
	}
	return plan
}

func labSuccessCriteria(q models.Question) []string {
	var out []string
	for _, g := range q.Goals {
		if strings.TrimSpace(g.Description) != "" {
			out = append(out, compactText(g.Description, 120))
		}
	}
	if len(out) == 0 && q.Validation != nil {
		out = append(out, "Comando de validacao retorna o estado esperado")
	}
	return out
}

func labSafety(q models.Question) []string {
	out := []string{"Comandos limitados ao cluster ativo", "Teardown definido para remover recursos do exercicio"}
	if len(q.Teardown) == 0 {
		out[1] = "Sem teardown automatico detectado"
	}
	if strings.Contains(strings.ToLower(q.Topic), "aws") {
		out = append(out, "AWS emulada com LocalStack, sem credenciais reais")
	}
	return out
}

func scoreLab(q models.Question, spec models.LabSpec) models.LabQuality {
	score := 100
	var checks, warnings []string
	pass := func(s string) { checks = append(checks, s) }
	warn := func(points int, s string) {
		score -= points
		warnings = append(warnings, s)
	}
	if len(strings.TrimSpace(q.Question)) >= 30 {
		pass("enunciado contextual")
	} else {
		warn(15, "enunciado curto demais")
	}
	if len(q.Goals) > 0 {
		pass("goals definidos")
	} else {
		warn(25, "sem goals")
	}
	for i, g := range q.Goals {
		if g.Validation == nil || strings.TrimSpace(g.Validation.Command) == "" {
			warn(15, fmt.Sprintf("goal %d sem validacao automatica", i+1))
		}
	}
	if len(q.Teardown) > 0 {
		pass("teardown definido")
	} else {
		warn(15, "sem teardown")
	}
	if len(spec.Sources) > 0 {
		pass("fonte oficial associada")
	} else {
		warn(10, "sem fonte oficial")
	}
	if len(spec.Chunks) > 0 {
		pass("chunks RAG associados")
	} else {
		warn(5, "sem chunks RAG")
	}
	switch {
	case spec.EvidenceScore >= 75:
		pass("evidencia curricular forte")
	case spec.EvidenceScore >= 50:
		pass("evidencia curricular parcial")
		warn(5, "evidencia curricular abaixo do ideal")
	default:
		warn(20, "sem evidencia curricular forte")
	}
	if len(spec.Evidence) > 0 {
		pass("dominio da certificacao rastreado")
	}
	if len(spec.Dependencies) > 0 {
		pass("dependencias declaradas")
	}
	if q.AnswerCommand != "" {
		pass("solucao reproduzivel")
	} else {
		warn(5, "sem comando de solucao")
	}
	if len(q.Setup) > 0 {
		pass("setup automatizado")
	}
	if spec.Namespace != "" {
		pass("namespace de validacao definido")
	}
	if spec.ValidationMode == "compiled" {
		pass("validacao compilada")
	}
	if spec.LabPlan != nil {
		pass("plano do lab compilado")
		if len(spec.LabPlan.Risks) > 0 {
			warn(5, "plano com riscos: "+strings.Join(spec.LabPlan.Risks, "; "))
		}
	}
	if score < 0 {
		score = 0
	}
	return models.LabQuality{Score: score, Checks: checks, Warnings: warnings}
}

func setupText(q models.Question) string {
	var b strings.Builder
	for _, s := range q.Setup {
		b.WriteString(s.Description)
		b.WriteByte(' ')
		b.WriteString(s.Command)
		b.WriteByte(' ')
	}
	return b.String()
}

func sourceTitle(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "Documentacao oficial"
	}
	host := strings.TrimPrefix(u.Host, "www.")
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return host
	}
	parts := strings.Split(path, "/")
	last := strings.TrimSuffix(parts[len(parts)-1], ".html")
	last = strings.ReplaceAll(last, "-", " ")
	if last == "" {
		return host
	}
	return host + " / " + last
}

func compactText(s string, max int) string {
	s = strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(s, " "))
	r := []rune(s)
	if max > 0 && len(r) > max {
		return strings.TrimSpace(string(r[:max-1])) + "..."
	}
	return s
}
