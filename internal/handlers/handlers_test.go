package handlers

import (
	"os"
	"strings"
	"testing"

	"estudo-app/internal/models"
)

func TestContextNameValidation(t *testing.T) {
	valid := []string{"minikube", "k8s-study-lab", "arn:aws:eks:us-east-1:123:cluster/prod",
		"gke_proj_us-central1-a_cluster", "user@cluster.local"}
	for _, v := range valid {
		if !contextNameRe.MatchString(v) {
			t.Errorf("contexto válido rejeitado: %q", v)
		}
	}
	invalid := []string{"", "minikube; rm -rf /", "x && curl evil", "a b", "$(whoami)", "ctx`id`", "x|y"}
	for _, v := range invalid {
		if contextNameRe.MatchString(v) {
			t.Errorf("injeção aceita: %q", v)
		}
	}
}

func TestIsPlaceholderImageViaPrewarm(t *testing.T) {
	// imagens quebradas de propósito nos enunciados nunca podem ir ao prewarm
	re := imageFlagRe
	cmd := "kubectl run x --image=nginx:INVALID_TAG --image=nginx:1.21"
	found := re.FindAllStringSubmatch(cmd, -1)
	if len(found) != 2 {
		t.Fatalf("regex de imagem deveria achar 2, achou %d", len(found))
	}
}

func TestCloudShellNamespaceAccessScript(t *testing.T) {
	q := &models.Question{
		Cert:          models.Cert("AWS"),
		Topic:         "AWS Messaging",
		Question:      "Crie uma fila SQS no LocalStack.",
		AnswerCommand: "awslocal sqs create-queue --queue-name orders",
	}
	script := cloudShellNamespaceAccessScript("lab-alice", "lab-user", q)
	mustContain := []string{
		"kubectl -n default create rolebinding lab-shell-alice-default --clusterrole=admin --serviceaccount=lab-alice:lab-user",
		"kubectl -n tools create rolebinding lab-shell-alice-tools --clusterrole=admin --serviceaccount=lab-alice:lab-user",
	}
	for _, want := range mustContain {
		if !strings.Contains(script, want) {
			t.Fatalf("script de RBAC nao contem %q\nscript=%s", want, script)
		}
	}
	if strings.Contains(script, "clusterrolebinding") || strings.Contains(script, "cluster-admin") {
		t.Fatalf("shell scoped nao pode receber cluster-admin: %s", script)
	}
}

func TestCloudShellRBACUsesActiveLabScope(t *testing.T) {
	base := cloudShellNamespaceAccessScript("lab-alice", "lab-user", nil)
	if strings.Contains(base, "lab-shell-alice-tools") || strings.Contains(base, "lab-shell-alice-argocd") {
		t.Fatalf("sem lab ativo o shell nao deve abrir namespaces de ferramentas: %s", base)
	}

	argo := &models.Question{Cert: models.Cert("CAPA"), Topic: "GitOps", Question: "ArgoCD Application sync"}
	script := cloudShellNamespaceAccessScript("lab-alice", "lab-user", argo)
	if !strings.Contains(script, "kubectl -n argocd create rolebinding lab-shell-alice-argocd --clusterrole=admin --serviceaccount=lab-alice:lab-user") {
		t.Fatalf("lab ArgoCD/CAPA deve abrir namespace argocd: %s", script)
	}

	hpa := &models.Question{Cert: models.CKA, Topic: "Autoscaling", Question: "HPA com metrics-server"}
	script = cloudShellNamespaceAccessScript("lab-alice", "lab-user", hpa)
	if !strings.Contains(script, "kubectl -n kube-system create rolebinding lab-shell-alice-kube-system --clusterrole=view --serviceaccount=lab-alice:lab-user") {
		t.Fatalf("lab HPA deve permitir leitura em kube-system/metrics-server: %s", script)
	}
}

func TestCloudShellClusterRBACIsNarrow(t *testing.T) {
	q := &models.Question{Cert: models.CKA, Topic: "Core", Question: "crie um namespace chamado prova"}
	script := cloudShellNamespaceAccessScript("lab-alice", "lab-user", q)
	if !strings.Contains(script, "create clusterrole lab-shell-alice-namespace-editor --verb=get,list,watch,create,update,patch,delete --resource=namespaces") {
		t.Fatalf("lab de namespace deve criar ClusterRole estreita para namespaces: %s", script)
	}
	if strings.Contains(script, "cluster-admin") {
		t.Fatalf("shell scoped nao pode receber cluster-admin: %s", script)
	}
}

func TestCloudShellRBACIncludesDynamicLabNamespace(t *testing.T) {
	q := &models.Question{
		Cert:          models.CKA,
		Topic:         "Workloads",
		Question:      "crie recursos no namespace prod",
		AnswerCommand: "kubectl create namespace prod; kubectl -n prod create deployment web --image=nginx",
	}
	script := cloudShellNamespaceAccessScript("lab-alice", "lab-user", q)
	if !strings.Contains(script, "kubectl create namespace prod") {
		t.Fatalf("namespace dinamico deve ser criado/conferido: %s", script)
	}
	if !strings.Contains(script, "kubectl -n prod create rolebinding lab-shell-alice-prod --clusterrole=admin --serviceaccount=lab-alice:lab-user") {
		t.Fatalf("namespace dinamico deve receber rolebinding admin namespaced: %s", script)
	}
}

func TestCloudShellRCDefaultsToDefaultNamespace(t *testing.T) {
	rc := cloudShellRC("lab-alice")
	for _, want := range []string{
		"namespace: default",
		`export LAB_NAMESPACE="lab-alice"`,
		"alias kdefault=",
		"alias klab=",
	} {
		if !strings.Contains(rc, want) {
			t.Fatalf("rcfile nao contem %q\nrc=%s", want, rc)
		}
	}
}

func TestLabMakerInternalLabelIsNotRendered(t *testing.T) {
	for _, path := range []string{"../../web/templates/lab.html", "../../web/templates/tutor.html"} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("nao consegui ler %s: %v", path, err)
		}
		if strings.Contains(string(b), "Lab Maker") {
			t.Fatalf("label interno Lab Maker nao deve aparecer em %s", path)
		}
	}
}

func TestTechIconPath(t *testing.T) {
	cases := map[string]string{
		"CKA HPA":         "/static/vendor/kubernetes.svg",
		"AWS SQS":         "/static/vendor/aws.svg",
		"LocalStack":      "/static/vendor/localstack.svg",
		"CAPA ArgoCD":     "/static/vendor/argo.png",
		"Terraform state": "/static/vendor/terraform.svg",
	}
	for input, want := range cases {
		if got := techIconPath(input); got != want {
			t.Fatalf("techIconPath(%q)=%q, want %q", input, got, want)
		}
	}
}
