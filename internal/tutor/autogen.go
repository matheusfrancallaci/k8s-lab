package tutor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Gerador AUTÔNOMO de labs (MVP: Terraform). O tutor:
//  1. garante a ferramenta (auto-instala da allowlist se faltar);
//  2. pede ao LLM local {tarefa, solução, validação};
//  3. AUTO-VERIFICA: roda a solução de verdade num workspace descartável e só
//     aceita o lab se a validação PASSA na solução e FALHA no vazio.
// Assim um lab gerado por IA fica realmente corrigível — sem isso, um modelo
// pequeno inventa validações que não batem. Tudo local, custo zero.
// ─────────────────────────────────────────────────────────────────────────────

// ─── Auto-provisionamento de ferramentas (allowlist — nada de comando do LLM) ─
type toolInstall struct {
	bin     string   // binário a detectar
	install []string // passos shell (rodados só se faltar); vazio = já vem na imagem
}

var toolRegistry = map[string]toolInstall{
	"terraform": {bin: "terraform"}, // já baked no Dockerfile
	// futuros (allowlist fixa, nunca vindo do LLM):
	// "ansible": {bin: "ansible", install: []string{"pip3 install --quiet ansible-core"}},
}

// EnsureTool garante que a ferramenta existe, instalando da allowlist se faltar.
func EnsureTool(name string) error {
	t, ok := toolRegistry[name]
	if !ok {
		return fmt.Errorf("ferramenta '%s' fora da allowlist", name)
	}
	if hasBin(t.bin) {
		return nil
	}
	for _, step := range t.install {
		if _, err := sh(step, 300); err != nil {
			return fmt.Errorf("instalação de %s falhou: %w", name, err)
		}
	}
	if !hasBin(t.bin) {
		return fmt.Errorf("%s ainda indisponível após instalar", name)
	}
	return nil
}

func hasBin(bin string) bool {
	return exec.Command("sh", "-c", "command -v "+bin).Run() == nil
}

// sh roda um comando shell (linux/container) com timeout, devolvendo a saída.
func sh(cmd string, timeoutSec int) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ─── Especificação de um lab gerado pelo LLM ─────────────────────────────────
type tfSpec struct {
	Question    string `json:"question"`
	SolutionHCL string `json:"solution_hcl"`
	Validation  string `json:"validation"` // usa $TFDIR; imprime Expected no sucesso
	Expected    string `json:"expected"`
	Hint        string `json:"hint"`
	Explanation string `json:"explanation"`
}

// ─── Segurança: HCL e validação do LLM SERÃO executados ──────────────────────
var (
	// tokens proibidos no HCL (exec arbitrário, providers de nuvem, exfiltração)
	hclDenyRe = regexp.MustCompile(`(?i)provisioner|local-exec|remote-exec|data\s+"external"|data\s+"http"|aws_|azurerm_|azuread_|google_|kubernetes_|helm_|vault_|filename\s*=\s*"?/|\.\.`)
	// providers permitidos no HCL
	hclAllowSrcRe = regexp.MustCompile(`(?i)source\s*=\s*"hashicorp/(local|random|null|time|tls)"`)
	// COMANDOS perigosos na validação (casam em qualquer lugar da string — então
	// mesmo dentro de $() ou `` os perigosos são pegos). Note: 2>/dev/null é OK
	// (só bloqueamos escrita em DISPOSITIVOS de bloco). $( e ` são permitidos
	// porque os comandos perigosos internos continuam sendo barrados aqui.
	valDenyRe = regexp.MustCompile(`(?i)\brm\b|\brmdir\b|\bmv\b|\bdd\b|mkfs|:\(\)|\bsudo\b|\bcurl\b|\bwget\b|\bchmod\b|\bchown\b|\bapt\b|\bpip[0-9]?\b|\bnpm\b|\bnc\b|\beval\b|\bkill\b|shutdown|reboot|>\s*/dev/(sd|hd|nvme|vd|mem)|>\s*/(etc|bin|usr|boot|root|home|var)/`)
)

