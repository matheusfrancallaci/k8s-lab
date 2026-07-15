package models

type Difficulty string
type QuestionType string
type Cert string

const (
	Easy Difficulty = "easy"
	Mid  Difficulty = "mid"
	Hard Difficulty = "hard"

	MultipleChoice QuestionType = "multiple_choice"
	Lab            QuestionType = "lab"

	CKA    Cert = "CKA"
	CKAD   Cert = "CKAD"
	CKS    Cert = "CKS"
	ArgoCD Cert = "ArgoCD"
)

type Validation struct {
	Command          string `yaml:"command"`
	ExpectedContains string `yaml:"expected_contains"`
	ExpectedOutput   string `yaml:"expected_output"`
}

type SetupStep struct {
	Description string `yaml:"description"`
	Command     string `yaml:"command"`
}

type Goal struct {
	Description string      `yaml:"description"`
	Hint        string      `yaml:"hint"`
	Validation  *Validation `yaml:"validation"`
}

type LabDependency struct {
	Name          string `yaml:"name"`
	Kind          string `yaml:"kind"`
	InstallAction string `yaml:"install_action"`
	StatusCommand string `yaml:"status_command"`
	Required      bool   `yaml:"required"`
}

type LabSource struct {
	Title   string `yaml:"title"`
	URL     string `yaml:"url"`
	Section string `yaml:"section"`
}

type LabQuality struct {
	Score    int      `yaml:"score"`
	Checks   []string `yaml:"checks"`
	Warnings []string `yaml:"warnings"`
}

// LabReadiness is the publication contract for a hands-on lab. Generated labs
// are never represented as implicitly ready: the compiler records what was
// checked and the runtime can promote them after an executable verification.
type LabReadiness struct {
	State           string   `yaml:"state" json:"state"` // compiled | verifying | ready | degraded | rejected
	Version         string   `yaml:"version" json:"version"`
	ContentDigest   string   `yaml:"content_digest" json:"content_digest"`
	CheckedAt       string   `yaml:"checked_at" json:"checked_at"`
	VerifiedAt      string   `yaml:"verified_at,omitempty" json:"verified_at,omitempty"`
	Executable      bool     `yaml:"executable" json:"executable"`
	SetupVerified   bool     `yaml:"setup_verified" json:"setup_verified"`
	SolveVerified   bool     `yaml:"solve_verified" json:"solve_verified"`
	ChecksVerified  bool     `yaml:"checks_verified" json:"checks_verified"`
	CleanupVerified bool     `yaml:"cleanup_verified" json:"cleanup_verified"`
	ImageDigests    []string `yaml:"image_digests,omitempty" json:"image_digests,omitempty"`
	Warnings        []string `yaml:"warnings,omitempty" json:"warnings,omitempty"`
	Failure         string   `yaml:"failure,omitempty" json:"failure,omitempty"`
}

// QuestionReadiness é o contrato de publicação de uma QUESTÃO de múltipla
// escolha gerada — o análogo de LabReadiness para labs. Uma questão gerada
// nunca é implicitamente confiável: o backend registra o que foi provado.
// Estados:
//
//	grounded — enunciado e resposta ancorados em evidência oficial (heurística
//	           determinística); os distratores ainda não foram provados errados.
//	verified — questão de comando cujos distratores foram EXECUTADOS num cluster
//	           efêmero e comprovadamente NÃO satisfazem o validador do efeito.
//	rejected — reprovada (não deve chegar ao aluno).
type QuestionReadiness struct {
	State         string   `yaml:"state" json:"state"`
	Version       string   `yaml:"version" json:"version"`
	ContentDigest string   `yaml:"content_digest" json:"content_digest"`
	CheckedAt     string   `yaml:"checked_at" json:"checked_at"`
	Grounded      bool     `yaml:"grounded" json:"grounded"`
	Executable    bool     `yaml:"executable" json:"executable"`
	VerifiedAt    string   `yaml:"verified_at,omitempty" json:"verified_at,omitempty"`
	SourceURL     string   `yaml:"source_url,omitempty" json:"source_url,omitempty"`
	Warnings      []string `yaml:"warnings,omitempty" json:"warnings,omitempty"`
	Failure       string   `yaml:"failure,omitempty" json:"failure,omitempty"`
}

