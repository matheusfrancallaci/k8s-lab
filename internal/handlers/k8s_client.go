package handlers

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// ─────────────────────────────────────────────────────────────────────────────
// Acesso client-go — usado quando o app roda DENTRO do WSL/Linux, onde a API do
// cluster (minikube em 192.168.49.2:8443, ou AKS) é alcançável nativamente.
// No host Windows a API fica isolada na rede docker do WSL, então o cliente não
// é sequer tentado — os chamadores caem no caminho shell (wslCmd/kubectl).
// ─────────────────────────────────────────────────────────────────────────────

var (
	k8sMu  sync.Mutex
	k8sCS  *kubernetes.Clientset
	k8sCtx string // contexto p/ o qual o cliente em cache foi construído
)

// k8sAvailable indica se vale a pena tentar o client-go nesta topologia.
// Só quando o app roda em Linux (dentro do WSL) a API do cluster é roteável.
func k8sAvailable() bool { return runtime.GOOS == "linux" }

// k8sClientFor devolve um clientset para o contexto informado, com cache. Erro
// != nil significa "indisponível" — o chamador deve usar o fallback shell.
func k8sClientFor(context string) (*kubernetes.Clientset, error) {
	if !k8sAvailable() {
		return nil, fmt.Errorf("client-go indisponível nesta plataforma (%s)", runtime.GOOS)
	}
	k8sMu.Lock()
	defer k8sMu.Unlock()
	if k8sCS != nil && k8sCtx == context {
		return k8sCS, nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if context != "" {
		overrides.CurrentContext = context
	}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		return nil, err
	}
	cfg.Timeout = 5 * time.Second
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	k8sCS = cs
	k8sCtx = context
	return cs, nil
}

// k8sClusterUp verifica a prontidão do cluster pela API (1 GET em /nodes, sem
// spawn de processo). handled=false significa "client-go indisponível" — o
// chamador deve usar o fallback shell. up só é válido quando handled=true.
func k8sClusterUp(kctx string) (up bool, handled bool) {
	cs, err := k8sClientFor(kctx)
	if err != nil {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if _, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1}); err != nil {
		return false, true
	}
	return true, true
}
