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

type PromptQualityCase struct {
	ID              string    `json:"id"`
	Prompt          string    `json:"prompt"`
	PromptHash      string    `json:"prompt_hash"`
	UserHash        string    `json:"user_hash,omitempty"`
	ActiveCert      string    `json:"active_cert,omitempty"`
	Cert            string    `json:"cert,omitempty"`
	Topic           string    `json:"topic,omitempty"`
	ActionType      string    `json:"action_type,omitempty"`
	Count           int       `json:"count"`
	FirstSeen       time.Time `json:"first_seen,omitempty"`
	LastSeen        time.Time `json:"last_seen,omitempty"`
	AvgQuality      float64   `json:"avg_quality"`
	MinQuality      int       `json:"min_quality"`
	MaxQuality      int       `json:"max_quality"`
	LastQuality     int       `json:"last_quality"`
	RegressionScore int       `json:"regression_score"`
	TotalLabs       int       `json:"total_labs"`
	LabIDs          []string  `json:"lab_ids,omitempty"`
	Dependencies    []string  `json:"dependencies,omitempty"`
	Sources         []string  `json:"sources,omitempty"`
	Evidence        []string  `json:"evidence,omitempty"`
	Chunks          []string  `json:"chunks,omitempty"`
	Risks           []string  `json:"risks,omitempty"`
}

type PromptQualitySummary struct {
	Total           int                 `json:"total"`
	AvgQuality      int                 `json:"avg_quality"`
	AvgRegression   int                 `json:"avg_regression"`
	LowQuality      int                 `json:"low_quality"`
	NeedsAttention  int                 `json:"needs_attention"`
	UpdatedAt       time.Time           `json:"updated_at,omitempty"`
	Weakest         []PromptQualityCase `json:"weakest"`
	Best            []PromptQualityCase `json:"best"`
	RegressionCases []PromptQualityCase `json:"regression_cases"`
	UnpromotedWeak  []PromptQualityCase `json:"unpromoted_weak"`
}

type regressionFixtureFile struct {
	Cases []PromptQualityCase `json:"cases"`
}

func regressionFixturePath() string { return filepath.Join("data", "eval", "regression_fixtures.json") }

func loadRegressionFixtures() []PromptQualityCase {
	var file regressionFixtureFile
	if b, err := os.ReadFile(regressionFixturePath()); err == nil {
		_ = json.Unmarshal(b, &file)
	}
	return file.Cases
}

