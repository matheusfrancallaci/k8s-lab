package tutor

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ConversationMessage struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	Sources   []string        `json:"sources,omitempty"`
	Audit     *GroundingAudit `json:"audit,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

type Conversation struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	Cert      string                `json:"cert"`
	Mode      string                `json:"mode"`
	Messages  []ConversationMessage `json:"messages,omitempty"`
	CreatedAt time.Time             `json:"created_at"`
	UpdatedAt time.Time             `json:"updated_at"`
}

type conversationDataset struct {
	Users map[string][]Conversation `json:"users"`
}

var conversationsMu sync.Mutex

func conversationsPath() string {
	if p := strings.TrimSpace(os.Getenv("TUTOR_CONVERSATIONS_PATH")); p != "" {
		return p
	}
	return filepath.Join("data", "tutor", "conversations.json")
}

func loadConversationsLocked() conversationDataset {
	d := conversationDataset{Users: map[string][]Conversation{}}
	b, err := os.ReadFile(conversationsPath())
	if err == nil {
		_ = json.Unmarshal(b, &d)
	}
	if d.Users == nil {
		d.Users = map[string][]Conversation{}
	}
	return d
}

func saveConversationsLocked(d conversationDataset) error {
	path := conversationsPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func conversationUserKey(userID string) string { return ragID(strings.TrimSpace(userID)) }

func ListConversations(userID string) []Conversation {
	conversationsMu.Lock()
	defer conversationsMu.Unlock()
	items := append([]Conversation(nil), loadConversationsLocked().Users[conversationUserKey(userID)]...)
	for i := range items {
		items[i].Messages = nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items
}

func GetConversation(userID, id string) (Conversation, bool) {
	conversationsMu.Lock()
	defer conversationsMu.Unlock()
	for _, c := range loadConversationsLocked().Users[conversationUserKey(userID)] {
		if c.ID == id {
			return c, true
		}
	}
	return Conversation{}, false
}

func CreateConversation(userID, cert, mode string) (Conversation, error) {
	conversationsMu.Lock()
	defer conversationsMu.Unlock()
	d := loadConversationsLocked()
	now := time.Now().UTC()
	c := Conversation{ID: ragID(userID + now.String()), Title: "Nova conversa", Cert: compactText(cert, 24), Mode: normalizeResponseMode(mode), CreatedAt: now, UpdatedAt: now}
	key := conversationUserKey(userID)
	d.Users[key] = append([]Conversation{c}, d.Users[key]...)
	if len(d.Users[key]) > 50 {
		d.Users[key] = d.Users[key][:50]
	}
	return c, saveConversationsLocked(d)
}

func AppendConversationMessage(userID, id, role, content string, sources []string, audit ...*GroundingAudit) (Conversation, error) {
	if role != "user" && role != "assistant" {
		return Conversation{}, errors.New("papel de mensagem invalido")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return Conversation{}, errors.New("mensagem vazia")
	}
	if len(content) > 12000 {
		content = content[:12000]
	}
	conversationsMu.Lock()
	defer conversationsMu.Unlock()
	d := loadConversationsLocked()
	key := conversationUserKey(userID)
	for i := range d.Users[key] {
		c := &d.Users[key][i]
		if c.ID != id {
			continue
		}
		now := time.Now().UTC()
		var claimAudit *GroundingAudit
		if len(audit) > 0 {
			claimAudit = audit[0]
		}
		c.Messages = append(c.Messages, ConversationMessage{ID: ragID(id + role + now.String()), Role: role, Content: content, Sources: limitedStrings(sources, 8), Audit: claimAudit, CreatedAt: now})
		if len(c.Messages) > 80 {
			c.Messages = c.Messages[len(c.Messages)-80:]
		}
		if c.Title == "Nova conversa" && role == "user" {
			c.Title = compactText(content, 52)
		}
		c.UpdatedAt = now
		return *c, saveConversationsLocked(d)
	}
	return Conversation{}, errors.New("conversa nao encontrada")
}

func DeleteConversation(userID, id string) error {
	conversationsMu.Lock()
	defer conversationsMu.Unlock()
	d := loadConversationsLocked()
	key := conversationUserKey(userID)
	items := d.Users[key]
	for i := range items {
		if items[i].ID == id {
			d.Users[key] = append(items[:i], items[i+1:]...)
			return saveConversationsLocked(d)
		}
	}
	return errors.New("conversa nao encontrada")
}

func RenameConversation(userID, id, title, mode string) error {
	conversationsMu.Lock()
	defer conversationsMu.Unlock()
	d := loadConversationsLocked()
	key := conversationUserKey(userID)
	for i := range d.Users[key] {
		if d.Users[key][i].ID == id {
			if strings.TrimSpace(title) != "" {
				d.Users[key][i].Title = compactText(title, 64)
			}
			d.Users[key][i].Mode = normalizeResponseMode(mode)
			d.Users[key][i].UpdatedAt = time.Now().UTC()
			return saveConversationsLocked(d)
		}
	}
	return errors.New("conversa nao encontrada")
}

func normalizeResponseMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "short", "deep", "exam", "diagnostic":
		return strings.ToLower(strings.TrimSpace(mode))
	default:
		return "didactic"
	}
}

func ConversationContext(c Conversation, current string) string {
	var b strings.Builder
	start := len(c.Messages) - 10
	if start < 0 {
		start = 0
	}
	for _, m := range c.Messages[start:] {
		if m.Content == current && m.Role == "user" {
			continue
		}
		text := sanitizeRetrievedText(compactText(m.Content, 1200))
		if text == "" {
			continue
		}
		label := "Aluno"
		if m.Role == "assistant" {
			label = "Tutor"
		}
		b.WriteString(label + ": " + text + "\n")
	}
	return strings.TrimSpace(b.String())
}