func safeHCL(h string) bool {
	if strings.TrimSpace(h) == "" || hclDenyRe.MatchString(h) {
		return false
	}
	// se declara required_providers, todos os sources precisam ser da allowlist
	if strings.Contains(strings.ToLower(h), "source") {
		for _, m := range regexp.MustCompile(`(?i)source\s*=\s*"[^"]+"`).FindAllString(h, -1) {
			if !hclAllowSrcRe.MatchString(m) {
				return false
			}
		}
	}
	return true
}

func safeValidation(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 600 || valDenyRe.MatchString(v) {
		return false
	}
	// só ASCII imprimível (+ tab/newline) — sem bytes de controle/binário
	for _, r := range v {
		if r != '\n' && r != '\t' && (r < 0x20 || r > 0x7e) {
			return false
		}
	}
	// tem que operar sobre o workspace do terraform
	return strings.Contains(v, "$TFDIR") || strings.Contains(v, "terraform")
}

// GenerateVerifiedTFLab gera um lab de Terraform e SÓ o devolve se a solução de
// referência realmente passa na validação (auto-verificação). tries controla o
// nº de tentativas do LLM.
func GenerateVerifiedTFLab(topic string, level, tries int) (models.Question, error) {
	if ok, _ := LLMStatus(); !ok {
		return models.Question{}, fmt.Errorf("IA local (Ollama) indisponível")
	}
	if err := EnsureTool("terraform"); err != nil {
		return models.Question{}, err
	}
	if tries < 1 {
		tries = 1
	}
	var lastErr error
	for i := 0; i < tries; i++ {
		spec, err := llmTFSpec(topic, level)
		if err != nil {
			lastErr = err
			continue
		}
		if !safeHCL(spec.SolutionHCL) {
			lastErr = fmt.Errorf("HCL gerado reprovado na segurança")
			continue
		}
		if !safeValidation(spec.Validation) || spec.Expected == "" {
			lastErr = fmt.Errorf("validação gerada reprovada na segurança")
			continue
		}
		if err := verifyTFLab(spec); err != nil {
			lastErr = err
			continue
		}
		return tfSpecToQuestion(spec, topic, level), nil
	}
	return models.Question{}, fmt.Errorf("não consegui gerar um lab verificável: %v", lastErr)
}

// llmTFSpec pede ao modelo local um lab de Terraform em JSON estrito.
func llmTFSpec(topic string, level int) (tfSpec, error) {
	if topic == "" {
		topic = "fundamentos (recursos, variáveis, outputs)"
	}
	prompt := fmt.Sprintf(`Você gera UM laboratório prático de Terraform em português do Brasil.

REGRAS ESTRITAS:
- Use SOMENTE providers hashicorp/local, hashicorp/random ou hashicorp/null. NUNCA aws/azurerm/google (o ambiente NÃO tem credenciais de nuvem).
- PROIBIDO: provisioner, local-exec, remote-exec, data "external", caminhos absolutos ("/..."), "..".
- "solution_hcl": HCL COMPLETO e correto que resolve a tarefa (o gabarito), com o bloco required_providers.
- "validation": UM comando shell de LEITURA que confirma o sucesso usando terraform -chdir="$TFDIR" e/ou testando arquivos em "$TFDIR". Imprime exatamente "OK" no sucesso e "FAIL" senão.
- "expected": "OK".
- Tópico do lab: %s. Nível de ajuda: %d (1=desafio sem dicas, 3=passo a passo).

Responda SOMENTE com JSON válido neste formato (sem texto fora do JSON):
{"question":"enunciado claro do que fazer","solution_hcl":"...","validation":"terraform -chdir=\"$TFDIR\" state list 2>/dev/null | grep -q TIPO.NOME && echo OK || echo FAIL","expected":"OK","hint":"dica curta","explanation":"explicação + gabarito comentado"}

Exemplo:
{"question":"Crie um recurso local_file chamado nota que grava 'oi' em nota.txt. Rode terraform init e apply.","solution_hcl":"terraform {\n  required_providers {\n    local = { source = \"hashicorp/local\" }\n  }\n}\nresource \"local_file\" \"nota\" {\n  filename = \"nota.txt\"\n  content  = \"oi\"\n}","validation":"terraform -chdir=\"$TFDIR\" state list 2>/dev/null | grep -q local_file.nota && test -f \"$TFDIR/nota.txt\" && echo OK || echo FAIL","expected":"OK","hint":"resource \"local_file\" \"nota\" { filename content }","explanation":"local_file grava um arquivo local; init baixa o provider e apply cria o recurso."}`, topic, level)

	raw, err := llmGenerate(prompt, true, 120*time.Second, tokensGen)
	if err != nil {
		return tfSpec{}, err
	}
	var s tfSpec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return tfSpec{}, fmt.Errorf("JSON inválido do modelo: %w", err)
	}
	if strings.TrimSpace(s.Question) == "" || strings.TrimSpace(s.SolutionHCL) == "" {
		return tfSpec{}, fmt.Errorf("resposta do modelo incompleta")
	}
	if s.Expected == "" {
		s.Expected = "OK"
	}
	return s, nil
}

