package handlers

import (
	"encoding/json"
	"net/http"

	"estudo-app/internal/tutor"
)

// ─────────────────────────────────────────────────────────────────────────────
// Perfil de usuário — identidade leve para separar ESTADO entre pessoas que
// usam a mesma instância (ex.: amigos). NÃO é fronteira de segurança: o gate de
// acesso continua sendo o APP_PASSWORD compartilhado. Aqui só isolamos progresso.
// ─────────────────────────────────────────────────────────────────────────────

const profileCookie = "k8slab_user"

// userID devolve o id de perfil do requisitante (cookie), ou "default".
func userID(r *http.Request) string {
	c, err := r.Cookie(profileCookie)
	if err != nil {
		return "default"
	}
	return tutor.SanitizeID(c.Value)
}

// ProfileHandler: GET devolve o perfil atual; POST {name} define o cookie.
func ProfileHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var body struct {
			Name string `json:"name"`
		}
		json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
		id := tutor.SanitizeID(body.Name)
		http.SetCookie(w, &http.Cookie{
			Name: profileCookie, Value: id, Path: "/",
			SameSite: http.SameSiteLaxMode, MaxAge: 365 * 24 * 3600,
		})
		json.NewEncoder(w).Encode(map[string]any{"profile": id}) //nolint:errcheck
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"profile": userID(r)}) //nolint:errcheck
}
