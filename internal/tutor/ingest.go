package tutor

import (
	"fmt"
	"math/rand/v2"
	"regexp"
	"strings"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Ingestão de documentação — extrai comandos, manifests e conceitos de um
// texto colado pelo usuário e gera labs + quiz. Heurístico, sem LLM.
// ─────────────────────────────────────────────────────────────────────────────

// IngestReport é a resposta da análise: o que foi encontrado, o que foi
// gerado, e quais insumos ainda faltam (o "agente pede informações").
type IngestReport struct {
	Commands  int      `json:"commands"`
	Manifests int      `json:"manifests"`
	Concepts  int      `json:"concepts"`
	Generated []string `json:"generated"` // IDs das questões criadas
	Labs      int      `json:"labs"`
	Quizzes   int      `json:"quizzes"`
	UsedLLM   bool     `json:"used_llm"` // perguntas geradas pela IA local (Ollama)
	Sources   []string `json:"sources"`  // URLs lidas automaticamente pelo tutor
	Notes     []string `json:"notes"`    // decisões que o tutor tomou (ex.: material fora do domínio)
	Missing   []string `json:"missing"`  // insumos que faltam para gerar mais
}

// snippet é um trecho extraído que sabe DE ONDE veio (para citar a fonte).
type snippet struct {
	val    string // o conteúdo (comando ou manifest)
	source string // URL ou "material colado"
	line   string // a linha exata na fonte
}

// cite devolve o rodapé de citação para a explicação da questão.
func (s snippet) cite() string {
	if s.line == "" {
		return ""
	}
	src := s.source
	if src == "" {
		src = "material colado"
	}
	return fmt.Sprintf("\n\n📍 Onde a fonte diz isso (%s):\n“%s”", src, s.line)
}

var (
	cmdRe   = regexp.MustCompile(`(?m)^\s*\$?\s*((?:kubectl|helm|az|argocd|terraform|docker|gcloud|aws)\s+[a-z][^\n#]{3,120})`)
	kindRe  = regexp.MustCompile(`(?m)^\s*kind:\s*([A-Za-z]+)`)
	nameRe  = regexp.MustCompile(`(?m)^\s*name:\s*([a-z0-9][a-z0-9.-]*)`)
	flagRe  = regexp.MustCompile(`\s(--?[a-z][a-z-]+)`)
	fenceRe = regexp.MustCompile("(?s)```[a-z]*\n(.*?)```")
)

// glossário embutido: termo → definição curta (base dos quizzes conceituais).
var glossary = map[string]string{
	"Pod":                     "A menor unidade implantável: um ou mais containers que compartilham rede e storage",
	"Deployment":              "Controller declarativo que gerencia ReplicaSets e rolling updates de apps stateless",
	"ReplicaSet":              "Garante que um número especificado de réplicas de um pod esteja rodando",
	"Service":                 "Abstração que dá IP/DNS estável e balanceia tráfego entre pods via selector",
	"Ingress":                 "Regras L7 (host/path) para rotear tráfego HTTP externo a Services internos",
	"ConfigMap":               "Armazena configuração não-sensível consumida como env vars ou volumes",
	"Secret":                  "Armazena dados sensíveis (base64) consumidos como env vars ou volumes",
	"Namespace":               "Partição lógica do cluster para isolar nomes, quotas e permissões",
	"PersistentVolume":        "Recurso de storage do cluster, provisionado estática ou dinamicamente",
	"StatefulSet":             "Controller para apps com identidade estável: nomes, storage e ordem garantidos",
	"DaemonSet":               "Garante uma cópia do pod em cada nó (ou subconjunto) do cluster",
	"Job":                     "Executa pods até a tarefa completar com sucesso",
	"CronJob":                 "Agenda Jobs recorrentes com sintaxe cron",
	"ServiceAccount":          "Identidade usada por processos dentro de pods para falar com a API",
	"Role":                    "Conjunto de permissões (verbos × recursos) dentro de um namespace",
	"ClusterRole":             "Conjunto de permissões válido no cluster inteiro",
	"RoleBinding":             "Concede um Role a users, groups ou service accounts",
	"NetworkPolicy":           "Regras de firewall L3/L4 entre pods baseadas em selectors",
	"ResourceQuota":           "Limita o consumo agregado de recursos por namespace",
	"LimitRange":              "Define defaults e limites de recursos por pod/container em um namespace",
	"HorizontalPodAutoscaler": "Escala réplicas automaticamente com base em métricas como CPU",
	"taint":                   "Marca em um nó que repele pods sem a toleration correspondente",
	"toleration":              "Permissão no pod para agendar em nós com determinado taint",
	"nodeSelector":            "Filtro simples que agenda o pod apenas em nós com as labels exigidas",
	"affinity":                "Regras ricas de atração/repulsão entre pods e nós no scheduling",
	"kubelet":                 "Agente que roda em cada nó e garante que os containers dos pods estejam rodando",
	"etcd":                    "Banco chave-valor que armazena todo o estado do cluster",
	"kube-apiserver":          "Porta de entrada do control plane: valida e persiste todos os objetos",
	"kube-scheduler":          "Decide em qual nó cada pod novo será executado",
	"kube-proxy":              "Implementa o roteamento dos Services em cada nó",
	"readinessProbe":          "Health check que decide se o pod recebe tráfego (entra nos endpoints)",
	"livenessProbe":           "Health check que reinicia o container quando falha",
	"initContainer":           "Container que roda e completa antes dos containers principais do pod",
	"PVC":                     "Pedido de storage feito pelo usuário, satisfeito por um PersistentVolume",
	"StorageClass":            "Perfil de storage que habilita provisionamento dinâmico de volumes",
	"rollout":                 "Processo de atualização gradual de um Deployment para nova versão",
	"cordon":                  "Marca o nó como unschedulable sem afetar pods existentes",
	"drain":                   "Evacua os pods de um nó para manutenção, respeitando PodDisruptionBudgets",
}

// distratores de flags para quizzes cloze de comandos
var flagPool = []string{"--replicas", "--image", "--port", "--namespace", "--selector", "--dry-run", "--force", "--grace-period", "--from-literal", "--target-port", "--type", "--verb", "--resource", "--labels", "--all"}

// Ingest analisa o texto e gera até wantLabs labs + wantQuiz quizzes.
// Retorna as questões geradas + relatório do que foi visto e do que falta.
func Ingest(text, cert, topic string, level, wantLabs, wantQuiz int) ([]models.Question, IngestReport, error) {
	if cert == "" {
		cert = "CKA"
	}
	if topic == "" {
		topic = "Custom"
	}
	if level < 1 || level > 3 {
		level = 2
	}

	// O tutor lê sozinho: URLs coladas (kubernetes.io, GitHub) ou, se o usuário
	// só descreveu o tema, localiza a página certa na documentação oficial.
	text, sources, blocked := EnrichSource(text)

	rep := IngestReport{Sources: sources}
	for _, b := range blocked {
		rep.Notes = append(rep.Notes, "URL ignorada por segurança (fora do escopo infra/cloud/dev): "+b)
	}

	// Consciência de certificação: se o material não cobre a cert pedida
	// (ex.: link fala de Pods mas o usuário quer CKS), busca sozinho nas
	// fontes oficiais da cert e complementa o material.
	if cert != "" && shouldComplement(text, cert) {
		extra, extraSrc := FetchCertSources(cert, 2)
		if extra != "" {
			rep.Notes = append(rep.Notes, fmt.Sprintf(
				"o material fornecido cobre pouco do domínio %s — complementei com fontes oficiais da certificação", cert))
			// Fontes da cert vêm ANTES: o LLM tem contexto limitado e precisa
			// ver o conteúdo do domínio pedido, não só o material original.
			text = extra + "\n\n---\n\n" + text
			rep.Sources = append(rep.Sources, extraSrc...)
		}
	}

	commands := extractCommands(text)
	manifests := extractManifests(text)

	// Fallback de tópicos: o material só LISTA temas de estudo (ex.: README de
	// GitHub sem manifests nem comandos)? Extraímos os títulos/bullets e vamos
	// buscar cada tema na documentação OFICIAL (kubernetes.io apenas).
	if len(commands) == 0 && len(manifests) == 0 {
		if urls := topicDocURLs(text, 3); len(urls) > 0 {
			var fetched []string
			for _, u := range urls {
				if content, err := fetchURL(u); err == nil && len(content) > 100 {
					fetched = append(fetched, markSource(u, content))
					rep.Sources = append(rep.Sources, u)
				}
			}
			if len(fetched) > 0 {
				rep.Notes = append(rep.Notes, fmt.Sprintf(
					"o material só lista tópicos de estudo (sem exemplos práticos) — busquei cada tema na documentação oficial (%d página(s))", len(fetched)))
				text = text + strings.Join(fetched, "")
				commands = extractCommands(text)
				manifests = extractManifests(text)
			}
		}
	}

	concepts := extractConcepts(text)
	rep.Commands, rep.Manifests, rep.Concepts = len(commands), len(manifests), len(concepts)
	var qs []models.Question

	// Labs a partir de comandos e manifests
	for _, c := range commands {
		if rep.Labs >= wantLabs {
			break
		}
		if q, ok := labFromCommand(c, cert, topic, level); ok {
			qs = append(qs, q)
			rep.Labs++
		}
	}
	for _, m := range manifests {
		if rep.Labs >= wantLabs {
			break
		}
		if q, ok := labFromManifest(m, cert, topic, level); ok {
			qs = append(qs, q)
			rep.Labs++
		}
	}

	// Quiz — com LLM local disponível, perguntas de compreensão real;
	// heurísticas (cloze + glossário) completam o que faltar.
	if ok, _ := LLMStatus(); ok && rep.Quizzes < wantQuiz {
		llmQs := llmQuizFromDoc(text, cert, topic, wantQuiz-rep.Quizzes)
		qs = append(qs, llmQs...)
		rep.Quizzes += len(llmQs)
		rep.UsedLLM = len(llmQs) > 0
	}
	for _, c := range commands {
		if rep.Quizzes >= wantQuiz {
			break
		}
		if q, ok := quizFromCommand(c.val, cert, topic); ok {
			qs = append(qs, q)
			rep.Quizzes++
		}
	}
	for _, term := range concepts {
		if rep.Quizzes >= wantQuiz {
			break
		}
		qs = append(qs, quizFromConcept(term, cert, topic))
		rep.Quizzes++
	}

	// Material não rendeu labs suficientes? Completa com labs de TEMPLATE do
	// domínio da cert — sempre com setup e validação garantidos.
	if rep.Labs < wantLabs {
		fill := GenerateForCert(cert, level, wantLabs-rep.Labs)
		if len(fill) > 0 {
			qs = append(qs, fill...)
			rep.Labs += len(fill)
			rep.Notes = append(rep.Notes, fmt.Sprintf(
				"o material rendeu poucos labs práticos — completei com %d lab(s) do domínio %s (com ambiente pré-configurado)", len(fill), cert))
		}
	}
	if rep.Labs < wantLabs {
		rep.Missing = append(rep.Missing, fmt.Sprintf(
			"para gerar %d lab(s) preciso de mais material prático: cole trechos com comandos kubectl/helm ou manifests YAML (kind: ...) — encontrei %d comando(s) e %d manifest(s) utilizáveis",
			wantLabs, len(commands), len(manifests)))
	}
	if rep.Quizzes < wantQuiz {
		rep.Missing = append(rep.Missing, fmt.Sprintf(
			"para gerar %d pergunta(s) preciso de mais conteúdo conceitual: cole seções da documentação que expliquem termos do Kubernetes — reconheci %d conceito(s)",
			wantQuiz, len(concepts)))
	}

	if len(qs) == 0 {
		return nil, rep, fmt.Errorf("nenhum conteúdo utilizável detectado — cole uma URL (kubernetes.io ou GitHub), um trecho com comandos/manifests, ou descreva o tema (ex.: \"init containers\")")
	}
	qs = FinalizeLabs(qs, text)
	if err := persist(qs); err != nil {
		return nil, rep, err
	}
	for _, q := range qs {
		rep.Generated = append(rep.Generated, q.ID)
	}
	return qs, rep, nil
}

// ── extração ────────────────────────────────────────────────────────────────

func extractCommands(text string) []snippet {
	seen := map[string]bool{}
	var out []snippet
	for _, m := range cmdRe.FindAllStringSubmatchIndex(text, -1) {
		c := strings.TrimSpace(text[m[2]:m[3]])
		// ignora exemplos claramente incompletos ou placeholders pesados
		if strings.Count(c, "<") > 1 || len(strings.Fields(c)) < 3 {
			continue
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, snippet{val: c, source: sourceAt(text, m[2]), line: lineAt(text, m[2])})
		}
	}
	return out
}

