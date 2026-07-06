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
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/estudo-app .

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
ARG TERRAFORM_VERSION=1.9.8
RUN curl -sfL "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip" -o /tmp/tf.zip \
    && unzip -o /tmp/tf.zip -d /usr/local/bin && rm /tmp/tf.zip && terraform version

# k3s (traz containerd + kubectl); tag tem '+', que precisa virar %2B na URL
ARG K3S_VERSION=v1.31.5+k3s1
RUN ENC=$(echo "$K3S_VERSION" | sed 's/+/%2B/') && \
    curl -sfL "https://github.com/k3s-io/k3s/releases/download/${ENC}/k3s" -o /usr/local/bin/k3s && \
    chmod +x /usr/local/bin/k3s && \
    ln -sf /usr/local/bin/k3s /usr/local/bin/kubectl

COPY --from=build /out/estudo-app /app/estudo-app
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
