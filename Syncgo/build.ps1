# =============================================================================
#  build.ps1 -- Compila o Syncgo para Linux e empacota o instalador
#  Uso: .\build.ps1
# =============================================================================

$ErrorActionPreference = "Stop"
$SyncgoDir    = $PSScriptRoot
$InstallerDir = "$SyncgoDir\installer"
$OutBin       = "$SyncgoDir\syncgo_linux"
$InstBin      = "$InstallerDir\syncgo"

Write-Host ""
Write-Host "=== BUILD Syncgo (Linux x86_64) ===" -ForegroundColor Cyan

# -- Compila ------------------------------------------------------------------
Write-Host "[1/3] Compilando para Linux..." -ForegroundColor Yellow
$env:GOOS        = "linux"
$env:GOARCH      = "amd64"
$env:CGO_ENABLED = "0"

Push-Location $SyncgoDir
try {
    go build -ldflags="-s -w" -o $OutBin ./cmd/syncgo/
    if ($LASTEXITCODE -ne 0) { throw "go build falhou (exit $LASTEXITCODE)" }
} finally {
    Pop-Location
    Remove-Item Env:GOOS        -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH      -ErrorAction SilentlyContinue
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
}

$size = (Get-Item $OutBin).Length
Write-Host "[OK] Binario gerado: syncgo_linux ($([math]::Round($size/1MB,1)) MB)" -ForegroundColor Green

# -- Verifica ELF -------------------------------------------------------------
Write-Host "[2/3] Verificando formato ELF..." -ForegroundColor Yellow
$header = [System.IO.File]::ReadAllBytes($OutBin)[0..3]
$isELF  = ($header[0] -eq 0x7F -and $header[1] -eq 0x45 -and $header[2] -eq 0x4C -and $header[3] -eq 0x46)
if (-not $isELF) { throw "Binario invalido -- nao e ELF Linux" }
Write-Host "[OK] ELF Linux valido" -ForegroundColor Green

# -- Copia para installer/ ----------------------------------------------------
Write-Host "[3/3] Atualizando installer\syncgo..." -ForegroundColor Yellow
if (-not (Test-Path $InstallerDir)) { New-Item -ItemType Directory -Path $InstallerDir | Out-Null }
Copy-Item -Path $OutBin -Destination $InstBin -Force

$md5src  = (Get-FileHash $OutBin  -Algorithm MD5).Hash
$md5inst = (Get-FileHash $InstBin -Algorithm MD5).Hash
if ($md5src -ne $md5inst) { throw "Arquivo corrompido apos copia (MD5 diverge)" }
Write-Host "[OK] installer\syncgo atualizado -- MD5: $md5inst" -ForegroundColor Green

Write-Host ""
Write-Host "Concluido! Para instalar em um servidor:" -ForegroundColor Cyan
Write-Host "  1. Envie a pasta installer\ para o servidor"
Write-Host "  2. sudo bash install.sh"
Write-Host ""
