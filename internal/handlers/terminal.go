package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"estudo-app/internal/models"
	"estudo-app/internal/tutor"

	"github.com/gorilla/websocket"
)

// ptySession abstrai um PTY interativo entre plataformas: ConPTY (Windows,
// spawnando wsl.exe) e /dev/pts via creack/pty (Linux, quando o app roda
// dentro do WSL). A implementação vem de terminal_windows.go/terminal_linux.go.
type ptySession interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	Resize(cols, rows int) error
	Close() error
}

// activeTerminals conta sessões de terminal abertas — enquanto houver uma,
// o auto-stop do cluster cloud não pode disparar (usuário está estudando).
var activeTerminals int32

func ActiveTerminals() int { return int(atomic.LoadInt32(&activeTerminals)) }

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     sameOriginWS,
}

// sameOriginWS bloqueia upgrade de WebSocket vindo de outra origem (defesa
// contra cross-site WebSocket hijacking do terminal). Requisições sem header
// Origin (clientes não-browser: wscat, testes) são permitidas. Um allowlist
// extra pode ser dado por ALLOWED_WS_ORIGINS (hosts separados por vírgula).
func sameOriginWS(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // não-browser
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	for _, allowed := range strings.Split(os.Getenv("ALLOWED_WS_ORIGINS"), ",") {
		if allowed = strings.TrimSpace(allowed); allowed != "" && strings.EqualFold(allowed, u.Host) {
			return true
		}
	}
	return false
}

type wsMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

var (
	wslUserOnce  sync.Once
	wslUserValue string
)

func getWslUser() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	wslUserOnce.Do(func() {
		out, err := exec.Command("wsl.exe", "--", "id", "-un", "1000").Output()
		if err == nil {
			u := strings.TrimSpace(string(out))
			if u != "" && u != "root" {
				wslUserValue = u
			}
		}
	})
	return wslUserValue
}

func wslArgs(name string, args ...string) []string {
	user := getWslUser()
	if user != "" {
		return append([]string{"-u", user, "--", name}, args...)
	}
	return append([]string{"--", name}, args...)
}

func wslCmd(name string, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("wsl.exe", wslArgs(name, args...)...)
	}
	return exec.Command(name, args...)
}

func wslCmdCtx(ctx context.Context, name string, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "wsl.exe", wslArgs(name, args...)...)
	}
	return exec.CommandContext(ctx, name, args...)
}

func wslShell(cmdStr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("wsl.exe", append(wslArgs("bash"), "-c", cmdStr)...)
	}
	return exec.Command("sh", "-c", cmdStr)
}

func wslShellCtx(ctx context.Context, cmdStr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "wsl.exe", append(wslArgs("bash"), "-c", cmdStr)...)
	}
	return exec.CommandContext(ctx, "sh", "-c", cmdStr)
}

var labRCOnce sync.Once

// labRCScript is a custom bash init file that gives the lab terminal a clean,
// branded prompt (no user@host, no ugly /mnt/c path) while still loading the
// user's real bashrc so kubectl completion, aliases, etc. keep working.
const labRCScript = `# k8s-lab terminal init (auto-generated)
[ -f /etc/profile ] && . /etc/profile
[ -f ~/.bashrc ] && source ~/.bashrc
unset PROMPT_COMMAND
# Workspace dos labs de IaC (Terraform), isolado por usuário. Use: cd $TFLAB/<lab>
export TFLAB="$HOME/tflab/${LAB_USER:-default}"
mkdir -p "$TFLAB" 2>/dev/null
[ -f /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion
source <(kubectl completion bash) 2>/dev/null
alias k=kubectl
complete -o default -F __start_kubectl kubectl 2>/dev/null
complete -o default -F __start_kubectl k 2>/dev/null
bind 'set show-all-if-ambiguous on' 2>/dev/null
bind 'set completion-ignore-case on' 2>/dev/null
alias kdefault='kubectl config set-context --current --namespace=default >/dev/null && echo namespace=default'
[ -n "$LAB_NAMESPACE" ] && alias klab='kubectl config set-context --current --namespace="$LAB_NAMESPACE" >/dev/null && echo namespace="$LAB_NAMESPACE"'
PS1='\[\e[38;2;129;140;248m\]\[\e[1m\]⎈ k8s\[\e[0m\] \[\e[38;2;52;211;153m\]\w\[\e[0m\] \[\e[38;2;129;140;248m\]❯\[\e[0m\] '
cd ~ 2>/dev/null
clear
`

