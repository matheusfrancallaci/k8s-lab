package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func changeWorkingDir(t *testing.T, dir string) func() error {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return func() error { return os.Chdir(old) }
}

func TestDeployGateIsPublicOperationalProbe(t *testing.T) {
	t.Setenv("APP_PASSWORD", "protected")
	called := false
	h := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/tutor/deploy-gate", nil))
	if !called {
		t.Fatal("deploy gate deveria permanecer acessivel ao pipeline sem cookie")
	}
}

func TestRegisterThenLoginPersistsAccount(t *testing.T) {
	t.Setenv("APP_PASSWORD", "invite-code")
	t.Setenv("DATABASE_URL", "")
	oldUsers, oldSessions := users, authSessions
	users = map[string]string{}
	authSessions = map[string]session{}
	usersOnce = sync.Once{}
	t.Cleanup(func() {
		users, authSessions = oldUsers, oldSessions
		usersOnce = sync.Once{}
	})
	oldDir := changeWorkingDir(t, t.TempDir())
	t.Cleanup(func() { _ = oldDir() })

	registerForm := url.Values{"username": {"alice"}, "password": {"correct-horse"}, "invite": {"invite-code"}}
	registerReq := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(registerForm.Encode()))
	registerReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	registerRes := httptest.NewRecorder()
	RegisterHandler(registerRes, registerReq)
	if registerRes.Code != http.StatusSeeOther {
		t.Fatalf("registro retornou %d, esperado 303", registerRes.Code)
	}
	if len(registerRes.Result().Cookies()) == 0 {
		t.Fatal("registro nao emitiu cookie de sessao")
	}

	// Simula restart: a conta precisa voltar do arquivo persistido.
	users = map[string]string{}
	usersOnce = sync.Once{}
	loginForm := url.Values{"username": {"alice"}, "password": {"correct-horse"}}
	loginReq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(loginForm.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRes := httptest.NewRecorder()
	LoginHandler(loginRes, loginReq)
	if loginRes.Code != http.StatusSeeOther {
		t.Fatalf("login apos restart retornou %d, esperado 303", loginRes.Code)
	}
}

func TestAuthSessionSurvivesReload(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	oldSessions := authSessions
	authSessions = map[string]session{
		"still-valid": {user: "alice", expiresAt: time.Now().Add(time.Hour)},
		"expired":     {user: "bob", expiresAt: time.Now().Add(-time.Hour)},
	}
	t.Cleanup(func() { authSessions = oldSessions })
	oldDir := changeWorkingDir(t, t.TempDir())
	t.Cleanup(func() { _ = oldDir() })

	persistSessions()
	authSessions = map[string]session{}
	LoadAuthSessions()

	if got, ok := authSessions["still-valid"]; !ok || got.user != "alice" {
		t.Fatalf("sessao valida nao foi restaurada: %#v", authSessions)
	}
	if _, ok := authSessions["expired"]; ok {
		t.Fatal("sessao expirada foi restaurada")
	}
}
