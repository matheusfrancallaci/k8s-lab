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
	"Workloads":          {tplDeployment, tplJob},
	"Services":           {tplService},
	"Configuration":      {tplConfigMap},
	"Storage":            {tplPVC},
	"Scheduling":         {tplNodeSelector},
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
	"CKA":    {"Core Concepts", "Workloads", "Services", "Scheduling", "Storage"},
	"CKAD":   {"Application Design", "Configuration", "Workloads", "Core Concepts"},
	"CKS":    {"Security"},
	"ArgoCD": {"Workloads", "Configuration"},
}

// generateQuestions cria labs de template SEM persistir (uso interno).
func generateQuestions(topic, cert string, level, count int) []models.Question {
	fns, ok := templates[topic]
	if !ok || count < 1 {
		return nil
	}
	var qs []models.Question
	for i := 0; i < count; i++ {
		fn := fns[rand.IntN(len(fns))]
		q := fn(randParams(), level, cert)
		q.ID = newID()
		q.Topic = topic
		q.Cert = models.Cert(cert)
		q.Difficulty = diffFor(level)
		q.Type = models.Lab
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
	if count > 10 {
		count = 10
	}

	qs := generateQuestions(topic, cert, level, count)
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

// ─────────────────────────────────────────────────────────────────────────────
// MODO INCIDENTE — o anti-lab: o setup QUEBRA o cluster de propósito e o
// usuário diagnostica e conserta contra o relógio. Treino de troubleshooting
// real, o que separa aprovado de reprovado no CKA.
// ─────────────────────────────────────────────────────────────────────────────

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
