package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// ttlCache armazena dados por um prazo fixo para reduzir chamadas caras.
type ttlCache[T any] struct {
	mu     sync.Mutex
	values map[string]cachedValue[T]
	ttl    time.Duration
}

type cachedValue[T any] struct {
	value    T
	expireAt time.Time
}

func newTTLCache[T any](ttl time.Duration) *ttlCache[T] {
	return &ttlCache[T]{ttl: ttl, values: map[string]cachedValue[T]{}}
}

func (c *ttlCache[T]) Set(key string, v T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values[key] = cachedValue[T]{value: v, expireAt: time.Now().Add(c.ttl)}
}

func (c *ttlCache[T]) Get(key string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.values[key]
	if !ok || time.Now().After(entry.expireAt) {
		var zero T
		return zero, false
	}
	return entry.value, true
}

var cloudStatusCache = newTTLCache[CloudStatus](20 * time.Second)

// ─────────────────────────────────────────────────────────────────────────────
// Configuração do cluster AKS (com override por variável de ambiente).
// Tudo é barato por padrão: 1 nó Standard_B2s, tier Free, sem addons.
// ─────────────────────────────────────────────────────────────────────────────

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func azRG() string     { return envOr("AZURE_RG", "k8s-study-lab-rg") }
func aksName() string  { return envOr("AKS_NAME", "k8s-study-lab") }
func azRegion() string { return envOr("AZURE_REGION", "eastus") }

// standard_d2als_v7: o SKU barato (2 vCPU/4GB AMD) permitido em subscriptions
// gratuitas novas — a série B (B2s) não é liberada nelas.
func aksNodeSize() string { return envOr("AKS_NODE_SIZE", "standard_d2als_v7") }

func cloudIdleMinutes() int {
	v := strings.TrimSpace(os.Getenv("CLOUD_IDLE_MINUTES"))
	if v == "" {
		return 45
	}
	n := 0
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return 45
	}
	return n
}

const localContext = "minikube"

// nomes de contexto kubectl válidos (vão para o shell — nada de metacaracteres)
var contextNameRe = regexp.MustCompile(`^[A-Za-z0-9_.:/@-]{1,128}$`)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers de estado
// ─────────────────────────────────────────────────────────────────────────────

func azInstalled() bool {
	_, err := wslCmd("which", "az").Output()
	return err == nil
}

// managedIdentity indica que o app roda numa VM Azure com identidade gerenciada
// (definido por AZURE_MANAGED_IDENTITY=1 na instância hospedada). Nesse modo o
// login é `az login --identity` — instantâneo, sem device-code.
func managedIdentity() bool { return os.Getenv("AZURE_MANAGED_IDENTITY") == "1" }

// EnsureAzureLogin faz login automático via identidade da VM no boot da instância
// hospedada, para a página Cloud já aparecer conectada (sem ação do usuário).
// No-op fora do modo hospedado ou se já estiver logado.
func EnsureAzureLogin() {
	if !managedIdentity() || !azInstalled() || azSubscription() != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := wslShellCtx(ctx, "az login --identity --only-show-errors 2>&1").Run(); err != nil {
		return
	}
	// Se já existe um AKS, conecta o kubectl a ele de uma vez.
	if aksExists() {
		wslShellCtx(ctx, fmt.Sprintf(
			"az aks get-credentials -g %s -n %s --overwrite-existing --only-show-errors 2>&1",
			azRG(), aksName())).Run()
	}
}