func extractManifests(text string) []snippet {
	var out []snippet
	for _, m := range fenceRe.FindAllStringSubmatchIndex(text, -1) {
		block := text[m[2]:m[3]]
		if kindRe.MatchString(block) && strings.Contains(block, "apiVersion") {
			kindOff := m[2] + kindRe.FindStringIndex(block)[0]
			out = append(out, snippet{val: block, source: sourceAt(text, m[2]), line: lineAt(text, kindOff)})
		}
	}
	// texto sem fences: um único manifest solto
	if len(out) == 0 && kindRe.MatchString(text) && strings.Contains(text, "apiVersion") {
		off := kindRe.FindStringIndex(text)[0]
		out = append(out, snippet{val: text, source: sourceAt(text, off), line: lineAt(text, off)})
	}
	return out
}

func extractConcepts(text string) []snippet {
	var out []snippet
	seen := map[string]bool{}
	for term := range glossary {
		if seen[term] {
			continue
		}
		// s? cobre plurais ("ReplicaSets", "Deployments")
		if loc := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(term) + `s?\b`).FindStringIndex(text); loc != nil {
			seen[term] = true
			out = append(out, snippet{val: term, source: sourceAt(text, loc[0]), line: lineAt(text, loc[0])})
		}
	}
	return out
}

