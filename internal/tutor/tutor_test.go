package tutor

import (
	"strings"
	"testing"
)

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

func TestGenerateLevel3HasStepByStep(t *testing.T) {
	qs := generateQuestions("Workloads", "CKA", 3, 1)
	if len(qs) == 0 || !strings.Contains(qs[0].Question, "passo") {
		t.Error("nível 3 deve trazer enunciado passo a passo")
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

// ── citações e fallback de tópicos ───────────────────────────────────────────

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

func TestCurriculumEmbedded(t *testing.T) {
	for _, cert := range []string{"CKA", "CKAD", "CKS"} {
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
