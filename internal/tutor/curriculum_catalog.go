package tutor

import (
	"regexp"
	"strings"
)

// CurriculumChoice is a learner-facing curriculum item. Available means the
// current lab engine can build an exact, safe, automatically validated lab for
// the item; unavailable items remain visible so curriculum coverage is honest.
type CurriculumChoice struct {
	Label        string `json:"label"`
	Description  string `json:"description,omitempty"`
	Prompt       string `json:"prompt"`
	Available    bool   `json:"available"`
	Researchable bool   `json:"researchable"`
	Topic        string `json:"topic,omitempty"`
	Source       string `json:"source,omitempty"`
}

type CurriculumDomainView struct {
	Domain       string             `json:"domain"`
	Weight       int                `json:"weight"`
	Sources      []string           `json:"sources,omitempty"`
	Competencies []CurriculumChoice `json:"competencies"`
}

type CurriculumView struct {
	Cert    string                 `json:"cert"`
	Domains []CurriculumDomainView `json:"domains"`
}

var curriculumCompetencies = map[string]map[string][]CurriculumChoice{
	"CKA": {
		"Cluster Architecture, Installation & Configuration": {
			{Label: "Manage role based access control (RBAC)", Topic: "RBAC", Available: true},
			{Label: "Prepare underlying infrastructure for installing a Kubernetes cluster"},
			{Label: "Create and manage Kubernetes clusters using kubeadm"},
			{Label: "Manage the lifecycle of Kubernetes clusters"},
			{Label: "Implement and configure a highly-available control plane"},
			{Label: "Use Helm and Kustomize to install cluster components", Topic: "Helm", Available: true},
			{Label: "Understand extension interfaces (CNI, CSI, CRI, etc.)"},
			{Label: "Understand CRDs, install and configure operators"},
		},
		"Workloads & Scheduling": {
			{Label: "Application deployments, rolling updates and rollbacks", Topic: "Workloads", Available: true},
			{Label: "ConfigMaps and Secrets", Topic: "Configuration", Available: true},
			{Label: "Workload autoscaling", Topic: "Autoscaling", Available: true},
			{Label: "Pod admission and scheduling", Topic: "Admission Control", Available: true},
			{Label: "Node affinity and pod affinity/anti-affinity", Topic: "Pod Affinity and Anti-Affinity", Available: true},
			{Label: "Taints and tolerations", Topic: "Taints and Tolerations", Available: true},
		},
		"Services & Networking": {
			{Label: "ClusterIP and NodePort Services", Topic: "NodePort", Available: true},
			{Label: "Ingress controllers and resources", Topic: "Services", Available: true},
			{Label: "CoreDNS and service discovery", Topic: "Services", Available: true},
			{Label: "NetworkPolicies", Topic: "Security", Available: true},
		},
		"Storage": {
			{Label: "StorageClasses and dynamic provisioning", Topic: "Storage", Available: true},
			{Label: "Volume types, access modes and reclaim policies", Topic: "Storage", Available: true},
			{Label: "PersistentVolumes and PersistentVolumeClaims", Topic: "Storage", Available: true},
		},
		"Troubleshooting": {
			{Label: "Troubleshoot clusters and nodes", Topic: "Troubleshooting", Available: true},
			{Label: "Troubleshoot cluster components", Topic: "Troubleshooting", Available: true},
			{Label: "Monitor cluster and application resource usage", Topic: "Troubleshooting", Available: true},
			{Label: "Manage and evaluate container output streams", Topic: "Troubleshooting", Available: true},
			{Label: "Troubleshoot services and networking", Topic: "Troubleshooting", Available: true},
		},
	},
	"CAPA": {
		"Argo CD e GitOps": {
			{Label: "Applications, Projects and repositories", Topic: "GitOps", Available: true},
			{Label: "Declarative setup and GitOps principles", Topic: "GitOps", Available: true},
		},
		"Sync, Health e Rollback": {
			{Label: "Sync policies, waves and hooks", Topic: "GitOps", Available: true},
			{Label: "Health, drift, rollback and self-heal", Topic: "GitOps", Available: true},
		},
		"Argo Rollouts": {
			{Label: "Progressive delivery with canary and blue-green", Available: false},
		},
	},
}

