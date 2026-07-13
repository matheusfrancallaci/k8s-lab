package tutor

import (
	"estudo-app/internal/models"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "k8slab-rag-test-")
	if err == nil {
		os.Setenv("RAG_DATA_DIR", dir)
	}
	code := m.Run()
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	os.Exit(code)
}

// ── fetch.go ─────────────────────────────────────────────────────────────────

func TestIsTrustedURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://kubernetes.io/docs/concepts/workloads/pods/", true},
		{"https://github.com/user/repo", true},
		{"https://raw.githubusercontent.com/u/r/main/README.md", true},
		{"https://learn.microsoft.com/azure/aks/", true},
		{"https://docs.aws.amazon.com/eks/", true},
		{"https://sub.cloud.google.com/kubernetes", true},
		{"https://www.globo.com/noticias", false},
		{"https://malicious-kubernetes.io.evil.com/x", false},
		{"https://kubernetes.io.phishing.net/", false},
		{"ftp://kubernetes.io/x", false},
		{"not-a-url", false},
	}
	for _, c := range cases {
		if got := isTrustedURL(c.url); got != c.want {
			t.Errorf("isTrustedURL(%q) = %v, quer %v", c.url, got, c.want)
		}
	}
}

func TestCertRelevanceAndComplement(t *testing.T) {
	cksText := "NetworkPolicy, RBAC, secret, seccomp, apparmor e pod security standards são temas de segurança."
	if rel := certRelevance(cksText, "CKS"); rel < 4 {
		t.Errorf("relevância CKS deveria ser >= 4, veio %d", rel)
	}
	if shouldComplement(cksText, "CKS") {
		t.Error("material claramente CKS não deveria pedir complemento")
	}
	podText := "Um pod contém containers. O kubelet no node executa pods. O scheduler decide o node do cluster, e o etcd guarda o estado. Upgrade via kubeadm; drain e cordon no node; kube-proxy roteia; persistent volumes (pv) e dns também."
	if !shouldComplement(podText, "CKS") {
		t.Error("material de CKA deveria disparar complemento quando o usuário quer CKS")
	}
}

func TestHTMLToTextPreservesCode(t *testing.T) {
	html := `<html><main><h1>Título</h1><p>texto</p><pre>kubectl get pods -A</pre></main><footer>lixo</footer></html>`
	out := htmlToText(html)
	if !strings.Contains(out, "```") || !strings.Contains(out, "kubectl get pods -A") {
		t.Errorf("blocos <pre> devem virar fences com o comando preservado; veio: %q", out)
	}
	if strings.Contains(out, "lixo") {
		t.Errorf("conteúdo fora de <main> deveria ser descartado; veio: %q", out)
	}
}

// ── ingest.go — extração ─────────────────────────────────────────────────────

func TestExtractCommands(t *testing.T) {
	text := "Para escalar:\nkubectl scale deployment web --replicas=5\n$ kubectl get pods\nhelm install app ./chart\nnão-comando qualquer\nkubectl x\n"
	cmds := extractCommands(text)
	if len(cmds) != 3 {
		t.Fatalf("esperava 3 comandos, veio %d: %v", len(cmds), cmds)
	}
	if !strings.HasPrefix(cmds[0].val, "kubectl scale") {
		t.Errorf("primeiro comando errado: %q", cmds[0].val)
	}
}

func TestExtractManifests(t *testing.T) {
	text := "```yaml\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: app-config\n```\n"
	ms := extractManifests(text)
	if len(ms) != 1 || !strings.Contains(ms[0].val, "kind: ConfigMap") {
		t.Fatalf("manifest não extraído: %v", ms)
	}
}

func TestExtractConcepts(t *testing.T) {
	text := "O Deployment gerencia ReplicaSets; o kubelet roda em cada nó."
	got := extractConcepts(text)
	want := map[string]bool{"Deployment": true, "ReplicaSet": true, "kubelet": true}
	for _, term := range got {
		delete(want, term.val)
	}
	if len(want) != 0 {
		t.Errorf("conceitos não detectados: %v (detectados: %v)", want, got)
	}
}

// ── ingest.go — geração de labs com setup ────────────────────────────────────

func TestParseVerbTarget(t *testing.T) {
	cases := []struct {
		cmd                  string
		verb, resource, name string
	}{
		{"kubectl delete pod old-pod", "delete", "pod", "old-pod"},
		{"kubectl rollout status deployment/api-deploy", "status", "deployment", "api-deploy"},
		{"kubectl scale deployment web --replicas=5", "scale", "deployment", "web"},
		{"kubectl describe svc my-svc -n prod", "describe", "svc", "my-svc"},
	}
	for _, c := range cases {
		v, r, n := parseVerbTarget(strings.Fields(c.cmd))
		if v != c.verb || r != c.resource || n != c.name {
			t.Errorf("parseVerbTarget(%q) = (%s,%s,%s), quer (%s,%s,%s)",
				c.cmd, v, r, n, c.verb, c.resource, c.name)
		}
	}
}

func TestLabFromCommandDeleteHasSetup(t *testing.T) {
	q, ok := labFromCommand(snippet{val: "kubectl delete pod old-pod --grace-period=0 --force"}, "CKA", "Core Concepts", 2)
	if !ok {
		t.Fatal("lab deveria ser gerado")
	}
	if len(q.Setup) == 0 {
		t.Fatal("lab de delete DEVE ter setup criando o recurso antes")
	}
	if !strings.Contains(q.Setup[0].Command, "kubectl run old-pod") {
		t.Errorf("setup deveria criar o pod old-pod: %q", q.Setup[0].Command)
	}
	if len(q.Goals) == 0 || q.Goals[0].Validation.ExpectedContains != "not found" {
		t.Errorf("goal de delete deve validar ausência (not found): %+v", q.Goals)
	}
}

