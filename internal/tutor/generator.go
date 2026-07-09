package tutor

import (
	crand "crypto/rand"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"estudo-app/internal/models"

	"gopkg.in/yaml.v3"
)

// ─────────────────────────────────────────────────────────────────────────────
// Gerador de labs — templates paramétricos, 3 níveis de ajuda, zero LLM.
// Cada geração randomiza nomes/imagens/valores → labs sempre inéditos.
// ─────────────────────────────────────────────────────────────────────────────

// params são os valores randomizados injetados num template.
type params struct {
	Name     string
	Name2    string
	Image    string
	Replicas int
	Port     int
	Key      string
	Value    string
	NS       string
}

var (
	adjectives = []string{"orion", "atlas", "nova", "pixel", "lunar", "delta", "ember", "quartz", "vega", "titan"}
	nouns      = []string{"api", "web", "cache", "worker", "gateway", "portal", "batch", "store", "feed", "auth"}
	// imagens já cobertas pelo prewarm — pods ficam Ready rápido
	images  = []string{"nginx:1.21", "nginx:1.25", "redis:7", "busybox:1.35"}
	kvPairs = [][2]string{{"APP_MODE", "production"}, {"LOG_LEVEL", "debug"}, {"REGION", "brazil-south"}, {"TIER", "gold"}}
)

func randParams() params {
	kv := kvPairs[rand.IntN(len(kvPairs))]
	a := adjectives[rand.IntN(len(adjectives))]
	return params{
		Name:     a + "-" + nouns[rand.IntN(len(nouns))],
		Name2:    a + "-" + nouns[rand.IntN(len(nouns))] + "-svc",
		Image:    images[rand.IntN(len(images))],
		Replicas: 2 + rand.IntN(3), // 2-4
		Port:     []int{80, 8080, 5000, 9090}[rand.IntN(4)],
		Key:      kv[0],
		Value:    kv[1],
		NS:       "lab-" + a,
	}
}

func labWorkspace(name string) string {
	return "${TFLAB:-$HOME/tflab/${LAB_USER:-default}}/" + name
}

func newID() string {
	b := make([]byte, 4)
	crand.Read(b) //nolint:errcheck
	return "custom-" + hex.EncodeToString(b)
}

// pickHelp escolhe o texto conforme o nível de ajuda (1=desafio, 3=guiado).
func pickHelp(level int, l1, l2, l3 string) string {
	switch level {
	case 3:
		return l3
	case 2:
		return l2
	default:
		return l1
	}
}

const localStackInstallCommand = "kubectl create namespace tools 2>/dev/null || true; kubectl get deploy localstack -n tools >/dev/null 2>&1 || kubectl create deployment localstack --image=localstack/localstack:latest -n tools 2>/dev/null || true; kubectl set env deployment/localstack -n tools SERVICES=s3,sqs,iam,sts DEBUG=0 2>/dev/null || true; kubectl get svc localstack -n tools >/dev/null 2>&1 || kubectl expose deployment localstack --port=4566 --target-port=4566 -n tools 2>/dev/null || true; kubectl rollout status deployment/localstack -n tools --timeout=180s 2>/dev/null || true; kubectl -n tools exec deploy/localstack -- localstack wait -t 60 2>/dev/null || true"

func localStackSetupSteps() []models.SetupStep {
	return []models.SetupStep{{
		Description: "Instalando/verificando LocalStack para emular AWS no cluster...",
		Command:     localStackInstallCommand,
	}}
}

func diffFor(level int) models.Difficulty {
	switch level {
	case 3:
		return models.Easy
	case 2:
		return models.Mid
	default:
		return models.Hard
	}
}

// templateFn produz uma questão a partir de params + nível de ajuda.
type templateFn func(p params, level int, cert string) models.Question

// Banco de templates por tópico (nomes casam com os tópicos do banco oficial).
var templates = map[string][]templateFn{
	"Core Concepts":      {tplPod, tplNamespaceQuota},
	"Workloads":          {tplDeployment, tplReplicaSet, tplJob, tplRolloutRollback},
	"ReplicaSet":         {tplReplicaSet},
	"Autoscaling":        {tplHPA},
	"Troubleshooting":    {tplIncidentImage, tplIncidentSelector, tplIncidentScaledDown},
	"GitOps":             {tplArgoCDApplication},
	"Linux":              {tplLinuxLogProcessing, tplLinuxPermissions},
	"Bash":               {tplBashCSVReport, tplBashArgs},
	"Java":               {tplJavaFizzBuzz, tplJavaWordCount},
	"Helm":               {tplHelmConfigMap},
	"Docker":             {tplDockerfileStatic},
	"AWS Compute":        {tplAWSCompute},
	"AWS Networking":     {tplAWSNetworking},
	"AWS IAM":            {tplAWSIAM},
	"AWS Storage":        {tplAWSStorage},
	"AWS Messaging":      {tplAWSMessaging},
	"Services":           {tplService, tplDNSLookup},
	"Configuration":      {tplConfigMap},
	"Storage":            {tplPVC},
	"Scheduling":         {tplNodeSelector, tplTaintToleration},
	"Application Design": {tplProbes, tplInitContainer},
	"Security":           {tplServiceAccount, tplNetworkPolicy, tplSecurityContext},
}

// Topics lista os tópicos que o gerador sabe criar.
func Topics() []string {
	out := make([]string, 0, len(templates))
	for t := range templates {
		out = append(out, t)
	}
	return out
}

// tópicos de template mais relevantes por certificação (usado para completar
// pedidos de ingestão quando o material não rende labs suficientes).
var certTemplateTopics = map[string][]string{
	"CKA":    {"Troubleshooting", "Workloads", "Services", "Scheduling", "Storage", "Autoscaling", "Core Concepts"},
	"CKAD":   {"Application Design", "Configuration", "Workloads", "Core Concepts", "Autoscaling"},
	"CKS":    {"Security"},
	"ArgoCD": {"GitOps", "Workloads", "Configuration"},
	"CAPA":   {"GitOps", "Workloads", "Configuration"},
	"AWS":    {"AWS Compute", "AWS Networking", "AWS IAM", "AWS Storage", "AWS Messaging"},
	"Linux":  {"Linux", "Bash"},
	"Bash":   {"Bash", "Linux"},
	"Java":   {"Java"},
	"Helm":   {"Helm", "Configuration"},
	"Docker": {"Docker", "Linux"},
}

// generateQuestions cria labs de template SEM persistir (uso interno).
func generateQuestions(topic, cert string, level, count int) []models.Question {
	fns, ok := templates[topic]
	if !ok || count < 1 {
		return nil
	}
	var qs []models.Question
	order := rand.Perm(len(fns))
	for i := 0; i < count; i++ {
		fn := fns[order[i%len(order)]]
		q := fn(randParams(), level, cert)
		q.ID = newID()
		q.Topic = topic
		q.Cert = models.Cert(cert)
		q.Difficulty = diffFor(level)
		q.Type = models.Lab
		q = FinalizeLab(q, "")
		qs = append(qs, q)
	}
	return qs
}

// GenerateForCert completa `count` labs usando os tópicos do domínio da cert.
func GenerateForCert(cert string, level, count int) []models.Question {
	topics := certTemplateTopics[cert]
	if len(topics) == 0 {
		topics = certTemplateTopics["CKA"]
	}
	var qs []models.Question
	for i := 0; i < count; i++ {
		t := topics[rand.IntN(len(topics))]
		qs = append(qs, generateQuestions(t, cert, level, 1)...)
	}
	return qs
}

// Generate cria `count` labs inéditos do tópico com o nível de ajuda pedido,
// grava em questions-custom/ e devolve as questões prontas para uso.
func Generate(topic, cert string, level, count int) ([]models.Question, error) {
	if _, ok := templates[topic]; !ok {
		return nil, fmt.Errorf("tópico %q não suportado pelo gerador (disponíveis: %v)", topic, Topics())
	}
	if cert == "" {
		cert = "CKA"
	}
	if level < 1 || level > 3 {
		level = 2
	}
	if count < 1 {
		count = 1
	}
	if count > 12 {
		count = 12
	}

	WarmRAG(cert, topic)
	qs := generateQuestions(topic, cert, level, count)
	for i := range qs {
		qs[i].Source = models.SourceGenerated
		q := qs[i]
		if err := LabQualityGate(q); err != nil {
			return nil, err
		}
	}
	if err := verifyGeneratedKubernetesLabs(qs); err != nil {
		return nil, err
	}
	for i := range qs {
		if qs[i].LabSpec != nil && isKubernetesLab(qs[i]) && shouldVerifyGeneratedKubernetesLabs() {
			qs[i].LabSpec.ValidationMode = "compiled+runtime-verified"
		}
	}
	if err := persist(qs); err != nil {
		return nil, err
	}
	return qs, nil
}

// persist grava as questões geradas como YAML em questions-custom/.
func persist(qs []models.Question) error {
	if err := os.MkdirAll("questions-custom", 0o755); err != nil {
		return err
	}
	qs = FinalizeLabs(qs, "")
	// Tudo que passa por persist foi gerado — o YAML carrega a proveniência
	// para o selo sobreviver a restart e à inspeção manual do arquivo.
	for i := range qs {
		if qs[i].Source == "" {
			qs[i].Source = models.SourceGenerated
		}
	}
	b, err := yaml.Marshal(models.QuestionFile{Questions: qs})
	if err != nil {
		return err
	}
	name := fmt.Sprintf("gen-%s.yaml", time.Now().Format("20060102-150405"))
	return os.WriteFile(filepath.Join("questions-custom", name), b, 0o644)
}

// ─────────────────────────────────────────────────────────────────────────────
// Templates
// ─────────────────────────────────────────────────────────────────────────────

func tplPod(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um Pod chamado **%s** com a imagem **%s** e a label **app=%s**.", p.Name, p.Image, p.Name),
			fmt.Sprintf("Crie um Pod chamado **%s** com a imagem **%s** e a label **app=%s**.\n\nDica: `kubectl run` aceita `--labels`.", p.Name, p.Image, p.Name),
			fmt.Sprintf("Vamos criar um Pod passo a passo:\n\n1. Use `kubectl run %s --image=%s --labels=app=%s`\n2. Confirme com `kubectl get pod %s --show-labels`\n3. Aguarde o STATUS ficar **Running**\n\nUm Pod é a menor unidade do Kubernetes: um ou mais containers que compartilham rede e storage.", p.Name, p.Image, p.Name, p.Name)),
		Hint:          fmt.Sprintf("kubectl run %s --image=%s --labels=app=%s", p.Name, p.Image, p.Name),
		AnswerCommand: fmt.Sprintf("kubectl run %s --image=%s --labels=app=%s", p.Name, p.Image, p.Name),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Pod **%s** está **Running**", p.Name),
				Hint:        pickHelp(level, "", "kubectl run cria o pod; aguarde ficar Ready.", fmt.Sprintf("Comando completo: kubectl run %s --image=%s --labels=app=%s — depois aguarde alguns segundos.", p.Name, p.Image, p.Name)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.status.phase}' 2>/dev/null", p.Name),
					ExpectedContains: "Running",
				},
			},
			{
				Description: fmt.Sprintf("Pod tem a label **app=%s**", p.Name),
				Hint:        pickHelp(level, "", "--labels no run, ou kubectl label depois.", fmt.Sprintf("Se esqueceu a label: kubectl label pod %s app=%s", p.Name, p.Name)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.metadata.labels.app}' 2>/dev/null", p.Name),
					ExpectedContains: p.Name,
				},
			},
		},
		Teardown: []string{fmt.Sprintf("kubectl delete pod %s --ignore-not-found=true", p.Name)},
		Explanation: pickHelp(level,
			"kubectl run é a forma imperativa de criar um Pod único.",
			"kubectl run cria um Pod único (sem controller). Labels permitem que Services e selectors encontrem o pod.",
			"kubectl run cria um Pod único, sem controller por trás — se morrer, ninguém recria (diferente de um Deployment). Labels são pares chave=valor usados por selectors de Services, NetworkPolicies e afins para localizar pods. --show-labels no get exibe as labels."),
		DocURL:     "https://kubernetes.io/docs/concepts/workloads/pods/",
		DocSection: "Concepts → Workloads → Pods",
	}
}

func tplDeployment(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um Deployment **%s** com **%d réplicas** da imagem **%s** e depois escale para **%d**.", p.Name, p.Replicas, p.Image, p.Replicas+1),
			fmt.Sprintf("Crie um Deployment **%s** com **%d réplicas** da imagem **%s** e depois escale para **%d**.\n\nDica: `kubectl create deployment` + `kubectl scale`.", p.Name, p.Replicas, p.Image, p.Replicas+1),
			fmt.Sprintf("Deployment na prática, passo a passo:\n\n1. Crie: `kubectl create deployment %s --image=%s --replicas=%d`\n2. Aguarde: `kubectl rollout status deployment/%s`\n3. Escale: `kubectl scale deployment %s --replicas=%d`\n\nO Deployment gerencia um ReplicaSet, que garante o número de pods desejado — escalar é só mudar esse número.", p.Name, p.Image, p.Replicas, p.Name, p.Name, p.Replicas+1)),
		Hint:          fmt.Sprintf("kubectl create deployment %s --image=%s --replicas=%d && kubectl scale deployment %s --replicas=%d", p.Name, p.Image, p.Replicas, p.Name, p.Replicas+1),
		AnswerCommand: fmt.Sprintf("kubectl create deployment %s --image=%s --replicas=%d; kubectl scale deployment %s --replicas=%d", p.Name, p.Image, p.Replicas, p.Name, p.Replicas+1),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Deployment **%s** existe com imagem **%s**", p.Name, p.Image),
				Hint:        pickHelp(level, "", "kubectl create deployment ...", fmt.Sprintf("kubectl create deployment %s --image=%s --replicas=%d", p.Name, p.Image, p.Replicas)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null", p.Name),
					ExpectedContains: p.Image,
				},
			},
			{
				Description: fmt.Sprintf("**%d réplicas** prontas após o scale", p.Replicas+1),
				Hint:        pickHelp(level, "", "kubectl scale deployment --replicas=N", fmt.Sprintf("kubectl scale deployment %s --replicas=%d — aguarde os pods ficarem Ready e cheque de novo.", p.Name, p.Replicas+1)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.status.readyReplicas}' 2>/dev/null", p.Name),
					ExpectedContains: fmt.Sprintf("%d", p.Replicas+1),
				},
			},
		},
		Teardown: []string{fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name)},
		Explanation: pickHelp(level,
			"Deployment → ReplicaSet → Pods; scale ajusta o replicas desejado.",
			"O Deployment declara o estado desejado; o ReplicaSet materializa o número de pods. kubectl scale altera .spec.replicas.",
			"O Deployment é o controller padrão para apps stateless: ele cria um ReplicaSet, que observa continuamente o cluster e cria/remove pods até bater com .spec.replicas. kubectl scale muda esse campo; o rollout status acompanha até todos ficarem Ready. Em prova, prefira comandos imperativos pela velocidade."),
		DocURL:     "https://kubernetes.io/docs/concepts/workloads/controllers/deployment/",
		DocSection: "Concepts → Workloads → Deployments",
	}
}