// ensureLabRC writes ~/.k8slab_rc inside WSL (idempotent, runs once).
func ensureLabRC() {
	labRCOnce.Do(func() {
		script := "cat > \"$HOME/.k8slab_rc\" <<'K8SLABEOF'\n" + labRCScript + "K8SLABEOF\n"
		if err := wslShell(script).Run(); err != nil {
			log.Printf("[terminal] could not write lab rcfile: %v", err)
		}
	})
}

func ClusterInfoCmd() *exec.Cmd {
	return wslCmd("kubectl", "cluster-info", "--request-timeout=3s")
}

// ─────────────────────────────────────────────────────────────────────────────
// Cloud shell — terminal DENTRO do cluster AKS (pod administrativo)
// O control plane do AKS é gerenciado (sem SSH), então o padrão é dar shell em
// um pod com kubectl + service account cluster-admin, como nos simuladores.
// ─────────────────────────────────────────────────────────────────────────────

const (
	cloudShellSystemNS  = "lab-system"
	cloudShellSystemPod = "lab-shell"
)

// alpine/k8s: bash, kubectl, helm, vim, jq — ideal p/ treino de certificação.
// Sobrescreva com CLOUD_SHELL_IMAGE se a tag não existir mais.
func cloudShellImage() string { return envOr("CLOUD_SHELL_IMAGE", "alpine/k8s:1.33.4") }

// terminalUsesCloudShell centralizes the security boundary for the terminal.
// On an Azure VM with managed identity, the application container shell is
// never exposed: even local/minikube contexts use a Kubernetes pod with its
// own ServiceAccount and RBAC. Outside hosted mode, legacy local behavior is
// preserved.
func terminalUsesCloudShell(kubeContext string) bool {
	return managedIdentity() || (kubeContext != "" && kubeContext != localContext)
}

func terminalLocalFallbackAllowed() bool { return !managedIdentity() }

// hostedTerminalIdentityReady fails closed if hosted mode is accidentally
// started without authentication. RequireAuth normally guarantees this, but a
// terminal must not silently become shared after a runtime misconfiguration.
func hostedTerminalIdentityReady(uid string) bool {
	if !managedIdentity() {
		return true
	}
	id := tutor.SanitizeID(uid)
	return appPassword() != "" && id != "" && id != "default"
}

// cloudShellTarget resolve o alvo do shell dentro do cluster para um usuário.
// Multi-user (APP_PASSWORD): cada conta ganha o PRÓPRIO pod num namespace
// isolado (lab-<user>) e RBAC namespaced para os namespaces usados pelos labs.
// Não damos cluster-admin compartilhado para usuário autenticado. Uso local/sem
// login cai no pod único de sistema com cluster-admin (single-tenant).
func cloudShellTarget(uid string) (ns, pod, sa string, scoped bool) {
	id := tutor.SanitizeID(uid)
	// Hosted environments must never resolve to the shared lab-admin pod. The
	// WebSocket handler rejects a default identity; this scoped fallback keeps
	// maintenance callers safe as defense in depth.
	if managedIdentity() {
		if id == "" || id == "default" {
			id = "hosted-default"
		}
		return "lab-" + id, "lab-shell-" + id, "lab-user", true
	}
	if appPassword() == "" || id == "" || id == "default" {
		return cloudShellSystemNS, cloudShellSystemPod, "lab-admin", false
	}
	return userLabNamespace(uid), "lab-shell-" + id, "lab-user", true
}

type cloudShellAccess struct {
	Namespace       string
	ClusterRole     string
	EnsureNamespace bool
}

type cloudShellClusterAccess struct {
	Name      string
	Resources []string
	Verbs     []string
}

type cloudShellAccessPlan struct {
	Namespaces []cloudShellAccess
	Cluster    []cloudShellClusterAccess
}

var (
	labNSFlagRe   = regexp.MustCompile(`(?i)(?:^|\s)(?:-n|--namespace)\s+([a-z0-9]([-a-z0-9]*[a-z0-9])?)\b`)
	labNSEqRe     = regexp.MustCompile(`(?i)(?:^|\s)--namespace=([a-z0-9]([-a-z0-9]*[a-z0-9])?)\b`)
	labCreateNSRe = regexp.MustCompile(`(?i)\bkubectl\s+create\s+(?:ns|namespace)\s+([a-z0-9]([-a-z0-9]*[a-z0-9])?)\b`)
	labAllPodsRe  = regexp.MustCompile(`(?i)\bkubectl\s+(?:get|describe)\s+(?:pods?|po)\b[^\n;]*(?:-A\b|--all-namespaces\b)`)
)

