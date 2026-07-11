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
	networkingv1 "k8s.io/api/networking/v1"
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

const labCommandNamespace = "default"

func userLabNamespace(userID string) string {
	id := tutor.SanitizeID(userID)
	if id == "default" {
		return ""
	}
	return "lab-" + id
}

// userKubeconfig devolve o caminho de um kubeconfig multi-user (criando
// lab-<user> se preciso). O namespace atual fica em "default" para preservar o
// comportamento esperado dos labs de certificação; o namespace privado é exposto
// via LAB_NAMESPACE para labs que precisem de isolamento explícito.
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
	ns := userLabNamespace(userID)
	if err := ensureNamespace(ns); err != nil {
		return "" // sem namespace -> sem isolamento (cai no padrão)
	}
	base := os.Getenv("KUBECONFIG")
	if base == "" {
		base = "/etc/rancher/k3s/k3s.yaml"
	}
	path := filepath.Join(os.TempDir(), "kc-"+id+".yaml")
	// Copia o kubeconfig base e seta o namespace atual como default. Muitos labs
	// oficiais de CKA/CKAD dizem "namespace default" e usam comandos sem -n; se
	// apontarmos silenciosamente para lab-<user>, o aluno cria em um namespace e
	// o validador procura em outro.
	// Usa a env KUBECONFIG (não o flag --kubeconfig) porque a base pode ser
	// mesclada (k3s local : AKS) com ':', que só a env aceita.
	script := fmt.Sprintf(
		"KUBECONFIG=%s kubectl config view --raw > %s && KUBECONFIG=%s kubectl config set-context --current --namespace=%s",
		base, path, path, labCommandNamespace)
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
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labNamespaceSecurityLabels()}},
		metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	ensureNamespaceLimits(name)
	ensureNamespaceSecurity(name)
	return nil
}

func labNamespaceSecurityLabels() map[string]string {
	return map[string]string{
		"pod-security.kubernetes.io/enforce":         envOr("LAB_PSA_ENFORCE", "baseline"),
		"pod-security.kubernetes.io/enforce-version": envOr("LAB_PSA_VERSION", "latest"),
		"app.kubernetes.io/part-of":                  "k8s-study-lab",
		"k8s-study-lab/user-namespace":               "true",
	}
}

// namespaceGuardrailsScript mirrors phase-0 guardrails when the app cannot use
// client-go and provisions the cloud shell through kubectl instead.
func namespaceGuardrailsScript(ns string) string {
	if ns == "" {
		return ""
	}
	return fmt.Sprintf(`kubectl label namespace %s pod-security.kubernetes.io/enforce=%s pod-security.kubernetes.io/enforce-version=%s app.kubernetes.io/part-of=k8s-study-lab k8s-study-lab/user-namespace=true --overwrite 2>/dev/null; kubectl -n %s apply -f - <<'K8SLABPOLICY' 2>/dev/null
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: lab-default-deny-ingress
spec:
  podSelector: {}
  policyTypes:
  - Ingress
K8SLABPOLICY`, ns, envOr("LAB_PSA_ENFORCE", "baseline"), envOr("LAB_PSA_VERSION", "latest"), ns)
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
	if current, err := cs.CoreV1().ResourceQuotas(ns).Get(ctx, quota.Name, metav1.GetOptions{}); err == nil {
		quota.ResourceVersion = current.ResourceVersion
		_, _ = cs.CoreV1().ResourceQuotas(ns).Update(ctx, quota, metav1.UpdateOptions{})
	} else {
		_, _ = cs.CoreV1().ResourceQuotas(ns).Create(ctx, quota, metav1.CreateOptions{})
	}

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
	if current, err := cs.CoreV1().LimitRanges(ns).Get(ctx, lr.Name, metav1.GetOptions{}); err == nil {
		lr.ResourceVersion = current.ResourceVersion
		_, _ = cs.CoreV1().LimitRanges(ns).Update(ctx, lr, metav1.UpdateOptions{})
	} else {
		_, _ = cs.CoreV1().LimitRanges(ns).Create(ctx, lr, metav1.CreateOptions{})
	}
}

// ensureNamespaceSecurity completes phase 0 for lab-* namespaces. The policy
// blocks traffic from other user namespaces while preserving normal egress to
// DNS, the Kubernetes API, and public documentation/images. CNI enforcement is
// cluster-dependent, so the namespace labels and quotas remain useful on their
// own even when NetworkPolicy is not implemented by the local cluster.
func ensureNamespaceSecurity(ns string) {
	cs, err := k8sClientFor(currentContext())
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if current, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err == nil {
		labels := current.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		for k, v := range labNamespaceSecurityLabels() {
			labels[k] = v
		}
		current.SetLabels(labels)
		_, _ = cs.CoreV1().Namespaces().Update(ctx, current, metav1.UpdateOptions{})
	}
	policy := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "lab-default-deny-ingress", Namespace: ns},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	}
	if current, err := cs.NetworkingV1().NetworkPolicies(ns).Get(ctx, policy.Name, metav1.GetOptions{}); err == nil {
		policy.ResourceVersion = current.ResourceVersion
		_, _ = cs.NetworkingV1().NetworkPolicies(ns).Update(ctx, policy, metav1.UpdateOptions{})
	} else {
		_, _ = cs.NetworkingV1().NetworkPolicies(ns).Create(ctx, policy, metav1.CreateOptions{})
	}
}
