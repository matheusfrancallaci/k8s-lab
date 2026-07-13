package tutor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"estudo-app/internal/models"
)

const labContractVersion = "2"

type LabCatalogEntry struct {
	ID        string              `json:"id"`
	Cert      string              `json:"cert"`
	Topic     string              `json:"topic"`
	UpdatedAt string              `json:"updated_at"`
	Readiness models.LabReadiness `json:"readiness"`
}

var labCatalogMu sync.Mutex

func labContentDigest(q models.Question) string {
	var validationContracts []string
	for _, validation := range appendValidationObjects(q) {
		validationContracts = append(validationContracts, strings.Join([]string{validation.Command, validation.ExpectedContains, validation.ExpectedOutput}, "\x1f"))
	}
	payload := strings.Join([]string{
		q.ID, string(q.Cert), q.Topic, q.Question, q.AnswerCommand,
		strings.Join(setupCommands(q), "\n"), strings.Join(labValidationCommands(q), "\n"),
		strings.Join(validationContracts, "\n"), strings.Join(q.Teardown, "\n"),
	}, "\x00")
	sum := sha256.Sum256([]byte(payload))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func compiledLabReadiness(q models.Question) models.LabReadiness {
	state := "compiled"
	warnings := []string{"verificacao executavel ainda nao concluida"}
	if q.Source == models.SourceCurated {
		state = "ready"
		warnings = nil
	}
	return models.LabReadiness{
		State: state, Version: labContractVersion, ContentDigest: labContentDigest(q),
		CheckedAt: time.Now().UTC().Format(time.RFC3339), Warnings: warnings,
	}
}

func ensureLabReadiness(q models.Question, spec *models.LabSpec) {
	if spec.Readiness.ContentDigest != labContentDigest(q) || spec.Readiness.Version != labContractVersion {
		spec.Readiness = compiledLabReadiness(q)
	}
}

func markLabVerified(q *models.Question, executable bool, err error) {
	if q == nil || q.LabSpec == nil {
		return
	}
	r := &q.LabSpec.Readiness
	r.Executable = executable
	r.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	if err != nil {
		r.State = "rejected"
		r.Failure = err.Error()
		r.Warnings = []string{"verificacao executavel falhou; conteudo mantido disponivel"}
		return
	}
	r.State = "ready"
	r.VerifiedAt = r.CheckedAt
	r.SetupVerified = executable
	r.SolveVerified = executable
	r.ChecksVerified = executable
	r.CleanupVerified = executable
	r.Failure = ""
	r.Warnings = nil
}

func markLabDegraded(q *models.Question, warning string) {
	if q == nil || q.LabSpec == nil {
		return
	}
	warning = strings.TrimSpace(warning)
	r := &q.LabSpec.Readiness
	r.State = "degraded"
	r.CheckedAt = time.Now().UTC().Format(time.RFC3339)
	r.Failure = warning
	if warning != "" {
		for _, existing := range r.Warnings {
			if existing == warning {
				return
			}
		}
		r.Warnings = append(r.Warnings, warning)
	}
}

func labCatalogPath() string { return filepath.Join("data", "labs", "catalog.json") }

func RecordLabCatalog(qs []models.Question) error {
	labCatalogMu.Lock()
	defer labCatalogMu.Unlock()
	entries := map[string]LabCatalogEntry{}
	if b, err := os.ReadFile(labCatalogPath()); err == nil {
		_ = json.Unmarshal(b, &entries)
	}
	for _, q := range qs {
		if q.Type != models.Lab || q.LabSpec == nil {
			continue
		}
		entries[q.ID] = LabCatalogEntry{ID: q.ID, Cert: string(q.Cert), Topic: q.Topic, UpdatedAt: time.Now().UTC().Format(time.RFC3339), Readiness: q.LabSpec.Readiness}
	}
	if err := os.MkdirAll(filepath.Dir(labCatalogPath()), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(labCatalogPath(), b, 0o644)
}

func LabCatalog() []LabCatalogEntry {
	labCatalogMu.Lock()
	defer labCatalogMu.Unlock()
	entries := map[string]LabCatalogEntry{}
	b, err := os.ReadFile(labCatalogPath())
	if err != nil || json.Unmarshal(b, &entries) != nil {
		return nil
	}
	out := make([]LabCatalogEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func catalogReadinessFor(q models.Question) (models.LabReadiness, bool) {
	if q.LabSpec == nil {
		return models.LabReadiness{}, false
	}
	labCatalogMu.Lock()
	defer labCatalogMu.Unlock()
	entries := map[string]LabCatalogEntry{}
	b, err := os.ReadFile(labCatalogPath())
	if err != nil || json.Unmarshal(b, &entries) != nil {
		return models.LabReadiness{}, false
	}
	entry, ok := entries[q.ID]
	if !ok || entry.Readiness.ContentDigest != q.LabSpec.Readiness.ContentDigest || entry.Readiness.Version != labContractVersion {
		return models.LabReadiness{}, false
	}
	return entry.Readiness, true
}

// PrepareLabForDelivery is the single preparation boundary used by the UI.
// Quality warnings never withhold content — mas lab gerado cuja verificação
// EXECUTÁVEL provou o gabarito quebrado (rejected) não chega ao aluno: servir
// exercício com solução que não aplica é pior que pedir outra geração.
// Executable verification belongs to generation, never to a GET or setup
// request. Potentially dangerous setup commands remain guarded at execution.
func PrepareLabForDelivery(q models.Question) models.Question {
	q = FinalizeLab(q, "")
	if err := LabQualityGate(q); err != nil {
		markLabDegraded(&q, "quality gate: "+err.Error())
		_ = RecordLabCatalog([]models.Question{q})
		return q
	}
	if q.LabSpec == nil || q.Source != models.SourceGenerated || q.LabSpec.Readiness.State == "ready" {
		return q
	}
	if readiness, ok := catalogReadinessFor(q); ok {
		q.LabSpec.Readiness = readiness
	}
	return q
}

// DeliveryBlockReason devolve o motivo para NÃO entregar o lab ao aluno, ou
// vazio. Só a prova executável reprova ("rejected" com Executable=true) —
// pendente/degradado continua disponível com o aviso de procedência.
func DeliveryBlockReason(q models.Question) string {
	if q.Source != models.SourceGenerated || q.LabSpec == nil {
		return ""
	}
	r := q.LabSpec.Readiness
	if r.State == "rejected" && r.Executable {
		reason := strings.TrimSpace(r.Failure)
		if reason == "" {
			reason = "verificacao executavel reprovou o gabarito"
		}
		return reason
	}
	return ""
}
