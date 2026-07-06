package handlers

import (
	"context"
	"embed"
	"encoding/json"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

var toolsStatusCache = newTTLCache[map[string]any](15 * time.Second)

// ─────────────────────────────────────────────────────────────────────────────
// Instalações — catálogo de ferramentas com scripts DA PLATAFORMA, agnósticos
// de nuvem: tudo que é de cluster roda via kubectl no ALVO ATIVO (minikube,
// AKS, EKS, GKE...); CLIs são instaladas no ambiente do lab (WSL).
// O usuário nunca digita script: clica em Instalar e acompanha o progresso.
// ─────────────────────────────────────────────────────────────────────────────

type toolStep struct {
	Desc string
	Cmd  string
	Root bool // roda como root no WSL (instalação de CLI em /usr/local/bin)
}

type toolDef struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Icon  string `json:"icon"`
	Desc  string `json:"desc"`
	Scope string `json:"scope"` // "cluster" (segue o alvo) | "cli" (ambiente do lab)
	After string `json:"after"` // instrução pós-instalação
	check string // fallback shell (usado quando client-go indisponível ou p/ CLIs)
	// Check via client-go (sem spawn): se checkDeploy != "", verifica o Deployment.
	checkNS     string
	checkDeploy string
	steps       []toolStep
}

var toolCatalog = []toolDef{
	{
		ID: "helm", Name: "Helm", Icon: "⎈", Scope: "cli",
		Desc:  "gerenciador de pacotes do Kubernetes — pré-requisito de vários charts",
		After: "use no terminal do lab: helm repo add ... && helm install ...",
		check: "which helm",
		steps: []toolStep{
			{Desc: "Baixando e instalando o Helm (CLI)...", Root: true,
				Cmd: "curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash 2>&1 | tail -3"},
		},
	},
	{
		ID: "metrics-server", Name: "Metrics Server", Icon: "📈", Scope: "cluster",
		Desc:    "habilita kubectl top nodes/pods e HPA — essencial para labs de autoscaling",
		After:   "teste: kubectl top nodes (leva ~1 min para coletar as primeiras métricas)",
		check:   "kubectl get deploy metrics-server -n kube-system -o name 2>/dev/null",
		checkNS: "kube-system", checkDeploy: "metrics-server",
		steps: []toolStep{
			{Desc: "Aplicando manifests do Metrics Server...",
				Cmd: "kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml 2>&1 | tail -3"},
			{Desc: "Ajustando TLS para clusters de estudo (kubelet-insecure-tls)...",
				Cmd: `kubectl patch deployment metrics-server -n kube-system --type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' 2>&1 || true`},
			{Desc: "Aguardando ficar disponível...",
				Cmd: "kubectl rollout status deployment/metrics-server -n kube-system --timeout=120s 2>&1 | tail -1"},
		},
	},
	{
		ID: "ingress-nginx", Name: "NGINX Ingress", Icon: "🌐", Scope: "cluster",
		Desc:    "Ingress Controller — necessário para os labs de Ingress responderem de verdade",
		After:   "seus Ingress passam a ser atendidos pela classe nginx",
		check:   "kubectl get deploy ingress-nginx-controller -n ingress-nginx -o name 2>/dev/null",
		checkNS: "ingress-nginx", checkDeploy: "ingress-nginx-controller",
		steps: []toolStep{
			{Desc: "Aplicando manifests do ingress-nginx...",
				Cmd: "kubectl apply -f https://raw.githubusercontent.com/kubernetes/ingress-nginx/controller-v1.11.2/deploy/static/provider/cloud/deploy.yaml 2>&1 | tail -3"},
			{Desc: "Aguardando o controller subir (pode levar 1-2 min)...",
				Cmd: "kubectl rollout status deployment/ingress-nginx-controller -n ingress-nginx --timeout=180s 2>&1 | tail -1"},
		},
	},
	{
		ID: "grafana", Name: "Grafana", Icon: "📊", Scope: "cluster",
		Desc:    "dashboards de observabilidade — par perfeito com o Metrics Server",
		After:   "acesse: kubectl port-forward -n tools svc/grafana 3000:3000 → http://localhost:3000 (admin/admin)",
		check:   "kubectl get deploy grafana -n tools -o name 2>/dev/null",
		checkNS: "tools", checkDeploy: "grafana",
		steps: []toolStep{
			{Desc: "Criando namespace tools...", Cmd: "kubectl create namespace tools 2>/dev/null || true"},
			{Desc: "Instalando o Grafana...",
				Cmd: "kubectl create deployment grafana --image=grafana/grafana:11.2.0 -n tools 2>&1 || true"},
			{Desc: "Expondo na porta 3000...",
				Cmd: "kubectl expose deployment grafana --port=3000 -n tools 2>&1 || true"},
			{Desc: "Aguardando ficar pronto...",
				Cmd: "kubectl rollout status deployment/grafana -n tools --timeout=180s 2>&1 | tail -1"},
		},
	},
	{
		ID: "prometheus", Name: "Prometheus", Icon: "🔥", Scope: "cluster",
		Desc:    "coleta de métricas — alimenta o Grafana e os labs de Observability",
		After:   "no Grafana, adicione o datasource: http://prometheus.tools.svc:9090",
		check:   "kubectl get deploy prometheus -n tools -o name 2>/dev/null",
		checkNS: "tools", checkDeploy: "prometheus",
		steps: []toolStep{
			{Desc: "Criando namespace tools...", Cmd: "kubectl create namespace tools 2>/dev/null || true"},
			{Desc: "Instalando o Prometheus...",
				Cmd: "kubectl create deployment prometheus --image=prom/prometheus:v2.53.0 -n tools 2>&1 || true"},
			{Desc: "Expondo na porta 9090...",
				Cmd: "kubectl expose deployment prometheus --port=9090 -n tools 2>&1 || true"},
			{Desc: "Aguardando ficar pronto...",
				Cmd: "kubectl rollout status deployment/prometheus -n tools --timeout=180s 2>&1 | tail -1"},
		},
	},
}

