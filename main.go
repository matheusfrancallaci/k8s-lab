package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"estudo-app/internal/handlers"
	"estudo-app/internal/repository"
	"estudo-app/internal/tutor"
)

//go:embed questions
var questionsFS embed.FS

//go:embed web/templates
var templatesFS embed.FS

//go:embed web/static
var staticFS embed.FS

type cliExample struct {
	Desc string
	Cmd  string
}

func argoCDPage(fs embed.FS) http.HandlerFunc {
	examples := []cliExample{
		{"Login no ArgoCD", "argocd login localhost:8090 --username admin --password <pwd> --insecure"},
		{"Criar aplicação", "argocd app create myapp --repo <url> --path . --dest-server https://kubernetes.default.svc --dest-namespace default"},
		{"Sincronizar aplicação", "argocd app sync myapp"},
		{"Listar aplicações", "argocd app list"},
		{"Ver status", "argocd app get myapp"},
		{"Histórico de deploys", "argocd app history myapp"},
		{"Rollback", "argocd app rollback myapp <id>"},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		handlers.RenderPage(w, fs, "argocd.html", map[string]any{
			"NavActive":   "argocd",
			"Port":        8090,
			"CLIExamples": examples,
		})
	}
}

func cloudPage(fs embed.FS) http.HandlerFunc {
	examples := []cliExample{
		{"Login na conta Azure", "az login --use-device-code"},
		{"Criar cluster AKS barato", "az aks create -g k8s-study-lab-rg -n k8s-study-lab --node-count 1 --node-vm-size standard_d2als_v7 --tier free --generate-ssh-keys"},
		{"Conectar o kubectl ao AKS", "az aks get-credentials -g k8s-study-lab-rg -n k8s-study-lab --overwrite-existing"},
		{"Ligar o cluster", "az aks start -g k8s-study-lab-rg -n k8s-study-lab"},
		{"Parar (pausa faturamento)", "az aks stop -g k8s-study-lab-rg -n k8s-study-lab"},
		{"Alternar para a nuvem", "kubectl config use-context k8s-study-lab"},
		{"Alternar para o minikube", "kubectl config use-context minikube"},
		{"Excluir o cluster", "az aks delete -g k8s-study-lab-rg -n k8s-study-lab --yes"},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		handlers.RenderPage(w, fs, "cloud.html", map[string]any{
			"NavActive":   "cloud",
			"CLIExamples": examples,
		})
	}
}