func cloudShellAccessRules(userNS string, q *models.Question) []cloudShellAccess {
	return cloudShellAccessPlanFor(userNS, q).Namespaces
}

func cloudShellAccessPlanFor(userNS string, q *models.Question) cloudShellAccessPlan {
	rules := []cloudShellAccess{
		{Namespace: userNS, ClusterRole: "admin", EnsureNamespace: true},
	}
	var cluster []cloudShellClusterAccess
	text := cloudShellAccessText(q)
	if containsAny(text, "aws", "s3", "sqs", "iam", "localstack", "awslocal", "terraform", "tofu") {
		rules = append(rules, cloudShellAccess{Namespace: userNS + "-tools", ClusterRole: "admin", EnsureNamespace: true})
	}
	if containsAny(text, "argocd", "argo cd", "argo-cd", "gitops", "capa", "application", "applications.argoproj.io") {
		rules = append(rules, cloudShellAccess{Namespace: "argocd", ClusterRole: "admin", EnsureNamespace: true})
	}
	if containsAny(text, "hpa", "horizontalpodautoscaler", "metrics-server", "metrics.k8s.io", "kube-system") {
		rules = append(rules, cloudShellAccess{Namespace: "kube-system", ClusterRole: "view"})
	}
	if containsAny(text, "ingress-nginx", "ingress controller") {
		rules = append(rules, cloudShellAccess{Namespace: "ingress-nginx", ClusterRole: "view"})
	}
	for _, ns := range labNamespacesFromText(text) {
		if ns == "default" || ns == userNS || ns == "kube-system" || ns == "ingress-nginx" {
			continue
		}
		rules = append(rules, cloudShellAccess{Namespace: ns, ClusterRole: "admin", EnsureNamespace: true})
	}
	if containsAny(text, "namespace", "namespaces", "create ns", "create namespace", "kubectl get ns", "kubectl get namespace") {
		cluster = append(cluster, cloudShellClusterAccess{
			Name:      "namespace-editor",
			Resources: []string{"namespaces"},
			Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		})
	}
	if containsAny(text, "node", "nodes", "cordon", "drain", "top node", "kubectl get nodes") {
		cluster = append(cluster, cloudShellClusterAccess{
			Name:      "node-reader",
			Resources: []string{"nodes", "nodes/status"},
			Verbs:     []string{"get", "list", "watch"},
		})
	}
	// A consulta `kubectl get pods -A` e cluster-scoped. Conceda somente
	// leitura e apenas aos labs que declaram explicitamente esse comando.
	if labAllPodsRe.MatchString(text) {
		cluster = append(cluster, cloudShellClusterAccess{
			Name:      "pod-reader",
			Resources: []string{"pods", "pods/log"},
			Verbs:     []string{"get", "list", "watch"},
		})
	}
	if containsAny(text, "storageclass", "storage class", "persistentvolume ", " persistent volume", " pv ") {
		cluster = append(cluster, cloudShellClusterAccess{
			Name:      "storage-reader",
			Resources: []string{"storageclasses", "persistentvolumes"},
			Verbs:     []string{"get", "list", "watch"},
		})
	}
	out := make([]cloudShellAccess, 0, len(rules))
	seen := map[string]bool{}
	for _, r := range rules {
		if r.Namespace == "" || seen[r.Namespace] {
			continue
		}
		seen[r.Namespace] = true
		out = append(out, r)
	}
	return cloudShellAccessPlan{Namespaces: out, Cluster: uniqueClusterAccess(cluster)}
}