func findTool(id string) *toolDef {
	for i := range toolCatalog {
		if toolCatalog[i].ID == id {
			return &toolCatalog[i]
		}
	}
	return nil
}

// toolInstalled decide se a ferramenta está instalada. Para os checks de
// cluster usa client-go (sem spawn) quando disponível; senão, cai no shell.
func toolInstalled(kctx string, t toolDef) bool {
	if t.checkDeploy != "" {
		if ok, handled := deploymentExists(kctx, t.checkNS, t.checkDeploy); handled {
			return ok
		}
	}
	return wslShell(t.check).Run() == nil
}

// rootShell executa como root no WSL (CLIs em /usr/local/bin, sem sudo do usuário).
func rootShell(ctx context.Context, cmdStr string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "wsl.exe", "-u", "root", "--", "bash", "-c", cmdStr)
	}
	return exec.CommandContext(ctx, "sh", "-c", cmdStr)
}

// ToolsStatusHandler lista o catálogo com o estado de instalação de cada um.
func ToolsStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if cached, ok := toolsStatusCache.Get("tools-status"); ok {
		json.NewEncoder(w).Encode(cached)
		return
	}
	type item struct {
		toolDef
		Installed bool `json:"installed"`
	}
	// Checks em paralelo — antes eram seriais (5× spawn de wsl.exe em fila).
	out := make([]item, len(toolCatalog))
	kctx := currentContext()
	var wg sync.WaitGroup
	for i, t := range toolCatalog {
		wg.Add(1)
		go func(i int, t toolDef) {
			defer wg.Done()
			out[i] = item{toolDef: t, Installed: toolInstalled(kctx, t)}
		}(i, t)
	}
	wg.Wait()
	payload := map[string]any{
		"tools":   out,
		"context": currentContext(),
	}
	toolsStatusCache.Set("tools-status", payload)
	json.NewEncoder(w).Encode(payload) //nolint:errcheck
}

// ToolInstallHandler instala uma ferramenta do catálogo via SSE.
// O id é validado contra o catálogo — nenhum input do usuário vira comando.
func ToolInstallHandler(w http.ResponseWriter, r *http.Request) {
	tool := findTool(r.URL.Query().Get("id"))
	if tool == nil {
		http.Error(w, "ferramenta desconhecida", http.StatusBadRequest)
		return
	}
	s, ok := newSSE(w)
	if !ok {
		return
	}

	total := len(tool.steps)
	for i, st := range tool.steps {
		s.send(map[string]any{"type": "step", "index": i, "total": total, "desc": st.Desc, "status": "running"})
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		var out []byte
		var err error
		if st.Root {
			out, err = rootShell(ctx, st.Cmd).CombinedOutput()
		} else {
			out, err = wslShellCtx(ctx, st.Cmd).CombinedOutput()
		}
		cancel()
		status := "done"
		if err != nil {
			status = "warn"
		}
		s.send(map[string]any{"type": "step", "index": i, "total": total, "desc": st.Desc,
			"status": status, "output": string(out)})
	}

	installed := wslShell(tool.check).Run() == nil
	s.send(map[string]any{"type": "done", "ok": installed, "after": tool.After})
}

// ToolsPage renderiza a página de Instalações.
func ToolsPage(fs embed.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		RenderPage(w, fs, "tools.html", map[string]any{"NavActive": "tools"})
	}
}
