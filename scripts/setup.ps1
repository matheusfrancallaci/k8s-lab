param(
    [switch]$Start
)

# K8s Study Lab - Setup Script (Windows + WSL)
# Instala kubectl, minikube e Docker DENTRO do WSL/Ubuntu.
# Execute: powershell -ExecutionPolicy Bypass -File scripts\setup.ps1
# Execute com -Start para reiniciar a aplicacao sem reinstalar nada.

$ErrorActionPreference = "Stop"

function Write-Step($msg)  { Write-Host "`n==> $msg" -ForegroundColor Cyan }
function Write-OK($msg)    { Write-Host "  [OK]   $msg" -ForegroundColor Green }
function Write-WARN($msg)  { Write-Host "  [WARN] $msg" -ForegroundColor Yellow }
function Write-Info($msg)  { Write-Host "  $msg" -ForegroundColor DarkGray }
function Write-Tempo($msg) { Write-Host "  Tempo estimado: $msg" -ForegroundColor DarkYellow }
function Write-FAIL($msg)  {
    Write-Host "`n  [FAIL] $msg" -ForegroundColor Red
    Read-Host "`nPressione Enter para fechar"
    exit 1
}

function Pedir-Acao($titulo, $descricao) {
    Write-Host ""
    Write-Host "  $titulo" -ForegroundColor Yellow
    Write-Info $descricao
    Write-Host ""
    Write-Host "  [1] Continuar com a instalacao existente" -ForegroundColor White
    Write-Host "  [2] Reinstalar do zero" -ForegroundColor White
    Write-Host "  [3] Cancelar" -ForegroundColor DarkGray
    Write-Host ""
    do { $escolha = Read-Host "  Escolha (1/2/3)" } while ($escolha -notin @('1','2','3'))
    return $escolha
}

function Run-Wsl($cmd) {
    $ErrorActionPreference = "Continue"
    Invoke-Expression "wsl.exe $cmd"
    $code = $LASTEXITCODE
    $ErrorActionPreference = "Stop"
    return $code
}

# -- Modo -Start: para e reinicia apenas a aplicacao Go ------------------
if ($Start) {
    Clear-Host
    Write-Host "============================================" -ForegroundColor Cyan
    Write-Host "  K8s Study Lab - Reiniciar Servico        " -ForegroundColor Cyan
    Write-Host "============================================" -ForegroundColor Cyan
    Write-Host ""

    $ErrorActionPreference = "Continue"
    $conn = Get-NetTCPConnection -LocalPort 8080 -State Listen -ErrorAction SilentlyContinue
    $ErrorActionPreference = "Stop"

    if ($conn) {
        $ownerPid = ($conn | Select-Object -First 1).OwningProcess
        $proc = Get-Process -Id $ownerPid -ErrorAction SilentlyContinue
        $procName = if ($proc) { $proc.Name } else { "pid $ownerPid" }
        Write-Host "  Servico encontrado: $procName (PID $ownerPid)" -ForegroundColor Yellow
        Write-Host "  Parando..." -ForegroundColor Yellow
        $ErrorActionPreference = "Continue"
        Stop-Process -Id $ownerPid -Force -ErrorAction SilentlyContinue
        $ErrorActionPreference = "Stop"
        Start-Sleep -Seconds 2
        Write-OK "Servico parado"
    } else {
        Write-Host "  Nenhum servico rodando na porta 8080" -ForegroundColor DarkGray
    }

    Write-Host ""
    Write-Host "  Iniciando K8s Study Lab..." -ForegroundColor Cyan
    $appDir = Split-Path -Parent $PSScriptRoot
    Start-Process powershell.exe -ArgumentList "-NoExit", "-Command", "cd '$appDir'; go run ."
    Write-Host ""
    Write-OK "Aplicacao iniciada - http://localhost:8080"
    Write-Host ""
    exit 0
}

