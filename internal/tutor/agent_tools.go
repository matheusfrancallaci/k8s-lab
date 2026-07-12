package tutor

import (
	"strings"
	"sync"
	"time"
)

type AgentToolStep struct {
	Tool        string `json:"tool"`
	Purpose     string `json:"purpose"`
	Status      string `json:"status"`
	Observation string `json:"observation,omitempty"`
	ReadOnly    bool   `json:"read_only"`
}

type AgentTrace struct {
	StartedAt  time.Time       `json:"started_at"`
	FinishedAt time.Time       `json:"finished_at"`
	Steps      []AgentToolStep `json:"steps"`
}

var agentTraces = struct {
	sync.RWMutex
	Values map[string]AgentTrace
}{Values: map[string]AgentTrace{}}

func WantsClusterInspection(msg, mode string) bool {
	l := strings.ToLower(msg)
	explicit := strings.Contains(l, "meu cluster") || strings.Contains(l, "o cluster") || strings.Contains(l, "cluster atual")
	action := strings.Contains(l, "inspec") || strings.Contains(l, "diagnost") || strings.Contains(l, "investig") || strings.Contains(l, "verifique")
	return explicit && action && (mode == "diagnostic" || strings.Contains(l, "somente leitura") || strings.Contains(l, "read-only"))
}

func RecordAgentTrace(userID string, trace AgentTrace) {
	agentTraces.Lock()
	defer agentTraces.Unlock()
	agentTraces.Values[ragID(userID)] = trace
}

func LastAgentTrace(userID string) AgentTrace {
	agentTraces.RLock()
	defer agentTraces.RUnlock()
	return agentTraces.Values[ragID(userID)]
}