func tplReplicaSet(p params, level int, cert string) models.Question {
	rs := p.Name + "-rs"
	labels := "app=" + p.Name
	return models.Question{
		Question: fmt.Sprintf("Crie um ReplicaSet chamado **%s** que mantenha **%d pods** com a label **%s** usando a imagem **%s**.", rs, p.Replicas, labels, p.Image),
		Hint:     "ReplicaSet usa `spec.replicas`, `spec.selector.matchLabels` e `spec.template.metadata.labels`; selector e template precisam casar.",
		AnswerCommand: fmt.Sprintf(`cat <<'EOF' | kubectl apply -f -
apiVersion: apps/v1
kind: ReplicaSet
metadata:
  name: %s
spec:
  replicas: %d
  selector:
    matchLabels:
      app: %s
  template:
    metadata:
      labels:
        app: %s
    spec:
      containers:
      - name: app
        image: %s
EOF`, rs, p.Replicas, p.Name, p.Name, p.Image),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("ReplicaSet **%s** existe com selector `app=%s`", rs, p.Name),
				Hint:        "Confira se `spec.selector.matchLabels.app` e `spec.template.metadata.labels.app` possuem o mesmo valor.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get rs %s -o jsonpath='{.spec.selector.matchLabels.app}:{.spec.template.metadata.labels.app}' 2>/dev/null", rs),
					ExpectedContains: p.Name + ":" + p.Name,
				},
			},
			{
				Description: fmt.Sprintf("ReplicaSet mantém **%d pods prontos**", p.Replicas),
				Hint:        "A quantidade desejada fica em `spec.replicas`; aguarde os pods ficarem Ready antes de validar.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get rs %s -o jsonpath='{.status.readyReplicas}' 2>/dev/null", rs),
					ExpectedContains: fmt.Sprintf("%d", p.Replicas),
				},
			},
		},
		Teardown:    []string{fmt.Sprintf("kubectl delete rs %s --ignore-not-found=true", rs)},
		Explanation: "ReplicaSet garante que um conjunto de pods com labels compatíveis exista na quantidade desejada. Na prática, Deployments normalmente criam e gerenciam ReplicaSets, mas praticar o recurso diretamente ajuda a entender selectors e reconciliação.",
		DocURL:      "https://kubernetes.io/docs/concepts/workloads/controllers/replicaset/",
		DocSection:  "Concepts -> Workloads -> ReplicaSet",
	}
}

func tplService(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Exponha o Deployment **%s** (já criado) com um Service **ClusterIP** chamado **%s** na porta **%d**, com endpoints ativos.", p.Name, p.Name2, p.Port),
			fmt.Sprintf("Exponha o Deployment **%s** (já criado) com um Service **ClusterIP** chamado **%s** na porta **%d**.\n\nDica: `kubectl expose deployment` herda o selector das labels do Deployment.", p.Name, p.Name2, p.Port),
			fmt.Sprintf("Expondo uma aplicação passo a passo:\n\n1. O Deployment **%s** já está rodando (criado pelo setup)\n2. Exponha: `kubectl expose deployment %s --name=%s --port=%d --target-port=80`\n3. Verifique endpoints: `kubectl get endpoints %s` — deve listar IPs\n\nEndpoints vazios = selector não casa com pods Ready.", p.Name, p.Name, p.Name2, p.Port, p.Name2)),
		Hint:          fmt.Sprintf("kubectl expose deployment %s --name=%s --port=%d --target-port=80", p.Name, p.Name2, p.Port),
		AnswerCommand: fmt.Sprintf("kubectl expose deployment %s --name=%s --port=%d --target-port=80", p.Name, p.Name2, p.Port),
		Setup: []models.SetupStep{
			{Description: fmt.Sprintf("Criando o Deployment %s...", p.Name), Command: fmt.Sprintf("kubectl create deployment %s --image=nginx:1.21 --replicas=2 2>/dev/null || true", p.Name)},
			{Description: "Aguardando pods ficarem prontos...", Command: fmt.Sprintf("kubectl rollout status deployment/%s --timeout=90s 2>/dev/null || true", p.Name)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Service **%s** é **ClusterIP** na porta **%d**", p.Name2, p.Port),
				Hint:        pickHelp(level, "", "kubectl expose deployment --name --port", fmt.Sprintf("kubectl expose deployment %s --name=%s --port=%d --target-port=80", p.Name, p.Name2, p.Port)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get svc %s -o jsonpath='{.spec.type}:{.spec.ports[0].port}' 2>/dev/null", p.Name2),
					ExpectedContains: fmt.Sprintf("ClusterIP:%d", p.Port),
				},
			},
			{
				Description: "🌐 Service tem **endpoints ativos** (app acessível)",
				Hint:        pickHelp(level, "", "Endpoints aparecem quando o selector casa com pods Ready.", "kubectl get endpoints "+p.Name2+" — se vazio, confira selector do svc vs labels dos pods (kubectl get pods --show-labels)."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get endpoints %s -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null | grep -qE '[0-9]' && echo REACHABLE || echo DOWN", p.Name2),
					ExpectedContains: "REACHABLE",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete svc %s --ignore-not-found=true", p.Name2),
			fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name),
		},
		Explanation: pickHelp(level,
			"kubectl expose cria um Service com selector herdado do Deployment.",
			"Service ClusterIP dá um IP estável interno; o selector conecta o Service aos pods e popula os endpoints.",
			"O Service resolve o problema de pods serem efêmeros: ele dá um IP/DNS estável e balanceia entre os pods cujo selector casa. --port é a porta do Service; --target-port é a do container. Os EndpointSlices são preenchidos automaticamente com os IPs dos pods Ready — é o elo que faz o tráfego chegar."),
		DocURL:     "https://kubernetes.io/docs/concepts/services-networking/service/",
		DocSection: "Concepts → Services",
	}
}

func tplDNSLookup(p params, level int, cert string) models.Question {
	client := p.Name + "-client"
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("O Deployment **%s** e o Service **%s** ja existem. Crie um Pod cliente **%s** e valide a resolucao DNS interna do Service.", p.Name, p.Name2, client),
			fmt.Sprintf("Treino CKA de Services & Networking: crie um Pod **%s** com busybox e use DNS interno para resolver **%s**.\n\nDica: Services ganham nome DNS dentro do namespace.", client, p.Name2),
			fmt.Sprintf("DNS de Service passo a passo:\n\n1. O setup cria Deployment **%s** e Service **%s**\n2. Crie o cliente: `kubectl run %s --image=busybox:1.35 --restart=Never --command -- sleep 3600`\n3. Valide DNS: `kubectl exec %s -- nslookup %s`\n\nCoreDNS resolve Services por nome dentro do namespace e por FQDN como `<svc>.<namespace>.svc.cluster.local`.", p.Name, p.Name2, client, client, p.Name2)),
		Hint:          fmt.Sprintf("kubectl run %s --image=busybox:1.35 --restart=Never --command -- sleep 3600; kubectl exec %s -- nslookup %s", client, client, p.Name2),
		AnswerCommand: fmt.Sprintf("kubectl run %s --image=busybox:1.35 --restart=Never --command -- sleep 3600", client),
		Setup: []models.SetupStep{
			{Description: fmt.Sprintf("Criando Deployment %s...", p.Name), Command: fmt.Sprintf("kubectl create deployment %s --image=nginx:1.25 --replicas=2 2>/dev/null || true; kubectl rollout status deployment/%s --timeout=90s 2>/dev/null || true", p.Name, p.Name)},
			{Description: fmt.Sprintf("Criando Service %s...", p.Name2), Command: fmt.Sprintf("kubectl expose deployment %s --name=%s --port=80 --target-port=80 2>/dev/null || true", p.Name, p.Name2)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Pod cliente **%s** esta Running", client),
				Hint:        pickHelp(level, "", "Crie o pod busybox com sleep para ele continuar vivo.", "Use kubectl run "+client+" --image=busybox:1.35 --restart=Never --command -- sleep 3600."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.status.phase}' 2>/dev/null", client),
					ExpectedContains: "Running",
				},
			},
			{
				Description: fmt.Sprintf("DNS interno resolve o Service **%s**", p.Name2),
				Hint:        pickHelp(level, "", "Use nslookup dentro do pod cliente.", "kubectl exec "+client+" -- nslookup "+p.Name2+" deve retornar endereco do Service."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl exec %s -- nslookup %s 2>/dev/null | grep -q 'Name:' && echo RESOLVED || echo FAIL", client, p.Name2),
					ExpectedContains: "RESOLVED",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete pod %s --ignore-not-found=true", client),
			fmt.Sprintf("kubectl delete svc %s --ignore-not-found=true", p.Name2),
			fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name),
		},
		Explanation: "Services recebem registros DNS pelo CoreDNS. Em CKA, diagnosticar conectividade inclui validar Service, endpoints e resolucao de nomes a partir de outro pod.",
		DocURL:      "https://kubernetes.io/docs/concepts/services-networking/dns-pod-service/",
		DocSection:  "Services & Networking -> DNS for Services",
	}
}

func tplConfigMap(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um ConfigMap **%s-config** com **%s=%s** e injete-o como env no Deployment **%s** (já criado).", p.Name, p.Key, p.Value, p.Name),
			fmt.Sprintf("Crie um ConfigMap **%s-config** com **%s=%s** e injete-o como variável de ambiente no Deployment **%s**.\n\nDica: `kubectl create configmap --from-literal` + `kubectl set env --from=configmap/...`.", p.Name, p.Key, p.Value, p.Name),
			fmt.Sprintf("Configuração externalizada, passo a passo:\n\n1. Crie o ConfigMap: `kubectl create configmap %s-config --from-literal=%s=%s`\n2. Injete no Deployment: `kubectl set env deployment/%s --from=configmap/%s-config`\n3. Confira: `kubectl set env deployment/%s --list`\n\nAssim o app muda de configuração sem rebuild da imagem (12-factor).", p.Name, p.Key, p.Value, p.Name, p.Name, p.Name)),
		Hint:          fmt.Sprintf("kubectl create configmap %s-config --from-literal=%s=%s && kubectl set env deployment/%s --from=configmap/%s-config", p.Name, p.Key, p.Value, p.Name, p.Name),
		AnswerCommand: fmt.Sprintf("kubectl create configmap %s-config --from-literal=%s=%s; kubectl set env deployment/%s --from=configmap/%s-config", p.Name, p.Key, p.Value, p.Name, p.Name),
		Setup: []models.SetupStep{
			{Description: fmt.Sprintf("Criando o Deployment %s...", p.Name), Command: fmt.Sprintf("kubectl create deployment %s --image=nginx:1.21 --replicas=1 2>/dev/null || true", p.Name)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("ConfigMap **%s-config** tem **%s=%s**", p.Name, p.Key, p.Value),
				Hint:        pickHelp(level, "", "kubectl create configmap --from-literal=CHAVE=valor", fmt.Sprintf("kubectl create configmap %s-config --from-literal=%s=%s", p.Name, p.Key, p.Value)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get configmap %s-config -o jsonpath='{.data.%s}' 2>/dev/null", p.Name, p.Key),
					ExpectedContains: p.Value,
				},
			},
			{
				Description: "Deployment referencia o ConfigMap via **env**",
				Hint:        pickHelp(level, "", "kubectl set env --from=configmap/...", fmt.Sprintf("kubectl set env deployment/%s --from=configmap/%s-config", p.Name, p.Name)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.spec.template.spec.containers[0].env[*].valueFrom.configMapKeyRef.name}' 2>/dev/null", p.Name),
					ExpectedContains: p.Name + "-config",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name),
			fmt.Sprintf("kubectl delete configmap %s-config --ignore-not-found=true", p.Name),
		},
		Explanation: pickHelp(level,
			"ConfigMaps externalizam configuração; set env injeta como variável.",
			"ConfigMaps guardam config não-sensível. kubectl set env --from=configmap injeta cada chave como env var e dispara um rollout.",
			"ConfigMaps separam configuração do código (12-factor). Injetados como env (valueFrom.configMapKeyRef) ou volume. Mudar o ConfigMap NÃO atualiza env vars de pods rodando — o set env gera novo template e o Deployment faz rollout. Para secrets sensíveis, use Secret + secretKeyRef."),
		DocURL:     "https://kubernetes.io/docs/tasks/configure-pod-container/configure-pod-configmap/",
		DocSection: "Tasks → ConfigMaps",
	}
}

