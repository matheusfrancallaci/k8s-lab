package handlers

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"estudo-app/internal/repository"
	"estudo-app/internal/tutor"
)

// TutorHandler expõe o tutor adaptativo local: status/recomendações,
// geração de labs personalizados e ingestão de documentação.
type TutorHandler struct {
	repo        *repository.QuestionRepository
	labSessions *repository.LabSessionStore
	templates   embed.FS
}

func NewTutorHandler(repo *repository.QuestionRepository, labSessions *repository.LabSessionStore, fs embed.FS) *TutorHandler {
	return &TutorHandler{repo: repo, labSessions: labSessions, templates: fs}
}

// Page renderiza a página do tutor (dashboard + geração + ingestão).
func (h *TutorHandler) Page(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.New("base.html").Funcs(funcMap).ParseFS(h.templates,
		"web/templates/base.html",
		"web/templates/nav.html",
		"web/templates/tutor.html",
	)
	if err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	topicsJSON, _ := json.Marshal(tutor.Topics())
	tmpl.ExecuteTemplate(w, "base.html", map[string]any{ //nolint:errcheck
		"GenTopicsJSON": template.JS(topicsJSON),
		"NavActive":     "tutor",
	})
}

// Status devolve recomendações ativas + estatísticas de habilidade.
func (h *TutorHandler) Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	llmOK, llmModel := tutor.LLMStatus()
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"recommendations": tutor.Advise(userID(r)),
		"stats":           tutor.Stats(userID(r)),
		"gen_topics":      tutor.Topics(),
		"certs":           tutor.AllCerts(),
		"llm":             map[string]any{"available": llmOK, "model": llmModel},
	})
}

// Explain usa o LLM local para explicar por que um goal falhou (tutoria real).
func (h *TutorHandler) Explain(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		QuestionID string `json:"question_id"`
		Goal       int    `json:"goal"`
		Output     string `json:"output"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": "payload inválido"}) //nolint:errcheck
		return
	}
	q, ok := h.repo.GetByID(body.QuestionID)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"error": "questão não encontrada"}) //nolint:errcheck
		return
	}
	goalDesc, valCmd := "", ""
	if body.Goal >= 0 && body.Goal < len(q.Goals) {
		goalDesc = q.Goals[body.Goal].Description
		if q.Goals[body.Goal].Validation != nil {
			valCmd = q.Goals[body.Goal].Validation.Command
		}
	}
	text, err := tutor.LLMExplainFailure(q.Question, goalDesc, valCmd, body.Output)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": "IA local indisponível — instale o Ollama para ter explicações em tempo real"}) //nolint:errcheck
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"explanation": text}) //nolint:errcheck
}

// Event registra eventos vindos do front (hint/solution abertos, dispensas).
func (h *TutorHandler) Event(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Type       string `json:"type"` // hint_view | solution_view | dismiss
		QuestionID string `json:"question_id"`
		Cert       string `json:"cert"`
		Topic      string `json:"topic"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"ok": false}) //nolint:errcheck
		return
	}
	switch body.Type {
	case "hint_view":
		if q, ok := h.repo.GetByID(body.QuestionID); ok {
			tutor.RecordHint(userID(r), q)
		}
	case "solution_view":
		if q, ok := h.repo.GetByID(body.QuestionID); ok {
			tutor.RecordSolution(userID(r), q)
		}
	case "dismiss":
		tutor.MarkAdvised(userID(r), body.Cert, body.Topic)
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
}

// Generate cria labs personalizados e já devolve uma sessão pronta.
func (h *TutorHandler) Generate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Cert  string `json:"cert"`
		Topic string `json:"topic"`
		Level int    `json:"level"`
		Count int    `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": "payload inválido"}) //nolint:errcheck
		return
	}

	qs, err := tutor.Generate(body.Topic, body.Cert, body.Level, body.Count)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()}) //nolint:errcheck
		return
	}
	h.repo.Add(qs)
	tutor.MarkAdvised(userID(r), body.Cert, body.Topic) // aceitou a sugestão → cooldown

	ids := make([]string, len(qs))
	for i, q := range qs {
		ids[i] = q.ID
	}
	sess := h.labSessions.Create(ids)
	// Pré-aquece as imagens usadas pelos labs gerados
	PrewarmLabImages(qs)

	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"id":    sess.ID,
		"first": ids[0],
		"total": len(ids),
	})
}

// Ingest analisa documentação colada e gera questões/labs dela.
func (h *TutorHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Text     string `json:"text"`
		Cert     string `json:"cert"`
		Topic    string `json:"topic"`
		Level    int    `json:"level"`
		WantLabs int    `json:"want_labs"`
		WantQuiz int    `json:"want_quiz"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Text) < 30 {
		json.NewEncoder(w).Encode(map[string]any{"error": "cole um trecho de documentação com pelo menos algumas linhas"}) //nolint:errcheck
		return
	}
	if body.WantLabs == 0 && body.WantQuiz == 0 {
		body.WantLabs, body.WantQuiz = 3, 3
	}

	if body.Cert != "" {
		tutor.RegisterCert(body.Cert)
	}
	qs, rep, err := tutor.Ingest(body.Text, body.Cert, body.Topic, body.Level, body.WantLabs, body.WantQuiz)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "report": rep}) //nolint:errcheck
		return
	}
	h.repo.Add(qs)

	// Sessão só com os LABS gerados (quiz fica disponível no /practice)
	var labIDs []string
	for _, q := range qs {
		if string(q.Type) == "lab" {
			labIDs = append(labIDs, q.ID)
		}
	}
	resp := map[string]any{"report": rep}
	if len(labIDs) > 0 {
		sess := h.labSessions.Create(labIDs)
		resp["session"] = map[string]any{"id": sess.ID, "first": labIDs[0], "total": len(labIDs)}
		PrewarmLabImages(qs)
	}
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

// ChatHandler — interface conversacional do tutor.
func (h *TutorHandler) Chat(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Message string `json:"message"`
		Cert    string `json:"cert"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		json.NewEncoder(w).Encode(map[string]any{"reply": "me manda uma mensagem :)"}) //nolint:errcheck
		return
	}

	res := tutor.Chat(body.Message, body.Cert, func(ids []string) (string, string, int) {
		sess := h.labSessions.Create(ids)
		return sess.ID, ids[0], len(ids)
	})
	if len(res.Questions) > 0 {
		h.repo.Add(res.Questions)
		PrewarmLabImages(res.Questions)
	}
	json.NewEncoder(w).Encode(map[string]any{"reply": res.Reply, "action": res.Action}) //nolint:errcheck
}

// ChatStream retorna a resposta do tutor em streaming simples para a UI.
func (h *TutorHandler) ChatStream(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	var body struct {
		Message string `json:"message"`
		Cert    string `json:"cert"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		fmt.Fprint(w, "me manda uma mensagem :)")
		return
	}

	res := tutor.Chat(body.Message, body.Cert, func(ids []string) (string, string, int) {
		sess := h.labSessions.Create(ids)
		return sess.ID, ids[0], len(ids)
	})
	if len(res.Questions) > 0 {
		h.repo.Add(res.Questions)
		PrewarmLabImages(res.Questions)
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		fmt.Fprint(w, res.Reply)
		return
	}
	if llmOK, _ := tutor.LLMStatus(); llmOK {
		err := tutor.StreamLLMReply(body.Message, func(chunk string) {
			if chunk == "" {
				return
			}
			fmt.Fprint(w, chunk)
			flusher.Flush()
		})
		if err != nil {
			fmt.Fprint(w, res.Reply)
		}
		return
	}
	fmt.Fprint(w, res.Reply)
}
