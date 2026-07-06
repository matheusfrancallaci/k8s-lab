# Deploy na Azure (uma instância hospedada)

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

## Passo a passo

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

Seus amigos acessam a **URL** e entram com a `app_password`. Cada um escolhe um
**perfil** (menu ⚙ → "seu perfil") — o progresso do tutor fica isolado por pessoa.

## Atualizar (nova versão do app)

```bash
az acr build --registry <acr> --image estudo-app:latest .   # rebuild+push
ssh azureuser@<ip> 'sudo systemctl restart estudo-app'      # puxa e reinicia
```

## Custo (ordem de grandeza)

- VM `Standard_B2s` (2 vCPU/4GB): ~US$30/mês ligada 24/7. Para economizar,
  `az vm deallocate` quando não estiverem usando, ou troque para `B1ms` (~US$15,
  mais apertado). ACR Basic: ~US$5/mês. IP público: ~US$3.

## Segurança / próximos passos

- **Restrinja o SSH**: em `terraform.tfvars`, ponha `allowed_ssh_cidr = "SEU_IP/32"`.
- **HTTPS**: hoje é HTTP puro. Para TLS, aponte um domínio (`dns_label`) e rode um
  Caddy/nginx com Let's Encrypt na frente — posso adicionar se quiser.
- **Labs compartilham o cluster**: nesta instância única, o k3s é um só. Ótimo para
  quiz/teoria/tutor (isolados por perfil); para hands-on simultâneo pesado, cada
  pessoa idealmente roda a própria imagem local (`make docker-run`).