// azSubscription retorna o nome da subscription se logado, ou "" se não logado.
func azSubscription() string {
	out, err := wslShell("az account show --query name -o tsv 2>/dev/null").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func aksExists() bool {
	cmd := wslShell(fmt.Sprintf(
		"az aks show -g %s -n %s --query name -o tsv 2>/dev/null", azRG(), aksName()))
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// aksPowerState retorna "Running", "Stopped" ou "" (desconhecido/inexistente).
func aksPowerState() string {
	out, err := wslShell(fmt.Sprintf(
		"az aks show -g %s -n %s --query powerState.code -o tsv 2>/dev/null", azRG(), aksName())).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// currentContext retorna o contexto ativo do kubectl (ex.: "minikube" ou o nome do AKS).
func currentContext() string {
	out, err := wslShell("kubectl config current-context 2>/dev/null").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CurrentContext expõe o contexto ativo do kubectl para outros pacotes.
func CurrentContext() string { return currentContext() }

// CloudStatus é o JSON retornado pelo endpoint de status.
type CloudStatus struct {
	AzInstalled   bool   `json:"az_installed"`
	LoggedIn      bool   `json:"logged_in"`
	Subscription  string `json:"subscription"`
	ClusterExists bool   `json:"cluster_exists"`
	PowerState    string `json:"power_state"`
	Connected     bool   `json:"connected"` // contexto atual == nome do AKS
	ActiveContext string `json:"active_context"`
	ClusterName   string `json:"cluster_name"`
	NodeSize      string `json:"node_size"`
	Region        string `json:"region"`
	IdleMinutes   int    `json:"idle_minutes"`
}

func getCloudStatus() CloudStatus {
	s := CloudStatus{
		ClusterName: aksName(),
		NodeSize:    aksNodeSize(),
		Region:      azRegion(),
		IdleMinutes: cloudIdleMinutes(),
	}
	s.AzInstalled = azInstalled()
	if !s.AzInstalled {
		return s
	}
	s.Subscription = azSubscription()
	s.LoggedIn = s.Subscription != ""
	s.ActiveContext = currentContext()
	if !s.LoggedIn {
		return s
	}
	s.ClusterExists = aksExists()
	if s.ClusterExists {
		s.PowerState = aksPowerState()
	}
	s.Connected = s.ActiveContext == aksName()
	return s
}

// CloudStatusHandler retorna o estado atual da nuvem como JSON.
func CloudStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if cached, ok := cloudStatusCache.Get("cloud-status"); ok {
		json.NewEncoder(w).Encode(cached)
		return
	}
	status := getCloudStatus()
	cloudStatusCache.Set("cloud-status", status)
	json.NewEncoder(w).Encode(status)
}

// ─────────────────────────────────────────────────────────────────────────────
// SSE — streaming ao vivo de processos longos (login device-code, aks create)
// ─────────────────────────────────────────────────────────────────────────────

type sseWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func newSSE(w http.ResponseWriter) (*sseWriter, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	return &sseWriter{w: w, flusher: flusher}, true
}

func (s *sseWriter) send(data map[string]any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(s.w, "data: %s\n\n", b)
	s.flusher.Flush()
}

// streamShell roda um comando de shell WSL vivo e emite cada linha de saída como
// um evento SSE {type:"line", line:"..."}. Retorna o erro final do processo.
func streamShell(ctx context.Context, s *sseWriter, cmdStr string) error {
	cmd := wslShellCtx(ctx, cmdStr)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout // funde stderr no mesmo pipe (device-code sai no stderr)
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		s.send(map[string]any{"type": "line", "line": scanner.Text()})
	}
	return cmd.Wait()
}

// ─────────────────────────────────────────────────────────────────────────────
// Handlers de ação
// ─────────────────────────────────────────────────────────────────────────────

// CloudInstallAzHandler instala o Azure CLI dentro do WSL (best-effort).
func CloudInstallAzHandler(w http.ResponseWriter, r *http.Request) {
	s, ok := newSSE(w)
	if !ok {
		return
	}
	if azInstalled() {
		s.send(map[string]any{"type": "line", "line": "Azure CLI já está instalado."})
		s.send(map[string]any{"type": "done", "ok": true})
		return
	}
	s.send(map[string]any{"type": "line", "line": "Instalando Azure CLI no WSL (pode pedir sudo)..."})
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	err := streamShell(ctx, s, "curl -sL https://aka.ms/InstallAzureCLIDeb | sudo -n bash 2>&1")
	if err != nil || !azInstalled() {
		s.send(map[string]any{"type": "line", "line": ""})
		s.send(map[string]any{"type": "line", "line": "Instalação automática falhou (provavelmente sudo pediu senha)."})
		s.send(map[string]any{"type": "line", "line": "Rode manualmente no terminal do lab:"})
		s.send(map[string]any{"type": "line", "line": "  curl -sL https://aka.ms/InstallAzureCLIDeb | sudo bash"})
		s.send(map[string]any{"type": "done", "ok": azInstalled()})
		return
	}
	s.send(map[string]any{"type": "done", "ok": true})
}

// CloudLoginHandler executa 'az login --use-device-code' streamando URL + código.
func CloudLoginHandler(w http.ResponseWriter, r *http.Request) {
	s, ok := newSSE(w)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	// Instância hospedada: usa a identidade gerenciada da VM — sem device-code.
	loginCmd := "az login --use-device-code --only-show-errors 2>&1"
	if managedIdentity() {
		s.send(map[string]any{"type": "line", "line": "Autenticando com a identidade da VM (sem device-code)..."})
		loginCmd = "az login --identity --only-show-errors 2>&1"
	}
	err := streamShell(ctx, s, loginCmd)
	sub := azSubscription()
	s.send(map[string]any{
		"type": "done", "ok": err == nil && sub != "", "subscription": sub,
	})
}

// CloudCreateHandler cria o grupo de recursos + o cluster AKS e conecta o kubectl.
func CloudCreateHandler(w http.ResponseWriter, r *http.Request) {
	s, ok := newSSE(w)
	if !ok {
		return
	}
	if !azInstalled() || azSubscription() == "" {
		s.send(map[string]any{"type": "line", "line": "Faça login no Azure primeiro."})
		s.send(map[string]any{"type": "done", "ok": false})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Minute)
	defer cancel()

	steps := []struct{ desc, cmd string }{
		{
			fmt.Sprintf("Criando resource group %s (%s)", azRG(), azRegion()),
			fmt.Sprintf("az group create -n %s -l %s --only-show-errors -o none 2>&1 && echo OK",
				azRG(), azRegion()),
		},
		{
			fmt.Sprintf("Criando cluster AKS %s (1x %s, tier free) — 5 a 10 min...", aksName(), aksNodeSize()),
			fmt.Sprintf("az aks create -g %s -n %s --node-count 1 --node-vm-size %s "+
				"--tier free --generate-ssh-keys --load-balancer-sku standard "+
				"--only-show-errors -o none 2>&1 && echo OK",
				azRG(), aksName(), aksNodeSize()),
		},
		{
			"Configurando credenciais do kubectl",
			fmt.Sprintf("az aks get-credentials -g %s -n %s --overwrite-existing --only-show-errors 2>&1 && echo OK",
				azRG(), aksName()),
		},
		{
			"Definindo o AKS como contexto ativo",
			fmt.Sprintf("kubectl config use-context %s 2>&1", aksName()),
		},
	}

	for i, st := range steps {
		s.send(map[string]any{"type": "step", "index": i, "total": len(steps),
			"desc": st.desc, "status": "running"})
		err := streamShell(ctx, s, st.cmd)
		status := "done"
		if err != nil {
			status = "warn"
		}
		s.send(map[string]any{"type": "step", "index": i, "total": len(steps),
			"desc": st.desc, "status": status})
	}

	st := getCloudStatus()
	statusBytes, _ := json.Marshal(st)
	s.send(map[string]any{"type": "done", "ok": st.ClusterExists && st.Connected,
		"status": string(statusBytes)})
}

// runAndJSON executa um comando de shell WSL e responde {success, output}.
func runAndJSON(w http.ResponseWriter, cmdStr string, timeout time.Duration) {
	w.Header().Set("Content-Type", "application/json")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := wslShellCtx(ctx, cmdStr).CombinedOutput()
	json.NewEncoder(w).Encode(map[string]any{
		"success": err == nil,
		"output":  strings.TrimSpace(string(out)),
	})
}

// CloudStartHandler liga o cluster AKS (retomando o faturamento de compute).
func CloudStartHandler(w http.ResponseWriter, r *http.Request) {
	touchActivity()
	cmd := fmt.Sprintf(
		"az aks start -g %s -n %s --only-show-errors 2>&1 && "+
			"az aks get-credentials -g %s -n %s --overwrite-existing --only-show-errors 2>&1 && "+
			"kubectl config use-context %s 2>&1",
		azRG(), aksName(), azRG(), aksName(), aksName())
	runAndJSON(w, cmd, 10*time.Minute)
}

// CloudStopHandler para o cluster AKS (pausa o faturamento de compute).
func CloudStopHandler(w http.ResponseWriter, r *http.Request) {
	cmd := fmt.Sprintf("az aks stop -g %s -n %s --only-show-errors 2>&1", azRG(), aksName())
	runAndJSON(w, cmd, 10*time.Minute)
}

// CloudDeleteHandler exclui o cluster AKS (a UI confirma antes).
func CloudDeleteHandler(w http.ResponseWriter, r *http.Request) {
	cmd := fmt.Sprintf("az aks delete -g %s -n %s --yes --only-show-errors 2>&1", azRG(), aksName())
	runAndJSON(w, cmd, 15*time.Minute)
}

// CloudConnectHandler conecta o kubectl a um AKS já existente e o torna o contexto ativo.
func CloudConnectHandler(w http.ResponseWriter, r *http.Request) {
	touchActivity()
	cmd := fmt.Sprintf(
		"az aks get-credentials -g %s -n %s --overwrite-existing --only-show-errors 2>&1 && "+
			"kubectl config use-context %s 2>&1",
		azRG(), aksName(), aksName())
	runAndJSON(w, cmd, 2*time.Minute)
}

// ContextsHandler lista todos os contextos do kubeconfig — é o que torna o
// lab multi-cloud: EKS, GKE, OKE, k3s... qualquer cluster conectável vira alvo.
func ContextsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	out, _ := wslShell("kubectl config get-contexts -o name 2>/dev/null").Output()
	var contexts []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			contexts = append(contexts, l)
		}
	}
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"contexts": contexts,
		"current":  currentContext(),
		"local":    localContext,
		"aks":      aksName(),
	})
}