func tplPVC(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um PVC **%s-pvc** de **1Gi** (accessMode ReadWriteOnce) e um Pod **%s** com a imagem **nginx:1.21** montando esse PVC em **/data**.", p.Name, p.Name),
			fmt.Sprintf("Crie um PVC **%s-pvc** de **1Gi** (ReadWriteOnce) e um Pod **%s** montando-o em **/data**.\n\nDica: PVC e Pod via YAML (`kubectl apply -f`); no pod, `volumes.persistentVolumeClaim` + `volumeMounts`.", p.Name, p.Name),
			fmt.Sprintf("Storage persistente, passo a passo:\n\n1. Crie o PVC (o minikube provisiona PV automaticamente):\n```\nkubectl apply -f - <<EOF\napiVersion: v1\nkind: PersistentVolumeClaim\nmetadata:\n  name: %s-pvc\nspec:\n  accessModes: [ReadWriteOnce]\n  resources:\n    requests:\n      storage: 1Gi\nEOF\n```\n2. Crie o Pod montando o PVC em /data (volumes + volumeMounts)\n3. Verifique: `kubectl get pvc %s-pvc` deve mostrar **Bound**", p.Name, p.Name)),
		Hint:          fmt.Sprintf("PVC: accessModes [ReadWriteOnce], storage 1Gi. Pod: volumes.persistentVolumeClaim.claimName=%s-pvc + volumeMounts mountPath=/data", p.Name),
		AnswerCommand: fmt.Sprintf("kubectl apply -f pvc.yaml && kubectl apply -f pod.yaml  # PVC %s-pvc 1Gi RWO + pod %s montando em /data", p.Name, p.Name),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("PVC **%s-pvc** está **Bound**", p.Name),
				Hint:        pickHelp(level, "", "kubectl apply do PVC; storageclass default do minikube provisiona sozinha.", "Aplique o YAML do passo 1 e aguarde: kubectl get pvc — STATUS deve virar Bound."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pvc %s-pvc -o jsonpath='{.status.phase}' 2>/dev/null", p.Name),
					ExpectedContains: "Bound",
				},
			},
			{
				Description: fmt.Sprintf("Pod **%s** monta o PVC em **/data** e está Running", p.Name),
				Hint:        pickHelp(level, "", "volumes + volumeMounts no spec do pod.", "No pod: spec.volumes[0].persistentVolumeClaim.claimName="+p.Name+"-pvc e containers[0].volumeMounts[0].mountPath=/data"),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.spec.volumes[*].persistentVolumeClaim.claimName}:{.status.phase}' 2>/dev/null", p.Name),
					ExpectedContains: fmt.Sprintf("%s-pvc:Running", p.Name),
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete pod %s --ignore-not-found=true", p.Name),
			fmt.Sprintf("kubectl delete pvc %s-pvc --ignore-not-found=true", p.Name),
		},
		Explanation: pickHelp(level,
			"PVC pede storage; o provisioner cria o PV; o pod monta via claimName.",
			"O PVC é o pedido de storage do usuário; um PV satisfaz o pedido (binding). O pod referencia o claim, não o volume físico.",
			"PV (recurso do cluster) e PVC (pedido do usuário) desacoplam app de infraestrutura de storage. Com StorageClass + dynamic provisioning (padrão no minikube/AKS), criar o PVC já provisiona o PV. accessModes: RWO = um nó por vez. O pod só referencia o claimName — portável entre ambientes."),
		DocURL:     "https://kubernetes.io/docs/concepts/storage/persistent-volumes/",
		DocSection: "Concepts → Storage → Persistent Volumes",
	}
}

func tplNodeSelector(p params, level int, cert string) models.Question {
	label := fmt.Sprintf("disk=%s", p.Value)
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Rotule o nó do cluster com **%s** e crie um Pod **%s** (imagem **%s**) que só agende em nós com essa label (nodeSelector).", label, p.Name, p.Image),
			fmt.Sprintf("Rotule o nó com **%s** e crie um Pod **%s** com **nodeSelector** para essa label.\n\nDica: `kubectl label node` + YAML com `spec.nodeSelector`.", label, p.Name),
			fmt.Sprintf("Scheduling dirigido, passo a passo:\n\n1. Descubra o nó: `kubectl get nodes`\n2. Rotule: `kubectl label node <nó> %s`\n3. Crie o pod com nodeSelector:\n```\nkubectl apply -f - <<EOF\napiVersion: v1\nkind: Pod\nmetadata:\n  name: %s\nspec:\n  nodeSelector:\n    disk: %s\n  containers:\n  - name: app\n    image: %s\nEOF\n```\n4. O pod deve ficar **Running** (a label casa).", label, p.Name, p.Value, p.Image)),
		Hint:          fmt.Sprintf("kubectl label node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') %s — depois pod com spec.nodeSelector {disk: %s}", label, p.Value),
		AnswerCommand: fmt.Sprintf("kubectl label node <nó> %s; kubectl apply -f pod.yaml  # com nodeSelector disk: %s", label, p.Value),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("O nó tem a label **%s**", label),
				Hint:        pickHelp(level, "", "kubectl label node <nome> chave=valor", fmt.Sprintf("kubectl label node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') %s", label)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get nodes -o jsonpath='{.items[0].metadata.labels.disk}' 2>/dev/null"),
					ExpectedContains: p.Value,
				},
			},
			{
				Description: fmt.Sprintf("Pod **%s** usa nodeSelector e está **Running**", p.Name),
				Hint:        pickHelp(level, "", "spec.nodeSelector no YAML do pod.", "Use o YAML do passo 3 — nodeSelector deve ser exatamente {disk: "+p.Value+"}."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.spec.nodeSelector.disk}:{.status.phase}' 2>/dev/null", p.Name),
					ExpectedContains: fmt.Sprintf("%s:Running", p.Value),
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete pod %s --ignore-not-found=true", p.Name),
			"kubectl label node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') disk- 2>/dev/null || true",
		},
		Explanation: pickHelp(level,
			"nodeSelector agenda pods apenas em nós com as labels exigidas.",
			"nodeSelector é a forma mais simples de direcionar scheduling: o pod só agenda em nós cujas labels contêm todos os pares exigidos.",
			"O scheduler filtra nós elegíveis; nodeSelector é o filtro mais simples (igualdade exata de labels). Para regras ricas (OR, pesos, preferências) use nodeAffinity. Se nenhum nó casa, o pod fica Pending com FailedScheduling — diagnóstico clássico de prova via kubectl describe pod."),
		DocURL:     "https://kubernetes.io/docs/concepts/scheduling-eviction/assign-pod-node/",
		DocSection: "Concepts → Scheduling → Assigning Pods to Nodes",
	}
}

func tplTaintToleration(p params, level int, cert string) models.Question {
	key := "dedicated"
	value := p.Value
	tolerationPod := p.Name + "-tol"
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Aplique no primeiro node o taint **%s=%s:NoSchedule** e crie um Pod **%s** que consiga agendar usando a toleration correta.", key, value, tolerationPod),
			fmt.Sprintf("Treino CKA de scheduling: taint o primeiro node com **%s=%s:NoSchedule** e crie o Pod **%s** com toleration correspondente.\n\nDica: taints repelem pods; tolerations permitem que um pod aceite esse node.", key, value, tolerationPod),
			fmt.Sprintf("Taints e tolerations passo a passo:\n\n1. Taint o primeiro node: `kubectl taint node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') %s=%s:NoSchedule --overwrite`\n2. Crie um Pod **%s** com toleration `key: %s`, `operator: Equal`, `value: %s`, `effect: NoSchedule`\n3. Confirme que o Pod ficou **Running**\n\nEsse topico pertence a CKA Workloads & Scheduling: controlar onde workloads podem rodar.", key, value, tolerationPod, key, value)),
		Hint:          fmt.Sprintf("spec.tolerations: [{key:%s, operator: Equal, value:%s, effect: NoSchedule}]", key, value),
		AnswerCommand: fmt.Sprintf("kubectl taint node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') %s=%s:NoSchedule --overwrite; kubectl apply -f pod.yaml", key, value),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Primeiro node tem taint **%s=%s:NoSchedule**", key, value),
				Hint:        pickHelp(level, "", "Use kubectl taint node <node> key=value:NoSchedule --overwrite.", fmt.Sprintf("kubectl taint node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') %s=%s:NoSchedule --overwrite", key, value)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') -o jsonpath='{.spec.taints[?(@.key==\"%s\")].value}:{.spec.taints[?(@.key==\"%s\")].effect}' 2>/dev/null", key, key),
					ExpectedContains: value + ":NoSchedule",
				},
			},
			{
				Description: fmt.Sprintf("Pod **%s** tolera o taint e esta Running", tolerationPod),
				Hint:        pickHelp(level, "", "Adicione spec.tolerations no manifesto do Pod.", "O Pod precisa tolerar key "+key+", value "+value+" e effect NoSchedule."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.spec.tolerations[?(@.key==\"%s\")].value}:{.status.phase}' 2>/dev/null", tolerationPod, key),
					ExpectedContains: value + ":Running",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete pod %s --ignore-not-found=true", tolerationPod),
			fmt.Sprintf("kubectl taint node $(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') %s- 2>/dev/null || true", key),
		},
		Explanation: "Taints ficam no node e impedem scheduling; tolerations ficam no Pod e permitem que ele aceite aquele taint. Isso nao força o pod a ir para o node, apenas remove a repulsao.",
		DocURL:      "https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/",
		DocSection:  "Scheduling -> Taints and Tolerations",
	}
}

func tplProbes(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Adicione **readinessProbe** e **livenessProbe** httpGet (path **/**, porta **80**) ao Deployment **%s** (já criado).", p.Name),
			fmt.Sprintf("Adicione **readinessProbe** e **livenessProbe** httpGet (path **/**, porta **80**) ao Deployment **%s**.\n\nDica: `kubectl edit deployment %s` e edite o container.", p.Name, p.Name),
			fmt.Sprintf("Health checks, passo a passo:\n\n1. `kubectl edit deployment %s`\n2. Em spec.template.spec.containers[0], adicione:\n```\nreadinessProbe:\n  httpGet:\n    path: /\n    port: 80\nlivenessProbe:\n  httpGet:\n    path: /\n    port: 80\n```\n3. Salve — o Deployment faz rollout automático\n\nreadiness controla tráfego; liveness reinicia container travado.", p.Name)),
		Hint:          "Em containers[0]: readinessProbe e livenessProbe com httpGet {path: /, port: 80}",
		AnswerCommand: fmt.Sprintf("kubectl edit deployment %s  # adicionar readinessProbe/livenessProbe httpGet / :80", p.Name),
		Setup: []models.SetupStep{
			{Description: fmt.Sprintf("Criando o Deployment %s...", p.Name), Command: fmt.Sprintf("kubectl create deployment %s --image=nginx:1.21 --replicas=1 2>/dev/null || true", p.Name)},
			{Description: "Aguardando pod ficar pronto...", Command: fmt.Sprintf("kubectl rollout status deployment/%s --timeout=90s 2>/dev/null || true", p.Name)},
		},
		Goals: []models.Goal{
			{
				Description: "**readinessProbe** httpGet configurada",
				Hint:        pickHelp(level, "", "readinessProbe: { httpGet: { path: /, port: 80 } }", "No kubectl edit, dentro do container, cole o bloco readinessProbe do passo 2 (indentação: mesmo nível de image)."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.spec.template.spec.containers[0].readinessProbe.httpGet.path}' 2>/dev/null", p.Name),
					ExpectedContains: "/",
				},
			},
			{
				Description: "**livenessProbe** httpGet na porta **80**",
				Hint:        pickHelp(level, "", "livenessProbe: { httpGet: { path: /, port: 80 } }", "Bloco livenessProbe igual ao readiness — só muda o nome da chave."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.spec.template.spec.containers[0].livenessProbe.httpGet.port}' 2>/dev/null", p.Name),
					ExpectedContains: "80",
				},
			},
		},
		Teardown: []string{fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name)},
		Explanation: pickHelp(level,
			"readiness controla endpoints; liveness reinicia containers travados.",
			"readinessProbe decide se o pod recebe tráfego (entra nos endpoints); livenessProbe reinicia o container quando falha.",
			"Probes são o contrato de saúde do container. readiness falhou → pod sai dos endpoints (sem tráfego), mas NÃO reinicia. liveness falhou → kubelet reinicia o container. Há ainda startupProbe para apps lentos no boot. Sem probes, rolling updates podem mandar tráfego para pods ainda não prontos."),
		DocURL:     "https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/",
		DocSection: "Tasks → Configure Probes",
	}
}

func tplInitContainer(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um Pod **%s** com um **initContainer** (busybox:1.35, comando `sh -c 'echo preparando && sleep 3'`) e um container principal **nginx:1.21**.", p.Name),
			fmt.Sprintf("Crie um Pod **%s** com um **initContainer** (busybox:1.35 rodando `sh -c 'echo preparando && sleep 3'`) e container principal **nginx:1.21**.\n\nDica: `spec.initContainers` é uma lista igual a `containers`.", p.Name),
			fmt.Sprintf("Init containers, passo a passo:\n\n1. Aplique:\n```\nkubectl apply -f - <<EOF\napiVersion: v1\nkind: Pod\nmetadata:\n  name: %s\nspec:\n  initContainers:\n  - name: prepara\n    image: busybox:1.35\n    command: ['sh', '-c', 'echo preparando && sleep 3']\n  containers:\n  - name: app\n    image: nginx:1.21\nEOF\n```\n2. Observe: `kubectl get pod %s -w` — status passa por **Init:0/1** antes de **Running**\n\nO container principal só inicia quando TODOS os init completam.", p.Name, p.Name)),
		Hint:          "spec.initContainers: [{name, image: busybox:1.35, command: ['sh','-c','...']}] antes de containers.",
		AnswerCommand: fmt.Sprintf("kubectl apply -f pod.yaml  # pod %s com initContainers busybox + container nginx", p.Name),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Pod **%s** tem um **initContainer** busybox", p.Name),
				Hint:        pickHelp(level, "", "spec.initContainers no YAML.", "Use exatamente o YAML do passo 1 — initContainers fica no mesmo nível de containers."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.spec.initContainers[0].image}' 2>/dev/null", p.Name),
					ExpectedContains: "busybox",
				},
			},
			{
				Description: "Pod chegou a **Running** (init completou)",
				Hint:        pickHelp(level, "", "Aguarde o init terminar (~3s).", "kubectl get pod "+p.Name+" — Init:0/1 → PodInitializing → Running. Se travar em Init, veja kubectl logs "+p.Name+" -c prepara."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.status.phase}' 2>/dev/null", p.Name),
					ExpectedContains: "Running",
				},
			},
		},
		Teardown: []string{fmt.Sprintf("kubectl delete pod %s --ignore-not-found=true", p.Name)},
		Explanation: pickHelp(level,
			"initContainers rodam em sequência ANTES do container principal.",
			"Init containers preparam o ambiente (migrations, download de config, esperar dependências) e devem completar com sucesso antes do app subir.",
			"Init containers rodam um a um, em ordem, até completarem — só então os containers principais iniciam. Casos clássicos: aguardar um Service ficar disponível, popular um volume compartilhado, rodar migrations. Se um init falha, o kubelet o reinicia conforme a restartPolicy; o pod fica em Init:Error/CrashLoopBackOff. kubectl logs <pod> -c <init> inspeciona."),
		DocURL:     "https://kubernetes.io/docs/concepts/workloads/pods/init-containers/",
		DocSection: "Concepts → Pods → Init Containers",
	}
}

