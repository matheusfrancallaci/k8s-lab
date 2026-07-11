package tutor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// Registro de certificações — o board é personalizável: pedir uma certificação
// ao tutor cadastra o botão de verdade (persistido em data/certs.json).
// ─────────────────────────────────────────────────────────────────────────────

var builtinCerts = []string{"CKA", "CKAD", "CKS", "KCNA", "ArgoCD", "CAPA", "AWS", "Terraform", "Ansible", "Linux", "Bash", "Java"}

var (
	certsMu    sync.Mutex
	registered []string
)

func certsFile() string { return filepath.Join("data", "certs.json") }

// LoadCerts carrega o registro do disco (chame no boot, junto do Load).
func LoadCerts() {
	certsMu.Lock()
	defer certsMu.Unlock()
	b, err := os.ReadFile(certsFile())
	if err != nil {
		return
	}
	json.Unmarshal(b, &registered) //nolint:errcheck
}

var certNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._+-]{1,30}$`)

// RegisterCert cadastra uma certificação nova (idempotente). Retorna o nome
// normalizado e se é inédita.
func RegisterCert(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if !certNameRe.MatchString(name) {
		return "", false
	}
	certsMu.Lock()
	defer certsMu.Unlock()
	for _, c := range append(builtinCerts, registered...) {
		if strings.EqualFold(c, name) {
			return c, false
		}
	}
	registered = append(registered, name)
	os.MkdirAll("data", 0o755) //nolint:errcheck
	if b, err := json.MarshalIndent(registered, "", "  "); err == nil {
		os.WriteFile(certsFile(), b, 0o644) //nolint:errcheck
	}
	return name, true
}

// AllCerts devolve builtin + cadastradas (ordem estável).
func AllCerts() []string {
	certsMu.Lock()
	defer certsMu.Unlock()
	out := append([]string{}, builtinCerts...)
	out = append(out, registered...)
	return out
}

func CanonicalCert(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	for _, c := range AllCerts() {
		if strings.EqualFold(c, name) {
			return c
		}
	}
	return name
}