// SetContextHandler troca o contexto ativo do kubectl: local, o AKS gerenciado,
// ou QUALQUER contexto nomeado do kubeconfig (multi-cloud).
func SetContextHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Target  string `json:"target"`  // "local" | "cloud"
		Context string `json:"context"` // nome explícito (tem precedência)
	}
	json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck

	ctxName := localContext
	switch {
	case body.Context != "":
		ctxName = body.Context
	case body.Target == "cloud":
		ctxName = aksName()
	}
	// Segurança: nomes de contexto vão para o shell — só caracteres válidos.
	if !contextNameRe.MatchString(ctxName) {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "output": "nome de contexto inválido"}) //nolint:errcheck
		return
	}
	touchActivity()
	out, err := wslShell(fmt.Sprintf("kubectl config use-context %s 2>&1", ctxName)).CombinedOutput()
	json.NewEncoder(w).Encode(map[string]any{
		"success": err == nil,
		"context": ctxName,
		"output":  strings.TrimSpace(string(out)),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Rastreio de ociosidade + auto-stop (controle de custo)
// ─────────────────────────────────────────────────────────────────────────────

var (
	activityMu   sync.Mutex
	lastActivity = time.Now()
)

// touchActivity marca atividade do usuário (terminal, validações de lab, ações cloud).
func touchActivity() {
	activityMu.Lock()
	lastActivity = time.Now()
	activityMu.Unlock()
}

// UserActivity registra uso real depois do gate de autenticacao. Probes,
// assets e o proprio poll do auto-stop ficam de fora para que monitoramento e
// trafego anonimo nao mantenham a VM ligada. Tutor e catalogo de labs passam a
// contar como atividade mesmo antes de o aluno abrir um terminal.
func UserActivity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if p != "/healthz" && p != "/readyz" && p != "/metrics" && p != "/api/idle" &&
			p != "/login" && p != "/register" && !strings.HasPrefix(p, "/static/") {
			touchActivity()
		}
		next.ServeHTTP(w, r)
	})
}

