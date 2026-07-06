package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"estudo-app/internal/tutor"
	"golang.org/x/crypto/bcrypt"
)

// ─────────────────────────────────────────────────────────────────────────────
// Autenticação — contas por usuário (login real). Sem APP_PASSWORD definido:
// uso local sem login (perfil "default"). Com APP_PASSWORD: cada pessoa cria a
// própria conta usando o APP_PASSWORD como CÓDIGO DE CONVITE; depois entra com
// usuário+senha. A conta logada É o perfil (progresso do tutor isolado por conta).
// ─────────────────────────────────────────────────────────────────────────────

var (
	authMu       sync.Mutex
	authSessions = map[string]string{} // token -> username
	users        = map[string]string{} // username -> bcrypt hash
	usersOnce    sync.Once
)

func appPassword() string { return os.Getenv("APP_PASSWORD") }

func usersFile() string { return filepath.Join("data", "users.json") }

func ensureUsers() { usersOnce.Do(loadUsers) }

func loadUsers() {
	b, err := os.ReadFile(usersFile())
	if err != nil {
		return
	}
	var doc struct {
		Users map[string]string `json:"users"`
	}
	if json.Unmarshal(b, &doc) == nil && doc.Users != nil {
		users = doc.Users
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
}

func newSessionToken() string {
	b := make([]byte, 24)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

// isAuthenticated devolve o usuário da sessão e se está autenticado.
func isAuthenticated(r *http.Request) (string, bool) {
	c, err := r.Cookie("k8slab_auth")
	if err != nil {
		return "", false
	}
	authMu.Lock()
	defer authMu.Unlock()
	u, ok := authSessions[c.Value]
	return u, ok
}

func setSessionCookie(w http.ResponseWriter, username string) {
	token := newSessionToken()
	authMu.Lock()
	authSessions[token] = username
	authMu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: "k8slab_auth", Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 30 * 24 * 3600,
	})
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
	user := tutor.SanitizeID(r.FormValue("username"))
	pw := r.FormValue("password")
	authMu.Lock()
	hash, ok := users[user]
	authMu.Unlock()
	if !ok || bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) != nil {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(renderLogin("usuário ou senha inválidos", "login"))) //nolint:errcheck
		return
	}
	setSessionCookie(w, user)
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
	if len(pw) < 4 {
		fail("a senha precisa de ao menos 4 caracteres")
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
	setSessionCookie(w, user)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// LogoutHandler encerra a sessão.
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("k8slab_auth"); err == nil {
		authMu.Lock()
		delete(authSessions, c.Value)
		authMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "k8slab_auth", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// RequireAuth protege todas as rotas quando APP_PASSWORD está definido.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Login, registro e assets estáticos são públicos.
		if appPassword() == "" || r.URL.Path == "/login" || r.URL.Path == "/register" ||
			strings.HasPrefix(r.URL.Path, "/static/") {
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
