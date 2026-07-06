#!/usr/bin/env bash
# Deploy de ponta a ponta do K8s Study Lab na Azure.
# Roda tudo: checa pré-requisitos -> terraform apply -> build da imagem na ACR
# -> imprime a URL HTTPS para compartilhar. Idempotente (pode rodar de novo).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

say()  { printf '\n\033[1;36m==> %s\033[0m\n' "$1"; }
ok()   { printf '\033[1;32m  ✓ %s\033[0m\n' "$1"; }
die()  { printf '\n\033[1;31m✗ ERRO: %s\033[0m\n' "$1" >&2; exit 1; }

# ── 1. Pré-requisitos ──────────────────────────────────────────────
say "Checando pré-requisitos"
command -v az        >/dev/null || die "Azure CLI (az) não encontrado — https://aka.ms/azcli"
command -v terraform >/dev/null || die "Terraform não encontrado — https://developer.hashicorp.com/terraform/install"
az account show >/dev/null 2>&1 || die "Não logado no Azure. Rode primeiro:  az login"
ok "az, terraform e login OK"

# tr -d '\r': no Windows/Git Bash o 'az' devolve com \r no fim, e o provider
# azurerm CRASHA com o subscription id "sujo" (erro GRPCProvider no apply).
export ARM_SUBSCRIPTION_ID="$(az account show --query id -o tsv | tr -d '\r\n')"
ok "Subscription: $(az account show --query name -o tsv | tr -d '\r\n')"

cd "$SCRIPT_DIR"
if [ ! -f terraform.tfvars ]; then
  die "Falta terraform.tfvars. Rode:  cp terraform.tfvars.example terraform.tfvars  e preencha ssh_public_key + app_password"
fi
ok "terraform.tfvars encontrado"

# ── 2. Provisiona a infra ──────────────────────────────────────────
say "terraform init"
terraform init -input=false >/dev/null
ok "init OK"

say "terraform apply (revise o plano e confirme com 'yes')"
terraform apply

# ── 3. Constrói e publica a imagem na ACR (build roda na Azure) ────
ACR="$(terraform output -raw acr_name | tr -d '\r\n')"
say "Construindo a imagem na ACR '$ACR' (roda na Azure, ~2-3 min)"
cd "$REPO_ROOT"
az acr build --registry "$ACR" --image estudo-app:latest .
ok "imagem publicada"

# ── 4. Pronto ──────────────────────────────────────────────────────
URL="$(terraform -chdir="$SCRIPT_DIR" output -raw app_url | tr -d '\r\n')"
RG="$(terraform -chdir="$SCRIPT_DIR" output -raw resource_group | tr -d '\r\n')"
VM="$(terraform -chdir="$SCRIPT_DIR" output -raw vm_name | tr -d '\r\n')"

say "Deploy concluído 🎉"
cat <<EOF

  Acesse:  $URL
  (a 1ª abertura leva ~1 min: a VM puxa a imagem e o cert HTTPS é emitido)

  Seus amigos:
    1. abrem a URL no browser
    2. clicam em "Criar conta" e usam o CÓDIGO DE CONVITE (= o app_password do tfvars)
    3. entram com usuário + senha próprios (progresso isolado por conta)

  Custo: a VM se desaloca sozinha após inatividade (auto-stop).
  Religar para estudar:   az vm start -g $RG -n $VM
  Parar manualmente:      az vm deallocate -g $RG -n $VM
  Atualizar o app:        az acr build --registry $ACR --image estudo-app:latest . && az vm restart -g $RG -n $VM

EOF
