# =============================================================================
# llm-stack Unified CLI Manager Tool for Windows PowerShell
# =============================================================================

param (
    [Parameter(Position=0)]
    [string]$Command = "help",

    [Parameter(Position=1)]
    [string]$Option = ""
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $ProjectRoot

function Show-Usage {
    Write-Host "=============================================" -ForegroundColor Cyan
    Write-Host " llm-stack Unified CLI Manager (PowerShell)" -ForegroundColor Cyan
    Write-Host "=============================================" -ForegroundColor Cyan
    Write-Host "Su dung: .\stack.ps1 [lenh] [tuy chon]`n"
    Write-Host "Cac lenh ho tro:"
    Write-Host "  start             Khoi dong toan bo dich vu (tu dong tao .env & seed DB neu moi)" -ForegroundColor Green
    Write-Host "  stop              Dung va don dep cac containers" -ForegroundColor Yellow
    Write-Host "  restart [service] Khoi dong lai toan bo hoac 1 dich vu (vi du: omniroute, cf-ai-proxy)" -ForegroundColor Green
    Write-Host "  status            Xem trang thai hoat dong cua cac containers" -ForegroundColor Cyan
    Write-Host "  logs [service]    Xem logs truc tiep (tail logs) cua toan bo hoac 1 dich vu" -ForegroundColor Cyan
    Write-Host "  sync-nim          Dong bo tai khoan NVIDIA NIM tu config/nim_accounts.csv" -ForegroundColor Green
    Write-Host "  flush             Xoa sach bo nho dem (cache) trong Redis" -ForegroundColor Yellow
    Write-Host "  update [service]  Cap nhat image moi nhat cho dich vu (mac dinh: omniroute)" -ForegroundColor Cyan
    Write-Host "  help              Hien thi huong dan nay`n"
    Write-Host "Vi du:"
    Write-Host "  .\stack.ps1 start"
    Write-Host "  .\stack.ps1 sync-nim"
    Write-Host "  .\stack.ps1 logs cf-ai-proxy"
}

switch ($Command.ToLower()) {
    "start" {
        Write-Host "🚀 Dang khoi chay llm-stack..." -ForegroundColor Green
        & (Join-Path $ProjectRoot "start.ps1")
    }
    "stop" {
        Write-Host "🛑 Dang dung llm-stack..." -ForegroundColor Yellow
        docker compose down
        Write-Host "✅ Da dung thanh cong!" -ForegroundColor Green
    }
    "restart" {
        if ($Option) {
            Write-Host "🔄 Dang khoi dong lai dich vu: $Option..." -ForegroundColor Green
            docker compose restart $Option
        } else {
            Write-Host "🔄 Dang khoi dong lai toan bo stack..." -ForegroundColor Green
            docker compose restart
        }
        Write-Host "✅ Hoan thanh!" -ForegroundColor Green
    }
    "status" {
        Write-Host "📊 Trang thai cac dich vu:" -ForegroundColor Cyan
        docker compose ps
    }
    "logs" {
        if ($Option) {
            docker compose logs -f --tail=100 $Option
        } else {
            docker compose logs -f --tail=100
        }
    }
    "sync-nim" {
        Write-Host "🔍 Dang bat dau dong bo NVIDIA NIM tu config/nim_accounts.csv..." -ForegroundColor Cyan
        $syncScript = Join-Path $ProjectRoot "scripts\sync_nim_accounts.py"
        if (Test-Path $syncScript) {
            $pythonCmd = Get-Command python -ErrorAction SilentlyContinue
            if (-not $pythonCmd) { $pythonCmd = Get-Command python3 -ErrorAction SilentlyContinue }

            if ($pythonCmd) {
                & $pythonCmd.Source $syncScript
                Write-Host "🧹 Dang xoa sach cache cu trong Redis..." -ForegroundColor Cyan
                docker exec llm-redis redis-cli flushall 2>$null | Out-Null
                Write-Host "🔄 Khoi dong lai omniroute de nap cau hinh moi..." -ForegroundColor Green
                docker compose restart omniroute
                Write-Host "🎉 Hoan tat! NVIDIA NIM da duoc dong bo va san sang hoat dong." -ForegroundColor Green
            } else {
                Write-Host "❌ Loi: Can cai dat Python de chay script dong bo." -ForegroundColor Red
            }
        } else {
            Write-Host "❌ Loi: Khong tim thay file $syncScript" -ForegroundColor Red
        }
    }
    "flush" {
        Write-Host "🧹 Dang xoa sach bo nho dem (cache) Redis..." -ForegroundColor Yellow
        docker exec llm-redis redis-cli flushall
        Write-Host "✅ Da xoa sach cache Redis!" -ForegroundColor Green
    }
    "update" {
        $svc = if ($Option) { $Option } else { "omniroute" }
        Write-Host "📦 Bat dau cap nhat dich vu: $svc..." -ForegroundColor Cyan
        docker compose pull $svc
        docker compose up -d --no-deps $svc
        Write-Host "🎉 Cap nhat thanh cong $svc!" -ForegroundColor Green
    }
    default {
        Show-Usage
    }
}
