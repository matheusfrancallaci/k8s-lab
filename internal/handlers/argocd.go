package handlers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	argoCDMu        sync.Mutex
	argoCDPFActive  bool
	argoCDPFScheme  = "http"
	argoCDLocalPort = 8090
	argoCDPFProc    *exec.Cmd // tracked Go subprocess — survives shell exit
)

var argoCDSteps = []struct {
	Desc string
	Cmd  string
}{
	{"Criando namespace argocd", "kubectl create namespace argocd 2>/dev/null || true"},
	{"Instalando ArgoCD (pode demorar 2-3 min...)", "kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml 2>&1 | tail -5"},
	{"Aguardando argocd-server ficar pronto", "kubectl wait deployment argocd-server -n argocd --for=condition=available --timeout=300s"},
	{"Configurando serviço NodePort", "kubectl patch svc argocd-server -n argocd -p '{\"spec\":{\"type\":\"NodePort\"}}' 2>/dev/null || true"},
	// Guards com grep: reinstalar não pode duplicar flags no deployment.
	{"Habilitando acesso HTTP", "kubectl get deployment argocd-server -n argocd -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q insecure || kubectl patch deployment argocd-server -n argocd --type=json -p='[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/args/-\",\"value\":\"--insecure\"}]' 2>/dev/null || true"},
	{"Servindo a UI sob /argocd (proxy da app)", "kubectl get deployment argocd-server -n argocd -o jsonpath='{.spec.template.spec.containers[0].args}' | grep -q rootpath || kubectl patch deployment argocd-server -n argocd --type=json -p='[{\"op\":\"add\",\"path\":\"/spec/template/spec/containers/0/args/-\",\"value\":\"--rootpath=/argocd\"}]' 2>/dev/null || true"},
	{"Aguardando reinício do servidor", "kubectl rollout status deployment/argocd-server -n argocd --timeout=120s 2>/dev/null || true"},
}

// ArgoCDStatus is returned by the status endpoint.
type ArgoCDStatus struct {
	Installed bool   `json:"installed"`
	Ready     bool   `json:"ready"`
	PFActive  bool   `json:"pf_active"`
	URL       string `json:"url"`
	Password  string `json:"password"`
}

func getArgoCDStatus() ArgoCDStatus {
	nsOut, err := wslShell("kubectl get namespace argocd -o jsonpath='{.status.phase}' 2>/dev/null").Output()
	if err != nil || !strings.Contains(string(nsOut), "Active") {
		return ArgoCDStatus{Installed: false}
	}

	readyOut, _ := wslShell("kubectl get deployment argocd-server -n argocd -o jsonpath='{.status.availableReplicas}' 2>/dev/null").Output()
	ready := strings.TrimSpace(string(readyOut)) != "" && strings.TrimSpace(string(readyOut)) != "0"

	pwOut, _ := wslShell("kubectl get secret argocd-initial-admin-secret -n argocd -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null").Output()
	password := strings.TrimSpace(string(pwOut))

	argoCDMu.Lock()
	pfActive := argoCDPFActive
	scheme := argoCDPFScheme
	argoCDMu.Unlock()

	_ = scheme
	// URL relativa: o browser chega ao ArgoCD PELA APP (/argocd/ → proxy →
	// port-forward interno). "localhost:8090" só existia para quem roda tudo
	// local — na instância hospedada essa porta nunca alcança o usuário.
	url := ""
	if pfActive {
		url = "/argocd/"
	}

	return ArgoCDStatus{
		Installed: true,
		Ready:     ready,
		PFActive:  pfActive,
		URL:       url,
		Password:  password,
	}
}

// ArgoCDStatusHandler returns current ArgoCD state as JSON.
func ArgoCDStatusHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(getArgoCDStatus())
}