# -- Auto-elevar para Administrador se necessario ----------------------
$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole(
    [Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
    Write-Host "`nEste script requer permissao de Administrador." -ForegroundColor Yellow
    Write-Host "Abrindo janela elevada (confirme o UAC que aparecer)...`n" -ForegroundColor Yellow
    $scriptArgs = "-ExecutionPolicy Bypass -File `"$PSCommandPath`""
    Start-Process powershell.exe -Verb RunAs -ArgumentList $scriptArgs
    exit 0
}

Clear-Host
Write-Host "============================================" -ForegroundColor Cyan
Write-Host "  K8s Study Lab - Setup (Windows + WSL)    " -ForegroundColor Cyan
Write-Host "============================================" -ForegroundColor Cyan
Write-Host ""
Write-Info "Este script configura o ambiente completo:"
Write-Info "  Etapa 1 - WSL2 (Windows Subsystem for Linux)"
Write-Info "  Etapa 2 - Ubuntu no WSL"
Write-Info "  Etapa 3 - Docker Engine (dentro do Ubuntu)"
Write-Info "  Etapa 4 - kubectl"
Write-Info "  Etapa 5 - minikube + primeiro start do cluster"
Write-Host ""

# =====================================================================
# ETAPA 1 - WSL
# =====================================================================
Write-Host "[ ETAPA 1 / 5 ]  WSL2" -ForegroundColor Cyan
Write-Host "--------------------------------------------" -ForegroundColor DarkGray

if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) {
    Write-FAIL "wsl.exe nao encontrado. Atualize para Windows 10 (versao 2004+) ou Windows 11."
}

$ErrorActionPreference = "Continue"
$null = wsl.exe --list 2>&1
$wslOk = ($LASTEXITCODE -eq 0)
$ErrorActionPreference = "Stop"

if ($wslOk) {
    $acao = Pedir-Acao "WSL ja esta instalado." "Continuar usa o WSL atual. Reinstalar exige reinicio do computador."
    if ($acao -eq '3') { Write-Host "Cancelado."; exit 0 }
    if ($acao -eq '2') {
        Write-Step "Reinstalando WSL..."
        Write-Tempo "2 a 5 minutos + reinicio obrigatorio"
        Write-Info "O Windows habilitara os recursos de virtualizacao e baixara o WSL."
        Write-Host ""
        $ErrorActionPreference = "Continue"
        $null = wsl.exe --unregister Ubuntu 2>&1
        wsl.exe --install 2>&1
        $ErrorActionPreference = "Stop"
        Write-Host ""
        Write-WARN "REINICIE O COMPUTADOR e execute este script novamente para continuar."
        Read-Host "`nPressione Enter para fechar"
        exit 0
    }
    Write-OK "WSL instalado e funcional"
} else {
    Write-Step "Instalando WSL2..."
    Write-Tempo "2 a 5 minutos + reinicio do computador obrigatorio"
    Write-Host ""
    Write-Info "O que vai acontecer agora:"
    Write-Info "  - Windows habilita o recurso 'Windows Subsystem for Linux'"
    Write-Info "  - Windows habilita o recurso 'Virtual Machine Platform'"
    Write-Info "  - Ubuntu sera baixado e instalado automaticamente"
    Write-Info "  - Progresso aparece nessa janela ou em janela separada do Windows"
    Write-Host ""
    $ErrorActionPreference = "Continue"
    wsl.exe --install 2>&1
    $ErrorActionPreference = "Stop"
    Write-Host ""
    Write-Host "  [!] PROXIMO PASSO:" -ForegroundColor Yellow
    Write-Host "      1. Reinicie o computador agora." -ForegroundColor Yellow
    Write-Host "      2. Apos reiniciar, abra um terminal e execute:" -ForegroundColor Yellow
    Write-Host "         powershell -ExecutionPolicy Bypass -File scripts\setup.ps1" -ForegroundColor White
    Write-Host ""
    Read-Host "Pressione Enter para fechar"
    exit 0
}

# =====================================================================
# ETAPA 2 - Ubuntu
# =====================================================================
Write-Host ""
Write-Host "[ ETAPA 2 / 5 ]  Ubuntu no WSL" -ForegroundColor Cyan
Write-Host "--------------------------------------------" -ForegroundColor DarkGray