func TestLabFromCommandScale(t *testing.T) {
	q, _ := labFromCommand(snippet{val: "kubectl scale deployment web --replicas=4"}, "CKA", "Workloads", 2)
	if len(q.Goals) == 0 || q.Goals[0].Validation.ExpectedContains != "4" {
		t.Errorf("goal de scale deve validar readyReplicas=4: %+v", q.Goals)
	}
	if len(q.Setup) == 0 || !strings.Contains(q.Setup[0].Command, "create deployment web") {
		t.Errorf("setup deve criar o deployment: %+v", q.Setup)
	}
}

// ── generator.go ─────────────────────────────────────────────────────────────

func TestGenerateQuestionsAllLevels(t *testing.T) {
	for topic := range templates {
		for level := 1; level <= 3; level++ {
			qs := generateQuestions(topic, "CKA", level, 1)
			if len(qs) != 1 {
				t.Fatalf("%s nível %d: sem questão", topic, level)
			}
			q := qs[0]
			if q.ID == "" || q.Question == "" || len(q.Goals) == 0 {
				t.Errorf("%s nível %d: questão incompleta (id=%q, goals=%d)", topic, level, q.ID, len(q.Goals))
			}
			for i, g := range q.Goals {
				if g.Validation == nil || g.Validation.Command == "" || g.Validation.ExpectedContains == "" {
					t.Errorf("%s nível %d goal %d: validação incompleta", topic, level, i)
				}
			}
			if len(q.Teardown) == 0 {
				t.Errorf("%s: template sem teardown deixa lixo no cluster", topic)
			}
		}
	}
}

func TestGenerateLevel3KeepsStepsOutOfMainTask(t *testing.T) {
	qs := generateQuestions("Workloads", "CKA", 3, 1)
	if len(qs) == 0 || strings.Contains(strings.ToLower(qs[0].Question), "passo a passo") || strings.Contains(strings.ToLower(qs[0].Question), "kubectl ") {
		t.Error("nível 3 deve trazer enunciado passo a passo")
	}
	if len(qs) > 0 && qs[0].Hint == "" {
		t.Error("nivel 3 ainda deve oferecer ajuda na aba Hint")
	}
}

func TestGenerateForCertCKSUsesSecurity(t *testing.T) {
	qs := GenerateForCert("CKS", 2, 3)
	if len(qs) != 3 {
		t.Fatalf("esperava 3 labs, veio %d", len(qs))
	}
	for _, q := range qs {
		if q.Topic != "Security" {
			t.Errorf("labs CKS devem vir do domínio Security, veio %q", q.Topic)
		}
	}
}

func TestDetectTopicHPAAndArgoCD(t *testing.T) {
	if got := detectTopic("criar lab de CKA para HPA"); got != "Autoscaling" {
		t.Fatalf("HPA deveria mapear para Autoscaling, veio %q", got)
	}
	if got := detectTopic("quero um lab de ArgoCD GitOps"); got != "GitOps" {
		t.Fatalf("ArgoCD deveria mapear para GitOps, veio %q", got)
	}
}

func TestHPALabInstallsMetricsServer(t *testing.T) {
	qs := generateQuestions("Autoscaling", "CKA", 2, 1)
	if len(qs) != 1 {
		t.Fatalf("esperava 1 lab HPA, veio %d", len(qs))
	}
	q := qs[0]
	if !strings.Contains(q.Question, "HPA") && !strings.Contains(q.Question, "HorizontalPodAutoscaler") {
		t.Fatalf("lab deveria ser explicitamente de HPA, veio: %q", q.Question)
	}
	if len(q.Setup) == 0 || !strings.Contains(q.Setup[0].Command, "metrics-server") {
		t.Fatalf("lab HPA deve instalar Metrics Server no setup: %+v", q.Setup)
	}
}

func TestArgoCDLabInstallsArgoCD(t *testing.T) {
	qs := generateQuestions("GitOps", "ArgoCD", 2, 1)
	if len(qs) != 1 {
		t.Fatalf("esperava 1 lab GitOps, veio %d", len(qs))
	}
	q := qs[0]
	if len(q.Setup) == 0 || !strings.Contains(q.Setup[0].Command, "argocd-server") {
		t.Fatalf("lab ArgoCD deve instalar/verificar ArgoCD no setup: %+v", q.Setup)
	}
	if len(q.Goals) == 0 || !strings.Contains(q.Goals[0].Validation.Command, "application") {
		t.Fatalf("lab ArgoCD deve validar Application: %+v", q.Goals)
	}
}

func TestGenericAWSLabUsesAWSFoundationTrack(t *testing.T) {
	msg := "crie um lab de AWS para mim"
	cert := inferCertFromMessage(msg, "CKA")
	if cert != "AWS" || !isAWSFocus(cert, msg) {
		t.Fatalf("pedido AWS deveria ignorar chip ativo CKA, cert=%q", cert)
	}
	topics := fallbackTopicsForCert(cert, msg)
	if len(topics) != 5 {
		t.Fatalf("pedido AWS generico deve mapear 5 topicos iniciais, veio %d: %v", len(topics), topics)
	}
	want := map[string]bool{"AWS Compute": true, "AWS Networking": true, "AWS IAM": true, "AWS Storage": true, "AWS Messaging": true}
	for _, topic := range topics {
		delete(want, topic)
		qs := generateQuestions(topic, cert, 2, 1)
		if len(qs) != 1 {
			t.Fatalf("topico AWS %q deveria gerar lab", topic)
		}
		if qs[0].Cert != "AWS" || qs[0].Topic != topic {
			t.Fatalf("lab AWS nao deveria herdar chip ativo CKA: %+v", qs[0])
		}
	}
	if len(want) != 0 {
		t.Fatalf("faltaram topicos AWS iniciais: %v", want)
	}
}

