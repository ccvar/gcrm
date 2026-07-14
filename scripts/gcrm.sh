#!/usr/bin/env sh
# =============================================================================
# GCRM —— 启停脚本（macOS / Linux，约定与 GCMS 的 cms.sh 一致）
#
#   用法：  ./scripts/gcrm.sh <命令>
#   命令：  start | stop | restart | status | build | logs | pilot-build
#
#   start 会自动检查 Go 环境：本机已装且 >= 1.23 直接用；否则自动下载官方
#         Go 工具链到项目内 .go/ 目录（不污染系统），构建后后台运行。
#
#   可用环境变量（可在命令前覆盖，或写进 scripts/gcrm.conf）：
#     CRM_ADDR=:9090        监听地址（默认 :8090）
#     BASE_URL=https://...  对外绝对地址（默认 http://localhost<CRM_ADDR>）
#     CRM_DB=/path/crm.db   数据库路径（默认 data/crm.db）
#     GO_VERSION=1.23.4     需要自动安装时下载的 Go 版本
# =============================================================================
set -eu

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
RUNDIR="$ROOT/run"
LOGDIR="$ROOT/logs"
PIDFILE="$RUNDIR/gcrm.pid"
LOGFILE="$LOGDIR/gcrm.log"
BIN="$ROOT/bin/gcrm"
GOROOT_LOCAL="$ROOT/.go/go"
CONF="$SCRIPT_DIR/gcrm.conf"

# ---- 配置文件（仅已知键；命令行环境变量优先）----
load_conf() {
  [ -f "$CONF" ] || return 0
  while IFS='=' read -r k v; do
    k=$(printf '%s' "$k" | tr -d '[:space:]')
    case "$k" in ''|\#*) continue ;; esac
    v=$(printf '%s' "$v" | sed 's/[[:space:]]*#.*$//; s/^[[:space:]]*//; s/[[:space:]]*$//')
    case "$k" in
      CRM_ADDR)   [ -n "${CRM_ADDR:-}" ]   || CRM_ADDR="$v" ;;
      BASE_URL)   [ -n "${BASE_URL:-}" ]   || BASE_URL="$v" ;;
      CRM_DB)     [ -n "${CRM_DB:-}" ]     || CRM_DB="$v" ;;
      GO_VERSION) [ -n "${GO_VERSION:-}" ] || GO_VERSION="$v" ;;
    esac
  done < "$CONF"
}
load_conf

CRM_ADDR=${CRM_ADDR:-:8090}
GO_VERSION=${GO_VERSION:-1.23.4}
CRM_DB=${CRM_DB:-data/crm.db}

# ---- 输出 ----
if [ -t 1 ]; then C_OK='\033[32m'; C_ERR='\033[31m'; C_DIM='\033[2m'; C_0='\033[0m'; else C_OK=; C_ERR=; C_DIM=; C_0=; fi
info() { printf "%b\n" "${C_DIM}» $*${C_0}"; }
ok()   { printf "%b\n" "${C_OK}✓ $*${C_0}"; }
err()  { printf "%b\n" "${C_ERR}✗ $*${C_0}" >&2; }

# ---- Go 环境：本机达标则用之，否则下载到 .go/ ----
go_ok() {
  command -v go >/dev/null 2>&1 || return 1
  v=$(go env GOVERSION 2>/dev/null | sed -e 's/^go//' -e 's/\.[0-9][0-9]*$//' -e 's/[a-z].*$//')
  major=${v%%.*}; minor=${v#*.}
  [ "${major:-0}" -gt 1 ] 2>/dev/null && return 0
  [ "${major:-0}" -eq 1 ] 2>/dev/null && [ "${minor:-0}" -ge 23 ] 2>/dev/null && return 0
  return 1
}

ensure_go() {
  if go_ok; then info "Go: $(go env GOVERSION)（系统）"; return; fi
  if [ -x "$GOROOT_LOCAL/bin/go" ]; then
    export PATH="$GOROOT_LOCAL/bin:$PATH"; export GOROOT="$GOROOT_LOCAL"
    if go_ok; then info "Go: $(go env GOVERSION)（项目内 .go/）"; return; fi
  fi
  info "未检测到合适的 Go（需 >= 1.23），自动安装 go${GO_VERSION} 到 .go/ …"
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
  case "$arch" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    armv6l|armv7l) arch=armv6l ;;
    *) err "不支持的 CPU 架构：${arch}，请手动安装 Go：https://go.dev/dl/"; exit 1 ;;
  esac
  case "$os" in linux|darwin) ;; *) err "请手动安装 Go：https://go.dev/dl/"; exit 1 ;; esac
  url="https://go.dev/dl/go${GO_VERSION}.${os}-${arch}.tar.gz"
  mkdir -p "$ROOT/.go"
  info "下载 $url"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" | tar -xz -C "$ROOT/.go"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO- "$url" | tar -xz -C "$ROOT/.go"
  else
    err "需要 curl 或 wget 才能自动安装 Go"; exit 1
  fi
  export PATH="$GOROOT_LOCAL/bin:$PATH"; export GOROOT="$GOROOT_LOCAL"
  go_ok && ok "已安装 $(go env GOVERSION) 到 .go/" || { err "Go 安装失败"; exit 1; }
}

