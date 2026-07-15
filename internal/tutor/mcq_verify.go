package tutor

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"time"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Questões de COMANDO executavelmente verificáveis (Fatia 3) — o diferencial do
// produto. Uma questão só é "de fato" confiável quando dá para PROVAR que os
// distratores estão errados, não só afirmar. Aqui a questão nasce de um template
// de lab (cuja resposta e validador já são provados): o enunciado vira "qual
// comando cumpre o objetivo?", a alternativa correta é o AnswerCommand real e os
// distratores são mutações plausíveis. Quando a verificação executável está
// ligada, cada distrator é RODADO no cluster efêmero e precisa NÃO satisfazer o
// validador do efeito — senão a questão é rejeitada. Determinístico e offline
// para a geração; a prova executável é o gate extra em produção.
// ─────────────────────────────────────────────────────────────────────────────

// AuthorCommandMCQBatch gera questões de comando para cert/topic e, quando a
// verificação executável está habilitada, prova cada distrator errado.
func AuthorCommandMCQBatch(cert, topic string, count, level int, existing []models.Question) ([]models.Question, MCQReport, error) {
	cert = CanonicalCert(strings.TrimSpace(cert))
	if cert == "" {
		cert = "CKA"
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
	report := MCQReport{Requested: count, Cert: cert, Topic: topic, Grounded: true}
	dedup := newMCQDedup(existing, cert)
	verify := shouldVerifyGeneratedKubernetesLabs()

	var out []models.Question
	attempts := 0
	for len(out) < count && attempts < count*4 {
		attempts++
		q, ok := commandChoiceFromTemplate(cert, topic, level)
		if !ok {
			const msg = "algum template não rendeu comando imperativo de uma linha (pulado)"
			if len(report.Failures) == 0 || report.Failures[len(report.Failures)-1] != msg {
				report.Failures = append(report.Failures, msg)
			}
			continue
		}
		if dedup.isDuplicate(q) {
			report.Duplicates++
			continue
		}
		if verify {
			report.Verified = true
			err := verifyCommandChoiceMCQ(q)
			markMCQVerified(&q, err)
			if err != nil {
				report.Rejected++
				report.Failures = append(report.Failures, compactText(err.Error(), 140))
				continue
			}
		}
		dedup.remember(q)
		quizAccepted.Add(1)
		out = append(out, q)
	}

	if len(out) == 0 {
		return nil, report, fmt.Errorf("nenhuma questão de comando publicável (%d reprovada(s), %d duplicada(s))", report.Rejected, report.Duplicates)
	}
	if err := persist(out); err != nil {
		return nil, report, err
	}
	_ = RecordMCQCatalog(out)
	report.Ready = len(out)
	return out, report, nil
}

// commandChoiceFromTemplate deriva uma questão de comando de um template de lab:
// a alternativa correta é o AnswerCommand provado; distratores são mutações.
func commandChoiceFromTemplate(cert, topic string, level int) (models.Question, bool) {
	pick := topic
	if pick == "" || templates[pick] == nil {
		// Tópico aleatório entre os elegíveis: pedido sem tópico deve variar de
		// assunto (senão o batch inteiro sai do mesmo template).
		var eligible []string
		for _, t := range fallbackTopicsForCert(cert, "") {
			if templates[t] != nil {
				eligible = append(eligible, t)
			}
		}
		if len(eligible) == 0 {
			return models.Question{}, false
		}
		pick = eligible[rand.IntN(len(eligible))]
	}
	labs := generateQuestions(pick, cert, 2, 1)
	if len(labs) == 0 {
		return models.Question{}, false
	}
	lab := labs[0]
	cmd := strings.TrimSpace(lab.AnswerCommand)
	// Só comandos imperativos de uma linha viram questão de comando (heredoc /
	// apply -f - não têm distrator limpo).
	if !strings.HasPrefix(cmd, "kubectl ") || strings.Contains(cmd, "\n") || strings.Contains(cmd, "<<") || strings.Contains(cmd, "-f -") {
		return models.Question{}, false
	}
	goal := models.Goal{}
	for _, g := range lab.Goals {
		if g.Validation != nil && strings.TrimSpace(g.Validation.Command) != "" {
			goal = g
			break
		}
	}
	if goal.Validation == nil {
		return models.Question{}, false
	}
	distractors := mutateCommandDistractors(cmd)
	if len(distractors) < 3 {
		return models.Question{}, false
	}
	options := append([]string{cmd}, distractors[:3]...)
	opts, answer := shuffleOptions(options, 0)

	objective := stripMarkdownEmphasis(goal.Description)
	q := models.Question{
		ID:         newID(),
		Cert:       models.Cert(cert),
		Topic:      mcqTopicLabel(pick),
		Difficulty: diffFor(level),
		Type:       models.MultipleChoice,
		Source:     models.SourceGenerated,
		Question:   fmt.Sprintf("No cluster, qual comando cumpre o objetivo abaixo?\n\n**%s**", objective),
		Options:    opts,
		Answer:     answer,
		Explanation: fmt.Sprintf("O comando correto é `%s`. Os demais usam verbo, flag ou valor que não produzem o estado exigido pelo validador.", cmd) +
			"\n\n(questão de comando gerada e verificável no cluster)",
		DocURL: lab.DocURL,
		// Setup/Validation/Teardown NÃO são exibidos no quiz — servem à
		// verificação executável dos distratores.
		Setup:      lab.Setup,
		Validation: goal.Validation,
		Teardown:   lab.Teardown,
	}
	q.Readiness = groundedMCQReadiness(q, lab.DocURL)
	return q, true
}

// mutateCommandDistractors produz variantes plausíveis-porém-erradas de um
// comando kubectl: troca de flag por irmã do conjunto de confusão, troca de
// verbo, e troca de valor. Retorna variantes distintas entre si e do original.
func mutateCommandDistractors(cmd string) []string {
	seen := map[string]bool{strings.TrimSpace(cmd): true}
	var out []string
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		out = append(out, c)
	}

	// 1) troca de flag por irmã do mesmo conjunto de confusão.
	for _, set := range confusionSets {
		for _, term := range set {
			if !strings.HasPrefix(term, "--") {
				continue
			}
			if !strings.Contains(cmd, term) {
				continue
			}
			for _, sib := range set {
				if sib == term || !strings.HasPrefix(sib, "--") {
					continue
				}
				add(strings.Replace(cmd, term, sib, 1))
			}
		}
	}

	// 2) troca de verbo (muda o efeito → validador não confirma).
	fields := strings.Fields(cmd)
	if len(fields) >= 2 {
		verbSwaps := map[string][]string{
			"create": {"get", "delete"}, "run": {"get", "delete"}, "apply": {"get", "delete"},
			"expose": {"get", "delete"}, "scale": {"get", "describe"}, "label": {"annotate", "get"},
			"annotate": {"label", "get"}, "set": {"get"}, "delete": {"get", "describe"},
			"patch": {"get"}, "edit": {"get"}, "taint": {"get"},
		}
		if swaps, ok := verbSwaps[fields[1]]; ok {
			for _, s := range swaps {
				f := append([]string(nil), fields...)
				f[1] = s
				add(strings.Join(f, " "))
			}
		}
	}

	// 3) troca de valor numérico (ex.: --replicas=3 → --replicas=1).
	for _, f := range fields {
		if i := strings.Index(f, "="); i > 0 {
			key, val := f[:i], f[i+1:]
			if alt := altNumeric(val); alt != "" {
				add(strings.Replace(cmd, f, key+"="+alt, 1))
			}
		}
	}
	return out
}

