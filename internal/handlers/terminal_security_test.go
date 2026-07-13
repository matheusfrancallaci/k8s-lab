package handlers

import (
	"strings"
	"testing"

	networkingv1 "k8s.io/api/networking/v1"
)

func TestHostedTerminalAlwaysUsesScopedCloudShell(t *testing.T) {
	t.Setenv("AZURE_MANAGED_IDENTITY", "1")
	t.Setenv("APP_PASSWORD", "invite")

	for _, ctx := range []string{"", localContext, "k8s-study-lab"} {
		if !terminalUsesCloudShell(ctx) {
			t.Fatalf("hosted terminal must use Kubernetes shell for context %q", ctx)
		}
	}
	if terminalLocalFallbackAllowed() {
		t.Fatal("hosted terminal must fail closed instead of falling back to the application shell")
	}

	ns, pod, sa, scoped := cloudShellTarget("alice")
	if !scoped || ns != "lab-alice" || pod != "lab-shell-alice" || sa != "lab-user" {
		t.Fatalf("hosted target is not per-user/scoped: ns=%q pod=%q sa=%q scoped=%v", ns, pod, sa, scoped)
	}
}

func TestHostedTerminalFailsClosedWithoutAuthenticatedIdentity(t *testing.T) {
	t.Setenv("AZURE_MANAGED_IDENTITY", "1")
	t.Setenv("APP_PASSWORD", "")

	if hostedTerminalIdentityReady("default") {
		t.Fatal("hosted terminal must reject the unauthenticated default profile")
	}
	ns, pod, sa, scoped := cloudShellTarget("default")
	if !scoped || ns == cloudShellSystemNS || pod == cloudShellSystemPod || sa == "lab-admin" {
		t.Fatalf("hosted fallback must never resolve to shared cluster-admin shell: ns=%q pod=%q sa=%q scoped=%v", ns, pod, sa, scoped)
	}
}

func TestLocalSingleUserTerminalBehaviorIsPreserved(t *testing.T) {
	t.Setenv("AZURE_MANAGED_IDENTITY", "0")
	t.Setenv("APP_PASSWORD", "")

	if terminalUsesCloudShell(localContext) {
		t.Fatal("local minikube must keep the local terminal when managed identity is off")
	}
	if !terminalLocalFallbackAllowed() {
		t.Fatal("local development must retain its existing fallback behavior")
	}
	if !terminalUsesCloudShell("k8s-study-lab") {
		t.Fatal("a cloud context must keep using the Kubernetes shell")
	}
	ns, pod, sa, scoped := cloudShellTarget("default")
	if scoped || ns != cloudShellSystemNS || pod != cloudShellSystemPod || sa != "lab-admin" {
		t.Fatalf("local single-user cloud target changed unexpectedly: ns=%q pod=%q sa=%q scoped=%v", ns, pod, sa, scoped)
	}
}

func TestIMDSEgressPolicyAllowsNormalTrafficExceptMetadata(t *testing.T) {
	p := imdsEgressNetworkPolicy("lab-alice")
	if p.Namespace != "lab-alice" || len(p.Spec.PolicyTypes) != 1 || p.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Fatalf("unexpected egress policy identity/types: %#v", p)
	}
	if len(p.Spec.Egress) != 2 {
		t.Fatalf("expected IPv4 and IPv6 allow rules, got %d", len(p.Spec.Egress))
	}
	v4 := p.Spec.Egress[0].To[0].IPBlock
	if v4 == nil || v4.CIDR != "0.0.0.0/0" || len(v4.Except) != 1 || v4.Except[0] != "169.254.169.254/32" {
		t.Fatalf("IPv4 rule must allow normal traffic except Azure IMDS: %#v", v4)
	}
	v6 := p.Spec.Egress[1].To[0].IPBlock
	if v6 == nil || v6.CIDR != "::/0" || len(v6.Except) != 0 {
		t.Fatalf("IPv6 normal traffic should remain allowed: %#v", v6)
	}
}

func TestKubectlNamespaceGuardrailsBlockIMDS(t *testing.T) {
	script := namespaceGuardrailsScript("lab-alice")
	for _, want := range []string{
		"name: lab-allow-egress-except-imds",
		"cidr: 0.0.0.0/0",
		"169.254.169.254/32",
		"cidr: ::/0",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("kubectl fallback is missing %q:\n%s", want, script)
		}
	}
	combined := cloudShellNamespaceAccessScript("lab-alice", "lab-user", nil)
	if strings.Contains(combined, "K8SLABPOLICY;") || !strings.Contains(combined, "K8SLABPOLICY\nkubectl") {
		t.Fatalf("heredoc terminator must be on its own line before the next command:\n%s", combined)
	}
	if !strings.Contains(combined, "kubectl -n lab-alice apply -f -") {
		t.Fatalf("namespaces where the user can create workloads must receive the IMDS policy:\n%s", combined)
	}
}
