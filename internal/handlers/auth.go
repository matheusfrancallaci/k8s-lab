package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"estudo-app/internal/persistence"
	"estudo-app/internal/tutor"
	"golang.org/x/crypto/bcrypt"
)

// ─────────────────────────────────────────────────────────────────────────────
// Autenticação — contas por usuário (login real). Sem APP_PASSWORD definido:
// uso local sem login (perfil "default"). Com APP_PASSWORD: cada pessoa cria a
// própria conta usando o APP_PASSWORD como CÓDIGO DE CONVITE; depois entra com
// usuário+senha. A conta logada É o perfil (progresso do tutor isolado por conta).
// ─────────────────────────────────────────────────────────────────────────────

// session é uma sessão autenticada com expiração absoluta. Guardar o prazo no
// servidor (não só no cookie) permite revogação por idade e limpeza de memória
// — sem isso o mapa cresceria indefinidamente numa instância multi-user.
type session struct {
	user      string
	expiresAt time.Time
}

const sessionTTL = 30 * 24 * time.Hour

var (
	authMu       sync.Mutex
	authSessions = map[string]session{} // token -> sessão
	users        = map[string]string{}  // username -> bcrypt hash
	usersOnce    sync.Once
)

// ─── Proteção contra brute-force de senha ───────────────────────────────────
// Trava tentativas por IP de origem (não por usuário — evita que um atacante
// tranque a conta de terceiros só errando a senha deles de propósito).
type loginThrottle struct {
	fails     int
	lockUntil time.Time
}

const (
	maxLoginFails = 8               // falhas antes de travar
	loginLockFor  = 5 * time.Minute // duração da trava
	failWindow    = 15 * time.Minute
)

var (
	throttleMu sync.Mutex
	throttles  = map[string]*loginThrottle{}
)

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginLocked informa se o IP está travado e, se sim, quanto falta.
func loginLocked(ip string) (bool, time.Duration) {
	throttleMu.Lock()
	defer throttleMu.Unlock()
	t := throttles[ip]
	if t == nil {
		return false, 0
	}
	if now := time.Now(); now.Before(t.lockUntil) {
		return true, time.Until(t.lockUntil)
	}
	return false, 0
}

func recordLoginFail(ip string) {
	throttleMu.Lock()
	defer throttleMu.Unlock()
	t := throttles[ip]
	now := time.Now()
	// Janela expirada sem falhas recentes: recomeça a contagem.
	if t == nil || now.Sub(t.lockUntil) > failWindow {
		t = &loginThrottle{}
		throttles[ip] = t
	}
	t.fails++
	if t.fails >= maxLoginFails {
		t.lockUntil = now.Add(loginLockFor)
		t.fails = 0
	} else {
		// lockUntil serve também de "visto por último" para a janela.
		t.lockUntil = now
	}
}

func clearLoginFails(ip string) {
	throttleMu.Lock()
	delete(throttles, ip)
	throttleMu.Unlock()
}

func appPassword() string { return os.Getenv("APP_PASSWORD") }

func usersFile() string { return filepath.Join("data", "users.json") }

// ─── Persistência das sessões de login ───────────────────────────────────────
// Sem isto, um restart do processo esvazia o mapa em memória e DESLOGA todo
// mundo — inaceitável num produto que reinicia a cada deploy. Persistimos o
// mapa token->sessão em disco (0600, junto do users.json) e recarregamos no
// boot, descartando o que já expirou.

func sessionsFile() string { return filepath.Join("data", "sessions.json") }

type persistedSession struct {
	User      string    `json:"user"`
	ExpiresAt time.Time `json:"expires_at"`
}

// LoadAuthSessions recarrega as sessões válidas do disco no boot. Chamado uma
// vez no start (antes de servir tráfego).
func LoadAuthSessions() {
	var doc map[string]persistedSession
	loadedFromDatabase := false
	if persistence.Enabled() {
		found, err := persistence.Get("auth", "sessions", &doc)
		if err != nil {
			log.Printf("[auth] falha ao carregar sessoes do PostgreSQL; usando disco: %v", err)
		} else {
			loadedFromDatabase = found
		}
	}
	if !loadedFromDatabase {
		b, err := os.ReadFile(sessionsFile())
		if err != nil || json.Unmarshal(b, &doc) != nil {
			return
		}
	}
	now := time.Now()
	authMu.Lock()
	for tok, s := range doc {
		if now.Before(s.ExpiresAt) {
			authSessions[tok] = session{user: s.User, expiresAt: s.ExpiresAt}
		}
	}
	authMu.Unlock()
	if persistence.Enabled() && !loadedFromDatabase {
		if err := persistence.Put("auth", "sessions", doc); err != nil {
			log.Printf("[auth] falha ao migrar sessoes para PostgreSQL: %v", err)
		}
	}
}

