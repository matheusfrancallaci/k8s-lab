package tutor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

type cachedDocument struct {
	content, etag string
	expires       time.Time
}

var documentCache = struct {
	sync.Mutex
	entries map[string]cachedDocument
}{entries: map[string]cachedDocument{}}

func documentCacheTTL() time.Duration {
	if n, err := time.ParseDuration(envOr("TUTOR_DOC_CACHE_TTL", "30m")); err == nil && n > 0 {
		return n
	}
	return 30 * time.Minute
}

func fetchTrustedDocument(u, accept string) (string, error) {
	started := time.Now()
	failed := true
	defer func() { recordTutorLatency("docs.fetch", time.Since(started), 0, failed) }()
	documentCache.Lock()
	cached, found := documentCache.entries[u]
	documentCache.Unlock()
	if found && time.Now().Before(cached.expires) {
		failed = false
		recordTutorLatency("docs.cache_hit", 0, 0, false)
		return cached.content, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "k8s-study-lab-tutor/1.0")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if found && cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}
	resp, err := sharedLLMHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && found {
		cached.expires = time.Now().Add(documentCacheTTL())
		documentCache.Lock()
		documentCache.entries[u] = cached
		documentCache.Unlock()
		failed = false
		return cached.content, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 800*1024))
	if err != nil {
		return "", err
	}
	content := string(body)
	documentCache.Lock()
	documentCache.entries[u] = cachedDocument{content: content, etag: resp.Header.Get("ETag"), expires: time.Now().Add(documentCacheTTL())}
	documentCache.Unlock()
	failed = false
	return content, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Busca de fontes — o tutor lê sozinho: URLs coladas (kubernetes.io, GitHub)
// ou, sem URL, localiza a página certa na documentação oficial pelo tema.
// Custo zero: só HTTP GET em conteúdo público.
// ─────────────────────────────────────────────────────────────────────────────