func tplServiceAccount(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um ServiceAccount **%s-sa**, um Role **%s-reader** (get/list pods) e um RoleBinding ligando os dois no namespace default.", p.Name, p.Name),
			fmt.Sprintf("Crie um ServiceAccount **%s-sa**, um Role **%s-reader** que permite **get/list** em **pods**, e o RoleBinding.\n\nDica: `kubectl create sa|role|rolebinding` fazem tudo imperativo.", p.Name, p.Name),
			fmt.Sprintf("RBAC mínimo, passo a passo:\n\n1. `kubectl create serviceaccount %s-sa`\n2. `kubectl create role %s-reader --verb=get --verb=list --resource=pods`\n3. `kubectl create rolebinding %s-bind --role=%s-reader --serviceaccount=default:%s-sa`\n4. Teste: `kubectl auth can-i list pods --as=system:serviceaccount:default:%s-sa` → **yes**", p.Name, p.Name, p.Name, p.Name, p.Name, p.Name)),
		Hint:          fmt.Sprintf("kubectl create sa %s-sa; kubectl create role %s-reader --verb=get,list --resource=pods; kubectl create rolebinding %s-bind --role=%s-reader --serviceaccount=default:%s-sa", p.Name, p.Name, p.Name, p.Name, p.Name),
		AnswerCommand: fmt.Sprintf("kubectl create sa %s-sa; kubectl create role %s-reader --verb=get --verb=list --resource=pods; kubectl create rolebinding %s-bind --role=%s-reader --serviceaccount=default:%s-sa", p.Name, p.Name, p.Name, p.Name, p.Name),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Role **%s-reader** permite get/list de pods", p.Name),
				Hint:        pickHelp(level, "", "kubectl create role --verb --resource", fmt.Sprintf("kubectl create role %s-reader --verb=get --verb=list --resource=pods", p.Name)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get role %s-reader -o jsonpath='{.rules[0].verbs}' 2>/dev/null", p.Name),
					ExpectedContains: "list",
				},
			},
			{
				Description: fmt.Sprintf("SA **%s-sa** consegue listar pods (RBAC efetivo)", p.Name),
				Hint:        pickHelp(level, "", "rolebinding liga role ↔ serviceaccount.", fmt.Sprintf("kubectl create rolebinding %s-bind --role=%s-reader --serviceaccount=default:%s-sa — valide com kubectl auth can-i.", p.Name, p.Name, p.Name)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl auth can-i list pods --as=system:serviceaccount:default:%s-sa 2>/dev/null", p.Name),
					ExpectedContains: "yes",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete rolebinding %s-bind --ignore-not-found=true", p.Name),
			fmt.Sprintf("kubectl delete role %s-reader --ignore-not-found=true", p.Name),
			fmt.Sprintf("kubectl delete sa %s-sa --ignore-not-found=true", p.Name),
		},
		Explanation: pickHelp(level,
			"RBAC: Role define permissões; RoleBinding as concede a um subject.",
			"Role (namespaced) lista verbos×recursos permitidos; RoleBinding concede a users/groups/serviceaccounts. auth can-i --as testa.",
			"RBAC em 3 peças: identidade (ServiceAccount), permissões (Role: verbos get/list sobre pods) e a ligação (RoleBinding). Sempre menor privilégio. ClusterRole/ClusterRoleBinding são o equivalente sem namespace. kubectl auth can-i --as=system:serviceaccount:<ns>:<sa> é o jeito rápido de auditar — cai MUITO em prova de CKS/CKA."),
		DocURL:     "https://kubernetes.io/docs/reference/access-authn-authz/rbac/",
		DocSection: "Reference → RBAC",
	}
}

func tplNetworkPolicy(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("O pod **%s** (já criado, label app=%s) precisa ser isolado. Crie uma **NetworkPolicy** chamada **%s-deny** que bloqueia TODO tráfego de entrada (ingress) para ele.", p.Name, p.Name, p.Name),
			fmt.Sprintf("Isole o pod **%s** com uma **NetworkPolicy** **%s-deny** de deny-all ingress.\n\nDica: podSelector com a label do pod e `policyTypes: [Ingress]` sem regras = nega tudo.", p.Name, p.Name),
			fmt.Sprintf("Deny-all ingress, passo a passo:\n\n1. O pod **%s** já existe (setup) com a label app=%s\n2. Aplique:\n```\nkubectl apply -f - <<EOF\napiVersion: networking.k8s.io/v1\nkind: NetworkPolicy\nmetadata:\n  name: %s-deny\nspec:\n  podSelector:\n    matchLabels:\n      app: %s\n  policyTypes:\n  - Ingress\nEOF\n```\n3. Sem regras `ingress` listadas, NADA entra — é o padrão seguro do CKS.", p.Name, p.Name, p.Name, p.Name)),
		Hint:          fmt.Sprintf("NetworkPolicy com podSelector app=%s e policyTypes [Ingress] sem regras ingress = deny-all.", p.Name),
		AnswerCommand: fmt.Sprintf("kubectl apply -f netpol.yaml  # NetworkPolicy %s-deny, podSelector app=%s, policyTypes [Ingress]", p.Name, p.Name),
		Setup: []models.SetupStep{
			{Description: fmt.Sprintf("Criando o pod alvo %s...", p.Name), Command: fmt.Sprintf("kubectl run %s --image=nginx:1.21 --labels=app=%s --restart=Never 2>/dev/null || true", p.Name, p.Name)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("NetworkPolicy **%s-deny** seleciona o pod (app=%s)", p.Name, p.Name),
				Hint:        pickHelp(level, "", "spec.podSelector.matchLabels.app", "Use o YAML do passo 2 exatamente."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get networkpolicy %s-deny -o jsonpath='{.spec.podSelector.matchLabels.app}' 2>/dev/null", p.Name),
					ExpectedContains: p.Name,
				},
			},
			{
				Description: "A política bloqueia **Ingress** (deny-all)",
				Hint:        pickHelp(level, "", "policyTypes deve conter Ingress, sem regras ingress.", "spec.policyTypes: [Ingress] e NENHUM item em spec.ingress."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get networkpolicy %s-deny -o jsonpath='{.spec.policyTypes}' 2>/dev/null", p.Name),
					ExpectedContains: "Ingress",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete networkpolicy %s-deny --ignore-not-found=true", p.Name),
			fmt.Sprintf("kubectl delete pod %s --ignore-not-found=true", p.Name),
		},
		Explanation: pickHelp(level,
			"NetworkPolicy sem regras ingress e com policyTypes [Ingress] nega todo tráfego de entrada.",
			"NetworkPolicies são whitelist: sem nenhuma regra ingress listada, nada é permitido. O podSelector define quem é protegido.",
			"NetworkPolicies funcionam como whitelist por seleção: o podSelector escolhe os pods protegidos; policyTypes declara as direções controladas; e a AUSÊNCIA de regras significa negação total. É a base do isolamento zero-trust cobrado no CKS. Atenção: exige um CNI que implemente NetworkPolicy (Calico/Cilium — o kindnet do minikube padrão não aplica de fato)."),
		DocURL:     "https://kubernetes.io/docs/concepts/services-networking/network-policies/",
		DocSection: "Concepts → Network Policies",
	}
}

func tplSecurityContext(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um Pod **%s** (imagem **busybox:1.35**, comando `sleep 3600`) endurecido: deve rodar como **não-root** (runAsNonRoot + runAsUser 1000) e com **allowPrivilegeEscalation: false**.", p.Name),
			fmt.Sprintf("Crie o Pod **%s** (busybox:1.35, `sleep 3600`) com securityContext endurecido: **runAsNonRoot: true**, **runAsUser: 1000** e **allowPrivilegeEscalation: false**.\n\nDica: runAs* vai no securityContext do POD; allowPrivilegeEscalation no do CONTAINER.", p.Name),
			fmt.Sprintf("Hardening de pod, passo a passo:\n\n1. Aplique:\n```\nkubectl apply -f - <<EOF\napiVersion: v1\nkind: Pod\nmetadata:\n  name: %s\nspec:\n  securityContext:\n    runAsNonRoot: true\n    runAsUser: 1000\n  containers:\n  - name: app\n    image: busybox:1.35\n    command: ['sleep', '3600']\n    securityContext:\n      allowPrivilegeEscalation: false\nEOF\n```\n2. Confirme que está Running: como busybox roda qualquer UID, o sleep funciona com UID 1000\n3. Inspecione: `kubectl get pod %s -o yaml | grep -A3 securityContext`", p.Name, p.Name)),
		Hint:          "securityContext do pod: runAsNonRoot+runAsUser; do container: allowPrivilegeEscalation: false.",
		AnswerCommand: fmt.Sprintf("kubectl apply -f pod.yaml  # %s com runAsNonRoot, runAsUser 1000, allowPrivilegeEscalation false", p.Name),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Pod **%s** roda como **não-root** (runAsUser 1000) e está Running", p.Name),
				Hint:        pickHelp(level, "", "spec.securityContext no nível do pod.", "Use o YAML do passo 1 — runAsNonRoot e runAsUser ficam em spec.securityContext."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.spec.securityContext.runAsUser}:{.status.phase}' 2>/dev/null", p.Name),
					ExpectedContains: "1000:Running",
				},
			},
			{
				Description: "Container proíbe **escalação de privilégio**",
				Hint:        pickHelp(level, "", "allowPrivilegeEscalation no securityContext do CONTAINER.", "containers[0].securityContext.allowPrivilegeEscalation: false"),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -o jsonpath='{.spec.containers[0].securityContext.allowPrivilegeEscalation}' 2>/dev/null", p.Name),
					ExpectedContains: "false",
				},
			},
		},
		Teardown: []string{fmt.Sprintf("kubectl delete pod %s --ignore-not-found=true", p.Name)},
		Explanation: pickHelp(level,
			"securityContext endurece o pod: não-root + sem escalação de privilégio.",
			"runAsNonRoot impede UID 0; runAsUser fixa o UID; allowPrivilegeEscalation: false bloqueia setuid/sudo dentro do container.",
			"Os três pilares do hardening básico de CKS: runAsNonRoot (kubelet recusa iniciar como root), runAsUser (UID explícito), allowPrivilegeEscalation: false (nem setuid escala). Campos de usuário ficam no securityContext do POD (herdado); os de capacidade/escala no do CONTAINER. O Pod Security Standard 'restricted' exige exatamente isso."),
		DocURL:     "https://kubernetes.io/docs/tasks/configure-pod-container/security-context/",
		DocSection: "Tasks → Security Context",
	}
}

func tplNamespaceQuota(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie o namespace **%s** com uma **ResourceQuota** limitando a **5 pods**, e rode um pod nginx dentro dele.", p.NS),
			fmt.Sprintf("Crie o namespace **%s** com **ResourceQuota** de **5 pods** e um pod nginx dentro dele.\n\nDica: `kubectl create ns` + `kubectl create quota --hard=pods=5 -n ...`.", p.NS),
			fmt.Sprintf("Namespaces e quotas, passo a passo:\n\n1. `kubectl create namespace %s`\n2. `kubectl create quota %s-quota --hard=pods=5 -n %s`\n3. `kubectl run %s --image=nginx:1.21 -n %s`\n4. Veja o consumo: `kubectl describe quota -n %s` (used: 1/5)", p.NS, p.NS, p.NS, p.Name, p.NS, p.NS)),
		Hint:          fmt.Sprintf("kubectl create ns %s; kubectl create quota %s-quota --hard=pods=5 -n %s; kubectl run %s --image=nginx:1.21 -n %s", p.NS, p.NS, p.NS, p.Name, p.NS),
		AnswerCommand: fmt.Sprintf("kubectl create ns %s; kubectl create quota %s-quota --hard=pods=5 -n %s; kubectl run %s --image=nginx:1.21 -n %s", p.NS, p.NS, p.NS, p.Name, p.NS),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Quota de **5 pods** ativa no namespace **%s**", p.NS),
				Hint:        pickHelp(level, "", "kubectl create quota --hard=pods=5 -n <ns>", fmt.Sprintf("kubectl create quota %s-quota --hard=pods=5 -n %s", p.NS, p.NS)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get quota -n %s -o jsonpath='{.items[0].spec.hard.pods}' 2>/dev/null", p.NS),
					ExpectedContains: "5",
				},
			},
			{
				Description: fmt.Sprintf("Pod **%s** Running dentro de **%s**", p.Name, p.NS),
				Hint:        pickHelp(level, "", "kubectl run ... -n <ns>", fmt.Sprintf("kubectl run %s --image=nginx:1.21 -n %s", p.Name, p.NS)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get pod %s -n %s -o jsonpath='{.status.phase}' 2>/dev/null", p.Name, p.NS),
					ExpectedContains: "Running",
				},
			},
		},
		Teardown: []string{fmt.Sprintf("kubectl delete namespace %s --ignore-not-found=true --wait=false", p.NS)},
		Explanation: pickHelp(level,
			"Namespaces isolam recursos; ResourceQuota limita o consumo por namespace.",
			"Namespace é a unidade de multi-tenancy; a quota (hard: pods=5) rejeita a criação do 6º pod com erro claro.",
			"Namespaces particionam o cluster (nomes, RBAC, quotas, network policies por escopo). ResourceQuota impõe limites agregados — pods, cpu, memória, PVCs. Quando estourada, o API server REJEITA a criação (403 exceeded quota), não mata nada existente. describe quota mostra used vs hard — ótimo para diagnóstico."),
		DocURL:     "https://kubernetes.io/docs/concepts/policy/resource-quotas/",
		DocSection: "Concepts → Policy → Resource Quotas",
	}
}

