package tutor

import (
	"strings"
	"testing"

	"estudo-app/internal/models"
)

func TestMutateCommandDistractors(t *testing.T) {
	cmd := "kubectl run web --image=nginx:1.21 --labels=app=web"
	d := mutateCommandDistractors(cmd)
	if len(d) < 3 {
		t.Fatalf("esperava >=3 distratores, veio %d: %v", len(d), d)
	}
	seen := map[string]bool{cmd: true}
	for _, v := range d {
		if v == cmd {
			t.Errorf("distrator igual ao comando correto: %q", v)
		}
		if seen[v] {
			t.Errorf("distrator duplicado: %q", v)
		}
		seen[v] = true
		if !strings.HasPrefix(v, "kubectl ") {
			t.Errorf("distrator deixou de ser kubectl: %q", v)
		}
	}
}

func TestMutateFlagUsesConfusionSibling(t *testing.T) {
	// --replicas pertence ao conjunto {--replicas,--scale,--instances}: a mutação
	// de flag deve trocar por uma irmã (distrator que o aluno de fato confunde).
	cmd := "kubectl scale deployment web --replicas=3"
	d := mutateCommandDistractors(cmd)
	found := false
	for _, v := range d {
		if strings.Contains(v, "--scale=") || strings.Contains(v, "--instances=") {
			found = true
		}
	}
	if !found {
		t.Errorf("nenhuma troca por flag-irmã do conjunto de confusão em %v", d)
	}
}

func TestCommandChoiceFromTemplateShape(t *testing.T) {
	ok := false
	for _, topic := range []string{"Core Concepts", "Autoscaling", "Workloads", "Services", "RBAC"} {
		for i := 0; i < 12 && !ok; i++ {
			q, built := commandChoiceFromTemplate("CKA", topic, 2)
			if !built {
				continue
			}
			ok = true
			if q.Type != models.MultipleChoice {
				t.Errorf("tipo esperado multiple_choice, veio %q", q.Type)
			}
			if len(q.Options) != 4 {
				t.Errorf("esperava 4 opções, veio %d", len(q.Options))
			}
			if q.Answer < 0 || q.Answer >= len(q.Options) {
				t.Fatalf("answer fora do intervalo: %d", q.Answer)
			}
			if dupOptions(q.Options) {
				t.Errorf("opções duplicadas: %v", q.Options)
			}
			if q.Validation == nil || strings.TrimSpace(q.Validation.Command) == "" {
				t.Errorf("questão de comando sem validador de efeito para verificação executável")
			}
			if q.Readiness == nil || q.Readiness.State != "grounded" || !q.Readiness.Grounded {
				t.Errorf("prontidão inicial deveria ser grounded, veio %+v", q.Readiness)
			}
			if q.Source != models.SourceGenerated {
				t.Errorf("proveniência deveria ser generated, veio %q", q.Source)
			}
		}
	}
	if !ok {
		t.Fatal("nenhum template rendeu questão de comando (esperava ao menos um)")
	}
}

func TestFinalizeMCQCandidateGates(t *testing.T) {
	ground := mcqGround{text: "O kube-scheduler decide em qual no um pod novo sera executado; o kubelet apenas executa.", sourceURL: "https://kubernetes.io/docs/concepts/scheduling-eviction/"}
	good := mcqCandidate{
		Question:    "Qual componente decide em qual no um pod novo sera executado?",
		Options:     []string{"kube-scheduler", "kubelet", "kube-proxy", "etcd"},
		Answer:      0,
		Explanation: "O scheduler faz o bind do pod ao no.",
	}
	q, reason := finalizeMCQCandidate(good, "CKA", "Scheduling", 2, ground)
	if reason != "" {
		t.Fatalf("candidato válido foi reprovado: %s", reason)
	}
	if q.Options[q.Answer] != "kube-scheduler" {
		t.Errorf("shuffle perdeu a resposta correta: idx %d em %v", q.Answer, q.Options)
	}
	if q.Readiness == nil || q.Readiness.ContentDigest == "" {
		t.Errorf("questão publicada sem digest de prontidão")
	}

	// Reprovações esperadas.
	bad := map[string]mcqCandidate{
		"poucas opções":    {Question: good.Question, Options: []string{"a", "b", "c"}, Answer: 0},
		"answer fora":      {Question: good.Question, Options: good.Options, Answer: 9},
		"opções repetidas": {Question: good.Question, Options: []string{"kube-scheduler", "kube-scheduler", "x", "y"}, Answer: 0},
		"sem grounding":    {Question: "Qual a capital da lua colonizada em 2200?", Options: []string{"Selenopolis", "Lunaris", "Armstrong", "Tycho"}, Answer: 0},
		"enunciado curto":  {Question: "curto?", Options: good.Options, Answer: 0},
	}
	for name, c := range bad {
		if _, reason := finalizeMCQCandidate(c, "CKA", "Scheduling", 2, ground); reason == "" {
			t.Errorf("candidato inválido (%s) foi aceito", name)
		}
	}
}

