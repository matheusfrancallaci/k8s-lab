package tutor

import (
	"fmt"
	"strings"

	"estudo-app/internal/models"
)

type LearningPath struct {
	Name        string   `json:"name"`
	Cert        string   `json:"cert"`
	Topic       string   `json:"topic"`
	Description string   `json:"description"`
	Topics      []string `json:"topics"`
	ExamCount   int      `json:"exam_count"`
	Minutes     int      `json:"minutes"`
}

func BuildLearningPath(request, activeCert string) LearningPath {
	cert := inferCertFromMessage(request, activeCert)
	if cert == "" {
		cert = "CKA"
	}
	topic := exactTopicForRequest(cert, request)
	if topic == "" {
		topic = detectTopic(request)
	}
	if topic == "" {
		topic = fallbackTopicsForCert(cert, request)[0]
	}
	topics := pathTopics(cert, topic, request)
	return LearningPath{
		Name:        fmt.Sprintf("%s - %s", cert, topic),
		Cert:        cert,
		Topic:       topic,
		Description: "Trilha progressiva com fundamento, pratica guiada, troubleshooting, reforco e prova curta.",
		Topics:      topics,
		ExamCount:   8,
		Minutes:     len(topics) * 18,
	}
}

func GenerateLearningPath(request, activeCert string, level int) ([]models.Question, LearningPath, error) {
	path := BuildLearningPath(request, activeCert)
	if level < 1 || level > 3 {
		level = 2
	}
	var qs []models.Question
	for _, topic := range path.Topics {
		if _, ok := templates[topic]; !ok {
			continue
		}
		qs = append(qs, generateQuestions(topic, path.Cert, level, 1)...)
	}
	if len(qs) == 0 {
		return nil, path, fmt.Errorf("nao consegui montar trilha com os templates atuais")
	}
	qs = FinalizeLabs(qs, request)
	for _, q := range qs {
		if err := LabQualityGate(q); err != nil {
			return nil, path, err
		}
	}
	if err := persist(qs); err != nil {
		return nil, path, err
	}
	return qs, path, nil
}

func pathTopics(cert, topic, request string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		if _, ok := templates[t]; !ok {
			return
		}
		seen[t] = true
		out = append(out, t)
	}
	add(topic)
	switch {
	case topic == "Autoscaling":
		add("Workloads")
		add("Autoscaling")
		add("Troubleshooting")
	case topic == "ReplicaSet":
		add("Workloads")
		add("ReplicaSet")
		add("Troubleshooting")
	case strings.Contains(strings.ToLower(request), "helm") || topic == "Helm":
		add("Configuration")
		add("Helm")
		add("Services")
	case strings.Contains(strings.ToLower(request), "docker") || topic == "Docker":
		add("Docker")
		add("Linux")
		add("Troubleshooting")
	case cert == "AWS" || strings.HasPrefix(topic, "AWS "):
		for _, t := range []string{"AWS Compute", "AWS Networking", "AWS IAM", "AWS Storage", "AWS Messaging"} {
			add(t)
		}
	default:
		for _, t := range fallbackTopicsForCert(cert, request) {
			add(t)
		}
	}
	add("Troubleshooting")
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func GenerateReplayLab(userID, activeCert string) ([]models.Question, string) {
	queue := ReviewQueue(userID)
	for _, item := range queue {
		if activeCert != "" && item.Cert != "" && !strings.EqualFold(item.Cert, activeCert) {
			continue
		}
		if _, ok := templates[item.Topic]; !ok {
			continue
		}
		cert := item.Cert
		if cert == "" {
			cert = activeCert
		}
		if cert == "" {
			cert = "CKA"
		}
		qs := generateQuestions(item.Topic, cert, 1, 1)
		if len(qs) == 0 {
			continue
		}
		qs[0].Question = "Reforco do erro anterior: " + qs[0].Question
		qs = FinalizeLabs(qs, "replay de erro "+item.Topic)
		if LabQualityGate(qs[0]) == nil {
			_ = persist(qs)
			return qs, item.Topic
		}
	}
	return nil, ""
}