func cloudShellAccessText(q *models.Question) string {
	if q == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(string(q.Cert))
	b.WriteByte(' ')
	b.WriteString(q.Topic)
	b.WriteByte(' ')
	b.WriteString(q.Question)
	b.WriteByte(' ')
	b.WriteString(q.AnswerCommand)
	if q.Validation != nil {
		b.WriteByte(' ')
		b.WriteString(q.Validation.Command)
	}
	for _, s := range q.Setup {
		b.WriteByte(' ')
		b.WriteString(s.Description)
		b.WriteByte(' ')
		b.WriteString(s.Command)
	}
	for _, g := range q.Goals {
		b.WriteByte(' ')
		b.WriteString(g.Description)
		if g.Validation != nil {
			b.WriteByte(' ')
			b.WriteString(g.Validation.Command)
		}
	}
	for _, s := range q.Teardown {
		b.WriteByte(' ')
		b.WriteString(s)
	}
	if q.LabSpec != nil {
		b.WriteByte(' ')
		b.WriteString(q.LabSpec.Objective)
		b.WriteByte(' ')
		b.WriteString(q.LabSpec.Scenario)
		for _, d := range q.LabSpec.Dependencies {
			b.WriteByte(' ')
			b.WriteString(d.Name)
			b.WriteByte(' ')
			b.WriteString(d.Kind)
			b.WriteByte(' ')
			b.WriteString(d.InstallAction)
			b.WriteByte(' ')
			b.WriteString(d.StatusCommand)
		}
		for _, e := range q.LabSpec.Evidence {
			b.WriteByte(' ')
			b.WriteString(e.Domain)
		}
		for _, c := range q.LabSpec.Chunks {
			b.WriteByte(' ')
			b.WriteString(c.Domain)
			b.WriteByte(' ')
			b.WriteString(c.Title)
		}
	}
	return strings.ToLower(b.String())
}

func labNamespacesFromText(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(ns string) {
		ns = strings.Trim(strings.ToLower(ns), "`'\".,;:()[]{}")
		if ns == "" || seen[ns] {
			return
		}
		seen[ns] = true
		out = append(out, ns)
	}
	for _, re := range []*regexp.Regexp{labNSFlagRe, labNSEqRe, labCreateNSRe} {
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			if len(m) > 1 {
				add(m[1])
			}
		}
	}
	return out
}

