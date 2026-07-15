# ─────────────────────────────────────────────────────────────────────────────
# K8s Study Lab — imagem autossuficiente: app + cluster k3s embutido.
# Cada amigo roda `docker run --privileged -p 8080:8080 estudo-app` e tem TUDO,
# sem WSL/minikube. client-go fala nativo com o k3s local; o terminal do lab
# spawna bash direto no container (cluster-admin de verdade por pessoa).
# ─────────────────────────────────────────────────────────────────────────────

# ---- build do binário Go ----
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION (git short SHA) injetado no binário via ldflags — cada imagem carrega
# a versão do commit que a originou (visível no site, /healthz e /metrics).
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -ldflags "-s -w -X estudo-app/internal/version.Version=${VERSION}" \
        -o /out/estudo-app .

# ---- runtime: k3s + app ----
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl bash vim-tiny iproute2 iptables procps mount unzip \
    && rm -rf /var/lib/apt/lists/*

# Azure CLI — para a instancia hospedada tambem conectar/gerenciar AKS
# (login, get-credentials, start/stop) direto pela pagina Cloud.
RUN curl -sL https://aka.ms/InstallAzureCLIDeb | bash && rm -rf /var/lib/apt/lists/*

# Terraform CLI — labs hands-on de IaC (providers local/random/null: rodam sem
# credencial de nuvem, custo zero). Cada usuario tem seu workspace isolado.
ARG TERRAFORM_VERSION=1.15.8
RUN curl -sfL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip" -o /tmp/tf.zip \
    && unzip -o /tmp/tf.zip -d /usr/local/bin && rm /tmp/tf.zip && terraform version

# Ansible — labs hands-on de automação (playbooks locais, sem custo/credencial).
RUN apt-get update && apt-get install -y --no-install-recommends ansible \
    && rm -rf /var/lib/apt/lists/* && ansible --version | head -1

# k3s (traz containerd + kubectl); tag tem '+', que precisa virar %2B na URL
ARG K3S_VERSION=v1.36.2+k3s1
RUN ENC=$(echo "$K3S_VERSION" | sed 's/+/%2B/') && \
    curl -sfL "https://github.com/k3s-io/k3s/releases/download/${ENC}/k3s" -o /usr/local/bin/k3s && \
    chmod +x /usr/local/bin/k3s && \
    ln -sf /usr/local/bin/k3s /usr/local/bin/kubectl

# vCluster OSS: cada aluno recebe um Kubernetes virtual com API server, RBAC e
# CRDs próprios sobre o node pool compartilhado do AKS. A versão e o checksum
# ficam fixos para o ambiente criado hoje ser reproduzível no próximo deploy.
ARG HELM_VERSION=3.21.3
ARG HELM_SHA256=15e041a93a590dce8100f39385cd98c84a765c9e36aeeb9e2dc6ff9e4769e2e0
RUN curl -fL "https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz" -o /tmp/helm.tgz \
    && echo "${HELM_SHA256}  /tmp/helm.tgz" | sha256sum -c - \
    && tar -xzf /tmp/helm.tgz -C /tmp \
    && install -m 0755 /tmp/linux-amd64/helm /usr/local/bin/helm \
    && rm -rf /tmp/helm.tgz /tmp/linux-amd64 \
    && helm version --short

ARG VCLUSTER_VERSION=0.35.1
ARG VCLUSTER_SHA256=baf9effb1de7c17cfa4462aacf92d913b4ec4e359c6e711e33c43f7e5e5b0dab
RUN curl -fL "https://github.com/loft-sh/vcluster/releases/download/v${VCLUSTER_VERSION}/vcluster-linux-amd64" -o /usr/local/bin/vcluster \
    && echo "${VCLUSTER_SHA256}  /usr/local/bin/vcluster" | sha256sum -c - \
    && chmod 0755 /usr/local/bin/vcluster \
    && vcluster --version

COPY --from=build /out/estudo-app /app/estudo-app
COPY questions-custom /app/questions-custom
COPY docker-entrypoint.sh /usr/local/bin/entrypoint.sh
RUN sed -i 's/\r$//' /usr/local/bin/entrypoint.sh && chmod +x /usr/local/bin/entrypoint.sh

WORKDIR /app
# KUBECONFIG mesclado: k3s local + ~/.kube/config (onde o 'az aks get-credentials'
# grava os contextos de AKS). Assim local e nuvem coexistem e dao pra alternar.
ENV KUBECONFIG=/etc/rancher/k3s/k3s.yaml:/root/.kube/config
EXPOSE 8080

# Persistência opcional do progresso: -v lab-data:/app/data
VOLUME ["/app/data"]

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