// ── geração a partir do material ────────────────────────────────────────────

// setupFor devolve o comando de setup que deixa o recurso PRONTO antes do
// exercício — o lab nasce configurado (o usuário nunca opera sobre o vazio).
func setupFor(res, name string) (string, bool) {
	switch res {
	case "pod", "pods", "po":
		return fmt.Sprintf("kubectl run %s --image=nginx:1.21 --restart=Never 2>/dev/null || true; kubectl wait --for=condition=ready pod/%s --timeout=60s 2>/dev/null || true", name, name), true
	case "deployment", "deploy", "deployments":
		return fmt.Sprintf("kubectl create deployment %s --image=nginx:1.21 --replicas=2 2>/dev/null || true; kubectl rollout status deployment/%s --timeout=90s 2>/dev/null || true", name, name), true
	case "service", "svc", "services":
		return fmt.Sprintf("kubectl create deployment %s-backend --image=nginx:1.21 2>/dev/null || true; kubectl expose deployment %s-backend --name=%s --port=80 2>/dev/null || true", name, name, name), true
	case "configmap", "cm":
		return fmt.Sprintf("kubectl create configmap %s --from-literal=key=value 2>/dev/null || true", name), true
	case "secret":
		return fmt.Sprintf("kubectl create secret generic %s --from-literal=password=s3cr3t 2>/dev/null || true", name), true
	case "namespace", "ns":
		return fmt.Sprintf("kubectl create namespace %s 2>/dev/null || true", name), true
	case "serviceaccount", "sa":
		return fmt.Sprintf("kubectl create serviceaccount %s 2>/dev/null || true", name), true
	}
	return "", false
}