func containsAny(text string, terms ...string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func uniqueClusterAccess(in []cloudShellClusterAccess) []cloudShellClusterAccess {
	out := make([]cloudShellClusterAccess, 0, len(in))
	seen := map[string]bool{}
	for _, r := range in {
		if r.Name == "" || seen[r.Name] {
			continue
		}
		seen[r.Name] = true
		out = append(out, r)
	}
	return out
}

func cloudShellRoleBindingName(userNS, targetNS string) string {
	id := strings.TrimPrefix(tutor.SanitizeID(userNS), "lab-")
	name := "lab-shell-" + id + "-" + tutor.SanitizeID(targetNS)
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.TrimRight(name, "-")
}

func cloudShellClusterRoleName(userNS, name string) string {
	id := strings.TrimPrefix(tutor.SanitizeID(userNS), "lab-")
	out := "lab-shell-" + id + "-" + tutor.SanitizeID(name)
	if len(out) > 63 {
		out = out[:63]
	}
	return strings.TrimRight(out, "-")
}

func cloudShellNamespaceAccessScript(userNS, sa string, q *models.Question) string {
	var parts []string
	parts = append(parts,
		fmt.Sprintf("kubectl create namespace %[1]s 2>/dev/null; ", userNS)+namespaceGuardrailsScript(userNS)+fmt.Sprintf("\nkubectl -n %[1]s create serviceaccount %[2]s 2>/dev/null", userNS, sa))
	plan := cloudShellAccessPlanFor(userNS, q)
	for _, r := range plan.Namespaces {
		if r.EnsureNamespace {
			parts = append(parts, fmt.Sprintf("kubectl create namespace %s 2>/dev/null", r.Namespace))
		}
		// Labs commonly use default/tools instead of the shell namespace. Apply
		// the same IMDS boundary anywhere this user can create pods, otherwise a
		// second pod would trivially bypass the shell's egress policy.
		if r.Namespace != userNS && r.ClusterRole == "admin" {
			parts = append(parts, imdsEgressGuardrailCommand(r.Namespace))
		}
		parts = append(parts, fmt.Sprintf(
			"kubectl -n %s create rolebinding %s --clusterrole=%s --serviceaccount=%s:%s 2>/dev/null",
			r.Namespace, cloudShellRoleBindingName(userNS, r.Namespace), r.ClusterRole, userNS, sa))
	}
	for _, r := range plan.Cluster {
		name := cloudShellClusterRoleName(userNS, r.Name)
		parts = append(parts, fmt.Sprintf(
			"kubectl create clusterrole %s --verb=%s --resource=%s 2>/dev/null",
			name, strings.Join(r.Verbs, ","), strings.Join(r.Resources, ",")))
		parts = append(parts, fmt.Sprintf(
			"kubectl create clusterrolebinding %s --clusterrole=%s --serviceaccount=%s:%s 2>/dev/null",
			name, name, userNS, sa))
	}
	return strings.Join(parts, "; ") + "; "
}

func ensureCloudShellNamespaceAccess(userNS, sa string, q *models.Question) {
	if userNS == "" || sa == "" {
		return
	}
	wslShell(cloudShellNamespaceAccessScript(userNS, sa, q)).Run()
}

// cloudShellRC gera o rcfile do bash dentro do pod, com o namespace default
// alinhado ao comportamento dos labs. Responsabilidades críticas:
//  1. kubeconfig próprio usando default como namespace atual — o in-cluster config
//     usaria o namespace do ServiceAccount, fazendo terminal e validação divergirem;
//  2. tokenFile (não token estático) — tokens de SA expiram, o arquivo é rotacionado;
//  3. completion de verdade: exige o pacote bash-completion (instalado no provisionamento).
func cloudShellRC(ns string) string {
	return `# kubeconfig alinhado com as validacoes do lab (namespace ` + ns + `)
if [ ! -f /tmp/.labkube ]; then
cat > /tmp/.labkube <<'KCFG'
apiVersion: v1
kind: Config
clusters:
- name: incluster
  cluster:
    server: https://kubernetes.default.svc
    certificate-authority: /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
users:
- name: sa
  user:
    tokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
contexts:
- name: incluster
  context:
    cluster: incluster
    user: sa
    namespace: ` + ns + `
current-context: incluster
KCFG
fi
export KUBECONFIG=/tmp/.labkube
export LAB_NAMESPACE="` + ns + `"
export LAB_DEFAULT_NAMESPACE="` + ns + `"
# completion (kubectl + alias k) — precisa do pacote bash-completion
[ -f /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion
source <(kubectl completion bash) 2>/dev/null
alias k=kubectl
complete -o default -F __start_kubectl kubectl 2>/dev/null
complete -o default -F __start_kubectl k 2>/dev/null
bind 'set show-all-if-ambiguous on' 2>/dev/null
bind 'set completion-ignore-case on' 2>/dev/null
alias kdefault='kubectl config set-context --current --namespace=` + ns + ` >/dev/null && echo namespace=` + ns + `'
alias klab='kubectl config set-context --current --namespace=` + ns + ` >/dev/null && echo namespace=` + ns + `'
__lab_ns(){ kubectl config view --minify -o jsonpath='{..namespace}' 2>/dev/null || printf ` + ns + `; }
export KUBE_EDITOR=vim
PS1='\[\e[38;2;56;189;248m\]\[\e[1m\]☁ aks\[\e[0m\]\[\e[38;2;107;114;128m\]·$(__lab_ns)\[\e[0m\] \[\e[38;2;52;211;153m\]\w\[\e[0m\] \[\e[38;2;56;189;248m\]❯\[\e[0m\] '
cd 2>/dev/null
`
}

// ensureCloudShellPod garante ns + service account + RBAC + pod prontos no
// cluster ativo (AKS), para o usuário informado. Idempotente; o report envia
// progresso ao terminal do usuário. Devolve (ns, pod) do shell provisionado.
func ensureCloudShellPod(uid string, report func(string)) (string, string, error) {
	ns, pod, sa, scoped := cloudShellTarget(uid)
	active, hasActive := tutor.ActiveQuestion(uid)
	var activeLab *models.Question
	if hasActive {
		activeLab = &active
	}

	// Prova de vida real: exec de verdade, não o phase do etcd. Após um
	// stop/start do AKS o pod pode ficar órfão (preso a um nó que foi
	// desalocado) com phase=Running stale — e aí todo exec falha com
	// "unable to upgrade connection".
	alive := wslShell(fmt.Sprintf(
		"kubectl -n %s exec %s --request-timeout=10s -- true 2>/dev/null",
		ns, pod)).Run() == nil

	if !alive {
		// Remove um eventual pod órfão antes de recriar (o nó dele já era).
		wslShell(fmt.Sprintf(
			"kubectl -n %s delete pod %s --force --grace-period=0 --ignore-not-found --wait=false 2>/dev/null",
			ns, pod)).Run()

		report("provisionando shell dentro do cluster (primeira vez leva ~1-2 min)...")

		// Multi-user: cria o namespace + quotas ANTES do pod. O LimitRange precisa
		// existir primeiro, senão o ResourceQuota rejeita o próprio pod do shell
		// (que sobe sem requests explícitos). Best-effort via client-go.
		if scoped {
			_ = ensureNamespace(ns)
		}

		// RBAC: escopo namespaced em multi-user; cluster-admin só no pod de
		// sistema single-tenant. O clusterrole "admin" é embutido no k8s.
		var bind string
		if scoped {
			bind = cloudShellNamespaceAccessScript(ns, sa, activeLab)
		} else {
			bind = fmt.Sprintf(
				"kubectl create clusterrolebinding lab-shell-admin --clusterrole=cluster-admin --serviceaccount=%s:%s 2>/dev/null; ",
				ns, sa)
		}
		script := fmt.Sprintf(
			`kubectl create namespace %[1]s 2>/dev/null; `+
				`kubectl -n %[1]s create serviceaccount %[4]s 2>/dev/null; `+
				bind+
				`kubectl -n %[1]s get pod %[2]s >/dev/null 2>&1 || `+
				`kubectl -n %[1]s run %[2]s --image=%[3]s --overrides='{"spec":{"serviceAccountName":"%[4]s"}}' --command -- sleep infinity; `+
				`kubectl -n %[1]s wait --for=condition=Ready pod/%[2]s --timeout=180s`,
			ns, pod, cloudShellImage(), sa)
		if out, err := wslShell(script).CombinedOutput(); err != nil {
			return ns, pod, fmt.Errorf("pod do shell não ficou pronto: %s", strings.TrimSpace(string(out)))
		}
	}
	if scoped {
		ensureCloudShellNamespaceAccess(ns, sa, activeLab)
	}

	// Sempre garante rcfile + vim + bash-completion — idempotente e barato;
	// cobre pod recém-criado E pod de sessão anterior (fast path).
	// Nenhum dos dois vem na imagem alpine/k8s; sem bash-completion o
	// autocomplete do kubectl simplesmente não funciona.
	wslShell(fmt.Sprintf(
		"kubectl -n %s exec %s -- sh -c 'which vim >/dev/null 2>&1 && [ -f /usr/share/bash-completion/bash_completion ] || apk add --no-cache vim bash-completion >/dev/null 2>&1'",
		ns, pod)).Run()

	// Grava o rcfile dentro do pod via base64 (evita inferno de escaping).
	b64 := base64.StdEncoding.EncodeToString([]byte(cloudShellRC(ns)))
	wslShell(fmt.Sprintf(
		"echo %s | base64 -d | kubectl -n %s exec -i %s -- sh -c 'cat > /tmp/.labrc'",
		b64, ns, pod)).Run()
	return ns, pod, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// GC de pods de shell por-usuário — sem isto, cada conta multi-user deixa um pod
// lab-shell-<user> vivo (sleep infinity) para sempre no cluster compartilhado.
// Removemos o pod após CLOUD_SHELL_IDLE_MINUTES sem nenhum terminal aberto; a
// próxima conexão o recria (o namespace/SA/quota permanecem, então é rápido).
// ─────────────────────────────────────────────────────────────────────────────

var (
	shellMu       sync.Mutex
	shellOpen     = map[string]int{}       // uid -> terminais cloud abertos agora
	shellLastSeen = map[string]time.Time{} // uid -> última atividade no shell cloud
)

func cloudShellIdleMinutes() int {
	n := 0
	if _, err := fmt.Sscanf(envOr("CLOUD_SHELL_IDLE_MINUTES", "30"), "%d", &n); err != nil || n <= 0 {
		return 30
	}
	return n
}

func markShellOpen(uid string) {
	shellMu.Lock()
	shellOpen[uid]++
	shellLastSeen[uid] = time.Now()
	shellMu.Unlock()
}

func markShellClosed(uid string) {
	shellMu.Lock()
	if shellOpen[uid] > 0 {
		shellOpen[uid]--
	}
	shellLastSeen[uid] = time.Now()
	shellMu.Unlock()
}

// StartCloudShellGC remove pods de shell ociosos periodicamente. Só age quando o
// alvo ativo é a nuvem e apenas em pods scoped (por-usuário) — o pod de sistema
// (uso local/sem-login) é compartilhado e nunca coletado.
func StartCloudShellGC() {
	idle := time.Duration(cloudShellIdleMinutes()) * time.Minute
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			if ctx := currentContext(); !terminalUsesCloudShell(ctx) {
				continue
			}
			now := time.Now()
			shellMu.Lock()
			var stale []string
			for uid, seen := range shellLastSeen {
				if shellOpen[uid] == 0 && now.Sub(seen) > idle {
					stale = append(stale, uid)
				}
			}
			shellMu.Unlock()
			for _, uid := range stale {
				ns, pod, _, scoped := cloudShellTarget(uid)
				if !scoped {
					continue
				}
				wslShell(fmt.Sprintf(
					"kubectl -n %s delete pod %s --ignore-not-found --wait=false 2>/dev/null", ns, pod)).Run()
				shellMu.Lock()
				delete(shellOpen, uid)
				delete(shellLastSeen, uid)
				shellMu.Unlock()
				log.Printf("[cloud-shell] pod ocioso removido: %s/%s", ns, pod)
			}
		}
	}()
}