var (
	urlRe    = regexp.MustCompile(`https?://[^\s<>"')]+`)
	ghRepoRe = regexp.MustCompile(`^https?://github\.com/([\w.-]+)/([\w.-]+)/?$`)
	ghBlobRe = regexp.MustCompile(`^https?://github\.com/([\w.-]+)/([\w.-]+)/blob/(.+)$`)

	preRe    = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`)
	scriptRe = regexp.MustCompile(`(?is)<(script|style|nav|header|footer|aside)[^>]*>.*?</(script|style|nav|header|footer|aside)>`)
	mainRe   = regexp.MustCompile(`(?is)<main[^>]*>(.*?)</main>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]+>`)
	blankRe  = regexp.MustCompile(`\n{3,}`)
)

// docsIndex: tema → página oficial. Usado quando o usuário só descreve o
// assunto ("crie questões de init containers") sem colar URL nem material.
var docsIndex = []struct {
	keys []string
	url  string
}{
	{[]string{"init container"}, "https://kubernetes.io/docs/concepts/workloads/pods/init-containers/"},
	{[]string{"rolling", "rollout", "deployment"}, "https://kubernetes.io/docs/concepts/workloads/controllers/deployment/"},
	{[]string{"replicaset", "replica set", "scaleset", "scale set"}, "https://kubernetes.io/docs/concepts/workloads/controllers/replicaset/"},
	{[]string{"pod "}, "https://kubernetes.io/docs/concepts/workloads/pods/"},
	{[]string{"service", "clusterip", "nodeport"}, "https://kubernetes.io/docs/concepts/services-networking/service/"},
	{[]string{"ingress"}, "https://kubernetes.io/docs/concepts/services-networking/ingress/"},
	{[]string{"configmap"}, "https://kubernetes.io/docs/concepts/configuration/configmap/"},
	{[]string{"secret"}, "https://kubernetes.io/docs/concepts/configuration/secret/"},
	{[]string{"persistent", "storage", "pvc", " pv "}, "https://kubernetes.io/docs/concepts/storage/persistent-volumes/"},
	{[]string{"statefulset"}, "https://kubernetes.io/docs/concepts/workloads/controllers/statefulset/"},
	{[]string{"daemonset"}, "https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/"},
	{[]string{"cronjob"}, "https://kubernetes.io/docs/concepts/workloads/controllers/cron-jobs/"},
	{[]string{"job"}, "https://kubernetes.io/docs/concepts/workloads/controllers/job/"},
	{[]string{"rbac", "role", "clusterrole"}, "https://kubernetes.io/docs/reference/access-authn-authz/rbac/"},
	{[]string{"network policy", "networkpolicy"}, "https://kubernetes.io/docs/concepts/services-networking/network-policies/"},
	{[]string{"taint", "toleration"}, "https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/"},
	{[]string{"affinity", "nodeselector", "scheduling"}, "https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/"},
	{[]string{"probe", "liveness", "readiness"}, "https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/"},
	{[]string{"namespace"}, "https://kubernetes.io/docs/concepts/overview/working-with-objects/namespaces/"},
	{[]string{"quota"}, "https://kubernetes.io/docs/concepts/policy/resource-quotas/"},
	{[]string{"autoscal", "hpa"}, "https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/"},
	{[]string{"drain", "cordon", "manutenção"}, "https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/"},
	{[]string{"etcd", "backup"}, "https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/"},
	{[]string{"upgrade", "kubeadm"}, "https://kubernetes.io/docs/tasks/administer-cluster/kubeadm/kubeadm-upgrade/"},
	{[]string{"troubleshoot", "debug"}, "https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/"},
	{[]string{"dns"}, "https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/"},
	{[]string{"serviceaccount", "service account"}, "https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/"},
	{[]string{"security context", "securitycontext"}, "https://kubernetes.io/docs/tasks/configure-pod-container/security-context/"},
	{[]string{"limitrange", "limit range"}, "https://kubernetes.io/docs/concepts/policy/limit-range/"},
	{[]string{"volume"}, "https://kubernetes.io/docs/concepts/storage/volumes/"},
	{[]string{"kustomize"}, "https://kubernetes.io/docs/tasks/manage-kubernetes-objects/kustomization/"},
	{[]string{"aks", "azure kubernetes service", "az-104", "azure administrator"}, "https://learn.microsoft.com/azure/aks/what-is-aks"},
	{[]string{"aks network", "azure cni"}, "https://learn.microsoft.com/azure/aks/concepts-network"},
	{[]string{"aks storage", "azure disk", "azure file"}, "https://learn.microsoft.com/azure/aks/concepts-storage"},
	{[]string{"aks security", "az-500"}, "https://learn.microsoft.com/azure/aks/concepts-security"},
	{[]string{"aws", "amazon web services", "cloud practitioner", "clf-c02"}, "https://docs.aws.amazon.com/whitepapers/latest/aws-overview/introduction.html"},
	{[]string{"ec2", "compute", "instancia", "instance"}, "https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/concepts.html"},
	{[]string{"vpc", "subnet", "security group"}, "https://docs.aws.amazon.com/vpc/latest/userguide/what-is-amazon-vpc.html"},
	{[]string{"iam", "identity", "policy"}, "https://docs.aws.amazon.com/IAM/latest/UserGuide/introduction.html"},
	{[]string{"s3", "bucket", "object storage"}, "https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html"},
	{[]string{"sqs", "queue", "fila", "messaging"}, "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/welcome.html"},
	{[]string{"eks", "elastic kubernetes service"}, "https://docs.aws.amazon.com/eks/latest/userguide/what-is-eks.html"},
	{[]string{"linux", "chmod", "permissions", "permissao", "permissão", "grep", "awk", "sed"}, "https://man7.org/linux/man-pages/"},
	{[]string{"bash", "shell script", "script bash"}, "https://www.gnu.org/software/bash/manual/bash.html"},
	{[]string{"java", "javac", "jvm"}, "https://docs.oracle.com/en/java/"},
	{[]string{"helm", "chart", "values.yaml"}, "https://helm.sh/docs/topics/charts/"},
	{[]string{"docker", "containerfile", "dockerfile", "container image"}, "https://docs.docker.com/get-started/docker-concepts/the-basics/what-is-a-container/"},
	{[]string{"terraform", "hcl", "provider", "state", "module"}, "https://developer.hashicorp.com/terraform/language"},
	{[]string{"prometheus", "promql", "alertmanager"}, "https://prometheus.io/docs/introduction/overview/"},
	{[]string{"grafana", "dashboard", "datasource", "data source"}, "https://grafana.com/docs/grafana/latest/introduction/"},
	{[]string{"cilium", "ebpf", "cni", "hubble"}, "https://docs.cilium.io/en/stable/overview/intro/"},
}

// ─────────────────────────────────────────────────────────────────────────────
// Consciência de certificação — cada cert tem seu domínio de palavras-chave e
// suas fontes oficiais. Se o material do usuário não cobre a cert pedida, o
// tutor busca sozinho nas fontes confiáveis (kubernetes.io = a própria doc).
// ─────────────────────────────────────────────────────────────────────────────

var certKeywords = map[string][]string{
	"CKS": {"security", "segurança", "networkpolicy", "network policy", "rbac", "secret", "seccomp",
		"apparmor", "falco", "audit", "admission", "pod security", "securitycontext", "runasnonroot",
		"trivy", "kube-bench", "tls", "certificate", "encrypt", "hardening", "sandbox", "gvisor"},
	"CKA": {"etcd", "kubeadm", "node", "drain", "cordon", "upgrade", "scheduler", "taint", "kubelet",
		"backup", "restore", "cluster", "control plane", "pv", "persistent", "dns", "kube-proxy"},
	"CKAD": {"probe", "liveness", "readiness", "configmap", "job", "cronjob", "multi-container",
		"sidecar", "init container", "deployment", "rollout", "canary", "blue-green", "helm"},
	"ArgoCD": {"argocd", "gitops", "sync", "application", "app of apps", "kustomize", "helm", "rollback"},
	"CAPA":   {"argocd", "argo project", "gitops", "sync", "application", "rollout", "helm", "kustomize"},
	"AWS":    {"aws", "amazon web services", "ec2", "vpc", "iam", "s3", "eks", "cloud practitioner", "region", "availability zone", "security group"},
	"AZ-104": {"aks", "azure kubernetes service", "node pool", "azure cni", "load balancer", "managed identity", "storage", "monitor"},
	"AZ-204": {"aks", "container apps", "deployment", "configmap", "secret", "ingress", "observability", "managed identity"},
	"AZ-400": {"aks", "devops", "deployment", "gitops", "helm", "pipeline", "rollout", "observability"},
	"AZ-500": {"aks", "security", "rbac", "network policy", "pod security", "secret", "managed identity", "private cluster"},
	"Linux":  {"linux", "chmod", "permiss", "grep", "awk", "sed", "logs", "shell", "filesystem"},
	"Bash":   {"bash", "shell script", "argument", "csv", "stdout", "stderr", "exit code"},
	"Java":   {"java", "javac", "jvm", "class", "main", "string args", "compile"},
	"Helm":   {"helm", "chart", "values", "template", "release"},
	"Docker": {"docker", "container", "image", "dockerfile", "registry"},
}

// fontes oficiais curadas por certificação (kubernetes.io + projetos oficiais)
var certSources = map[string][]string{
	"CKS": {
		"https://kubernetes.io/docs/concepts/security/pod-security-standards/",
		"https://kubernetes.io/docs/concepts/services-networking/network-policies/",
		"https://kubernetes.io/docs/tasks/configure-pod-container/security-context/",
		"https://kubernetes.io/docs/reference/access-authn-authz/rbac/",
		"https://kubernetes.io/docs/concepts/configuration/secret/",
	},
	"CKA": {
		"https://kubernetes.io/docs/concepts/workloads/controllers/deployment/",
		"https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/",
		"https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/",
		"https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/",
		"https://kubernetes.io/docs/concepts/storage/persistent-volumes/",
	},
	"CKAD": {
		"https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/",
		"https://kubernetes.io/docs/concepts/workloads/pods/init-containers/",
		"https://kubernetes.io/docs/concepts/configuration/configmap/",
		"https://kubernetes.io/docs/concepts/workloads/controllers/job/",
	},
	"ArgoCD": {
		"https://argo-cd.readthedocs.io/en/stable/getting_started/",
		"https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/",
	},
	"CAPA": {
		"https://argo-cd.readthedocs.io/en/stable/getting_started/",
		"https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/",
		"https://argoproj.github.io/rollouts/",
	},
	"AWS": {
		"https://docs.aws.amazon.com/whitepapers/latest/aws-overview/introduction.html",
		"https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/concepts.html",
		"https://docs.aws.amazon.com/vpc/latest/userguide/what-is-amazon-vpc.html",
		"https://docs.aws.amazon.com/IAM/latest/UserGuide/introduction.html",
		"https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html",
		"https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/welcome.html",
		"https://docs.aws.amazon.com/eks/latest/userguide/what-is-eks.html",
	},
	"AZ-104": {
		"https://learn.microsoft.com/azure/aks/what-is-aks",
		"https://learn.microsoft.com/azure/aks/concepts-network",
		"https://learn.microsoft.com/azure/aks/concepts-storage",
	},
	"AZ-204": {
		"https://learn.microsoft.com/azure/aks/concepts-clusters-workloads",
		"https://learn.microsoft.com/azure/aks/concepts-security",
	},
	"AZ-400": {
		"https://learn.microsoft.com/azure/aks/cluster-configuration",
		"https://learn.microsoft.com/azure/aks/concepts-clusters-workloads",
	},
	"AZ-500": {
		"https://learn.microsoft.com/azure/aks/concepts-security",
		"https://learn.microsoft.com/azure/aks/operator-best-practices-network",
	},
}

// certRelevance conta quantas palavras do domínio da cert aparecem no texto.
func certRelevance(text, cert string) int {
	lower := strings.ToLower(text)
	hits := 0
	for _, k := range certKeywords[cert] {
		if strings.Contains(lower, k) {
			hits++
		}
	}
	return hits
}

// shouldComplement decide se o material está desalinhado da cert pedida:
// ou cobre pouco do domínio em absoluto, ou OUTRA cert domina o conteúdo
// (ex.: página de Pods pontua alto em CKA/CKAD mas o usuário quer CKS).
func shouldComplement(text, cert string) bool {
	own := certRelevance(text, cert)
	if own < 4 {
		return true
	}
	for other := range certKeywords {
		if other == cert {
			continue
		}
		if certRelevance(text, other) > own {
			return true
		}
	}
	return false
}

// FetchCertSources baixa até `max` fontes oficiais da certificação.
func FetchCertSources(cert string, max int) (string, []string) {
	var parts, sources []string
	for _, u := range certSources[cert] {
		if len(sources) >= max {
			break
		}
		if content, err := fetchURL(u); err == nil && len(content) > 100 {
			parts = append(parts, markSource(u, content))
			sources = append(sources, u)
		}
	}
	return strings.Join(parts, ""), sources
}

// EnrichSource resolve as fontes do texto do usuário:
//   - URLs coladas → baixa e converte (GitHub README, páginas de doc, markdown)
//   - sem URL e texto curto → localiza páginas oficiais pelo tema
//
// Retorna o texto enriquecido + a lista de fontes lidas.
func EnrichSource(text string) (string, []string, []string) {
	var sources, blocked, fetched []string

	urls := urlRe.FindAllString(text, -1)
	if len(urls) > 3 {
		urls = urls[:3]
	}
	for i, u := range urls {
		if !isTrustedURL(u) {
			blocked = append(blocked, u)
			continue
		}
		// A 1ª URL é percorrida em profundidade: o tutor segue links da MESMA
		// seção da doc e lê várias páginas (não só a que foi colada). As demais
		// URLs coladas entram como página única para não explodir a latência.
		if i == 0 {
			if txt, srcs := crawlDoc(u, crawlPages()); len(srcs) > 0 {
				fetched = append(fetched, txt)
				sources = append(sources, srcs...)
				continue
			}
		}
		if content, err := fetchURL(u); err == nil && len(content) > 100 {
			fetched = append(fetched, markSource(u, content))
			sources = append(sources, u)
		}
	}

	// Sem URL e sem material substancial: busca na doc oficial pelo tema
	if len(sources) == 0 && len(text) < 600 {
		lower := strings.ToLower(text)
		for _, d := range docsIndex {
			if len(sources) >= 2 {
				break
			}
			for _, k := range d.keys {
				if strings.Contains(lower, k) {
					if content, err := fetchURL(d.url); err == nil && len(content) > 100 {
						fetched = append(fetched, markSource(d.url, content))
						sources = append(sources, d.url)
					}
					break
				}
			}
		}
	}

	if len(fetched) == 0 {
		return text, nil, blocked
	}
	enriched := text + "\n\n" + strings.Join(fetched, "\n\n---\n\n")
	if len(enriched) > 30000 {
		enriched = enriched[:30000]
	}
	return enriched, sources, blocked
}

// ─────────────────────────────────────────────────────────────────────────────
// Segurança: o tutor só lê fontes confiáveis de infra, cloud e programação.
// Qualquer URL fora da allowlist é recusada (sem surpresas, sem conteúdo lixo).
// ─────────────────────────────────────────────────────────────────────────────

var trustedDomains = []string{
	// Kubernetes & CNCF
	"kubernetes.io", "cncf.io", "training.linuxfoundation.org", "linuxfoundation.org", "docs.linuxfoundation.org", "etcd.io", "helm.sh", "istio.io",
	"prometheus.io", "grafana.com", "argo-cd.readthedocs.io", "argoproj.github.io",
	"cilium.io", "falco.org", "containerd.io",
	// Código e docs de projetos
	"github.com", "raw.githubusercontent.com", "api.github.com", "gitlab.com",
	// Clouds
	"learn.microsoft.com", "docs.microsoft.com", "azure.microsoft.com",
	"docs.aws.amazon.com", "aws.amazon.com", "eksctl.io",
	"cloud.google.com",
	"docs.oracle.com", "docs.digitalocean.com",
	"docs.openshift.com", "access.redhat.com", "developers.redhat.com",
	// Containers & IaC
	"docs.docker.com", "docker.com", "developer.hashicorp.com", "terraform.io",
	"docs.ansible.com",
	// Linguagens / dev
	"go.dev", "golang.org", "docs.python.org", "python.org", "nodejs.org",
	"developer.mozilla.org", "git-scm.com", "linux.org", "man7.org",
	"gnu.org",
}

// isTrustedURL valida o host contra a allowlist (aceita subdomínios).
func isTrustedURL(u string) bool {
	m := regexp.MustCompile(`^https?://([^/]+)`).FindStringSubmatch(u)
	if m == nil {
		return false
	}
	host := strings.ToLower(m[1])
	for _, d := range trustedDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// fetchURL baixa uma URL pública e devolve texto limpo (código preservado).