// PromotePromptRegression makes a reviewed real prompt a durable fixture. It
// stores only routing/quality metadata, never the user hash.
func PromotePromptRegression(id string) error {
	promptQualityMu.Lock()
	defer promptQualityMu.Unlock()
	c := ensurePromptQualityLocked().Cases[id]
	if c == nil {
		return os.ErrNotExist
	}
	file := regressionFixtureFile{Cases: loadRegressionFixtures()}
	for _, existing := range file.Cases {
		if existing.ID == c.ID {
			return nil
		}
	}
	fixture := *c
	fixture.UserHash = ""
	fixture.FirstSeen = time.Time{}
	fixture.LastSeen = time.Time{}
	file.Cases = append(file.Cases, fixture)
	if err := os.MkdirAll(filepath.Dir(regressionFixturePath()), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(regressionFixturePath(), b, 0o644)
}

type promptQualityDataset struct {
	Cases     map[string]*PromptQualityCase `json:"cases"`
	UpdatedAt time.Time                     `json:"updated_at,omitempty"`
}

var (
	promptQualityMu     sync.Mutex
	promptQualityLoaded bool
	promptQualityState  *promptQualityDataset
)

func promptQualityPath() string {
	if p := strings.TrimSpace(os.Getenv("PROMPT_QUALITY_PATH")); p != "" {
		return p
	}
	return filepath.Join("data", "eval", "prompt_quality.json")
}

func ensurePromptQualityLocked() *promptQualityDataset {
	if promptQualityLoaded && promptQualityState != nil {
		return promptQualityState
	}
	promptQualityLoaded = true
	st := &promptQualityDataset{}
	if b, err := os.ReadFile(promptQualityPath()); err == nil {
		_ = json.Unmarshal(b, st)
	}
	if st.Cases == nil {
		st.Cases = map[string]*PromptQualityCase{}
	}
	promptQualityState = st
	return st
}

func savePromptQualityLocked(st *promptQualityDataset) {
	if st == nil {
		return
	}
	st.UpdatedAt = time.Now()
	path := promptQualityPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if b, err := json.MarshalIndent(st, "", "  "); err == nil {
		_ = os.WriteFile(path, b, 0o644)
	}
}

func RecordPromptQuality(userID, prompt, activeCert string, res ChatResult) {
	prompt = compactText(prompt, 320)
	if prompt == "" {
		return
	}
	isSession := res.Action != nil && res.Action.Type == "session"
	if !isSession && !isBroadLabRequest(prompt) {
		return
	}

	action := &ChatAction{}
	if res.Action != nil {
		action = res.Action
	}
	score := action.Quality
	if score == 0 {
		score = averageQuestionQuality(res.Questions)
	}
	risks := promptQualityRisks(isSession, score, action, res.Questions)
	regressionScore := promptRegressionScore(score, risks)
	now := time.Now()
	key := ragID(strings.ToLower(prompt), activeCert, action.Cert, action.Topic)

	promptQualityMu.Lock()
	defer promptQualityMu.Unlock()
	st := ensurePromptQualityLocked()
	c := st.Cases[key]
	if c == nil {
		c = &PromptQualityCase{
			ID:         key,
			Prompt:     prompt,
			PromptHash: ragID(prompt),
			UserHash:   ragID(userID),
			ActiveCert: activeCert,
			MinQuality: score,
			MaxQuality: score,
			FirstSeen:  now,
		}
		st.Cases[key] = c
	}
	prevCount := c.Count
	c.Count++
	c.LastSeen = now
	c.ActionType = action.Type
	c.Cert = action.Cert
	c.Topic = action.Topic
	c.LastQuality = score
	if prevCount == 0 {
		c.AvgQuality = float64(score)
		c.MinQuality = score
		c.MaxQuality = score
	} else {
		c.AvgQuality = (c.AvgQuality*float64(prevCount) + float64(score)) / float64(c.Count)
		if score < c.MinQuality {
			c.MinQuality = score
		}
		if score > c.MaxQuality {
			c.MaxQuality = score
		}
	}
	c.RegressionScore = regressionScore
	c.TotalLabs = action.Total
	c.LabIDs = limitedStrings(questionIDs(res.Questions), 5)
	c.Dependencies = limitedStrings(action.Dependencies, 6)
	c.Sources = limitedStrings(action.Sources, 6)
	c.Evidence = limitedStrings(action.Evidence, 6)
	c.Chunks = limitedStrings(action.Chunks, 6)
	c.Risks = risks
	savePromptQualityLocked(st)
}

func PromptQualityReport() PromptQualitySummary {
	promptQualityMu.Lock()
	defer promptQualityMu.Unlock()
	st := ensurePromptQualityLocked()
	report := PromptQualitySummary{UpdatedAt: st.UpdatedAt}
	var all []PromptQualityCase
	var qualitySum, regressionSum int
	for _, c := range st.Cases {
		if c == nil {
			continue
		}
		cp := *c
		all = append(all, cp)
		report.Total++
		qualitySum += int(cp.AvgQuality + 0.5)
		regressionSum += cp.RegressionScore
		if cp.AvgQuality < float64(minimumLabQuality) {
			report.LowQuality++
		}
		if cp.RegressionScore < minimumLabQuality || len(cp.Risks) > 0 {
			report.NeedsAttention++
		}
	}
	if report.Total > 0 {
		report.AvgQuality = qualitySum / report.Total
		report.AvgRegression = regressionSum / report.Total
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].RegressionScore == all[j].RegressionScore {
			return all[i].LastSeen.After(all[j].LastSeen)
		}
		return all[i].RegressionScore < all[j].RegressionScore
	})
	report.Weakest = limitedCases(all, 8)
	report.RegressionCases = limitedCases(all, 12)
	for _, c := range report.Weakest {
		if c.RegressionScore < minimumLabQuality {
			report.UnpromotedWeak = append(report.UnpromotedWeak, c)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].RegressionScore == all[j].RegressionScore {
			return all[i].Count > all[j].Count
		}
		return all[i].RegressionScore > all[j].RegressionScore
	})
	report.Best = limitedCases(all, 5)
	return report
}