func CurriculumViewFor(cert string) (CurriculumView, bool) {
	cert = CanonicalCert(cert)
	cur, ok := CurriculumFor(cert)
	if !ok {
		return CurriculumView{}, false
	}
	view := CurriculumView{Cert: cert}
	known := curriculumCompetencies[strings.ToUpper(cert)]
	if len(known) == 0 {
		known = curriculumCompetencies[cert]
	}
	for _, domain := range cur {
		items := append([]CurriculumChoice(nil), known[domain.Domain]...)
		if len(items) == 0 {
			topic := exactTopicForRequest(cert, domain.Domain)
			if topic == "" {
				topic = detectTopic(domain.Domain)
			}
			items = []CurriculumChoice{{Label: domain.Domain, Topic: topic, Available: topic != ""}}
		}
		for i := range items {
			if len(domain.URLs) > 0 {
				items[i].Source = domain.URLs[0]
			}
			if items[i].Available {
				items[i].Researchable = true
				items[i].Prompt = "Crie um lab para " + cert + " sobre " + items[i].Label
				items[i].Description = "lab hands-on disponivel"
			} else {
				items[i].Researchable = items[i].Source != ""
				items[i].Topic = items[i].Label
				items[i].Prompt = "Pesquise a documentacao oficial e tente criar um lab funcional para " + cert + " sobre " + items[i].Label
				if items[i].Source != "" {
					items[i].Prompt += " usando " + items[i].Source
				}
				items[i].Description = "sem template pronto; pesquisar fonte oficial e validar antes de criar"
			}
		}
		view.Domains = append(view.Domains, CurriculumDomainView{
			Domain: domain.Domain, Weight: domain.Weight, Sources: append([]string(nil), domain.URLs...), Competencies: items,
		})
	}
	return view, true
}

func curriculumDomainInMessage(cert, msg string) (CurriculumDomainView, bool) {
	view, ok := CurriculumViewFor(cert)
	if !ok {
		return CurriculumDomainView{}, false
	}
	lower := strings.ToLower(msg)
	for _, domain := range view.Domains {
		if strings.Contains(lower, strings.ToLower(domain.Domain)) {
			return domain, true
		}
	}
	return CurriculumDomainView{}, false
}

func curriculumTopicInMessage(cert, msg string) string {
	view, ok := CurriculumViewFor(cert)
	if !ok {
		return ""
	}
	lower := strings.ToLower(msg)
	for _, domain := range view.Domains {
		for _, item := range domain.Competencies {
			if item.Topic != "" && strings.Contains(lower, strings.ToLower(item.Label)) {
				return item.Topic
			}
		}
	}
	return ""
}

func curriculumChoiceInMessage(cert, msg string) (CurriculumChoice, bool) {
	view, ok := CurriculumViewFor(cert)
	if !ok {
		return CurriculumChoice{}, false
	}
	lower := strings.ToLower(msg)
	for _, domain := range view.Domains {
		for _, item := range domain.Competencies {
			if strings.Contains(lower, strings.ToLower(item.Label)) {
				return item, true
			}
		}
	}
	return CurriculumChoice{}, false
}

func isBareCertificationLabRequest(msg, cert string) bool {
	if !labAskRe.MatchString(msg) || !isBroadLabRequest(msg) {
		return false
	}
	if exactTopicForRequest(cert, msg) != "" || detectTopic(msg) != "" || curriculumTopicInMessage(cert, msg) != "" {
		return false
	}
	if _, ok := curriculumDomainInMessage(cert, msg); ok {
		return false
	}
	_, ok := CurriculumFor(cert)
	return ok
}

func topicFromCurriculumOrRequest(cert, msg string) string {
	if topic := curriculumTopicInMessage(cert, msg); topic != "" {
		return topic
	}
	return ""
}

func looksLikeLabGeneration(msg string) bool {
	if !labAskRe.MatchString(msg) && !regexp.MustCompile(`(?i)\b(quest|pergunta)\w*`).MatchString(msg) {
		return false
	}
	cert := inferCertFromMessage(msg, "CKA")
	if isBareCertificationLabRequest(msg, cert) {
		return false
	}
	return exactTopicForRequest(cert, msg) != "" || detectTopic(msg) != "" || curriculumTopicInMessage(cert, msg) != "" ||
		regexp.MustCompile(`(?i)\b(trilha|replay|revis|incidente|simulado|exame)\b`).MatchString(msg)
}

// RequiresClusterForRequest is used by HTTP boundaries before any lab is
// generated. Explanations and curriculum browsing remain available offline.
func RequiresClusterForRequest(msg, activeCert string) bool {
	cert := inferCertFromMessage(msg, activeCert)
	if isBareCertificationLabRequest(msg, cert) {
		return false
	}
	return looksLikeLabGeneration(msg)
}
