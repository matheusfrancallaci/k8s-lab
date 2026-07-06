package main

import (
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strings"

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
		tmpl, err := template.New("base.html").ParseFS(fs,
			"web/templates/base.html",
			"web/templates/nav.html",
			"web/templates/argocd.html",
		)
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpl.ExecuteTemplate(w, "base.html", map[string]any{
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
		tmpl, err := template.New("base.html").ParseFS(fs,
			"web/templates/base.html",
			"web/templates/nav.html",
			"web/templates/cloud.html",
		)
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpl.ExecuteTemplate(w, "base.html", map[string]any{
			"NavActive":   "cloud",
			"CLIExamples": examples,
		})
	}
}

func main() {
	repo, err := repository.NewQuestionRepository(questionsFS)
	if err != nil {
		log.Fatalf("erro ao carregar questões: %v", err)
	}

	// Labs/quiz gerados pelo tutor (persistidos em disco entre execuções)
	repo.LoadDir("questions-custom")

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

	// Static files
	staticSub, _ := fs.Sub(staticFS, "web/static")
	staticHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".css") || strings.HasSuffix(r.URL.Path, ".js") || strings.HasSuffix(r.URL.Path, ".svg") || strings.HasSuffix(r.URL.Path, ".png") || strings.HasSuffix(r.URL.Path, ".jpg") || strings.HasSuffix(r.URL.Path, ".webp") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.FileServer(http.FS(staticSub)).ServeHTTP(w, r)
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
		tmpl, err := template.New("base.html").ParseFS(templatesFS,
			"web/templates/base.html",
			"web/templates/nav.html",
			"web/templates/docs.html",
		)
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpl.ExecuteTemplate(w, "base.html", map[string]any{"NavActive": "docs"})
	})

	// Instalações (catálogo de ferramentas)
	mux.HandleFunc("GET /tools", handlers.ToolsPage(templatesFS))
	mux.HandleFunc("GET /api/tools", handlers.ToolsStatusHandler)
	mux.HandleFunc("GET /api/tools/install", handlers.ToolInstallHandler)

	// On-premise page
	mux.HandleFunc("GET /onpremise", func(w http.ResponseWriter, r *http.Request) {
		tmpl, err := template.New("base.html").ParseFS(templatesFS,
			"web/templates/base.html",
			"web/templates/nav.html",
			"web/templates/onpremise.html",
		)
		if err != nil {
			http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		tmpl.ExecuteTemplate(w, "base.html", map[string]any{"NavActive": "onprem"})
	})

	// Tutor (IA local adaptativa) routes
	mux.HandleFunc("GET /tutor", tutorH.Page)
	mux.HandleFunc("GET /api/tutor/status", tutorH.Status)
	mux.HandleFunc("POST /api/tutor/event", tutorH.Event)
	mux.HandleFunc("POST /api/tutor/generate", tutorH.Generate)
	mux.HandleFunc("POST /api/tutor/ingest", tutorH.Ingest)
	mux.HandleFunc("POST /api/tutor/explain", tutorH.Explain)
	mux.HandleFunc("POST /api/tutor/chat", tutorH.Chat)
	mux.HandleFunc("POST /api/tutor/chat/stream", tutorH.ChatStream)

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

	// ArgoCD routes
	mux.HandleFunc("GET /argocd", argoCDPage(templatesFS))
	mux.HandleFunc("GET /api/argocd/status", handlers.ArgoCDStatusHandler)
	mux.HandleFunc("GET /api/argocd/install", handlers.ArgoCDInstallHandler)
	mux.HandleFunc("POST /api/argocd/portforward", handlers.ArgoCDPortForwardHandler)
	mux.HandleFunc("DELETE /api/argocd/portforward", handlers.ArgoCDPortForwardHandler)

	// Cluster status API (client-go no WSL, shell no host Windows)
	mux.HandleFunc("GET /api/cluster-status", handlers.ClusterStatusHandler)

	// Ociosidade — lido pelo auto-stop da VM (público, info não sensível)
	mux.HandleFunc("GET /api/idle", handlers.IdleHandler)

	// LAB_NO_CLUSTER=1 pula o auto-start do minikube e o monitor de nuvem —
	// útil para testes, CI ou subir só a UI sem tocar em cluster.
	if os.Getenv("LAB_NO_CLUSTER") == "" {
		go handlers.EnsureCluster()
		go handlers.StartCloudMonitor()
	} else {
		log.Println("LAB_NO_CLUSTER definido — auto-gerenciamento de cluster desativado")
	}

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	log.Printf("K8s Study Lab rodando em http://localhost%s", addr)
	if err := http.ListenAndServe(addr, handlers.RequireAuth(mux)); err != nil {
		log.Fatal(err)
	}
}