// verifyTFLab roda a solução de referência num workspace descartável e confirma
// que a validação PASSA na solução e FALHA no vazio (senão é fraca demais).
func verifyTFLab(s tfSpec) error {
	dir, err := os.MkdirTemp("", "tfverify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(s.SolutionHCL), 0o644); err != nil {
		return err
	}
	// cache de providers compartilhado → init repetido fica rápido
	cache := filepath.Join(os.TempDir(), "tf-plugin-cache")
	os.MkdirAll(cache, 0o755) //nolint:errcheck

	base := fmt.Sprintf(`export TF_PLUGIN_CACHE_DIR=%q TFDIR=%q; `, cache, dir)
	if out, err := sh(base+fmt.Sprintf(`terraform -chdir=%q init -no-color -input=false && terraform -chdir=%q apply -auto-approve -no-color -input=false`, dir, dir), 240); err != nil {
		return fmt.Errorf("a solução gerada não aplicou: %s", trunc(out, 200))
	}
	// 1) validação passa na solução?
	out, _ := sh(base+s.Validation, 60)
	if !strings.Contains(out, s.Expected) {
		return fmt.Errorf("a validação não passou na própria solução")
	}
	// 2) validação falha no vazio? (senão é fraca/sempre-verde)
	empty, _ := os.MkdirTemp("", "tfempty-")
	defer os.RemoveAll(empty)
	oute, _ := sh(fmt.Sprintf(`export TFDIR=%q; `, empty)+s.Validation, 30)
	if strings.Contains(oute, s.Expected) {
		return fmt.Errorf("a validação passa mesmo sem solução (fraca)")
	}
	return nil
}

// tfSpecToQuestion converte a spec verificada num lab do repositório. O workspace
// do usuário é $HOME/tflab/$LAB_USER/<id>, criado no setup e usado na validação.
func tfSpecToQuestion(s tfSpec, topic string, level int) models.Question {
	id := "tfgen-" + newID()
	work := `$HOME/tflab/$LAB_USER/` + id
	setDir := `export TFDIR="` + work + `"; `
	diff := models.Mid
	if level >= 3 {
		diff = models.Easy
	} else if level <= 1 {
		diff = models.Hard
	}
	if topic == "" {
		topic = "Gerado pela IA"
	}
	return models.Question{
		ID:         id,
		Cert:       models.Cert("Terraform"),
		Topic:      topic,
		Difficulty: diff,
		Type:       models.Lab,
		Question:   strings.TrimSpace(s.Question) + "\n\nNo terminal do lab: **cd $TFLAB/" + id + "**\n\n_(lab gerado e auto-verificado pela IA local)_",
		Hint:       strings.TrimSpace(s.Hint),
		Setup: []models.SetupStep{{
			Description: "Preparando o workspace isolado...",
			Command:     "mkdir -p " + work,
		}},
		Goals: []models.Goal{{
			Description: "A tarefa foi concluída (validação automática)",
			Hint:        strings.TrimSpace(s.Hint),
			Validation: &models.Validation{
				Command:          setDir + s.Validation,
				ExpectedContains: s.Expected,
			},
		}},
		Explanation: strings.TrimSpace(s.Explanation) + "\n\n--- Gabarito (main.tf) ---\n" + strings.TrimSpace(s.SolutionHCL),
		Teardown:    []string{`terraform -chdir="` + work + `" destroy -auto-approve 2>/dev/null; rm -rf "` + work + `"`},
		DocURL:      "https://developer.hashicorp.com/terraform/language",
	}
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
