Param(
    [int]$LmPort   = 1234,
    [int]$NatsPort = 4222
)

Write-Host "===> Step 1: Install LM Studio via winget" -ForegroundColor Cyan

if (-not (Get-Command winget -ErrorAction SilentlyContinue)) {
    Write-Host "[ERROR] winget not found. Update the App Installer in the Microsoft Store." -ForegroundColor Red
    exit 1
}

# Instala ou garante ElementLabs.LMStudio
winget install -e --id ElementLabs.LMStudio -h --accept-source-agreements --accept-package-agreements

Write-Host "===> Step 2: Bootstrap of the CLI 'lms'" -ForegroundColor Cyan

$lmBin = "$env:USERPROFILE\.lmstudio\bin\lms.exe"
if (Test-Path $lmBin) {
    Write-Host "Executing bootstrap of lms..."
    cmd /c "$lmBin bootstrap"
} else {
    Write-Host "[WARNING] $lmBin not found. Open LM Studio at least once and run this script again."
}

Write-Host "===> Step 3: Start LM Studio server on port $LmPort" -ForegroundColor Cyan

if (Get-Command lms -ErrorAction SilentlyContinue) {
    # --background pode variar por versão; se falhar, usuário inicia manualmente
    lms server start --port $LmPort --background | Out-Null
} else {
    Write-Host "[WARNING] 'lms' not in PATH. Start manually later:" -ForegroundColor Yellow
    Write-Host "  cmd /c `"$lmBin server start --port $LmPort`""
}

Write-Host "===> Step 4: Install NATS server (without Docker)" -ForegroundColor Cyan

if (Get-Command choco -ErrorAction SilentlyContinue) {
    choco install nats-server -y
} else {
    Write-Host "[WARNING] Chocolatey not found. Download NATS manually at https://nats.io/download/." -ForegroundColor Yellow
}

Write-Host "===> Step 5: Create simple NATS config with JetStream" -ForegroundColor Cyan

$configPath = Join-Path (Get-Location) "nats-server.conf"
@"
port: $NatsPort

jetstream {
  store_dir: "./nats_data"
  max_memory_store: 1073741824
  max_file_store:   107374182400
}
"@ | Out-File -FilePath $configPath -Encoding utf8

Write-Host "Config generated in $configPath"

Write-Host "Starting nats-server with JetStream..." -ForegroundColor Cyan

if (Get-Command nats-server -ErrorAction SilentlyContinue) {
    Start-Process nats-server -ArgumentList "-js","-c",$configPath -WindowStyle Minimized
    Write-Host "NATS server started at nats://127.0.0.1:$NatsPort"
} else {
    Write-Host "[WARNING] nats-server not in PATH. Start manually later." -ForegroundColor Yellow
}

Write-Host "===> Step 6: Generate .env for the Go service" -ForegroundColor Cyan

$envPath = Join-Path (Get-Location) ".env"
@"
NATS_URL=nats://127.0.0.1:$NatsPort
LMSTUDIO_BASE_URL=http://127.0.0.1:$LmPort
# LMSTUDIO_MODELS_DIR=%USERPROFILE%\.lmstudio\models
"@ | Out-File -FilePath $envPath -Encoding utf8

Write-Host "Content of .env:"
Get-Content $envPath

Write-Host "Windows setup completed."