func TestMCQDedupSemantic(t *testing.T) {
	existing := []models.Question{{
		Cert: "CKA", Type: models.MultipleChoice,
		Question: "Qual componente decide em qual no um pod novo sera executado?",
		Options:  []string{"kube-scheduler", "kubelet", "kube-proxy", "etcd"}, Answer: 0,
	}}
	d := newMCQDedup(existing, "CKA")

	// Paráfrase muito próxima (mesmos tokens-chave) → duplicata.
	para := models.Question{
		Cert: "CKA", Type: models.MultipleChoice,
		Question: "Que componente decide em qual no um pod novo sera executado agora?",
		Options:  []string{"kube-scheduler", "kubelet", "etcd", "kube-proxy"}, Answer: 0,
	}
	if !d.isDuplicate(para) {
		t.Error("paráfrase próxima não foi detectada como duplicata")
	}

	// Questão de outro assunto → não é duplicata.
	other := models.Question{
		Cert: "CKA", Type: models.MultipleChoice,
		Question: "Qual recurso expoe um Deployment via porta estatica em todos os nos?",
		Options:  []string{"NodePort", "ClusterIP", "Ingress", "ConfigMap"}, Answer: 0,
	}
	if d.isDuplicate(other) {
		t.Error("questão de outro assunto marcada como duplicata")
	}

	// Dedup respeita a cert: mesma pergunta em outra cert não conta.
	dOther := newMCQDedup(existing, "CKAD")
	if dOther.isDuplicate(para) {
		t.Error("dedup vazou entre certificações diferentes")
	}
}

func TestConfusionHintsForContext(t *testing.T) {
	ctx := "Um Service pode ser ClusterIP ou NodePort; o NodePort abre uma porta em todos os nos."
	hints := confusionHintsForContext(ctx, "Services")
	joined := strings.Join(hints, " ")
	for _, want := range []string{"ClusterIP", "NodePort"} {
		if !strings.Contains(joined, want) {
			t.Errorf("hint esperado %q ausente em %v", want, hints)
		}
	}
	if len(confusionHintsForContext("texto sem termos de confusao conhecidos", "")) != 0 {
		t.Error("contexto sem termos deveria render zero hints")
	}
}

func TestMCQContentDigestStable(t *testing.T) {
	q := models.Question{ID: "x1", Cert: "CKA", Topic: "Scheduling", Question: "q?", Options: []string{"a", "b", "c", "d"}, Answer: 1, Explanation: "e"}
	a := mcqContentDigest(q)
	if a != mcqContentDigest(q) {
		t.Error("digest não é estável para o mesmo conteúdo")
	}
	q.Answer = 2
	if a == mcqContentDigest(q) {
		t.Error("digest não mudou ao alterar a resposta")
	}
}

func TestParseMCQContract(t *testing.T) {
	raw := `{"questions":[
		{"question":"Q valida com tamanho ok?","options":["op um","op dois","op tres","op quatro"],"answer":"1","explanation":"pq"},
		{"question":"outra","options":["so","uma"],"answer":0,"explanation":"x"}
	]}`
	cands, err := parseMCQContract(raw)
	if err != nil {
		t.Fatalf("parse falhou: %v", err)
	}
	if len(cands) != 2 {
		t.Fatalf("esperava 2 candidatos, veio %d", len(cands))
	}
	if cands[0].Answer != 1 {
		t.Errorf("answer string \"1\" não virou 1: %d", cands[0].Answer)
	}
	if _, err := parseMCQContract("{isso nao e json"); err == nil {
		t.Error("JSON inválido deveria retornar erro")
	}
}

func TestMarkMCQVerified(t *testing.T) {
	q := models.Question{ID: "c1", Cert: "CKA", Type: models.MultipleChoice, Options: []string{"a", "b", "c", "d"}, Answer: 0}
	q.Readiness = groundedMCQReadiness(q, "https://kubernetes.io/")
	markMCQVerified(&q, nil)
	if q.Readiness.State != "verified" || !q.Readiness.Executable || q.Readiness.VerifiedAt == "" {
		t.Errorf("verificação bem-sucedida não promoveu para verified: %+v", q.Readiness)
	}
	markMCQVerified(&q, errDistratorAmbiguo)
	if q.Readiness.State != "rejected" || q.Readiness.Failure == "" {
		t.Errorf("falha de verificação não marcou rejected: %+v", q.Readiness)
	}
}

func TestMajorityVote(t *testing.T) {
	cases := []struct {
		votes   []int
		wantVal int
		wantN   int
	}{
		{[]int{0, 0, 1}, 0, 2},
		{[]int{2, 2, 2}, 2, 3},
		{[]int{0, 1, 2}, 0, 1}, // empate: primeiro que atinge o topo
		{[]int{3, 1, 3}, 3, 2},
	}
	for _, c := range cases {
		v, n := majorityVote(c.votes)
		if n != c.wantN || (c.wantN > 1 && v != c.wantVal) {
			t.Errorf("majorityVote(%v)=(%d,%d), quer (%d,%d)", c.votes, v, n, c.wantVal, c.wantN)
		}
	}
}

func TestMarkMCQJudged(t *testing.T) {
	q := models.Question{ID: "j1", Cert: "CKA", Type: models.MultipleChoice, Options: []string{"a", "b", "c", "d"}, Answer: 2}
	q.Readiness = groundedMCQReadiness(q, "https://kubernetes.io/")
	if q.Readiness.State != "grounded" {
		t.Fatalf("estado inicial deveria ser grounded, veio %q", q.Readiness.State)
	}
	markMCQJudged(&q)
	if q.Readiness.State != "judged" {
		t.Errorf("markMCQJudged não promoveu para judged: %q", q.Readiness.State)
	}
}

func TestOptionsBlock(t *testing.T) {
	q := models.Question{Options: []string{"alpha", "beta", "gamma"}}
	got := optionsBlock(q)
	want := "0) alpha\n1) beta\n2) gamma"
	if got != want {
		t.Errorf("optionsBlock=%q, quer %q", got, want)
	}
}

var errDistratorAmbiguo = &mcqTestErr{"distrator satisfez o validador"}

type mcqTestErr struct{ s string }

func (e *mcqTestErr) Error() string { return e.s }
