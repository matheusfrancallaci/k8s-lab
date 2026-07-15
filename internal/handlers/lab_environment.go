package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"estudo-app/internal/repository"
	"estudo-app/internal/tutor"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	labEnvironmentStore *repository.LabEnvironmentStore
	labEnvironmentMu    sync.Mutex
	labEnvironmentLocks = map[string]*sync.Mutex{}
	labCapacityMu       sync.Mutex
	labCapacityChecked  bool
)

func ensureLabClusterCapacity(ctx context.Context) error {
	if !managedIdentity() {
		return nil
	}
	labCapacityMu.Lock()
	defer labCapacityMu.Unlock()
	if labCapacityChecked {
		return nil
	}
	poolOut, err := wslCmdCtx(ctx, "az", "aks", "nodepool", "list", "-g", azRG(), "--cluster-name", aksName(),
		"--query", "[0].{name:name,enabled:enableAutoScaling,min:minCount,max:maxCount}", "-o", "json").Output()
	var poolState struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Min     int    `json:"min"`
		Max     int    `json:"max"`
	}
	if err != nil || json.Unmarshal(poolOut, &poolState) != nil || strings.TrimSpace(poolState.Name) == "" {
		return errors.New("nao consegui identificar o node pool do AKS")
	}
	desiredMax, parseErr := strconv.Atoi(aksMaxNodes())
	if parseErr != nil || desiredMax < 1 {
		return errors.New("AKS_MAX_NODES invalido")
	}
	if poolState.Enabled && poolState.Min == 1 && poolState.Max == desiredMax {
		labCapacityChecked = true
		return nil
	}
	cmd := wslCmdCtx(ctx, "az", "aks", "nodepool", "update", "-g", azRG(), "--cluster-name", aksName(), "-n", poolState.Name,
		"--enable-cluster-autoscaler", "--min-count", "1", "--max-count", strconv.Itoa(desiredMax), "--only-show-errors", "-o", "none")
	if out, updateErr := cmd.CombinedOutput(); updateErr != nil {
		return fmt.Errorf("nao consegui habilitar capacidade elastica no AKS: %s", strings.TrimSpace(string(out)))
	}
	labCapacityChecked = true
	return nil
}

func ConfigureLabEnvironmentStore(store *repository.LabEnvironmentStore) {
	labEnvironmentStore = store
}

func virtualClustersEnabled() bool {
	return managedIdentity() && os.Getenv("LAB_VCLUSTER_ENABLED") == "1"
}

func environmentLock(owner string) *sync.Mutex {
	labEnvironmentMu.Lock()
	defer labEnvironmentMu.Unlock()
	lock := labEnvironmentLocks[owner]
	if lock == nil {
		lock = &sync.Mutex{}
		labEnvironmentLocks[owner] = lock
	}
	return lock
}

func labEnvironmentNamespace(owner string) string {
	id := tutor.SanitizeID(owner)
	if len(id) > 36 {
		id = id[:36]
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "lab-" + id + "-" + hex.EncodeToString(b)
}

func beginLabEnvironment(owner, lease string, expiresAt time.Time) (*repository.LabEnvironment, error) {
	if !virtualClustersEnabled() {
		return nil, nil
	}
	if labEnvironmentStore == nil {
		return nil, errors.New("gerenciador de ambientes nao configurado")
	}
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return nil, errors.New("o tempo deste lab terminou")
	}
	lock := environmentLock(owner)
	lock.Lock()
	defer lock.Unlock()
	if current, ok := labEnvironmentStore.Current(owner, ""); ok {
		if current.Lease == lease && time.Now().Before(current.ExpiresAt) {
			return current, nil
		}
		if err := destroyLabEnvironment(*current); err != nil {
			return nil, fmt.Errorf("nao consegui remover o cluster anterior: %w", err)
		}
		if _, removed := labEnvironmentStore.End(owner, current.ID); !removed {
			return nil, errors.New("o lease anterior mudou durante a limpeza")
		}
	}
	env, replaced := labEnvironmentStore.Start(owner, lease, labEnvironmentNamespace(owner), ttl)
	if replaced != nil && replaced.ID != env.ID {
		return nil, errors.New("outro cluster foi reservado simultaneamente")
	}
	return &env, nil
}

