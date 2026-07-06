package tutor

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Chat do tutor — interface conversacional. A compreensão é local (regras +
// sinônimos PT-BR); o LLM local só entra para conversa livre, com persona
// restrita a infra/cloud/programação. Zero API externa.
// ─────────────────────────────────────────────────────────────────────────────

// ChatAction diz à UI o que fazer além de exibir a resposta.
type ChatAction struct {
	Type  string `json:"type"`            // session | stats | exam | none
	First string `json:"first,omitempty"` // primeira questão (session)
	ID    string `json:"id,omitempty"`    // id da sessão
	Total int    `json:"total,omitempty"` //
}

// ChatResult é a resposta completa do tutor.
type ChatResult struct {
	Reply     string            `json:"reply"`
	Action    *ChatAction       `json:"action,omitempty"`
	Questions []models.Question `json:"-"` // para o handler registrar no repo
	// NeedsLLM sinaliza que NENHUMA intenção casou e o caller deve responder
	// com o LLM local (conversa livre). O roteamento NÃO chama o LLM aqui —
	// assim o endpoint de streaming responde token a token sem chamada dupla, e
	// intenções ("criar certificação X", "montar currículo", "modo incidente")
	// nunca são sobrescritas por um papo genérico. Reply carrega o fallback de
	// capacidades para quando o LLM estiver indisponível ou falhar.
	NeedsLLM bool `json:"-"`
}

// capabilitiesReply é o fallback mostrado quando não há intenção nem LLM.
const capabilitiesReply = "Posso te ajudar assim:\n" +
	"• **\"criar certificação Terraform\"** — coloco a cert no seu board\n" +
	"• **\"montar currículo de <cert>\"** — pesquiso a prova e gero labs+quiz\n" +
	"• **\"lab de storage nível 3\"** — crio labs sob medida\n" +
	"• **\"modo incidente\"** — quebro o cluster e você conserta\n" +
	"• **\"simulado\"** — exame completo cronometrado\n" +
	"• **cole uma URL** (kubernetes.io, GitHub) — gero questões dela\n" +
	"• **\"como está meu desempenho?\"** — seu painel"

// FreeChatReply responde conversa livre (persona restrita ao escopo) de forma
// síncrona. Usado pelo handler quando ChatResult.NeedsLLM é true.
func FreeChatReply(msg string) (string, error) { return llmChatReply(msg) }

// sinônimos PT-BR → tópico do gerador
var topicSynonyms = []struct {
	re    *regexp.Regexp
	topic string
}{
	{regexp.MustCompile(`(?i)storage|armazenament|volume|pvc|pv\b`), "Storage"},
	{regexp.MustCompile(`(?i)seguran|security|rbac|networkpolicy|secret`), "Security"},
	// "networkpolicy" nunca chega aqui: Security vem antes na lista
	{regexp.MustCompile(`(?i)servi[cç]|service|rede|network|expose`), "Services"},
	{regexp.MustCompile(`(?i)agendament|scheduling|taint|affinity|nodeselector`), "Scheduling"},
	{regexp.MustCompile(`(?i)config|secret|env|vari[aá]vel`), "Configuration"},
	{regexp.MustCompile(`(?i)probe|liveness|readiness|init.?container|design`), "Application Design"},
	{regexp.MustCompile(`(?i)deployment|workload|job|cronjob|replica|rollout`), "Workloads"},
	{regexp.MustCompile(`(?i)\bpod\b|namespace|quota|core`), "Core Concepts"},
}

func detectTopic(msg string) string {
	for _, s := range topicSynonyms {
		if s.re.MatchString(msg) {
			return s.topic
		}
	}
	return ""
}

func detectLevel(msg string) int {
	l := strings.ToLower(msg)
	switch {
	case strings.Contains(l, "nível 3") || strings.Contains(l, "nivel 3") ||
		strings.Contains(l, "máxima") || strings.Contains(l, "maxima") ||
		strings.Contains(l, "passo a passo") || strings.Contains(l, "iniciante"):
		return 3
	case strings.Contains(l, "nível 1") || strings.Contains(l, "nivel 1") ||
		strings.Contains(l, "desafio") || strings.Contains(l, "difícil") || strings.Contains(l, "dificil") ||
		strings.Contains(l, "como na prova"):
		return 1
	}
	return 2
}

var countRe = regexp.MustCompile(`(\d+)\s*(quest|lab|exerc)`)

func detectCount(msg string, def int) int {
	if m := countRe.FindStringSubmatch(strings.ToLower(msg)); m != nil {
		n := 0
		fmt.Sscanf(m[1], "%d", &n) //nolint:errcheck
		if n >= 1 && n <= 10 {
			return n
		}
	}
	return def
}

// Chat processa uma mensagem e devolve a resposta + ação.
// createSession é injetado pelo handler (cria LabSession e devolve id/first).
func Chat(msg, cert string, createSession func(ids []string) (string, string, int)) ChatResult {
	l := strings.ToLower(strings.TrimSpace(msg))
	if cert == "" {
		cert = "CKA"
	}

	// 0. Cadastrar certificação nova → personaliza o board de verdade
	if m := regexp.MustCompile(`(?i)(?:criar|adicionar|cadastrar|nova|quero(?:\s+estudar)?(?:\s+para)?)\s+(?:a\s+)?certifica[cç][aã]o\s+(?:de\s+|do\s+|da\s+)?([A-Za-z0-9][A-Za-z0-9 ._+-]{1,30})`).FindStringSubmatch(msg); m != nil {
		name, isNew := RegisterCert(strings.TrimSpace(m[1]))
		if name == "" {
			return ChatResult{Reply: "esse nome de certificação não parece válido — tenta algo como \"criar certificação Terraform\"."}
		}
		if !isNew {
			return ChatResult{Reply: fmt.Sprintf("**%s** já está no seu board! Selecione o chip dela aqui embaixo e me manda material (URL da doc oficial ou um tema) que eu gero labs e questões.", name), Action: &ChatAction{Type: "certs"}}
		}
		reply := fmt.Sprintf("✦ Cadastrei **%s** no seu board — o chip dela já aparece aqui embaixo!\n\n", name)
		if _, hasCur := CurriculumFor(name); hasCur {
			reply += fmt.Sprintf("Tenho o currículo oficial dela: diga **\"montar currículo de %s\"** que eu pesquiso os temas da prova na documentação e construo tudo sozinho.", name)
		} else {
			reply += fmt.Sprintf("Agora me alimenta: cole a **URL da página oficial do exame** ou da documentação com o chip de %s selecionado — eu extraio os temas, busco cada um na doc oficial e cito a linha exata de onde cada questão saiu.", name)
		}
		return ChatResult{Reply: reply, Action: &ChatAction{Type: "certs"}}
	}

	// 0.5. Montar currículo oficial — o tutor pesquisa os temas da prova nas
	// páginas oficiais e constrói o pacote inicial sozinho.
	if regexp.MustCompile(`(?i)mont(?:ar|e).{0,12}curr[ií]cul|pesquis(?:ar|e).{0,20}(?:temas|t[óo]picos)`).MatchString(l) {
		// cert pode vir na mensagem ("montar currículo de CKS") ou do chip ativo
		target := cert
		for _, c := range AllCerts() {
			if regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(c) + `\b`).MatchString(msg) {
				target = c
				break
			}
		}
		text, srcs, domains, ok := FetchCurriculum(target, 1)
		if !ok {
			return ChatResult{Reply: fmt.Sprintf(
				"Ainda não tenho o currículo oficial de **%s** embutido. Cole aqui a **URL da página oficial do exame** (guia de domínios/tópicos) que eu leio, extraio os temas e monto tudo — só aceito fontes oficiais.", target)}
		}
		qs, rep, err := Ingest(text, target, target+" · Currículo", detectLevel(msg), 4, 4)
		if err != nil {
			return ChatResult{Reply: "Li o currículo mas não consegui gerar: " + err.Error()}
		}
		reply := fmt.Sprintf("📋 Pesquisei o **currículo oficial de %s** e li %d página(s) da documentação:\n", target, len(srcs))
		for _, d := range domains {
			reply += fmt.Sprintf("• **%s** — %d%%\n", d.Domain, d.Weight)
		}
		reply += fmt.Sprintf("\n✦ Montei o pacote inicial: **%d lab(s)** e **%d pergunta(s)** — cada questão cita a linha exata da doc de onde saiu.", rep.Labs, rep.Quizzes)
		var labIDs []string
		for _, q := range qs {
			if q.Type == models.Lab {
				labIDs = append(labIDs, q.ID)
			}
		}
		res := ChatResult{Reply: reply, Questions: qs}
		if len(labIDs) > 0 {
			sid, first, total := createSession(labIDs)
			res.Action = &ChatAction{Type: "session", ID: sid, First: first, Total: total}
		}
		return res
	}

	// 1. Desempenho / progresso
	if regexp.MustCompile(`desempenho|progresso|como estou|estat|pontos? frac|dashboard`).MatchString(l) {
		return ChatResult{
			Reply:  "Aqui está o seu panorama. Onde o score está baixo é onde eu atacaria primeiro — quer que eu gere um lab focado nele?",
			Action: &ChatAction{Type: "stats"},
		}
	}

	// 2. Modo incidente
	if regexp.MustCompile(`incidente|troubleshoot|quebra|fora do ar|diagn[oó]stic`).MatchString(l) {
		qs, err := GenerateIncidents(cert, detectCount(msg, 2))
		if err != nil {
			return ChatResult{Reply: "Não consegui montar os incidentes agora: " + err.Error()}
		}
		ids := questionIDs(qs)
		sid, first, total := createSession(ids)
		return ChatResult{
			Reply:     fmt.Sprintf("🚨 Preparei **%d incidente(s)** — o cluster vai estar quebrado quando você chegar. Diagnostique com `kubectl get/describe` antes de agir. Boa caçada!", total),
			Action:    &ChatAction{Type: "session", ID: sid, First: first, Total: total},
			Questions: qs,
		}
	}

	// 3. Modo exame
	if regexp.MustCompile(`exame|simulado|prova completa`).MatchString(l) {
		return ChatResult{
			Reply:  "🏆 Modo Exame: 16 questões, 2 horas, sem dicas — como na prova real. Pronto?",
			Action: &ChatAction{Type: "exam"},
		}
	}

	// 4. Gerar de documentação (URL ou pedido explícito)
	if urlRe.MatchString(msg) || regexp.MustCompile(`quest[õo]es sobre|gerar d[ea]|estudar sobre|quiz sobre`).MatchString(l) {
		topic := detectTopic(msg)
		if topic == "" {
			topic = "Custom"
		}
		qs, rep, err := Ingest(msg, cert, topic, detectLevel(msg), detectCount(msg, 2), 2)
		if err != nil {
			return ChatResult{Reply: "Não consegui gerar daí: " + err.Error()}
		}
		reply := fmt.Sprintf("📖 Li o material (%d comando(s), %d manifest(s), %d conceito(s))", rep.Commands, rep.Manifests, rep.Concepts)
		for _, n := range rep.Notes {
			reply += "\n🧭 " + n
		}
		reply += fmt.Sprintf("\n✦ Gerei **%d lab(s)** e **%d pergunta(s)** de quiz.", rep.Labs, rep.Quizzes)
		var labIDs []string
		for _, q := range qs {
			if q.Type == models.Lab {
				labIDs = append(labIDs, q.ID)
			}
		}
		res := ChatResult{Reply: reply, Questions: qs}
		if len(labIDs) > 0 {
			sid, first, total := createSession(labIDs)
			res.Action = &ChatAction{Type: "session", ID: sid, First: first, Total: total}
		}
		return res
	}

	// 4.7 Gerar lab de Terraform AUTÔNOMO: o tutor gera com o LLM e AUTO-VERIFICA
	// rodando a solução de verdade (só entrega labs que corrigem certo).
	if regexp.MustCompile(`(?i)\b(ger[ae]|cria|criar|monta|montar|quero|fa[çc]a|faz)\b`).MatchString(l) &&
		regexp.MustCompile(`(?i)lab|laborat|exerc[ií]cio|pr[aá]tic`).MatchString(l) &&
		(strings.EqualFold(cert, "Terraform") || strings.Contains(l, "terraform")) {
		topic := ""
		if m := regexp.MustCompile(`(?i)sobre\s+(.+?)\s*$`).FindStringSubmatch(msg); m != nil {
			topic = strings.TrimSpace(m[1])
		}
		q, err := GenerateVerifiedTFLab(topic, detectLevel(msg), 2)
		if err != nil {
			return ChatResult{Reply: "Tentei gerar um lab de Terraform e auto-verificar rodando a solução, mas não consegui agora: " + err.Error() + "\n\nTenta pedir um tema específico, ex.: **\"gerar lab de terraform sobre variáveis\"**."}
		}
		sid, first, total := createSession([]string{q.ID})
		return ChatResult{
			Reply:     "🤖 Gerei um lab de **Terraform do zero** e **auto-verifiquei** rodando a solução de verdade — a correção funciona. Bora praticar?",
			Action:    &ChatAction{Type: "session", ID: sid, First: first, Total: total},
			Questions: []models.Question{q},
		}
	}

	// 5. Criar lab por tópico
	if topic := detectTopic(msg); topic != "" && regexp.MustCompile(`lab|exerc[ií]cio|praticar|treinar|criar?|gera`).MatchString(l) {
		level := detectLevel(msg)
		qs, err := Generate(topic, cert, level, detectCount(msg, 2))
		if err != nil {
			return ChatResult{Reply: err.Error()}
		}
		sid, first, total := createSession(questionIDs(qs))
		levelDesc := map[int]string{1: "modo desafio (sem dicas)", 2: "guiado", 3: "passo a passo completo"}[level]
		return ChatResult{
			Reply:     fmt.Sprintf("✦ Criei **%d lab(s) de %s** no nível %d — %s. O ambiente já está sendo preparado.", total, topic, level, levelDesc),
			Action:    &ChatAction{Type: "session", ID: sid, First: first, Total: total},
			Questions: qs,
		}
	}

	// 6. Nenhuma intenção casou → conversa livre. Sinaliza ao handler para usar
	// o LLM (streaming ou síncrono). Reply carrega o fallback de capacidades,
	// usado só se o LLM estiver fora ou falhar.
	if ok, _ := LLMStatus(); ok {
		return ChatResult{NeedsLLM: true, Reply: capabilitiesReply}
	}
	return ChatResult{Reply: capabilitiesReply + "\n\nInstale o Ollama para conversarmos livremente."}
}

func questionIDs(qs []models.Question) []string {
	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	return ids
}

// llmChatReply responde conversa livre com persona restrita ao escopo.
func llmChatReply(msg string) (string, error) {
	if len(msg) > 1500 {
		msg = msg[:1500]
	}
	prompt := fmt.Sprintf(`Você é o Tutor do k8s-lab: um mentor especialista em infraestrutura, cloud, IaC e programação. Responda em português do Brasil, direto e didático, em NO MÁXIMO 6 frases.

ESCOPO (tudo isto é válido, responda normalmente): Kubernetes, containers, cloud (Azure/AWS/GCP), Terraform e Infraestrutura como Código, Linux, redes, DevOps, CI/CD, GitOps/ArgoCD, Helm e programação. Terraform/IaC SÃO tópicos centrais — ajude com HCL, providers, state, módulos, plan/apply. Só recuse se a pergunta fugir TOTALMENTE de tecnologia (ex.: culinária, política); aí recuse em 1 frase.

Pergunta do aluno: %s`, strings.TrimSpace(msg))
	return llmGenerate(prompt, false, 60*time.Second, tokensChat)
}