// parseVerbTarget entende também o formato recurso/nome (rollout status deployment/x).
func parseVerbTarget(fields []string) (verb, res, name string) {
	verb = fields[1]
	rest := fields[2:]
	if verb == "rollout" && len(fields) >= 4 { // rollout status deployment/x
		verb, rest = fields[2], fields[3:]
	}
	for _, f := range rest {
		if strings.HasPrefix(f, "-") {
			continue
		}
		if strings.Contains(f, "/") { // deployment/web
			parts := strings.SplitN(f, "/", 2)
			return verb, parts[0], parts[1]
		}
		if res == "" {
			res = f
			continue
		}
		if name == "" && !strings.Contains(f, "=") {
			name = f
			break
		}
	}
	return verb, res, name
}

// labFromCommand vira um lab COMPLETO: se o comando kubectl opera sobre um
// recurso existente (delete/describe/scale/label...), o setup CRIA esse
// recurso antes; a validação confere o efeito real. Outras ferramentas
// (terraform/docker/aws...) viram labs de execução guiada. Toda questão
// cita a FONTE e a linha exata de onde saiu.
func labFromCommand(sn snippet, cert, topic string, level int) (models.Question, bool) {
	cmd := sn.val
	fields := strings.Fields(cmd)
	if len(fields) < 3 {
		return models.Question{}, false
	}

	q := models.Question{
		ID:   newID(),
		Cert: models.Cert(cert), Topic: topic, Type: models.Lab, Difficulty: diffFor(level),
		Question: pickHelp(level,
			fmt.Sprintf("Da documentação: execute a operação abaixo no cluster.\n\n`%s`", cmd),
			fmt.Sprintf("Da documentação: execute a operação abaixo e confira o resultado.\n\n`%s`\n\nDica: leia cada flag antes de rodar.", cmd),
			fmt.Sprintf("Da documentação, vamos praticar:\n\n1. Analise o comando: `%s`\n2. Execute-o no terminal\n3. Confirme o efeito com o comando de consulta da ferramenta", cmd)),
		Hint:          cmd,
		AnswerCommand: cmd,
		Explanation:   "Exercício gerado da sua documentação. Pratique até reproduzir sem olhar a dica." + sn.cite(),
	}
	if strings.HasPrefix(sn.source, "http") {
		q.DocURL = sn.source
		q.DocSection = "Fonte lida pelo tutor"
	}

	// Ferramentas não-kubectl (terraform/docker/aws/gcloud...): lab de
	// execução guiada, sem validação automática de cluster.
	if fields[0] != "kubectl" {
		return q, true
	}

	verb, res, name := parseVerbTarget(fields)

	// Verbos que criam → valida existência (sem setup)
	if verb == "create" || verb == "run" || verb == "expose" || verb == "apply" {
		cres, cname := inferResource(fields)
		if cres != "" && cname != "" {
			q.Goals = []models.Goal{{
				Description: fmt.Sprintf("O recurso **%s/%s** existe no cluster", cres, cname),
				Hint:        pickHelp(level, "", "Execute o comando do enunciado.", "Copie o comando do enunciado exatamente como está e rode no terminal."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get %s %s -o name 2>/dev/null", cres, cname),
					ExpectedContains: cname,
				},
			}}
			q.Teardown = []string{fmt.Sprintf("kubectl delete %s %s --ignore-not-found=true", cres, cname)}
		}
		return q, true
	}

	// Verbos que operam sobre recurso existente → SETUP cria o recurso antes
	if res != "" && name != "" {
		if setupCmd, ok := setupFor(res, name); ok {
			q.Setup = []models.SetupStep{{
				Description: fmt.Sprintf("Preparando o ambiente: criando %s/%s...", res, name),
				Command:     setupCmd,
			}}
			q.Teardown = []string{fmt.Sprintf("kubectl delete %s %s --ignore-not-found=true 2>/dev/null || true", res, name)}
			if res == "service" || res == "svc" {
				q.Teardown = append(q.Teardown, fmt.Sprintf("kubectl delete deployment %s-backend --ignore-not-found=true", name))
			}

			switch verb {
			case "delete":
				q.Goals = []models.Goal{{
					Description: fmt.Sprintf("**%s/%s** não existe mais no cluster", res, name),
					Hint:        pickHelp(level, "", "kubectl delete "+res+" "+name, "Rode o comando do enunciado; confirme com kubectl get "+res+" — deve dizer NotFound."),
					Validation: &models.Validation{
						Command:          fmt.Sprintf("kubectl get %s %s 2>&1", res, name),
						ExpectedContains: "not found",
					},
				}}
			case "scale":
				replicas := "3"
				for _, f := range fields {
					if strings.HasPrefix(f, "--replicas=") {
						replicas = strings.TrimPrefix(f, "--replicas=")
					}
				}
				q.Goals = []models.Goal{{
					Description: fmt.Sprintf("**%s** está com **%s réplicas** prontas", name, replicas),
					Hint:        pickHelp(level, "", "kubectl scale --replicas=N", cmd),
					Validation: &models.Validation{
						Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.status.readyReplicas}' 2>/dev/null", name),
						ExpectedContains: replicas,
					},
				}}
			case "label":
				for _, f := range fields {
					if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") {
						kv := strings.SplitN(f, "=", 2)
						q.Goals = []models.Goal{{
							Description: fmt.Sprintf("**%s/%s** tem a label **%s**", res, name, f),
							Hint:        pickHelp(level, "", "kubectl label ...", cmd),
							Validation: &models.Validation{
								Command:          fmt.Sprintf("kubectl get %s %s -o jsonpath='{.metadata.labels.%s}' 2>/dev/null", res, name, kv[0]),
								ExpectedContains: kv[1],
							},
						}}
						break
					}
				}
			default: // get/describe/logs/rollout status... — o setup garante que há o que inspecionar
				q.Goals = []models.Goal{{
					Description: fmt.Sprintf("Você executou o comando sobre **%s/%s** (recurso preparado pelo lab)", res, name),
					Hint:        pickHelp(level, "", "O recurso já foi criado pelo setup — só executar.", cmd),
					Validation: &models.Validation{
						Command:          fmt.Sprintf("kubectl get %s %s -o name 2>/dev/null", res, name),
						ExpectedContains: name,
					},
				}}
			}
			return q, true
		}
	}

	return q, true
}

