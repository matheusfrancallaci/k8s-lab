package tutor

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"estudo-app/internal/models"
)

// DocumentTopic is a selectable section found in a trusted documentation page.
// Available means an exact deterministic template exists; Researchable means the
// tutor can still inspect the section and attempt a strictly validated lab.
type DocumentTopic struct {
	Label        string `json:"label"`
	Source       string `json:"source"`
	Level        int    `json:"level"`
	Topic        string `json:"topic,omitempty"`
	Available    bool   `json:"available"`
	Researchable bool   `json:"researchable"`
	Description  string `json:"description"`
}

var (
	docHeadingRe = regexp.MustCompile(`(?is)<h([1-4])\b([^>]*)>(.*?)</h[1-4]>`)
	docIDRe      = regexp.MustCompile(`(?i)\bid=["']([^"']+)["']`)
	docSpaceRe   = regexp.MustCompile(`\s+`)
)

// AnalyzeDocumentTopics reads one trusted page and exposes its real headings as
// topic buttons. It never invents sections and never treats a successful fetch
// as proof that a runnable lab exists.
func AnalyzeDocumentTopics(source string) ([]DocumentTopic, []string, error) {
	source = strings.TrimSpace(source)
	if source == "" || !isTrustedURL(source) {
		return nil, nil, fmt.Errorf("informe uma URL de documentacao oficial permitida")
	}
	raw, err := fetchRawHTML(source)
	if err != nil {
		return nil, nil, fmt.Errorf("nao consegui ler a documentacao: %w", err)
	}
	topics := documentTopicsFromHTML(source, raw)
	if len(topics) == 0 {
		text, err := fetchURL(source)
		if err != nil {
			return nil, nil, fmt.Errorf("nao consegui extrair topicos da documentacao: %w", err)
		}
		topics = documentTopicsFromText(source, text)
	}
	if len(topics) == 0 {
		return nil, nil, fmt.Errorf("a pagina foi lida, mas nao encontrei secoes selecionaveis")
	}
	return topics, []string{source}, nil
}

func documentTopicsFromHTML(source, raw string) []DocumentTopic {
	if m := mainRe.FindStringSubmatch(raw); m != nil {
		raw = m[1]
	}
	seen := map[string]bool{}
	out := make([]DocumentTopic, 0, 16)
	for _, match := range docHeadingRe.FindAllStringSubmatch(raw, -1) {
		level, _ := strconv.Atoi(match[1])
		label := cleanDocumentHeading(match[3])
		key := strings.ToLower(label)
		if !useDocumentHeading(label) || seen[key] {
			continue
		}
		seen[key] = true
		anchor := ""
		if id := docIDRe.FindStringSubmatch(match[2]); len(id) > 1 {
			anchor = id[1]
		}
		out = append(out, newDocumentTopic(label, documentFragment(source, anchor), level))
		if len(out) >= 30 {
			break
		}
	}
	return out
}

func documentTopicsFromText(source, text string) []DocumentTopic {
	seen := map[string]bool{}
	var out []DocumentTopic
	for _, match := range topicLineRe.FindAllStringSubmatch(text, -1) {
		label := cleanDocumentHeading(match[1])
		key := strings.ToLower(label)
		if !useDocumentHeading(label) || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, newDocumentTopic(label, source, 2))
		if len(out) >= 30 {
			break
		}
	}
	return out
}

func cleanDocumentHeading(value string) string {
	value = tagRe.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	value = strings.TrimSpace(docSpaceRe.ReplaceAllString(value, " "))
	return value
}

func useDocumentHeading(label string) bool {
	if len(label) < 3 || len(label) > 120 {
		return false
	}
	lower := strings.ToLower(label)
	for _, ignored := range []string{"on this page", "nesta pagina", "table of contents", "feedback", "whats next", "what's next"} {
		if lower == ignored {
			return false
		}
	}
	return true
}

func documentFragment(source, anchor string) string {
	u, err := url.Parse(source)
	if err != nil || anchor == "" {
		return source
	}
	u.Fragment = anchor
	return u.String()
}

func newDocumentTopic(label, source string, level int) DocumentTopic {
	topic := exactTopicForRequest("", label)
	_, available := templates[topic]
	description := "Pesquisar esta secao e validar um lab funcional"
	if available {
		description = "Template exato disponivel; o lab sera validado antes de iniciar"
	}
	return DocumentTopic{
		Label: label, Source: source, Level: level, Topic: topic,
		Available: available, Researchable: true, Description: description,
	}
}