var hrefRe = regexp.MustCompile(`(?i)<a\s[^>]*href=["']([^"'#\s]+)`)

// crawlPages define quantas páginas o crawler lê por URL semente (env TUTOR_CRAWL_PAGES).
func crawlPages() int {
	n := 0
	if _, err := fmt.Sscanf(envOr("TUTOR_CRAWL_PAGES", "5"), "%d", &n); err == nil && n >= 1 && n <= 20 {
		return n
	}
	return 5
}

// crawlDoc lê a página semente e SEGUE até maxPages-1 links da mesma seção da
// documentação (mesmo host, sob o mesmo diretório de caminho), concatenando o
// texto de cada página com marcador de fonte. É isto que faz o tutor "percorrer
// a documentação" em vez de olhar uma página só. GitHub cai no fetch único
// (fetchURL já trata repo/blob de forma especial).
func crawlDoc(seed string, maxPages int) (string, []string) {
	if maxPages < 1 {
		maxPages = 1
	}
	if !isTrustedURL(seed) || ghRepoRe.MatchString(seed) || ghBlobRe.MatchString(seed) {
		if c, err := fetchURL(seed); err == nil && len(c) > 100 {
			return markSource(seed, c), []string{seed}
		}
		return "", nil
	}
	base, err := url.Parse(seed)
	if err != nil {
		return "", nil
	}
	raw, err := fetchRawHTML(seed)
	if err != nil {
		if c, e := fetchURL(seed); e == nil && len(c) > 100 {
			return markSource(seed, c), []string{seed}
		}
		return "", nil
	}

	var out strings.Builder
	var sources []string
	seen := map[string]bool{seed: true}
	out.WriteString(markSource(seed, htmlToText(raw)))
	sources = append(sources, seed)

	// Só segue links irmãos: mesmo host e sob o mesmo diretório-pai da semente
	// (a "seção" da doc). TrimSuffix tira a barra final, senão path.Dir devolve a
	// própria página e nenhum irmão casa.
	dir := path.Dir(strings.TrimSuffix(base.Path, "/"))
	if dir == "/" || dir == "." {
		dir = strings.TrimSuffix(base.Path, "/")
	}
	prefix := strings.TrimSuffix(dir, "/") + "/"
	for _, m := range hrefRe.FindAllStringSubmatch(raw, -1) {
		if len(sources) >= maxPages {
			break
		}
		ref, err := base.Parse(m[1])
		if err != nil {
			continue
		}
		ref.Fragment, ref.RawQuery = "", ""
		link := ref.String()
		if seen[link] || ref.Host != base.Host || !strings.HasPrefix(ref.Path, prefix) {
			continue
		}
		seen[link] = true
		if c, e := fetchURL(link); e == nil && len(c) > 100 {
			out.WriteString("\n\n---\n\n" + markSource(link, c))
			sources = append(sources, link)
		}
	}
	return out.String(), sources
}