func TestExactCKAHPARoutesToAutoscaling(t *testing.T) {
	msg := "criar questao da CKA de HPA"
	if got := exactTopicForRequest("CKA", msg); got != "Autoscaling" {
		t.Fatalf("pedido CKA/HPA deve mapear exatamente para Autoscaling, veio %q", got)
	}
	qs := generateQuestions("Autoscaling", "CKA", 3, 1)
	if len(qs) != 1 {
		t.Fatalf("pedido CKA/HPA deveria gerar lab de Autoscaling, veio %d", len(qs))
	}
	hpaText := qs[0].Question + " " + qs[0].AnswerCommand
	if qs[0].LabSpec != nil {
		hpaText += " " + qs[0].LabSpec.Objective
	}
	if qs[0].Topic != "Autoscaling" || (!strings.Contains(hpaText, "HPA") && !strings.Contains(hpaText, "HorizontalPodAutoscaler")) {
		t.Fatalf("lab deveria ser HPA/Autoscaling, veio: %+v", qs[0])
	}
}

// ── citações e fallback de tópicos ───────────────────────────────────────────

func TestRouteCertForLabRequestUsesPromptCert(t *testing.T) {
	cases := []struct {
		active string
		msg    string
		topic  string
		want   string
	}{
		{"CKA", "crie um lab da CAPA sobre ArgoCD sync", "GitOps", "CAPA"},
		{"CKA", "crie um lab de ArgoCD sync", "GitOps", "ArgoCD"},
		{"CKA", "crie um lab de AWS para SQS", "AWS Messaging", "AWS"},
		{"CKA", "crie um lab de SQS", "AWS Messaging", "AWS"},
		{"CKA", "criar questao da CKA de HPA", "Autoscaling", "CKA"},
	}
	for _, c := range cases {
		if got := routeCertForLabRequest(c.active, c.msg, c.topic); got != c.want {
			t.Fatalf("routeCertForLabRequest(%q,%q,%q)=%q, want %q", c.active, c.msg, c.topic, got, c.want)
		}
	}
}

func TestExactAWSMessagingWorksWithoutAWSWord(t *testing.T) {
	msg := "crie um lab de SQS"
	cert := routeCertForLabRequest("CKA", msg, "")
	if cert != "AWS" {
		t.Fatalf("SQS sozinho deve ser reconhecido como AWS, veio %q", cert)
	}
	if got := exactTopicForRequest(cert, msg); got != "AWS Messaging" {
		t.Fatalf("SQS deve mapear para AWS Messaging, veio %q", got)
	}
}

func TestAWSStorageUsesLocalStackS3(t *testing.T) {
	qs := generateQuestions("AWS Storage", "AWS", 2, 1)
	if len(qs) != 1 {
		t.Fatalf("esperava 1 lab AWS Storage, veio %d", len(qs))
	}
	q := qs[0]
	if len(q.Setup) == 0 || !strings.Contains(q.Setup[0].Command, "localstack") {
		t.Fatalf("lab AWS Storage deve instalar/verificar LocalStack no setup: %+v", q.Setup)
	}
	if !strings.Contains(q.AnswerCommand, "awslocal s3") {
		t.Fatalf("lab AWS Storage deve usar API S3 via awslocal: %q", q.AnswerCommand)
	}
	for _, g := range q.Goals {
		if g.Validation == nil || !strings.Contains(g.Validation.Command, "awslocal") {
			t.Fatalf("validacoes AWS Storage devem consultar LocalStack: %+v", q.Goals)
		}
	}
}

func TestAWSMessagingUsesLocalStackSQS(t *testing.T) {
	qs := generateQuestions("AWS Messaging", "AWS", 2, 1)
	if len(qs) != 1 {
		t.Fatalf("esperava 1 lab AWS Messaging, veio %d", len(qs))
	}
	q := qs[0]
	if len(q.Setup) == 0 || !strings.Contains(q.Setup[0].Command, "localstack") {
		t.Fatalf("lab AWS Messaging deve instalar/verificar LocalStack no setup: %+v", q.Setup)
	}
	if !strings.Contains(q.AnswerCommand, "awslocal sqs") {
		t.Fatalf("lab AWS Messaging deve usar API SQS via awslocal: %q", q.AnswerCommand)
	}
}

func TestLabAgentAddsSpecAndQualityGate(t *testing.T) {
	qs := generateQuestions("Autoscaling", "CKA", 2, 1)
	if len(qs) != 1 {
		t.Fatalf("esperava 1 lab, veio %d", len(qs))
	}
	q := qs[0]
	if q.LabSpec == nil {
		t.Fatal("lab gerado deve carregar LabSpec")
	}
	if q.LabSpec.Quality.Score < minimumLabQuality {
		t.Fatalf("score abaixo do gate: %+v", q.LabSpec.Quality)
	}
	if q.LabSpec.EvidenceScore < 70 || len(q.LabSpec.Evidence) == 0 {
		t.Fatalf("lab deve ter evidencia curricular forte: score=%d evidence=%+v", q.LabSpec.EvidenceScore, q.LabSpec.Evidence)
	}
	if !strings.Contains(strings.ToLower(q.LabSpec.Evidence[0].Domain), "workloads") {
		t.Fatalf("HPA deve ancorar em Workloads/Scheduling, veio %+v", q.LabSpec.Evidence)
	}
	if len(q.LabSpec.Dependencies) == 0 || q.LabSpec.Dependencies[0].Name == "" {
		t.Fatalf("LabSpec deve declarar dependencias: %+v", q.LabSpec.Dependencies)
	}
	if err := LabQualityGate(q); err != nil {
		t.Fatalf("lab de template deve passar quality gate: %v", err)
	}
}

func TestRAGSearchAnchorsCKAHPA(t *testing.T) {
	hits := RAGSearch("CKA", "criar questao da CKA de HPA autoscaling CPU", 3, false)
	if len(hits) == 0 {
		t.Fatal("RAG deveria recuperar chunks para HPA/CKA")
	}
	found := false
	for _, h := range hits {
		if strings.Contains(strings.ToLower(h.Chunk.Domain), "workloads") {
			found = true
		}
		if h.Relevance <= 0 {
			t.Fatalf("chunk deve ter relevancia positiva: %+v", h)
		}
	}
	if !found {
		t.Fatalf("HPA deveria ancorar em Workloads & Scheduling, hits=%+v", hits)
	}
}

