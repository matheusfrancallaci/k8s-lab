<#
.SYNOPSIS
  Backup do volume lab-data (progresso, contas, sessões) da VM Azure.

.DESCRIPTION
  O progresso dos usuários, o users.json e o sessions.json vivem no volume Docker
  lab-data numa VM só. Disco perdido = tudo perdido. Este script tira um tar.gz
  do volume. Por padrão grava em /opt/lab/backups na própria VM (protege contra
  corrupção do container, NÃO contra perda do disco). Passe -StorageAccount para
  também subir o tar para um blob (proteção real contra perda da VM/disco).

.EXAMPLE
  ./scripts/backup-lab-data.ps1
  ./scripts/backup-lab-data.ps1 -StorageAccount k8slabbackups -Container backups
#>
[CmdletBinding()]
param(
  [string]$ResourceGroup  = "k8slab-rg",
  [string]$VmName         = "k8slab-vm",
  [string]$StorageAccount = "",
  [string]$Container      = "backups"
)
$ErrorActionPreference = "Stop"
function Say($m) { Write-Host "==> $m" -ForegroundColor Cyan }

$stamp = Get-Date -Format "yyyyMMdd-HHmmss"
$file  = "lab-data-$stamp.tar.gz"

# Tar do volume via container efêmero (não precisa parar o app).
$script = @"
set -e
mkdir -p /opt/lab/backups
docker run --rm -v lab-data:/data:ro -v /opt/lab/backups:/backup busybox \
  tar czf /backup/$file -C /data .
echo "backup local: /opt/lab/backups/$file (`$(du -h /opt/lab/backups/$file | cut -f1))"
"@

if ($StorageAccount) {
  # Sobe para blob usando a managed identity da VM (--auth-mode login).
  $script += @"

az storage blob upload --auth-mode login --account-name $StorageAccount \
  --container-name $Container --name $file --file /opt/lab/backups/$file --overwrite
echo "backup remoto: $StorageAccount/$Container/$file"
"@
}

Say "Fazendo backup do volume lab-data na VM $VmName"
$out = az vm run-command invoke -g $ResourceGroup -n $VmName --command-id RunShellScript `
  --scripts $script --query "value[0].message" -o tsv
Write-Host $out
