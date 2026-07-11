package tutor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Currículos APRENDIDOS — o caminho genérico para certificação nova.
//
// O embutido (certCurricula) é o núcleo curado das certs principais; ele não
// escala para "qualquer certificação". Quando o aluno traz a página oficial de
// um exame que o app não conhece, o tutor extrai os domínios e pesos via LLM
// (contrato JSON + validação + grounding anti-alucinação), PERSISTE em
// data/curricula.json e a cert vira cidadã de primeira classe: trilha,
// cobertura, simulado ponderado — sem ninguém editar código.
// ─────────────────────────────────────────────────────────────────────────────

var (
	learnedCurMu     sync.Mutex
	learnedCurLoaded bool
	learnedCur       map[string][]CurriculumDomain
)

func learnedCurriculaPath() string {
	if p := strings.TrimSpace(os.Getenv("LEARNED_CURRICULA_PATH")); p != "" {
		return p
	}
	return filepath.Join("data", "curricula.json")
}

func ensureLearnedLocked() map[string][]CurriculumDomain {
	if learnedCurLoaded && learnedCur != nil {
		return learnedCur
	}
	learnedCurLoaded = true
	learnedCur = map[string][]CurriculumDomain{}
	if b, err := os.ReadFile(learnedCurriculaPath()); err == nil {
		_ = json.Unmarshal(b, &learnedCur)
	}
	return learnedCur
}

func saveLearnedLocked(st map[string][]CurriculumDomain) {
	path := learnedCurriculaPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if b, err := json.MarshalIndent(st, "", "  "); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

func learnedCurriculumFor(cert string) ([]CurriculumDomain, bool) {
	learnedCurMu.Lock()
	defer learnedCurMu.Unlock()
	st := ensureLearnedLocked()
	for k, v := range st {
		if strings.EqualFold(k, cert) && len(v) > 0 {
			return v, true
		}
	}
	return nil, false
}

// LearnCurriculumFromMaterial extrai domínios/pesos oficiais do material e
// persiste. Best-effort e conservador: sem LLM, material curto, cert já
// conhecida ou domínios não ancorados no texto → não aprende (e não inventa).
func LearnCurriculumFromMaterial(cert, text string, sources []string) ([]CurriculumDomain, bool) {
	cert = CanonicalCert(strings.TrimSpace(cert))
	if cert == "" || len(text) < 200 {
		return nil, false
	}
	if _, known := CurriculumFor(cert); known {
		return nil, false
	}
	if ok, _ := LLMStatus(); !ok {
		return nil, false
	}
	sample := text
	if len(sample) > 7000 {
		sample = sample[:7000]
	}
	prompt := fmt.Sprintf(`Você analisa a página oficial de uma certificação de TI (%s).
Extraia EXCLUSIVAMENTE do material os domínios oficiais do exame e o peso percentual de cada um.

Regras:
- "domains": 2 a 8 itens; "domain" é o nome oficial do domínio, "weight" é inteiro 0-100.
- Os pesos devem somar aproximadamente 100. Se o material não traz pesos, use 0 em todos.
- NÃO invente domínios que não estejam no material. Se o material não é um guia de exame, retorne {"domains":[]}.

MATERIAL:
%s`, cert, sample)

	raw, err := llmGenerateContract(prompt, "curriculum", 90*time.Second, tokensGen, genModel())
	if err != nil {
		return nil, false
	}
	var parsed struct {
		Domains []struct {
			Domain string `json:"domain"`
			Weight int    `json:"weight"`
		} `json:"domains"`
	}
	if json.Unmarshal([]byte(raw), &parsed) != nil {
		return nil, false
	}

	seedURLs := limitedStrings(sources, 2)
	low := strings.ToLower(text)
	var out []CurriculumDomain
	sum := 0
	for _, d := range parsed.Domains {
		name := compactText(d.Domain, 80)
		if name == "" || d.Weight < 0 || d.Weight > 100 {
			continue
		}
		// Grounding anti-alucinação: o domínio precisa estar ancorado no
		// material — mesmo princípio do quiz (prompt não é contrato).
		toks := contentTokens(name)
		hits := 0
		for _, t := range toks {
			if strings.Contains(low, t) {
				hits++
			}
		}
		if len(toks) > 0 && hits*100 < len(toks)*50 {
			continue
		}
		out = append(out, CurriculumDomain{Domain: name, Weight: d.Weight, URLs: seedURLs})
		sum += d.Weight
	}
	if len(out) < 2 || len(out) > 8 {
		return nil, false
	}
	// Pesos ausentes ou implausíveis: distribui igualmente — o currículo ainda
	// vale para trilha/cobertura; o peso refina depois com a fonte certa.
	if sum < 60 || sum > 130 {
		eq := 100 / len(out)
		for i := range out {
			out[i].Weight = eq
		}
		out[0].Weight += 100 - eq*len(out)
	}

	learnedCurMu.Lock()
	defer learnedCurMu.Unlock()
	st := ensureLearnedLocked()
	st[cert] = out
	saveLearnedLocked(st)
	return out, true
}

func resetLearnedCurriculaForTest() {
	learnedCurMu.Lock()
	defer learnedCurMu.Unlock()
	learnedCurLoaded = true
	learnedCur = map[string][]CurriculumDomain{}
}
