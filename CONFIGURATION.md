# Configuração — K8s Study Lab

Referência das variáveis de ambiente. Nada é obrigatório para uso local
single-user; os defaults cobrem esse caso.

## Autenticação / multi-user
| Variável | Default | O que faz |
|---|---|---|
| `APP_PASSWORD` | *(vazio)* | Ativa login por conta. O valor é o **código de convite** usado no cadastro. Vazio = uso local sem login (perfil `default`). |
| `COOKIE_SECURE` | `0` | `1` marca o cookie de sessão como `Secure` (obrigatório atrás de HTTPS em produção). |
| `ALLOWED_WS_ORIGINS` | *(vazio)* | Hosts extra (separados por vírgula) aceitos no WebSocket do terminal além do próprio host. |

## Cluster / labs
| Variável | Default | O que faz |
|---|---|---|
| `PORT` | `8080` | Porta HTTP. |
| `LAB_NO_CLUSTER` | *(vazio)* | `1` pula o auto-start do cluster e o monitor de nuvem (CI/testes/só-UI). |
| `LAB_QUOTA_PODS` | `20` | ResourceQuota do namespace `lab-<user>`. |
| `LAB_QUOTA_CPU_REQ` / `LAB_QUOTA_CPU_LIM` | `2` / `4` | Requests/limits de CPU da quota. |
| `LAB_QUOTA_MEM_REQ` / `LAB_QUOTA_MEM_LIM` | `2Gi` / `4Gi` | Requests/limits de memória da quota. |
| `LAB_QUOTA_SVC` / `LAB_QUOTA_PVC` | `20` / `10` | Máx. de services / PVCs por usuário. |
| `LAB_LIMIT_CPU_DEFAULT` / `LAB_LIMIT_MEM_DEFAULT` | `250m` / `256Mi` | Limits default (LimitRange) p/ pods sem limites. |
| `LAB_LIMIT_CPU_REQUEST` / `LAB_LIMIT_MEM_REQUEST` | `50m` / `64Mi` | Requests default (LimitRange). |
| `CLOUD_SHELL_IMAGE` | `alpine/k8s:1.33.4` | Imagem do shell interativo dentro do cluster (modo AKS). |
| `CLOUD_SHELL_IDLE_MINUTES` | `30` | Coleta o pod `lab-shell-<user>` após esse tempo sem terminal aberto. |

## Nuvem (AKS)
| Variável | Default | O que faz |
|---|---|---|
| `AZURE_MANAGED_IDENTITY` | `0` | `1` (instância hospedada): login via `az login --identity` — sem device-code. |
| `AZURE_RG` | `k8s-study-lab-rg` | Resource group do AKS gerenciado pela página Cloud. |
| `AKS_NAME` | `k8s-study-lab` | Nome do cluster AKS. |
| `AZURE_REGION` | `eastus` | Região do AKS. |
| `AKS_NODE_SIZE` | `standard_d2als_v7` | SKU do nó (barato, liberado em subscription free). |
| `CLOUD_IDLE_MINUTES` | `45` | Auto-stop do AKS após inatividade (controle de custo). |

## Tutor (IA local — Ollama, opcional)
| Variável | Default | O que faz |
|---|---|---|
| `OLLAMA_URL` | `http://localhost:11434` | Endpoint do Ollama. Sem Ollama, o tutor cai nas heurísticas. |
| `OLLAMA_MODEL` | *(auto)* | Modelo fixo. Sem isso, escolhe o 1º instalado da lista de preferência (`llama3.2`, `qwen2.5`, ...). Modelo **menor** = respostas mais rápidas em CPU. |
| `OLLAMA_CHAT_MODEL` | `OLLAMA_MODEL` | Perfil de conversa grounded/streaming. Use um modelo menor para menor tempo ao primeiro token. |
| `OLLAMA_ROUTER_MODEL` | `OLLAMA_CHAT_MODEL` | Perfil de classificação/planejamento de tópicos; pode ser o modelo mais rápido instalado. |
| `OLLAMA_GEN_MODEL` | `OLLAMA_MODEL` | Perfil de geração estruturada de labs e quiz; prefira o modelo mais capaz de código. |
| `OLLAMA_EMBED_MODEL` | *(auto/local fallback)* | Modelo dedicado aos embeddings persistidos do RAG, ex.: `nomic-embed-text`. Sem ele, o RAG usa fallback local determinístico. |
| `OLLAMA_NUM_PREDICT` | `1200` | Teto de tokens da geração. Conversa de chat já usa `400` fixo (menor = mais rápido). |
| `TUTOR_DOC_CACHE_TTL` | `30m` | TTL do cache com ETag para documentação oficial; reduz fetch/crawl repetido. |
| `K8S_LAB_VERIFY_GENERATED` | `0` | Quando `1`, executa templates Kubernetes gerados em namespace efêmero antes de entregá-los. Ative somente no cluster de verificação. |
| `LAB_PSA_ENFORCE` | `baseline` | Perfil Pod Security Admission aplicado aos namespaces `lab-<usuário>`. |

## Deploy hospedado (Azure)
Ver [deploy/azure/README.md](deploy/azure/README.md) e a memória de deploy. Resumo:
- `az acr build --registry <acr> --image estudo-app:latest .` (build na Azure)
- reiniciar o app na VM: `az vm run-command invoke -g <rg> -n <vm> --command-id RunShellScript --scripts "systemctl restart estudo-app.service"`