// fetchRawHTML devolve o HTML cru de uma página confiável (sem virar texto) —
// usado pelo crawler para extrair os links antes de limpar as tags.
func fetchRawHTML(u string) (string, error) {
	if !isTrustedURL(u) {
		return "", fmt.Errorf("fora do escopo")
	}
	return fetchTrustedDocument(u, "")
}

func fetchURL(u string) (string, error) {
	if !isTrustedURL(u) {
		return "", fmt.Errorf("fora do escopo do tutor (apenas fontes de infra, cloud e programação)")
	}
	accept := ""
	// Repo GitHub → README via API (Accept raw evita base64)
	if m := ghRepoRe.FindStringSubmatch(u); m != nil {
		u = fmt.Sprintf("https://api.github.com/repos/%s/%s/readme", m[1], m[2])
		accept = "application/vnd.github.raw"
	}
	// Arquivo no GitHub → conteúdo raw
	if m := ghBlobRe.FindStringSubmatch(u); m != nil {
		u = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s", m[1], m[2], m[3])
	}

	content, err := fetchTrustedDocument(u, accept)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(strings.TrimSpace(content), "<") {
		content = htmlToText(content)
	}
	return content, nil
}

// htmlToText extrai o conteúdo útil de uma página HTML, preservando blocos
// <pre> como código cercado (```), para os extratores de comandos/manifests.
func htmlToText(html string) string {
	// foca no conteúdo principal quando existe (kubernetes.io usa <main>)
	if m := mainRe.FindStringSubmatch(html); m != nil {
		html = m[1]
	}
	html = scriptRe.ReplaceAllString(html, " ")

	// preserva blocos de código como fences
	html = preRe.ReplaceAllStringFunc(html, func(block string) string {
		inner := preRe.FindStringSubmatch(block)[1]
		inner = tagRe.ReplaceAllString(inner, "")
		return "\n```\n" + decodeEntities(inner) + "\n```\n"
	})

	// quebras estruturais → newline
	for _, t := range []string{"</p>", "</div>", "</li>", "</h1>", "</h2>", "</h3>", "</h4>", "</tr>", "<br>", "<br/>", "<br />"} {
		html = strings.ReplaceAll(html, t, t+"\n")
	}
	html = tagRe.ReplaceAllString(html, "")
	html = decodeEntities(html)

	// limpa linhas em branco excessivas e espaços
	var lines []string
	for _, l := range strings.Split(html, "\n") {
		lines = append(lines, strings.TrimRight(l, " \t"))
	}
	out := blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return strings.TrimSpace(out)
}