func TestRAGEmbeddingsArePersistentChunks(t *testing.T) {
	t.Setenv("OLLAMA_URL", "http://127.0.0.1:1")
	idx := &ragIndex{Cert: "CKA", BuiltAt: time.Now(), Chunks: syntheticRAGChunks("CKA")}
	if !ensureRAGEmbeddings(idx) {
		t.Fatal("chunks sem embedding deveriam ser preenchidos")
	}
	if len(idx.Chunks) == 0 || len(idx.Chunks[0].Embedding) == 0 {
		t.Fatalf("embedding nao persistido no chunk: %+v", idx.Chunks)
	}
	if idx.Chunks[0].EmbeddingModel == "" {
		t.Fatalf("modelo de embedding deve ser registrado")
	}
	a := localEmbedding("hpa autoscaling workloads")
	if sim := cosineDense(a, a); sim < .99 {
		t.Fatalf("similaridade do embedding consigo mesmo deveria ser ~1, veio %.3f", sim)
	}
}

func TestLabSpecCarriesRAGChunks(t *testing.T) {
	qs := generateQuestions("Autoscaling", "CKA", 3, 1)
	if len(qs) != 1 || qs[0].LabSpec == nil {
		t.Fatalf("lab deveria ser gerado com LabSpec: %+v", qs)
	}
	if len(qs[0].LabSpec.Chunks) == 0 {
		t.Fatalf("LabSpec deveria carregar chunks RAG: %+v", qs[0].LabSpec)
	}
	if qs[0].LabSpec.Quality.Score < minimumLabQuality {
		t.Fatalf("chunks RAG nao deveriam derrubar quality gate: %+v", qs[0].LabSpec.Quality)
	}
}

func TestLabObservabilityTracksFailuresAndTopics(t *testing.T) {
	t.Setenv("LAB_OBSERVABILITY_PATH", filepath.Join(t.TempDir(), "labs.json"))
	resetLabObservabilityForTest()
	user := "unit-observability"
	q := generateQuestions("Autoscaling", "CKA", 2, 1)[0]

	RecordLabValidation(user, q, 0, "kubectl get hpa", false, "Error from server (Forbidden)")
	RecordLabValidation(user, q, 0, "kubectl get hpa", true, "OK")
	RecordLabSetup(user, q, "kubectl apply -f metrics-server.yaml", "warn", "forbidden")
	SetActiveQuestion(user, q)
	RecordTermErrorText(user, "Error from server (Forbidden): hpa")

	got := LabObservability()
	if got.Attempts != 2 || got.Successes != 1 || got.Failures != 1 {
		t.Fatalf("observabilidade deveria contar validacoes, veio %+v", got)
	}
	if got.SuccessRate < .49 || got.SuccessRate > .51 {
		t.Fatalf("taxa de sucesso esperada 50%%, veio %.2f", got.SuccessRate)
	}
	if len(got.TopFailures) == 0 || len(got.StuckTopics) == 0 {
		t.Fatalf("deveria expor falhas e topicos travados: %+v", got)
	}
}

func TestPromptQualityRanksRealPrompts(t *testing.T) {
	t.Setenv("PROMPT_QUALITY_PATH", filepath.Join(t.TempDir(), "quality.json"))
	resetPromptQualityForTest()

	qs := generateQuestions("Autoscaling", "CKA", 2, 1)
	RecordPromptQuality("alice", "criar questao da CKA de HPA nivel 3", "CKA", ChatResult{
		Reply:     "lab criado",
		Action:    sessionAction("s1", qs[0].ID, len(qs), qs),
		Questions: qs,
	})
	RecordPromptQuality("bob", "crie um lab muito generico de XPTO", "CKA", ChatResult{
		Reply: "nao consegui mapear",
	})

	rep := PromptQualityReport()
	if rep.Total != 2 {
		t.Fatalf("dataset deveria ter 2 prompts, veio %+v", rep)
	}
	if len(rep.Weakest) == 0 || !strings.Contains(rep.Weakest[0].Prompt, "XPTO") {
		t.Fatalf("ranking deveria colocar prompt ruim primeiro: %+v", rep.Weakest)
	}
	if rep.Weakest[0].UserHash == "" || strings.Contains(rep.Weakest[0].UserHash, "bob") {
		t.Fatalf("usuario deve ser salvo como hash, veio %+v", rep.Weakest[0])
	}
	hist := HistoricalRegressionPrompts(5)
	if len(hist) != 1 || hist[0].ActionType != "session" {
		t.Fatalf("regressao historica deve usar apenas prompts com sessao: %+v", hist)
	}
}

func TestGoldenEvalIncludesHistoricalRegression(t *testing.T) {
	t.Setenv("PROMPT_QUALITY_PATH", filepath.Join(t.TempDir(), "quality.json"))
	resetPromptQualityForTest()

	qs := generateQuestions("AWS Messaging", "AWS", 2, 1)
	RecordPromptQuality("alice", "crie um lab de AWS para SQS", "CKA", ChatResult{
		Reply:     "lab criado",
		Action:    sessionAction("s1", qs[0].ID, len(qs), qs),
		Questions: qs,
	})

	rep := RunGoldenEval()
	if rep.RegressionTotal < 1 || len(rep.RegressionCases) < 1 {
		t.Fatalf("golden eval deve anexar regressao historica: %+v", rep)
	}
	if rep.RegressionScore < 75 {
		t.Fatalf("regressao historica deveria passar para AWS SQS, veio %+v", rep.RegressionCases)
	}
	if rep.Quality.Total != 1 {
		t.Fatalf("eval deve embutir resumo de qualidade: %+v", rep.Quality)
	}
}