func main() {
	setupLogging()

	repo, err := repository.NewQuestionRepository(questionsFS)
	if err != nil {
		log.Fatalf("erro ao carregar questões: %v", err)
	}

	// Labs/quiz gerados pelo tutor (persistidos em disco entre execuções).
	// GC antes de carregar: gerado é descartável — sem retenção o diretório
	// cresce sem limite e polui o banco de prática e o pré-aquecimento.
	if n := tutor.PruneGeneratedQuestionsDefault(); n > 0 {
		slog.Info("[gc] labs gerados antigos removidos", "removidos", n)
	}
	repo.LoadDir("questions-custom")
	if generatedDir := tutor.CustomQuestionsDir(); generatedDir != "questions-custom" {
		repo.LoadDir(generatedDir)
	}

	// Pré-compila todas as páginas HTML no boot: um template quebrado falha
	// aqui (no start), não em produção no primeiro acesso.
	if err := handlers.PrecompileTemplates(templatesFS); err != nil {
		log.Fatalf("erro ao compilar templates: %v", err)
	}

	handlers.LoadAuthSessions() // restaura sessões de login (sobrevive a deploy/restart)
	handlers.StartAuthGC()      // limpa sessões expiradas periodicamente

	store := repository.NewSessionStore()
	labSessions := repository.NewLabSessionStore()
	quiz := handlers.NewQuizHandler(repo, store, templatesFS)
	lab := handlers.NewLabHandler(repo, store, labSessions, templatesFS)
	tutorH := handlers.NewTutorHandler(repo, labSessions, templatesFS)
	tutor.LoadCerts()

	mux := http.NewServeMux()

	// Login / contas por usuário (ativo apenas com APP_PASSWORD definido)
	mux.HandleFunc("GET /login", handlers.LoginHandler)
	mux.HandleFunc("POST /login", handlers.LoginHandler)
	mux.HandleFunc("GET /register", handlers.RegisterHandler)
	mux.HandleFunc("POST /register", handlers.RegisterHandler)
	mux.HandleFunc("GET /logout", handlers.LogoutHandler)

	// Probes de saúde e métricas (públicos — usados por LB/orquestrador/scrape).
	mux.HandleFunc("GET /healthz", handlers.HealthHandler) // liveness (processo vivo)
	mux.HandleFunc("GET /readyz", handlers.ReadyHandler)   // readiness (cluster pronto)
	mux.HandleFunc("GET /metrics", handlers.MetricsHandler)

	// Static files — ETag por conteúdo + revalidação (no-cache). Com "immutable"
	// o browser servia o CSS/JS ANTIGO após um rebuild (larguras esticadas etc.);
	// com ETag ele revalida e só rebaixa quando o arquivo muda de verdade.
	staticSub, _ := fs.Sub(staticFS, "web/static")
	staticETags := map[string]string{}
	fs.WalkDir(staticSub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if b, e := fs.ReadFile(staticSub, p); e == nil {
			sum := sha256.Sum256(b)
			staticETags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		}
		return nil
	})
	fileSrv := http.FileServer(http.FS(staticSub))
	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		if etag, ok := staticETags[strings.TrimPrefix(r.URL.Path, "/")]; ok {
			w.Header().Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		fileSrv.ServeHTTP(w, r)
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", staticHandler))

	// Quiz routes
	mux.HandleFunc("GET /", quiz.Home)
	mux.HandleFunc("GET /practice", quiz.Practice)
	mux.HandleFunc("POST /quiz/start", quiz.Start)
	mux.HandleFunc("GET /quiz/{id}", quiz.Question)
	mux.HandleFunc("POST /quiz/{id}/answer", quiz.Answer)
	mux.HandleFunc("GET /quiz/{id}/result", quiz.Result)

	// Lab routes
	mux.HandleFunc("GET /lab", lab.List)
	mux.HandleFunc("GET /lab/{id}", lab.Show)
	mux.HandleFunc("GET /lab/{id}/validate", lab.Validate)
	mux.HandleFunc("POST /lab/{id}/validate", lab.Validate)
	mux.HandleFunc("GET /lab/{id}/setup", lab.Setup)
	mux.HandleFunc("GET /lab/{id}/state", lab.State)
	mux.HandleFunc("POST /lab/{id}/teardown", lab.Teardown)

	// Terminal WebSocket
	mux.HandleFunc("GET /ws/terminal", handlers.TerminalWS)

	// Lab session routes
	mux.HandleFunc("POST /lab/session", lab.CreateSession)
	mux.HandleFunc("POST /lab/session/{sid}/advance", lab.AdvanceSession)

	// Cluster reset SSE
	mux.HandleFunc("GET /api/cluster/reset", handlers.ClusterResetHandler)

	// Docs page
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, r *http.Request) {
		handlers.RenderPage(w, templatesFS, "docs.html", map[string]any{"NavActive": "docs"})
	})

	// Instalações (catálogo de ferramentas)
	mux.HandleFunc("GET /tools", handlers.ToolsPage(templatesFS))
	mux.HandleFunc("GET /api/tools", handlers.ToolsStatusHandler)
	mux.HandleFunc("GET /api/tools/install", handlers.ToolInstallHandler)

	// On-premise page
	mux.HandleFunc("GET /onpremise", func(w http.ResponseWriter, r *http.Request) {
		handlers.RenderPage(w, templatesFS, "onpremise.html", map[string]any{"NavActive": "onprem"})
	})

	// Tutor (IA local adaptativa) routes
	mux.HandleFunc("GET /tutor", tutorH.Page)
	mux.HandleFunc("GET /api/tutor/status", tutorH.Status)
	mux.HandleFunc("POST /api/tutor/curriculum/verify", tutorH.VerifyCurriculum)
	mux.HandleFunc("POST /api/tutor/event", tutorH.Event)
	mux.HandleFunc("POST /api/tutor/generate", tutorH.Generate)
	mux.HandleFunc("POST /api/tutor/ingest", tutorH.Ingest)
	mux.HandleFunc("POST /api/tutor/explain", tutorH.Explain)
	mux.HandleFunc("POST /api/tutor/exam-report", tutorH.ExamReport)
	mux.HandleFunc("POST /api/tutor/goal", tutorH.Goal)
	mux.HandleFunc("POST /api/tutor/author", tutorH.Author)
	mux.HandleFunc("GET /api/tutor/eval", tutorH.Eval)
	mux.HandleFunc("GET /api/tutor/quality", tutorH.Quality)
	mux.HandleFunc("POST /api/tutor/quality/promote", tutorH.PromoteQualityFixture)
	mux.HandleFunc("GET /api/tutor/admin-quality", tutorH.AdminQuality)
	mux.HandleFunc("GET /api/tutor/deploy-gate", tutorH.DeployGate)
	mux.HandleFunc("POST /api/tutor/chat", tutorH.Chat)
	mux.HandleFunc("POST /api/tutor/chat/stream", tutorH.ChatStream)
	mux.HandleFunc("GET /api/tutor/conversations", tutorH.Conversations)
	mux.HandleFunc("POST /api/tutor/conversations", tutorH.Conversations)
	mux.HandleFunc("PATCH /api/tutor/conversations", tutorH.Conversations)
	mux.HandleFunc("DELETE /api/tutor/conversations", tutorH.Conversations)
	mux.HandleFunc("GET /api/tutor/agent-trace", tutorH.AgentTrace)
	mux.HandleFunc("GET /api/tutor/model-experiments", tutorH.ModelExperiments)
	mux.HandleFunc("GET /api/tutor/orchestration", tutorH.Orchestration)

	// Perfil = conta logada (progresso do tutor isolado por usuário)
	mux.HandleFunc("GET /api/profile", handlers.ProfileHandler)

	// Cloud (Azure AKS) routes
	mux.HandleFunc("GET /cloud", cloudPage(templatesFS))
	mux.HandleFunc("GET /api/cloud/status", handlers.CloudStatusHandler)
	mux.HandleFunc("GET /api/cloud/install-az", handlers.CloudInstallAzHandler)
	mux.HandleFunc("GET /api/cloud/login", handlers.CloudLoginHandler)
	mux.HandleFunc("GET /api/cloud/create", handlers.CloudCreateHandler)
	mux.HandleFunc("POST /api/cloud/start", handlers.CloudStartHandler)
	mux.HandleFunc("POST /api/cloud/stop", handlers.CloudStopHandler)
	mux.HandleFunc("POST /api/cloud/delete", handlers.CloudDeleteHandler)
	mux.HandleFunc("POST /api/cloud/connect", handlers.CloudConnectHandler)
	mux.HandleFunc("POST /api/context", handlers.SetContextHandler)
	mux.HandleFunc("GET /api/contexts", handlers.ContextsHandler)
	mux.HandleFunc("POST /api/context/test", handlers.ContextTestHandler)

	// ArgoCD routes — /argocd (exato) é a página de controle; /argocd/{...} é a
	// UI real do ArgoCD servida por proxy (argocd-server --rootpath=/argocd),
	// atrás do login da app. "localhost:8090" não existe para quem acessa a
	// instância hospedada — o proxy é o único caminho que funciona nos dois.
	mux.HandleFunc("GET /argocd", argoCDPage(templatesFS))
	// Por método: padrão sem método em "/argocd/" conflita com "GET /" no
	// ServeMux do Go 1.22+ (panic no boot — pego no smoke local, não em prod).
	for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
		mux.HandleFunc(m+" /argocd/", handlers.ArgoCDProxyHandler)
	}
	mux.HandleFunc("GET /api/argocd/status", handlers.ArgoCDStatusHandler)
	mux.HandleFunc("GET /api/argocd/install", handlers.ArgoCDInstallHandler)
	mux.HandleFunc("POST /api/argocd/portforward", handlers.ArgoCDPortForwardHandler)
	mux.HandleFunc("DELETE /api/argocd/portforward", handlers.ArgoCDPortForwardHandler)

	// Cluster status API (client-go no WSL, shell no host Windows)
	mux.HandleFunc("GET /api/cluster-status", handlers.ClusterStatusHandler)
	mux.HandleFunc("GET /api/labs/readiness", handlers.LabReadinessStatusHandler)

	// Ociosidade — lido pelo auto-stop da VM (público, info não sensível)
	mux.HandleFunc("GET /api/idle", handlers.IdleHandler)

	// LAB_NO_CLUSTER=1 pula o auto-start do minikube e o monitor de nuvem —
	// útil para testes, CI ou subir só a UI sem tocar em cluster.
	if os.Getenv("LAB_NO_CLUSTER") == "" {
		go handlers.EnsureAzureLogin() // hospedado: login via identity da VM (sem device-code)
		go handlers.EnsureCluster()
		go handlers.StartCloudMonitor()
		handlers.StartCloudShellGC() // coleta pods de shell por-usuário ociosos
	} else {
		log.Println("LAB_NO_CLUSTER definido — auto-gerenciamento de cluster desativado")
	}

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}

	// Cadeia de middleware: Recover (panic->500) -> RequestMetrics (conta+loga)
	// -> RequireAuth (gating) -> mux. Recover fica por fora para capturar panics
	// de qualquer camada abaixo.
	root := handlers.Recover(handlers.RequestMetrics(handlers.RequireAuth(handlers.UserActivity(mux))))

	srv := &http.Server{
		Addr:    addr,
		Handler: root,
		// ReadHeaderTimeout mata o slow-loris (headers a conta-gotas) sem afetar
		// WebSocket/SSE — que só começam DEPOIS dos headers. Read/WriteTimeout
		// ficam 0 de propósito: o terminal (WS) e os streams (SSE do tutor/reset)
		// são conexões longevas e um WriteTimeout global as cortaria no meio.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown: no SIGTERM/SIGINT (deploy, `systemctl restart`, Ctrl-C)
	// para de aceitar conexões novas e dá até 25s para as em andamento drenarem,
	// em vez de cortar requests/streams no meio.
	idleClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		slog.Info("sinal de parada recebido — encerrando com graça")
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			slog.Warn("shutdown com conexões pendentes", "err", err)
		}
		close(idleClosed)
	}()

	slog.Info("K8s Study Lab no ar", "addr", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-idleClosed
	slog.Info("encerrado")
}

// setupLogging configura o slog como logger padrão. LOG_FORMAT=json emite JSON
// estruturado (bom para produção/coleta); qualquer outro valor usa texto legível
// (dev). O nível vem de LOG_LEVEL (debug|info|warn|error), default info.
func setupLogging() {
	level := slog.LevelInfo
	switch strings.ToLower(os.Getenv("LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}