// persistSessions grava o mapa atual em disco. Tira um snapshot sob o lock e
// escreve fora dele (não segura authMu durante o I/O). Logins/logouts são raros,
// então gravar o arquivo inteiro a cada mutação é barato.
func persistSessions() {
	authMu.Lock()
	snap := make(map[string]persistedSession, len(authSessions))
	for tok, s := range authSessions {
		snap[tok] = persistedSession{User: s.user, ExpiresAt: s.expiresAt}
	}
	authMu.Unlock()

	if err := os.MkdirAll("data", 0o755); err != nil {
		return
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return
	}
	_ = os.WriteFile(sessionsFile(), b, 0o600)
	if persistence.Enabled() {
		if err := persistence.Put("auth", "sessions", snap); err != nil {
			log.Printf("[auth] falha ao persistir sessoes no PostgreSQL: %v", err)
		}
	}
}

func ensureUsers() { usersOnce.Do(loadUsers) }

func loadUsers() {
	var doc struct {
		Users map[string]string `json:"users"`
	}
	loadedFromDatabase := false
	if persistence.Enabled() {
		found, err := persistence.Get("auth", "users", &doc)
		if err != nil {
			log.Printf("[auth] falha ao carregar usuarios do PostgreSQL; usando disco: %v", err)
		} else {
			loadedFromDatabase = found
		}
	}
	if !loadedFromDatabase {
		b, err := os.ReadFile(usersFile())
		if err != nil || json.Unmarshal(b, &doc) != nil {
			return
		}
	}
	if doc.Users != nil {
		users = doc.Users
		if persistence.Enabled() && !loadedFromDatabase {
			if err := persistence.Put("auth", "users", doc); err != nil {
				log.Printf("[auth] falha ao migrar usuarios para PostgreSQL: %v", err)
			}
		}
	}
}

// saveUsers persiste o mapa (caller deve segurar authMu).
func saveUsers() {
	if err := os.MkdirAll("data", 0o755); err != nil {
		return
	}
	b, _ := json.MarshalIndent(struct { //nolint:errchkjson
		Users map[string]string `json:"users"`
	}{users}, "", "  ")
	_ = os.WriteFile(usersFile(), b, 0o600)
	if persistence.Enabled() {
		if err := persistence.Put("auth", "users", struct {
			Users map[string]string `json:"users"`
		}{users}); err != nil {
			log.Printf("[auth] falha ao persistir usuarios no PostgreSQL: %v", err)
		}
	}
}

// newSessionToken gera um token de 192 bits. Erro do CSPRNG é fatal para a
// requisição (retorna "") — nunca emitir um token previsível.
func newSessionToken() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		log.Printf("[auth] falha no CSPRNG ao gerar token: %v", err)
		return ""
	}
	return hex.EncodeToString(b)
}

// isAuthenticated devolve o usuário da sessão e se está autenticado. Sessões
// expiradas são tratadas como não autenticadas e removidas na hora.
func isAuthenticated(r *http.Request) (string, bool) {
	c, err := r.Cookie("k8slab_auth")
	if err != nil {
		return "", false
	}
	authMu.Lock()
	defer authMu.Unlock()
	s, ok := authSessions[c.Value]
	if !ok {
		return "", false
	}
	if time.Now().After(s.expiresAt) {
		delete(authSessions, c.Value)
		return "", false
	}
	return s.user, true
}

// setSessionCookie cria a sessão e grava o cookie. Devolve false se não foi
// possível gerar um token seguro (o caller deve responder com erro 500).
func setSessionCookie(w http.ResponseWriter, username string) bool {
	token := newSessionToken()
	if token == "" {
		http.Error(w, "erro ao criar a sessão", http.StatusInternalServerError)
		return false
	}
	authMu.Lock()
	authSessions[token] = session{user: username, expiresAt: time.Now().Add(sessionTTL)}
	authMu.Unlock()
	persistSessions() // sobrevive a restart/deploy
	http.SetCookie(w, &http.Cookie{
		Name: "k8slab_auth", Value: token, Path: "/",
		HttpOnly: true, Secure: cookieSecure(), SameSite: http.SameSiteLaxMode,
		MaxAge: int(sessionTTL.Seconds()),
	})
	return true
}

