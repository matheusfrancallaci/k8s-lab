package tutor

import (
	"encoding/json"
	"os"
	"path/filepath"
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
}

type FeedbackSummary struct {
	Positive int `json:"positive"`
	Negative int `json:"negative"`
	Total    int `json:"total"`
}

var feedbackMu sync.Mutex

func RecordTutorFeedback(userID, messageID, kind, cert, topic string) error {
	if kind != "helpful" && kind != "unhelpful" {
		return nil
	}
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	dir := filepath.Join("data", "eval")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "tutor_feedback.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	e := TutorFeedback{At: time.Now().UTC(), UserHash: ragID(userID), MessageID: messageID, Kind: kind, Cert: cert, Topic: topic}
	return json.NewEncoder(f).Encode(e)
}

func TutorFeedbackSummary() FeedbackSummary {
	feedbackMu.Lock()
	defer feedbackMu.Unlock()
	b, err := os.ReadFile(filepath.Join("data", "eval", "tutor_feedback.jsonl"))
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