func tplJob(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um Job **%s-job** (busybox:1.35) que roda `echo concluido` e complete com sucesso.", p.Name),
			fmt.Sprintf("Crie um Job **%s-job** com busybox:1.35 rodando `echo concluido`.\n\nDica: `kubectl create job --image=... -- <comando>`.", p.Name),
			fmt.Sprintf("Jobs, passo a passo:\n\n1. `kubectl create job %s-job --image=busybox:1.35 -- sh -c 'echo concluido'`\n2. Acompanhe: `kubectl get jobs -w` até COMPLETIONS 1/1\n3. Logs: `kubectl logs job/%s-job` → \"concluido\"\n\nJobs rodam tarefas até completar (ao contrário de Deployments, que mantêm rodando).", p.Name, p.Name)),
		Hint:          fmt.Sprintf("kubectl create job %s-job --image=busybox:1.35 -- sh -c 'echo concluido'", p.Name),
		AnswerCommand: fmt.Sprintf("kubectl create job %s-job --image=busybox:1.35 -- sh -c 'echo concluido'", p.Name),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Job **%s-job** completou (**succeeded=1**)", p.Name),
				Hint:        pickHelp(level, "", "kubectl create job --image -- <cmd>; aguarde completar.", fmt.Sprintf("kubectl create job %s-job --image=busybox:1.35 -- sh -c 'echo concluido' — aguarde ~10s e cheque de novo.", p.Name)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get job %s-job -o jsonpath='{.status.succeeded}' 2>/dev/null", p.Name),
					ExpectedContains: "1",
				},
			},
		},
		Teardown: []string{fmt.Sprintf("kubectl delete job %s-job --ignore-not-found=true", p.Name)},
		Explanation: pickHelp(level,
			"Jobs executam pods até completarem com sucesso.",
			"O Job cria pods e os re-executa (backoffLimit) até atingir completions. status.succeeded conta as execuções bem-sucedidas.",
			"Job = tarefa finita: o controller cria pods até .spec.completions sucessos, re-tentando falhas até backoffLimit. restartPolicy do template deve ser Never/OnFailure. Para agendamento recorrente, CronJob envolve um Job com cron schedule. kubectl logs job/<nome> pega os logs do pod gerado."),
		DocURL:     "https://kubernetes.io/docs/concepts/workloads/controllers/job/",
		DocSection: "Concepts → Workloads → Jobs",
	}
}

func tplRolloutRollback(p params, level int, cert string) models.Question {
	badImage := "nginx:1.25"
	goodImage := "nginx:1.21"
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("O Deployment **%s** ja existe. Atualize a imagem para **%s** usando rolling update, verifique o rollout e depois faca rollback para **%s**.", p.Name, badImage, goodImage),
			fmt.Sprintf("Treino CKA de rollout/rollback: atualize **%s** para **%s**, acompanhe o rollout e reverta para **%s**.\n\nDica: use `kubectl set image`, `kubectl rollout status` e `kubectl rollout undo`.", p.Name, badImage, goodImage),
			fmt.Sprintf("Rollout e rollback passo a passo:\n\n1. O setup cria **%s** com imagem **%s**\n2. Atualize: `kubectl set image deployment/%s nginx=%s`\n3. Aguarde: `kubectl rollout status deployment/%s`\n4. Reverta: `kubectl rollout undo deployment/%s`\n5. Confirme que a imagem voltou para **%s**\n\nEsse tipo de tarefa cai bem no dominio CKA Workloads & Scheduling: deployments, updates e recuperacao rapida.", p.Name, goodImage, p.Name, badImage, p.Name, p.Name, goodImage)),
		Hint:          fmt.Sprintf("kubectl set image deployment/%s nginx=%s; kubectl rollout status deployment/%s; kubectl rollout undo deployment/%s", p.Name, badImage, p.Name, p.Name),
		AnswerCommand: fmt.Sprintf("kubectl set image deployment/%s nginx=%s; kubectl rollout status deployment/%s; kubectl rollout undo deployment/%s", p.Name, badImage, p.Name, p.Name),
		Setup: []models.SetupStep{
			{Description: fmt.Sprintf("Criando Deployment %s...", p.Name), Command: fmt.Sprintf("kubectl create deployment %s --image=%s --replicas=2 2>/dev/null || true; kubectl rollout status deployment/%s --timeout=90s 2>/dev/null || true", p.Name, goodImage, p.Name)},
			{Description: "Registrando revisao inicial...", Command: fmt.Sprintf("kubectl annotate deployment/%s kubernetes.io/change-cause='baseline %s' --overwrite 2>/dev/null || true", p.Name, goodImage)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Deployment **%s** voltou para imagem **%s**", p.Name, goodImage),
				Hint:        pickHelp(level, "", "Use rollout undo depois de atualizar.", "Depois do set image e rollout status, rode kubectl rollout undo deployment/"+p.Name+"."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null", p.Name),
					ExpectedContains: goodImage,
				},
			},
			{
				Description: "Historico de rollout tem pelo menos **2 revisoes**",
				Hint:        pickHelp(level, "", "rollout history mostra as revisoes.", "Use kubectl rollout history deployment/"+p.Name+" e confirme que houve update/rollback."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl rollout history deployment/%s 2>/dev/null | grep -c '^REVISION' >/dev/null; kubectl rollout history deployment/%s 2>/dev/null | grep -E '^[0-9]+' | wc -l", p.Name, p.Name),
					ExpectedContains: "2",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name)},
		Explanation: "Deployment guarda historico de ReplicaSets para permitir rollout progressivo e rollback. Em CKA, isso valida dominio de Workloads: atualizar com seguranca, acompanhar status e recuperar rapidamente.",
		DocURL:      "https://kubernetes.io/docs/concepts/workloads/controllers/deployment/",
		DocSection:  "Workloads -> Deployments -> Updating and rolling back",
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// MODO INCIDENTE — o anti-lab: o setup QUEBRA o cluster de propósito e o
// usuário diagnostica e conserta contra o relógio. Treino de troubleshooting
// real, o que separa aprovado de reprovado no CKA.
// ─────────────────────────────────────────────────────────────────────────────

func tplHPA(p params, level int, cert string) models.Question {
	hpa := p.Name + "-hpa"
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie um **HorizontalPodAutoscaler** chamado **%s** para o Deployment **%s**, com **min=2**, **max=5** e alvo de **50%% de CPU**.", hpa, p.Name),
			fmt.Sprintf("Crie um HPA para o Deployment **%s**: **min=2**, **max=5**, **CPU 50%%**.\n\nDica: o Metrics Server ja sera instalado pelo setup; use `kubectl autoscale deployment`.", p.Name),
			fmt.Sprintf("Autoscaling passo a passo:\n\n1. O setup instala o Metrics Server e cria o Deployment **%s** com request de CPU\n2. Crie o HPA: `kubectl autoscale deployment %s --name=%s --cpu-percent=50 --min=2 --max=5`\n3. Confira: `kubectl get hpa %s`\n\nHPA ajusta `.spec.replicas` do Deployment usando metricas coletadas pelo Metrics Server.", p.Name, p.Name, hpa, hpa)),
		Hint:          fmt.Sprintf("kubectl autoscale deployment %s --name=%s --cpu-percent=50 --min=2 --max=5", p.Name, hpa),
		AnswerCommand: fmt.Sprintf("kubectl autoscale deployment %s --name=%s --cpu-percent=50 --min=2 --max=5", p.Name, hpa),
		Setup: []models.SetupStep{
			{
				Description: "Instalando Metrics Server se necessario...",
				Command:     `kubectl get deploy metrics-server -n kube-system >/dev/null 2>&1 || kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml 2>&1 | tail -3; kubectl get deploy metrics-server -n kube-system -o jsonpath='{.spec.template.spec.containers[0].args}' 2>/dev/null | grep -q -- '--kubelet-insecure-tls' || kubectl patch deployment metrics-server -n kube-system --type=json -p='[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--kubelet-insecure-tls"}]' 2>/dev/null || true; kubectl rollout status deployment/metrics-server -n kube-system --timeout=180s 2>/dev/null || true`,
			},
			{
				Description: fmt.Sprintf("Criando workload alvo %s com requests de CPU...", p.Name),
				Command:     fmt.Sprintf("kubectl create deployment %s --image=registry.k8s.io/hpa-example --port=80 2>/dev/null || true; kubectl set resources deployment/%s --requests=cpu=100m --limits=cpu=200m 2>/dev/null || true; kubectl rollout status deployment/%s --timeout=120s 2>/dev/null || true", p.Name, p.Name, p.Name),
			},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("HPA **%s** aponta para o Deployment **%s**", hpa, p.Name),
				Hint:        pickHelp(level, "", "kubectl autoscale deployment ... --name ...", fmt.Sprintf("kubectl autoscale deployment %s --name=%s --cpu-percent=50 --min=2 --max=5", p.Name, hpa)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get hpa %s -o jsonpath='{.spec.scaleTargetRef.name}:{.spec.minReplicas}:{.spec.maxReplicas}' 2>/dev/null", hpa),
					ExpectedContains: fmt.Sprintf("%s:2:5", p.Name),
				},
			},
			{
				Description: "HPA usa alvo de **50% CPU**",
				Hint:        pickHelp(level, "", "Confira o campo averageUtilization.", "No YAML do HPA, spec.metrics[0].resource.target.averageUtilization deve ser 50."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get hpa %s -o jsonpath='{.spec.metrics[0].resource.target.averageUtilization}' 2>/dev/null", hpa),
					ExpectedContains: "50",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete hpa %s --ignore-not-found=true", hpa),
			fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name),
		},
		Explanation: pickHelp(level,
			"HPA escala replicas de um workload com base em metricas como CPU.",
			"O HorizontalPodAutoscaler observa metricas do Metrics Server e altera o numero desejado de replicas do Deployment dentro dos limites min/max.",
			"O HPA nao cria pods diretamente: ele escreve em `.spec.replicas` do alvo. O Metrics Server fornece as metricas de CPU/memoria; por isso o lab instala esse recurso antes. Requests de CPU sao importantes porque a porcentagem de utilizacao e calculada em relacao ao request."),
		DocURL:     "https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/",
		DocSection: "Tasks -> Horizontal Pod Autoscaling",
	}
}

func tplArgoCDApplication(p params, level int, cert string) models.Question {
	app := p.Name + "-app"
	ns := p.NS
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Crie uma **Application** do ArgoCD chamada **%s** no namespace **argocd**, apontando para o repo **argoproj/argocd-example-apps**, path **guestbook**, destino **%s**.", app, ns),
			fmt.Sprintf("Crie uma Application ArgoCD **%s** usando o repo `https://github.com/argoproj/argocd-example-apps.git`, path `guestbook`, destino `https://kubernetes.default.svc` e namespace `%s`.\n\nDica: use o kind `Application` do grupo `argoproj.io/v1alpha1`.", app, ns),
			fmt.Sprintf("GitOps com ArgoCD, passo a passo:\n\n1. O setup instala o ArgoCD no cluster se ele ainda nao existir\n2. Crie uma Application chamada **%s** em `argocd`\n3. Use `repoURL: https://github.com/argoproj/argocd-example-apps.git`, `path: guestbook`, `targetRevision: HEAD`\n4. Defina o destino como `server: https://kubernetes.default.svc` e `namespace: %s`\n5. Habilite `syncPolicy.automated.prune: true` e `selfHeal: true`\n\nAssim o ArgoCD passa a reconciliar o estado desejado a partir do Git.", app, ns)),
		Hint:          "apiVersion: argoproj.io/v1alpha1; kind: Application; spec.source.repoURL/path; spec.destination.server/namespace; syncPolicy.automated",
		AnswerCommand: fmt.Sprintf("kubectl apply -f app.yaml  # Application %s apontando para guestbook", app),
		Setup: []models.SetupStep{
			{
				Description: "Instalando ArgoCD se necessario...",
				Command:     `kubectl create namespace argocd 2>/dev/null || true; kubectl get deployment argocd-server -n argocd >/dev/null 2>&1 || kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml 2>&1 | tail -5; kubectl wait deployment argocd-server -n argocd --for=condition=available --timeout=300s 2>/dev/null || true`,
			},
			{
				Description: fmt.Sprintf("Criando namespace destino %s...", ns),
				Command:     fmt.Sprintf("kubectl create namespace %s 2>/dev/null || true", ns),
			},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Application **%s** existe em **argocd** apontando para **guestbook**", app),
				Hint:        pickHelp(level, "", "Confira repoURL e path no spec.source.", "Crie o manifest Application com metadata.namespace: argocd e spec.source.path: guestbook."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get application %s -n argocd -o jsonpath='{.spec.source.path}:{.spec.destination.namespace}' 2>/dev/null", app),
					ExpectedContains: fmt.Sprintf("guestbook:%s", ns),
				},
			},
			{
				Description: "Sync automatico esta habilitado",
				Hint:        pickHelp(level, "", "syncPolicy.automated deve existir.", "Use spec.syncPolicy.automated.prune: true e selfHeal: true."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get application %s -n argocd -o jsonpath='{.spec.syncPolicy.automated.prune}:{.spec.syncPolicy.automated.selfHeal}' 2>/dev/null", app),
					ExpectedContains: "true:true",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete application %s -n argocd --ignore-not-found=true", app),
			fmt.Sprintf("kubectl delete namespace %s --ignore-not-found=true --wait=false", ns),
		},
		Explanation: pickHelp(level,
			"ArgoCD Application liga um repositorio Git a um destino Kubernetes.",
			"A Application e o recurso central do ArgoCD: source define onde esta o desejado; destination define onde aplicar; syncPolicy automatiza a reconciliacao.",
			"GitOps separa intencao e execucao: o estado desejado fica no Git, o ArgoCD observa o repo e reconcilia o cluster. Automated sync aplica mudancas sem clique manual, prune remove recursos que sairam do Git e selfHeal corrige drift feito diretamente no cluster."),
		DocURL:     "https://argo-cd.readthedocs.io/en/stable/operator-manual/declarative-setup/",
		DocSection: "ArgoCD -> Declarative setup -> Applications",
	}
}