// ArgoCDInstallHandler streams installation progress via SSE.
func ArgoCDInstallHandler(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	sendEvent := func(data map[string]any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}

	total := len(argoCDSteps) + 1 // +1 for port-forward step

	for i, step := range argoCDSteps {
		sendEvent(map[string]any{
			"type": "step", "index": i, "total": total,
			"desc": step.Desc, "status": "running",
		})

		out, err := wslShell(step.Cmd).CombinedOutput()
		status := "done"
		if err != nil {
			output := strings.ToLower(string(out))
			if !strings.Contains(output, "already exists") &&
				!strings.Contains(output, "unchanged") &&
				!strings.Contains(output, "no change") {
				status = "warn"
			}
		}

		sendEvent(map[string]any{
			"type": "step", "index": i, "total": total,
			"desc": step.Desc, "status": status,
			"output": strings.TrimSpace(string(out)),
		})
	}

	// Final step: port-forward
	pfIdx := len(argoCDSteps)
	sendEvent(map[string]any{
		"type": "step", "index": pfIdx, "total": total,
		"desc": fmt.Sprintf("Iniciando acesso externo (porta %d)", argoCDLocalPort), "status": "running",
	})

	pfErr := doStartPortForward()
	pfStatus := "done"
	pfDesc := "ArgoCD acessível em /argocd/ (atrás do seu login)"
	if pfErr != nil {
		pfStatus = "warn"
		pfDesc = "Port-forward falhou — tente reiniciar manualmente na página ArgoCD"
	}
	sendEvent(map[string]any{
		"type": "step", "index": pfIdx, "total": total,
		"desc":   pfDesc,
		"status": pfStatus,
	})

	// Emit final done event with full status
	finalStatus := getArgoCDStatus()
	statusBytes, _ := json.Marshal(finalStatus)
	sendEvent(map[string]any{
		"type":   "done",
		"status": string(statusBytes),
	})
}

// ArgoCDPortForwardHandler starts or stops the kubectl port-forward.
func ArgoCDPortForwardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodDelete {
		doStopPortForward()
		json.NewEncoder(w).Encode(map[string]any{"success": true})
		return
	}

	// POST: start
	if err := doStartPortForward(); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"success": false, "error": err.Error()})
		return
	}

	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"url":     "/argocd/",
	})
}