func decodeEntities(s string) string {
	r := strings.NewReplacer(
		"&lt;", "<", "&gt;", ">", "&amp;", "&", "&quot;", `"`,
		"&#39;", "'", "&#34;", `"`, "&nbsp;", " ", "&mdash;", "—", "&hellip;", "...",
	)
	return r.Replace(s)
}

// ─────────────────────────────────────────────────────────────────────────────
// Rastreio de fonte + citações — cada trecho extraído sabe DE ONDE veio
// (URL + linha exata), para as questões citarem a documentação com precisão.
// ─────────────────────────────────────────────────────────────────────────────

const srcMarker = "@@FONTE:"

func markSource(url, content string) string {
	return "\n\n" + srcMarker + url + "@@\n" + content
}

// sourceAt devolve a fonte do trecho no offset (última marca antes dele).
func sourceAt(text string, off int) string {
	if off > len(text) {
		off = len(text)
	}
	i := strings.LastIndex(text[:off], srcMarker)
	if i < 0 {
		return ""
	}
	rest := text[i+len(srcMarker):]
	if j := strings.Index(rest, "@@"); j >= 0 {
		return rest[:j]
	}
	return ""
}

// lineAt devolve a linha completa que contém o offset (a citação exata).
func lineAt(text string, off int) string {
	if off >= len(text) {
		return ""
	}
	start := strings.LastIndexByte(text[:off], '\n') + 1
	end := strings.IndexByte(text[off:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += off
	}
	line := strings.TrimSpace(text[start:end])
	if len(line) > 180 {
		line = line[:177] + "..."
	}
	return line
}

// ─────────────────────────────────────────────────────────────────────────────
// Fallback de tópicos — material que só LISTA temas (ex.: README de estudo
// sem manifests): extraímos os títulos/bullets e buscamos cada tema na
// documentação OFICIAL (kubernetes.io apenas, via docsIndex).
// ─────────────────────────────────────────────────────────────────────────────

var topicLineRe = regexp.MustCompile(`(?m)^\s*(?:#{1,4}|[-*•]|\d+[.)])\s+(.{3,90})$`)

// topicDocURLs varre títulos/bullets do material e devolve as páginas
// oficiais correspondentes (dedupe, no máx. `max`).
func topicDocURLs(text string, max int) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range topicLineRe.FindAllStringSubmatch(text, -1) {
		line := strings.ToLower(m[1])
		for _, d := range docsIndex {
			if seen[d.url] || len(out) >= max {
				continue
			}
			for _, k := range d.keys {
				if strings.Contains(line, k) {
					seen[d.url] = true
					out = append(out, d.url)
					break
				}
			}
		}
		if len(out) >= max {
			break
		}
	}
	return out
}

