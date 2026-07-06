package repository

import (
	"embed"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"estudo-app/internal/models"

	"gopkg.in/yaml.v3"
)

type QuestionRepository struct {
	mu        sync.RWMutex
	questions []models.Question
}

// snapshot devolve uma referência de leitura segura ao slice atual.
// Adições em runtime substituem o slice (append copia), então iterar sobre
// um snapshot é seguro sem segurar o lock.
func (r *QuestionRepository) snapshot() []models.Question {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.questions
}

// qKey identifica uma questão pelo CONTEÚDO (não só o ID) — gerações repetidas
// produzem o mesmo texto com IDs diferentes, e é isso que aparecia duplicado.
func qKey(q models.Question) string {
	return string(q.Cert) + "|" + string(q.Type) + "|" +
		strings.ToLower(strings.Join(strings.Fields(q.Question), " "))
}

// Add insere questões em runtime (labs/quizzes gerados pelo tutor), pulando
// duplicatas por ID E por conteúdo. Cobre também o carregamento do disco
// (LoadDir chama Add), então arquivos gen-*.yaml repetidos não duplicam.
func (r *QuestionRepository) Add(qs []models.Question) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool, len(r.questions)*2)
	for _, q := range r.questions {
		seen["id:"+q.ID] = true
		seen[qKey(q)] = true
	}
	for _, q := range qs {
		if seen["id:"+q.ID] || seen[qKey(q)] {
			continue
		}
		seen["id:"+q.ID] = true
		seen[qKey(q)] = true
		r.questions = append(r.questions, q)
	}
}

// LoadDir carrega YAMLs de um diretório do disco (questions-custom/),
// complementando o banco embutido. Ausência do diretório não é erro.
func (r *QuestionRepository) LoadDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var loaded []models.Question
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var qf models.QuestionFile
		if yaml.Unmarshal(data, &qf) == nil {
			loaded = append(loaded, qf.Questions...)
		}
	}
	if len(loaded) > 0 {
		r.Add(loaded)
		log.Printf("[repo] %d questões custom carregadas de %s", len(loaded), dir)
	}
}

func NewQuestionRepository(fs embed.FS) (*QuestionRepository, error) {
	var all []models.Question

	entries, err := fs.ReadDir("questions")
	if err != nil {
		return nil, fmt.Errorf("erro ao ler diretório questions: %w", err)
	}

	for _, certDir := range entries {
		if !certDir.IsDir() {
			continue
		}

		dirPath := path.Join("questions", certDir.Name())
		files, err := fs.ReadDir(dirPath)
		if err != nil {
			continue
		}

		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".yaml") {
				continue
			}

			filePath := path.Join("questions", certDir.Name(), f.Name())
			data, err := fs.ReadFile(filePath)
			if err != nil {
				continue
			}

			var qf models.QuestionFile
			if err := yaml.Unmarshal(data, &qf); err != nil {
				continue
			}

			all = append(all, qf.Questions...)
		}
	}

	return &QuestionRepository{questions: all}, nil
}

func (r *QuestionRepository) Filter(certs []string, difficulty string) []models.Question {
	var result []models.Question

	for _, q := range r.snapshot() {
		matchCert := len(certs) == 0 || containsIgnoreCase(certs, string(q.Cert))
		matchDiff := difficulty == "" || strings.EqualFold(string(q.Difficulty), difficulty)

		if matchCert && matchDiff {
			result = append(result, q)
		}
	}

	return result
}

func (r *QuestionRepository) Random(certs []string, difficulty string, limit int) []models.Question {
	questions := r.Filter(certs, difficulty)
	rand.Shuffle(len(questions), func(i, j int) {
		questions[i], questions[j] = questions[j], questions[i]
	})

	if limit > 0 && limit < len(questions) {
		return questions[:limit]
	}

	return questions
}

func (r *QuestionRepository) Count() int { return len(r.snapshot()) }

func (r *QuestionRepository) Certs() []string {
	seen := map[string]bool{}
	var certs []string

	for _, q := range r.snapshot() {
		c := string(q.Cert)
		if !seen[c] {
			seen[c] = true
			certs = append(certs, c)
		}
	}

	return certs
}

func (r *QuestionRepository) GetByID(id string) (models.Question, bool) {
	for _, q := range r.snapshot() {
		if q.ID == id {
			return q, true
		}
	}
	return models.Question{}, false
}

func (r *QuestionRepository) FilterByType(qtype string) []models.Question {
	var result []models.Question
	for _, q := range r.snapshot() {
		if string(q.Type) == qtype {
			result = append(result, q)
		}
	}
	return result
}

func (r *QuestionRepository) FilterLabs(certs []string, difficulty string, topics []string) []models.Question {
	var result []models.Question
	for _, q := range r.snapshot() {
		if string(q.Type) != "lab" {
			continue
		}
		matchCert := len(certs) == 0 || containsIgnoreCase(certs, string(q.Cert))
		matchDiff := difficulty == "" || strings.EqualFold(string(q.Difficulty), difficulty)
		matchTopic := len(topics) == 0 || containsIgnoreCase(topics, q.Topic)
		if matchCert && matchDiff && matchTopic {
			result = append(result, q)
		}
	}
	return result
}

// TopicInfo descreve um tópico disponível para estudo dirigido.
type TopicInfo struct {
	Name  string `json:"name"`
	Cert  string `json:"cert"`
	Count int    `json:"count"`
}

// LabTopics lista os tópicos dos labs com certificação e quantidade,
// ordenados por cert e nome — alimenta o seletor de tópicos da UI.
func (r *QuestionRepository) LabTopics() []TopicInfo {
	type key struct{ cert, topic string }
	counts := map[key]int{}
	for _, q := range r.snapshot() {
		if string(q.Type) != "lab" {
			continue
		}
		counts[key{string(q.Cert), q.Topic}]++
	}
	out := make([]TopicInfo, 0, len(counts))
	for k, n := range counts {
		out = append(out, TopicInfo{Name: k.topic, Cert: k.cert, Count: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cert != out[j].Cert {
			return out[i].Cert < out[j].Cert
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (r *QuestionRepository) GetLabNeighbors(id string) (prevID, nextID string) {
	labs := r.FilterLabs(nil, "", nil)
	for i, q := range labs {
		if q.ID == id {
			if i > 0 {
				prevID = labs[i-1].ID
			}
			if i < len(labs)-1 {
				nextID = labs[i+1].ID
			}
			return
		}
	}
	return
}

func containsIgnoreCase(slice []string, val string) bool {
	for _, s := range slice {
		if strings.EqualFold(s, val) {
			return true
		}
	}
	return false
}
