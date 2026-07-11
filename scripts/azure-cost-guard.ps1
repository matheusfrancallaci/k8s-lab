param(
  [switch]$Stop,
  [switch]$DeleteDuplicate,
  [string]$CanonicalResourceGroup = 'k8slab-rg'
)

$ErrorActionPreference = 'Stop'
$clusters = @(az aks list --query "[].{name:name,rg:resourceGroup,power:powerState.code,nodeRG:nodeResourceGroup}" -o json | ConvertFrom-Json)
if (-not $clusters) { Write-Host 'Nenhum AKS encontrado.'; exit 0 }

$clusters | Format-Table name, rg, power, nodeRG
$canonical = $CanonicalResourceGroup.Trim().ToLowerInvariant()
$duplicates = @($clusters | Where-Object { ([string]$_.rg).Trim().ToLowerInvariant() -ne $canonical })

if ($Stop) {
  foreach ($cluster in $clusters) {
    if ($cluster.power -ne 'Stopped') {
      Write-Host "Parando AKS $($cluster.rg)/$($cluster.name)..."
      az aks stop -g $cluster.rg -n $cluster.name --no-wait | Out-Null
    }
  }
}

if ($DeleteDuplicate) {
  throw 'Exclusao intencionalmente bloqueada: exporte manifests/dados e remova o cluster duplicado manualmente apos revisar o inventario acima.'
}

if ($duplicates) {
  Write-Warning "Cluster(es) fora do resource group canonico: $($duplicates.rg -join ', '). Eles continuam cobrando armazenamento/IP mesmo parados."
}
