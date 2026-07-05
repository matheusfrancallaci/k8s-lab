package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Three fast steps: namespace cleanup (fire-and-forget), default NS cleanup (combined), verify.
var clusterResetSteps = []struct {
	Desc    string
	Cmd     string
	Timeout time.Duration
}{
	{
		"Removendo namespaces de usuário",
		// xargs with --wait=false — kubectl returns instantly; '&' backgrounds the actual GC
		// kube-.* cobre system/public/node-lease; os demais são gerenciados (Azure/app)
		`NS=$(kubectl get ns --no-headers -o custom-columns=':metadata.name' 2>/dev/null | grep -vE '^(default|kube-.*|argocd|lab-system|tools|ingress-nginx|gatekeeper-system|calico-system|tigera-operator|aks-command|app-routing-system)$'); ` +
			`if [ -z "$NS" ]; then echo "nenhum namespace de usuário"; else echo "$NS" | xargs kubectl delete ns --wait=false 2>/dev/null & echo "deletando: $NS"; fi`,
		8 * time.Second,
	},
	{
		"Limpando namespace default",
		// Single kubectl call for all resource types — much faster than multiple calls
		`kubectl delete pods,deployments,replicasets,statefulsets,daemonsets,jobs,cronjobs,services,` +
			`persistentvolumeclaims,configmaps,secrets,ingresses,networkpolicies,roles,rolebindings ` +
			`--all -n default --force --grace-period=0 --wait=false 2>/dev/null; ` +
			`kubectl get sa -n default -o name 2>/dev/null | grep -v '/default$' | xargs -r kubectl delete -n default --wait=false 2>/dev/null; ` +
			`echo ok`,
		12 * time.Second,
	},
	{
		"Verificando cluster",
		// Labs de cordon/drain/taint sujam o nó e deixam os pods das próximas
		// questões Pending para sempre. Cura: uncordon + remove taints não-sistema.
		`kubectl get nodes -o name 2>/dev/null | xargs -r -n1 kubectl uncordon >/dev/null 2>&1; ` +
			`for k in $(kubectl get nodes -o jsonpath='{range .items[*].spec.taints[*]}{.key}{"\n"}{end}' 2>/dev/null | sort -u | grep -vE 'kubernetes\.io|CriticalAddonsOnly'); do ` +
			`kubectl taint nodes --all "$k"- >/dev/null 2>&1; done; ` +
			`kubectl get nodes --no-headers 2>/dev/null | awk '{print $1": "$2}' | head -3`,
		10 * time.Second,
	},
}

func ClusterResetHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	send := func(data map[string]any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	total := len(clusterResetSteps)
	for i, step := range clusterResetSteps {
		send(map[string]any{
			"type": "step", "index": i, "total": total,
			"desc": step.Desc, "status": "running",
		})

		ctx, cancel := context.WithTimeout(r.Context(), step.Timeout)
		out, _ := wslShellCtx(ctx, step.Cmd).CombinedOutput()
		cancel()

		outStr := strings.TrimSpace(string(out))
		send(map[string]any{
			"type": "step", "index": i, "total": total,
			"desc": step.Desc, "status": "done", "output": outStr,
		})
	}

	send(map[string]any{"type": "done"})
}