func tplAWSCompute(p params, level int, cert string) models.Question {
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Fundamentos AWS Compute: crie uma aplicacao estilo **EC2/EKS workload** com um Deployment **%s** usando imagem **nginx:1.25** e **2 replicas**.", p.Name),
			fmt.Sprintf("Fundamentos AWS Compute: crie um Deployment **%s** com **2 replicas** da imagem **nginx:1.25**.\n\nPense nele como o primeiro bloco de compute: em EC2 voce gerencia instancias; em EKS voce declara workloads e o control plane agenda pods nos nodes.", p.Name),
			fmt.Sprintf("AWS Compute passo a passo:\n\n1. Crie o workload: `kubectl create deployment %s --image=nginx:1.25 --replicas=2`\n2. Aguarde: `kubectl rollout status deployment/%s`\n3. Confira as replicas: `kubectl get deploy %s`\n\nNa AWS, EC2 entrega maquinas virtuais; EKS entrega Kubernetes gerenciado para executar workloads em nodes EC2/Fargate.", p.Name, p.Name, p.Name)),
		Hint:          fmt.Sprintf("kubectl create deployment %s --image=nginx:1.25 --replicas=2", p.Name),
		AnswerCommand: fmt.Sprintf("kubectl create deployment %s --image=nginx:1.25 --replicas=2", p.Name),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Deployment **%s** existe com imagem **nginx:1.25**", p.Name),
				Hint:        pickHelp(level, "", "Use kubectl create deployment com --image.", fmt.Sprintf("kubectl create deployment %s --image=nginx:1.25 --replicas=2", p.Name)),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.spec.template.spec.containers[0].image}' 2>/dev/null", p.Name),
					ExpectedContains: "nginx:1.25",
				},
			},
			{
				Description: "O workload tem **2 replicas** desejadas",
				Hint:        pickHelp(level, "", "Confira .spec.replicas.", "Use --replicas=2 no create deployment ou kubectl scale depois."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.spec.replicas}' 2>/dev/null", p.Name),
					ExpectedContains: "2",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name)},
		Explanation: "AWS Compute comeca por EC2, Auto Scaling e, em ambientes Kubernetes, EKS. Este lab usa Kubernetes para praticar a ideia central: declarar capacidade desejada e deixar o control plane reconciliar.",
		DocURL:      "https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/concepts.html",
		DocSection:  "AWS -> EC2 concepts",
	}
}

func tplAWSNetworking(p params, level int, cert string) models.Question {
	ns := p.NS
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Fundamentos AWS Networking/VPC: crie o namespace **%s** e uma **NetworkPolicy** chamada **%s-deny-ingress** que bloqueia todo trafego de entrada para pods com label `app=%s`.", ns, p.Name, p.Name),
			fmt.Sprintf("Fundamentos AWS Networking/VPC: isole a aplicacao **%s** no namespace **%s** com uma NetworkPolicy deny-all ingress.\n\nPense nisso como o paralelo Kubernetes para isolamento de subnets/security groups em uma VPC.", p.Name, ns),
			fmt.Sprintf("AWS Networking passo a passo:\n\n1. O setup cria namespace e pod alvo\n2. Aplique uma NetworkPolicy **%s-deny-ingress** em `%s`\n3. Use `podSelector.matchLabels.app: %s`\n4. Defina `policyTypes: [Ingress]` sem regras `ingress`\n\nEm AWS, VPC/subnets/security groups controlam alcance de rede; em Kubernetes, NetworkPolicy expressa isolamento entre pods.", p.Name, ns, p.Name)),
		Hint:          fmt.Sprintf("NetworkPolicy em %s com podSelector app=%s e policyTypes [Ingress] sem ingress = deny all.", ns, p.Name),
		AnswerCommand: fmt.Sprintf("kubectl apply -f networkpolicy.yaml  # %s/%s-deny-ingress", ns, p.Name),
		Setup: []models.SetupStep{
			{Description: fmt.Sprintf("Criando namespace %s...", ns), Command: fmt.Sprintf("kubectl create namespace %s 2>/dev/null || true", ns)},
			{Description: "Criando pod alvo para isolamento...", Command: fmt.Sprintf("kubectl run %s --image=nginx:1.25 --labels=app=%s -n %s --restart=Never 2>/dev/null || true", p.Name, p.Name, ns)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("NetworkPolicy **%s-deny-ingress** seleciona pods `app=%s`", p.Name, p.Name),
				Hint:        pickHelp(level, "", "Use spec.podSelector.matchLabels.app.", "metadata.name deve ser "+p.Name+"-deny-ingress e spec.podSelector.matchLabels.app deve ser "+p.Name+"."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get networkpolicy %s-deny-ingress -n %s -o jsonpath='{.spec.podSelector.matchLabels.app}' 2>/dev/null", p.Name, ns),
					ExpectedContains: p.Name,
				},
			},
			{
				Description: "A politica controla **Ingress**",
				Hint:        pickHelp(level, "", "policyTypes precisa conter Ingress.", "spec.policyTypes: [Ingress], sem regras ingress."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get networkpolicy %s-deny-ingress -n %s -o jsonpath='{.spec.policyTypes}' 2>/dev/null", p.Name, ns),
					ExpectedContains: "Ingress",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf("kubectl delete namespace %s --ignore-not-found=true --wait=false", ns)},
		Explanation: "Em AWS, VPC, subnets, route tables e security groups definem fronteiras de rede. Em Kubernetes, NetworkPolicy treina o mesmo raciocinio: quem pode falar com quem e em qual direcao.",
		DocURL:      "https://docs.aws.amazon.com/vpc/latest/userguide/what-is-amazon-vpc.html",
		DocSection:  "AWS -> VPC fundamentals",
	}
}

func tplAWSIAM(p params, level int, cert string) models.Question {
	user := p.Name + "-user"
	policy := p.Name + "-s3-list"
	policyDoc := `'{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:ListAllMyBuckets"],"Resource":"*"}]}'`
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Fundamentos AWS IAM: usando LocalStack, crie o usuario IAM **%s** e anexe uma policy inline **%s** permitindo listar buckets S3.", user, policy),
			fmt.Sprintf("Fundamentos AWS IAM: crie o usuario **%s** no LocalStack e uma policy inline **%s** com permissao `s3:ListAllMyBuckets`.\n\nDica: execute `awslocal iam ...` dentro do Deployment localstack.", user, policy),
			fmt.Sprintf("AWS IAM passo a passo:\n\n1. O setup instala/verifica o LocalStack\n2. Crie o usuario: `kubectl -n tools exec deploy/localstack -- awslocal iam create-user --user-name %s`\n3. Anexe policy inline com `put-user-policy` e Action `s3:ListAllMyBuckets`\n4. Valide com `list-user-policies`\n\nIAM combina identidade, permissao e escopo. LocalStack permite praticar a API sem conta AWS.", user)),
		Hint:          fmt.Sprintf("awslocal iam create-user --user-name %s; awslocal iam put-user-policy --user-name %s --policy-name %s --policy-document <json>", user, user, policy),
		AnswerCommand: fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal iam create-user --user-name %s; kubectl -n tools exec deploy/localstack -- awslocal iam put-user-policy --user-name %s --policy-name %s --policy-document %s", user, user, policy, policyDoc),
		Setup:         localStackSetupSteps(),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Usuario IAM **%s** existe no LocalStack", user),
				Hint:        pickHelp(level, "", "Use awslocal iam create-user.", "Comando completo: kubectl -n tools exec deploy/localstack -- awslocal iam create-user --user-name "+user),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal iam get-user --user-name %s >/dev/null 2>&1 && echo OK || echo FAIL", user),
					ExpectedContains: "OK",
				},
			},
			{
				Description: fmt.Sprintf("Policy inline **%s** esta anexada ao usuario", policy),
				Hint:        pickHelp(level, "", "Use awslocal iam put-user-policy.", "A policy deve ser inline no usuario "+user+" com nome "+policy+"."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal iam list-user-policies --user-name %s --query 'PolicyNames[0]' --output text 2>/dev/null", user),
					ExpectedContains: policy,
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal iam delete-user-policy --user-name %s --policy-name %s >/dev/null 2>&1 || true", user, policy),
			fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal iam delete-user --user-name %s >/dev/null 2>&1 || true", user),
		},
		Explanation: "IAM aplica minimo privilegio com identidades e policies. Este lab usa a API IAM real emulada pelo LocalStack, mantendo o treino seguro e sem credenciais reais.",
		DocURL:      "https://docs.aws.amazon.com/IAM/latest/UserGuide/introduction.html",
		DocSection:  "AWS -> IAM introduction",
	}
}

func tplAWSMessaging(p params, level int, cert string) models.Question {
	queue := p.Name + "-queue"
	body := "msg-" + p.Name
	createCmd := fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal sqs create-queue --queue-name %s", queue)
	sendCmd := fmt.Sprintf("QURL=$(kubectl -n tools exec deploy/localstack -- awslocal sqs get-queue-url --queue-name %s --query QueueUrl --output text); kubectl -n tools exec deploy/localstack -- awslocal sqs send-message --queue-url \"$QURL\" --message-body %s", queue, body)
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Fundamentos AWS Messaging: usando LocalStack, crie uma fila SQS **%s** e envie uma mensagem com corpo **%s**.", queue, body),
			fmt.Sprintf("Fundamentos AWS SQS: crie a fila **%s** e envie a mensagem **%s**.\n\nDica: use `awslocal sqs` dentro do Deployment localstack.", queue, body),
			fmt.Sprintf("AWS SQS passo a passo:\n\n1. O setup instala/verifica o LocalStack\n2. Crie a fila: `%s`\n3. Pegue a URL da fila com `get-queue-url`\n4. Envie a mensagem: `%s`\n5. Valide com `receive-message`\n\nSQS desacopla produtores e consumidores por mensagens persistidas em fila.", createCmd, sendCmd)),
		Hint:          fmt.Sprintf("awslocal sqs create-queue --queue-name %s; depois get-queue-url e send-message.", queue),
		AnswerCommand: createCmd + "; " + sendCmd,
		Setup:         localStackSetupSteps(),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Fila SQS **%s** existe", queue),
				Hint:        pickHelp(level, "", "Use awslocal sqs create-queue.", "Comando completo: "+createCmd),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal sqs list-queues --queue-name-prefix %s --query 'QueueUrls[0]' --output text 2>/dev/null", queue),
					ExpectedContains: queue,
				},
			},
			{
				Description: fmt.Sprintf("Mensagem **%s** esta disponivel na fila", body),
				Hint:        pickHelp(level, "", "Use send-message com a QueueUrl.", "Recupere QURL com get-queue-url e envie message-body "+body+"."),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("QURL=$(kubectl -n tools exec deploy/localstack -- awslocal sqs get-queue-url --queue-name %s --query QueueUrl --output text 2>/dev/null); kubectl -n tools exec deploy/localstack -- awslocal sqs receive-message --queue-url \"$QURL\" --visibility-timeout 0 --wait-time-seconds 1 --query 'Messages[0].Body' --output text 2>/dev/null", queue),
					ExpectedContains: body,
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("QURL=$(kubectl -n tools exec deploy/localstack -- awslocal sqs get-queue-url --queue-name %s --query QueueUrl --output text 2>/dev/null); [ -n \"$QURL\" ] && kubectl -n tools exec deploy/localstack -- awslocal sqs delete-queue --queue-url \"$QURL\" >/dev/null 2>&1 || true", queue),
		},
		Explanation: "SQS e um servico de fila gerenciado para desacoplar componentes. O lab usa LocalStack para praticar create-queue, send-message e receive-message com comandos AWS reais.",
		DocURL:      "https://docs.aws.amazon.com/AWSSimpleQueueService/latest/SQSDeveloperGuide/welcome.html",
		DocSection:  "AWS -> SQS basics",
	}
}

func tplAWSStorage(p params, level int, cert string) models.Question {
	bucket := "lab-" + p.Name + "-bucket"
	key := "hello.txt"
	putCmd := fmt.Sprintf("printf 'hello from %s\\n' | kubectl -n tools exec -i deploy/localstack -- awslocal s3 cp - s3://%s/%s", p.Name, bucket, key)
	return models.Question{
		Question: pickHelp(level,
			fmt.Sprintf("Fundamentos AWS Storage: usando o LocalStack ja instalado no cluster, crie um bucket S3 **%s** e envie o objeto **%s**.", bucket, key),
			fmt.Sprintf("Fundamentos AWS Storage/S3: crie o bucket **%s** no LocalStack e envie um arquivo **%s**.\n\nDica: execute os comandos AWS pelo pod do LocalStack com `kubectl -n tools exec deploy/localstack -- awslocal ...`.", bucket, key),
			fmt.Sprintf("AWS Storage passo a passo:\n\n1. O setup instala/verifica o LocalStack no namespace `tools`\n2. Crie o bucket: `kubectl -n tools exec deploy/localstack -- awslocal s3 mb s3://%s`\n3. Envie o objeto: `%s`\n4. Valide com `awslocal s3 ls s3://%s`\n\nS3 armazena objetos em buckets. LocalStack permite praticar a API real sem conta AWS, credenciais ou custo.", bucket, putCmd, bucket)),
		Hint:          fmt.Sprintf("Use awslocal dentro do deployment localstack: s3 mb s3://%s e depois s3 cp - s3://%s/%s.", bucket, bucket, key),
		AnswerCommand: fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal s3 mb s3://%s; %s", bucket, putCmd),
		Setup:         localStackSetupSteps(),
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Bucket S3 **%s** existe no LocalStack", bucket),
				Hint:        pickHelp(level, "", "Use awslocal s3 mb s3://<bucket> dentro do deployment localstack.", "Comando completo: kubectl -n tools exec deploy/localstack -- awslocal s3 mb s3://"+bucket),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal s3api head-bucket --bucket %s >/dev/null 2>&1 && echo OK || echo FAIL", bucket),
					ExpectedContains: "OK",
				},
			},
			{
				Description: fmt.Sprintf("Objeto **%s** foi enviado para o bucket", key),
				Hint:        pickHelp(level, "", "Envie conteudo para s3://bucket/chave com awslocal s3 cp.", "Comando completo: "+putCmd),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal s3api head-object --bucket %s --key %s >/dev/null 2>&1 && echo OK || echo FAIL", bucket, key),
					ExpectedContains: "OK",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl -n tools exec deploy/localstack -- awslocal s3 rb s3://%s --force >/dev/null 2>&1 || true", bucket),
		},
		Explanation: "AWS Storage inicial costuma separar objeto (S3), bloco (EBS) e arquivo (EFS). Este lab pratica S3 com LocalStack: a API e os comandos sao de AWS, mas o endpoint roda dentro do AKS como recurso de estudo.",
		DocURL:      "https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html",
		DocSection:  "AWS -> Storage fundamentals",
	}
}