func activeLabEnvironment(owner string) (*repository.LabEnvironment, bool) {
	if labEnvironmentStore == nil {
		return nil, false
	}
	return labEnvironmentStore.Active(owner)
}

func requireLabEnvironment(owner string) (*repository.LabEnvironment, error) {
	if !virtualClustersEnabled() {
		return nil, nil
	}
	env, ok := activeLabEnvironment(owner)
	if !ok {
		return nil, errors.New("o cluster temporario expirou; inicie um novo lab")
	}
	return env, nil
}

func vclusterValues(env repository.LabEnvironment) string {
	server := fmt.Sprintf("%s.%s.svc", env.ClusterName, env.Namespace)
	return fmt.Sprintf(`controlPlane:
  proxy:
    extraSANs:
      - %s
      - %s.cluster.local
  statefulSet:
    persistence:
      volumeClaim:
        enabled: true
        size: 1Gi
        retentionPolicy: Delete
    resources:
      requests:
        cpu: 100m
        memory: 256Mi
      limits:
        cpu: 750m
        memory: 1Gi
exportKubeConfig:
  context: k8s-lab
  server: https://%s:443
`, server, server, server)
}

func ensureVirtualEnvironmentNamespace(env repository.LabEnvironment) error {
	cs, err := k8sClientFor(currentContext())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	labels := labNamespaceSecurityLabels()
	labels["k8s-study-lab/environment"] = "true"
	labels["k8s-study-lab/environment-id"] = env.ID
	annotations := map[string]string{
		"k8s-study-lab/owner":      tutor.SanitizeID(env.Owner),
		"k8s-study-lab/lease":      env.Lease,
		"k8s-study-lab/expires-at": env.ExpiresAt.UTC().Format(time.RFC3339),
	}
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: env.Namespace, Labels: labels, Annotations: annotations}}
	current, getErr := cs.CoreV1().Namespaces().Get(ctx, env.Namespace, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		_, err = cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{})
	} else if getErr == nil {
		for key, value := range labels {
			if current.Labels == nil {
				current.Labels = map[string]string{}
			}
			current.Labels[key] = value
		}
		for key, value := range annotations {
			if current.Annotations == nil {
				current.Annotations = map[string]string{}
			}
			current.Annotations[key] = value
		}
		_, err = cs.CoreV1().Namespaces().Update(ctx, current, metav1.UpdateOptions{})
	} else {
		err = getErr
	}
	if err != nil {
		return err
	}
	ensureNamespaceLimits(env.Namespace)
	ensureNamespaceSecurity(env.Namespace)
	allow := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "lab-allow-same-environment", Namespace: env.Namespace},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{{From: []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}}},
		},
	}
	if existing, getErr := cs.NetworkingV1().NetworkPolicies(env.Namespace).Get(ctx, allow.Name, metav1.GetOptions{}); getErr == nil {
		allow.ResourceVersion = existing.ResourceVersion
		_, _ = cs.NetworkingV1().NetworkPolicies(env.Namespace).Update(ctx, allow, metav1.UpdateOptions{})
	} else {
		_, _ = cs.NetworkingV1().NetworkPolicies(env.Namespace).Create(ctx, allow, metav1.CreateOptions{})
	}
	return nil
}

