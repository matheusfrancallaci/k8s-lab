package handlers

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"estudo-app/internal/models"
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
	topicsJSON, _ := json.Marshal(tutor.Topics())
	RenderPage(w, h.templates, "tutor.html", map[string]any{
		"GenTopicsJSON": template.JS(topicsJSON),
		"NavActive":     "tutor",
	})
}

// Status devolve recomendações ativas + estatísticas de habilidade.
func (h *TutorHandler) Status(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	llmOK, llmModel := tutor.LLMStatus()
	cert := r.URL.Query().Get("cert")
	if cert == "" {
		cert = "CKA"
	}
	nextDecision := tutor.BuildTutorDecision(userID(r), "", cert)
	// Predictive preparation: opening the tutor dashboard warms the images for
	// the learner's most likely next topic before they request the lab.
	if likely := h.repo.FilterLabs([]string{cert}, "", []string{nextDecision.TargetTopic}); len(likely) > 0 {
		PrewarmLabImages(likely)
	}
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"recommendations": tutor.Advise(userID(r)),
		"stats":           tutor.Stats(userID(r)),
		"domain_map":      tutor.DomainMap(userID(r), cert),
		"review":          tutor.ReviewQueue(userID(r)),
		"history":         tutor.History(userID(r)),
		"journey":         tutor.Journey(userID(r)),
		"mastery":         tutor.MasteryPathForCert(userID(r), cert),
		"learning_memory": tutor.LearningMemoryFor(userID(r)),
		"next_decision":   nextDecision,
		"coverage":        coverageOrNil(cert, h.repo.Filter([]string{cert}, "")),
		"rag":             tutor.RAGStatus(),
		"observability":   tutor.LabObservability(),
		"gen_topics":      tutor.Topics(),
		"certs":           tutor.AllCerts(),
		"llm":             map[string]any{"available": llmOK, "model": llmModel},
		"model_readiness": tutor.LLMModelReadiness(),
	})
}

// Author engorda o banco em lote: labs nível prova com verificação executável
// OBRIGATÓRIA (reprovado não entra). Usa o gateway remoto quando configurado.
func (h *TutorHandler) Author(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Cert  string `json:"cert"`
		Topic string `json:"topic"`
		Count int    `json:"count"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": "payload inválido"}) //nolint:errcheck
		return
	}
	qs, rep, err := tutor.AuthorExamBatch(body.Cert, body.Topic, body.Count)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "report": rep}) //nolint:errcheck
		return
	}
	h.repo.Add(qs)
	PrewarmLabImages(qs)
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "report": rep}) //nolint:errcheck
}

// Goal persiste o objetivo do aluno (onboarding): cert, data da prova e nível.
func (h *TutorHandler) Goal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Cert     string `json:"cert"`
		ExamDate string `json:"exam_date"`
		Level    string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": "payload inválido"}) //nolint:errcheck
		return
	}
	if err := tutor.SetStudyGoal(userID(r), body.Cert, body.ExamDate, body.Level); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()}) //nolint:errcheck
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "journey": tutor.Journey(userID(r))}) //nolint:errcheck
}

// coverageOrNil evita mandar um relatório vazio para certs sem currículo
// embutido (o front esconde o card quando vem null).
func coverageOrNil(cert string, qs []models.Question) any {
	rep, ok := tutor.CurriculumCoverage(cert, qs)
	if !ok {
		return nil
	}
	return rep
}

// Eval roda golden prompts determinísticos contra o roteador/gerador de labs.
func (h *TutorHandler) Eval(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tutor.RunGoldenEval()) //nolint:errcheck
}

// Quality mostra o ranking dos prompts reais capturados para regressao continua.
func (h *TutorHandler) Quality(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tutor.PromptQualityReport()) //nolint:errcheck
}

// PromoteQualityFixture promotes a reviewed real prompt into durable regression coverage.
func (h *TutorHandler) PromoteQualityFixture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		json.NewEncoder(w).Encode(map[string]any{"error": "id obrigatorio"})
		return
	}
	if err := tutor.PromotePromptRegression(body.ID); err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": "prompt nao encontrado"})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

// AdminQuality agrega sinais de qualidade para operacao/admin.
func (h *TutorHandler) AdminQuality(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tutor.BuildAdminQualityReport()) //nolint:errcheck
}

// DeployGate retorna os gates que devem estar verdes antes de publicar imagem.
func (h *TutorHandler) DeployGate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tutor.RunDeployGate()) //nolint:errcheck
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
	text, err := tutor.LLMExplainFailure(q.Question, goalDesc, valCmd, q.AnswerCommand, body.Output)
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
		Type       string `json:"type"` // hint_view | solution_view | dismiss | helpful | unhelpful
		QuestionID string `json:"question_id"`
		Cert       string `json:"cert"`
		Topic      string `json:"topic"`
		MessageID  string `json:"message_id"`
		Prompt     string `json:"prompt"` // pergunta que gerou a resposta avaliada
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
	case "helpful", "unhelpful":
		_ = tutor.RecordTutorFeedback(userID(r), body.MessageID, body.Type, body.Cert, body.Topic, body.Prompt)
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true}) //nolint:errcheck
}

// ExamReport traduz o resultado do Modo Exame em projeção de aprovação
// ponderada pelos pesos oficiais dos domínios da certificação.
func (h *TutorHandler) ExamReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body struct {
		Cert    string             `json:"cert"`
		Results []tutor.ExamAnswer `json:"results"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Results) == 0 {
		json.NewEncoder(w).Encode(map[string]any{"error": "resultados do simulado obrigatórios"}) //nolint:errcheck
		return
	}
	if len(body.Results) > 64 {
		body.Results = body.Results[:64]
	}
	json.NewEncoder(w).Encode(tutor.BuildExamReport(body.Cert, body.Results)) //nolint:errcheck
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

	uid := userID(r)
	res := tutor.Chat(uid, body.Message, body.Cert, func(ids []string) (string, string, int) {
		sess := h.labSessions.Create(ids)
		return sess.ID, ids[0], len(ids)
	})
	decision := tutor.BuildTutorDecision(uid, body.Message, body.Cert)
	res.Decision = &decision
	if len(res.Questions) > 0 {
		h.repo.Add(res.Questions)
		PrewarmLabImages(res.Questions)
	}
	tutor.RecordPromptQuality(uid, body.Message, body.Cert, res)
	reply := res.Reply
	if res.NeedsLLM { // conversa livre: resolve o LLM síncrono (fallback = res.Reply)
		if r, err := tutor.FreeChatReplyContext(r.Context(), body.Message); err == nil && r != "" {
			reply = r
		}
	}
	json.NewEncoder(w).Encode(map[string]any{"reply": reply, "action": res.Action, "decision": res.Decision}) //nolint:errcheck
}