$ErrorActionPreference = "Continue"
$distrosRaw = wsl.exe -l -q 2>&1
$ErrorActionPreference = "Stop"
$distros = ($distrosRaw -join "") -replace "`0", ""
$temUbuntu = $distros -match "Ubuntu"

if ($temUbuntu) {
    $acao = Pedir-Acao "Ubuntu ja esta instalado no WSL." "Reinstalar apaga TODOS os dados do Ubuntu WSL (kubectl, minikube, Docker, etc)."
    if ($acao -eq '3') { Write-Host "Cancelado."; exit 0 }
    if ($acao -eq '2') {
        Write-Step "Removendo Ubuntu e reinstalando..."
        Write-Tempo "3 a 8 minutos (download ~500 MB)"
        Write-Info "Apagando distribuicao Ubuntu existente..."
        $ErrorActionPreference = "Continue"
        $null = wsl.exe --unregister Ubuntu 2>&1
        Write-Info "Instalando Ubuntu novo..."
        wsl.exe --install -d Ubuntu --no-launch 2>&1
        $ok = ($LASTEXITCODE -eq 0)
        $ErrorActionPreference = "Stop"
        if (-not $ok) { Write-FAIL "Falha ao reinstalar Ubuntu. Verifique os erros acima." }
        Write-OK "Ubuntu reinstalado"
    } else {
        Write-OK "Ubuntu existente mantido"
    }
} else {
    Write-Step "Instalando Ubuntu no WSL..."
    Write-Tempo "3 a 8 minutos (download ~500 MB)"
    Write-Info "A imagem do Ubuntu sera baixada da Microsoft Store e registrada no WSL."
    Write-Host ""
    $ErrorActionPreference = "Continue"
    wsl.exe --install -d Ubuntu --no-launch 2>&1
    $ok = ($LASTEXITCODE -eq 0)
    $ErrorActionPreference = "Stop"
    if (-not $ok) { Write-FAIL "Falha ao instalar Ubuntu. Verifique os erros acima." }
    Write-OK "Ubuntu instalado"
}

# =====================================================================
# ETAPA 2.5 - Criar usuario WSL (se nao existir)
# =====================================================================
Write-Host ""
Write-Host "[ ETAPA 2.5 ]  Usuario no Ubuntu WSL" -ForegroundColor Cyan
Write-Host "--------------------------------------------" -ForegroundColor DarkGray

# Buscar primeiro usuario com UID >= 1000 (usuario real, nao root)
$ErrorActionPreference = "Continue"
$wslUser = wsl.exe -u root -- id -un 1000
$wslUserExit = $LASTEXITCODE
$ErrorActionPreference = "Stop"
$wslUser = if ($wslUser) { $wslUser.Trim() } else { "" }

if ($wslUser -and $wslUserExit -eq 0) {
    Write-OK "Usuario WSL encontrado: $wslUser"
} else {
    Write-Step "Nenhum usuario configurado no Ubuntu. Criando automaticamente..."
    Write-Host ""

    # Sugerir o nome de usuario do Windows como padrao
    $sugestao = ($env:USERNAME.ToLower() -replace '[^a-z0-9_]', '')
    if (-not $sugestao) { $sugestao = "k8slab" }

    do {
        $novoUser = Read-Host "  Nome do usuario Linux (Enter para usar '$sugestao')"
        if (-not $novoUser) { $novoUser = $sugestao }
        $novoUser = ($novoUser.ToLower() -replace '[^a-z0-9_-]', '')
    } while (-not $novoUser)

    Write-Step "Criando usuario '$novoUser' no Ubuntu WSL..."
    $ErrorActionPreference = "Continue"

    # Criar usuario com home e shell bash
    wsl.exe -u root -- useradd -m -s /bin/bash $novoUser

    # Adicionar ao sudoers sem senha (necessario para instalar pacotes)
    wsl.exe -u root -- bash -c "echo '$novoUser ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/$novoUser && chmod 0440 /etc/sudoers.d/$novoUser"

    # Definir como usuario padrao do WSL
    wsl.exe -u root -- bash -c "printf '[user]\ndefault=$novoUser\n' > /etc/wsl.conf"

    $ErrorActionPreference = "Stop"
    Write-OK "Usuario '$novoUser' criado e configurado como padrao do WSL"
    $wslUser = $novoUser

    # Reiniciar WSL para aplicar o usuario padrao
    Write-Info "Reiniciando WSL para aplicar configuracoes..."
    $ErrorActionPreference = "Continue"
    wsl.exe --shutdown
    $ErrorActionPreference = "Stop"
    Start-Sleep -Seconds 3
}

