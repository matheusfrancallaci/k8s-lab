package tutor

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"estudo-app/internal/models"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestDocumentTopicsExposeExactAffinitySection(t *testing.T) {
	raw := `<html><main><h1 id="assigning-pods">Assigning Pods to Nodes</h1><h2 id="node-affinity">Node affinity</h2><p>x</p><h2 id="inter-pod-affinity-and-anti-affinity">Inter-pod affinity and anti-affinity</h2><p>y</p></main></html>`
	topics := documentTopicsFromHTML("https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/", raw)
	if len(topics) != 3 {
		t.Fatalf("esperava headings reais, obtive %+v", topics)
	}
	got := topics[2]
	if got.Topic != "Pod Affinity and Anti-Affinity" || !got.Available || !got.Researchable {
		t.Fatalf("secao de affinity deve habilitar geracao exata: %+v", got)
	}
	if !strings.HasSuffix(got.Source, "#inter-pod-affinity-and-anti-affinity") {
		t.Fatalf("fonte deve apontar para a secao exata: %s", got.Source)
	}
}

func TestUnknownDocumentTopicRemainsResearchable(t *testing.T) {
	topics := documentTopicsFromHTML("https://kubernetes.io/docs/example/", `<main><h2 id="new-api">Frobnicator API internals</h2></main>`)
	if len(topics) != 1 || topics[0].Available || !topics[0].Researchable {
		t.Fatalf("topico novo deve permitir pesquisa sem fingir template pronto: %+v", topics)
	}
}

func TestAnalyzeAndGenerateFromExactTrustedDocumentSection(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("K8S_LAB_VERIFY_GENERATED", "0")
	page := `<html><main><h1 id="assigning-pods">Assigning Pods to Nodes</h1><h2 id="node-affinity">Node affinity</h2><p>node section</p><h2 id="inter-pod-affinity-and-anti-affinity">Inter-pod affinity and anti-affinity</h2><p>pod affinity section</p></main></html>`
	previousClient := sharedLLMHTTPClient
	sharedLLMHTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(page))}, nil
	})}
	t.Cleanup(func() { sharedLLMHTTPClient = previousClient })

	source := "https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/?test=exact-section"
	topics, verified, err := AnalyzeDocumentTopics(source)
	if err != nil || len(verified) != 1 || len(topics) != 3 {
		t.Fatalf("analise da pagina oficial falhou: topics=%+v sources=%v err=%v", topics, verified, err)
	}
	qs, report, err := GenerateDocumentLabs(source, "CKA", "Inter-pod affinity and anti-affinity", 2, 1)
	if err != nil || len(qs) != 1 {
		t.Fatalf("geracao exata pela secao falhou: labs=%+v report=%+v err=%v", qs, report, err)
	}
	if qs[0].Topic != "Pod Affinity and Anti-Affinity" || !strings.Contains(qs[0].DocURL, "#inter-pod-affinity-and-anti-affinity") {
		t.Fatalf("lab nao preservou topico/fonte exatos: %+v", qs[0])
	}
}

func TestStudentPermissionGateBlocksClusterWritesAndAllowsPodInspection(t *testing.T) {
	base := models.Question{Type: models.Lab, Cert: models.CKA, Topic: "Troubleshooting", AnswerCommand: "kubectl get pods -A"}
	if err := StudentPermissionGate(base); err != nil {
		t.Fatalf("leitura de pods em todos os namespaces deve funcionar: %v", err)
	}
	base.AnswerCommand = "kubectl patch node worker-1 -p '{}'"
	if err := StudentPermissionGate(base); err == nil {
		t.Fatal("escrita em node precisa ser bloqueada antes da sessao")
	}
	base.AnswerCommand = "kubectl apply -f - <<'EOF'\napiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: demos.example.io\nEOF"
	if err := StudentPermissionGate(base); err == nil {
		t.Fatal("CRD cluster-scoped precisa ser bloqueado antes da sessao")
	}
}

func TestAdvertisedCurriculumLabsFitStudentPermissionEnvelope(t *testing.T) {
	for cert, domains := range curriculumCompetencies {
		for domain, choices := range domains {
			for _, choice := range choices {
				if !choice.Available {
					continue
				}
				qs := generateQuestions(choice.Topic, cert, 2, 1)
				if len(qs) != 1 {
					t.Fatalf("%s/%s/%s nao gerou lab", cert, domain, choice.Topic)
				}
				q := FinalizeLab(qs[0], choice.Label)
				q.Source = models.SourceGenerated
				if err := StudentPermissionGate(q); err != nil {
					t.Errorf("%s anuncia %q como disponivel, mas o aluno teria erro de permissao: %v", cert, choice.Label, err)
				}
				if err := LabQualityGate(q); err != nil {
					t.Errorf("%s anuncia %q como disponivel, mas o quality gate falhou: %v", cert, choice.Label, err)
				}
			}
		}
	}
}