// chatActionMarker separa o texto da resposta do JSON da ação no stream. Usa um
// byte nulo (nunca aparece em texto/markdown) para o front recortar com segurança.
const chatActionMarker = "\x00@@ACTION@@"

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

	uid := userID(r)
	res := tutor.Chat(uid, body.Message, body.Cert, func(ids []string) (string, string, int) {
		sess := h.labSessions.Create(ids)
		return sess.ID, ids[0], len(ids)
	})
	if len(res.Questions) > 0 {
		h.repo.Add(res.Questions)
		PrewarmLabImages(res.Questions)
	}
	tutor.RecordPromptQuality(uid, body.Message, body.Cert, res)

	flusher, _ := w.(http.Flusher)

	// Conversa livre (nenhuma intenção casou): streama o LLM token a token.
	// Só aqui o LLM entra — NUNCA sobre uma intenção reconhecida.
	if res.NeedsLLM && flusher != nil {
		var got bool
		err := tutor.StreamLLMReplyContext(r.Context(), body.Message, func(chunk string) {
			if chunk == "" {
				return
			}
			got = true
			fmt.Fprint(w, chunk)
			flusher.Flush()
		})
		if err == nil && got {
			return
		}
		fmt.Fprint(w, res.Reply) // LLM falhou → fallback de capacidades
		return
	}
	if res.NeedsLLM { // sem flusher (raro): resolve síncrono
		if r, err := tutor.FreeChatReply(body.Message); err == nil && r != "" {
			fmt.Fprint(w, r)
			return
		}
		fmt.Fprint(w, res.Reply)
		return
	}

	// Intenção reconhecida: entrega a resposta pronta + a ação (para a UI
	// registrar a cert, iniciar a sessão de labs, abrir o exame, etc.).
	fmt.Fprint(w, res.Reply)
	if res.Action != nil {
		if b, err := json.Marshal(res.Action); err == nil {
			fmt.Fprint(w, chatActionMarker+string(b))
		}
	}
	if flusher != nil {
		flusher.Flush()
	}
}