# =====================================================================
# ETAPAS 3, 4 e 5 - Docker + kubectl + minikube (dentro do Ubuntu/WSL)
# =====================================================================
Write-Host ""
Write-Host "[ ETAPAS 3-5 ]  Docker + kubectl + minikube" -ForegroundColor Cyan
Write-Host "--------------------------------------------" -ForegroundColor DarkGray
Write-Tempo "5 a 15 minutos (depende da internet e se Docker ja esta instalado)"
Write-Host ""
Write-Info "O que sera instalado dentro do Ubuntu:"
Write-Info "  Etapa 3 - Docker Engine  (motor de containers, necessario para o minikube)"
Write-Info "  Etapa 4 - kubectl        (~50 MB, cliente Kubernetes)"
Write-Info "  Etapa 5 - minikube       (~100 MB, cluster K8s local + primeiro start)"
Write-Host ""
Write-Info "Acompanhe o progresso linha a linha abaixo:"
Write-Host "--------------------------------------------" -ForegroundColor DarkGray
Write-Host ""

$scriptPath = (Resolve-Path (Join-Path $PSScriptRoot "wsl-init.sh")).Path

# Converter caminho Windows para caminho WSL manualmente (evita problemas com wslpath)
# Ex: C:\desenv\estudo-app\scripts\wsl-init.sh -> /mnt/c/desenv/estudo-app/scripts/wsl-init.sh
$drive   = $scriptPath.Substring(0, 1).ToLower()
$rest    = $scriptPath.Substring(2).Replace('\', '/')
$wslPath = "/mnt/$drive$rest"

Write-Info "Script Linux: $wslPath"

$ErrorActionPreference = "Continue"
wsl.exe -- bash -c "chmod +x '$wslPath'; bash '$wslPath'"
$exitCode = $LASTEXITCODE
$ErrorActionPreference = "Stop"

if ($exitCode -ne 0) {
    Write-FAIL "A instalacao Linux falhou (codigo $exitCode). Leia os erros acima e tente novamente."
}

# =====================================================================
# VERIFICACAO FINAL
# =====================================================================
Write-Host ""
Write-Host "[ VERIFICACAO FINAL ]" -ForegroundColor Cyan
Write-Host "--------------------------------------------" -ForegroundColor DarkGray
Write-Host ""

$ErrorActionPreference = "Continue"
$kubectlPath  = wsl.exe -- bash -c "command -v kubectl 2>/dev/null"
$minikubePath = wsl.exe -- bash -c "command -v minikube 2>/dev/null"
$null = wsl.exe -- kubectl cluster-info --request-timeout=5s 2>&1
$clusterOk = ($LASTEXITCODE -eq 0)
$ErrorActionPreference = "Stop"

if ($kubectlPath)  { Write-OK "kubectl   : $($kubectlPath.Trim())"  } else { Write-WARN "kubectl nao encontrado no WSL"  }
if ($minikubePath) { Write-OK "minikube  : $($minikubePath.Trim())" } else { Write-WARN "minikube nao encontrado no WSL" }
if ($clusterOk)    { Write-OK "Cluster   : respondendo"             } else { Write-WARN "Cluster ainda nao responde (a aplicacao tentara subir ao iniciar)" }

Write-Host ""
Write-Host "============================================" -ForegroundColor Green
Write-Host "  Setup concluido!" -ForegroundColor Green
Write-Host "============================================" -ForegroundColor Green
Write-Host "  Para iniciar o app:  go run ." -ForegroundColor White
Write-Host "  Acesso:              http://localhost:8080" -ForegroundColor White
Write-Host ""
Read-Host "Pressione Enter para fechar"