// ─────────────────────────────────────────────────────────────────────────────
// Currículos oficiais embutidos — domínios e pesos das provas (públicos e
// estáveis) + páginas oficiais de cada domínio. "Montar currículo" busca
// tudo sozinho e constrói o pacote inicial da certificação.
// ─────────────────────────────────────────────────────────────────────────────

type CurriculumDomain struct {
	Domain string
	Weight int
	URLs   []string
}

var certCurricula = map[string][]CurriculumDomain{
	"CKA": {
		{"Troubleshooting", 30, []string{"https://kubernetes.io/docs/tasks/debug/debug-application/debug-pods/", "https://kubernetes.io/docs/tasks/debug/debug-cluster/", "https://kubernetes.io/docs/tasks/debug/debug-application/debug-service/"}},
		{"Cluster Architecture, Installation & Configuration", 25, []string{"https://kubernetes.io/docs/reference/access-authn-authz/rbac/", "https://kubernetes.io/docs/tasks/administer-cluster/kubeadm/kubeadm-upgrade/", "https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/", "https://kubernetes.io/docs/concepts/extend-kubernetes/api-extension/custom-resources/"}},
		{"Services & Networking", 20, []string{"https://kubernetes.io/docs/concepts/services-networking/service/", "https://kubernetes.io/docs/concepts/services-networking/network-policies/", "https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/", "https://kubernetes.io/docs/concepts/services-networking/ingress/"}},
		{"Workloads & Scheduling", 15, []string{"https://kubernetes.io/docs/concepts/workloads/controllers/deployment/", "https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/", "https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/", "https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/"}},
		{"Storage", 10, []string{"https://kubernetes.io/docs/concepts/storage/persistent-volumes/"}},
	},
	"CKAD": {
		{"Application Design and Build", 20, []string{"https://kubernetes.io/docs/concepts/workloads/pods/init-containers/", "https://kubernetes.io/docs/concepts/workloads/controllers/job/"}},
		{"Application Deployment", 20, []string{"https://kubernetes.io/docs/concepts/workloads/controllers/deployment/"}},
		{"Application Observability and Maintenance", 15, []string{"https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/"}},
		{"Application Environment, Configuration and Security", 25, []string{"https://kubernetes.io/docs/concepts/configuration/configmap/", "https://kubernetes.io/docs/tasks/configure-pod-container/security-context/"}},
		{"Services and Networking", 20, []string{"https://kubernetes.io/docs/concepts/services-networking/service/"}},
	},
	"CKS": {
		{"Cluster Setup", 15, []string{"https://kubernetes.io/docs/concepts/services-networking/network-policies/"}},
		{"Cluster Hardening", 15, []string{"https://kubernetes.io/docs/reference/access-authn-authz/rbac/"}},
		{"System Hardening", 10, []string{"https://kubernetes.io/docs/tasks/configure-pod-container/security-context/"}},
		{"Minimize Microservice Vulnerabilities", 20, []string{"https://kubernetes.io/docs/concepts/security/pod-security-standards/", "https://kubernetes.io/docs/concepts/configuration/secret/"}},
		{"Supply Chain Security", 20, []string{"https://kubernetes.io/docs/concepts/containers/images/"}},
		{"Monitoring, Logging and Runtime Security", 20, []string{"https://kubernetes.io/docs/tasks/debug/debug-cluster/audit/"}},
	},
	// KCNA — Kubernetes and Cloud Native Associate (CNCF). Pesos oficiais do
	// exame; era a cert que o chat recusava por "não existir" (bug real).
	"KCNA": {
		{"Kubernetes Fundamentals", 46, []string{"https://kubernetes.io/docs/concepts/overview/", "https://kubernetes.io/docs/concepts/overview/components/", "https://kubernetes.io/docs/concepts/workloads/pods/"}},
		{"Container Orchestration", 22, []string{"https://kubernetes.io/docs/concepts/architecture/", "https://kubernetes.io/docs/concepts/services-networking/service/", "https://kubernetes.io/docs/concepts/scheduling-eviction/kube-scheduler/"}},
		{"Cloud Native Architecture", 16, []string{"https://www.cncf.io/projects/", "https://kubernetes.io/docs/concepts/architecture/nodes/"}},
		{"Cloud Native Observability", 8, []string{"https://kubernetes.io/docs/tasks/debug/debug-cluster/resource-metrics-pipeline/", "https://prometheus.io/docs/introduction/overview/"}},
		{"Cloud Native Application Delivery", 8, []string{"https://argo-cd.readthedocs.io/en/stable/", "https://kubernetes.io/docs/concepts/workloads/management/"}},
	},
	"ArgoCD": {
		{"Fundamentos GitOps", 40, []string{"https://argo-cd.readthedocs.io/en/stable/getting_started/"}},
		{"Sync e Rollback", 30, []string{"https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/"}},
	},
	"CAPA": {
		{"Argo CD e GitOps", 40, []string{"https://argo-cd.readthedocs.io/en/stable/getting_started/"}},
		{"Sync, Health e Rollback", 30, []string{"https://argo-cd.readthedocs.io/en/stable/user-guide/sync-options/"}},
		{"Argo Rollouts", 30, []string{"https://argoproj.github.io/rollouts/"}},
	},
	// Ansible — fundamentos + módulos (doc oficial).
	"Ansible": {
		{"Fundamentos & Inventário", 25, []string{"https://docs.ansible.com/ansible/latest/getting_started/index.html", "https://docs.ansible.com/ansible/latest/inventory_guide/intro_inventory.html"}},
		{"Playbooks & Tarefas", 35, []string{"https://docs.ansible.com/ansible/latest/playbook_guide/playbooks_intro.html"}},
		{"Módulos comuns", 25, []string{"https://docs.ansible.com/ansible/latest/collections/ansible/builtin/index.html"}},
		{"Variáveis & Templates", 15, []string{"https://docs.ansible.com/ansible/latest/playbook_guide/playbooks_variables.html"}},
	},
	"AWS": {
		{"Cloud fundamentals", 20, []string{"https://docs.aws.amazon.com/whitepapers/latest/aws-overview/introduction.html"}},
		{"Compute: EC2 e EKS", 25, []string{"https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/concepts.html", "https://docs.aws.amazon.com/eks/latest/userguide/what-is-eks.html"}},
		{"Networking: VPC", 20, []string{"https://docs.aws.amazon.com/vpc/latest/userguide/what-is-amazon-vpc.html"}},
		{"Security: IAM", 20, []string{"https://docs.aws.amazon.com/IAM/latest/UserGuide/introduction.html"}},
		{"Storage: S3", 10, []string{"https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html"}},
		{"Messaging: SQS", 5, []string{"https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/welcome.html"}},
	},
	// Terraform Associate (003) — domínios oficiais + páginas da doc HashiCorp.
	"Terraform": {
		{"IaC & Fluxo Terraform", 20, []string{"https://developer.hashicorp.com/terraform/intro", "https://developer.hashicorp.com/terraform/cli/commands"}},
		{"Linguagem: recursos, variáveis, outputs", 25, []string{"https://developer.hashicorp.com/terraform/language/resources", "https://developer.hashicorp.com/terraform/language/values/variables", "https://developer.hashicorp.com/terraform/language/values/outputs"}},
		{"Estado (state)", 20, []string{"https://developer.hashicorp.com/terraform/language/state", "https://developer.hashicorp.com/terraform/language/backend"}},
		{"Módulos", 15, []string{"https://developer.hashicorp.com/terraform/language/modules"}},
		{"Providers", 20, []string{"https://developer.hashicorp.com/terraform/language/providers/requirements"}},
	},
	"Linux": {
		{"Arquivos e permissoes", 35, []string{"https://man7.org/linux/man-pages/man1/chmod.1.html"}},
		{"Processamento de texto", 35, []string{"https://man7.org/linux/man-pages/man1/grep.1.html", "https://man7.org/linux/man-pages/man1/awk.1p.html"}},
		{"Shell e processos", 30, []string{"https://www.gnu.org/software/bash/manual/bash.html"}},
	},
	"Bash": {
		{"Scripts e parametros", 40, []string{"https://www.gnu.org/software/bash/manual/bash.html"}},
		{"Controle de erro", 30, []string{"https://www.gnu.org/software/bash/manual/bash.html"}},
		{"Texto e arquivos", 30, []string{"https://man7.org/linux/man-pages/man1/awk.1p.html"}},
	},
	"Java": {
		{"Linguagem e main", 40, []string{"https://docs.oracle.com/en/java/"}},
		{"Compilacao e execucao", 30, []string{"https://docs.oracle.com/en/java/javase/"}},
		{"Containers para runtime", 30, []string{"https://docs.docker.com/language/java/"}},
	},
}

// CurriculumFor devolve o currículo da cert: o embutido (núcleo curado) tem
// precedência; sem ele, cai nos APRENDIDOS de material oficial (persistidos —
// ver curriculum_learned.go). É o que torna cert nova primeira classe sem
// editar código.
func CurriculumFor(cert string) ([]CurriculumDomain, bool) {
	for k, v := range certCurricula {
		if strings.EqualFold(k, cert) {
			return v, true
		}
	}
	return learnedCurriculumFor(cert)
}

// FetchCurriculum baixa 1 página oficial por domínio (com marcadores de fonte)
// e devolve o material agregado + fontes + resumo dos domínios.
func FetchCurriculum(cert string, maxPerDomain int) (string, []string, []CurriculumDomain, bool) {
	cur, ok := CurriculumFor(cert)
	if !ok {
		return "", nil, nil, false
	}
	var parts, sources []string
	for _, d := range cur {
		n := 0
		for _, u := range d.URLs {
			if n >= maxPerDomain {
				break
			}
			if content, err := fetchURL(u); err == nil && len(content) > 100 {
				parts = append(parts, markSource(u, content))
				sources = append(sources, u)
				n++
			}
		}
	}
	return strings.Join(parts, "\n"), sources, cur, true
}
