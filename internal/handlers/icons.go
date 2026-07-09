package handlers

import "strings"

func techIconPath(parts ...string) string {
	return iconPathFor(strings.Join(parts, " "))
}

func sourceIconPath(parts ...string) string {
	return iconPathFor(strings.Join(parts, " "))
}

func iconPathFor(text string) string {
	l := strings.ToLower(text)
	switch {
	case hasAny(l, "localstack"):
		return "/static/vendor/localstack.svg"
	case hasAny(l, "argocd", "argo cd", "argo-cd", "argoproj", "gitops", "capa"):
		return "/static/vendor/argo.png"
	case hasAny(l, "terraform", "hcl", "hashicorp"):
		return "/static/vendor/terraform.svg"
	case hasAny(l, "ansible", "playbook"):
		return "/static/vendor/ansible.svg"
	case hasAny(l, "azure", "aks", "microsoft"):
		return "/static/vendor/azure.svg"
	case hasAny(l, "aws", "amazon", "iam", "s3", "sqs", "vpc", "ec2", "eks", "cloudfront", "lambda", "dynamodb"):
		return "/static/vendor/aws.svg"
	case hasAny(l, "kubernetes", "k8s", "kubectl", "kube", "cka", "ckad", "cks", "hpa", "metrics-server", "pod", "deployment", "service"):
		return "/static/vendor/kubernetes.svg"
	default:
		return "/static/vendor/docs.svg"
	}
}

func hasAny(haystack string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