// documentSection returns only the selected section. This prevents a request
// for one heading from silently borrowing commands or manifests from another.
func documentSection(source, selected string) (string, string, error) {
	base := source
	if u, err := url.Parse(source); err == nil {
		u.Fragment = ""
		base = u.String()
	}
	if !isTrustedURL(base) {
		return "", "", fmt.Errorf("fonte fora da lista de documentacao permitida")
	}
	raw, err := fetchRawHTML(base)
	if err != nil {
		return "", "", err
	}
	if m := mainRe.FindStringSubmatch(raw); m != nil {
		raw = m[1]
	}
	matches := docHeadingRe.FindAllStringSubmatchIndex(raw, -1)
	wanted := strings.ToLower(strings.TrimSpace(selected))
	for i, m := range matches {
		label := strings.ToLower(cleanDocumentHeading(raw[m[6]:m[7]]))
		if label != wanted && !strings.Contains(label, wanted) && !strings.Contains(wanted, label) {
			continue
		}
		level, _ := strconv.Atoi(raw[m[2]:m[3]])
		end := len(raw)
		for j := i + 1; j < len(matches); j++ {
			nextLevel, _ := strconv.Atoi(raw[matches[j][2]:matches[j][3]])
			if nextLevel <= level {
				end = matches[j][0]
				break
			}
		}
		attrs := raw[m[4]:m[5]]
		anchor := ""
		if id := docIDRe.FindStringSubmatch(attrs); len(id) > 1 {
			anchor = id[1]
		}
		sectionURL := documentFragment(base, anchor)
		return markSource(sectionURL, htmlToText(raw[m[0]:end])), sectionURL, nil
	}
	return "", "", fmt.Errorf("a secao %q nao foi encontrada na pagina informada", selected)
}

// GenerateDocumentLabs uses an exact built-in template when one exists. For a
// new topic it only uses commands/manifests from the selected section and never
// falls back to an unrelated certification template.
func GenerateDocumentLabs(source, cert, selected string, level, count int) ([]models.Question, IngestReport, error) {
	section, sectionURL, err := documentSection(source, selected)
	if err != nil {
		return nil, IngestReport{}, err
	}
	if count < 1 {
		count = 1
	}
	if count > 8 {
		count = 8
	}
	if topic := exactTopicForRequest(cert, selected); topic != "" {
		if _, ok := templates[topic]; ok {
			qs := generateQuestions(topic, cert, level, count)
			for i := range qs {
				qs[i].Source = models.SourceGenerated
				qs[i].DocURL = sectionURL
				qs[i].DocSection = selected
			}
			qs = FinalizeLabs(qs, section)
			if err := validateGeneratedLabs(qs); err != nil {
				return nil, IngestReport{Sources: []string{sectionURL}}, err
			}
			if err := persist(qs); err != nil {
				return nil, IngestReport{Sources: []string{sectionURL}}, err
			}
			_ = RecordLabCatalog(qs)
			return qs, IngestReport{Sources: []string{sectionURL}, Labs: len(qs)}, nil
		}
	}

	commands := extractCommands(section)
	manifests := extractManifests(section)
	rep := IngestReport{Sources: []string{sectionURL}, Commands: len(commands), Manifests: len(manifests)}
	var qs []models.Question
	for _, command := range commands {
		if len(qs) >= count {
			break
		}
		if q, ok := labFromCommand(command, cert, selected, level); ok {
			q.Source = models.SourceGenerated
			qs = append(qs, q)
		}
	}
	for _, manifest := range manifests {
		if len(qs) >= count {
			break
		}
		if q, ok := labFromManifest(manifest, cert, selected, level); ok {
			q.Source = models.SourceGenerated
			qs = append(qs, q)
		}
	}
	if len(qs) == 0 {
		return nil, rep, fmt.Errorf("pesquisei a secao, mas ela nao contem um exercicio executavel e seguro para este cluster; nenhum lab generico foi criado")
	}
	qs = FinalizeLabs(qs, section)
	if err := validateGeneratedLabs(qs); err != nil {
		return nil, rep, err
	}
	if err := persist(qs); err != nil {
		return nil, rep, err
	}
	_ = RecordLabCatalog(qs)
	rep.Labs = len(qs)
	return qs, rep, nil
}
