<#
.SYNOPSIS
  Redeploy reproduzível do K8s Study Lab na VM Azure.

.DESCRIPTION
  Builda a imagem na ACR taggeando com o git SHA curto ALÉM de :latest, liga a
  VM se estiver desalocada (auto-stop), reinicia o app puxando a imagem nova e
  CONFERE que o digest do container em produção bate com o build. Assim a imagem
  no ar sempre corresponde a um commit conhecido (rollback = re-tag de um SHA).

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
$tagSha = "${Image}:$sha"
$tagLatest = "${Image}:latest"
Say "Deploy do commit $sha (tags: $sha + latest)"

# ── 2. Build na ACR (roda na Azure) ─────────────────────────────────
Say "az acr build na '$Acr' (~3-5 min, roda na Azure)"
az acr build --registry $Acr --image $tagSha --image $tagLatest --build-arg VERSION=$sha $repoRoot
if ($LASTEXITCODE -ne 0) { Die "az acr build falhou" }
$digest = (az acr repository show --name $Acr --image $tagSha --query "digest" -o tsv).Trim()
Ok "imagem publicada — digest $digest"

# ── 3. Liga a VM se estiver desalocada (auto-stop) ──────────────────
$power = (az vm get-instance-view -g $ResourceGroup -n $VmName --query "instanceView.statuses[?starts_with(code,'PowerState')].displayStatus" -o tsv).Trim()
if ($power -ne "VM running") {
  Say "VM esta '$power' — iniciando"
  az vm start -g $ResourceGroup -n $VmName | Out-Null
  Ok "VM iniciada"
}

# ── 4. Restart na VM puxando a imagem nova + confere o digest ───────
Say "Reiniciando o app na VM e conferindo o digest"
$remote = "az acr login --name $Acr && docker pull $Acr.azurecr.io/$tagLatest && systemctl restart estudo-app.service && sleep 8 && docker inspect --format '{{.Image}}' lab"
$out = az vm run-command invoke -g $ResourceGroup -n $VmName --command-id RunShellScript --scripts $remote --query "value[0].message" -o tsv
if ($out -notmatch [regex]::Escape($digest)) {
  Write-Host $out
  Die "o digest do container 'lab' NAO bate com o build ($digest) — deploy nao confirmado"
}
Ok "container 'lab' rodando o digest esperado"

Say "Deploy concluido do commit $sha"
