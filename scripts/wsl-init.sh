#!/usr/bin/env bash
# Instala kubectl, minikube e Docker Engine dentro do Ubuntu/WSL
# Chamado automaticamente pelo setup.ps1

set -euo pipefail

GREEN='\033[0;32m'; CYAN='\033[0;36m'; YELLOW='\033[1;33m'; RED='\033[0;31m'; NC='\033[0m'
step()  { echo -e "\n${CYAN}==> $*${NC}"; }
ok()    { echo -e "  ${GREEN}[OK]${NC} $*"; }
warn()  { echo -e "  ${YELLOW}[WARN]${NC} $*"; }
fail()  { echo -e "  ${RED}[FAIL]${NC} $*"; exit 1; }

# ── Detectar usuario nao-root ─────────────────────────────────────────
# Quando invocado via PowerShell elevado, o WSL roda como root.
# minikube recusa --driver=docker como root, entao precisamos do usuario real.
if [ "$(id -u)" = "0" ]; then
    # Primeiro usuario com UID >= 1000 (usuario criado no Ubuntu)
    WSL_USER=$(id -un 1000 2>/dev/null || getent passwd | awk -F: '$3 >= 1000 && $3 < 65534 {print $1; exit}' || true)
    if [ -z "$WSL_USER" ]; then
        warn "Nenhum usuario nao-root encontrado — usando --force para o minikube."
        WSL_USER="root"
        RUN_AS_USER=""
        MINIKUBE_FORCE="--force"
    else
        echo -e "  ${CYAN}Rodando como root. Operacoes de usuario serao feitas como: ${WSL_USER}${NC}"
        RUN_AS_USER="sudo -u $WSL_USER HOME=/home/$WSL_USER"
        MINIKUBE_FORCE=""
    fi
    SUDO=""
else
    WSL_USER="$(whoami)"
    RUN_AS_USER=""
    SUDO="sudo"
    MINIKUBE_FORCE=""
fi

# ── 0. Habilitar systemd no WSL2 ─────────────────────────────────────
# Systemd garante que o Docker inicie automaticamente quando o WSL acorda,
# mantendo o cluster minikube vivo entre sessoes.
step "Configurando systemd no WSL2..."
WSLCONF=/etc/wsl.conf
if ! grep -q "systemd=true" "$WSLCONF" 2>/dev/null; then
    # Preservar secao [boot] existente ou criar nova
    if grep -q "^\[boot\]" "$WSLCONF" 2>/dev/null; then
        # Adicionar systemd=true dentro da secao [boot] existente
        ${SUDO:-} sed -i '/^\[boot\]/a systemd=true' "$WSLCONF"
    else
        printf '\n[boot]\nsystemd=true\n' | ${SUDO:-} tee -a "$WSLCONF" >/dev/null
    fi
    ok "systemd habilitado em $WSLCONF (WSL deve ser reiniciado para ter efeito)"
    SYSTEMD_CHANGED=true
else
    ok "systemd ja habilitado"
    SYSTEMD_CHANGED=false
fi

# ── 1. Dependencias base ──────────────────────────────────────────────
step "Atualizando apt e instalando dependencias..."
${SUDO:-} apt-get update -qq
${SUDO:-} apt-get install -y -qq curl ca-certificates apt-transport-https gnupg lsb-release
ok "Dependencias prontas"

# ── 2. Docker Engine ──────────────────────────────────────────────────
step "Verificando Docker..."
if command -v docker &>/dev/null && docker info &>/dev/null 2>&1; then
    ok "Docker ja instalado e acessivel"
elif [ "$(id -u)" = "0" ] && command -v docker &>/dev/null; then
    ok "Docker instalado — verificando acesso para $WSL_USER"
else
    step "Instalando Docker Engine..."
    curl -fsSL https://get.docker.com | ${SUDO:-} sh
    ok "Docker instalado"
fi

# Adicionar usuario nao-root ao grupo docker
if [ "$WSL_USER" != "root" ]; then
    if ! groups "$WSL_USER" 2>/dev/null | grep -q docker; then
        ${SUDO:-} usermod -aG docker "$WSL_USER"
        warn "Usuario $WSL_USER adicionado ao grupo docker. Reinicie o terminal WSL apos o setup para que tenha efeito."
    else
        ok "Usuario $WSL_USER ja esta no grupo docker"
    fi
fi

# Iniciar servico Docker
if command -v systemctl &>/dev/null && systemctl is-active docker &>/dev/null 2>&1; then
    ok "Servico Docker ja esta rodando"
elif command -v systemctl &>/dev/null && systemctl list-units --all &>/dev/null 2>&1; then
    ${SUDO:-} systemctl enable docker --now 2>/dev/null || true
    ok "Servico Docker iniciado via systemctl"
else
    ${SUDO:-} service docker start 2>/dev/null || true
    ok "Servico Docker iniciado via service"
fi

# ── 3. kubectl ────────────────────────────────────────────────────────
step "Verificando kubectl..."
if command -v kubectl &>/dev/null; then
    ok "kubectl ja instalado: $(kubectl version --client 2>/dev/null | head -1)"
