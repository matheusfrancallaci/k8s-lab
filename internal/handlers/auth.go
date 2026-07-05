package handlers

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// Autenticação opcional — para expor a plataforma a outras pessoas.
// Sem APP_PASSWORD definido: comportamento atual (uso local, sem login).
// Com APP_PASSWORD: toda rota exige sessão; /login apresenta o formulário.
// ─────────────────────────────────────────────────────────────────────────────

var (
	authMu       sync.Mutex
	authSessions = map[string]bool{}
)

func appPassword() string { return os.Getenv("APP_PASSWORD") }

func newSessionToken() string {
	b := make([]byte, 24)
	rand.Read(b) //nolint:errcheck
	return hex.EncodeToString(b)
}

func isAuthenticated(r *http.Request) bool {
	c, err := r.Cookie("k8slab_auth")
	if err != nil {
		return false
	}
	authMu.Lock()
	defer authMu.Unlock()
	return authSessions[c.Value]
}

const loginPage = `<!DOCTYPE html>
<html lang="pt-BR"><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>K8s Study Lab — Login</title>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@400;600;800&family=JetBrains+Mono:wght@600&display=swap" rel="stylesheet">
<style>
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
background:#050510;font-family:'Inter',sans-serif;color:#eef0ff;}
.card{width:100%;max-width:340px;padding:36px 32px;border-radius:14px;
background:#0d0d20;border:1px solid #20204a;text-align:center;}
.brand{font-family:'JetBrains Mono',monospace;font-weight:600;color:#818cf8;font-size:15px;margin-bottom:6px;}
p{font-size:12px;color:#50527a;margin:0 0 22px;}
input{width:100%;box-sizing:border-box;padding:11px 13px;border-radius:8px;margin-bottom:12px;
border:1px solid #20204a;background:#08081a;color:#eef0ff;font-size:14px;outline:none;}
input:focus{border-color:#5b5fef;}
button{width:100%;padding:11px;border-radius:8px;border:none;cursor:pointer;font-weight:800;font-size:13px;
background:linear-gradient(135deg,#7c82f7,#5b5fef);color:#fff;}
.err{color:#f87171;font-size:11px;margin-top:10px;}
</style></head><body>
<form class="card" method="POST" action="/login">
  <div class="brand">⎈ k8s study lab</div>
  <p>plataforma protegida — informe a senha de acesso</p>
  <input type="password" name="password" placeholder="senha" autofocus autocomplete="current-password">
  <button type="submit">Entrar</button>
  {{ERR}}
</form></body></html>`

// LoginHandler exibe e processa o formulário de login.
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	if appPassword() == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if r.Method == http.MethodPost {
		pw := r.FormValue("password")
		if subtle.ConstantTimeCompare([]byte(pw), []byte(appPassword())) == 1 {
			token := newSessionToken()
			authMu.Lock()
			authSessions[token] = true
			authMu.Unlock()
			http.SetCookie(w, &http.Cookie{
				Name: "k8slab_auth", Value: token, Path: "/",
				HttpOnly: true, SameSite: http.SameSiteLaxMode,
				MaxAge: 7 * 24 * 3600,
			})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(replaceErr(loginPage, `<div class="err">senha incorreta</div>`))) //nolint:errcheck
		return
	}
	w.Write([]byte(replaceErr(loginPage, ""))) //nolint:errcheck
}

func replaceErr(page, err string) string {
	out := ""
	for i := 0; i < len(page); i++ {
		if i+7 <= len(page) && page[i:i+7] == "{{ERR}}" {
			out += err
			i += 6
			continue
		}
		out += string(page[i])
	}
	return out
}

// RequireAuth protege todas as rotas quando APP_PASSWORD está definido.
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if appPassword() == "" || r.URL.Path == "/login" {
			next.ServeHTTP(w, r)
			return
		}
		if !isAuthenticated(r) {
			// APIs recebem 401; páginas vão para o login
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
