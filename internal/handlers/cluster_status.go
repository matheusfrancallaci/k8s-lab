package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterStatusHandler responde {running, info, context} para o cluster ativo.
// Rodando no WSL/Linux usa client-go (sem spawn); no host Windows cai no shell.
func ClusterStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	kctx := currentContext()

	if up, handled := k8sClusterUp(kctx); handled {
		info := ""
		if up {
			info = clusterInfoViaAPI(kctx)
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"running": up, "info": info, "context": CurrentContext(),
		})
		return
	}

	// Fallback shell (host Windows, ou client-go indisponível).
	out, err := ClusterInfoCmd().CombinedOutput()
	running := err == nil && strings.Contains(string(out), "running")
	info := ""
	if running {
		if lines := strings.Split(string(out), "\n"); len(lines) > 0 {
			info = strings.TrimSpace(lines[0])
		}
	}
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"running": running, "info": info, "context": CurrentContext(),
	})
}

// clusterInfoViaAPI monta um resumo curto (versão do servidor + nº de nós) via
// API. String vazia se algo falhar — o chamador já sabe que o cluster está up.
func clusterInfoViaAPI(kctx string) string {
	cs, err := k8sClientFor(kctx)
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	parts := []string{}
	if ver, err := cs.Discovery().ServerVersion(); err == nil && ver.GitVersion != "" {
		parts = append(parts, "Kubernetes "+ver.GitVersion)
	}
	if nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		n := len(nodes.Items)
		unit := "nós"
		if n == 1 {
			unit = "nó"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, unit))
	}
	return strings.Join(parts, " · ")
}
