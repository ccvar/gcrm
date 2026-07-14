<#
=============================================================================
 GCRM —— 启停脚本（Windows / PowerShell，约定与 GCMS 的 cms.ps1 一致）

   用法：  .\scripts\gcrm.ps1 <命令>
   命令：  start | stop | restart | status | build | logs | pilot-build

   可用环境变量（运行前 $env:XXX 覆盖）：
     $env:CRM_ADDR=":9090"           监听地址（默认 :8090）
     $env:BASE_URL="https://..."     对外绝对地址（默认 http://localhost<CRM_ADDR>）
     $env:CRM_DB="C:\path\crm.db"    数据库路径（默认 data\crm.db）
=============================================================================
#>
param([string]$Command = "")

$ErrorActionPreference = "Stop"

$Root    = Split-Path -Parent $PSScriptRoot
$RunDir  = Join-Path $Root "run"
$LogDir  = Join-Path $Root "logs"
$PidFile = Join-Path $RunDir "gcrm.pid"
$LogFile = Join-Path $LogDir "gcrm.log"
$Bin     = Join-Path $Root "bin\gcrm.exe"

if (-not $env:CRM_ADDR) { $env:CRM_ADDR = ":8090" }
if (-not $env:CRM_DB)   { $env:CRM_DB = "data\crm.db" }

function Get-BaseUrl {
    if ($env:BASE_URL) { return $env:BASE_URL }
    if ($env:CRM_ADDR.StartsWith(":")) { return "http://localhost$($env:CRM_ADDR)" }
    return "http://$($env:CRM_ADDR)"
}

function Test-GcrmRunning {
    if (-not (Test-Path $PidFile)) { return $null }
    $procId = Get-Content $PidFile -ErrorAction SilentlyContinue
    if (-not $procId) { return $null }
    $p = Get-Process -Id $procId -ErrorAction SilentlyContinue
    if ($p) { return $p } else { return $null }
}

function Invoke-Build {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        Write-Error "未检测到 Go（需 >= 1.23），请安装：https://go.dev/dl/"
    }
    Write-Host "» 编译 gcrm …"
    New-Item -ItemType Directory -Force -Path (Join-Path $Root "bin") | Out-Null
    Push-Location $Root
    try { go build -trimpath -ldflags "-s -w" -o $Bin . }
    finally { Pop-Location }
    Write-Host "✓ 已编译 $Bin"
}

function Invoke-Start {
    $p = Test-GcrmRunning
    if ($p) { Write-Host "✓ 已在运行（PID $($p.Id)）"; return }
    if (-not (Test-Path $Bin)) { Invoke-Build }
    New-Item -ItemType Directory -Force -Path $RunDir, $LogDir | Out-Null
    $env:BASE_URL = Get-BaseUrl
    Write-Host "» 启动：监听 $($env:CRM_ADDR)，数据库 $($env:CRM_DB)"
    $proc = Start-Process -FilePath $Bin -WorkingDirectory $Root -WindowStyle Hidden `
        -RedirectStandardError $LogFile -RedirectStandardOutput (Join-Path $LogDir "gcrm.out.log") -PassThru
    Set-Content -Path $PidFile -Value $proc.Id
    Start-Sleep -Seconds 1
    if (Test-GcrmRunning) {
        Write-Host "✓ 已启动（PID $($proc.Id)）→ $(Get-BaseUrl)"
    } else {
        Write-Host "✗ 启动失败，最近日志：" -ForegroundColor Red
        if (Test-Path $LogFile) { Get-Content $LogFile -Tail 20 }
        exit 1
    }
}

function Invoke-Stop {
    $p = Test-GcrmRunning
    if (-not $p) { Write-Host "» 未在运行"; Remove-Item $PidFile -ErrorAction SilentlyContinue; return }
    Write-Host "» 停止 PID $($p.Id) …"
    Stop-Process -Id $p.Id -ErrorAction SilentlyContinue
    $p.WaitForExit(5000) | Out-Null
    Remove-Item $PidFile -ErrorAction SilentlyContinue
    Write-Host "✓ 已停止"
}

function Invoke-Status {
    $p = Test-GcrmRunning
    if ($p) { Write-Host "✓ 运行中（PID $($p.Id)）→ $(Get-BaseUrl)" } else { Write-Host "» 未在运行" }
}

function Invoke-Logs {
    if (-not (Test-Path $LogFile)) { New-Item -ItemType File -Force -Path $LogFile | Out-Null }
    Get-Content $LogFile -Tail 100 -Wait
}

function Invoke-PilotBuild {
    if (-not (Get-Command npm -ErrorAction SilentlyContinue)) { Write-Error "需要 Node/npm（Pilot 桌面端构建）" }
    Push-Location (Join-Path $Root "desktop")
    try { npm install; npm run tauri build }
    finally { Pop-Location }
    Write-Host "✓ Pilot 产物在 desktop\src-tauri\target\release\bundle\"
}

switch ($Command) {
    "start"       { Invoke-Start }
    "stop"        { Invoke-Stop }
    "restart"     { Invoke-Stop; Invoke-Build; Invoke-Start }
    "status"      { Invoke-Status }
    "build"       { Invoke-Build }
    "logs"        { Invoke-Logs }
    "pilot-build" { Invoke-PilotBuild }
    default {
        Write-Host "用法: .\scripts\gcrm.ps1 {start|stop|restart|status|build|logs|pilot-build}"
        Write-Host "  restart = 停止 → 重新编译 → 启动（本地改完代码一条命令生效）"
        exit 1
    }
}