func tplLinuxLogProcessing(p params, level int, cert string) models.Question {
	dir := labWorkspace("linux-" + p.Name)
	return models.Question{
		Question: fmt.Sprintf("No diretório **%s**, escreva um script Linux chamado **analyze.sh** que leia **access.log** e gere **summary.txt** com a quantidade de linhas de erro e o primeiro IP que gerou erro.", dir),
		Hint:     "Combine `grep`/`awk` e redirecionamento. O arquivo final precisa ter as chaves `errors=` e `first_error_ip=`.",
		AnswerCommand: fmt.Sprintf(`cd "%[1]s" && cat > analyze.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
errors=$(grep -c 'ERROR' access.log || true)
first_ip=$(awk '/ERROR/ {print $1; exit}' access.log)
{
  echo "errors=${errors}"
  echo "first_error_ip=${first_ip}"
} > summary.txt
EOF
chmod +x analyze.sh
./analyze.sh`, dir),
		Setup: []models.SetupStep{{
			Description: "Preparando arquivo de log para análise Linux...",
			Command: fmt.Sprintf(`mkdir -p "%[1]s"; cat > "%[1]s/access.log" <<'EOF'
10.0.0.2 INFO start
10.0.0.5 ERROR timeout
10.0.0.8 INFO ok
10.0.0.5 ERROR retry
EOF`, dir),
		}},
		Goals: []models.Goal{
			{
				Description: "Script **analyze.sh** existe e é executável",
				Hint:        "Depois de criar o script, ajuste permissão de execução.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`test -x "%s/analyze.sh" && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
			{
				Description: "**summary.txt** contém `errors=2` e `first_error_ip=10.0.0.5`",
				Hint:        "Execute o script e confira o conteúdo de `summary.txt`.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`cd "%[1]s" && ./analyze.sh >/dev/null 2>&1; grep -q '^errors=2$' summary.txt && grep -q '^first_error_ip=10.0.0.5$' summary.txt && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf(`rm -rf "%s"`, dir)},
		Explanation: "Este lab treina pipeline básico de shell: filtragem com grep, extração com awk, variáveis e redirecionamento para arquivo. É um fluxo real de troubleshooting em Linux.",
		DocURL:      "https://man7.org/linux/man-pages/",
		DocSection:  "Linux man-pages",
	}
}

func tplLinuxPermissions(p params, level int, cert string) models.Question {
	dir := labWorkspace("linux-" + p.Name + "-perm")
	return models.Question{
		Question:      fmt.Sprintf("No diretório **%s**, corrija permissões de um script operacional: ele deve ser executável pelo dono, não gravável por grupo/outros, e ao executar deve criar **run.out**.", dir),
		Hint:          "Use permissões numéricas ou simbólicas. O modo final esperado para o script é restrito, mas executável pelo dono.",
		AnswerCommand: fmt.Sprintf(`cd "%[1]s" && chmod 700 deploy.sh && ./deploy.sh`, dir),
		Setup: []models.SetupStep{{
			Description: "Preparando script com permissões incorretas...",
			Command: fmt.Sprintf(`mkdir -p "%[1]s"; cat > "%[1]s/deploy.sh" <<'EOF'
#!/usr/bin/env bash
echo deployed > run.out
EOF
chmod 600 "%[1]s/deploy.sh"; rm -f "%[1]s/run.out"`, dir),
		}},
		Goals: []models.Goal{
			{
				Description: "**deploy.sh** está executável e protegido contra escrita por grupo/outros",
				Hint:        "Verifique o modo com `stat`; ajuste com `chmod`.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`mode=$(stat -c '%%a' "%s/deploy.sh" 2>/dev/null); [ "$mode" = "700" ] && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
			{
				Description: "Executar o script gera **run.out** com o conteúdo esperado",
				Hint:        "Depois da permissão correta, execute o script a partir do diretório do lab.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`cd "%[1]s" && ./deploy.sh >/dev/null 2>&1; grep -q '^deployed$' run.out && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf(`rm -rf "%s"`, dir)},
		Explanation: "Permissão de execução é necessária para rodar scripts diretamente. `700` deixa o dono ler, escrever e executar, removendo acesso de grupo/outros.",
		DocURL:      "https://man7.org/linux/man-pages/man1/chmod.1.html",
		DocSection:  "Linux -> chmod",
	}
}

func tplBashCSVReport(p params, level int, cert string) models.Question {
	dir := labWorkspace("bash-" + p.Name)
	return models.Question{
		Question: fmt.Sprintf("No diretório **%s**, programe **report.sh** para ler **users.csv** e gerar **active-users.txt** somente com os nomes dos usuários ativos, um por linha.", dir),
		Hint:     "Leia CSV linha a linha. Ignore o cabeçalho e filtre linhas onde o status é `active`.",
		AnswerCommand: fmt.Sprintf(`cd "%[1]s" && cat > report.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
tail -n +2 users.csv | awk -F, '$3=="active" {print $2}' > active-users.txt
EOF
chmod +x report.sh
./report.sh`, dir),
		Setup: []models.SetupStep{{
			Description: "Preparando CSV para processamento com Bash...",
			Command: fmt.Sprintf(`mkdir -p "%[1]s"; cat > "%[1]s/users.csv" <<'EOF'
id,name,status
1,Ana,active
2,Bruno,inactive
3,Carla,active
EOF`, dir),
		}},
		Goals: []models.Goal{
			{
				Description: "**active-users.txt** contém Ana e Carla, sem Bruno",
				Hint:        "A terceira coluna define se o usuário está ativo.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`cd "%[1]s" && bash report.sh >/dev/null 2>&1; grep -qx 'Ana' active-users.txt && grep -qx 'Carla' active-users.txt && ! grep -qx 'Bruno' active-users.txt && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf(`rm -rf "%s"`, dir)},
		Explanation: "O lab pratica parsing simples em Bash: separar campos, filtrar por condição e gerar arquivo de saída previsível.",
		DocURL:      "https://www.gnu.org/software/bash/manual/bash.html",
		DocSection:  "Bash manual",
	}
}

func tplBashArgs(p params, level int, cert string) models.Question {
	dir := labWorkspace("bash-" + p.Name + "-args")
	return models.Question{
		Question: fmt.Sprintf("No diretório **%s**, crie **greet.sh** para receber um nome por argumento e imprimir `hello,<nome>`. Se nenhum nome for enviado, o script deve falhar com código diferente de zero.", dir),
		Hint:     "Valide a quantidade de argumentos antes de usar `$1`; em erro, escreva uma mensagem em stderr e saia com status 1.",
		AnswerCommand: fmt.Sprintf(`cd "%[1]s" && cat > greet.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
if [ "$#" -ne 1 ]; then
  echo "usage: greet.sh <name>" >&2
  exit 1
fi
echo "hello,$1"
EOF
chmod +x greet.sh`, dir),
		Setup: []models.SetupStep{{
			Description: "Preparando workspace de argumentos Bash...",
			Command:     fmt.Sprintf(`mkdir -p "%s"; rm -f "%s/greet.sh"`, dir, dir),
		}},
		Goals: []models.Goal{
			{
				Description: "Com argumento, **greet.sh** imprime `hello,Ana`",
				Hint:        "Use `$#` para contar argumentos e `$1` para ler o primeiro.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`cd "%[1]s" && [ "$(./greet.sh Ana 2>/dev/null)" = "hello,Ana" ] && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
			{
				Description: "Sem argumento, **greet.sh** falha",
				Hint:        "Um script robusto não deve seguir com argumento ausente.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`cd "%[1]s" && ./greet.sh >/dev/null 2>&1; [ "$?" -ne 0 ] && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf(`rm -rf "%s"`, dir)},
		Explanation: "Scripts Bash reais precisam validar entrada. Este exercício treina argumentos posicionais, stderr e códigos de saída.",
		DocURL:      "https://www.gnu.org/software/bash/manual/bash.html",
		DocSection:  "Bash -> Shell Parameters",
	}
}

func tplJavaFizzBuzz(p params, level int, cert string) models.Question {
	dir := labWorkspace("java-" + p.Name)
	pod := p.Name + "-java"
	cm := pod + "-src"
	run := fmt.Sprintf(`kubectl delete pod %[1]s --ignore-not-found=true --wait=false >/dev/null 2>&1 || true; kubectl delete configmap %[2]s --ignore-not-found=true >/dev/null 2>&1 || true; kubectl create configmap %[2]s --from-file="%[3]s/App.java"; kubectl run %[1]s --image=eclipse-temurin:17-jdk --restart=Never --overrides='{"spec":{"containers":[{"name":"%[1]s","image":"eclipse-temurin:17-jdk","command":["sh","-c","cp /src/App.java /tmp/App.java && javac /tmp/App.java && java -cp /tmp App"],"volumeMounts":[{"name":"src","mountPath":"/src"}]}],"volumes":[{"name":"src","configMap":{"name":"%[2]s"}}]}}'`, pod, cm, dir)
	return models.Question{
		Question: fmt.Sprintf("No diretório **%s**, programe **App.java**. O programa deve imprimir FizzBuzz de 1 a 15 e ser executado em um pod Java no Kubernetes.", dir),
		Hint:     "Crie uma classe pública `App` com `main`. Use `%` para detectar múltiplos de 3 e 5. Depois envie o arquivo como ConfigMap e rode em uma imagem JDK.",
		AnswerCommand: fmt.Sprintf(`mkdir -p "%[1]s"; cat > "%[1]s/App.java" <<'EOF'
public class App {
  public static void main(String[] args) {
    for (int i = 1; i <= 15; i++) {
      if (i %% 15 == 0) System.out.println("FizzBuzz");
      else if (i %% 3 == 0) System.out.println("Fizz");
      else if (i %% 5 == 0) System.out.println("Buzz");
      else System.out.println(i);
    }
  }
}
EOF
%[2]s`, dir, run),
		Setup: []models.SetupStep{{
			Description: "Preparando workspace Java...",
			Command:     fmt.Sprintf(`mkdir -p "%s"; rm -f "%s/App.java"; kubectl delete pod %s --ignore-not-found=true --wait=false >/dev/null 2>&1 || true; kubectl delete configmap %s --ignore-not-found=true >/dev/null 2>&1 || true`, dir, dir, pod, cm),
		}},
		Goals: []models.Goal{
			{
				Description: "O pod Java compila e executa **App.java**",
				Hint:        "Monte o arquivo em uma imagem JDK e execute `javac` seguido de `java -cp`.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`test -f "%[3]s/App.java" || { echo FAIL; exit 0; }; kubectl delete pod %[1]s --ignore-not-found=true --wait=false >/dev/null 2>&1 || true; kubectl delete configmap %[2]s --ignore-not-found=true >/dev/null 2>&1 || true; kubectl create configmap %[2]s --from-file="%[3]s/App.java" >/dev/null 2>&1; kubectl run %[1]s --image=eclipse-temurin:17-jdk --restart=Never --overrides='{"spec":{"containers":[{"name":"%[1]s","image":"eclipse-temurin:17-jdk","command":["sh","-c","cp /src/App.java /tmp/App.java && javac /tmp/App.java && java -cp /tmp App"],"volumeMounts":[{"name":"src","mountPath":"/src"}]}],"volumes":[{"name":"src","configMap":{"name":"%[2]s"}}]}}' >/dev/null 2>&1; kubectl wait --for=condition=Ready pod/%[1]s --timeout=90s >/dev/null 2>&1 || true; kubectl logs %[1]s 2>/dev/null | grep -q 'FizzBuzz' && echo OK || echo FAIL`, pod, cm, dir),
					ExpectedContains: "OK",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf(`kubectl delete pod %s --ignore-not-found=true --wait=false; kubectl delete configmap %s --ignore-not-found=true; rm -rf "%s"`, pod, cm, dir)},
		Explanation: "O exercício força ciclo real de desenvolvimento: escrever Java, empacotar o fonte como ConfigMap e executar em um container JDK. Isso evita depender de JDK local no terminal.",
		DocURL:      "https://docs.oracle.com/en/java/",
		DocSection:  "Java documentation",
	}
}

func tplJavaWordCount(p params, level int, cert string) models.Question {
	dir := labWorkspace("java-" + p.Name + "-wc")
	pod := p.Name + "-wc"
	cm := pod + "-src"
	return models.Question{
		Question: fmt.Sprintf("No diretório **%s**, programe **App.java** para contar palavras recebidas por argumento. Ao executar com `kubernetes java lab`, a saída deve ser `words=3`.", dir),
		Hint:     "Use `args.length`. A classe precisa se chamar `App` para o comando de execução encontrar o `main`.",
		AnswerCommand: fmt.Sprintf(`mkdir -p "%[1]s"; cat > "%[1]s/App.java" <<'EOF'
public class App {
  public static void main(String[] args) {
    System.out.println("words=" + args.length);
  }
}
EOF
kubectl delete pod %[2]s --ignore-not-found=true --wait=false >/dev/null 2>&1 || true
kubectl delete configmap %[3]s --ignore-not-found=true >/dev/null 2>&1 || true
kubectl create configmap %[3]s --from-file="%[1]s/App.java"
kubectl run %[2]s --image=eclipse-temurin:17-jdk --restart=Never --overrides='{"spec":{"containers":[{"name":"%[2]s","image":"eclipse-temurin:17-jdk","command":["sh","-c","cp /src/App.java /tmp/App.java && javac /tmp/App.java && java -cp /tmp App kubernetes java lab"],"volumeMounts":[{"name":"src","mountPath":"/src"}]}],"volumes":[{"name":"src","configMap":{"name":"%[3]s"}}]}}'`, dir, pod, cm),
		Setup: []models.SetupStep{{
			Description: "Preparando workspace Java...",
			Command:     fmt.Sprintf(`mkdir -p "%s"; rm -f "%s/App.java"; kubectl delete pod %s --ignore-not-found=true --wait=false >/dev/null 2>&1 || true; kubectl delete configmap %s --ignore-not-found=true >/dev/null 2>&1 || true`, dir, dir, pod, cm),
		}},
		Goals: []models.Goal{{
			Description: "O programa Java imprime **words=3**",
			Hint:        "Compile e rode a classe `App` dentro da imagem JDK passando três argumentos.",
			Validation: &models.Validation{
				Command:          fmt.Sprintf(`test -f "%[3]s/App.java" || { echo FAIL; exit 0; }; kubectl delete pod %[1]s --ignore-not-found=true --wait=false >/dev/null 2>&1 || true; kubectl delete configmap %[2]s --ignore-not-found=true >/dev/null 2>&1 || true; kubectl create configmap %[2]s --from-file="%[3]s/App.java" >/dev/null 2>&1; kubectl run %[1]s --image=eclipse-temurin:17-jdk --restart=Never --overrides='{"spec":{"containers":[{"name":"%[1]s","image":"eclipse-temurin:17-jdk","command":["sh","-c","cp /src/App.java /tmp/App.java && javac /tmp/App.java && java -cp /tmp App kubernetes java lab"],"volumeMounts":[{"name":"src","mountPath":"/src"}]}],"volumes":[{"name":"src","configMap":{"name":"%[2]s"}}]}}' >/dev/null 2>&1; kubectl wait --for=condition=Ready pod/%[1]s --timeout=90s >/dev/null 2>&1 || true; kubectl logs %[1]s 2>/dev/null | grep -q '^words=3$' && echo OK || echo FAIL`, pod, cm, dir),
				ExpectedContains: "OK",
			},
		}},
		Teardown:    []string{fmt.Sprintf(`kubectl delete pod %s --ignore-not-found=true --wait=false; kubectl delete configmap %s --ignore-not-found=true; rm -rf "%s"`, pod, cm, dir)},
		Explanation: "Este lab pratica `main(String[] args)`, compilação e execução em container JDK, mantendo o ambiente do aluno reproduzível.",
		DocURL:      "https://docs.oracle.com/en/java/",
		DocSection:  "Java documentation",
	}
}

func tplHelmConfigMap(p params, level int, cert string) models.Question {
	dir := labWorkspace("helm-" + p.Name)
	release := p.Name + "-rel"
	cm := release + "-config"
	msg := "hello-" + p.Name
	return models.Question{
		Question: fmt.Sprintf("No diretorio **%s**, crie um chart Helm que instale um ConfigMap chamado **%s** com `message=%s`, e faca o deploy com release **%s**.", dir, cm, msg, release),
		Hint:     "Monte Chart.yaml, values.yaml e templates/configmap.yaml. Use `.Values.message` no template e instale com `helm upgrade --install`.",
		AnswerCommand: fmt.Sprintf(`mkdir -p "%[1]s/templates"
cat > "%[1]s/Chart.yaml" <<'EOF'
apiVersion: v2
name: config-lab
version: 0.1.0
EOF
cat > "%[1]s/values.yaml" <<'EOF'
message: %[3]s
EOF
cat > "%[1]s/templates/configmap.yaml" <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: %[2]s
data:
  message: "{{ .Values.message }}"
EOF
helm upgrade --install %[4]s "%[1]s"`, dir, cm, msg, release),
		Setup: []models.SetupStep{{
			Description: "Preparando workspace Helm limpo...",
			Command:     fmt.Sprintf(`rm -rf "%[1]s"; mkdir -p "%[1]s/templates"; kubectl delete configmap %[2]s --ignore-not-found=true >/dev/null 2>&1 || true`, dir, cm),
		}},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Chart Helm instala ConfigMap **%s**", cm),
				Hint:        "O template precisa gerar kind ConfigMap com metadata.name correto.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`kubectl get configmap %[1]s -o jsonpath='{.data.message}' 2>/dev/null`, cm),
					ExpectedContains: msg,
				},
			},
			{
				Description: "Chart possui estrutura minima versionada",
				Hint:        "Chart.yaml deve declarar apiVersion v2 e version.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`test -f "%[1]s/Chart.yaml" && test -f "%[1]s/templates/configmap.yaml" && grep -q '^apiVersion: v2' "%[1]s/Chart.yaml" && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf(`kubectl delete configmap %s --ignore-not-found=true >/dev/null 2>&1 || true; rm -rf "%s"`, cm, dir)},
		Explanation: "Helm empacota manifestos parametrizados como charts. Este lab pratica Chart.yaml, values.yaml, template Go e instalacao idempotente via upgrade --install.",
		DocURL:      "https://helm.sh/docs/topics/charts/",
		DocSection:  "Helm -> Charts",
	}
}

func tplDockerfileStatic(p params, level int, cert string) models.Question {
	dir := labWorkspace("docker-" + p.Name)
	return models.Question{
		Question: fmt.Sprintf("No diretorio **%s**, crie um **Dockerfile** para uma app shell minima baseada em Alpine. Ele deve copiar **app.sh** e executar esse script como comando padrao.", dir),
		Hint:     "Use uma imagem base pequena, copie o script, garanta permissao de execucao e defina CMD.",
		AnswerCommand: fmt.Sprintf(`mkdir -p "%[1]s"
cat > "%[1]s/app.sh" <<'EOF'
#!/usr/bin/env sh
echo container-ready
EOF
chmod +x "%[1]s/app.sh"
cat > "%[1]s/Dockerfile" <<'EOF'
FROM alpine:3.20
WORKDIR /app
COPY app.sh /app/app.sh
RUN chmod +x /app/app.sh
CMD ["/app/app.sh"]
EOF`, dir),
		Setup: []models.SetupStep{{
			Description: "Preparando workspace Dockerfile...",
			Command:     fmt.Sprintf(`rm -rf "%[1]s"; mkdir -p "%[1]s"; rm -f "%[1]s/Dockerfile" "%[1]s/app.sh"`, dir),
		}},
		Goals: []models.Goal{
			{
				Description: "Dockerfile usa Alpine e copia **app.sh**",
				Hint:        "Confira FROM e COPY.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`grep -qi '^FROM alpine:' "%[1]s/Dockerfile" 2>/dev/null && grep -q 'COPY app.sh' "%[1]s/Dockerfile" && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
			{
				Description: "Imagem teria comando padrao executavel",
				Hint:        "O script deve ser executavel e o Dockerfile precisa declarar CMD.",
				Validation: &models.Validation{
					Command:          fmt.Sprintf(`test -x "%[1]s/app.sh" && grep -qi '^CMD ' "%[1]s/Dockerfile" && echo OK || echo FAIL`, dir),
					ExpectedContains: "OK",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf(`rm -rf "%s"`, dir)},
		Explanation: "Dockerfile bom explicita base, contexto de trabalho, arquivos copiados, permissao e processo principal. A validacao evita depender de Docker daemon no ambiente do lab.",
		DocURL:      "https://docs.docker.com/get-started/docker-concepts/building-images/writing-a-dockerfile/",
		DocSection:  "Docker -> Writing a Dockerfile",
	}
}

var incidentTemplates = []templateFn{tplIncidentImage, tplIncidentSelector, tplIncidentScaledDown}

// GenerateIncidents cria `count` incidentes aleatórios, persiste e devolve.
func GenerateIncidents(cert string, count int) ([]models.Question, error) {
	if cert == "" {
		cert = "CKA"
	}
	if count < 1 {
		count = 1
	}
	if count > 5 {
		count = 5
	}
	var qs []models.Question
	for i := 0; i < count; i++ {
		fn := incidentTemplates[rand.IntN(len(incidentTemplates))]
		q := fn(randParams(), 1, cert) // incidentes são sempre nível 1 (sem mão na roda)
		q.ID = newID()
		q.Topic = "Troubleshooting"
		q.Cert = models.Cert(cert)
		q.Difficulty = models.Hard
		q.Type = models.Lab
		qs = append(qs, q)
	}
	if err := persist(qs); err != nil {
		return nil, err
	}
	return qs, nil
}

func tplIncidentImage(p params, level int, cert string) models.Question {
	return models.Question{
		Question:      fmt.Sprintf("🚨 **INCIDENTE** — a aplicação **%s** está fora do ar: os pods não sobem.\n\nDiagnostique a causa e restaure o serviço. O Deployment deve ficar **Available** com a imagem **nginx:1.21**.", p.Name),
		Hint:          "kubectl get pods → status dos pods; kubectl describe pod → Events contam a história.",
		AnswerCommand: fmt.Sprintf("kubectl set image deployment/%s nginx=nginx:1.21", p.Name),
		Setup: []models.SetupStep{
			{Description: "Provisionando a aplicação...", Command: fmt.Sprintf("kubectl create deployment %s --image=nginx:1.21 --replicas=2 2>/dev/null || true", p.Name)},
			{Description: "💥 Injetando a falha...", Command: fmt.Sprintf("kubectl set image deployment/%s nginx=nginx:INEXISTENTE_v99 2>/dev/null || true", p.Name)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("Deployment **%s** voltou a ficar **Available** com imagem válida", p.Name),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get deploy %s -o jsonpath='{.spec.template.spec.containers[0].image}:{.status.conditions[?(@.type==\"Available\")].status}' 2>/dev/null", p.Name),
					ExpectedContains: "nginx:1.21:True",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name)},
		Explanation: "Pods em ImagePullBackOff/ErrImagePull indicam imagem inexistente ou sem acesso. Diagnóstico: kubectl describe pod (Events) ou kubectl get deploy -o wide. Correção: kubectl set image (ou rollout undo). Sempre confirme com rollout status.",
		DocURL:      "https://kubernetes.io/docs/concepts/containers/images/",
		DocSection:  "Concepts → Images",
	}
}

func tplIncidentSelector(p params, level int, cert string) models.Question {
	return models.Question{
		Question:      fmt.Sprintf("🚨 **INCIDENTE** — o serviço **%s** não entrega tráfego: usuários reportam timeout.\n\nOs pods estão saudáveis. Encontre a desconexão e restaure os **endpoints** do Service.", p.Name2),
		Hint:          "Compare o selector do Service com as labels reais dos pods (describe svc vs get pods --show-labels).",
		AnswerCommand: fmt.Sprintf("kubectl patch svc %s -p '{\"spec\":{\"selector\":{\"app\":\"%s\"}}}'", p.Name2, p.Name),
		Setup: []models.SetupStep{
			{Description: "Provisionando a aplicação...", Command: fmt.Sprintf("kubectl create deployment %s --image=nginx:1.21 --replicas=2 2>/dev/null || true; kubectl rollout status deployment/%s --timeout=90s 2>/dev/null || true", p.Name, p.Name)},
			{Description: "💥 Injetando a falha...", Command: fmt.Sprintf("kubectl create service clusterip %s --tcp=80:80 2>/dev/null || true; kubectl set selector service %s 'app=%s-QUEBRADO' 2>/dev/null || true", p.Name2, p.Name2, p.Name)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("🌐 Service **%s** com **endpoints ativos** (tráfego restaurado)", p.Name2),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("kubectl get endpoints %s -o jsonpath='{.subsets[0].addresses[0].ip}' 2>/dev/null | grep -qE '[0-9]' && echo RECOVERED || echo DOWN", p.Name2),
					ExpectedContains: "RECOVERED",
				},
			},
		},
		Teardown: []string{
			fmt.Sprintf("kubectl delete svc %s --ignore-not-found=true", p.Name2),
			fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name),
		},
		Explanation: "Endpoints vazios com pods saudáveis = selector não casa com as labels. É um dos incidentes mais comuns de prova e de produção. Diagnóstico: describe svc (Selector) vs get pods --show-labels; correção via kubectl patch/edit.",
		DocURL:      "https://kubernetes.io/docs/concepts/services-networking/service/",
		DocSection:  "Services → Selectors",
	}
}

func tplIncidentScaledDown(p params, level int, cert string) models.Question {
	return models.Question{
		Question:      fmt.Sprintf("🚨 **INCIDENTE** — monitoramento alerta: **0 réplicas** respondendo para **%s**. Um deploy de sexta-feira deixou o serviço zerado.\n\nInvestigue e restaure **pelo menos 2 réplicas** saudáveis.", p.Name),
		Hint:          "kubectl get deploy — repare na coluna READY. Alguém mexeu no replicas...",
		AnswerCommand: fmt.Sprintf("kubectl scale deployment %s --replicas=2", p.Name),
		Setup: []models.SetupStep{
			{Description: "Provisionando a aplicação...", Command: fmt.Sprintf("kubectl create deployment %s --image=nginx:1.21 --replicas=2 2>/dev/null || true", p.Name)},
			{Description: "💥 Injetando a falha...", Command: fmt.Sprintf("kubectl scale deployment %s --replicas=0 2>/dev/null || true", p.Name)},
		},
		Goals: []models.Goal{
			{
				Description: fmt.Sprintf("**%s** com **2+ réplicas prontas** (serviço restaurado)", p.Name),
				Validation: &models.Validation{
					Command:          fmt.Sprintf("R=$(kubectl get deploy %s -o jsonpath='{.status.readyReplicas}' 2>/dev/null); [ \"${R:-0}\" -ge 2 ] && echo RECOVERED-$R || echo DOWN", p.Name),
					ExpectedContains: "RECOVERED",
				},
			},
		},
		Teardown:    []string{fmt.Sprintf("kubectl delete deployment %s --ignore-not-found=true", p.Name)},
		Explanation: "Réplicas zeradas não geram pods com erro — o app simplesmente some. Por isso o diagnóstico começa em kubectl get deploy (READY 0/0), não em get pods. kubectl scale restaura; em produção, procure o autor no rollout history/auditoria.",
		DocURL:      "https://kubernetes.io/docs/concepts/workloads/controllers/deployment/#scaling-a-deployment",
		DocSection:  "Deployments → Scaling",
	}
}
