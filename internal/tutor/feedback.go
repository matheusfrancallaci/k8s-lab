package tutor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type TutorFeedback struct {
	At        time.Time `json:"at"`
	UserHash  string    `json:"user_hash"`
	MessageID string    `json:"message_id,omitempty"`
	Kind      string    `json:"kind"`
	Cert      string    `json:"cert,omitempty"`
	Topic     string    `json:"topic,omitempty"`
	Prompt    string    `json:"prompt,omitempty"`
	// EvalCase é o id do caso de prompt-quality promovido a fixture de
	// regressão por causa deste feedback — o elo "👎 do aluno → eval".
	EvalCase string `json:"eval_case,omitempty"`
}

type FeedbackSummary struct {
	Positive int `json:"positive"`
	Negative int `json:"negative"`
	Total    int `json:"total"`
	// PromotedToEval conta quantos 👎 viraram caso durável de regressão.
	PromotedToEval int `json:"promoted_to_eval"`
}

var feedbackMu sync.Mutex

func feedbackLogPath() string {
	if p := strings.TrimSpace(os.Getenv("TUTOR_FEEDBACK_PATH")); p != "" {
		return p
	}
	return filepath.Join("data", "eval", "tutor_feedback.jsonl")
}

func RecordTutorFeedback(userID, messageID, kind, cert, topic, prompt string) error {
	if kind != "helpful" && kind != "unhelpful" {
		return nil
	}
	// Feedback negativo fecha o loop de qualidade: o prompt avaliado vira
	// fixture de regressão e passa a rodar em todo golden eval. Promove ANTES
	// de segurar feedbackMu (a promoção usa o mutex do prompt-quality).
	evalCase := ""
	if kind == "unhelpful" {
		evalCase, _ = PromoteNegativeFeedbackRegression(prompt)
	}
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	path := feedbackLogPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	e := TutorFeedback{
		At:        time.Now().UTC(),
		UserHash:  ragID(userID),
		MessageID: messageID,
		Kind:      kind,
		Cert:      cert,
		Topic:     topic,
		Prompt:    compactText(prompt, 320),
		EvalCase:  evalCase,
	}
	return json.NewEncoder(f).Encode(e)
}

func TutorFeedbackSummary() FeedbackSummary {
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	b, err := os.ReadFile(feedbackLogPath())
	if err != nil {
		return FeedbackSummary{}
	}
	var s FeedbackSummary
	for _, line := range bytesLines(b) {
		var e TutorFeedback
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		s.Total++
		if e.Kind == "helpful" {
			s.Positive++
		} else if e.Kind == "unhelpful" {
			s.Negative++
		}
		if e.EvalCase != "" {
			s.PromotedToEval++
		}
	}
	return s
}

func bytesLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			if i > start {
				out = append(out, b[start:i])
			}
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}