func altNumeric(v string) string {
	switch v {
	case "0":
		return "1"
	case "1":
		return "2"
	case "2":
		return "1"
	case "3":
		return "2"
	case "4", "5":
		return "3"
	}
	return ""
}

func stripMarkdownEmphasis(s string) string {
	return strings.NewReplacer("**", "", "`", "", "__", "").Replace(strings.TrimSpace(s))
}

// verifyCommandChoiceMCQ prova, num cluster efêmero, que a alternativa correta
// satisfaz o validador do efeito e que NENHUM distrator o satisfaz. Só roda com
// K8S_LAB_VERIFY_GENERATED habilitado (produção com sandbox).
func verifyCommandChoiceMCQ(q models.Question) error {
	if q.Validation == nil || q.Answer < 0 || q.Answer >= len(q.Options) {
		return fmt.Errorf("questão de comando sem validador de efeito")
	}
	commands := append([]string{}, q.Options...)
	commands = append(commands, q.Teardown...)
	for _, s := range q.Setup {
		commands = append(commands, s.Command)
	}
	commands = append(commands, q.Validation.Command)
	for _, c := range commands {
		if reason := BlockedLabCommandReason(c); reason != "" {
			return fmt.Errorf("questão reprovada pelo guardrail: %s", reason)
		}
	}

	want := q.Validation.ExpectedContains
	if want == "" {
		want = q.Validation.ExpectedOutput
	}

	// A correta precisa produzir o efeito; setup limpo antes.
	if err := runOptionInSandbox(q, q.Options[q.Answer], want, true); err != nil {
		return fmt.Errorf("alternativa correta não passou: %w", err)
	}
	// Cada distrator NÃO pode produzir o efeito (senão a questão é ambígua).
	for i, opt := range q.Options {
		if i == q.Answer {
			continue
		}
		if err := runOptionInSandbox(q, opt, want, false); err != nil {
			return err
		}
	}
	return nil
}

