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
  [string]$AppUrl        = "https://k8slab-b9ue3.eastus2.cloudapp.azure.com",
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
# .claude/ fica de fora do check: as permissões de sessão do agente gravam ali
# a cada aprovação e não entram na imagem — travavam deploy limpo à toa.
$dirty = git -C $repoRoot status --porcelain | Where-Object { $_ -notmatch '\.claude/' }
if ($dirty -and -not $AllowDirty) {
  Die "working tree suja — commite antes (ou use -AllowDirty). A imagem em prod deve corresponder a um commit."
}
Say "Deploy do commit $sha (tags: $sha + latest)"

# ── 1.5 Lease anti-auto-stop: se a VM esta de pe, segura ela acordada ──
# O build leva ~5 min sem nenhuma atividade na VM — o timer lab-autostop ja
# desalocou no MEIO do deploy (2x em 2026-07-09). O lease /run/lab-busy faz o
# autostop pular; o script remoto do passo 4 o remove ao final.
$power = (az vm get-instance-view -g $ResourceGroup -n $VmName --query "instanceView.statuses[1].displayStatus" -o tsv).Trim()
if ($power -eq "VM running") {
  Say "VM de pe — colocando lease anti-auto-stop"
  az vm run-command invoke -g $ResourceGroup -n $VmName --command-id RunShellScript --scripts "touch /run/lab-busy" --query "value[0].message" -o tsv | Out-Null
  Ok "lease colocado"
}

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
touch /run/lab-busy
# Cada deploy empilha ~2GB de imagem; com o disco cheio o pull falha MUDO e o
# boot fica na imagem cacheada (visto em 2026-07-09: az login sem tmp, pull
# 'no space left'). Prune mantém as imagens dos containers rodando e NUNCA
# toca em volumes (lab-data = progresso dos usuários).
docker image prune -af >/dev/null 2>&1 || true
timeout 60 az acr login --name $Acr >/dev/null
timeout 240 docker pull $Acr.azurecr.io/${Image}:latest >/dev/null
systemctl restart estudo-app.service
sleep 10
want=`$(docker inspect --format '{{.Id}}' $Acr.azurecr.io/${Image}:latest)
got=`$(docker inspect --format '{{.Image}}' lab)
if [ "`$want" = "`$got" ]; then echo "MATCH `$got"; else echo "MISMATCH want=`$want got=`$got"; fi
# A imagem recem-substituida ficou orfa — limpa JA, nao no proximo deploy.
# Rollback continua a um pull de distancia (a ACR guarda todas as tags por SHA).
docker image prune -af >/dev/null 2>&1 || true
rm -f /run/lab-busy
"@
$tmp = New-TemporaryFile
Set-Content -Path $tmp -Value $remote -Encoding ascii
try {
  $out = az vm run-command invoke -g $ResourceGroup -n $VmName --command-id RunShellScript --scripts "@$tmp" --query "value[0].message" -o tsv
} finally {
  Remove-Item $tmp -Force -ErrorAction SilentlyContinue
}
# $out pode vir como ARRAY de linhas (az -o tsv): -notmatch em array FILTRA
# elementos em vez de testar boolean, e array nao-vazio e truthy — falso
# negativo com o deploy OK. Colapsa para string antes de testar.
$outStr = ($out | Out-String)
if ($outStr -notmatch "MATCH") {
  Write-Host $outStr
  Die "container 'lab' nao esta rodando a imagem recem-buildada"
}
Ok "container 'lab' rodando a imagem de $sha"

Say "Confirmando health e deploy gate remotos"
$baseUrl = $AppUrl.TrimEnd("/")
$healthy = $false
for ($i = 1; $i -le 12; $i++) {
  try {
    $health = (Invoke-WebRequest -UseBasicParsing -Uri "$baseUrl/healthz" -TimeoutSec 10).Content.Trim()
    if ($health -match "ok\s+$sha") { $healthy = $true; break }
  } catch { }
  Start-Sleep -Seconds 5
}
if (-not $healthy) { Die "healthz remoto nao confirmou o commit $sha" }
# O gate roda o golden eval INTEIRO na VM de 2 vCPU: ~2 min a frio. Timeout de
# 30s dava falso negativo com o deploy ja OK (2026-07-11) — 300s + 1 retry.
$gateOK = $false
$gateErr = ""
for ($i = 1; $i -le 2; $i++) {
  try {
    $gate = Invoke-RestMethod -Uri "$baseUrl/api/tutor/deploy-gate" -TimeoutSec 300
    if (-not $gate.passed) { Die ("deploy gate remoto falhou: " + ($gate.blockers -join "; ")) }
    $gateOK = $true; break
  } catch { $gateErr = $_.Exception.Message }
}
if (-not $gateOK) { Die "nao consegui confirmar o deploy gate remoto: $gateErr" }
Ok "healthz e deploy gate confirmados"
Say "Deploy concluido do commit $sha"
