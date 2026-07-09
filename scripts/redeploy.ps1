<#
.SYNOPSIS
  Redeploy reproduzível do K8s Study Lab na VM Azure.

.DESCRIPTION
  Builda a imagem na ACR taggeando com o git SHA curto ALÉM de :latest (e
  injetando a versão no binário via --build-arg VERSION), liga a VM se estiver
  desalocada (auto-stop), reinicia o app puxando a imagem nova e CONFERE que o
  container 'lab' passou a rodar exatamente a imagem recém-buildada. Assim a
  imagem no ar sempre corresponde a um commit conhecido (rollback = re-tag de
  um SHA) e a versão fica visível no site / /healthz / /metrics.

  Aborta se a working tree estiver suja (a menos que -AllowDirty), porque uma
  imagem buildada de código não commitado não corresponde a nenhum commit.

.EXAMPLE
  ./scripts/redeploy.ps1
  ./scripts/redeploy.ps1 -AllowDirty   # deploy de emergência sem commit
#>
[CmdletBinding()]
param(
  [string]$ResourceGroup = "k8slab-rg",
  [string]$VmName        = "k8slab-vm",
  [string]$Acr           = "k8slabacrb9ue3",
  [string]$Image         = "estudo-app",
  [switch]$AllowDirty
)
$ErrorActionPreference = "Stop"
$repoRoot = Split-Path -Parent $PSScriptRoot

function Say($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Ok($m)  { Write-Host "  OK $m" -ForegroundColor Green }
function Die($m) { Write-Host "ERRO: $m" -ForegroundColor Red; exit 1 }

# ── 1. Estado do git: SHA + working tree limpa ──────────────────────
$sha = (git -C $repoRoot rev-parse --short HEAD).Trim()
if (-not $sha) { Die "nao consegui obter o git SHA (esta num repo git?)" }
$dirty = git -C $repoRoot status --porcelain
if ($dirty -and -not $AllowDirty) {
  Die "working tree suja — commite antes (ou use -AllowDirty). A imagem em prod deve corresponder a um commit."
}
Say "Deploy do commit $sha (tags: $sha + latest)"

# ── 2. Build na ACR (roda na Azure), versão injetada via ldflags ────
Say "az acr build na '$Acr' (~3-5 min, roda na Azure)"
az acr build --registry $Acr --image "${Image}:$sha" --image "${Image}:latest" --build-arg "VERSION=$sha" $repoRoot
if ($LASTEXITCODE -ne 0) { Die "az acr build falhou" }
Ok "imagem $sha publicada"

# ── 3. Liga a VM se estiver desalocada (auto-stop) ──────────────────
# statuses[1] = PowerState (statuses[0] = ProvisioningState). Índice em vez de
# filtro JMESPath com aspas simples, que quebra o parser do PowerShell 5.1.
$power = (az vm get-instance-view -g $ResourceGroup -n $VmName --query "instanceView.statuses[1].displayStatus" -o tsv).Trim()
if ($power -ne "VM running") {
  Say "VM esta '$power' — iniciando"
  az vm start -g $ResourceGroup -n $VmName | Out-Null
  Ok "VM iniciada"
}

# ── 4. Restart na VM + confere que o container roda a imagem nova ───
# A verificação compara IMAGE IDs na própria VM (config digest == config digest),
# evitando confundir digest de manifesto (az acr) com o de config (docker inspect).
Say "Reiniciando o app na VM e conferindo a imagem"
$remote = @"
set -e
az acr login --name $Acr >/dev/null
docker pull $Acr.azurecr.io/${Image}:latest >/dev/null
systemctl restart estudo-app.service
sleep 10
want=`$(docker inspect --format '{{.Id}}' $Acr.azurecr.io/${Image}:latest)
got=`$(docker inspect --format '{{.Image}}' lab)
if [ "`$want" = "`$got" ]; then echo "MATCH `$got"; else echo "MISMATCH want=`$want got=`$got"; fi
"@
$tmp = New-TemporaryFile
Set-Content -Path $tmp -Value $remote -Encoding ascii
try {
  $out = az vm run-command invoke -g $ResourceGroup -n $VmName --command-id RunShellScript --scripts "@$tmp" --query "value[0].message" -o tsv
} finally {
  Remove-Item $tmp -Force -ErrorAction SilentlyContinue
}
if ($out -notmatch "MATCH") {
  Write-Host $out
  Die "container 'lab' nao esta rodando a imagem recem-buildada"
}
Ok "container 'lab' rodando a imagem de $sha"

Say "Deploy concluido do commit $sha"
Write-Host "  Confira a versao:  https://k8slab-b9ue3.eastus2.cloudapp.azure.com/healthz  (deve mostrar 'ok $sha')"
