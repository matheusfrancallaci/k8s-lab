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
