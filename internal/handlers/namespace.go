package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"estudo-app/internal/tutor"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ─────────────────────────────────────────────────────────────────────────────
// Isolamento de labs por usuário — cada conta ganha um namespace lab-<user> e um
// kubeconfig com esse namespace como default. Assim os recursos de um não colidem
// com os do outro na instância compartilhada. Só ativa em modo multi-user
// (APP_PASSWORD) e quando a API é alcançável nativamente (linux/container).
// Labs cluster-scoped (nós, RBAC global) seguem compartilhados — natureza do domínio.
// ─────────────────────────────────────────────────────────────────────────────

var (
	kubeconfigMu    sync.Mutex
	userKubeconfigs = map[string]string{}
)

// userKubeconfig devolve o caminho de um kubeconfig cujo namespace default é
// lab-<user> (criando o namespace se preciso). "" = sem isolamento (padrão).
func userKubeconfig(userID string) string {
	if appPassword() == "" || !k8sAvailable() {
		return ""
	}
	id := tutor.SanitizeID(userID)
	if id == "default" {
		return ""
	}
	kubeconfigMu.Lock()
	defer kubeconfigMu.Unlock()
	if p, ok := userKubeconfigs[id]; ok {
		return p
	}
	ns := "lab-" + id
	if err := ensureNamespace(ns); err != nil {
		return "" // sem namespace -> sem isolamento (cai no padrão)
	}
	base := os.Getenv("KUBECONFIG")
	if base == "" {
		base = "/etc/rancher/k3s/k3s.yaml"
	}
	path := filepath.Join(os.TempDir(), "kc-"+id+".yaml")
	// Copia o kubeconfig base e seta o namespace default = lab-<user>.
	script := fmt.Sprintf(
		"kubectl --kubeconfig=%s config view --raw > %s && kubectl --kubeconfig=%s config set-context --current --namespace=%s",
		base, path, path, ns)
	if err := wslShell(script).Run(); err != nil {
		return ""
	}
	userKubeconfigs[id] = path
	return path
}

// ensureNamespace cria o namespace via client-go (idempotente).
func ensureNamespace(name string) error {
	cs, err := k8sClientFor(currentContext())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = cs.CoreV1().Namespaces().Create(ctx,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}},
		metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	return err
}
