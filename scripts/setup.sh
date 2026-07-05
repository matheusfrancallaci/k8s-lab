#!/usr/bin/env bash
# K8s Study Lab — Setup Script (Linux / macOS)
# Installs kubectl and minikube, then starts minikube.
# Usage: bash scripts/setup.sh

set -e

CYAN='\033[0;36m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

step()  { echo -e "\n${CYAN}==> $1${NC}"; }
ok()    { echo -e "  ${GREEN}[OK]${NC} $1"; }
warn()  { echo -e "  ${YELLOW}[WARN]${NC} $1"; }
fail()  { echo -e "  ${RED}[FAIL]${NC} $1"; exit 1; }

echo -e "\n${CYAN}K8s Study Lab — Setup para Linux/macOS${NC}"
echo -e "${CYAN}========================================${NC}\n"

OS="$(uname -s)"
ARCH="$(uname -m)"

# ── 1. Install kubectl ───────────────────────────────────
step "Verificando kubectl..."
if command -v kubectl &>/dev/null; then
    ok "kubectl já instalado: $(kubectl version --client --short 2>/dev/null)"
else
    step "Instalando kubectl..."
    if [ "$OS" = "Darwin" ]; then
        if command -v brew &>/dev/null; then
            brew install kubectl
        else
            fail "Homebrew não encontrado. Instale em https://brew.sh e rode novamente."
        fi
    else
        KUBECTL_VER=$(curl -sL https://dl.k8s.io/release/stable.txt)
        curl -sLo /tmp/kubectl "https://dl.k8s.io/release/${KUBECTL_VER}/bin/linux/${ARCH}/kubectl"
        chmod +x /tmp/kubectl
        sudo mv /tmp/kubectl /usr/local/bin/kubectl
    fi
    ok "kubectl instalado"
fi

# ── 2. Install minikube ──────────────────────────────────
step "Verificando minikube..."
if command -v minikube &>/dev/null; then
    ok "minikube já instalado: $(minikube version --short 2>/dev/null)"
else
    step "Instalando minikube..."
    if [ "$OS" = "Darwin" ]; then
        if command -v brew &>/dev/null; then
            brew install minikube
        else
            fail "Homebrew não encontrado. Instale em https://brew.sh e rode novamente."
        fi
    else
        MINKUBE_ARCH="amd64"
        [ "$ARCH" = "aarch64" ] && MINKUBE_ARCH="arm64"
        curl -sLo /tmp/minikube "https://storage.googleapis.com/minikube/releases/latest/minikube-linux-${MINKUBE_ARCH}"
        chmod +x /tmp/minikube
        sudo mv /tmp/minikube /usr/local/bin/minikube
    fi
    ok "minikube instalado"
fi

# ── 3. Check Docker ──────────────────────────────────────
step "Verificando Docker..."
if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
    ok "Docker está rodando"
    DRIVER="docker"
else
    warn "Docker não encontrado ou não está rodando."
    if [ "$OS" = "Darwin" ]; then
        warn "Inicie o Docker Desktop e rode este script novamente."
        warn "Ou instale em: https://www.docker.com/products/docker-desktop"
    else
        warn "Tentando usar driver 'none' (requer root) ou instale o Docker."
    fi
    DRIVER=""
fi

# ── 4. Start minikube ────────────────────────────────────
step "Verificando cluster minikube..."
if minikube status --format "{{.Host}}" 2>/dev/null | grep -q "Running"; then
    ok "minikube já está rodando"
else
    step "Iniciando minikube..."
    DRIVER_FLAG=""
    [ -n "$DRIVER" ] && DRIVER_FLAG="--driver=$DRIVER"
    minikube start $DRIVER_FLAG --cpus=2 --memory=2048
    ok "minikube iniciado com sucesso"
fi

# ── 5. Verify ────────────────────────────────────────────
step "Verificando conectividade com o cluster..."
if kubectl cluster-info &>/dev/null 2>&1; then
    ok "Cluster respondendo"
else
    warn "Cluster não responde ainda. Aguarde e tente: kubectl cluster-info"
fi

echo -e "\n${GREEN}=====================================${NC}"
echo -e "${GREEN}  Setup completo!${NC}"
echo -e "${GREEN}  Inicie o app: go run .${NC}"
echo -e "${GREEN}  Acesse:       http://localhost:8080${NC}"
echo -e "${GREEN}=====================================${NC}\n"