func provisionLabEnvironment(ctx context.Context, owner string, report func(string)) (*repository.LabEnvironment, error) {
	if !virtualClustersEnabled() {
		return nil, nil
	}
	lock := environmentLock(owner)
	lock.Lock()
	defer lock.Unlock()
	env, err := requireLabEnvironment(owner)
	if err != nil {
		return nil, err
	}
	if !env.ReadyAt.IsZero() {
		return env, nil
	}
	if report == nil {
		report = func(string) {}
	}
	report("ligando o cluster base e reservando seu ambiente isolado...")
	if err := EnsureClusterReady(ctx); err != nil {
		return nil, fmt.Errorf("cluster base nao ficou pronto: %w", err)
	}
	if err := ensureLabClusterCapacity(ctx); err != nil {
		return nil, err
	}
	if err := ensureVirtualEnvironmentNamespace(*env); err != nil {
		return nil, fmt.Errorf("namespace do ambiente nao ficou pronto: %w", err)
	}
	valuesPath := filepath.Join(os.TempDir(), "vcluster-"+env.ID+".yaml")
	if err := os.WriteFile(valuesPath, []byte(vclusterValues(*env)), 0o600); err != nil {
		return nil, err
	}
	defer os.Remove(valuesPath)
	report("criando um Kubernetes virtual exclusivo para voce...")
	cmd := wslCmdCtx(ctx, "vcluster", "create", env.ClusterName,
		"--namespace", env.Namespace,
		"--driver", "helm",
		"--chart-version", envOr("VCLUSTER_CHART_VERSION", "0.35.1"),
		"--connect=false", "--background-proxy=false", "--add=false", "--upgrade",
		"--values", valuesPath)
	if out, cmdErr := cmd.CombinedOutput(); cmdErr != nil {
		return nil, fmt.Errorf("vcluster nao ficou pronto: %s", strings.TrimSpace(string(out)))
	}
	report("validando API server, RBAC e kubeconfig do seu cluster...")
	secret := "vc-" + env.ClusterName
	wait := fmt.Sprintf("for i in $(seq 1 60); do kubectl -n %s get secret %s -o jsonpath='{.data.config}' 2>/dev/null | grep -q . && exit 0; sleep 2; done; exit 1", env.Namespace, secret)
	if out, waitErr := wslShellCtx(ctx, wait).CombinedOutput(); waitErr != nil {
		return nil, fmt.Errorf("kubeconfig do cluster nao foi publicado: %s", strings.TrimSpace(string(out)))
	}
	if !labEnvironmentStore.MarkReady(owner, env.ID) {
		return nil, errors.New("o ambiente expirou durante o provisionamento")
	}
	env, _ = activeLabEnvironment(owner)
	return env, nil
}

func destroyLabEnvironment(env repository.LabEnvironment) error {
	if strings.TrimSpace(env.Namespace) == "" || !strings.HasPrefix(env.Namespace, "lab-") {
		return errors.New("namespace temporario invalido")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	if err := EnsureClusterReady(ctx); err != nil {
		return fmt.Errorf("cluster base indisponivel para limpeza: %w", err)
	}
	cmd := wslCmdCtx(ctx, "kubectl", "delete", "namespace", env.Namespace,
		"--ignore-not-found=true", "--wait=true", "--timeout=5m")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("falha ao remover %s: %s", env.Namespace, strings.TrimSpace(string(out)))
	}
	forgetCloudShell(env.Owner)
	log.Printf("[lab-environment] cluster temporario removido: %s", env.Namespace)
	return nil
}

func endLabEnvironment(owner, id string) bool {
	if labEnvironmentStore == nil {
		return false
	}
	lock := environmentLock(owner)
	lock.Lock()
	defer lock.Unlock()
	env, ok := labEnvironmentStore.Current(owner, id)
	if !ok {
		return true
	}
	if err := destroyLabEnvironment(*env); err != nil {
		log.Printf("[lab-environment] limpeza pendente para %s: %v", env.Namespace, err)
		return false
	}
	_, removed := labEnvironmentStore.End(owner, env.ID)
	return removed
}

func StartLabEnvironmentGC() {
	if labEnvironmentStore == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			for _, env := range labEnvironmentStore.Expired() {
				endLabEnvironment(env.Owner, env.ID)
			}
		}
	}()
}

func (h *LabHandler) EnvironmentStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	env, ok := activeLabEnvironment(userID(r))
	if !ok {
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]any{"active": false, "expired": true}) //nolint:errcheck
		return
	}
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"active": true, "id": env.ID, "cluster": env.ClusterName,
		"namespace": env.Namespace, "created_at": env.CreatedAt, "expires_at": env.ExpiresAt,
		"ready": !env.ReadyAt.IsZero(), "ttl_seconds": max(0, int(time.Until(env.ExpiresAt).Seconds())),
	})
}

func (h *LabHandler) EndEnvironment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	uid := userID(r)
	requested := strings.TrimSpace(r.URL.Query().Get("id"))
	if requested != "" && labEnvironmentStore != nil {
		success := endLabEnvironment(uid, requested)
		if !success {
			w.WriteHeader(http.StatusAccepted)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": success, "cleanup_pending": !success}) //nolint:errcheck
		return
	}
	if env, ok := activeLabEnvironment(uid); ok {
		json.NewEncoder(w).Encode(map[string]any{"success": endLabEnvironment(uid, env.ID)}) //nolint:errcheck
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"success": true, "already_ended": true}) //nolint:errcheck
}
