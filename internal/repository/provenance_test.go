package repository

import (
	"testing"

	"estudo-app/internal/models"
)

func TestAddMarksUnsourcedAsGenerated(t *testing.T) {
	// Invariante: o repo nasce com o embed curado; tudo que entra em runtime
	// veio de geração — Add carimba para nenhum gerador esquecer o selo.
	r := &QuestionRepository{}
	r.Add([]models.Question{
		{ID: "gen-1", Cert: "CKA", Type: models.Lab, Question: "Crie um Deployment web."},
		{ID: "cur-1", Cert: "CKA", Source: models.SourceCurated, Question: "Questao migrada com selo."},
	})
	q, ok := r.GetByID("gen-1")
	if !ok || q.Source != models.SourceGenerated {
		t.Fatalf("questao sem selo adicionada em runtime deveria virar generated, veio %q", q.Source)
	}
	q, _ = r.GetByID("cur-1")
	if q.Source != models.SourceCurated {
		t.Fatalf("selo explicito nao pode ser sobrescrito, veio %q", q.Source)
	}
}