func HistoricalRegressionPrompts(limit int) []PromptQualityCase {
	report := PromptQualityReport()
	seen := map[string]bool{}
	var out []PromptQualityCase
	for _, c := range append(loadRegressionFixtures(), report.RegressionCases...) {
		if c.Prompt == "" || c.Cert == "" || c.Topic == "" || c.ActionType != "session" {
			continue
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		out = append(out, c)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func averageQuestionQuality(qs []models.Question) int {
	total, n := 0, 0
	for _, q := range qs {
		if q.LabSpec == nil {
			q = FinalizeLab(q, "")
		}
		if q.LabSpec != nil && q.LabSpec.Quality.Score > 0 {
			total += q.LabSpec.Quality.Score
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return total / n
}

func promptQualityRisks(isSession bool, score int, action *ChatAction, qs []models.Question) []string {
	var risks []string
	if !isSession {
		risks = append(risks, "pedido de lab nao gerou sessao")
	}
	if score < minimumLabQuality {
		risks = append(risks, "score abaixo do quality gate")
	}
	if action.Cert == "" {
		risks = append(risks, "certificacao nao inferida")
	}
	if action.Topic == "" {
		risks = append(risks, "topico nao inferido")
	}
	if isSession && len(qs) == 0 {
		risks = append(risks, "sessao sem questoes anexadas")
	}
	if isSession && len(action.Sources) == 0 {
		risks = append(risks, "sem fontes oficiais na action")
	}
	if isSession && len(action.Chunks) == 0 {
		risks = append(risks, "sem chunks RAG na action")
	}
	if !questionsHaveValidator(qs) {
		risks = append(risks, "sem validador automatico")
	}
	return limitedStrings(risks, 8)
}

func promptRegressionScore(quality int, risks []string) int {
	score := quality
	if score == 0 {
		score = 50
	}
	score -= len(risks) * 8
	if containsFold(risks, "nao gerou sessao") {
		score -= 25
	}
	if containsFold(risks, "sem validador") {
		score -= 12
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func questionsHaveValidator(qs []models.Question) bool {
	for _, q := range qs {
		if q.Validation != nil {
			return true
		}
		for _, g := range q.Goals {
			if g.Validation != nil && strings.TrimSpace(g.Validation.Command) != "" {
				return true
			}
		}
	}
	return false
}

func limitedStrings(items []string, limit int) []string {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, item := range items {
		item = compactText(item, 120)
		key := strings.ToLower(item)
		if item == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, item)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func limitedCases(items []PromptQualityCase, limit int) []PromptQualityCase {
	if limit <= 0 || len(items) == 0 {
		return nil
	}
	if len(items) < limit {
		limit = len(items)
	}
	out := make([]PromptQualityCase, limit)
	copy(out, items[:limit])
	return out
}

func resetPromptQualityForTest() {
	promptQualityMu.Lock()
	defer promptQualityMu.Unlock()
	promptQualityLoaded = true
	promptQualityState = &promptQualityDataset{Cases: map[string]*PromptQualityCase{}}
}
