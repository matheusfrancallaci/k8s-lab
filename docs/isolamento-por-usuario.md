# Isolamento por usuário — arquitetura implantada

> Decisão atualizada em 2026-07-13. No ambiente hospedado, cada usuário recebe
> um Kubernetes virtual descartável durante o lab. Os nós continuam no AKS
> compartilhado; API server, RBAC, namespaces, CRDs e recursos cluster-scoped
> pertencem somente ao cluster virtual daquele usuário.

## Fluxo do lab

1. O servidor cria um lease absoluto de 1 hora. Recarregar a página ou avançar
   uma questão não renova o prazo.
2. Antes de liberar setup e terminal, o app confirma que o AKS está pronto,
   habilita o autoscaler do node pool e instala um vCluster no namespace
   `lab-<user>-<id>`.
3. O shell não recebe token ou kubeconfig do AKS host. Ele recebe somente o
   kubeconfig cluster-admin do vCluster, portanto comandos como
   `kubectl get pods -A`, criação de ClusterRoles e instalação de CRDs ficam
   dentro do ambiente do aluno.
4. Ao concluir, encerrar ou atingir 1 hora, o app apaga o namespace host. Isso
   remove o vCluster, shell e todos os workloads sincronizados. O coletor roda
   mesmo sem o navegador aberto e mantém o lease persistido até a exclusão ser
   confirmada, permitindo nova tentativa se o AKS estiver temporariamente fora.

## Capacidade

- O AKS usa autoscaler com mínimo de 1 e máximo configurável por
  `AKS_MAX_NODES` (3 no deploy atual).
- Cada namespace host tem ResourceQuota, LimitRange, Pod Security Admission e
  NetworkPolicy; acesso ao Azure Instance Metadata Service é bloqueado.
- O estado do control plane usa um PVC de 1 GiB durante a hora do lab, evitando
  perda de recursos se o pod reiniciar. O PVC é apagado junto com o namespace.
- `VCLUSTER_CHART_VERSION` fica fixado no runtime e a CLI no container é
  verificada por SHA-256 durante o build.

## Limite de isolamento

O vCluster isola o control plane lógico, não o hardware: workloads de usuários
diferentes ainda executam nos mesmos nós AKS. Isso é adequado para laboratórios
de estudo e evita o custo e o tempo de provisionar um AKS completo por aluno,
mas não equivale a uma VM ou node pool dedicado contra escape de container.
Labs que exigem kubeadm, etcd real ou alteração do nó físico ainda precisam de
um modo futuro com cluster físico descartável.

## Configuração

```text
LAB_VCLUSTER_ENABLED=1
VCLUSTER_CHART_VERSION=0.35.1
LAB_SESSION_TTL=1h
LAB_ENVIRONMENTS_PATH=/app/data/lab-environments.json
AKS_MAX_NODES=3
```

O modo vCluster só é ativado no runtime hospedado com identidade gerenciada.
Desenvolvimento local e a imagem single-user com k3s embutido preservam o fluxo
existente.