func TestGoldenEvalPassesCorePrompts(t *testing.T) {
	t.Setenv("OLLAMA_URL", "http://127.0.0.1:1")
	rep := RunGoldenEval()
	if rep.Total < 4 {
		t.Fatalf("golden eval deveria cobrir CKA, AWS, ArgoCD/CAPA e Terraform: %+v", rep)
	}
	if rep.Score < 75 {
		t.Fatalf("golden eval abaixo do minimo: %+v", rep)
	}
}

func TestReviewQueueAndDomainMap(t *testing.T) {
	user := "unit-rag-review"
	p := profileFor(user)
	p.mu.Lock()
	p.Skills = map[string]*TopicSkill{}
	p.Review = map[string]*ReviewItem{}
	if p.saveTimer != nil {
		p.saveTimer.Stop()
	}
	p.mu.Unlock()
	t.Cleanup(func() {
		p.mu.Lock()
		if p.saveTimer != nil {
			p.saveTimer.Stop()
		}
		p.mu.Unlock()
	})

	q := generateQuestions("Autoscaling", "CKA", 2, 1)[0]
	RecordGoal(user, q, false)
	queue := ReviewQueue(user)
	if len(queue) == 0 || !queue[0].Ready || queue[0].Topic != "Autoscaling" {
		t.Fatalf("falha deveria entrar pronta no caderno de erros: %+v", queue)
	}
	domains := DomainMap(user, "CKA")
	found := false
	for _, d := range domains {
		if strings.Contains(strings.ToLower(d.Domain), "workloads") {
			found = true
			if d.Attempts == 0 || d.DueReviews == 0 {
				t.Fatalf("mapa de dominio deveria refletir tentativa e revisao: %+v", d)
			}
		}
	}
	if !found {
		t.Fatalf("mapa CKA deveria incluir Workloads: %+v", domains)
	}

	RecordGoal(user, q, true)
	queue = ReviewQueue(user)
	if len(queue) == 0 || !queue[0].Due.After(time.Now()) {
		t.Fatalf("acerto apos erro deveria espacacar revisao futura: %+v", queue)
	}
}

func TestSessionActionCarriesAgentMetadata(t *testing.T) {
	qs := generateQuestions("AWS Messaging", "AWS", 2, 1)
	a := sessionAction("s1", qs[0].ID, len(qs), qs)
	if a.Quality < minimumLabQuality {
		t.Fatalf("action deve expor qualidade do pacote, veio %d", a.Quality)
	}
	if len(a.Dependencies) == 0 || a.Dependencies[0] != "localstack" {
		t.Fatalf("action deve expor dependencia localstack: %+v", a.Dependencies)
	}
	if len(a.Sources) == 0 {
		t.Fatalf("action deve expor fontes oficiais")
	}
	if len(a.Evidence) == 0 {
		t.Fatalf("action deve expor evidencia curricular")
	}
	if len(a.Chunks) == 0 {
		t.Fatalf("action deve expor chunks RAG")
	}
}

func TestEvidenceContextAnchorsGenericAWSRequest(t *testing.T) {
	ctx := EvidenceContext("AWS", "", "crie um lab de AWS para SQS e IAM", 3)
	lower := strings.ToLower(ctx)
	if !strings.Contains(lower, "messaging") || !strings.Contains(lower, "security") {
		t.Fatalf("contexto RAG deve ancorar pedido AWS em dominios oficiais, veio: %s", ctx)
	}
	if !strings.Contains(ctx, "docs.aws.amazon.com") {
		t.Fatalf("contexto RAG deve incluir fonte oficial AWS, veio: %s", ctx)
	}
}

func TestLabAgentBlocksDangerousSetupCommands(t *testing.T) {
	if got := BlockedLabCommandReason("rm -rf /"); got == "" {
		t.Fatal("deveria bloquear rm -rf /")
	}
	if got := BlockedLabCommandReason("kubectl get pods"); got != "" {
		t.Fatalf("nao deveria bloquear comando kubectl seguro: %s", got)
	}
}

func TestGenerateQuestionsAvoidsTemplateRepeatWhenPossible(t *testing.T) {
	qs := generateQuestions("Services", "CKA", 2, 2)
	if len(qs) != 2 {
		t.Fatalf("esperava 2 labs de Services, veio %d", len(qs))
	}
	if qs[0].DocURL == qs[1].DocURL {
		t.Fatalf("quando ha templates suficientes, nao deve repetir o mesmo template: %#v / %#v", qs[0].DocURL, qs[1].DocURL)
	}
}

func TestCitationTracksSourceAndLine(t *testing.T) {
	text := markSource("https://kubernetes.io/docs/x/", "Para escalar um Deployment:\nkubectl scale deployment web --replicas=5\n")
	cmds := extractCommands(text)
	if len(cmds) != 1 {
		t.Fatalf("esperava 1 comando, veio %d", len(cmds))
	}
	if cmds[0].source != "https://kubernetes.io/docs/x/" {
		t.Errorf("fonte errada: %q", cmds[0].source)
	}
	if !strings.Contains(cmds[0].line, "kubectl scale deployment web") {
		t.Errorf("linha exata não capturada: %q", cmds[0].line)
	}
	q, _ := labFromCommand(cmds[0], "CKA", "Workloads", 2)
	if !strings.Contains(q.Explanation, "📍") || !strings.Contains(q.Explanation, "kubernetes.io/docs/x") {
		t.Errorf("explicação deve citar fonte + linha: %q", q.Explanation)
	}
	if q.DocURL != "https://kubernetes.io/docs/x/" {
		t.Errorf("DocURL deve apontar para a fonte: %q", q.DocURL)
	}
}