// runOptionInSandbox cria um namespace efêmero, roda o setup, executa a opção e
// verifica se o validador confirma (mustPass) ou não (deve falhar).
func runOptionInSandbox(q models.Question, option, want string, mustPass bool) error {
	ns := "mcq-verify-" + SanitizeID(newID())
	if len(ns) > 63 {
		ns = ns[:63]
	}
	kubeconfig, err := os.CreateTemp("", "k8s-mcq-verify-*.yaml")
	if err != nil {
		return err
	}
	kubeconfigPath := kubeconfig.Name()
	_ = kubeconfig.Close()
	defer os.Remove(kubeconfigPath)
	if _, err := sh(fmt.Sprintf(`kubectl create namespace %s >/dev/null && kubectl config view --raw > %q && KUBECONFIG=%q kubectl config set-context --current --namespace=%s >/dev/null`, ns, kubeconfigPath, kubeconfigPath, ns), 30); err != nil {
		return fmt.Errorf("namespace efêmero não iniciou: %w", err)
	}
	base := fmt.Sprintf("export KUBECONFIG=%q; ", kubeconfigPath)
	defer func() {
		_, _ = sh(base+fmt.Sprintf(`kubectl delete namespace %s --ignore-not-found --wait=false >/dev/null 2>&1`, ns), 15)
	}()

	var setup []string
	for _, s := range q.Setup {
		setup = append(setup, s.Command)
	}
	if len(setup) > 0 {
		if _, err := sh(base+strings.Join(setup, "; "), 180); err != nil {
			return fmt.Errorf("setup falhou: %w", err)
		}
	}
	_, _ = sh(base+option, 120)

	confirmed := false
	for attempt := 0; attempt < 4 && !confirmed; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 3 * time.Second)
		}
		output, verr := sh(base+q.Validation.Command, 60)
		if verr == nil && (want == "" || strings.Contains(output, want)) {
			confirmed = true
		}
	}
	if mustPass && !confirmed {
		return fmt.Errorf("o validador não confirmou o efeito esperado")
	}
	if !mustPass && confirmed {
		return fmt.Errorf("distrator %q satisfez o validador (questão ambígua)", compactText(option, 60))
	}
	return nil
}
