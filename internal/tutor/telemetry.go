package tutor

import (
	"sort"
	"sync"
	"time"
)

type LatencyMetric struct {
	Count        int `json:"count"`
	Failures     int `json:"failures"`
	AvgMS        int `json:"avg_ms"`
	P95MS        int `json:"p95_ms"`
	FirstTokenMS int `json:"first_token_ms,omitempty"`
}

type TutorTelemetryReport struct {
	Stages map[string]LatencyMetric `json:"stages"`
}

type latencySample struct {
	duration   time.Duration
	failed     bool
	firstToken time.Duration
}

var tutorTelemetry = struct {
	sync.Mutex
	stages map[string][]latencySample
}{stages: map[string][]latencySample{}}

func recordTutorLatency(stage string, duration, firstToken time.Duration, failed bool) {
	tutorTelemetry.Lock()
	defer tutorTelemetry.Unlock()
	samples := append(tutorTelemetry.stages[stage], latencySample{duration: duration, firstToken: firstToken, failed: failed})
	if len(samples) > 256 {
		samples = samples[len(samples)-256:]
	}
	tutorTelemetry.stages[stage] = samples
}

func TutorTelemetry() TutorTelemetryReport {
	tutorTelemetry.Lock()
	defer tutorTelemetry.Unlock()
	report := TutorTelemetryReport{Stages: map[string]LatencyMetric{}}
	for stage, samples := range tutorTelemetry.stages {
		if len(samples) == 0 {
			continue
		}
		ms := make([]int, 0, len(samples))
		total, failures, first := 0, 0, 0
		for _, s := range samples {
			v := int(s.duration.Milliseconds())
			ms = append(ms, v)
			total += v
			if s.failed {
				failures++
			}
			if s.firstToken > 0 {
				first += int(s.firstToken.Milliseconds())
			}
		}
		sort.Ints(ms)
		report.Stages[stage] = LatencyMetric{Count: len(samples), Failures: failures, AvgMS: total / len(samples), P95MS: ms[(len(ms)*95+99)/100-1], FirstTokenMS: first / len(samples)}
	}
	return report
}
