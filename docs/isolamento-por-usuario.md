# Isolamento por usuário — desenho de arquitetura

> Documento de decisão, 2026-07-09. O bloqueador nº 1 do produto: enquanto
> todos os alunos compartilham um k3s num container `--privileged`, só dá para
> aceitar gente de confiança. Este doc decide COMO isolar antes de codar.

## Onde estamos

Um container `lab` (privileged) roda o app + um k3s compartilhado. Isolamento
atual: namespace `lab-<user>` + RBAC gerado por lab no terminal. O que isso
NÃO isola:

- **Control-plane**: um aluno pode saturar o API server de todos (sem quota de
  requests) e listar/afetar recursos cluster-scoped.
- **CRDs e recursos globais**: ArgoCD é um só para todos; labs de cluster
  hardening (CKS) e de administração (CKA: etcd, upgrade) mexem no cluster de
  todo mundo — hoje esses labs são possíveis justamente porque confiamos nos
  usuários.
- **Runtime**: escape de container no k3s = acesso ao host = VM inteira.

## Orçamento real (o número que decide tudo)

VM `Standard_D2s_v3`: **2 vCPU, 8 GB RAM**, disco 31 GB.

| Consumidor fixo | RAM |
|---|---|
| Sistema + Docker | ~1.0 GB |
| Ollama + qwen2.5:1.5b residente | ~2.0–2.5 GB (gen 3b sob demanda: +2 GB) |
| k3s host + app + caddy | ~1.5–1.8 GB |
| **Sobra para isolamento** | **~2.5–3.5 GB** |

## Opções

**A. vcluster (cluster virtual por usuário)** — um pod por usuário com API
server próprio + syncer que traduz para o k3s host. ~250–350 MB/usuário ocioso.
Isola control-plane e CRDs (ArgoCD por usuário!); workloads continuam no
runtime do host (escape de container segue sendo risco de host — igual a hoje,
não pior). Startup ~20–40 s. **Cabem ~8 usuários simultâneos na VM atual.**

**B. k3d por usuário (k3s-in-docker)** — cluster completo por usuário.
Isolamento máximo (inclui runtime separado por container) e realismo máximo
(multi-node, etcd de verdade). Custo: 600 MB–1 GB/usuário → **2–4 usuários na
VM atual**. Startup 30–60 s.

**C. Endurecer o modelo atual** — ResourceQuota + LimitRange + NetworkPolicy +
PSA `restricted` nos namespaces `lab-<user>`. Custo zero de RAM, mas NÃO isola
control-plane nem CRDs — é mitigação, não isolamento. Não destrava abrir para
estranhos.

## Decisão

**vcluster como mecanismo principal (Fase 1), com C como pré-requisito
imediato (Fase 0) e B reservado para os labs que realmente precisam de um
cluster físico (modo "cluster admin", efêmero e um por vez).**

Racional: a razão isolamento/RAM do vcluster é ~3× a do k3d, e ele resolve
exatamente o que dói (control-plane + CRDs por usuário) sem mudar o modelo do
app — o terminal já injeta kubeconfig por contexto, então apontar cada usuário
para o kubeconfig do seu vcluster é uma troca de arquivo, não de arquitetura.
Labs CKA de administração de cluster (etcd backup, kubeadm upgrade) não cabem
em vcluster por design; esses viram k3d efêmero single-user sob demanda, com
lease e TTL agressivo.

## Fases

1. **Fase 0 — endurecer o que existe (1 sessão):** ResourceQuota + LimitRange
   + PSA `baseline` em todo `lab-<user>`; NetworkPolicy default-deny entre
   namespaces de usuários. Não destrava estranhos, mas reduz o raio de dano
   entre amigos JÁ.
2. **Fase 1 — vcluster por usuário (2–3 sessões):** criar sob demanda no
   primeiro lab (padrão do `lab-shell-<user>` que já existe), kubeconfig do
   usuário aponta para o vcluster, `cluster_reset` = recriar o vcluster,
   TTL de inatividade destrói (o estado do aluno mora no perfil, não no
   cluster). ArgoCD/tools instalam DENTRO do vcluster.
3. **Fase 2 — polimento:** pool de 1–2 vclusters pré-aquecidos (cold start
   <10 s); métrica `app_vclusters_active`; upgrade da VM para D4s_v3 (16 GB)
   quando passar de ~6 usuários ativos — dobra o teto para ~20.
4. **Fase 3 — modo cluster-admin (quando houver demanda):** k3d efêmero para
   labs de administração de cluster, um por vez, com lease anti-auto-stop.

## Riscos e limites aceitos

- vcluster NÃO protege contra escape de runtime — igual a hoje; o ganho é
  control-plane/CRD/quota. Escape de runtime só se resolve com VM por usuário
  (custo proibitivo) ou gVisor/kata (complexidade alta). Aceito por ora.
- Versão do k8s no vcluster pode divergir do host — fixar na mesma minor.
- Ollama compartilhado continua: um aluno gerando labs enfileira os outros.
  Mitigação futura: fila com prioridade por usuário (já há budget de tokens).
