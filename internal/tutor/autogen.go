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
// Gerador AUTÔNOMO de labs — framework de FAMÍLIAS (Terraform, Ansible, ...).
// Para cada família o tutor:
//  1. garante a ferramenta (auto-instala da allowlist se faltar);
//  2. pede ao LLM (modelo de CÓDIGO) {tarefa, solução, validação};
//  3. AUTO-VERIFICA: escreve a solução num workspace descartável, APLICA de
//     verdade e confirma que a validação PASSA na solução e FALHA no vazio.
// Só entrega labs que corrigem certo. Tudo local, custo zero.
// ─────────────────────────────────────────────────────────────────────────────

// labFamily descreve como gerar+verificar labs de uma ferramenta file-based
// (a solução é um arquivo no workspace $TFDIR, aplicado por applyTmpl).
type labFamily struct {
	name       string            // "terraform", "ansible"
	cert       string            // cert associada no board
	tool       string            // binário p/ EnsureTool
	solFile    string            // arquivo da solução (main.tf, playbook.yml)
	applyTmpl  string            // comandos p/ aplicar a solução (usa $TFDIR)
	idPrefix   string            // prefixo do id gerado
	docURL     string            // doc de referência
	promptTail string            // regras+few-shot específicos da família
	safeCode   func(string) bool // segurança do código gerado (é executado!)
	runHint    string            // como o usuário aplica no terminal (vai no enunciado)
}

func labFamilies() map[string]*labFamily {
	return map[string]*labFamily{
		"terraform": {
			name: "terraform", cert: "Terraform", tool: "terraform", solFile: "main.tf",
			applyTmpl: `export TF_PLUGIN_CACHE_DIR="$HOME/.tf-plugin-cache"; mkdir -p "$TF_PLUGIN_CACHE_DIR"; terraform -chdir="$TFDIR" init -no-color -input=false && terraform -chdir="$TFDIR" apply -auto-approve -no-color -input=false`,
			idPrefix:  "tfgen", docURL: "https://developer.hashicorp.com/terraform/language",
			runHint:  "edite **main.tf** e rode **terraform init && terraform apply**",
			safeCode: safeHCL,
			promptTail: `- Use SOMENTE providers hashicorp/local, hashicorp/random ou hashicorp/null. NUNCA aws/azurerm/google (sem credenciais de nuvem).
- PROIBIDO: provisioner, local-exec, remote-exec, data "external", caminhos absolutos, "..".
- "solution" é o main.tf COMPLETO (com required_providers).
Exemplo:
{"question":"Crie um local_file 'nota' com conteudo 'oi' em nota.txt.","solution":"terraform {\n  required_providers {\n    local = { source = \"hashicorp/local\" }\n  }\n}\nresource \"local_file\" \"nota\" {\n  filename = \"nota.txt\"\n  content  = \"oi\"\n}","validation":"terraform -chdir=\"$TFDIR\" state list 2>/dev/null | grep -q local_file.nota && test -f \"$TFDIR/nota.txt\" && echo OK || echo FAIL","expected":"OK","hint":"resource local_file { filename content }","explanation":"local_file grava um arquivo; init baixa o provider, apply cria."}`,
		},
		"ansible": {
			name: "ansible", cert: "Ansible", tool: "ansible-playbook", solFile: "playbook.yml",
			applyTmpl: `cd "$TFDIR" && ansible-playbook -i localhost, -c local playbook.yml 2>&1`,
			idPrefix:  "ansgen", docURL: "https://docs.ansible.com/ansible/latest/collections/ansible/builtin/",
			runHint:  "edite **playbook.yml** e rode **ansible-playbook -i localhost, -c local playbook.yml**",
			safeCode: safeAnsible,
			promptTail: `- O playbook roda com connection local em localhost. Use SOMENTE módulos SEGUROS: copy, file, template, lineinfile, blockinfile, set_fact, debug, assert, stat. Escreva SEMPRE em arquivos DENTRO de "{{ lookup('env','TFDIR') }}" ou caminhos relativos ao workspace.
- PROIBIDO: shell, command, raw, script, become, package/apt/yum, service, user, systemd, get_url, uri, caminhos absolutos.
- "solution" é o playbook.yml COMPLETO (uma play com hosts: localhost).
Exemplo:
{"question":"Escreva um playbook que cria o arquivo out.txt (no workspace) com o conteudo 'ola ansible' usando o modulo copy.","solution":"- hosts: localhost\n  connection: local\n  gather_facts: false\n  tasks:\n    - name: cria out.txt\n      copy:\n        dest: \"{{ lookup('env','TFDIR') }}/out.txt\"\n        content: \"ola ansible\\n\"","validation":"grep -q 'ola ansible' \"$TFDIR/out.txt\" 2>/dev/null && echo OK || echo FAIL","expected":"OK","hint":"module copy: dest + content","explanation":"o modulo copy grava conteudo literal num arquivo; connection local roda na propria maquina."}`,
		},
	}
}

