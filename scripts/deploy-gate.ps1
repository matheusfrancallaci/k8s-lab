param(
  [string]$BaseUrl = "",
  [switch]$SkipGoTest
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$checks = New-Object System.Collections.Generic.List[string]
if (-not $SkipGoTest) {
  $env:GOCACHE = Join-Path $root ".tmp/go-build"
  go test -count=1 ./...
  $checks.Add("go test ./...")
}

if ($BaseUrl) {
  $gate = Invoke-RestMethod -Uri ($BaseUrl.TrimEnd("/") + "/api/tutor/deploy-gate") -TimeoutSec 60
  if (-not $gate.passed) {
    Write-Error ("Deploy gate remoto falhou: " + (($gate.blockers | ForEach-Object { $_ }) -join "; "))
  }
  $checks.Add("remote /api/tutor/deploy-gate")
}

$forbidden = rg -n "Lab Maker" web README.md 2>$null
if ($LASTEXITCODE -eq 0 -and $forbidden) {
  Write-Error "Label interno 'Lab Maker' apareceu em superficie visivel."
}
$checks.Add("no Lab Maker label")

Write-Host "Deploy gate OK:" ($checks -join ", ")
