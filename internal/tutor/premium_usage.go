package tutor

import (
	"encoding/json"
	"estudo-app/internal/persistence"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type PremiumUsage struct {
	Model     string `json:"model"`
	Used      int    `json:"used"`
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Available bool   `json:"available"`
}

type premiumUsageDataset struct {
	Users map[string]int `json:"users"`
}

var premiumUsageMu sync.Mutex

func premiumUsagePath() string {
	if p := strings.TrimSpace(os.Getenv("TUTOR_PREMIUM_USAGE_PATH")); p != "" {
		return p
	}
	return filepath.Join("data", "tutor", "premium-usage.json")
}

func premiumQuestionLimit() int {
	n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("LLM_PREMIUM_QUESTION_LIMIT")))
	if err != nil || n < 1 {
		return 10
	}
	return n
}

func isSolModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return model == "gpt-5.6-sol" || model == "gpt-5.6"
}

func solConfigured() bool {
	c, remote := remoteLLM()
	if !remote {
		return false
	}
	return isSolModel(c.Model) || isSolModel(os.Getenv("LLM_FRONTIER_MODEL")) ||
		isSolModel(os.Getenv("LLM_FAST_MODEL")) || isSolModel(os.Getenv("LLM_CHAT_MODEL"))
}

func loadPremiumUsageLocked() premiumUsageDataset {
	d := premiumUsageDataset{Users: map[string]int{}}
	if persistence.Enabled() {
		if found, err := persistence.Get("tutor_premium_usage", "global", &d); err == nil && found {
			if d.Users == nil {
				d.Users = map[string]int{}
			}
			return d
		}
	}
	if b, err := os.ReadFile(premiumUsagePath()); err == nil {
		_ = json.Unmarshal(b, &d)
	}
	if d.Users == nil {
		d.Users = map[string]int{}
	}
	return d
}

func savePremiumUsageLocked(d premiumUsageDataset) error {
	path := premiumUsagePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if persistence.Enabled() {
		_ = persistence.Put("tutor_premium_usage", "global", d)
	}
	return nil
}

func PremiumUsageFor(userID string) PremiumUsage {
	premiumUsageMu.Lock()
	defer premiumUsageMu.Unlock()
	used := loadPremiumUsageLocked().Users[conversationUserKey(userID)]
	limit := premiumQuestionLimit()
	remaining := limit - used
	if remaining < 0 {
		remaining = 0
	}
	return PremiumUsage{Model: "gpt-5.6-sol", Used: used, Limit: limit, Remaining: remaining, Available: solConfigured()}
}

// ReserveConversationRoute applies the Sol-only per-user allowance. Local
// models and every other remote model remain unlimited.
func ReserveConversationRoute(userID, msg, mode string) (ModelRoute, PremiumUsage, bool) {
	route := RouteConversationModel(msg, mode)
	if !isSolModel(route.Model) {
		return route, PremiumUsageFor(userID), false
	}
	premiumUsageMu.Lock()
	defer premiumUsageMu.Unlock()
	d := loadPremiumUsageLocked()
	key := conversationUserKey(userID)
	limit := premiumQuestionLimit()
	if d.Users[key] >= limit {
		fallback := strings.TrimSpace(os.Getenv("LLM_FAST_MODEL"))
		if fallback == "" || isSolModel(fallback) {
			fallback = "gpt-5.6-luna"
		}
		route = ModelRoute{Tier: "fast", Model: fallback, Score: route.Score, Reason: "limite do GPT-5.6 Sol atingido; usando fallback"}
		return route, PremiumUsage{Model: "gpt-5.6-sol", Used: d.Users[key], Limit: limit, Remaining: 0, Available: true}, false
	}
	d.Users[key]++
	_ = savePremiumUsageLocked(d)
	used := d.Users[key]
	return route, PremiumUsage{Model: "gpt-5.6-sol", Used: used, Limit: limit, Remaining: limit - used, Available: true}, true
}

func ReleasePremiumQuestion(userID string) {
	premiumUsageMu.Lock()
	defer premiumUsageMu.Unlock()
	d := loadPremiumUsageLocked()
	key := conversationUserKey(userID)
	if d.Users[key] > 0 {
		d.Users[key]--
		_ = savePremiumUsageLocked(d)
	}
}
