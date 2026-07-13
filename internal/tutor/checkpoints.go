package tutor

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"estudo-app/internal/persistence"
)

type TutorCheckpoint struct {
	ID        string    `json:"id"`
	Question  string    `json:"question"`
	Topic     string    `json:"topic,omitempty"`
	Expected  []string  `json:"expected,omitempty"`
	Attempts  int       `json:"attempts"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type CheckpointEvaluation struct {
	CheckpointID string   `json:"checkpoint_id"`
	Score        int      `json:"score"`
	Outcome      string   `json:"outcome"` // deepen | remediate | release
	Matched      []string `json:"matched,omitempty"`
	Missing      []string `json:"missing,omitempty"`
	Feedback     string   `json:"feedback"`
	NextPrompt   string   `json:"next_prompt,omitempty"`
}

type persistedCheckpoint struct {
	Key        string          `json:"key"`
	Checkpoint TutorCheckpoint `json:"checkpoint"`
}

var checkpoints = struct {
	sync.Mutex
	Values map[string]TutorCheckpoint
	Loaded bool
}{Values: map[string]TutorCheckpoint{}}

func checkpointsPath() string {
	if p := strings.TrimSpace(os.Getenv("TUTOR_CHECKPOINTS_PATH")); p != "" {
		return p
	}
	return filepath.Join("data", "tutor", "checkpoints.json")
}

func ensureCheckpointsLoadedLocked() {
	if checkpoints.Loaded {
		return
	}
	checkpoints.Loaded = true
	if persistence.Enabled() {
		var items []persistedCheckpoint
		if persistence.List("tutor_checkpoint", &items) == nil {
			for _, item := range items {
				checkpoints.Values[item.Key] = item.Checkpoint
			}
		}
	}
	if b, err := os.ReadFile(checkpointsPath()); err == nil {
		var local map[string]TutorCheckpoint
		if json.Unmarshal(b, &local) == nil {
			for key, cp := range local {
				if _, exists := checkpoints.Values[key]; !exists {
					checkpoints.Values[key] = cp
				}
			}
		}
	}
	if checkpoints.Values == nil {
		checkpoints.Values = map[string]TutorCheckpoint{}
	}
}

func saveCheckpointsLocked() {
	if persistence.Enabled() {
		for key, cp := range checkpoints.Values {
			_ = persistence.Put("tutor_checkpoint", key, persistedCheckpoint{Key: key, Checkpoint: cp})
		}
	}
	path := checkpointsPath()
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	b, err := json.Marshal(checkpoints.Values)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, path)
	}
}

func checkpointKey(userID, conversationID string) string { return ragID(userID, conversationID) }

func RegisterTutorCheckpoint(userID, conversationID string, plan TutorOrchestration) (TutorCheckpoint, bool) {
	if strings.TrimSpace(conversationID) == "" || plan.Intent == "practice" || plan.Intent == "exam" {
		return TutorCheckpoint{}, false
	}
	topic := plan.TargetTopic
	if topic == "" {
		topic = "o conceito discutido"
	}
	question, expected := checkpointFor(plan.Intent, topic)
	cp := TutorCheckpoint{ID: ragID(userID, conversationID, plan.TurnID), Question: question, Topic: topic, Expected: expected, Status: "awaiting", CreatedAt: time.Now().UTC()}
	checkpoints.Lock()
	ensureCheckpointsLoadedLocked()
	checkpoints.Values[checkpointKey(userID, conversationID)] = cp
	saveCheckpointsLocked()
	checkpoints.Unlock()
	return cp, true
}

func ActiveTutorCheckpoint(userID, conversationID string) (TutorCheckpoint, bool) {
	checkpoints.Lock()
	defer checkpoints.Unlock()
	ensureCheckpointsLoadedLocked()
	cp, ok := checkpoints.Values[checkpointKey(userID, conversationID)]
	return cp, ok && cp.Status == "awaiting"
}

func EvaluateTutorCheckpoint(userID, conversationID, answer string) (CheckpointEvaluation, bool) {
	if strings.TrimSpace(answer) == "" || regexp.MustCompile(`(?i)^(novo assunto|cancelar checkpoint|pular checkpoint)`).MatchString(strings.TrimSpace(answer)) {
		return CheckpointEvaluation{}, false
	}
	key := checkpointKey(userID, conversationID)
	checkpoints.Lock()
	defer checkpoints.Unlock()
	ensureCheckpointsLoadedLocked()
	cp, ok := checkpoints.Values[key]
	if !ok || cp.Status != "awaiting" {
		return CheckpointEvaluation{}, false
	}
	// Uma ordem nova nao e resposta ao checkpoint anterior. Troque de intencao
	// imediatamente para nao avaliar "criar lab..." como resposta conceitual.
	if explicitTutorCommand(answer) {
		cp.Status = "superseded"
		checkpoints.Values[key] = cp
		saveCheckpointsLocked()
		return CheckpointEvaluation{}, false
	}
	answerNorm := normalizeEvidenceText(answer)
	ev := CheckpointEvaluation{CheckpointID: cp.ID}
	for _, concept := range cp.Expected {
		if strings.Contains(answerNorm, normalizeEvidenceText(concept)) {
			ev.Matched = append(ev.Matched, concept)
		} else {
			ev.Missing = append(ev.Missing, concept)
		}
	}
	if len(cp.Expected) > 0 {
		ev.Score = len(ev.Matched) * 100 / len(cp.Expected)
	}
	cp.Attempts++
	switch {
	case ev.Score >= 70:
		ev.Outcome = "release"
		ev.Feedback = "Boa resposta: voce conectou os conceitos essenciais. A pratica esta liberada."
		ev.NextPrompt = fmt.Sprintf("crie um lab hands-on de %s nivel 2", cp.Topic)
		cp.Status = "passed"
	case ev.Score >= 35:
		ev.Outcome = "deepen"
		ev.Feedback = "Voce acertou parte do raciocinio. Aprofunde os pontos que faltaram antes de praticar."
		cp.Status = "awaiting"
	default:
		ev.Outcome = "remediate"
		ev.Feedback = "Ainda falta um pre-requisito importante. Vou reduzir a complexidade e reconstruir a base."
		cp.Status = "awaiting"
	}
	if cp.Attempts >= 3 && cp.Status == "awaiting" {
		cp.Status = "remediate"
		ev.Outcome = "remediate"
		ev.Feedback = "Vamos pausar o checkpoint e reforcar o pre-requisito com um exemplo menor."
	}
	checkpoints.Values[key] = cp
	saveCheckpointsLocked()
	return ev, true
}

var explicitTutorCommandRe = regexp.MustCompile(`(?i)^\s*(?:por\s+favor\s+)?(?:cri(?:e|ar)|ger(?:e|ar)|mont(?:e|ar)|fa(?:ca|ça|zer)|faz|quero|inici(?:e|ar)|abr(?:a|ir)|mostr(?:e|ar)|revis(?:e|ar)|simulado|exame|modo\s+incidente|como\s+estou)\b`)

func explicitTutorCommand(answer string) bool {
	return explicitTutorCommandRe.MatchString(strings.TrimSpace(answer))
}

func checkpointFor(intent, topic string) (string, []string) {
	l := strings.ToLower(topic)
	switch {
	case strings.Contains(l, "autoscal") || strings.Contains(l, "hpa"):
		return "Quais sinais e configuracoes precisam existir para um HPA conseguir escalar?", []string{"metricas", "requests", "target"}
	case strings.Contains(l, "storage"):
		return "Qual e a relacao entre PVC, PV e StorageClass neste fluxo?", []string{"pvc", "pv", "storageclass"}
	case strings.Contains(l, "security") || strings.Contains(l, "rbac"):
		return "Como voce separaria identidade, permissao e escopo nesta decisao?", []string{"identidade", "permissao", "escopo"}
	case intent == "diagnose":
		return "Qual hipotese voce testaria primeiro e qual evidencia confirmaria ou refutaria?", []string{"hipotese", "evidencia", "teste"}
	case intent == "compare":
		return fmt.Sprintf("Qual trade-off faria voce escolher uma alternativa em vez da outra em %s?", topic), []string{"trade-off", "contexto", "escolha"}
	default:
		return fmt.Sprintf("Explique %s com suas palavras e cite uma verificacao pratica.", topic), []string{topic, "verificar"}
	}
}
