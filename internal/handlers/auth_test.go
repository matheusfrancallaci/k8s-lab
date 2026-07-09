package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeployGateIsPublicOperationalProbe(t *testing.T) {
	t.Setenv("APP_PASSWORD", "protected")
	called := false
	h := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(http.StatusOK) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/tutor/deploy-gate", nil))
	if !called {
		t.Fatal("deploy gate deveria permanecer acessivel ao pipeline sem cookie")
	}
}