base_url() {
  if [ -n "${BASE_URL:-}" ]; then printf '%s' "$BASE_URL"; return; fi
  case "$CRM_ADDR" in
    :*) printf 'http://localhost%s' "$CRM_ADDR" ;;
    *)  printf 'http://%s' "$CRM_ADDR" ;;
  esac
}

pid_alive() {
  [ -f "$PIDFILE" ] || return 1
  pid=$(cat "$PIDFILE" 2>/dev/null) || return 1
  [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null
}

do_build() {
  ensure_go
  info "编译 gcrm …"
  mkdir -p "$ROOT/bin"
  (cd "$ROOT" && go build -trimpath -ldflags "-s -w" -o "$BIN" .)
  ok "已编译 $BIN"
}

do_start() {
  if pid_alive; then ok "已在运行（PID $(cat "$PIDFILE")），无需重复启动"; return; fi
  [ -x "$BIN" ] || do_build
  mkdir -p "$RUNDIR" "$LOGDIR"
  info "启动：监听 $CRM_ADDR，数据库 $CRM_DB"
  CRM_ADDR="$CRM_ADDR" CRM_DB="$CRM_DB" BASE_URL="$(base_url)" \
    nohup "$BIN" >>"$LOGFILE" 2>&1 &
  echo $! > "$PIDFILE"
  sleep 1
  if pid_alive; then
    ok "已启动（PID $(cat "$PIDFILE")）→ $(base_url)"
  else
    err "启动失败，最近日志："; tail -20 "$LOGFILE" >&2 || true; exit 1
  fi
}

do_stop() {
  if ! pid_alive; then info "未在运行"; rm -f "$PIDFILE"; return; fi
  pid=$(cat "$PIDFILE")
  info "停止 PID $pid …"
  kill "$pid" 2>/dev/null || true
  for _ in 1 2 3 4 5 6 7 8 9 10; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.5
  done
  kill -0 "$pid" 2>/dev/null && { info "未退出，强制结束"; kill -9 "$pid" 2>/dev/null || true; }
  rm -f "$PIDFILE"
  ok "已停止"
}

do_status() {
  if pid_alive; then
    ok "运行中（PID $(cat "$PIDFILE")）→ $(base_url)"
  else
    info "未在运行"
  fi
}

do_logs() { touch "$LOGFILE"; tail -n 100 -f "$LOGFILE"; }

do_pilot_build() {
  command -v npm >/dev/null 2>&1 || { err "需要 Node/npm（Pilot 桌面端构建）"; exit 1; }
  (cd "$ROOT/desktop" && npm install && npm run tauri build)
  ok "Pilot 产物在 desktop/src-tauri/target/release/bundle/"
}

case "${1:-}" in
  start)       do_start ;;
  stop)        do_stop ;;
  restart)     do_stop; do_build; do_start ;;
  status)      do_status ;;
  build)       do_build ;;
  logs)        do_logs ;;
  pilot-build) do_pilot_build ;;
  *)
    echo "用法: $0 {start|stop|restart|status|build|logs|pilot-build}"
    echo "  restart = 停止 → 重新编译 → 启动（本地改完代码一条命令生效）"
    exit 1
    ;;
esac
