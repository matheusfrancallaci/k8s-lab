package tutor

import "sync/atomic"

// Taxa de aprovação do quality gate — a métrica "confiança do conteúdo" do
// docs/game-change.md: quanto do que a IA gera está bom o bastante de primeira.
// Contadores do processo (não por usuário); expostos em /metrics.
var (
	gatePassed atomic.Int64
	gateFailed atomic.Int64
)

// GateStats devolve o total de avaliações do LabQualityGate desde o boot.
func GateStats() (passed, failed int64) {
	return gatePassed.Load(), gateFailed.Load()
}
