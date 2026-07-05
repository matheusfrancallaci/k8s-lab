package handlers

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"math/rand"
	"net/http"
	"strconv"
	"strings"

	"estudo-app/internal/models"
	"estudo-app/internal/repository"
	"estudo-app/internal/tutor"
)

type LabHandler struct {
	repo        *repository.QuestionRepository
	store       *repository.SessionStore
	labSessions *repository.LabSessionStore
	fs          embed.FS
}

func NewLabHandler(repo *repository.QuestionRepository, store *repository.SessionStore, labSessions *repository.LabSessionStore, fs embed.FS) *LabHandler {
	return &LabHandler{repo: repo, store: store, labSessions: labSessions, fs: fs}
}

func (h *LabHandler) render(w http.ResponseWriter, page string, data any) {
	tmpl, err := template.New("base.html").Funcs(funcMap).ParseFS(
		h.fs,
		"web/templates/base.html",
		"web/templates/nav.html",
		"web/templates/"+page,
	)
	if err != nil {
		http.Error(w, "erro template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	tmpl.ExecuteTemplate(w, "base.html", data)
}

func (h *LabHandler) List(w http.ResponseWriter, r *http.Request) {
	certs := r.URL.Query()["cert"]
	difficulty := r.URL.Query().Get("difficulty")

	labs := h.repo.FilterLabs(certs, difficulty, nil)

	// Aggregate counts only — we intentionally do NOT expose the individual
	// questions here. Labs are drawn randomly when a session is created.
	counts := map[string]int{}
	for _, l := range labs {
		counts[string(l.Cert)]++
	}

	// Tópicos disponíveis (nome+cert+contagem) para o seletor de estudo dirigido.
	topicsJSON, _ := json.Marshal(h.repo.LabTopics())

	data := map[string]any{
		"Total":      len(labs),
		"CountCKA":   counts["CKA"],
		"CountCKAD":  counts["CKAD"],
		"CountCKS":   counts["CKS"],
		"CountArgo":  counts["ArgoCD"],
		"Certs":      certs,
		"Difficulty": difficulty,
		"TopicsJSON": template.JS(topicsJSON),
		"ExtraCerts": extraCerts(h.repo.Certs()),
		"NavActive":  "labs",
	}
	h.render(w, "lab_list.html", data)
}

func (h *LabHandler) Show(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q, ok := h.repo.GetByID(id)
	if !ok {
		http.Redirect(w, r, "/lab", http.StatusSeeOther)
		return
	}
	prevID, nextID := h.repo.GetLabNeighbors(id)

	// Contexto do tutor: eventos do terminal serão atribuídos a esta questão.
	tutor.SetActiveQuestion(q)

	// Build effective goals: use explicit Goals if defined, else synthesize from question's own Validation
	goals := q.Goals
	if len(goals) == 0 && q.Validation != nil {
		goals = []models.Goal{{
			Description: "Validar o resultado",
			Validation:  q.Validation,
		}}
	}

	// Session support
	sessionID := ""
	sessionIndex := 0
	sessionTotal := 0
	sessionIsLast := false
	sessionPct := 0
	if sid := r.URL.Query().Get("session"); sid != "" {
		if sess, ok := h.labSessions.Get(sid); ok {
			sessionID = sess.ID
			sessionIndex = sess.Index + 1 // 1-based for display
			sessionTotal = len(sess.Questions)
			sessionIsLast = sess.Index == sessionTotal-1
			if sessionTotal > 0 {
				sessionPct = sessionIndex * 100 / sessionTotal
			}
		}
	}

	data := map[string]any{
		"Question":      q,
		"Goals":         goals,
		"HasSetup":      len(q.Setup) > 0,
		"PrevID":        prevID,
		"NextID":        nextID,
		"SessionID":     sessionID,
		"SessionIndex":  sessionIndex,
		"SessionTotal":  sessionTotal,
		"SessionIsLast": sessionIsLast,
		"SessionPct":    sessionPct,
	}
	h.render(w, "lab.html", data)
}

func (h *LabHandler) CreateSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var req struct {
		Certs      []string `json:"certs"`
		Difficulty string   `json:"difficulty"`
		Topics     []string `json:"topics"`
		Count      int      `json:"count"` // 9999 = "todas" (limitado a 50)
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Count <= 0 {
		if req.Count <= 0 {
			req.Count = 10
		}
	}
	if req.Count > 50 {
		req.Count = 50
	}

	questions := h.repo.FilterLabs(req.Certs, req.Difficulty, req.Topics)
	if len(questions) == 0 {
		json.NewEncoder(w).Encode(map[string]any{"error": "nenhuma questão encontrada com esses filtros"})
		return
	}

	// Pré-aquece as imagens dos labs no cluster ativo (background, throttled)
	// para que os pods de setup fiquem Ready em segundos.
	PrewarmLabImages(questions)

	rand.Shuffle(len(questions), func(i, j int) { questions[i], questions[j] = questions[j], questions[i] })
	if req.Count > len(questions) {
		req.Count = len(questions)
	}

	ids := make([]string, req.Count)
	for i := range ids {
		ids[i] = questions[i].ID
	}

	sess := h.labSessions.Create(ids)
	json.NewEncoder(w).Encode(map[string]any{
		"id":    sess.ID,
		"first": ids[0],
		"total": len(ids),
	})
}

func (h *LabHandler) AdvanceSession(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	sid := r.PathValue("sid")
	idx, total, nextID, done := h.labSessions.Advance(sid)
	json.NewEncoder(w).Encode(map[string]any{
		"done":  done,
		"next":  nextID,
		"index": idx + 1,
		"total": total,
	})
}

func runCmd(cmdStr string) (string, error) {
	touchActivity()
	out, err := wslShell(cmdStr).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func (h *LabHandler) Setup(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id := r.PathValue("id")
	q, ok := h.repo.GetByID(id)
	if !ok {
		fmt.Fprintf(w, "data: {\"type\":\"error\",\"msg\":\"Questão não encontrada\"}\n\n")
		flusher.Flush()
		return
	}

	if len(q.Setup) == 0 {
		fmt.Fprintf(w, "data: {\"type\":\"done\"}\n\n")
		flusher.Flush()
		return
	}

	total := len(q.Setup)
	for i, step := range q.Setup {
		msg, _ := json.Marshal(map[string]any{
			"type":        "step",
			"index":       i,
			"total":       total,
			"description": step.Description,
			"status":      "running",
		})
		fmt.Fprintf(w, "data: %s\n\n", msg)
		flusher.Flush()

		output, err := runCmd(step.Command)

		status := "done"
		if err != nil {
			lower := strings.ToLower(output)
			if !strings.Contains(lower, "already exists") && !strings.Contains(lower, "alreadyexists") {
				status = "warn"
			}
		}

		msg, _ = json.Marshal(map[string]any{
			"type":        "step",
			"index":       i,
			"total":       total,
			"description": step.Description,
			"status":      status,
			"output":      output,
		})
		fmt.Fprintf(w, "data: %s\n\n", msg)
		flusher.Flush()
	}

	done, _ := json.Marshal(map[string]any{"type": "done"})
	fmt.Fprintf(w, "data: %s\n\n", done)
	flusher.Flush()
}

func (h *LabHandler) Teardown(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := r.PathValue("id")
	q, ok := h.repo.GetByID(id)
	if !ok {
		json.NewEncoder(w).Encode(map[string]any{"success": false})
		return
	}

	for _, cmdStr := range q.Teardown {
		runCmd(cmdStr)
	}

	json.NewEncoder(w).Encode(map[string]any{"success": true})
}

type validateResponse struct {
	Success     bool   `json:"success"`
	Output      string `json:"output"`
	Explanation string `json:"explanation"`
	DocURL      string `json:"doc_url"`
	DocSection  string `json:"doc_section"`
}

func (h *LabHandler) Validate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	id := r.PathValue("id")
	q, ok := h.repo.GetByID(id)
	if !ok {
		json.NewEncoder(w).Encode(validateResponse{Success: false, Output: "Questão não encontrada"})
		return
	}

	// Determine which validation to run: specific goal or the question's own.
	var validation *models.Validation
	goalIdxStr := r.URL.Query().Get("goal")
	if goalIdxStr != "" {
		goalIdx, err := strconv.Atoi(goalIdxStr)
		if err == nil && goalIdx >= 0 && goalIdx < len(q.Goals) {
			validation = q.Goals[goalIdx].Validation
		}
	}
	if validation == nil {
		validation = q.Validation
	}

	if validation == nil {
		json.NewEncoder(w).Encode(validateResponse{
			Success:     true,
			Output:      "Sem validação automática para este goal.",
			Explanation: q.Explanation,
			DocURL:      q.DocURL,
			DocSection:  q.DocSection,
		})
		return
	}

	output, err := runCmd(validation.Command)
	if output == "" && err != nil {
		output = err.Error()
	}

	// O conteúdo esperado é a fonte de verdade — NÃO o exit code. Validações de
	// ausência (ex.: 'kubectl get pod X' deve dizer "not found") saem com exit 1
	// justamente quando o estado está correto.
	success := false
	if validation.ExpectedContains != "" {
		success = strings.Contains(output, validation.ExpectedContains)
	} else if validation.ExpectedOutput != "" {
		success = strings.TrimSpace(output) == strings.TrimSpace(validation.ExpectedOutput)
	} else {
		// Sem expectativa definida: sucesso = comando executou sem erro.
		success = err == nil
	}

	// Alimenta o modelo de habilidade do tutor com o resultado do check.
	tutor.RecordGoal(q, success)

	resp := validateResponse{
		Success: success,
		Output:  output,
	}
	// Only send explanation/docs on success to avoid giving away the answer on failure.
	if success {
		resp.Explanation = q.Explanation
		resp.DocURL = q.DocURL
		resp.DocSection = q.DocSection
	}
	json.NewEncoder(w).Encode(resp)
}