func idleFor() time.Duration {
	activityMu.Lock()
	defer activityMu.Unlock()
	return time.Since(lastActivity)
}

// IdleHandler expõe a ociosidade para o auto-stop da VM (timer no host lê isto).
// Público (sem login): só revela segundos ociosos e nº de terminais — nada sensível.
func IdleHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"idle_seconds":     int(idleFor().Seconds()),
		"active_terminals": ActiveTerminals(),
	})
}

// StartCloudMonitor liga um monitor que para o AKS após inatividade prolongada,
// evitando faturar um cluster esquecido ligado. Só age quando o contexto ativo é
// o AKS e o cluster está Running.
func StartCloudMonitor() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		if currentContext() != aksName() {
			continue
		}
		// Terminal aberto = usuário estudando; nunca parar por baixo dele.
		if ActiveTerminals() > 0 {
			touchActivity()
			continue
		}
		if idleFor() < time.Duration(cloudIdleMinutes())*time.Minute {
			continue
		}
		if aksPowerState() != "Running" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		wslShellCtx(ctx, fmt.Sprintf(
			"az aks stop -g %s -n %s --only-show-errors 2>&1", azRG(), aksName())).Run()
		cancel()
		touchActivity() // evita re-disparo imediato
	}
}

// ContextTestHandler testa a conectividade de um contexto específico sem
// trocar o alvo ativo (kubectl --context <nome> get nodes).
func ContextTestHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Context string `json:"context"`
	}
	json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
	if !contextNameRe.MatchString(body.Context) {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "output": "nome de contexto inválido"}) //nolint:errcheck
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := wslShellCtx(ctx, fmt.Sprintf(
		"kubectl --context %s get nodes --request-timeout=15s 2>&1 | head -5", body.Context)).CombinedOutput()
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"success": err == nil,
		"output":  strings.TrimSpace(string(out)),
	})
}
