package handlers

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"estudo-app/internal/tutor"
	"estudo-app/internal/version"
)

// ─────────────────────────────────────────────────────────────────────────────
// Observabilidade e robustez do servidor HTTP.
//
//   - /healthz  (liveness):  200 enquanto o processo está de pé. Barato — não
//     toca no cluster. É o que um orquestrador usa para decidir reiniciar.
//   - /readyz   (readiness): 200 só quando o cluster k3s embutido está pronto;
//     503 durante o boot (k3s leva ~30-90s). Evita servir tráfego que depende
//     do cluster cedo demais e dá um sinal claro pós-auto-stop (cold start).
//   - /metrics: contadores em texto (formato Prometheus), sem dependência nova.
//
// Middlewares: Recover (um panic em handler vira 500 + log, não derruba a
// conexão no meio) e RequestMetrics (conta requests/latência/status).
// ─────────────────────────────────────────────────────────────────────────────

var startedAt = time.Now()

// Contadores globais do processo (lidos por /metrics).
var (
	reqTotal    atomic.Int64
	reqInFlight atomic.Int64
	req5xx      atomic.Int64
	req4xx      atomic.Int64
	panicsTotal atomic.Int64
)

// HealthHandler é o liveness probe: responde sempre que o processo está vivo.
// Inclui a versão do build para conferir rapidamente o que está no ar.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-App-Version", version.Short())
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "ok %s\n", version.Short())
}

// ── Readiness com cache curto ────────────────────────────────────────────────
// clusterIsUp() pode custar um shell no fallback; um probe a cada poucos
// segundos não deve martelar o cluster. Cacheamos o resultado por readyTTL.
var (
	readyMu        sync.Mutex
	readyCached    bool
	readyCheckedAt time.Time
)

const readyTTL = 5 * time.Second

func clusterReadyCached() bool {
	readyMu.Lock()
	defer readyMu.Unlock()
	if time.Since(readyCheckedAt) < readyTTL {
		return readyCached
	}
	readyCached = clusterIsUp()
	readyCheckedAt = time.Now()
	return readyCached
}

// ReadyHandler é o readiness probe: 200 só com o cluster pronto, senão 503.
func ReadyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if clusterReadyCached() {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready\n"))
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte("cluster not ready\n"))
}