// cookieSecure marca o cookie como Secure quando o app é servido por HTTPS
// (produção atrás de um proxy TLS). Controlado por COOKIE_SECURE=1.
func cookieSecure() bool { return os.Getenv("COOKIE_SECURE") == "1" }

// StartAuthGC remove sessões expiradas periodicamente para o mapa não crescer
// sem limite numa instância de longa duração. Chamado uma vez no boot.
func StartAuthGC() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			authMu.Lock()
			removed := 0
			for tok, s := range authSessions {
				if now.After(s.expiresAt) {
					delete(authSessions, tok)
					removed++
				}
			}
			authMu.Unlock()
			if removed > 0 {
				persistSessions() // remove do disco os que expiraram
			}
		}
	}()
}

// LoginHandler exibe o formulário e processa o login (usuário + senha).
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if appPassword() == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ensureUsers()
	if r.Method != http.MethodPost {
		w.Write([]byte(renderLogin("", "login"))) //nolint:errcheck
		return
	}
	ip := clientIP(r)
	if locked, remaining := loginLocked(ip); locked {
		w.WriteHeader(http.StatusTooManyRequests)
		mins := int(remaining.Minutes()) + 1
		w.Write([]byte(renderLogin( //nolint:errcheck
			"muitas tentativas — tente novamente em "+strconv.Itoa(mins)+" min", "login")))
		return
	}
	user := tutor.SanitizeID(r.FormValue("username"))
	pw := r.FormValue("password")
	authMu.Lock()
	hash, ok := users[user]
	authMu.Unlock()
	if !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		recordLoginFail(ip)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(renderLogin("usuário ou senha inválidos", "login"))) //nolint:errcheck
		return
	}
	clearLoginFails(ip)
	if setSessionCookie(w, user) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// RegisterHandler cria uma conta nova. Exige o código de convite (= APP_PASSWORD).
func RegisterHandler(w http.ResponseWriter, r *http.Request) {
	if appPassword() == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	ensureUsers()
	if r.Method != http.MethodPost {
		w.Write([]byte(renderLogin("", "register"))) //nolint:errcheck
		return
	}
	fail := func(msg string) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(renderLogin(msg, "register"))) //nolint:errcheck
	}
	if subtle.ConstantTimeCompare([]byte(r.FormValue("invite")), []byte(appPassword())) != 1 {
		fail("código de convite incorreto")
		return
	}
	user := tutor.SanitizeID(r.FormValue("username"))
	pw := r.FormValue("password")
	if user == "" || user == "default" {
		fail("escolha um nome de usuário válido")
		return
	}
	if len(pw) < 8 {
		fail("a senha precisa de ao menos 8 caracteres")
		return
	}
	authMu.Lock()
	if _, exists := users[user]; exists {
		authMu.Unlock()
		fail("esse usuário já existe — faça login")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		authMu.Unlock()
		fail("erro ao criar a conta")
		return
	}
	users[user] = string(hash)
	saveUsers()
	authMu.Unlock()
	if setSessionCookie(w, user) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// LogoutHandler encerra a sessão.
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("k8slab_auth"); err == nil {
		authMu.Lock()
		delete(authSessions, c.Value)
		authMu.Unlock()
		persistSessions()
	}
	http.SetCookie(w, &http.Cookie{Name: "k8slab_auth", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// RequireAuth protege todas as rotas quando APP_PASSWORD está definido.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Login, registro, assets, probes (health/ready/metrics) e o endpoint de
		// ociosidade são públicos (load balancer / auto-stop os consultam sem cookie).
		switch r.URL.Path {
		case "/login", "/register", "/api/idle", "/healthz", "/readyz", "/metrics", "/api/tutor/deploy-gate":
			next.ServeHTTP(w, r)
			return
		}
		if appPassword() == "" || strings.HasPrefix(r.URL.Path, "/static/") {
			next.ServeHTTP(w, r)
			return
		}
		if _, ok := isAuthenticated(r); !ok {
			if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
				http.Error(w, "não autenticado", http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}
