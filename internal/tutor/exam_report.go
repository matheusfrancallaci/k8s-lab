package tutor

import (
	"sort"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// Relatório pós-simulado — traduz o resultado do Modo Exame para a métrica que
// o aluno entende: "eu passaria na prova real?". Pondera os acertos pelos PESOS
// oficiais dos domínios (CurriculumFor) e compara com a nota de corte da cert.
// Determinístico, zero LLM.
// ─────────────────────────────────────────────────────────────────────────────

// ExamAnswer é o resultado de uma questão do simulado, como o front registra.
type ExamAnswer struct {
	Topic string `json:"topic"`
	Cert  string `json:"cert,omitempty"`
	OK    bool   `json:"ok"`
}

// ExamDomainScore é o desempenho em um domínio oficial da certificação.
type ExamDomainScore struct {
	Domain   string `json:"domain"`
	Weight   int    `json:"weight"`
	Attempts int    `json:"attempts"`
	Correct  int    `json:"correct"`
	Pct      int    `json:"pct"`
}

// ExamReport é a projeção de aprovação do simulado.
type ExamReport struct {
	Cert   string `json:"cert"`
	Total  int    `json:"total"`
	OK     int    `json:"ok"`
	RawPct int    `json:"raw_pct"`
	// WeightedPct pondera cada domínio pelo peso oficial da prova — a projeção
	// honesta de "quanto você faria na prova real" com o que o simulado cobriu.
	WeightedPct int  `json:"weighted_pct"`
	PassCut     int  `json:"pass_cut"`
	Passed      bool `json:"passed"`
	// CoveredWeightPct: quanto do peso oficial da prova o simulado tocou. Baixo
	// = a projeção vale pouco (o aluno treinou só uma fatia da prova).
	CoveredWeightPct int               `json:"covered_weight_pct"`
	Domains          []ExamDomainScore `json:"domains"`
	Unseen           []ExamDomainScore `json:"unseen,omitempty"` // domínios que o simulado não cobriu
	Extra            []ExamDomainScore `json:"extra,omitempty"`  // tópicos fora do currículo oficial
}

// passCuts: nota de corte oficial (aproximada) por certificação. Fonte: Linux
// Foundation (CKA/CKAD 66, CKS 67), CNCF/Argo (CAPA 75), HashiCorp (70),
// AWS Associate (~72). Default conservador: 66 (o corte já usado na UI).
var passCuts = map[string]int{
	"CKA":       66,
	"CKAD":      66,
	"CKS":       67,
	"KCNA":      75,
	"CAPA":      75,
	"ARGOCD":    75,
	"TERRAFORM": 70,
	"AWS":       72,
}

func passCutFor(cert string) int {
	if cut, ok := passCuts[strings.ToUpper(strings.TrimSpace(cert))]; ok {
		return cut
	}
	return 66
}

// topicInDomain reutiliza EXATAMENTE o matching do DomainMap (fonte única de
// semântica tópico→domínio): contains bidirecional + pontes conhecidas.
func topicInDomain(domainNorm, topic string) bool {
	topicNorm := normalizeEvidenceText(topic)
	if topicNorm == "" || domainNorm == "" {
		return false
	}
	return strings.Contains(domainNorm, topicNorm) || strings.Contains(topicNorm, domainNorm) || domainMatchesTopic(domainNorm, topicNorm)
}

// BuildExamReport agrega os resultados do simulado por domínio oficial e
// projeta aprovação. Sem currículo embutido para a cert, a projeção degrada
// para o percentual bruto (e os tópicos viram "extra").
func BuildExamReport(cert string, answers []ExamAnswer) ExamReport {
	cert = CanonicalCert(strings.TrimSpace(cert))
	if cert == "" && len(answers) > 0 {
		cert = CanonicalCert(majorityCert(answers))
	}
	rep := ExamReport{Cert: cert, PassCut: passCutFor(cert)}
	for _, a := range answers {
		rep.Total++
		if a.OK {
			rep.OK++
		}
	}
	if rep.Total > 0 {
		rep.RawPct = rep.OK * 100 / rep.Total
	}

	cur, hasCur := CurriculumFor(cert)
	extra := map[string]*ExamDomainScore{}
	totalWeight, coveredWeight := 0, 0
	var weightedSum float64

	for _, d := range cur {
		totalWeight += d.Weight
		score := ExamDomainScore{Domain: d.Domain, Weight: d.Weight}
		domainNorm := normalizeEvidenceText(d.Domain)
		for _, a := range answers {
			if !topicInDomain(domainNorm, a.Topic) {
				continue
			}
			score.Attempts++
			if a.OK {
				score.Correct++
			}
		}
		if score.Attempts == 0 {
			rep.Unseen = append(rep.Unseen, score)
			continue
		}
		score.Pct = score.Correct * 100 / score.Attempts
		coveredWeight += d.Weight
		weightedSum += float64(d.Weight) * float64(score.Pct)
		rep.Domains = append(rep.Domains, score)
	}

	// Tópicos que não casaram com domínio nenhum (ou cert sem currículo):
	// aparecem como "extra" para o aluno não achar que a questão sumiu.
	for _, a := range answers {
		matched := false
		for _, d := range cur {
			if topicInDomain(normalizeEvidenceText(d.Domain), a.Topic) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(a.Topic))
		e := extra[key]
		if e == nil {
			e = &ExamDomainScore{Domain: a.Topic}
			extra[key] = e
		}
		e.Attempts++
		if a.OK {
			e.Correct++
		}
	}
	for _, e := range extra {
		e.Pct = e.Correct * 100 / e.Attempts
		rep.Extra = append(rep.Extra, *e)
	}
	sort.SliceStable(rep.Extra, func(i, j int) bool { return rep.Extra[i].Domain < rep.Extra[j].Domain })
	sort.SliceStable(rep.Domains, func(i, j int) bool { return rep.Domains[i].Weight > rep.Domains[j].Weight })

	if coveredWeight > 0 {
		rep.WeightedPct = int(weightedSum/float64(coveredWeight) + 0.5)
	} else {
		rep.WeightedPct = rep.RawPct // nada mapeado: projeção = bruto
	}
	if hasCur && totalWeight > 0 {
		rep.CoveredWeightPct = coveredWeight * 100 / totalWeight
	} else if rep.Total > 0 {
		rep.CoveredWeightPct = 100 // sem currículo, "cobertura" não se aplica
	}
	rep.Passed = rep.Total > 0 && rep.WeightedPct >= rep.PassCut
	return rep
}

func majorityCert(answers []ExamAnswer) string {
	counts := map[string]int{}
	best, bestN := "", 0
	for _, a := range answers {
		c := strings.TrimSpace(a.Cert)
		if c == "" {
			continue
		}
		counts[c]++
		if counts[c] > bestN {
			best, bestN = c, counts[c]
		}
	}
	return best
}
