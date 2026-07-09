package tutor

import (
	"strings"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Cobertura do catálogo vs currículo oficial — responde "o conteúdo CURADO
// cobre a prova?" com número, não com fé. O selo curado-vs-gerado diz quem
// responde por cada questão; isto diz se o núcleo confiável alcança todos os
// domínios oficiais (e seus pesos) da certificação.
// ─────────────────────────────────────────────────────────────────────────────

// DomainCoverage é a cobertura de um domínio oficial da certificação.
type DomainCoverage struct {
	Domain    string `json:"domain"`
	Weight    int    `json:"weight"`
	Curated   int    `json:"curated"`   // questões curadas que cobrem o domínio
	Generated int    `json:"generated"` // geradas (complemento, não substituto)
	Labs      int    `json:"labs"`      // das curadas, quantas são hands-on
}

// CoverageReport agrega a cobertura da certificação inteira.
type CoverageReport struct {
	Cert        string           `json:"cert"`
	Domains     []DomainCoverage `json:"domains"`
	CuratedPct  int              `json:"curated_pct"` // % do PESO oficial com >=1 item curado
	GapDomains  []string         `json:"gap_domains"` // domínios sem nenhum curado
	CuratedQs   int              `json:"curated_qs"`
	GeneratedQs int              `json:"generated_qs"`
}

// CurriculumCoverage cruza o banco de questões com o currículo oficial da cert.
// Pura (recebe o snapshot do repo) para ser testável e não acoplar o pacote ao
// repositório. Matching de domínio = o mesmo critério do DomainMap.
func CurriculumCoverage(cert string, qs []models.Question) (CoverageReport, bool) {
	cert = CanonicalCert(cert)
	cur, ok := CurriculumFor(cert)
	if !ok || len(cur) == 0 {
		return CoverageReport{Cert: cert}, false
	}
	rep := CoverageReport{Cert: cert}
	weightTotal, weightCovered := 0, 0
	for _, d := range cur {
		dc := DomainCoverage{Domain: d.Domain, Weight: d.Weight}
		domainNorm := normalizeEvidenceText(d.Domain)
		for _, q := range qs {
			if !strings.EqualFold(string(q.Cert), cert) {
				continue
			}
			topicNorm := normalizeEvidenceText(q.Topic)
			if topicNorm == "" || !(strings.Contains(domainNorm, topicNorm) ||
				strings.Contains(topicNorm, domainNorm) ||
				domainMatchesTopic(domainNorm, topicNorm)) {
				continue
			}
			if q.Source == models.SourceCurated {
				dc.Curated++
				if q.Type == models.Lab {
					dc.Labs++
				}
			} else {
				dc.Generated++
			}
		}
		weightTotal += d.Weight
		if dc.Curated > 0 {
			weightCovered += d.Weight
		} else {
			rep.GapDomains = append(rep.GapDomains, d.Domain)
		}
		rep.Domains = append(rep.Domains, dc)
	}
	for _, q := range qs {
		if !strings.EqualFold(string(q.Cert), cert) {
			continue
		}
		if q.Source == models.SourceCurated {
			rep.CuratedQs++
		} else {
			rep.GeneratedQs++
		}
	}
	if weightTotal > 0 {
		rep.CuratedPct = weightCovered * 100 / weightTotal
	}
	return rep, true
}