type LabPlan struct {
	ExactTopic      string      `yaml:"exact_topic"`
	Cert            string      `yaml:"cert"`
	CheckedAt       string      `yaml:"checked_at"`
	SourceVersion   string      `yaml:"source_version"`
	Namespace       string      `yaml:"namespace"`
	Resources       []string    `yaml:"resources"`
	Validations     []string    `yaml:"validations"`
	Risks           []string    `yaml:"risks"`
	PreflightChecks []string    `yaml:"preflight_checks"`
	Sources         []LabSource `yaml:"sources"`
}

type LabEvidence struct {
	Domain       string      `yaml:"domain"`
	Weight       int         `yaml:"weight"`
	Confidence   int         `yaml:"confidence"`
	MatchedTerms []string    `yaml:"matched_terms"`
	Sources      []LabSource `yaml:"sources"`
}

type LabChunk struct {
	ID        string `yaml:"id"`
	Domain    string `yaml:"domain"`
	Title     string `yaml:"title"`
	URL       string `yaml:"url"`
	Excerpt   string `yaml:"excerpt"`
	Relevance int    `yaml:"relevance"`
}

type LabSpec struct {
	Objective        string          `yaml:"objective"`
	Scenario         string          `yaml:"scenario"`
	Namespace        string          `yaml:"namespace,omitempty"`
	ValidationMode   string          `yaml:"validation_mode,omitempty"`
	Skills           []string        `yaml:"skills"`
	EstimatedMinutes int             `yaml:"estimated_minutes"`
	Dependencies     []LabDependency `yaml:"dependencies"`
	Sources          []LabSource     `yaml:"sources"`
	Evidence         []LabEvidence   `yaml:"evidence"`
	EvidenceScore    int             `yaml:"evidence_score"`
	Chunks           []LabChunk      `yaml:"chunks"`
	Plan             []string        `yaml:"plan"`
	SuccessCriteria  []string        `yaml:"success_criteria"`
	Safety           []string        `yaml:"safety"`
	LabPlan          *LabPlan        `yaml:"lab_plan,omitempty"`
	Quality          LabQuality      `yaml:"quality"`
	Readiness        LabReadiness    `yaml:"readiness" json:"readiness"`
}

// Proveniência do conteúdo: quem responde pela qualidade desta questão.
// Curado = escrito/verificado por humano (banco embutido); gerado = produzido
// por IA local em runtime. O aluno vê a diferença (selo no lab) — confiança
// de conteúdo é o maior risco do produto e não pode ser implícita.
const (
	SourceCurated   = "curated"
	SourceGenerated = "generated"
)

type Question struct {
	ID            string       `yaml:"id"`
	Cert          Cert         `yaml:"cert"`
	Topic         string       `yaml:"topic"`
	Difficulty    Difficulty   `yaml:"difficulty"`
	Type          QuestionType `yaml:"type"`
	Source        string       `yaml:"source,omitempty"`
	Question      string       `yaml:"question"`
	Options       []string     `yaml:"options"`
	Answer        int          `yaml:"answer"`
	Explanation   string       `yaml:"explanation"`
	DocURL        string       `yaml:"doc_url"`
	DocSection    string       `yaml:"doc_section"`
	Hint          string       `yaml:"hint"`
	AnswerCommand string       `yaml:"answer_command"`
	Validation    *Validation  `yaml:"validation"`
	Goals         []Goal       `yaml:"goals"`
	Setup         []SetupStep  `yaml:"setup"`
	Teardown      []string     `yaml:"teardown"`
	LabSpec       *LabSpec     `yaml:"lab_spec,omitempty"`
	// Readiness carrega a proveniência de uma questão de múltipla escolha gerada
	// (labs guardam a prontidão em LabSpec.Readiness; MC não tem LabSpec).
	Readiness *QuestionReadiness `yaml:"readiness,omitempty" json:"readiness,omitempty"`
}

type QuestionFile struct {
	Questions []Question `yaml:"questions"`
}

type QuizSession struct {
	Questions  []Question
	Current    int
	Answers    []int
	Score      int
	Cert       string
	Difficulty string
}

type QuizResult struct {
	Question   Question
	UserAnswer int
	Correct    bool
}
