# Deploy na Azure (uma instância hospedada)

> Controle de custo: rode `powershell -File scripts/azure-cost-guard.ps1 -Stop`
> periodicamente ou antes de encerrar os estudos. O script para todos os AKS,
> aponta clusters duplicados e bloqueia exclusão automática sem revisão.

Sobe **uma VM Linux** rodando o container completo (app + k3s), acessível pelo
browser dos seus amigos. A imagem é construída na **ACR** (`az acr build`, sem
Docker local) e a VM a puxa com a própria identidade (sem segredos na VM).

> Por que VM e não ACI/Container Apps? O container precisa de `--privileged` (roda
> um cluster k3s dentro), e os serviços serverless da Azure não permitem privileged.

## Pré-requisitos

- [Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli) e [Terraform](https://developer.hashicorp.com/terraform/install)
- Uma chave SSH (`ssh-keygen -t ed25519`)
- Login e subscription (o provider azurerm 4.x exige o subscription id):

```bash
az login
export ARM_SUBSCRIPTION_ID=$(az account show --query id -o tsv)
# no Windows PowerShell: $env:ARM_SUBSCRIPTION_ID = (az account show --query id -o tsv)
```

## Jeito rápido (um comando)

```bash
cd deploy/azure
cp terraform.tfvars.example terraform.tfvars   # preencha ssh_public_key e app_password
bash deploy.sh                                 # checa tudo, aplica, builda e imprime a URL
```

O `deploy.sh` valida pré-requisitos (`az`/`terraform`/login), roda o `apply` (você
confirma o plano), constrói a imagem na ACR e imprime a **URL HTTPS** pronta para
compartilhar, com os comandos de religar/parar/atualizar.

## Passo a passo (manual, se preferir)

```bash
cd deploy/azure
cp terraform.tfvars.example terraform.tfvars   # preencha ssh_public_key e app_password

terraform init
terraform apply                                # cria RG, rede, ACR e a VM

# publica a imagem na ACR (rode na RAIZ do repo; build acontece na Azure)
cd ../..
az acr build --registry "$(terraform -chdir=deploy/azure output -raw acr_name)" \
             --image estudo-app:latest .

# a VM detecta a imagem e sobe o container em ~1 min
terraform -chdir=deploy/azure output app_url   # abra essa URL no browser
```

Seus amigos acessam a **URL (HTTPS)** e entram com a `app_password`. Cada um
escolhe um **perfil** (menu ⚙ → "seu perfil") — o progresso do tutor fica
isolado por pessoa.

> **HTTPS é automático.** Um Caddy na frente do app emite o certificado
> Let's Encrypt para o FQDN da Azure (`<label>.<região>.cloudapp.azure.com`) no
> primeiro acesso — a primeira abertura pode levar ~30s enquanto o cert é emitido.
> O `app_url` do output já é o link `https://...` pronto para compartilhar.

## Atualizar (nova versão do app)

Use o script de redeploy **reproduzível** — ele tagueia a imagem com o git SHA
(além de `latest`), liga a VM se estiver desalocada, reinicia o app e **confere
que o digest em produção bate com o build**:

```powershell
./scripts/redeploy.ps1              # aborta se a working tree estiver suja
./scripts/redeploy.ps1 -AllowDirty  # deploy de emergência sem commit
```

Ou pelo CI: dispare o workflow **Deploy (Azure)** (`.github/workflows/deploy.yml`,
`workflow_dispatch`) escolhendo o commit/tag — requer os secrets de OIDC configurados.

Modo manual (equivalente, sem o script):

```bash
az acr build --registry <acr> --image estudo-app:<git-sha> --image estudo-app:latest .
ssh azureuser@<ip> 'sudo systemctl restart estudo-app'      # puxa e reinicia
```

> **Rollback:** como cada deploy fica taggeado por SHA na ACR, para voltar basta
> re-taggear um SHA bom como `latest` (`az acr import`/`az acr repository`) e reiniciar.

### Saúde e observabilidade

O app expõe (públicos, sem cookie — para LB/orquestrador/scrape):

- `GET /healthz` — liveness (processo vivo). Barato, não toca no cluster.
- `GET /readyz` — readiness (503 até o k3s ficar pronto; ~30-90s no boot / pós auto-stop).
- `GET /metrics` — contadores em formato Prometheus (requests, 4xx/5xx, panics,
  terminais ativos, goroutines, heap, uptime).

Logs são estruturados; `LOG_FORMAT=json` (já ligado no cloud-init) emite JSON e
`LOG_LEVEL` ajusta o nível. O restart é **graceful**: no `systemctl restart` o app
drena as conexões em andamento (até 25s) antes de sair.

### Backup do progresso

O volume `lab-data` (progresso, `users.json`, `sessions.json`) vive numa VM só —
faça backup periódico:

```powershell
./scripts/backup-lab-data.ps1                                   # tar.gz na VM
./scripts/backup-lab-data.ps1 -StorageAccount <conta>           # + sobe pra blob (DR real)
```

## Custo (ordem de grandeza)

- VM `Standard_B2s` (2 vCPU/4GB): ~US$30/mês **ligada 24/7** — mas o **auto-stop**
  já vem ligado: a VM se **desaloca sozinha** após `idle_minutes` (default 30) sem
  ninguém usando (nenhum terminal ativo), e aí você só paga o disco (~US$2/mês).
  Para religar quando quiser estudar: `az vm start -g <rg> -n <prefix>-vm` (ou pelo
  portal). Desligue o auto-stop com `idle_minutes = 0`. ACR Basic ~US$5, IP ~US$3.

## Segurança / próximos passos

- **HTTPS**: já vem pronto (Caddy + Let's Encrypt no FQDN da Azure). Se quiser um
  domínio próprio (`labs.seudominio.com`), aponte um CNAME para o FQDN e troque o
  endereço no `deploy/azure/cloud-init.yaml` (Caddyfile) — o Caddy emite o cert sozinho.
- **Restrinja o SSH**: em `terraform.tfvars`, ponha `allowed_ssh_cidr = "SEU_IP/32"`.
- **Labs compartilham o cluster**: nesta instância única, o k3s é um só. Ótimo para
  quiz/teoria/tutor (isolados por perfil); para hands-on simultâneo pesado, cada
  pessoa idealmente roda a própria imagem local (`make docker-run`).
- **State do Terraform remoto**: hoje o `terraform.tfstate` é local (frágil e pode
  conter valores sensíveis). Veja `backend.tf.example` para migrar para um Azure
  Storage (`terraform init -migrate-state`).

## Limitações arquiteturais conhecidas (dívida técnica)

Assumidas de propósito para manter o custo/complexidade baixos numa instância entre
amigos. Reavaliar ao virar produto de verdade:

- **Container `--privileged` = raiz do host.** O app, o k3s e o cluster-admin rodam
  no mesmo container privilegiado; comprometer o processo do app é ter root na VM. O
  hardening multi-tenant (namespaces `lab-<user>` com admin *namespaced*) limita o que
  um usuário faz *no cluster*, mas não muda o fato de o processo ser privilegiado.
  Mitigação real exigiria separar o app (não privilegiado) do runtime de cluster —
  refactor grande, fora do escopo desta instância.
- **Ponto único de falha.** Uma VM, um container, um volume. Sem HA. Um `az vm start`
  frio + pull do modelo Ollama deixa o **primeiro acesso pós auto-stop lento**
  (cold start); o `/readyz` sinaliza quando o cluster está de fato pronto. Para um
  produto: múltiplas réplicas atrás de um LB e storage gerenciado para o estado.
