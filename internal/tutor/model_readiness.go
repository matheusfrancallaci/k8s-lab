package tutor

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

type ModelReadiness struct {
	Configured bool              `json:"configured"`
	Ready      bool              `json:"ready"`
	Roles      map[string]string `json:"roles"`
	Missing    []string          `json:"missing,omitempty"`
}

func LLMModelReadiness() ModelReadiness {
	rep := ModelReadiness{Ready: true, Roles: map[string]string{}}
	roleVars := map[string]string{"chat": "OLLAMA_CHAT_MODEL", "router": "OLLAMA_ROUTER_MODEL", "generation": "OLLAMA_GEN_MODEL", "embedding": "OLLAMA_EMBED_MODEL"}
	base := strings.TrimSpace(os.Getenv("OLLAMA_MODEL"))
	for role, envName := range roleVars {
		model := strings.TrimSpace(os.Getenv(envName))
		if model == "" && role != "embedding" {
			model = base
		}
		if model != "" {
			rep.Configured = true
			rep.Roles[role] = model
		}
	}
	if !rep.Configured || remoteConfigured() {
		return rep
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaURL()+"/api/tags", nil)
	if err != nil {
		rep.Ready = false
		rep.Missing = []string{"ollama"}
		return rep
	}
	resp, err := sharedLLMHTTPClient.Do(req)
	if err != nil {
		rep.Ready = false
		rep.Missing = []string{"ollama"}
		return rep
	}
	defer resp.Body.Close()
	var body struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if resp.StatusCode/100 != 2 || json.NewDecoder(resp.Body).Decode(&body) != nil {
		rep.Ready = false
		rep.Missing = []string{"ollama"}
		return rep
	}
	for role, required := range rep.Roles {
		found := false
		for _, installed := range body.Models {
			if modelNameMatches(installed.Name, required) {
				found = true
				break
			}
		}
		if !found {
			rep.Missing = append(rep.Missing, role+":"+required)
		}
	}
	sort.Strings(rep.Missing)
	rep.Ready = len(rep.Missing) == 0
	return rep
}

func remoteConfigured() bool { _, ok := remoteLLM(); return ok }
