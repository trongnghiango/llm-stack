# =============================================================================
# llm-stack Start Wrapper Script for Windows PowerShell
# Tự động khởi tạo cấu hình, sinh khóa bảo mật và khởi chạy Docker Compose
# =============================================================================

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ProjectRoot

$EnvFile = Join-Path $ProjectRoot ".env"
$EnvExample = Join-Path $ProjectRoot ".env.example"
$ConfigDir = Join-Path $ProjectRoot "config"
$ProxyConf = Join-Path $ConfigDir "proxy_routes.json"
$ProxyConfExample = Join-Path $ConfigDir "proxy_routes.json.example"
$CfAccounts = Join-Path $ConfigDir "cf_accounts.csv"
$CfAccountsExample = Join-Path $ConfigDir "cf_accounts.csv.example"
$NimAccounts = Join-Path $ConfigDir "nim_accounts.csv"
$NimAccountsExample = Join-Path $ConfigDir "nim_accounts.csv.example"

$LogsDir = Join-Path $ProjectRoot "data\logs"
$DbDir = Join-Path $ProjectRoot "data\omniroute"
$DbPath = Join-Path $DbDir "storage.sqlite"
$InitSql = Join-Path $ProjectRoot "data\init.sql"

# Đảm bảo thư mục config tồn tại
if (-not (Test-Path $ConfigDir)) {
    New-Item -ItemType Directory -Path $ConfigDir -Force | Out-Null
}

# 1. Tự động khởi tạo .env nếu chưa có và sinh key ngẫu nhiên bằng .NET
if (-not (Test-Path $EnvFile) -and (Test-Path $EnvExample)) {
    Write-Host "ℹ️ Khong tim thay .env. Tu dong tao tu .env.example..." -ForegroundColor Cyan
    Copy-Item $EnvExample $EnvFile

    try {
        Write-Host "🔑 Dang sinh khoa bao mat ngau nhien cho JWT_SECRET va API_KEY_SECRET..." -ForegroundColor Yellow
        $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
        
        # 48 bytes base64 for JWT
        $jwtBytes = New-Object byte[] 48
        $rng.GetBytes($jwtBytes)
        $jwtSec = [Convert]::ToBase64String($jwtBytes)

        # 32 bytes hex for AES key
        $apiBytes = New-Object byte[] 32
        $rng.GetBytes($apiBytes)
        $apiSec = ($apiBytes | ForEach-Object { "{0:x2}" -f $_ }) -join ""

        $content = Get-Content $EnvFile -Raw
        $content = $content -replace "JWT_SECRET=.*", "JWT_SECRET=$jwtSec"
        $content = $content -replace "API_KEY_SECRET=.*", "API_KEY_SECRET=$apiSec"
        Set-Content -Path $EnvFile -Value $content -NoNewline
    }
    catch {
        Write-Warning "Khong the tu dong sinh khoa bao mat: $_. Vui long tu dien JWT_SECRET va API_KEY_SECRET trong file .env."
    }
}

# 2. Tự động khởi tạo proxy_routes.json cho claude-proxy nếu chưa có
if (-not (Test-Path $ProxyConf) -and (Test-Path $ProxyConfExample)) {
    Write-Host "ℹ️ Tu dong tao config/proxy_routes.json tu ban vi du..." -ForegroundColor Cyan
    Copy-Item $ProxyConfExample $ProxyConf
}

# 3. Tự động khởi tạo cf_accounts.csv cho cf-ai-proxy nếu chưa có
if (-not (Test-Path $CfAccounts) -and (Test-Path $CfAccountsExample)) {
    Write-Host "ℹ️ Tu dong tao config/cf_accounts.csv tu ban vi du..." -ForegroundColor Cyan
    Copy-Item $CfAccountsExample $CfAccounts
}

# 4. Tự động khởi tạo nim_accounts.csv cho omniroute nếu chưa có
if (-not (Test-Path $NimAccounts) -and (Test-Path $NimAccountsExample)) {
    Write-Host "ℹ️ Tu dong tao config/nim_accounts.csv tu ban vi du..." -ForegroundColor Cyan
    Copy-Item $NimAccountsExample $NimAccounts
}

# 5. Tạo các thư mục dữ liệu cần thiết
$DirsToCreate = @(
    $DbDir,
    (Join-Path $ProjectRoot "data\redis"),
    $LogsDir,
    (Join-Path $LogsDir "claude-proxy"),
    (Join-Path $LogsDir "omniroute"),
    (Join-Path $LogsDir "omniroute-calls")
)

foreach ($dir in $DirsToCreate) {
    if (-not (Test-Path $dir)) {
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
    }
}

$DbExists = Test-Path $DbPath

# 6. Khởi động Docker Compose
Write-Host "🚀 Dang khoi dong llm-stack bang Docker Compose..." -ForegroundColor Green
docker compose down 2>$null
docker compose up -d --build

# 7. Thực hiện Seeding nếu DB được tạo mới
if (-not $DbExists) {
    Write-Host "⏳ Cho omniroute khoi chay va tao cau truc DB (5 giay)..." -ForegroundColor Yellow
    Start-Sleep -Seconds 5

    if (Test-Path $DbPath) {
        Write-Host "🤖 [Init] Dang nap cau hinh Custom Providers va Combos tu init.sql..." -ForegroundColor Cyan
        $pythonCmd = Get-Command python -ErrorAction SilentlyContinue
        if (-not $pythonCmd) { $pythonCmd = Get-Command python3 -ErrorAction SilentlyContinue }

        if ($pythonCmd -and (Test-Path $InitSql)) {
            $pyScript = @"
import sqlite3
import sys

try:
    conn = sqlite3.connect(r'$DbPath')
    cursor = conn.cursor()
    with open(r'$InitSql', 'r', encoding='utf-8') as f:
        sql = f.read()
    cursor.executescript(sql)
    conn.commit()
    conn.close()
    print('✅ [Init] Nap du lieu seed thanh cong!')
except Exception as e:
    print(f'❌ [Init] Loi seed: {e}', file=sys.stderr)
    sys.exit(1)
"@
            & $pythonCmd.Source -c $pyScript
            if ($LASTEXITCODE -eq 0) {
                Write-Host "🔄 Khoi dong lai omniroute de cap nhat cau hinh..." -ForegroundColor Green
                docker compose restart omniroute
            }
        }
        else {
            Write-Host "ℹ️ [Init] Chay tu dong tao du lieu mac dinh tren container..." -ForegroundColor Yellow
        }
    }
}

Write-Host "🎉 llm-stack da duoc khoi dong thanh cong!" -ForegroundColor Green
docker compose ps
