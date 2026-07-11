package tutor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type LatencyMetric struct {
	Count           int `json:"count"`
	Failures        int `json:"failures"`
	AvgMS           int `json:"avg_ms"`
	P50MS           int `json:"p50_ms"`
	P95MS           int `json:"p95_ms"`
	P99MS           int `json:"p99_ms"`
	FirstTokenMS    int `json:"first_token_ms,omitempty"`
	FirstTokenCount int `json:"first_token_count,omitempty"`
}

type TutorTelemetryReport struct {
	Stages map[string]LatencyMetric `json:"stages"`
}

type latencySample struct {
	Stage      string        `json:"stage"`
	Duration   time.Duration `json:"duration_ns"`
	Failed     bool          `json:"failed"`
	FirstToken time.Duration `json:"first_token_ns,omitempty"`
	RecordedAt time.Time     `json:"recorded_at"`
}

var tutorTelemetry = struct {
	sync.Mutex
	loaded bool
	stages map[string][]latencySample
}{stages: map[string][]latencySample{}}

func telemetryPath() string {
	if p := strings.TrimSpace(os.Getenv("TUTOR_TELEMETRY_FILE")); p != "" {
		return p
	}
	return filepath.Join("data", "eval", "tutor_telemetry.jsonl")
}

func telemetryPersistenceEnabled() bool {
	v := strings.TrimSpace(os.Getenv("TUTOR_TELEMETRY_PERSIST"))
	return v == "" || (v != "0" && !strings.EqualFold(v, "false"))
}

func loadTutorTelemetryLocked() {
	if tutorTelemetry.loaded {
		return
	}
	tutorTelemetry.loaded = true
	b, err := os.ReadFile(telemetryPath())
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var sample latencySample
		if json.Unmarshal([]byte(line), &sample) != nil || sample.Stage == "" {
			continue
		}
		tutorTelemetry.stages[sample.Stage] = appendBounded(tutorTelemetry.stages[sample.Stage], sample)
	}
}

func appendBounded(samples []latencySample, sample latencySample) []latencySample {
	samples = append(samples, sample)
	if len(samples) > 2048 {
		samples = samples[len(samples)-2048:]
	}
	return samples
}

func recordTutorLatency(stage string, duration, firstToken time.Duration, failed bool) {
	sample := latencySample{Stage: stage, Duration: duration, FirstToken: firstToken, Failed: failed, RecordedAt: time.Now().UTC()}
	tutorTelemetry.Lock()
	defer tutorTelemetry.Unlock()
	loadTutorTelemetryLocked()
	tutorTelemetry.stages[stage] = appendBounded(tutorTelemetry.stages[stage], sample)
	if !telemetryPersistenceEnabled() {
		return
	}
	if err := os.MkdirAll(filepath.Dir(telemetryPath()), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(telemetryPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_ = json.NewEncoder(f).Encode(sample)
	_ = f.Close()
}

func percentile(sorted []int, pct int) int {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted)*pct + 99) / 100
	if idx < 1 {
		idx = 1
	}
	return sorted[idx-1]
}

func TutorTelemetry() TutorTelemetryReport {
	tutorTelemetry.Lock()
	defer tutorTelemetry.Unlock()
	loadTutorTelemetryLocked()
	report := TutorTelemetryReport{Stages: map[string]LatencyMetric{}}
	for stage, samples := range tutorTelemetry.stages {
		if len(samples) == 0 {
			continue
		}
		ms := make([]int, 0, len(samples))
		firstMS := make([]int, 0, len(samples))
		total, failures := 0, 0
		for _, s := range samples {
			v := int(s.Duration.Milliseconds())
			ms = append(ms, v)
			total += v
			if s.Failed {
				failures++
			}
			if s.FirstToken > 0 {
				firstMS = append(firstMS, int(s.FirstToken.Milliseconds()))
			}
		}
		sort.Ints(ms)
		sort.Ints(firstMS)
		metric := LatencyMetric{Count: len(samples), Failures: failures, AvgMS: total / len(samples), P50MS: percentile(ms, 50), P95MS: percentile(ms, 95), P99MS: percentile(ms, 99), FirstTokenCount: len(firstMS)}
		if len(firstMS) > 0 {
			metric.FirstTokenMS = percentile(firstMS, 95)
		}
		report.Stages[stage] = metric
	}
	return report
}
