package tutor

import (
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

type ModelRoute struct {
	Tier   string `json:"tier"`
	Model  string `json:"model"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

func RouteConversationModel(msg, mode string) ModelRoute {
	score := len(strings.Fields(msg)) / 20
	complex := regexp.MustCompile(`(?i)arquitet|trade.?off|incidente|diagn[oó]st|migra|seguran|produ[cç][aã]o|multi.?cluster|terraform|c[oó]digo|analise|compare|por que`)
	if complex.MatchString(msg) {
		score += 3
	}
	if mode == "deep" || mode == "diagnostic" {
		score += 3
	}
	if strings.Count(msg, "\n") >= 5 {
		score += 2
	}
	if score > 10 {
		score = 10
	}

	local := chatModel()
	if _, remote := remoteLLM(); remote {
		fast := strings.TrimSpace(os.Getenv("LLM_FAST_MODEL"))
		if fast == "" {
			fast = remoteModelFor("chat", "")
		}
		frontier := strings.TrimSpace(os.Getenv("LLM_FRONTIER_MODEL"))
		if frontier == "" {
			frontier = remoteModelFor("chat", "")
		}
		candidate := strings.TrimSpace(os.Getenv("LLM_CANDIDATE_MODEL"))
		pct, _ := strconv.Atoi(strings.TrimSpace(os.Getenv("LLM_CANARY_PERCENT")))
		if candidate != "" && pct > 0 && stableBucket(msg) < pct {
			return ModelRoute{Tier: "canary", Model: candidate, Score: score, Reason: "amostra deterministica do modelo candidato"}
		}
		if score >= 5 {
			return ModelRoute{Tier: "frontier", Model: frontier, Score: score, Reason: "pedido complexo ou modo aprofundado"}
		}
		return ModelRoute{Tier: "fast", Model: fast, Score: score, Reason: "pedido objetivo"}
	}
	return ModelRoute{Tier: "local", Model: local, Score: score, Reason: "provedor remoto desativado; privacidade e custo local"}
}

func stableBucket(text string) int {
	h := ragID(strings.ToLower(strings.TrimSpace(text)))
	total := 0
	for _, r := range h {
		total += int(r)
	}
	return total % 100
}

type ModelExperimentStat struct {
	Requests      int `json:"requests"`
	Passed        int `json:"passed"`
	Failed        int `json:"failed"`
	AvgCoverage   int `json:"avg_coverage"`
	coverageTotal int
}
type ModelExperimentReport struct {
	Models              map[string]ModelExperimentStat `json:"models"`
	RollbackRecommended bool                           `json:"rollback_recommended"`
	Candidate           string                         `json:"candidate,omitempty"`
}

var modelExperiments = struct {
	sync.Mutex
	Values map[string]ModelExperimentStat
}{Values: map[string]ModelExperimentStat{}}

func RecordModelOutcome(route ModelRoute, audit GroundingAudit) {
	if route.Model == "" {
		return
	}
	modelExperiments.Lock()
	defer modelExperiments.Unlock()
	s := modelExperiments.Values[route.Model]
	s.Requests++
	s.coverageTotal += audit.Coverage
	if audit.Passed {
		s.Passed++
	} else {
		s.Failed++
	}
	s.AvgCoverage = s.coverageTotal / s.Requests
	modelExperiments.Values[route.Model] = s
}

func ModelExperiments() ModelExperimentReport {
	modelExperiments.Lock()
	defer modelExperiments.Unlock()
	out := ModelExperimentReport{Models: map[string]ModelExperimentStat{}, Candidate: strings.TrimSpace(os.Getenv("LLM_CANDIDATE_MODEL"))}
	for k, v := range modelExperiments.Values {
		out.Models[k] = v
		if k == out.Candidate && v.Requests >= 10 && (v.AvgCoverage < 80 || v.Failed*100/v.Requests > 20) {
			out.RollbackRecommended = true
		}
	}
	return out
}
