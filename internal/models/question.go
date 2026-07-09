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
}

type Question struct {
	ID            string       `yaml:"id"`
	Cert          Cert         `yaml:"cert"`
	Topic         string       `yaml:"topic"`
	Difficulty    Difficulty   `yaml:"difficulty"`
	Type          QuestionType `yaml:"type"`
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