// familyForMessage escolhe a família a partir do texto/cert (ex.: "ansible" na
// mensagem ou cert "Ansible" → família ansible; default terraform).
func familyForMessage(msg, cert string) *labFamily {
	fams := labFamilies()
	l := strings.ToLower(msg + " " + cert)
	for _, f := range fams {
		if strings.Contains(l, f.name) || strings.EqualFold(cert, f.cert) {
			return f
		}
	}
	if strings.Contains(l, "playbook") {
		return fams["ansible"]
	}
	return fams["terraform"]
}

// ─── Auto-provisionamento de ferramentas (allowlist — nada de comando do LLM) ─
type toolInstall struct {
	bin     string
	install []string
}

var toolRegistry = map[string]toolInstall{
	"terraform":        {bin: "terraform"},        // baked no Dockerfile
	"ansible-playbook": {bin: "ansible-playbook"}, // baked no Dockerfile
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

// labSpec é a especificação de um lab gerada pelo LLM (genérica p/ qualquer família).
type labSpec struct {
	Question    string `json:"question"`
	Solution    string `json:"solution"` // conteúdo do solFile da família
	Validation  string `json:"validation"`
	Expected    string `json:"expected"`
	Hint        string `json:"hint"`
	Explanation string `json:"explanation"`
}

// ─── Segurança do código/validação gerados (SERÃO executados) ────────────────
var (
	hclDenyRe     = regexp.MustCompile(`(?i)provisioner|local-exec|remote-exec|data\s+"external"|data\s+"http"|aws_|azurerm_|azuread_|google_|kubernetes_|helm_|vault_|filename\s*=\s*"?/|\.\.`)
	hclAllowSrcRe = regexp.MustCompile(`(?i)source\s*=\s*"hashicorp/(local|random|null|time|tls)"`)
	ansibleDenyRe = regexp.MustCompile(`(?i)\b(shell|command|raw|script|become|apt|yum|dnf|package|service|systemd|user|get_url|uri|expect|copy_url):|dest\s*:\s*["']?/|path\s*:\s*["']?/|hosts\s*:\s*all`)
	valDenyRe     = regexp.MustCompile(`(?i)\brm\b|\brmdir\b|\bmv\b|\bdd\b|mkfs|:\(\)|\bsudo\b|\bcurl\b|\bwget\b|\bchmod\b|\bchown\b|\bapt\b|\bpip[0-9]?\b|\bnpm\b|\bnc\b|\beval\b|\bkill\b|shutdown|reboot|>\s*/dev/(sd|hd|nvme|vd|mem)|>\s*/(etc|bin|usr|boot|root|home|var)/`)
)

func safeHCL(h string) bool {
	if strings.TrimSpace(h) == "" || hclDenyRe.MatchString(h) {
		return false
	}
	if strings.Contains(strings.ToLower(h), "source") {
		for _, m := range regexp.MustCompile(`(?i)source\s*=\s*"[^"]+"`).FindAllString(h, -1) {
			if !hclAllowSrcRe.MatchString(m) {
				return false
			}
		}
	}
	return true
}

func safeAnsible(p string) bool {
	if strings.TrimSpace(p) == "" || ansibleDenyRe.MatchString(p) {
		return false
	}
	// tem que ser uma play local (não sair pra máquinas reais)
	low := strings.ToLower(p)
	return strings.Contains(low, "localhost") || strings.Contains(low, "connection: local")
}

func safeValidation(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 600 || valDenyRe.MatchString(v) {
		return false
	}
	for _, r := range v {
		if r != '\n' && r != '\t' && (r < 0x20 || r > 0x7e) {
			return false
		}
	}
	return strings.Contains(v, "$TFDIR") || strings.Contains(v, "terraform") || strings.Contains(v, "TFDIR")
}

// GenerateVerifiedLab gera+verifica um lab da família dada.
func GenerateVerifiedLab(fam *labFamily, topic string, level, tries int) (models.Question, error) {
	if ok, _ := LLMStatus(); !ok {
		return models.Question{}, fmt.Errorf("IA local (Ollama) indisponível")
	}
	if err := EnsureTool(fam.tool); err != nil {
		return models.Question{}, err
	}
	if tries < 1 {
		tries = 1
	}
	var lastErr error
	for i := 0; i < tries; i++ {
		spec, err := llmLabSpec(fam, topic, level)
		if err != nil {
			lastErr = err
			continue
		}
		if !fam.safeCode(spec.Solution) {
			lastErr = fmt.Errorf("código gerado reprovado na segurança")
			continue
		}
		if !safeValidation(spec.Validation) || spec.Expected == "" {
			lastErr = fmt.Errorf("validação gerada reprovada na segurança")
			continue
		}
		if err := verifyLab(fam, spec); err != nil {
			lastErr = err
			continue
		}
		return specToQuestion(fam, spec, topic, level), nil
	}
	return models.Question{}, fmt.Errorf("não consegui gerar um lab verificável: %v", lastErr)
}

// GenerateVerifiedTFLab mantém a assinatura antiga (Terraform) usada pelo chat.
func GenerateVerifiedTFLab(topic string, level, tries int) (models.Question, error) {
	return GenerateVerifiedLab(labFamilies()["terraform"], topic, level, tries)
}

func llmLabSpec(fam *labFamily, topic string, level int) (labSpec, error) {
	if topic == "" {
		topic = "fundamentos"
	}
	prompt := fmt.Sprintf(`Você gera UM laboratório prático de %s em português do Brasil. Tópico: %s. Nível de ajuda: %d (1=desafio, 3=passo a passo).

REGRAS ESTRITAS:
%s
- "validation": UM comando shell de LEITURA que confirma o sucesso usando "$TFDIR" (o diretório do workspace). Imprime "OK" no sucesso e "FAIL" senão.
- "expected": "OK".

Responda SOMENTE JSON válido: {"question":"...","solution":"...","validation":"...","expected":"OK","hint":"...","explanation":"..."}
`, fam.name, topic, level, fam.promptTail)

	raw, err := llmGenerate(prompt, true, 120*time.Second, tokensGen, genModel())
	if err != nil {
		return labSpec{}, err
	}
	var s labSpec
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return labSpec{}, fmt.Errorf("JSON inválido do modelo: %w", err)
	}
	if strings.TrimSpace(s.Question) == "" || strings.TrimSpace(s.Solution) == "" {
		return labSpec{}, fmt.Errorf("resposta do modelo incompleta")
	}
	if s.Expected == "" {
		s.Expected = "OK"
	}
	return s, nil
}

