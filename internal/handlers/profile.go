package handlers

import (
	"encoding/json"
	"net/http"

	"estudo-app/internal/tutor"
)

// ─────────────────────────────────────────────────────────────────────────────
// Perfil = conta logada. O progresso do tutor é keyed pelo usuário autenticado.
// Sem APP_PASSWORD (uso local, sem login), tudo cai no perfil "default".
// ─────────────────────────────────────────────────────────────────────────────

// userID devolve o id de perfil do requisitante (a conta logada), ou "default".
func userID(r *http.Request) string {
	if appPassword() == "" {
		return "default"
	}
	if u, ok := isAuthenticated(r); ok {
		return tutor.SanitizeID(u)
	}
	return "default"
}

// ProfileHandler (GET): devolve a conta atual e se o login está ativo.
func ProfileHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"profile": userID(r),
		"auth":    appPassword() != "",
	})
}
