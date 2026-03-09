# ============================================================
# MinerIP Code Signing Script (Azure Trusted Signing)
# ============================================================
# Usage:
#   .\signing\sign.ps1
#
# Prerequisites:
#   1. Azure CLI installed and logged in (run: az login)
#   2. Azure Trusted Signing account set up with approved identity
#   3. Update metadata.json with your actual Endpoint and account names
# ============================================================

$ErrorActionPreference = "Stop"

# --- Configuration ---
$ProjectRoot    = Split-Path -Parent $PSScriptRoot
$ExePath        = Join-Path $ProjectRoot "MinerIP.exe"
$MetadataPath   = Join-Path $PSScriptRoot "metadata.json"
$ZipPath        = Join-Path $ProjectRoot "MinerIP.zip"
# Set this to your local website downloads folder (not committed)
$WebsiteDownloads = $env:MINERIP_WEBSITE_DOWNLOADS

$SignTool       = "C:\Program Files (x86)\Windows Kits\10\bin\10.0.22621.0\x64\signtool.exe"
$DlibPath       = Join-Path $env:LOCALAPPDATA "Microsoft\MicrosoftTrustedSigningClientTools\Azure.CodeSigning.Dlib.dll"
$TimestampUrl   = "http://timestamp.acs.microsoft.com"

# --- Validation ---
Write-Host ""
Write-Host "  ========================================" -ForegroundColor Cyan
Write-Host "  MinerIP Code Signing" -ForegroundColor Cyan
Write-Host "  ========================================" -ForegroundColor Cyan
Write-Host ""

if (-not (Test-Path $ExePath)) {
    Write-Host "[ERROR] MinerIP.exe not found at: $ExePath" -ForegroundColor Red
    Write-Host "        Build it first: go build -ldflags='-s -w' -o MinerIP.exe ." -ForegroundColor Yellow
    exit 1
}

if (-not (Test-Path $SignTool)) {
    Write-Host "[ERROR] SignTool.exe not found at: $SignTool" -ForegroundColor Red
    Write-Host "        Install Windows SDK or update the path in this script." -ForegroundColor Yellow
    exit 1
}

if (-not (Test-Path $DlibPath)) {
    Write-Host "[ERROR] Azure.CodeSigning.Dlib.dll not found at: $DlibPath" -ForegroundColor Red
    Write-Host "        Install: winget install -e --id Microsoft.Azure.TrustedSigningClientTools" -ForegroundColor Yellow
    exit 1
}

if (-not (Test-Path $MetadataPath)) {
    Write-Host "[ERROR] metadata.json not found at: $MetadataPath" -ForegroundColor Red
    exit 1
}

# --- Step 1: Check Azure Login ---
Write-Host "[1/5] Checking Azure authentication..." -ForegroundColor White
$azAccount = $null
try {
    $azAccount = az account show 2>$null | ConvertFrom-Json
} catch {}

if (-not $azAccount) {
    Write-Host "       Not logged in. Opening browser for Azure login..." -ForegroundColor Yellow
    az login
    $azAccount = az account show | ConvertFrom-Json
}
Write-Host "       Signed in as: $($azAccount.user.name)" -ForegroundColor Green
Write-Host "       Subscription: $($azAccount.name)" -ForegroundColor Green
Write-Host ""

# --- Step 2: Sign the EXE ---
Write-Host "[2/5] Signing MinerIP.exe..." -ForegroundColor White
Write-Host "       Using: Azure Trusted Signing" -ForegroundColor Gray
Write-Host "       Timestamp: $TimestampUrl" -ForegroundColor Gray
Write-Host ""

& $SignTool sign /v /debug /fd SHA256 `
    /tr $TimestampUrl /td SHA256 `
    /dlib $DlibPath `
    /dmdf $MetadataPath `
    $ExePath

if ($LASTEXITCODE -ne 0) {
    Write-Host ""
    Write-Host "[ERROR] Signing failed! Check the output above." -ForegroundColor Red
    Write-Host ""
    Write-Host "Common issues:" -ForegroundColor Yellow
    Write-Host "  - Identity validation not yet approved" -ForegroundColor Yellow
    Write-Host "  - Wrong Endpoint region in metadata.json" -ForegroundColor Yellow
    Write-Host "  - Missing IAM roles on your Azure account" -ForegroundColor Yellow
    exit 1
}
Write-Host ""
Write-Host "       Signing successful!" -ForegroundColor Green
Write-Host ""

# --- Step 3: Verify the signature ---
Write-Host "[3/5] Verifying signature..." -ForegroundColor White

& $SignTool verify /v /pa $ExePath

if ($LASTEXITCODE -ne 0) {
    Write-Host "[WARNING] Signature verification returned non-zero." -ForegroundColor Yellow
} else {
    Write-Host "       Signature verified!" -ForegroundColor Green
}
Write-Host ""

# --- Step 4: Package into ZIP ---
Write-Host "[4/5] Packaging MinerIP.zip..." -ForegroundColor White
if (Test-Path $ZipPath) { Remove-Item $ZipPath -Force }
Compress-Archive -Path $ExePath -DestinationPath $ZipPath -Force
$zipSize = [math]::Round((Get-Item $ZipPath).Length / 1MB, 1)
Write-Host "       Created: $ZipPath ($zipSize MB)" -ForegroundColor Green
Write-Host ""

# --- Step 5: Copy to website ---
Write-Host "[5/5] Copying to website downloads..." -ForegroundColor White
if ($WebsiteDownloads -and (Test-Path $WebsiteDownloads)) {
    Copy-Item $ZipPath (Join-Path $WebsiteDownloads "MinerIP.zip") -Force
    Write-Host "       Copied to: $WebsiteDownloads\MinerIP.zip" -ForegroundColor Green
} else {
    Write-Host "       Set MINERIP_WEBSITE_DOWNLOADS env var to auto-copy." -ForegroundColor Yellow
    Write-Host "       Otherwise, copy MinerIP.zip manually." -ForegroundColor Yellow
}

Write-Host ""
Write-Host "  ========================================" -ForegroundColor Cyan
Write-Host "  Done! MinerIP.exe is signed and ready." -ForegroundColor Cyan
Write-Host "  ========================================" -ForegroundColor Cyan
Write-Host ""
