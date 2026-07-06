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
	"k8s.io/apimachinery/pkg/api/resource"
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
	// Usa a env KUBECONFIG (não o flag --kubeconfig) porque a base pode ser
	// mesclada (k3s local : AKS) com ':', que só a env aceita.
	script := fmt.Sprintf(
		"KUBECONFIG=%s kubectl config view --raw > %s && KUBECONFIG=%s kubectl config set-context --current --namespace=%s",
		base, path, path, ns)
	if err := wslShell(script).Run(); err != nil {
		return ""
	}
	userKubeconfigs[id] = path
	return path
}

// ensureNamespace cria o namespace via client-go (idempotente) e aplica quotas
// para o usuário não conseguir esgotar o cluster compartilhado.
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
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	ensureNamespaceLimits(name)
	return nil
}

// ensureNamespaceLimits aplica um ResourceQuota + LimitRange no namespace do
// usuário, protegendo o cluster compartilhado contra um lab que consome tudo.
// Valores sobrescrevíveis por env (LAB_QUOTA_*). Best-effort e idempotente:
// falhas (AlreadyExists ou client-go indisponível) são silenciosas.
func ensureNamespaceLimits(ns string) {
	cs, err := k8sClientFor(currentContext())
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	quota := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "lab-quota", Namespace: ns},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourcePods:                   resource.MustParse(envOr("LAB_QUOTA_PODS", "20")),
				corev1.ResourceRequestsCPU:            resource.MustParse(envOr("LAB_QUOTA_CPU_REQ", "2")),
				corev1.ResourceLimitsCPU:              resource.MustParse(envOr("LAB_QUOTA_CPU_LIM", "4")),
				corev1.ResourceRequestsMemory:         resource.MustParse(envOr("LAB_QUOTA_MEM_REQ", "2Gi")),
				corev1.ResourceLimitsMemory:           resource.MustParse(envOr("LAB_QUOTA_MEM_LIM", "4Gi")),
				corev1.ResourceServices:               resource.MustParse(envOr("LAB_QUOTA_SVC", "20")),
				corev1.ResourcePersistentVolumeClaims: resource.MustParse(envOr("LAB_QUOTA_PVC", "10")),
			},
		},
	}
	_, _ = cs.CoreV1().ResourceQuotas(ns).Create(ctx, quota, metav1.CreateOptions{})

	// LimitRange dá requests/limits default — sem isso, com ResourceQuota de
	// requests.cpu/memory ativo, todo pod sem requests explícitos é REJEITADO.
	lr := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "lab-limits", Namespace: ns},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{{
				Type: corev1.LimitTypeContainer,
				Default: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(envOr("LAB_LIMIT_CPU_DEFAULT", "250m")),
					corev1.ResourceMemory: resource.MustParse(envOr("LAB_LIMIT_MEM_DEFAULT", "256Mi")),
				},
				DefaultRequest: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse(envOr("LAB_LIMIT_CPU_REQUEST", "50m")),
					corev1.ResourceMemory: resource.MustParse(envOr("LAB_LIMIT_MEM_REQUEST", "64Mi")),
				},
			}},
		},
	}
	_, _ = cs.CoreV1().LimitRanges(ns).Create(ctx, lr, metav1.CreateOptions{})
}
