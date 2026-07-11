package handlers

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"estudo-app/internal/models"
)

// ─────────────────────────────────────────────────────────────────────────────
// Image prewarm — puxa as imagens dos labs para o nó em background, para que
// os pods dos exercícios fiquem Ready em segundos (crítico no AKS, onde o
// primeiro pull de imagens grandes como perl:5 leva minutos).
// Os pods de prewarm rodam `true` e completam na hora; ficam no ns lab-system,
// que é excluído do cluster reset.
// ─────────────────────────────────────────────────────────────────────────────

var (
	imageFlagRe = regexp.MustCompile(`--image=([a-zA-Z0-9][a-zA-Z0-9./:_-]*)`)
	imageYamlRe = regexp.MustCompile(`image:\s*([a-zA-Z0-9][a-zA-Z0-9./:_-]*)`)
	nonAlnumRe  = regexp.MustCompile(`[^a-z0-9]+`)

	prewarmMu   sync.Mutex
	prewarmLast = map[string]time.Time{} // por contexto kubectl
)

// imagens de exemplo/quebradas de propósito nos enunciados — não puxar.
func isPlaceholderImage(img string) bool {
	l := strings.ToLower(img)
	return strings.Contains(l, "invalid") ||
		strings.HasPrefix(l, "new-image") ||
		strings.HasPrefix(l, "registry/") ||
		strings.HasPrefix(l, "image:")
}

// extractLabImages varre as questões e devolve as imagens reais usadas.
func extractLabImages(qs []models.Question) []string {
	seen := map[string]bool{}
	scan := func(s string) {
		for _, m := range imageFlagRe.FindAllStringSubmatch(s, -1) {
			seen[m[1]] = true
		}
		for _, m := range imageYamlRe.FindAllStringSubmatch(s, -1) {
			seen[m[1]] = true
		}
	}
	for _, q := range qs {
		scan(q.AnswerCommand)
		for _, st := range q.Setup {
			scan(st.Command)
		}
	}
	var out []string
	for img := range seen {
		if !isPlaceholderImage(img) {
			out = append(out, img)
		}
	}
	sort.Strings(out)
	return out
}

// PrewarmLabImages dispara o pré-aquecimento (no máx. 1x/30min por contexto).
// Não bloqueia: roda em goroutine própria.
func PrewarmLabImages(qs []models.Question) {
	if os.Getenv("LAB_NO_CLUSTER") != "" {
		return
	}
	ctxName := currentContext()
	if ctxName == "" {
		return
	}
	images := extractLabImages(qs)
	if len(images) == 0 {
		return
	}
	cacheKey := ctxName + "|" + strings.Join(images, ",")
	prewarmMu.Lock()
	if t, ok := prewarmLast[cacheKey]; ok && time.Since(t) < 30*time.Minute {
		prewarmMu.Unlock()
		return
	}
	prewarmLast[cacheKey] = time.Now()
	prewarmMu.Unlock()
	savePrewarmStatus(PrewarmStatus{Context: ctxName, State: "preparing", Images: images, StartedAt: time.Now().UTC().Format(time.RFC3339)})

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		var sb strings.Builder
		// ns compartilhado com o cloud shell; limpa prewarms antigos já completados
		sb.WriteString("set -e; kubectl create namespace " + cloudShellSystemNS + " 2>/dev/null || true; ")
		sb.WriteString("kubectl -n " + cloudShellSystemNS + " delete pod -l prewarm=1 --ignore-not-found --wait=true >/dev/null 2>&1; ")
		for _, img := range images {
			name := "prewarm-" + strings.Trim(nonAlnumRe.ReplaceAllString(strings.ToLower(img), "-"), "-")
			if len(name) > 60 {
				name = name[:60]
			}
			sb.WriteString(fmt.Sprintf(
				"kubectl -n %s run %s --image=%s --restart=Never --labels=prewarm=1 --command -- true 2>/dev/null; ",
				cloudShellSystemNS, name, img))
		}
		sb.WriteString("kubectl -n " + cloudShellSystemNS + " wait --for=jsonpath='{.status.phase}'=Succeeded pod -l prewarm=1 --timeout=12m >/dev/null; ")
		sb.WriteString("kubectl -n " + cloudShellSystemNS + " get pod -l prewarm=1 -o jsonpath='{range .items[*]}{.status.containerStatuses[0].imageID}{\"\\n\"}{end}'; ")
		output, err := wslShellCtx(ctx, sb.String()).CombinedOutput()
		if err != nil {
			savePrewarmStatus(PrewarmStatus{Context: ctxName, State: "degraded", Images: images, StartedAt: time.Now().UTC().Format(time.RFC3339), Failure: err.Error()})
			log.Printf("[prewarm] falhou (contexto %s): %v", ctxName, err)
			return
		}
		var resolved []string
		for _, line := range strings.Split(string(output), "\n") {
			if id := strings.TrimSpace(line); id != "" {
				resolved = append(resolved, id)
			}
		}
		savePrewarmStatus(PrewarmStatus{Context: ctxName, State: "ready", Images: images, Resolved: resolved, ReadyAt: time.Now().UTC().Format(time.RFC3339)})
		log.Printf("[prewarm] %d imagens sendo aquecidas no contexto %s", len(images), ctxName)
	}()
}
