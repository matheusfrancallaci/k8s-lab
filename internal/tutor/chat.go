package tutor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"estudo-app/internal/models"
)

// errNoCurriculum sinaliza que a cert não tem currículo embutido nem aprendido.
var errNoCurriculum = errors.New("curriculo desconhecido")

// curriculumSession pesquisa o currículo oficial da cert e monta o pacote
// inicial (labs + quiz). Usado pelo "montar currículo de X" e pelo pedido de
// lab que nomeia uma cert inteira ("crie um lab de KCNA").
func curriculumSession(target, msg string, level int, createSession func(ids []string) (string, string, int)) (ChatResult, error) {
	WarmRAG(target, msg)
	text, srcs, domains, ok := FetchCurriculum(target, 1)
	if !ok {
		return ChatResult{}, errNoCurriculum
	}
	qs, rep, err := Ingest(text, target, target+" · Currículo", level, 4, 4)
	if err != nil {
		return ChatResult{}, err
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
		res.Action = sessionAction(sid, first, total, qs)
	}
	return res, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Chat do tutor — interface conversacional. A compreensão é local (regras +
// sinônimos PT-BR); o LLM local só entra para conversa livre, com persona
// restrita a infra/cloud/programação. Zero API externa.
// ─────────────────────────────────────────────────────────────────────────────

// ChatAction diz à UI o que fazer além de exibir a resposta.
type ChatAction struct {
	Type         string   `json:"type"`            // session | stats | exam | none
	First        string   `json:"first,omitempty"` // primeira questão (session)
	ID           string   `json:"id,omitempty"`    // id da sessão
	Total        int      `json:"total,omitempty"` //
	Cert         string   `json:"cert,omitempty"`
	Topic        string   `json:"topic,omitempty"`
	Quality      int      `json:"quality,omitempty"`
	Sources      []string `json:"sources,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	Evidence     []string `json:"evidence,omitempty"`
	Chunks       []string `json:"chunks,omitempty"`
	DurationMin  int      `json:"duration_min,omitempty"`
	NoHints      bool     `json:"no_hints,omitempty"`
	Mode         string   `json:"mode,omitempty"`
	CheckpointID string   `json:"checkpoint_id,omitempty"`
	Question     string   `json:"question,omitempty"`
	Outcome      string   `json:"outcome,omitempty"`
	Score        int      `json:"score,omitempty"`
	NextPrompt   string   `json:"next_prompt,omitempty"`
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
	NeedsLLM bool           `json:"-"`
	Decision *TutorDecision `json:"decision,omitempty"`
}

// capabilitiesReply é o fallback mostrado quando não há intenção nem LLM.
const capabilitiesReply = "Posso te ajudar assim:\n" +
	"• **\"criar certificação Terraform\"** — coloco a cert no seu board\n" +
	"• **\"montar currículo de <cert>\"** — pesquiso a prova e gero labs+quiz\n" +
	"• **\"crie um lab de AWS\"** — gero fundamentos: compute, VPC, IAM e storage\n" +
	"• **\"crie labs para AZ-104/qualquer certificação\"** — entendo o foco e preparo labs no AKS\n" +
	"• **\"lab de storage nível 3\"** — crio labs sob medida\n" +
	"• **\"modo incidente\"** — quebro o cluster e você conserta\n" +
	"• **\"simulado\"** — exame completo cronometrado\n" +
	"• **\"revisar meus erros\"** — crio uma sessao guiada com seu caderno de erros\n" +
	"• **cole uma URL** (kubernetes.io, GitHub) — gero questões dela\n" +
	"• **\"como está meu desempenho?\"** — seu painel"

// FreeChatReply responde conversa livre (persona restrita ao escopo) de forma
// síncrona. Usado pelo handler quando ChatResult.NeedsLLM é true.
func FreeChatReply(msg string) (string, error) { return llmChatReply(msg) }

func FreeChatReplyContext(ctx context.Context, msg string) (string, error) {
	return llmChatReplyContext(ctx, msg)
}

func FreeChatConversationReplyContext(ctx context.Context, msg, history, mode string) (string, []string, GroundingAudit, error) {
	prompt, report := BuildGroundedChatPromptWithContext(msg, history, mode)
	route := RouteConversationModel(msg, mode)
	if technicalQuestion(msg) && !report.Answerable {
		return report.Refusal(), report.VerifiedSources(), GroundingAudit{Passed: true, Coverage: 100}, nil
	}
	reply, err := llmGenerateContext(ctx, prompt, false, 90*time.Second, chatTokenBudget(msg)+400, route.Model)
	if err != nil {
		return "", nil, GroundingAudit{}, err
	}
	final := FinalizeGroundedReply(reply, report)
	audit := AuditGroundedReply(final, report)
	RecordModelOutcome(route, audit)
	return final, report.VerifiedSources(), audit, nil
}

// sinônimos PT-BR → tópico do gerador
var topicSynonyms = []struct {
	re    *regexp.Regexp
	topic string
}{
	{regexp.MustCompile(`(?i)\bhpa\b|horizontal.?pod.?autoscal|autoscal|auto.?scal|escala(?:mento)? automatic`), "Autoscaling"},
	{regexp.MustCompile(`(?i)\breplica.?set\b|\breplicaset\b|\bscale.?set\b`), "ReplicaSet"},
	{regexp.MustCompile(`(?i)\blinux\b|chmod|permiss(?:ao|[ãa]o)|logs?|processamento de texto|grep|awk|sed`), "Linux"},
	{regexp.MustCompile(`(?i)\bbash\b|shell script|script bash|argumentos?|csv`), "Bash"},
	{regexp.MustCompile(`(?i)\bjava\b|javac|jvm|spring|classe|public static void main`), "Java"},
	{regexp.MustCompile(`(?i)\bhelm\b|helm chart|values\.yaml|chart\.yaml`), "Helm"},
	{regexp.MustCompile(`(?i)\bdockerfile\b|\bdocker\b|containerfile|container image`), "Docker"},
	{regexp.MustCompile(`(?i)argocd|argo.?cd|gitops|application.?set|sync.?policy`), "GitOps"},
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
		if n >= 1 && n <= 12 {
			return n
		}
	}
	return def
}

func routeCertForLabRequest(active, msg, topic string) string {
	cert := inferCertFromMessage(msg, active)
	if isAWSFocus(cert, msg) {
		cert = "AWS"
	}
	if cert == "" {
		cert = "CKA"
	}
	if topic == "GitOps" {
		if regexp.MustCompile(`(?i)\bCAPA\b`).MatchString(msg) || strings.EqualFold(cert, "CAPA") {
			return "CAPA"
		}
		if regexp.MustCompile(`(?i)argocd|argo.?cd`).MatchString(msg + " " + cert) {
			return "ArgoCD"
		}
	}
	return cert
}

// Chat processa uma mensagem e devolve a resposta + ação.
// createSession é injetado pelo handler (cria LabSession e devolve id/first).
func Chat(userID, msg, cert string, createSession func(ids []string) (string, string, int)) ChatResult {
	l := strings.ToLower(strings.TrimSpace(msg))
	if cert == "" {
		cert = "CKA"
	}
	intentDecision := ClassifyTutorRequest(msg, cert)

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
		res, err := curriculumSession(target, msg, detectLevel(msg), createSession)
		if err == nil {
			return res
		}
		if errors.Is(err, errNoCurriculum) {
			return ChatResult{Reply: fmt.Sprintf(
				"Ainda não tenho o currículo oficial de **%s** embutido. Cole aqui a **URL da página oficial do exame** (guia de domínios/tópicos) que eu leio, extraio os temas e monto tudo — só aceito fontes oficiais.", target)}
		}
		return ChatResult{Reply: "Li o currículo mas não consegui gerar: " + err.Error()}
	}

	// 1. Desempenho / progresso
	if intentDecision.Intent == "progress" {
		return ChatResult{
			Reply:  "Aqui está o seu panorama. Onde o score está baixo é onde eu atacaria primeiro — quer que eu gere um lab focado nele?",
			Action: &ChatAction{Type: "stats"},
		}
	}

	if regexp.MustCompile(`(?i)\b(trilha|plano de estudo|roadmap|sequ[eê]ncia|curriculo pratico|curr[ií]culo pr[aá]tico)\b`).MatchString(l) {
		qs, path, err := GenerateGatedLearningPath(userID, msg, cert, detectLevel(msg))
		if err != nil {
			return ChatResult{Reply: "Nao consegui montar uma trilha segura agora: " + err.Error()}
		}
		sid, first, total := createSession(questionIDs(qs))
		return ChatResult{
			Reply:     learningPathReply(path, total),
			Action:    sessionAction(sid, first, total, qs),
			Questions: qs,
		}
	}

	if regexp.MustCompile(`(?i)\b(replay|refazer erro|repetir erro|treinar erro|corrigir erro anterior)\b`).MatchString(l) {
		qs, topic := GenerateReplayLab(userID, cert)
		if len(qs) == 0 {
			return ChatResult{Reply: "Ainda nao tenho erro pronto para replay. Faz um lab, deixa a validacao registrar a falha, e eu monto o reforco exato depois."}
		}
		sid, first, total := createSession(questionIDs(qs))
		return ChatResult{
			Reply:     fmt.Sprintf("Criei um replay focado no seu erro anterior de **%s**. Vem sem mao na roda para reforcar memoria de prova.", topic),
			Action:    sessionAction(sid, first, total, qs),
			Questions: qs,
		}
	}

	if regexp.MustCompile(`(?i)revis|caderno|erros?|falhas?|refor[cç]o`).MatchString(l) {
		qs := reviewLabs(userID, cert, detectCount(msg, 5))
		if len(qs) == 0 {
			return ChatResult{
				Reply:  "Ainda nao tenho erros suficientes no seu perfil para montar uma revisao automatica. Faz alguns labs ou pede um topico exato que eu crio o treino agora.",
				Action: &ChatAction{Type: "stats"},
			}
		}
		if err := persist(qs); err != nil {
			return ChatResult{Reply: "Montei a revisao, mas nao consegui salvar os labs: " + err.Error()}
		}
		sid, first, total := createSession(questionIDs(qs))
		return ChatResult{
			Reply:     fmt.Sprintf("Montei uma revisao guiada com **%d lab(s)** baseada no seu caderno de erros. Comecei pelos topicos vencidos e com mais falhas.", total),
			Action:    sessionAction(sid, first, total, qs),
			Questions: qs,
		}
	}

	// 1.9. Lab composto estilo prova — cenário multi-tarefa com crédito parcial
	if regexp.MustCompile(`(?i)\b(estilo prova|desafio composto|quest[aã]o composta|lab de prova|multi.?tarefa)\b`).MatchString(l) {
		q, err := GenerateExamComposite(cert, 3)
		if err != nil {
			return ChatResult{Reply: "Não consegui compor um lab estilo prova agora: " + err.Error()}
		}
		sid, first, total := createSession([]string{q.ID})
		return ChatResult{
			Reply:     fmt.Sprintf("🎯 Montei um **lab estilo prova de %s**: %d tarefas independentes com crédito parcial por goal — como na certificação real. Sem pressa, mas sem dica.", cert, len(q.Goals)),
			Action:    sessionAction(sid, first, total, []models.Question{q}),
			Questions: []models.Question{q},
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
			Action:    sessionAction(sid, first, total, qs),
			Questions: qs,
		}
	}

	// 3. Modo exame
	if regexp.MustCompile(`exame|simulado|prova completa`).MatchString(l) {
		return ChatResult{
			Reply:  "🏆 Modo Exame: 16 questões, 2 horas, sem dicas — como na prova real. Pronto?",
			Action: &ChatAction{Type: "exam", Total: 16, DurationMin: 120, NoHints: true, Mode: "strict"},
		}
	}

	// 4. Gerar de documentação (URL ou pedido explícito)
	if urlRe.MatchString(msg) || regexp.MustCompile(`quest[õo]es sobre|gerar d[ea]|estudar sobre|quiz sobre`).MatchString(l) {
		// A cert do material é a da MENSAGEM ("crie um lab da KCNA <url>"), não
		// o chip ativo — senão o conteúdo novo é atribuído à cert errada (bug
		// real: URL da KCNA virava labs de CKA). Cert citada e inédita é
		// registrada na hora; o Ingest aprende o currículo dela do material.
		if named, ok := certNamedInMessage(msg); ok && !strings.EqualFold(named, cert) {
			if reg, _ := RegisterCert(named); reg != "" {
				cert = reg
			}
		}
		topic := detectTopic(msg)
		if topic == "" {
			topic = "Custom"
		}
		WarmRAG(cert, msg)
		qs, rep, err := Ingest(msg, cert, topic, detectLevel(msg), detectCount(msg, 5), 4)
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
			res.Action = sessionAction(sid, first, total, qs)
		}
		return res
	}

	// 4.7 Gerar lab hands-on AUTÔNOMO (Terraform, Ansible, ...): o tutor gera com
	// o LLM e AUTO-VERIFICA rodando a solução de verdade (só entrega labs que
	// corrigem certo). Só dispara para famílias suportadas.
	if regexp.MustCompile(`(?i)\b(ger|cri|mont|fa[çc]|faz|quero|monta)`).MatchString(l) &&
		regexp.MustCompile(`(?i)lab|laborat|exerc[ií]cio|pr[aá]tic`).MatchString(l) &&
		regexp.MustCompile(`(?i)terraform|ansible|playbook`).MatchString(l+" "+strings.ToLower(cert)) {
		fam := familyForMessage(msg, cert)
		topic := ""
		if m := regexp.MustCompile(`(?i)sobre\s+(.+?)\s*$`).FindStringSubmatch(msg); m != nil {
			topic = strings.TrimSpace(m[1])
		}
		q, err := GenerateVerifiedLab(fam, topic, detectLevel(msg), 3)
		if err != nil {
			return ChatResult{Reply: "Tentei gerar um lab de " + fam.name + " e auto-verificar rodando a solução, mas não consegui agora: " + err.Error() + "\n\nTenta um tema específico, ex.: **\"gerar lab de " + fam.name + " sobre <tema>\"**."}
		}
		sid, first, total := createSession([]string{q.ID})
		return ChatResult{
			Reply:     "🤖 Gerei um lab de **" + fam.name + " do zero** e **auto-verifiquei** rodando a solução de verdade — a correção funciona. Bora praticar?",
			Action:    sessionAction(sid, first, total, []models.Question{q}),
			Questions: []models.Question{q},
		}
	}

	routeCert := routeCertForLabRequest(cert, msg, "")
	if topic := exactTopicForRequest(routeCert, msg); topic != "" && regexp.MustCompile(`lab|exerc|quest|pergunta|praticar|treinar|criar?|gera`).MatchString(l) {
		level := detectLevel(msg)
		cert = routeCertForLabRequest(cert, msg, topic)
		qs, err := Generate(topic, cert, level, detectCount(msg, 5))
		if err != nil {
			return ChatResult{Reply: err.Error()}
		}
		for _, q := range qs {
			if err := LabRequestAdherence(q, msg); err != nil {
				return ChatResult{Reply: "Nao gerei esse lab: " + err.Error()}
			}
		}
		sid, first, total := createSession(questionIDs(qs))
		levelDesc := map[int]string{1: "modo desafio (sem dicas)", 2: "guiado", 3: "passo a passo completo"}[level]
		return ChatResult{
			Reply:     fmt.Sprintf("Criei **%d lab(s) de %s** no nivel %d - %s. O ambiente ja esta sendo preparado.", total, topic, level, levelDesc),
			Action:    sessionAction(sid, first, total, qs),
			Questions: qs,
		}
	}

	// 5. Criar lab por tópico
	if topic := detectTopic(msg); topic != "" && regexp.MustCompile(`lab|exerc[ií]cio|praticar|treinar|criar?|gera`).MatchString(l) {
		level := detectLevel(msg)
		cert = routeCertForLabRequest(cert, msg, topic)
		qs, err := Generate(topic, cert, level, detectCount(msg, 5))
		if err != nil {
			return ChatResult{Reply: err.Error()}
		}
		for _, q := range qs {
			if err := LabRequestAdherence(q, msg); err != nil {
				return ChatResult{Reply: "Nao gerei esse lab: " + err.Error()}
			}
		}
		sid, first, total := createSession(questionIDs(qs))
		levelDesc := map[int]string{1: "modo desafio (sem dicas)", 2: "guiado", 3: "passo a passo completo"}[level]
		return ChatResult{
			Reply:     fmt.Sprintf("✦ Criei **%d lab(s) de %s** no nível %d — %s. O ambiente já está sendo preparado.", total, topic, level, levelDesc),
			Action:    sessionAction(sid, first, total, qs),
			Questions: qs,
		}
	}

	// 5.5. Pedido amplo de lab/certificacao: entende o foco livre (ex.: AZ-104,
	// AKS, DevOps, cloud security), registra a certificacao e entrega labs
	// Kubernetes/AKS seguros em vez de cair em resposta generica.
	if isBroadLabRequest(msg) {
		level := detectLevel(msg)
		// Pedido nomeia uma CERT inteira ("crie um lab de KCNA") sem tópico:
		// se o currículo é conhecido (embutido ou aprendido), monta direto da
		// fonte oficial em vez de recusar — a recusa aqui era o beco sem saída
		// do bug da KCNA.
		if named, ok := certNamedInMessage(msg); ok && exactTopicForRequest(named, msg) == "" && detectTopic(msg) == "" {
			if _, hasCur := CurriculumFor(named); hasCur {
				if reg, _ := RegisterCert(named); reg != "" {
					named = reg
				}
				if res, err := curriculumSession(named, msg, level, createSession); err == nil {
					return res
				}
			}
		}
		if err := LabRequestPreflight(msg, cert); err != nil {
			return ChatResult{Reply: "Nao gerei um lab generico para esse pedido. " + err.Error()}
		}
		qs, rep, err := GenerateSmartLabs(msg, cert, level, detectCount(msg, 5))
		if err != nil {
			return ChatResult{Reply: "Nao gerei um lab generico para esse pedido: " + err.Error()}
		}
		sid, first, total := createSession(questionIDs(qs))
		reply := fmt.Sprintf("Preparei **%d lab(s)** para **%s** com foco em **%s**. O ambiente AKS/Kubernetes ja fica pronto com setup, validacao automatica e limpeza.", total, rep.Cert, rep.Topic)
		if rep.UsedLLM && rep.Reason != "" {
			reply += "\n\nCriterio usado pela IA: " + rep.Reason
		}
		if len(rep.Ingest.Sources) > 0 {
			reply += fmt.Sprintf("\n\nPesquisei %d fonte(s) confiavel(is) para contextualizar o pedido.", len(rep.Ingest.Sources))
		}
		return ChatResult{
			Reply:     reply,
			Action:    sessionAction(sid, first, total, qs),
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

func reviewLabs(userID, activeCert string, count int) []models.Question {
	if count < 1 {
		count = 3
	}
	if count > 6 {
		count = 6
	}
	var qs []models.Question
	seen := map[string]bool{}
	addTopic := func(cert, topic string) {
		if len(qs) >= count || topic == "" {
			return
		}
		if _, ok := templates[topic]; !ok {
			return
		}
		if cert == "" {
			cert = activeCert
		}
		if cert == "" {
			cert = "CKA"
		}
		key := cert + "|" + topic
		if seen[key] {
			return
		}
		seen[key] = true
		WarmRAG(cert, topic)
		qs = append(qs, generateQuestions(topic, cert, 3, 1)...)
	}
	for _, item := range ReviewQueue(userID) {
		if activeCert != "" && item.Cert != "" && !strings.EqualFold(item.Cert, activeCert) && len(qs) > 0 {
			continue
		}
		addTopic(item.Cert, item.Topic)
	}
	stats := Stats(userID)
	sort.SliceStable(stats, func(i, j int) bool {
		if stats[i].Score == stats[j].Score {
			return stats[i].Failures > stats[j].Failures
		}
		return stats[i].Score < stats[j].Score
	})
	for _, s := range stats {
		if s.Attempts == 0 {
			continue
		}
		addTopic(s.Cert, s.Topic)
	}
	return qs
}

func sessionAction(id, first string, total int, qs []models.Question) *ChatAction {
	a := &ChatAction{Type: "session", ID: id, First: first, Total: total}
	if len(qs) == 0 {
		return a
	}
	a.Cert = string(qs[0].Cert)
	a.Topic = qs[0].Topic
	qualitySum, qualityN := 0, 0
	seenSources := map[string]bool{}
	seenDeps := map[string]bool{}
	seenEvidence := map[string]bool{}
	seenChunks := map[string]bool{}
	for _, q := range qs {
		if q.LabSpec == nil {
			q = FinalizeLab(q, "")
		}
		if q.LabSpec == nil {
			continue
		}
		if q.LabSpec.Quality.Score > 0 {
			qualitySum += q.LabSpec.Quality.Score
			qualityN++
		}
		for _, s := range q.LabSpec.Sources {
			if s.URL != "" && !seenSources[s.URL] && len(a.Sources) < 3 {
				seenSources[s.URL] = true
				a.Sources = append(a.Sources, s.Title)
			}
		}
		for _, d := range q.LabSpec.Dependencies {
			if d.Name != "" && !seenDeps[d.Name] && len(a.Dependencies) < 4 {
				seenDeps[d.Name] = true
				a.Dependencies = append(a.Dependencies, d.Name)
			}
		}
		for _, e := range q.LabSpec.Evidence {
			label := e.Domain
			if e.Confidence > 0 {
				label = fmt.Sprintf("%s %d", e.Domain, e.Confidence)
			}
			if label != "" && !seenEvidence[label] && len(a.Evidence) < 4 {
				seenEvidence[label] = true
				a.Evidence = append(a.Evidence, label)
			}
		}
		for _, c := range q.LabSpec.Chunks {
			label := c.Domain
			if c.Relevance > 0 {
				label = fmt.Sprintf("%s %d", c.Domain, c.Relevance)
			}
			if label != "" && !seenChunks[label] && len(a.Chunks) < 4 {
				seenChunks[label] = true
				a.Chunks = append(a.Chunks, label)
			}
		}
	}
	if qualityN > 0 {
		a.Quality = qualitySum / qualityN
	}
	return a
}

// llmChatReply responde conversa livre com persona restrita ao escopo.
func llmChatReply(msg string) (string, error) {
	return llmChatReplyContext(context.Background(), msg)
}

func llmChatReplyContext(ctx context.Context, msg string) (string, error) {
	prompt, report := BuildGroundedChatPrompt(msg)
	if technicalQuestion(msg) && !report.Answerable {
		return report.Refusal(), nil
	}
	cacheKey := groundedReplyKey(msg, chatModel(), report)
	if reply, ok := cachedGroundedReply(cacheKey); ok {
		return reply, nil
	}
	reply, err := llmGenerateContext(ctx, prompt, false, 60*time.Second, chatTokenBudget(msg), chatModel())
	if err != nil {
		return "", err
	}
	final := FinalizeGroundedReply(reply, report)
	storeGroundedReply(cacheKey, final)
	return final, nil
}