func TestTopicDocURLsFromStudyList(t *testing.T) {
	readme := "# Guia de estudos CKA\n\n## Tópicos\n- Init Containers\n- Persistent Volumes e storage\n* Network Policies\n"
	urls := topicDocURLs(readme, 3)
	if len(urls) < 2 {
		t.Fatalf("deveria achar 2+ docs oficiais para os tópicos, veio %v", urls)
	}
	for _, u := range urls {
		if !strings.HasPrefix(u, "https://kubernetes.io/") {
			t.Errorf("fallback só pode usar doc OFICIAL: %q", u)
		}
	}
}

func TestGenericToolCommandBecomesLab(t *testing.T) {
	cmds := extractCommands("Para provisionar:\nterraform apply -auto-approve\n")
	if len(cmds) != 1 {
		t.Fatalf("terraform deveria ser extraído: %v", cmds)
	}
	q, ok := labFromCommand(cmds[0], "Terraform", "IaC", 2)
	if !ok || q.AnswerCommand == "" {
		t.Error("comando de outra ferramenta deve virar lab de execução guiada")
	}
	if len(q.Goals) != 0 {
		t.Error("labs não-kubectl não devem ter validação automática de cluster")
	}
}

func TestGeneratedLabMainTaskDoesNotRevealCommands(t *testing.T) {
	qs := generateQuestions("Autoscaling", "CKA", 3, 1)
	if len(qs) != 1 {
		t.Fatalf("esperava um lab")
	}
	q := qs[0]
	if strings.Contains(strings.ToLower(q.Question), "kubectl ") {
		t.Fatalf("enunciado principal nao deve entregar comando kubectl: %q", q.Question)
	}
	if q.AnswerCommand == "" || !strings.Contains(q.AnswerCommand, "kubectl") {
		t.Fatalf("gabarito deve continuar disponivel na Solution: %q", q.AnswerCommand)
	}
}

func TestExactReplicaSetAndFamilyTopics(t *testing.T) {
	cases := []struct {
		msg  string
		want string
	}{
		{"crie um lab voltado para k8s sobre o topico replicaset", "ReplicaSet"},
		{"crie um lab de k8s sobre scaleset", "ReplicaSet"},
		{"crie um lab linux com chmod e logs", "Linux"},
		{"crie um lab java para programar algo", "Java"},
		{"crie um lab bash com argumentos", "Bash"},
		{"Criar lab de pod estaticos", "Static Pods"},
	}
	for _, tc := range cases {
		if got := exactTopicForRequest("CKA", tc.msg); got != tc.want {
			t.Fatalf("%q deveria mapear para %s, veio %s", tc.msg, tc.want, got)
		}
	}
}

func TestStaticPodRequestCreatesExactSession(t *testing.T) {
	t.Setenv("QUESTIONS_CUSTOM_DIR", t.TempDir())
	res := Chat("alice", "Criar lab de pod estaticos", "CKA", func(ids []string) (string, string, int) {
		return "session-static", ids[0], len(ids)
	})
	if res.Action == nil || res.Action.Type != "session" || res.Action.First == "" {
		t.Fatalf("pedido explicito deveria criar sessao: %+v", res)
	}
	if len(res.Questions) == 0 || res.Questions[0].Topic != "Static Pods" {
		t.Fatalf("lab deveria ser aderente a Static Pods: %+v", res.Questions)
	}
	if !strings.Contains(strings.ToLower(res.Questions[0].Explanation), "staticpodpath") {
		t.Fatalf("lab deve ensinar o mecanismo real de static pods: %+v", res.Questions[0])
	}
	if err := LabQualityGate(res.Questions[0]); err != nil {
		t.Fatalf("lab de Static Pods deve passar no gate de qualidade: %v", err)
	}
}

func TestFamilyLabsAreRealHandsOn(t *testing.T) {
	for _, topic := range []string{"Linux", "Bash", "Java", "ReplicaSet", "Helm", "Docker"} {
		qs := generateQuestions(topic, topic, 2, 1)
		if len(qs) != 1 {
			t.Fatalf("%s deveria gerar um lab", topic)
		}
		q := qs[0]
		if q.Type != models.Lab || len(q.Goals) == 0 {
			t.Fatalf("%s deveria ser lab com goals: %+v", topic, q)
		}
		if q.AnswerCommand == "" {
			t.Fatalf("%s deve ter gabarito executavel na aba Solution", topic)
		}
		if strings.Contains(strings.ToLower(q.Question), strings.ToLower(q.AnswerCommand)) {
			t.Fatalf("%s vazou AnswerCommand no enunciado", topic)
		}
	}
}

func TestLabCompilerAlignsExplicitNamespace(t *testing.T) {
	q, ok := labFromCommand(snippet{val: "kubectl scale deployment web --replicas=4 -n prod"}, "CKA", "Workloads", 2)
	if !ok {
		t.Fatal("comando kubectl deveria virar lab")
	}
	q = FinalizeLab(q, "")
	if q.LabSpec == nil || q.LabSpec.Namespace != "prod" {
		t.Fatalf("LabSpec deveria registrar namespace prod: %+v", q.LabSpec)
	}
	if len(q.Setup) == 0 || !strings.Contains(q.Setup[0].Command, "-n prod") {
		t.Fatalf("setup deveria criar recurso no namespace prod: %+v", q.Setup)
	}
	if len(q.Goals) == 0 || q.Goals[0].Validation == nil || !strings.Contains(q.Goals[0].Validation.Command, "-n prod") {
		t.Fatalf("validacao deveria procurar no namespace prod: %+v", q.Goals)
	}
	if q.LabSpec.LabPlan == nil || q.LabSpec.LabPlan.SourceVersion == "" {
		t.Fatalf("LabPlan deveria carregar versionamento/fonte: %+v", q.LabSpec.LabPlan)
	}
	if err := LabDeliveryPreflight(q); err != nil {
		t.Fatalf("lab compilado deveria passar preflight: %v", err)
	}
}