var clusterMu sync.Mutex

func clusterIsUp() bool {
	// Hot path (polled): quando rodando no WSL, checa via API sem spawn.
	if up, handled := k8sClusterUp(currentContext()); handled {
		return up
	}
	// Fallback: host Windows ou client-go indisponível → shell.
	out, err := wslShell("kubectl cluster-info --request-timeout=5s 2>/dev/null").Output()
	return err == nil && len(out) > 0
}

func EnsureCluster() {
	if !clusterMu.TryLock() {
		return
	}
	defer clusterMu.Unlock()

	// Se o contexto ativo é um cluster de nuvem (não o minikube local), não subir
	// o minikube — o usuário está estudando contra o AKS. Exceção: se o cluster
	// cloud foi EXCLUÍDO na Azure, o contexto aponta para um alvo morto e trava
	// tudo — nesse caso, voltamos ao local automaticamente.
	if ctx := currentContext(); ctx != "" && ctx != localContext {
		if ctx == aksName() && azInstalled() && azSubscription() != "" && !aksExists() {
			log.Printf("[cluster] contexto '%s' aponta para cluster excluído — voltando ao minikube", ctx)
			wslShell("kubectl config use-context " + localContext + " 2>/dev/null").Run()
		} else {
			log.Printf("[cluster] contexto ativo é '%s' (nuvem) — pulando auto-start do minikube", ctx)
			return
		}
	}

	if _, err := wslCmd("which", "kubectl").Output(); err != nil {
		log.Println("[cluster] kubectl not found in WSL — skipping auto-start")
		return
	}

	for i := 0; i < 3; i++ {
		if clusterIsUp() {
			log.Println("[cluster] already running")
			return
		}
		if i < 2 {
			time.Sleep(3 * time.Second)
		}
	}

	// --keep-context: nunca sequestrar o contexto ativo do kubectl — se o usuário
	// estiver usando a nuvem (AKS), o start do minikube não pode trocar o alvo.
	args := []string{"start", "--driver=docker", "--keep-context"}
	if getWslUser() == "" {
		args = append(args, "--force")
	}

	wslShell("service docker status >/dev/null 2>&1 || service docker start >/dev/null 2>&1").Run()

	log.Println("[cluster] starting minikube...")
	out, err := wslCmd("minikube", args...).CombinedOutput()
	if err != nil {
		log.Printf("[cluster] minikube start failed: %v\n%s", err, string(out))
	} else {
		// Com --keep-context, um kubeconfig recém-criado fica sem contexto ativo.
		// Só nesse caso apontamos para o minikube.
		if currentContext() == "" {
			wslShell("kubectl config use-context minikube 2>/dev/null").Run()
		}
		log.Println("[cluster] ready")
	}
}

