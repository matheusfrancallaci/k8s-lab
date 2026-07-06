package handlers

import (
	"embed"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"estudo-app/internal/models"
	"estudo-app/internal/repository"
)

var funcMap = template.FuncMap{
	"not":   func(b bool) bool { return !b },
	"join":  func(s []string, sep string) string { return strings.Join(s, sep) },
	"add":   func(a, b int) int { return a + b },
	"mul":   func(a int, b float64) float64 { return float64(a) * b },
	"div":   func(a float64, b int) float64 { return a / float64(b) },
	"slice": func(args ...string) []string { return args },
	"seq": func(n int) []int {
		s := make([]int, n)
		for i := range s {
			s[i] = i
		}
		return s
	},
}

type QuizHandler struct {
	repo  *repository.QuestionRepository
	store *repository.SessionStore
	fs    embed.FS
}

func NewQuizHandler(repo *repository.QuestionRepository, store *repository.SessionStore, fs embed.FS) *QuizHandler {
	return &QuizHandler{repo: repo, store: store, fs: fs}
}

func (h *QuizHandler) render(w http.ResponseWriter, page string, data any) {
	RenderPage(w, h.fs, page, data)
}

func (h *QuizHandler) Home(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Certs":     h.repo.Certs(),
		"Limits":    []int{5, 10, 20, 30, 0},
		"Total":     h.repo.Count(),
		"LabCount":  len(h.repo.FilterLabs(nil, "", nil)),
		"NavActive": "home",
	}
	h.render(w, "home.html", data)
}

func (h *QuizHandler) Practice(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Certs":      h.repo.Certs(),
		"Limits":     []int{5, 10, 20, 30, 0},
		"NavActive":  "quiz",
		"ExtraCerts": extraCerts(h.repo.Certs()),
	}
	h.render(w, "practice.html", data)
}

func (h *QuizHandler) Start(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	certs := r.Form["cert"]
	difficulty := r.FormValue("difficulty")
	limitStr := r.FormValue("limit")

	limit := 10
	if n, err := strconv.Atoi(limitStr); err == nil && n > 0 {
		limit = n
	}

	questions := h.repo.Random(certs, difficulty, limit)
	if len(questions) == 0 {
		http.Redirect(w, r, "/?error=no_questions", http.StatusSeeOther)
		return
	}

	certLabel := strings.Join(certs, " · ")
	if certLabel == "" {
		certLabel = "Todas"
	}

	id, _ := h.store.Create(questions, certLabel, difficulty)
	http.Redirect(w, r, "/quiz/"+id, http.StatusSeeOther)
}

func (h *QuizHandler) Question(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := h.store.Get(id)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	if sess.Current >= len(sess.Questions) {
		http.Redirect(w, r, "/quiz/"+id+"/result", http.StatusSeeOther)
		return
	}

	q := sess.Questions[sess.Current]
	data := map[string]any{
		"SessionID":  id,
		"Question":   q,
		"Number":     sess.Current + 1,
		"Total":      len(sess.Questions),
		"Current":    sess.Current,
		"Progress":   float64(sess.Current) / float64(len(sess.Questions)) * 100,
		"Cert":       sess.Cert,
		"Difficulty": sess.Difficulty,
		"Score":      sess.Score,
		"Answers":    sess.Answers,
	}
	h.render(w, "question.html", data)
}

func (h *QuizHandler) Answer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	id := r.PathValue("id")
	answerStr := r.FormValue("answer")
	answer, err := strconv.Atoi(answerStr)
	if err != nil {
		http.Redirect(w, r, "/quiz/"+id, http.StatusSeeOther)
		return
	}

	sess, ok := h.store.Get(id)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	currentQ := sess.Questions[sess.Current]
	correct := answer == currentQ.Answer
	questionNumber := sess.Current + 1
	total := len(sess.Questions)

	h.store.Answer(id, answer)
	updatedSess, _ := h.store.Get(id)

	data := map[string]any{
		"SessionID":  id,
		"Question":   currentQ,
		"UserAnswer": answer,
		"Correct":    correct,
		"Number":     questionNumber,
		"Total":      total,
		"IsLast":     updatedSess.Current >= total,
		"Cert":       sess.Cert,
		"Difficulty": sess.Difficulty,
		"Score":      updatedSess.Score,
		"Answers":    updatedSess.Answers,
		"Current":    updatedSess.Current,
	}
	h.render(w, "answer.html", data)
}

func (h *QuizHandler) Result(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, ok := h.store.Get(id)
	if !ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	var results []models.QuizResult
	for i, q := range sess.Questions {
		userAnswer := sess.Answers[i]
		results = append(results, models.QuizResult{
			Question:   q,
			UserAnswer: userAnswer,
			Correct:    userAnswer == q.Answer,
		})
	}

	percentage := 0
	if len(sess.Questions) > 0 {
		percentage = sess.Score * 100 / len(sess.Questions)
	}

	data := map[string]any{
		"Score":      sess.Score,
		"Total":      len(sess.Questions),
		"Percentage": percentage,
		"Results":    results,
		"Cert":       sess.Cert,
		"Difficulty": sess.Difficulty,
		"Passed":     percentage >= 66,
	}
	h.render(w, "result.html", data)
	h.store.Delete(id)
}

// extraCerts devolve certificações fora do conjunto embutido (criadas pela
// ingestão do tutor) — as UIs as exibem dinamicamente.
func extraCerts(all []string) []string {
	builtin := map[string]bool{"CKA": true, "CKAD": true, "CKS": true, "ArgoCD": true}
	var out []string
	for _, c := range all {
		if !builtin[c] {
			out = append(out, c)
		}
	}
	return out
}
