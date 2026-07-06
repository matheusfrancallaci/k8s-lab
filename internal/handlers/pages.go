package handlers

import (
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"
	"sync"
)

// ─────────────────────────────────────────────────────────────────────────────
// Renderização de páginas HTML — compila-uma-vez + cache.
//
// Antes cada handler chamava template.ParseFS a CADA request (parse do disco
// embed + compilação toda vez). Agora as páginas são pré-compiladas no boot
// (PrecompileTemplates) e só executadas por request. Ganhos:
//   - latência: sem re-parse por hit;
//   - robustez: um template quebrado falha no START, não em produção;
//   - concorrência: *template.Template é seguro p/ Execute concorrente depois
//     de totalmente parseado (nenhum parse acontece mais em runtime).
// ─────────────────────────────────────────────────────────────────────────────

var (
	pageTmplMu sync.RWMutex
	pageTmpls  = map[string]*template.Template{}
)

// parsePage compila base + nav + a página, com o funcMap compartilhado.
func parsePage(fs embed.FS, page string) (*template.Template, error) {
	return template.New("base.html").Funcs(funcMap).ParseFS(fs,
		"web/templates/base.html",
		"web/templates/nav.html",
		"web/templates/"+page,
	)
}

// PrecompileTemplates compila todas as páginas de web/templates no boot e as
// cacheia. Um template inválido retorna erro aqui (o caller deve abortar o
// start). base.html e nav.html são apenas includes — nunca renderizados sós.
func PrecompileTemplates(fs embed.FS) error {
	entries, err := fs.ReadDir("web/templates")
	if err != nil {
		return err
	}
	pageTmplMu.Lock()
	defer pageTmplMu.Unlock()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".html") || name == "base.html" || name == "nav.html" {
			continue
		}
		t, err := parsePage(fs, name)
		if err != nil {
			return fmt.Errorf("template %s: %w", name, err)
		}
		pageTmpls[name] = t
	}
	return nil
}

// pageTemplate devolve o template cacheado; se ausente (não pré-compilado, ex.
// em testes), compila sob demanda e cacheia — fallback seguro.
func pageTemplate(fs embed.FS, page string) (*template.Template, error) {
	pageTmplMu.RLock()
	t, ok := pageTmpls[page]
	pageTmplMu.RUnlock()
	if ok {
		return t, nil
	}
	t, err := parsePage(fs, page)
	if err != nil {
		return nil, err
	}
	pageTmplMu.Lock()
	pageTmpls[page] = t
	pageTmplMu.Unlock()
	return t, nil
}

// RenderPage renderiza uma página (base.html) com os dados. Ponto único usado
// por todos os handlers de página (quiz, lab, tutor, tools, docs, argocd, ...).
func RenderPage(w http.ResponseWriter, fs embed.FS, page string, data any) {
	t, err := pageTemplate(fs, page)
	if err != nil {
		http.Error(w, "erro ao carregar template: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, "base.html", data); err != nil {
		http.Error(w, "erro ao renderizar: "+err.Error(), http.StatusInternalServerError)
	}
}