// TerminalWS opens a real PTY-backed terminal via Windows ConPTY running WSL bash.
// This gives the user a full interactive shell: VIM, tab completion, colors, etc.
func TerminalWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	// Perfil do usuário desta sessão (erros de terminal vão para o skill dele).
	uid := userID(r)
	if !hostedTerminalIdentityReady(uid) {
		conn.WriteMessage(websocket.TextMessage, []byte( //nolint:errcheck
			"\x1b[1;31mTerminal indisponível: o ambiente Azure exige login por usuário.\x1b[0m\r\n"))
		return
	}

	touchActivity()
	atomic.AddInt32(&activeTerminals, 1)
	defer atomic.AddInt32(&activeTerminals, -1)

	// No Azure hospedado o shell sempre roda DENTRO do Kubernetes, inclusive
	// quando o contexto atual é minikube. Assim uma falha de provisionamento
	// nunca expõe o bash root do container privilegiado da aplicação.
	kubeContext := currentContext()
	isCloud := terminalUsesCloudShell(kubeContext)
	var cloudNS, cloudPod string
	if isCloud {
		label := kubeContext
		if label == "" {
			label = "cluster local"
		}
		conn.WriteMessage(websocket.TextMessage, []byte(
			"\x1b[38;2;56;189;248mconectando ao shell Kubernetes isolado ("+label+")...\x1b[0m\r\n"))
		ns, pod, err := ensureCloudShellPod(uid, func(msg string) {
			conn.WriteMessage(websocket.TextMessage, []byte("\x1b[38;2;107;114;128m"+msg+"\x1b[0m\r\n")) //nolint:errcheck
		})
		if err != nil {
			if !terminalLocalFallbackAllowed() {
				conn.WriteMessage(websocket.TextMessage, []byte( //nolint:errcheck
					"\x1b[1;31mNão foi possível abrir o shell Kubernetes isolado: "+err.Error()+"\x1b[0m\r\n"+
						"\x1b[38;2;107;114;128mPor segurança, o terminal local não está disponível neste ambiente.\x1b[0m\r\n"))
				return
			}
			conn.WriteMessage(websocket.TextMessage, []byte( //nolint:errcheck
				"\x1b[1;31mNão foi possível abrir o shell no cluster: "+err.Error()+"\x1b[0m\r\n"+
					"\x1b[38;2;107;114;128mO cluster está ligado? Caindo para o terminal local (kubectl ainda aponta para o AKS).\x1b[0m\r\n"))
			isCloud = false
		} else {
			cloudNS, cloudPod = ns, pod
			// Marca o shell como em uso para o GC não coletar sob o usuário.
			markShellOpen(uid)
			defer markShellClosed(uid)
		}
	}

	// Build the PTY command line.
	// - cloud: bash interativo dentro do pod lab-shell (kubectl exec)
	// - local: bash do WSL com o rcfile custom (prompt limpo, env real do usuário)
	var shellCmd string
	if isCloud {
		shellCmd = fmt.Sprintf("kubectl exec -it -n %s %s -- bash --rcfile /tmp/.labrc -i",
			cloudNS, cloudPod)
	} else {
		// The host rcfile is only created for local development. Hosted mode
		// cannot reach this branch and fails closed above.
		ensureLabRC()
		shellCmd = "bash -c \"exec bash --rcfile ~/.k8slab_rc -i\""
		// LAB_USER isola o workspace dos labs de IaC ($TFLAB no rcfile).
		shellCmd = "LAB_USER=" + tutor.SanitizeID(uid) + " " + shellCmd
		if ns := userLabNamespace(uid); ns != "" {
			shellCmd = "LAB_NAMESPACE=" + ns + " " + shellCmd
		}
		// Multi-user: kubeconfig dedicado, mas namespace atual = default para
		// casar com os labs de prova; LAB_NAMESPACE guarda o namespace privado.
		if kc := userKubeconfig(uid); kc != "" {
			shellCmd = "KUBECONFIG=" + kc + " " + shellCmd
		}
	}
	// Inicia o PTY pela implementação da plataforma: ConPTY (Windows, via
	// wsl.exe) ou PTY nativo (Linux, quando o app roda dentro do WSL).
	cpty, err := startPTY(shellCmd, 220, 50)
	if err != nil {
		log.Printf("[terminal] PTY start failed: %v", err)
		conn.WriteMessage(websocket.TextMessage, []byte(
			"\x1b[1;31mFalha ao iniciar terminal PTY: "+err.Error()+"\x1b[0m\r\n",
		))
		return
	}
	defer cpty.Close()

	// Ensure cluster is up in background
	go EnsureCluster()

	// PTY output → WebSocket (binary frames, raw ANSI)
	// De passagem, o tutor observa o output em busca de erros de comando —
	// sinal de dificuldade prática no tópico ativo (tudo local).
	go func() {
		buf := make([]byte, 4096)
		var lastErrAt time.Time
		for {
			n, err := cpty.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				if time.Since(lastErrAt) > 2*time.Second &&
					(strings.Contains(chunk, "command not found") ||
						strings.Contains(chunk, "Error from server") ||
						strings.Contains(chunk, "error: unknown command") ||
						strings.Contains(chunk, "error: exactly one") ||
						strings.Contains(chunk, "Error: unknown flag")) {
					tutor.RecordTermErrorText(uid, chunk)
					lastErrAt = time.Now()
				}
				if wErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); wErr != nil {
					break
				}
			}
			if err != nil {
				break
			}
		}
	}()

	// WebSocket → PTY
	// Text frames = JSON control (resize); Binary frames = raw input
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if msgType == websocket.TextMessage {
			var msg wsMsg
			if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
				if rErr := cpty.Resize(int(msg.Cols), int(msg.Rows)); rErr != nil {
					log.Printf("[terminal] resize error: %v", rErr)
				}
			}
		} else {
			// Raw keystrokes / paste → PTY
			touchActivity()
			if _, wErr := cpty.Write(data); wErr != nil {
				break
			}
		}
	}
}
