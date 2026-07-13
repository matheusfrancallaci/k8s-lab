# ⎈ K8s Study Lab

Ambiente local para estudar as certificações Kubernetes (**CKA · CKAD · CKS · ArgoCD**):
questões de teoria, laboratórios práticos com terminal real (`kubectl`, `vim`, tab
completion), validação automática, e um **tutor adaptativo 100% local** (zero API) que
observa seu desempenho e gera labs sob medida.

## Rodar (recomendado — 1 comando, sem instalar nada)

Cada pessoa sobe a sua própria instância **com cluster próprio embutido** (k3s).
Não precisa de WSL, minikube ou kubectl na máquina — só Docker.

```bash
docker run --privileged -p 8080:8080 -v lab-data:/app/data ghcr.io/você/estudo-app
# ou, a partir do repo:
make docker-build && make docker-run
```

Abra **http://localhost:8080**. O container sobe um cluster k3s interno e o app fala
com ele nativamente (client-go) — cada aluno tem **cluster-admin de verdade**, que é o
que os labs de CKA/CKS exigem.

> `--privileged` é necessário porque o k3s roda um cluster Kubernetes dentro do container.
> `-v lab-data:/app/data` mantém seu progresso entre execuções.

### Vários amigos, mesma instância

Se você hospeda uma instância para o grupo, defina uma senha de acesso e cada pessoa
escolhe um **perfil** (no menu ⚙ → "seu perfil"). O progresso do tutor fica isolado por
perfil.

```bash
docker run --privileged -p 8080:8080 -e APP_PASSWORD=segredo -v lab-data:/app/data ghcr.io/você/estudo-app
```

> Observação honesta: os **labs práticos** de cert precisam de cluster-admin, então cada
> pessoa idealmente roda a própria instância (cluster próprio). A mesma instância
> compartilhada é ótima para **quiz/teoria** e progresso separado; para hands-on
> simultâneo, prefira uma instância por pessoa.

## Desenvolvimento

```bash
make run       # go run . (host; usa o cluster do host se houver)
make run-wsl   # cross-compila p/ Linux e roda dentro do WSL (client-go nativo no minikube)
make build     # binário local
```

`LAB_NO_CLUSTER=1` sobe só a UI, sem tocar em cluster (testes/CI).

## Como funciona

- **Go** (`net/http`, `embed.FS` — assets e questões embutidos, roda offline)
- **Terminal real** via WebSocket + PTY (ConPTY no Windows, `creack/pty` no Linux)
- **client-go** para falar com a API do cluster sem `kubectl` shell (hot paths)
- **Tutor local**: modelo de habilidade EWMA por tópico, recomendações determinísticas,
  e enhancer opcional via **Ollama** (streaming), tudo local
- **Planner pedagógico**: memória por aluno, pré-requisitos, diagnóstico de lacunas e estratégia explicável
- **Grounding verificável**: RAG híbrido, allowlist de fontes, proteção contra prompt injection e citações por afirmação
- **Labs com contrato de publicação**: digest de conteúdo, quality gate, verificação executável e estado real de prontidão
- **Hospedagem multiusuário isolada**: um vCluster por usuário e sessão, lease absoluto de 1 hora e remoção automática de todos os recursos
- **Operação mensurável**: fila da LLM, keep-alive, cancelamento, telemetria persistente p50/p95/p99 e TTFT
