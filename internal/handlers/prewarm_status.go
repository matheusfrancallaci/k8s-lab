package handlers

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"estudo-app/internal/tutor"
)

type PrewarmStatus struct {
	Context   string   `json:"context"`
	State     string   `json:"state"`
	Images    []string `json:"images"`
	Resolved  []string `json:"resolved_image_ids,omitempty"`
	StartedAt string   `json:"started_at,omitempty"`
	ReadyAt   string   `json:"ready_at,omitempty"`
	Failure   string   `json:"failure,omitempty"`
}

var prewarmStatusStore = struct {
	sync.Mutex
	ByContext map[string]PrewarmStatus `json:"by_context"`
}{ByContext: map[string]PrewarmStatus{}}

func savePrewarmStatus(status PrewarmStatus) {
	prewarmStatusStore.Lock()
	defer prewarmStatusStore.Unlock()
	prewarmStatusStore.ByContext[status.Context] = status
	path := filepath.Join("data", "labs", "prewarm.json")
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	b, err := json.MarshalIndent(struct {
		ByContext map[string]PrewarmStatus `json:"by_context"`
	}{ByContext: prewarmStatusStore.ByContext}, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}

func LabReadinessStatusHandler(w http.ResponseWriter, r *http.Request) {
	prewarmStatusStore.Lock()
	prewarm := make(map[string]PrewarmStatus, len(prewarmStatusStore.ByContext))
	for k, v := range prewarmStatusStore.ByContext {
		prewarm[k] = v
	}
	prewarmStatusStore.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"catalog":      tutor.LabCatalog(),
		"prewarm":      prewarm,
	})
}