// inferResource extrai (tipo, nome) de comandos kubectl comuns.
func inferResource(fields []string) (res, name string) {
	switch fields[1] {
	case "run":
		return "pod", fields[2]
	case "create", "expose":
		if len(fields) >= 4 && !strings.HasPrefix(fields[3], "-") {
			r := fields[2]
			if r == "deployment" || r == "deploy" {
				r = "deployment"
			}
			// expose deployment X --name=Y → o recurso final é o service Y
			if fields[1] == "expose" {
				for _, f := range fields {
					if strings.HasPrefix(f, "--name=") {
						return "service", strings.TrimPrefix(f, "--name=")
					}
				}
				return "", ""
			}
			return r, fields[3]
		}
	}
	return "", ""
}

// labFromManifest vira um lab "crie este recurso" com goals extraídos do YAML.
func labFromManifest(sn snippet, cert, topic string, level int) (models.Question, bool) {
	manifest := sn.val
	kindM := kindRe.FindStringSubmatch(manifest)
	nameM := nameRe.FindStringSubmatch(manifest)
	if kindM == nil || nameM == nil {
		return models.Question{}, false
	}
	kind, name := kindM[1], nameM[1]

	q := models.Question{
		ID:   newID(),
		Cert: models.Cert(cert), Topic: topic, Type: models.Lab, Difficulty: diffFor(level),
		Question: pickHelp(level,
			fmt.Sprintf("Da documentação que você forneceu: crie um **%s** chamado **%s** conforme o manifest de referência (aba SOLUTION).", kind, name),
			fmt.Sprintf("Da documentação que você forneceu: crie o **%s/%s**.\n\nDica: monte o YAML e aplique com `kubectl apply -f`.", kind, name),
			fmt.Sprintf("Da sua documentação, vamos criar um **%s** passo a passo:\n\n1. Crie um arquivo com o manifest abaixo\n2. Aplique: `kubectl apply -f arquivo.yaml`\n3. Confirme: `kubectl get %s %s`\n\n```yaml\n%s\n```", kind, strings.ToLower(kind), name, strings.TrimSpace(manifest))),
		Hint:          fmt.Sprintf("kubectl apply -f - <<EOF\n%s\nEOF", strings.TrimSpace(manifest)),
		AnswerCommand: fmt.Sprintf("kubectl apply -f manifest.yaml  # %s/%s da documentação", kind, name),
		Goals: []models.Goal{{
			Description: fmt.Sprintf("**%s/%s** existe no cluster", kind, name),
			Hint:        pickHelp(level, "", "kubectl apply -f com o manifest da doc.", "O YAML completo está no enunciado/hint — aplique com kubectl apply -f -."),
			Validation: &models.Validation{
				Command:          fmt.Sprintf("kubectl get %s %s -o name 2>/dev/null", strings.ToLower(kind), name),
				ExpectedContains: name,
			},
		}},
		Teardown:    []string{fmt.Sprintf("kubectl delete %s %s --ignore-not-found=true", strings.ToLower(kind), name)},
		Explanation: fmt.Sprintf("Manifest extraído da sua documentação. O kind %s foi criado de forma declarativa — compare o resultado (kubectl get -o yaml) com o manifest original.", kind) + sn.cite(),
	}
	return q, true
}