else
    step "Instalando kubectl..."
    K8S_VER=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
    curl -fsSL -o /tmp/kubectl "https://dl.k8s.io/release/${K8S_VER}/bin/linux/amd64/kubectl"
    ${SUDO:-} install -o root -g root -m 0755 /tmp/kubectl /usr/local/bin/kubectl
    rm /tmp/kubectl
    ok "kubectl instalado: $(kubectl version --client 2>/dev/null | head -1)"
fi

# ── 4. minikube ───────────────────────────────────────────────────────
step "Verificando minikube..."
if command -v minikube &>/dev/null; then
    ok "minikube ja instalado: $(minikube version --short)"
else
    step "Instalando minikube..."
    curl -fsSL -o /tmp/minikube \
        https://storage.googleapis.com/minikube/releases/latest/minikube-linux-amd64
    ${SUDO:-} install /tmp/minikube /usr/local/bin/minikube
    rm /tmp/minikube
    ok "minikube instalado: $(minikube version --short)"
fi

# ── 5. Iniciar minikube ───────────────────────────────────────────────
step "Verificando cluster minikube..."

# Checar status como o usuario correto
MINIKUBE_STATUS=$(${RUN_AS_USER:-} minikube status --format '{{.Host}}' 2>/dev/null || true)

if [ "$MINIKUBE_STATUS" = "Running" ]; then
    ok "minikube ja esta rodando"
else
    step "Iniciando minikube (driver=docker, 2 CPU, 2 GB RAM)..."
    # Minikube deve rodar como usuario nao-root com --driver=docker.
    ${RUN_AS_USER:-} /usr/local/bin/minikube start --driver=docker --cpus=2 --memory=2048 ${MINIKUBE_FORCE:-}
    ok "minikube iniciado"
fi

# ── 5.5 Ajustar timeouts do etcd (tolerancia a I/O lento do WSL2) ────
# O --extra-config=etcd.* e incompativel com kubeadm v1beta4 (K8s 1.31+).
# Editamos o manifesto do etcd diretamente apos o cluster subir.
# Os valores padrao (heartbeat=100ms, election=1000ms) sao muito apertados
# para o Docker-dentro-de-Docker no WSL2, causando crash loop do etcd.
step "Ajustando timeouts do etcd no manifesto estatico..."
${RUN_AS_USER:-} minikube ssh -- "
  MANIFEST=/etc/kubernetes/manifests/etcd.yaml
  if sudo grep -q 'heartbeat-interval=500' \$MANIFEST 2>/dev/null; then
    echo 'etcd ja com timeouts ajustados'
  else
    sudo sed -i 's/--heartbeat-interval=[0-9]*/--heartbeat-interval=500/g' \$MANIFEST 2>/dev/null || true
    sudo sed -i 's/--election-timeout=[0-9]*/--election-timeout=5000/g'   \$MANIFEST 2>/dev/null || true
    echo 'etcd manifesto atualizado'
  fi
" 2>/dev/null || warn "Nao foi possivel ajustar o manifesto do etcd (cluster pode ainda estar subindo)"

# ── 6. Servicos systemd (keepalive + minikube auto-start) ─────────────
step "Configurando servicos systemd..."

# Keepalive: impede o WSL de terminar por inatividade
if [ ! -f /etc/systemd/system/wsl-keepalive.service ]; then
    cat > /etc/systemd/system/wsl-keepalive.service << 'EOF'
[Unit]
Description=Keep WSL2 instance alive

[Service]
ExecStart=/bin/sleep infinity
Restart=always

[Install]
WantedBy=multi-user.target
EOF
fi

# Minikube: inicia o cluster automaticamente quando o WSL boota
cat > /etc/systemd/system/minikube.service << SVCEOF
[Unit]
Description=Minikube Kubernetes Cluster
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
User=$WSL_USER
Environment=HOME=/home/$WSL_USER
ExecStart=/usr/local/bin/minikube start --driver=docker --keep-context
ExecStop=/usr/local/bin/minikube stop

[Install]
WantedBy=multi-user.target
SVCEOF

${SUDO:-} systemctl daemon-reload
${SUDO:-} systemctl enable wsl-keepalive.service
${SUDO:-} systemctl enable minikube.service
${SUDO:-} systemctl start wsl-keepalive.service 2>/dev/null || true
ok "Servicos systemd configurados (minikube + wsl-keepalive)"

# ── 7. Verificar cluster ──────────────────────────────────────────────
step "Verificando conectividade com o cluster..."
if ${RUN_AS_USER:-} kubectl cluster-info --request-timeout=10s &>/dev/null; then
    ok "Cluster respondendo"
    ${RUN_AS_USER:-} kubectl cluster-info 2>/dev/null | head -2
else
    warn "Cluster ainda nao responde. Tente: wsl -- kubectl cluster-info"
fi

echo ""
echo -e "${GREEN}=====================================${NC}"
echo -e "${GREEN}  WSL/Linux pronto!${NC}"
echo -e "${GREEN}=====================================${NC}"
