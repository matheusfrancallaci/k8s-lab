package tutor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"estudo-app/internal/models"
)

type LabObservationSummary struct {
	Labs        int                `json:"labs"`
	Attempts    int                `json:"attempts"`
	Successes   int                `json:"successes"`
	Failures    int                `json:"failures"`
	SuccessRate float64            `json:"success_rate"`
	TopFailures []LabFailureMetric `json:"top_failures"`
	StuckTopics []LabTopicMetric   `json:"stuck_topics"`
	UpdatedAt   time.Time          `json:"updated_at,omitempty"`
}

type LabFailureMetric struct {
	LabID      string    `json:"lab_id,omitempty"`
	Cert       string    `json:"cert,omitempty"`
	Topic      string    `json:"topic,omitempty"`
	Command    string    `json:"command"`
	Count      int       `json:"count"`
	LastOutput string    `json:"last_output,omitempty"`
	LastSeen   time.Time `json:"last_seen,omitempty"`
}

type LabTopicMetric struct {
	Cert          string    `json:"cert"`
	Topic         string    `json:"topic"`
	Attempts      int       `json:"attempts"`
	Successes     int       `json:"successes"`
	Failures      int       `json:"failures"`
	SetupWarnings int       `json:"setup_warnings"`
	TermErrors    int       `json:"term_errors"`
	SuccessRate   float64   `json:"success_rate"`
	LastSeen      time.Time `json:"last_seen,omitempty"`
}

type labObservationState struct {
	Labs      map[string]bool              `json:"labs"`
	Attempts  int                          `json:"attempts"`
	Successes int                          `json:"successes"`
	Failures  int                          `json:"failures"`
	FailedCmd map[string]*LabFailureMetric `json:"failed_cmd"`
	Topics    map[string]*LabTopicMetric   `json:"topics"`
	UpdatedAt time.Time                    `json:"updated_at,omitempty"`
}

var (
	labObsMu     sync.Mutex
	labObsLoaded bool
	labObsState  *labObservationState
)

func labObservabilityPath() string {
	if p := strings.TrimSpace(os.Getenv("LAB_OBSERVABILITY_PATH")); p != "" {
		return p
	}
	return filepath.Join("data", "observability", "labs.json")
}

func ensureLabObsLocked() *labObservationState {
	if labObsLoaded && labObsState != nil {
		return labObsState
	}
	labObsLoaded = true
	st := &labObservationState{}
	if b, err := os.ReadFile(labObservabilityPath()); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Labs == nil {
		st.Labs = map[string]bool{}
	}
	if st.FailedCmd == nil {
		st.FailedCmd = map[string]*LabFailureMetric{}
	}
	if st.Topics == nil {
		st.Topics = map[string]*LabTopicMetric{}
	}
	labObsState = st
	return st
}