// MetricsHandler expõe contadores em formato de texto Prometheus. Sem segredos
// (só contagens e stats de runtime), então pode ser público como /api/idle.
func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	uptime := time.Since(startedAt).Seconds()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP app_build_info Versão do build (label version).\n")
	fmt.Fprintf(w, "# TYPE app_build_info gauge\napp_build_info{version=%q} 1\n", version.Short())
	fmt.Fprintf(w, "# HELP app_uptime_seconds Tempo desde o boot do processo.\n")
	fmt.Fprintf(w, "# TYPE app_uptime_seconds gauge\napp_uptime_seconds %.0f\n", uptime)
	fmt.Fprintf(w, "# HELP app_http_requests_total Requests HTTP atendidos.\n")
	fmt.Fprintf(w, "# TYPE app_http_requests_total counter\napp_http_requests_total %d\n", reqTotal.Load())
	fmt.Fprintf(w, "# HELP app_http_requests_in_flight Requests em andamento agora.\n")
	fmt.Fprintf(w, "# TYPE app_http_requests_in_flight gauge\napp_http_requests_in_flight %d\n", reqInFlight.Load())
	fmt.Fprintf(w, "# HELP app_http_responses_4xx_total Respostas com status 4xx.\n")
	fmt.Fprintf(w, "# TYPE app_http_responses_4xx_total counter\napp_http_responses_4xx_total %d\n", req4xx.Load())
	fmt.Fprintf(w, "# HELP app_http_responses_5xx_total Respostas com status 5xx.\n")
	fmt.Fprintf(w, "# TYPE app_http_responses_5xx_total counter\napp_http_responses_5xx_total %d\n", req5xx.Load())
	fmt.Fprintf(w, "# HELP app_panics_recovered_total Panics recuperados em handlers.\n")
	fmt.Fprintf(w, "# TYPE app_panics_recovered_total counter\napp_panics_recovered_total %d\n", panicsTotal.Load())
	fmt.Fprintf(w, "# HELP app_active_terminals Terminais WebSocket abertos.\n")
	fmt.Fprintf(w, "# TYPE app_active_terminals gauge\napp_active_terminals %d\n", ActiveTerminals())
	fmt.Fprintf(w, "# HELP app_goroutines Goroutines vivas.\n")
	fmt.Fprintf(w, "# TYPE app_goroutines gauge\napp_goroutines %d\n", runtime.NumGoroutine())
	fmt.Fprintf(w, "# HELP go_memstats_heap_alloc_bytes Heap alocado.\n")
	fmt.Fprintf(w, "# TYPE go_memstats_heap_alloc_bytes gauge\ngo_memstats_heap_alloc_bytes %d\n", m.HeapAlloc)
	gatePassed, gateFailed := tutor.GateStats()
	fmt.Fprintf(w, "# HELP app_tutor_gate_passed_total Labs que passaram no quality gate desde o boot.\n")
	fmt.Fprintf(w, "# TYPE app_tutor_gate_passed_total counter\napp_tutor_gate_passed_total %d\n", gatePassed)
	fmt.Fprintf(w, "# HELP app_tutor_gate_failed_total Labs barrados no quality gate desde o boot.\n")
	fmt.Fprintf(w, "# TYPE app_tutor_gate_failed_total counter\napp_tutor_gate_failed_total %d\n", gateFailed)
	quizOK, quizNo := tutor.QuizStats()
	fmt.Fprintf(w, "# HELP app_tutor_quiz_accepted_total Questoes de quiz geradas aceitas na validacao (formato+grounding).\n")
	fmt.Fprintf(w, "# TYPE app_tutor_quiz_accepted_total counter\napp_tutor_quiz_accepted_total %d\n", quizOK)
	fmt.Fprintf(w, "# HELP app_tutor_quiz_rejected_total Questoes de quiz geradas descartadas na validacao.\n")
	fmt.Fprintf(w, "# TYPE app_tutor_quiz_rejected_total counter\napp_tutor_quiz_rejected_total %d\n", quizNo)
	telemetry := tutor.TutorTelemetry()
	stages := make([]string, 0, len(telemetry.Stages))
	for stage := range telemetry.Stages {
		stages = append(stages, stage)
	}
	sort.Strings(stages)
	fmt.Fprintln(w, "# HELP app_tutor_latency_ms Tutor latency sampled in milliseconds.")
	fmt.Fprintln(w, "# TYPE app_tutor_latency_ms gauge")
	for _, stage := range stages {
		metric := telemetry.Stages[stage]
		label := strings.ReplaceAll(stage, `"`, "")
		fmt.Fprintf(w, "app_tutor_latency_ms{stage=\"%s\",quantile=\"avg\"} %d\n", label, metric.AvgMS)
		fmt.Fprintf(w, "app_tutor_latency_ms{stage=\"%s\",quantile=\"p95\"} %d\n", label, metric.P95MS)
		if metric.FirstTokenMS > 0 {
			fmt.Fprintf(w, "app_tutor_latency_ms{stage=\"%s\",quantile=\"first_token\"} %d\n", label, metric.FirstTokenMS)
		}
		fmt.Fprintf(w, "app_tutor_requests_total{stage=\"%s\",result=\"all\"} %d\n", label, metric.Count)
		fmt.Fprintf(w, "app_tutor_requests_total{stage=\"%s\",result=\"failure\"} %d\n", label, metric.Failures)
	}
}

// statusRecorder captura o status code para métricas/logs (o ResponseWriter não
// o expõe). Preserva Flush e Hijack — SSE e WebSocket dependem deles.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.written {
		r.status = http.StatusOK
		r.written = true
	}
	return r.ResponseWriter.Write(b)
}

// Flush repassa para o writer real (SSE: chat/stream, cluster/reset).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack repassa para o writer real (WebSocket do terminal via gorilla).
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, http.ErrNotSupported
}

// Recover transforma um panic em handler numa resposta 500 limpa + log, em vez
// de abortar a conexão no meio. Nota: cobre só a goroutine do request — panics
// em goroutines de background (EnsureCluster, monitores) ainda derrubam o
// processo (e o systemd/Docker reinicia).
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				panicsTotal.Add(1)
				slog.Error("panic em handler recuperado",
					"path", r.URL.Path, "method", r.Method, "panic", rec,
					"stack", string(debug.Stack()))
				// Só escreve o status se o handler ainda não começou a responder.
				if sr, ok := w.(*statusRecorder); ok && sr.written {
					return
				}
				http.Error(w, "erro interno", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// RequestMetrics conta requests e status, e loga (slog) cada request com a
// latência. Endpoints de probe/estáticos são contados mas não logados (ruído).
func RequestMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		reqTotal.Add(1)
		reqInFlight.Add(1)
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)
		reqInFlight.Add(-1)

		switch {
		case sr.status >= 500:
			req5xx.Add(1)
		case sr.status >= 400:
			req4xx.Add(1)
		}

		if isNoisyPath(r.URL.Path) {
			return
		}
		slog.Info("http",
			"method", r.Method, "path", r.URL.Path,
			"status", sr.status, "dur_ms", time.Since(start).Milliseconds(),
			"ip", clientIP(r))
	})
}

// isNoisyPath silencia o log de rotas de alta frequência e baixo valor:
// assets, probes de health e o poll de ociosidade do auto-stop.
func isNoisyPath(p string) bool {
	switch p {
	case "/healthz", "/readyz", "/metrics", "/api/idle":
		return true
	}
	return len(p) >= 8 && p[:8] == "/static/"
}