// ArgoCDProxyHandler serve a UI do ArgoCD pela própria app, atrás do login.
// O port-forward vive DENTRO do processo/container; expor a UI como caminho
// da app (/argocd/) é o único jeito de ela funcionar tanto local quanto na
// instância hospedada. Exige argocd-server com --rootpath=/argocd (garantido
// no install e em ensureArgoCDRootpathLocked para instalações antigas).
func ArgoCDProxyHandler(w http.ResponseWriter, r *http.Request) {
	argoCDMu.Lock()
	active := argoCDPFActive
	argoCDMu.Unlock()
	if !active {
		if err := doStartPortForward(); err != nil {
			http.Error(w, "ArgoCD indisponível: "+err.Error()+". Instale/reinicie na página /argocd.", http.StatusServiceUnavailable)
			return
		}
	}
	argoCDMu.Lock()
	scheme := argoCDPFScheme
	port := argoCDLocalPort // sob o mutex: a porta rotaciona a cada restart
	argoCDMu.Unlock()
	target, err := url.Parse(fmt.Sprintf("%s://127.0.0.1:%d", scheme, port))
	if err != nil {
		http.Error(w, "proxy indisponível", http.StatusServiceUnavailable)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	if scheme == "https" {
		// fallback 443: o cert interno do argocd-server é self-signed
		proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	proxy.ServeHTTP(w, r)
}

// ensureArgoCDFlagsLocked cura instalações antigas do argocd-server (caller
// segura argoCDMu): --insecure é OBRIGATÓRIO atrás do proxy — sem ele o
// listener http responde 307 para https e o browser entra em loop infinito
// (ERR_TOO_MANY_REDIRECTS, visto em produção 2026-07-12); --rootpath=/argocd
// põe a UI sob o caminho da app. Guard por flag; rollout só se patchou.
func ensureArgoCDFlagsLocked() {
	out, err := wslShell("kubectl get deployment argocd-server -n argocd -o jsonpath='{.spec.template.spec.containers[0].args}' 2>/dev/null").Output()
	if err != nil {
		return
	}
	args := string(out)
	patched := false
	if !strings.Contains(args, "insecure") {
		wslShell(`kubectl patch deployment argocd-server -n argocd --type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--insecure"}]'`).Run() //nolint:errcheck
		patched = true
	}
	if !strings.Contains(args, "rootpath") {
		wslShell(`kubectl patch deployment argocd-server -n argocd --type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--rootpath=/argocd"}]'`).Run() //nolint:errcheck
		patched = true
	}
	if patched {
		wslShell("kubectl rollout status deployment/argocd-server -n argocd --timeout=120s 2>/dev/null").Run() //nolint:errcheck
	}
}

// doStopPortForwardLocked kills the tracked subprocess. Caller must hold argoCDMu.
// Does NOT call Wait — the monitor goroutine owns that to avoid a double-Wait race.
func doStopPortForwardLocked() {
	argoCDPFActive = false
	if argoCDPFProc != nil {
		argoCDPFProc.Process.Kill() //nolint:errcheck
		argoCDPFProc = nil
	}
	// Kill any stragglers started by older nohup-based runs
	wslShell("pkill -f 'kubectl port-forward.*argocd' 2>/dev/null").Run() //nolint:errcheck
}

func doStartPortForward() error {
	argoCDMu.Lock()
	defer argoCDMu.Unlock()

	ensureArgoCDFlagsLocked()
	doStopPortForwardLocked()
	time.Sleep(400 * time.Millisecond)

	// Try port 80 (HTTP, requires --insecure patch) then fall back to 443 (HTTPS).
	// --address=0.0.0.0 ensures the port is reachable from Windows even without WSL
	// mirrored-networking, because it binds on all WSL interfaces.
	// A porta LOCAL rotaciona a cada start (8090-8093): o kill do pf anterior
	// pode deixar o socket preso (TIME_WAIT/filho órfão do sh) e o rebind na
	// MESMA porta falhava para sempre — "reiniciar port-forward" matava o
	// ArgoCD até reiniciar o app (visto em produção 2026-07-12). Status e
	// proxy leem argoCDLocalPort sob o mutex, então seguem a rotação.
	argoCDLocalPort++
	if argoCDLocalPort > 8093 || argoCDLocalPort < 8090 {
		argoCDLocalPort = 8090
	}
	for _, target := range []struct {
		svcPort int
		scheme  string
	}{
		{80, "http"},
		{443, "https"},
	} {
		cmd := wslShell(fmt.Sprintf(
			"kubectl port-forward svc/argocd-server -n argocd --address=0.0.0.0 %d:%d",
			argoCDLocalPort, target.svcPort,
		))

		if err := cmd.Start(); err != nil {
			continue
		}

		// exited is closed by a goroutine when the process exits.
		exited := make(chan struct{})
		go func() {
			cmd.Wait() //nolint:errcheck
			close(exited)
		}()

		// Poll (via WSL nc) until the TCP port is accepting connections.
		listening := false
		for i := 0; i < 12; i++ {
			select {
			case <-exited:
				// process died before port came up
			default:
			}
			time.Sleep(400 * time.Millisecond)
			checkCtx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
			err := wslShellCtx(checkCtx, fmt.Sprintf(
				"nc -z 127.0.0.1 %d 2>/dev/null || bash -c 'echo >/dev/tcp/127.0.0.1/%d' 2>/dev/null",
				argoCDLocalPort, argoCDLocalPort,
			)).Run()
			cancel()
			if err == nil {
				listening = true
				break
			}
		}

		if !listening {
			cmd.Process.Kill() //nolint:errcheck
			<-exited           // goroutine above already calls Wait — just drain the channel
			continue
		}

		argoCDPFProc = cmd
		argoCDPFScheme = target.scheme
		argoCDPFActive = true

		// When the process exits (killed or crashed), clear the active flag.
		go func(c *exec.Cmd, done <-chan struct{}) {
			<-done
			argoCDMu.Lock()
			if argoCDPFProc == c {
				argoCDPFActive = false
				argoCDPFProc = nil
			}
			argoCDMu.Unlock()
		}(cmd, exited)

		return nil
	}

	return fmt.Errorf("port-forward falhou nas portas 80 e 443 — verifique se o ArgoCD está instalado e pronto")
}

func doStopPortForward() {
	argoCDMu.Lock()
	defer argoCDMu.Unlock()
	doStopPortForwardLocked()
}