func TestLabQualityPenalizesExitCodeOnlyValidation(t *testing.T) {
	base := models.Question{
		Type: models.Lab, Cert: models.CKA, Topic: "Workloads",
		Question:      "Crie um deployment e comprove de forma automatica que ele ficou disponivel.",
		AnswerCommand: "kubectl create deployment web --image=nginx:1.25",
		Teardown:      []string{"kubectl delete deployment web"},
	}
	exitOnly := base
	exitOnly.Goals = []models.Goal{{Description: "Deployment existe", Validation: &models.Validation{Command: "kubectl get deployment web"}}}
	verifiable := base
	verifiable.Goals = []models.Goal{{Description: "Deployment existe", Validation: &models.Validation{Command: "kubectl get deployment web -o name", ExpectedOutput: "deployment.apps/web"}}}

	exitOnly = FinalizeLab(exitOnly, "lab CKA de deployment")
	verifiable = FinalizeLab(verifiable, "lab CKA de deployment")
	if exitOnly.LabSpec.Quality.Score >= verifiable.LabSpec.Quality.Score {
		t.Fatalf("validacao por exit code deveria pontuar menos: exit=%d verificavel=%d", exitOnly.LabSpec.Quality.Score, verifiable.LabSpec.Quality.Score)
	}
	if !strings.Contains(strings.Join(exitOnly.LabSpec.Quality.Warnings, " "), "exit code") {
		t.Fatalf("warning deveria explicar validacao ambigua: %+v", exitOnly.LabSpec.Quality.Warnings)
	}
}

func TestPreflightAllowsCommandWordTopic(t *testing.T) {
	// Regressão: quando o TÓPICO é um comando (bash/java/terraform) a palavra
	// aparece na prosa do enunciado, e o HideLabSpoilers deixa `comando
	// apropriado` entre crases. O preflight NÃO pode confundir isso com um
	// comando pronto — senão nenhum lab desses temas é entregue.
	for _, topic := range []string{"bash", "java", "terraform"} {
		q := FinalizeLab(models.Question{
			Type:     models.Lab,
			Topic:    topic,
			Question: "Automatize a rotina de " + topic + " descrita nos goals.",
			Goals: []models.Goal{{
				Description: "Recurso criado e validado",
				Validation:  &models.Validation{Command: "kubectl get pods -n lab-x"},
			}},
			AnswerCommand: "echo ok",
			Teardown:      []string{"kubectl delete ns lab-x"},
		}, "Crie um lab de "+topic)
		if err := LabDeliveryPreflight(q); err != nil {
			t.Fatalf("lab de %s deveria passar preflight apos sanitizacao: %v\nenunciado: %q", topic, err, q.Question)
		}
	}
}

func TestHideLabSpoilersCutsEnglishHeadings(t *testing.T) {
	// Regressão: o modelo de geração escreve o gabarito sob cabeçalho em inglês
	// ("SOLUTION:", "Answer:"). O corte só reconhecia PT, então a resposta
	// inteira vazava para o enunciado do lab.
	for _, heading := range []string{"SOLUTION:", "Solution:", "Answer:", "Steps:", "Step-by-step:", "Hint:", "Solucao:", "Gabarito:"} {
		q := HideLabSpoilers(models.Question{
			Type:     models.Lab,
			Topic:    "Services",
			Question: "Exponha o Deployment titan-cache com um Service ClusterIP.\n\n" + heading + "\nkubectl expose deploy titan-cache --port=80",
		})
		if strings.Contains(q.Question, "kubectl expose") {
			t.Fatalf("gabarito vazou apos o cabecalho %q:\n%s", heading, q.Question)
		}
	}
}

func TestPreflightBlocksInlineCommandInStatement(t *testing.T) {
	// Um comando REAL entre crases no enunciado continua barrado.
	if !questionHasReadyCommand("Rode `kubectl apply -f pod.yaml` para criar o pod") {
		t.Fatal("comando pronto entre crases deveria ser detectado")
	}
	// O placeholder redigido NÃO é um comando pronto.
	if questionHasReadyCommand("Crie o recurso com o `comando apropriado` no terminal") {
		t.Fatal("placeholder redigido nao deveria contar como comando pronto")
	}
	// Palavra-comando na prosa, sem crase ao redor, tambem nao conta.
	if questionHasReadyCommand("Pratique bash e valide pelos goals") {
		t.Fatal("palavra-comando solta na prosa nao deveria contar como comando pronto")
	}
}

func TestLearningPathGeneratesProgressiveLabs(t *testing.T) {
	path := BuildLearningPath("trilha de CKA para HPA", "CKA")
	var qs []models.Question
	for _, topic := range path.Topics {
		qs = append(qs, generateQuestions(topic, path.Cert, 2, 1)...)
	}
	if len(qs) < 3 {
		t.Fatalf("trilha deveria ter 3+ labs, veio %d / %+v", len(qs), path)
	}
	if path.Topic != "Autoscaling" {
		t.Fatalf("trilha deveria focar Autoscaling, veio %+v", path)
	}
	for _, q := range qs {
		if q.LabSpec == nil || q.LabSpec.LabPlan == nil {
			t.Fatalf("todo lab da trilha deve ter LabPlan: %+v", q)
		}
	}
}

func TestAdminQualityAndDeployGateReports(t *testing.T) {
	admin := BuildAdminQualityReport()
	if admin.GeneratedAt == "" || admin.GoldenTotal == 0 || len(admin.Topics) == 0 {
		t.Fatalf("painel admin incompleto: %+v", admin)
	}
	gate := RunDeployGate()
	if gate.GeneratedAt == "" || len(gate.Checks) == 0 {
		t.Fatalf("deploy gate incompleto: %+v", gate)
	}
}

func TestDefaultLabCountIsPracticeSessionSized(t *testing.T) {
	if got := detectCount("crie um lab de linux", 5); got != 5 {
		t.Fatalf("default de lab deveria ser 5, veio %d", got)
	}
	if got := detectCount("crie 9 labs de java", 5); got != 9 {
		t.Fatalf("contagem explicita deveria ser respeitada, veio %d", got)
	}
}

