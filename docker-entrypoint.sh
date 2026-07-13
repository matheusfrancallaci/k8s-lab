#!/usr/bin/env bash
# Sobe o k3s embutido, espera ficar pronto e inicia o K8s Study Lab.
set -e

if [ "${EMBEDDED_K3S:-1}" = "0" ]; then
    mkdir -p /root/.kube
    export KUBECONFIG=/root/.kube/config
    export K8S_LAB_VERIFY_GENERATED="${K8S_LAB_VERIFY_GENERATED:-0}"
    echo "[entrypoint] modo hospedado seguro: k3s embutido desativado; aguardando AKS"
    exec /app/estudo-app
fi

# cgroup v2 aninhado (docker-in-docker): move os processos para um subcgroup
# "init" e delega os controllers aos subtrees — senão o kubelet falha com
# "cannot enter cgroupv2 ... invalid state". Padrão usado por k3d/kind.
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    mkdir -p /sys/fs/cgroup/init
    xargs -rn1 < /sys/fs/cgroup/cgroup.procs > /sys/fs/cgroup/init/cgroup.procs 2>/dev/null || true
    sed -e 's/ / +/g' -e 's/^/+/' < /sys/fs/cgroup/cgroup.controllers \
        > /sys/fs/cgroup/cgroup.subtree_control 2>/dev/null || true
fi

# Verificação executável de labs gerados LIGADA por padrão na imagem: aqui há
# k3s embutido, então nenhum lab gerado chega ao aluno sem a solução ter sido
# executada num namespace efêmero. Dev local/testes seguem opt-in via env.
export K8S_LAB_VERIFY_GENERATED="${K8S_LAB_VERIFY_GENERATED:-1}"

echo "[entrypoint] iniciando cluster k3s local..."
# --snapshotter=native: overlayfs não monta dentro do Docker (DinD).
k3s server \
    --disable traefik \
    --disable metrics-server \
    --snapshotter=native \
    --write-kubeconfig-mode 644 \
    >/var/log/k3s.log 2>&1 &

# KUBECONFIG mesclado: k3s local + ~/.kube/config (contextos de AKS via az).
# Se o arquivo do az ainda nao existe, o kubectl ignora — sem problema.
mkdir -p /root/.kube
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml:/root/.kube/config

echo "[entrypoint] aguardando o cluster ficar pronto..."
ready=0
for i in $(seq 1 90); do
    if kubectl get nodes 2>/dev/null | grep -q ' Ready'; then
        ready=1
        echo "[entrypoint] cluster pronto (${i}x2s)."
        break
    fi
    sleep 2
done
if [ "$ready" != "1" ]; then
    echo "[entrypoint] AVISO: k3s não ficou Ready a tempo — últimas linhas do log:"
    tail -20 /var/log/k3s.log || true
    echo "[entrypoint] seguindo assim mesmo; o app cai no fallback shell se a API falhar."
fi

# O app trata o contexto "minikube" como LOCAL. O k3s cria "default"; renomeia
# para o app reconhecer o cluster como local — senão o terminal do lab acha que
# é nuvem e tenta abrir shell num pod em vez do bash local.
kubectl config rename-context default minikube 2>/dev/null || true

echo "[entrypoint] subindo o K8s Study Lab em http://localhost:8080"
exec /app/estudo-app
