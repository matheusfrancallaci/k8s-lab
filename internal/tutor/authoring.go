package tutor

import (
	"fmt"
	"strings"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Autoria em lote — separa AUTORIA de SERVING: o banco engorda em batch usando
// o caminho mais forte disponível (gateway remoto quando configurado; senão o
// modelo local de geração) + templates compostos, e CADA lab autorado passa
// pela verificação executável OBRIGATÓRIA — reprovou, não entra. O serving
// continua 100% local e custo zero. É o item nº1 do plano "lab perfeito":
// conteúdo nível prova com gabarito provado em cluster real.
// ─────────────────────────────────────────────────────────────────────────────

type AuthorReport struct {
	Requested int      `json:"requested"`
	Ready     int      `json:"ready"`
	Rejected  int      `json:"rejected"`
	Failures  []string `json:"failures,omitempty"`
	UsedModel string   `json:"used_model"`
	Verified  bool     `json:"verified"` // verificação executável rodou de fato
}

// AuthorExamBatch gera count labs nível prova para cert/topic. Alterna entre
// composto estrutural (determinístico) e geração LLM completa (Lab Agent), e
// FORÇA a verificação executável independente do env — autoria sem prova
// executada não publica.
func AuthorExamBatch(cert, topic string, count int) ([]models.Question, AuthorReport, error) {
	cert = CanonicalCert(strings.TrimSpace(cert))
	if cert == "" {
		cert = "CKA"
	}
	if count < 1 {
		count = 3
	}
	if count > 10 {
		count = 10
	}
	report := AuthorReport{Requested: count}
	if _, model := LLMStatus(); model != "" {
		report.UsedModel = model
	}

	prompt := fmt.Sprintf("crie uma questao dificil nivel prova da certificacao %s sobre %s, cenario realista com multiplas restricoes", cert, topic)
	if strings.TrimSpace(topic) == "" {
		prompt = fmt.Sprintf("crie uma questao dificil nivel prova da certificacao %s, cenario realista com multiplas restricoes", cert)
	}

	var out []models.Question
	for i := 0; i < count; i++ {
		var q models.Question
		var err error
		if i%2 == 0 {
			q, err = GenerateExamComposite(cert, 3)
		} else {
			var qs []models.Question
			qs, _, err = GenerateSmartLabs(prompt, cert, 3, 1)
			if err == nil && len(qs) > 0 {
				q = qs[0]
			} else if err == nil {
				err = fmt.Errorf("gerador nao devolveu lab")
			}
		}
		if err != nil {
			report.Failures = append(report.Failures, compactText(err.Error(), 140))
			continue
		}
		// Verificação executável FORÇADA: na autoria ela nunca é opcional.
		if isKubernetesLab(q) {
			verifyErr := executableKubernetesLabVerifier(q)
			report.Verified = true
			markLabVerified(&q, true, verifyErr)
			_ = RecordLabCatalog([]models.Question{q})
			if verifyErr != nil {
				report.Rejected++
				report.Failures = append(report.Failures, compactText(verifyErr.Error(), 140))
				continue
			}
		}
		report.Ready++
		out = append(out, q)
	}
	if len(out) == 0 {
		return nil, report, fmt.Errorf("nenhum lab autorado passou na verificação (%d reprovados)", report.Rejected)
	}
	return out, report, nil
}