func saveLabObsLocked(st *labObservationState) {
	if st == nil {
		return
	}
	st.UpdatedAt = time.Now()
	path := labObservabilityPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if b, err := json.MarshalIndent(st, "", "  "); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

func topicMetricLocked(st *labObservationState, q models.Question) *LabTopicMetric {
	cert, topic := string(q.Cert), strings.TrimSpace(q.Topic)
	if cert == "" {
		cert = "Geral"
	}
	if topic == "" {
		topic = "Sem topico"
	}
	key := cert + "|" + topic
	m := st.Topics[key]
	if m == nil {
		m = &LabTopicMetric{Cert: cert, Topic: topic}
		st.Topics[key] = m
	}
	m.LastSeen = time.Now()
	return m
}

func observeLabLocked(st *labObservationState, q models.Question) {
	id := strings.TrimSpace(q.ID)
	if id == "" {
		id = string(q.Cert) + "|" + q.Topic
	}
	st.Labs[id] = true
}

func recordFailedCommandLocked(st *labObservationState, q models.Question, command, output string) {
	command = compactText(command, 220)
	if command == "" {
		return
	}
	key := ragID(string(q.Cert), q.Topic, command)
	f := st.FailedCmd[key]
	if f == nil {
		f = &LabFailureMetric{
			LabID:   q.ID,
			Cert:    string(q.Cert),
			Topic:   q.Topic,
			Command: command,
		}
		st.FailedCmd[key] = f
	}
	f.Count++
	f.LastOutput = compactText(output, 260)
	f.LastSeen = time.Now()
}

// RecordLabSetup registra avisos/falhas do provisionamento do lab. Isso mostra
// quais dependências estão travando os alunos ou o cluster.
func RecordLabSetup(userID string, q models.Question, command, status, output string) {
	_ = userID
	if strings.TrimSpace(command) == "" {
		return
	}
	labObsMu.Lock()
	defer labObsMu.Unlock()
	st := ensureLabObsLocked()
	observeLabLocked(st, q)
	m := topicMetricLocked(st, q)
	if strings.EqualFold(status, "warn") || strings.Contains(strings.ToLower(output), "forbidden") {
		m.SetupWarnings++
		recordFailedCommandLocked(st, q, command, output)
	}
	saveLabObsLocked(st)
}

// RecordLabValidation registra o resultado de um validador real do lab.
func RecordLabValidation(userID string, q models.Question, goal int, command string, success bool, output string) {
	_ = userID
	_ = goal
	if strings.TrimSpace(command) == "" {
		return
	}
	labObsMu.Lock()
	defer labObsMu.Unlock()
	st := ensureLabObsLocked()
	observeLabLocked(st, q)
	st.Attempts++
	m := topicMetricLocked(st, q)
	m.Attempts++
	if success {
		st.Successes++
		m.Successes++
	} else {
		st.Failures++
		m.Failures++
		recordFailedCommandLocked(st, q, command, output)
	}
	saveLabObsLocked(st)
}

func recordLabTerminalError(userID string, q models.Question, output string) {
	_ = userID
	labObsMu.Lock()
	defer labObsMu.Unlock()
	st := ensureLabObsLocked()
	observeLabLocked(st, q)
	m := topicMetricLocked(st, q)
	m.TermErrors++
	if strings.TrimSpace(output) != "" {
		recordFailedCommandLocked(st, q, "terminal error", output)
	}
	saveLabObsLocked(st)
}

func LabObservability() LabObservationSummary {
	labObsMu.Lock()
	defer labObsMu.Unlock()
	st := ensureLabObsLocked()
	summary := LabObservationSummary{
		Labs:      len(st.Labs),
		Attempts:  st.Attempts,
		Successes: st.Successes,
		Failures:  st.Failures,
		UpdatedAt: st.UpdatedAt,
	}
	if st.Attempts > 0 {
		summary.SuccessRate = float64(st.Successes) / float64(st.Attempts)
	}
	for _, f := range st.FailedCmd {
		if f != nil {
			summary.TopFailures = append(summary.TopFailures, *f)
		}
	}
	sort.SliceStable(summary.TopFailures, func(i, j int) bool {
		if summary.TopFailures[i].Count == summary.TopFailures[j].Count {
			return summary.TopFailures[i].LastSeen.After(summary.TopFailures[j].LastSeen)
		}
		return summary.TopFailures[i].Count > summary.TopFailures[j].Count
	})
	if len(summary.TopFailures) > 5 {
		summary.TopFailures = summary.TopFailures[:5]
	}
	for _, t := range st.Topics {
		if t == nil {
			continue
		}
		cp := *t
		if cp.Attempts > 0 {
			cp.SuccessRate = float64(cp.Successes) / float64(cp.Attempts)
		}
		summary.StuckTopics = append(summary.StuckTopics, cp)
	}
	sort.SliceStable(summary.StuckTopics, func(i, j int) bool {
		a, b := summary.StuckTopics[i], summary.StuckTopics[j]
		aPain := a.Failures + a.SetupWarnings + a.TermErrors
		bPain := b.Failures + b.SetupWarnings + b.TermErrors
		if aPain == bPain {
			return a.LastSeen.After(b.LastSeen)
		}
		return aPain > bPain
	})
	if len(summary.StuckTopics) > 5 {
		summary.StuckTopics = summary.StuckTopics[:5]
	}
	return summary
}

func resetLabObservabilityForTest() {
	labObsMu.Lock()
	defer labObsMu.Unlock()
	labObsLoaded = true
	labObsState = &labObservationState{
		Labs:      map[string]bool{},
		FailedCmd: map[string]*LabFailureMetric{},
		Topics:    map[string]*LabTopicMetric{},
	}
}