func TestGroundedPromptRequiresSourcesAndInferenceLabels(t *testing.T) {
	prompt := buildChatPrompt("explique frobnicator mesh xpto em Kubernetes")
	if !strings.Contains(prompt, "REGRAS ANTI-ALUCINACAO") {
		t.Fatalf("prompt deve carregar regras anti-alucinacao: %s", prompt)
	}
	if !strings.Contains(prompt, "Fontes:") || !strings.Contains(prompt, "Inferencia:") {
		t.Fatalf("prompt deve exigir fontes e marcar inferencia: %s", prompt)
	}
}

func TestFreeChatRefusesUnknownInfraWithoutEvidence(t *testing.T) {
	reply, err := llmChatReply("explique frobnicator mesh xpto em Kubernetes")
	if err != nil {
		t.Fatalf("recusa fundamentada nao deveria depender do LLM: %v", err)
	}
	if !strings.Contains(strings.ToLower(reply), "nao encontrei evidencia suficiente") {
		t.Fatalf("pergunta tecnica sem evidencia deveria ser recusada, veio: %s", reply)
	}
}

func TestSpecificUnknownLabDoesNotFallbackToGeneric(t *testing.T) {
	qs, _, err := GenerateSmartLabs("crie um lab de k8s sobre frobnicator mesh xpto", "CKA", 2, 5)
	if err == nil {
		t.Fatalf("pedido especifico desconhecido nao deveria virar lab generico: %+v", qs)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "generico") && !strings.Contains(strings.ToLower(err.Error()), "confiavel") {
		t.Fatalf("erro deveria explicar falta de evidencia/template, veio: %v", err)
	}
}

func TestReplicaSetSpecificLabPassesAdherenceGate(t *testing.T) {
	msg := "crie um lab voltado para k8s sobre o topico replicaset"
	qs := generateQuestions("ReplicaSet", "CKA", 2, 1)
	if len(qs) != 1 {
		t.Fatalf("esperava 1 lab, veio %d", len(qs))
	}
	if err := LabRequestAdherence(qs[0], msg); err != nil {
		t.Fatalf("lab ReplicaSet deveria aderir ao pedido exato: %v", err)
	}
}

func TestCurriculumEmbedded(t *testing.T) {
	for _, cert := range []string{"CKA", "CKAD", "CKS", "CAPA", "AWS", "Linux", "Bash", "Java"} {
		cur, ok := CurriculumFor(cert)
		if !ok || len(cur) < 3 {
			t.Errorf("%s deveria ter currículo embutido com 3+ domínios", cert)
		}
		total := 0
		for _, d := range cur {
			total += d.Weight
			if len(d.URLs) == 0 {
				t.Errorf("%s/%s sem URLs oficiais", cert, d.Domain)
			}
		}
		if total != 100 {
			t.Errorf("%s: pesos somam %d, deveriam somar 100", cert, total)
		}
	}
}

func TestBareCertificationRequestAsksForDomainBeforeGenerating(t *testing.T) {
	created := false
	res := Chat("learner", "Crie um lab para CKA", "CKA", func(ids []string) (string, string, int) {
		created = true
		return "", "", 0
	})
	if created || len(res.Questions) != 0 {
		t.Fatal("pedido sem contexto nao deve criar sessao generica")
	}
	if res.Action == nil || res.Action.Type != "choices" || len(res.Action.Options) != 5 {
		t.Fatalf("esperava dominios oficiais da CKA: %+v", res.Action)
	}
}

func TestClusterGateOnlyBlocksLabProducingTurns(t *testing.T) {
	if RequiresClusterForRequest("Crie um lab para CKA", "CKA") {
		t.Fatal("escolha de dominio ainda nao cria lab")
	}
	if !RequiresClusterForRequest("Crie um lab de NodePort", "CKA") {
		t.Fatal("pedido especifico precisa validar/subir o cluster")
	}
	if RequiresClusterForRequest("Explique a diferenca entre ClusterIP e NodePort", "CKA") {
		t.Fatal("explicacao sem lab deve continuar disponivel offline")
	}
}

func TestCurriculumDomainAsksForExactCompetency(t *testing.T) {
	res := Chat("learner", "Quero criar um lab de CKA no dominio Cluster Architecture, Installation & Configuration", "CKA", func(ids []string) (string, string, int) {
		t.Fatal("dominio ainda deve pedir a competencia")
		return "", "", 0
	})
	if res.Action == nil || res.Action.Type != "choices" || len(res.Action.Options) != 8 {
		t.Fatalf("esperava as 8 competencias oficiais: %+v", res.Action)
	}
	if res.Action.Options[0].Topic != "RBAC" || !res.Action.Options[0].Available {
		t.Fatalf("RBAC deve aparecer como lab exato disponivel: %+v", res.Action.Options[0])
	}
}

func TestExactTopicsAndTyposDoNotFallBackToGenericLabs(t *testing.T) {
	cases := map[string]string{
		"crie lab podAntiAffinity":         "Pod Affinity and Anti-Affinity",
		"lab de NodePort":                  "NodePort",
		"lab de Taint e Tolerations":       "Taints and Tolerations",
		"lab sobre Admission controlle":    "Admission Control",
		"lab de role based access control": "RBAC",
	}
	for prompt, want := range cases {
		if got := exactTopicForRequest("CKA", prompt); got != want {
			t.Errorf("%q roteou para %q, esperado %q", prompt, got, want)
		}
		qs := generateQuestions(want, "CKA", 2, 1)
		if len(qs) != 1 || qs[0].Topic != want {
			t.Errorf("%q nao produziu template exato: %+v", want, qs)
			continue
		}
		if err := LabRequestAdherence(qs[0], prompt); err != nil {
			t.Errorf("%q falhou no gate de aderencia: %v", prompt, err)
		}
	}
}
