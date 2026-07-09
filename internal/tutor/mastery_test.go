package tutor

import (
	"testing"
	"time"
)

func resetProfile(t *testing.T, user string) *Profile {
	t.Helper()
	p := profileFor(user)
	p.mu.Lock()
	p.Skills = map[string]*TopicSkill{}
	p.Review = map[string]*ReviewItem{}
	if p.saveTimer != nil {
		p.saveTimer.Stop()
	}
	p.mu.Unlock()
	t.Cleanup(func() {
		p.mu.Lock()
		if p.saveTimer != nil {
			p.saveTimer.Stop()
		}
		p.mu.Unlock()
	})
	return p
}

func TestMasteryGateFrontierAndLock(t *testing.T) {
	user := "unit-mastery-gate"
	resetProfile(t, user)
	topics := []string{"Workloads", "Services", "Troubleshooting"}

	// Usuário novo: primeiro tópico é a fronteira, o resto travado.
	path := MasteryPath(user, "CKA", topics)
	if path[0].Status != CurrentGap {
		t.Fatalf("primeiro topico deveria ser a fronteira, veio %q", path[0].Status)
	}
	for _, tm := range path[1:] {
		if tm.Status != Locked {
			t.Fatalf("topico %s deveria estar travado antes de dominar o anterior, veio %q", tm.Topic, tm.Status)
		}
	}
	if got := unlockedTopics(path); len(got) != 1 || got[0] != "Workloads" {
		t.Fatalf("so a fronteira deveria estar liberada, veio %v", got)
	}
}

func TestMasteryGateUnlocksAfterMastery(t *testing.T) {
	user := "unit-mastery-unlock"
	resetProfile(t, user)
	topics := []string{"Workloads", "Services"}

	// Três acertos no primeiro tópico: score cruza a barra, sem revisão vencida.
	q := generateQuestions("Workloads", "CKA", 2, 1)[0]
	for i := 0; i < 3; i++ {
		RecordGoal(user, q, true)
	}
	path := MasteryPath(user, "CKA", topics)
	if path[0].Status != Mastered {
		t.Fatalf("Workloads deveria estar dominado apos 3 acertos (score %.2f), veio %q", path[0].Score, path[0].Status)
	}
	if path[1].Status != CurrentGap {
		t.Fatalf("Services deveria virar a nova fronteira, veio %q", path[1].Status)
	}
}

func TestMasteryGateDueReviewBlocksMastery(t *testing.T) {
	user := "unit-mastery-duereview"
	p := resetProfile(t, user)

	// Score alto e amostra suficiente, MAS uma revisão vencida no tópico.
	p.mu.Lock()
	p.Skills["CKA|Workloads"] = &TopicSkill{Cert: "CKA", Topic: "Workloads", Score: 0.9, Attempts: 5}
	p.Review["CKA|Workloads|x"] = &ReviewItem{Cert: "CKA", Topic: "Workloads", Due: time.Now().Add(-24 * time.Hour)}
	p.mu.Unlock()

	path := MasteryPath(user, "CKA", []string{"Workloads", "Services"})
	if path[0].Status == Mastered {
		t.Fatal("revisao vencida deveria impedir o dominio mesmo com score alto")
	}
	if path[0].Status != CurrentGap {
		t.Fatalf("topico com revisao vencida deveria ser a fronteira, veio %q", path[0].Status)
	}
}

func TestRetentionCountsOnRepresentation(t *testing.T) {
	user := "unit-retention"
	p := resetProfile(t, user)

	q := generateQuestions("Workloads", "CKA", 2, 1)[0]

	// Primeira tentativa (sem revisão pendente): NÃO conta retenção.
	RecordGoal(user, q, false)
	p.mu.Lock()
	s := p.Skills["CKA|"+q.Topic]
	if s.RetentionHits != 0 || s.RetentionMisses != 0 {
		p.mu.Unlock()
		t.Fatalf("primeira tentativa nao e reapresentacao: hits=%d misses=%d", s.RetentionHits, s.RetentionMisses)
	}
	// A falha agendou revisão com Due=now — a próxima resposta é reapresentação.
	p.mu.Unlock()

	RecordGoal(user, q, true)
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.RetentionHits != 1 {
		t.Fatalf("acerto em revisao vencida deveria contar retention hit, veio hits=%d misses=%d", s.RetentionHits, s.RetentionMisses)
	}
}

func TestAdviseSurfacesDueReviewsFirst(t *testing.T) {
	user := "unit-advise-review"
	p := resetProfile(t, user)

	// Um tópico fraco (nudge de skill) e outro com revisão vencida.
	p.mu.Lock()
	p.Skills["CKA|Services"] = &TopicSkill{Cert: "CKA", Topic: "Services", Score: 0.3, Attempts: 5}
	p.Review["CKA|Networking|x"] = &ReviewItem{Cert: "CKA", Topic: "Networking", Due: time.Now().Add(-48 * time.Hour), Failures: 1}
	p.mu.Unlock()

	recs := Advise(user)
	if len(recs) < 2 {
		t.Fatalf("esperava recomendacao de revisao e de skill, veio %+v", recs)
	}
	if recs[0].Topic != "Networking" {
		t.Fatalf("revisao vencida deveria vir primeiro, veio %q", recs[0].Topic)
	}
}

func TestAdviseDedupesReviewedTopic(t *testing.T) {
	user := "unit-advise-dedupe"
	p := resetProfile(t, user)

	// Mesmo tópico fraco E com revisão vencida: só uma recomendação (a de revisão).
	p.mu.Lock()
	p.Skills["CKA|Services"] = &TopicSkill{Cert: "CKA", Topic: "Services", Score: 0.2, Attempts: 6}
	p.Review["CKA|Services|x"] = &ReviewItem{Cert: "CKA", Topic: "Services", Due: time.Now().Add(-24 * time.Hour), Failures: 2}
	p.mu.Unlock()

	recs := Advise(user)
	count := 0
	for _, r := range recs {
		if r.Topic == "Services" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("topico com revisao nao deveria receber tambem o nudge de skill, veio %d recomendacoes de Services: %+v", count, recs)
	}
}