// quizFromCommand gera um cloze: esconde uma flag do comando real.
func quizFromCommand(cmd, cert, topic string) (models.Question, bool) {
	flags := flagRe.FindAllStringSubmatch(cmd, -1)
	if len(flags) == 0 {
		return models.Question{}, false
	}
	target := flags[rand.IntN(len(flags))][1]
	masked := strings.Replace(cmd, target, "____", 1)

	opts := []string{target}
	for _, f := range flagPool {
		if len(opts) >= 4 {
			break
		}
		if f != target && !strings.Contains(cmd, f) {
			opts = append(opts, f)
		}
	}
	if len(opts) < 4 {
		return models.Question{}, false
	}
	rand.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
	answer := 0
	for i, o := range opts {
		if o == target {
			answer = i
		}
	}

	return models.Question{
		ID:   newID(),
		Cert: models.Cert(cert), Topic: topic, Type: models.MultipleChoice, Difficulty: models.Mid,
		Question:    fmt.Sprintf("Da sua documentação — qual flag completa corretamente o comando?\n\n`%s`", masked),
		Options:     opts,
		Answer:      answer,
		Explanation: fmt.Sprintf("O comando original da documentação é:\n%s", cmd),
	}, true
}

// quizFromConcept gera pergunta "qual conceito corresponde à definição",
// citando a linha exata da fonte onde o termo aparece.
func quizFromConcept(sn snippet, cert, topic string) models.Question {
	term := sn.val
	def := glossary[term]
	opts := []string{term}
	// distratores: outros termos do glossário
	for other := range glossary {
		if len(opts) >= 4 {
			break
		}
		if other != term {
			opts = append(opts, other)
		}
	}
	rand.Shuffle(len(opts), func(i, j int) { opts[i], opts[j] = opts[j], opts[i] })
	answer := 0
	for i, o := range opts {
		if o == term {
			answer = i
		}
	}
	return models.Question{
		ID:   newID(),
		Cert: models.Cert(cert), Topic: topic, Type: models.MultipleChoice, Difficulty: models.Easy,
		Question:    fmt.Sprintf("Este conceito apareceu na sua documentação. Qual termo corresponde à definição?\n\n\"%s\"", def),
		Options:     opts,
		Answer:      answer,
		Explanation: fmt.Sprintf("%s: %s.", term, def) + sn.cite(),
		DocURL: func() string {
			if strings.HasPrefix(sn.source, "http") {
				return sn.source
			}
			return ""
		}(),
	}
}