// verifyLab escreve a solução, APLICA e confere que a validação passa na solução
// e falha no vazio.
func verifyLab(fam *labFamily, s labSpec) error {
	dir, err := os.MkdirTemp("", "labverify-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, fam.solFile), []byte(s.Solution), 0o644); err != nil {
		return err
	}
	base := fmt.Sprintf(`export TFDIR=%q; `, dir)
	if out, err := sh(base+fam.applyTmpl, 240); err != nil {
		return fmt.Errorf("a solução gerada não aplicou: %s", trunc(out, 200))
	}
	if out, _ := sh(base+s.Validation, 60); !strings.Contains(out, s.Expected) {
		return fmt.Errorf("a validação não passou na própria solução")
	}
	empty, _ := os.MkdirTemp("", "labempty-")
	defer os.RemoveAll(empty)
	if oute, _ := sh(fmt.Sprintf(`export TFDIR=%q; `, empty)+s.Validation, 30); strings.Contains(oute, s.Expected) {
		return fmt.Errorf("a validação passa mesmo sem solução (fraca)")
	}
	return nil
}

func specToQuestion(fam *labFamily, s labSpec, topic string, level int) models.Question {
	id := fam.idPrefix + "-" + newID()
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
	q := models.Question{
		ID:         id,
		Cert:       models.Cert(fam.cert),
		Topic:      topic,
		Difficulty: diff,
		Type:       models.Lab,
		Question: strings.TrimSpace(s.Question) + "\n\nNo terminal do lab: **cd $TFLAB/" + id + "**, " + fam.runHint +
			"\n\n_(lab gerado e auto-verificado pela IA local)_",
		Hint: strings.TrimSpace(s.Hint),
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
		Explanation: strings.TrimSpace(s.Explanation) + "\n\n--- Gabarito (" + fam.solFile + ") ---\n" + strings.TrimSpace(s.Solution),
		Teardown:    []string{`rm -rf "` + work + `"`},
		DocURL:      fam.docURL,
	}
	return FinalizeLab(q, "")
}

func trunc(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
