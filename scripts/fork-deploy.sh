#!/usr/bin/env bash
# Fork-only Docker deployment. Does not modify the upstream systemd installer.
set -Eeuo pipefail
umask 077

readonly FORK_REPO="https://github.com/slobys/dujiao-next.git"
readonly COMPOSE_PROJECT="dujiao-next-fork"
INSTALL_DIR="${DUJIAO_FORK_DIR:-/opt/dujiao-next-fork}"
BACKUP_DIR="${DUJIAO_FORK_BACKUP_DIR:-/var/backups/dujiao-next-fork}"
readonly MANAGED_MARKER=".dujiao-next-fork-managed"
STAGE_DIR=""
RESTORE_AFTER_BACKUP=0

info() { printf '[INFO] %s\n' "$*" >&2; }
die() { printf '[ERROR] %s\n' "$*" >&2; exit 1; }

random_admin_password() { printf 'Dn-%s-Aa9!' "$(openssl rand -hex 16)"; }
random_admin_path() { printf '/dj-%s' "$(openssl rand -hex 8)"; }

compose() {
  docker compose --project-name "$COMPOSE_PROJECT" \
    --project-directory "$INSTALL_DIR" \
    --env-file "$INSTALL_DIR/.env" -f "$INSTALL_DIR/compose.yaml" "$@"
}

cleanup() {
  local original_exit=$?
  trap - EXIT
  if (( RESTORE_AFTER_BACKUP == 1 )); then
    info "正在尝试重新启动备份前暂停的服务..."
    compose up -d --no-build >&2 || info "自动恢复失败，请运行 dujiao-fork restart 并查看日志。"
  fi
  if [[ -n "$STAGE_DIR" && -d "$STAGE_DIR" ]]; then
    rm -rf -- "$STAGE_DIR"
  fi
  exit "$original_exit"
}

require_root() {
  [[ "$EUID" -eq 0 ]] || die "请使用 sudo 或 root 执行。"
}

require_tools() {
  local tool
  for tool in docker git python3 openssl tar flock; do
    command -v "$tool" >/dev/null 2>&1 || die "缺少 $tool，请先安装。Debian/Ubuntu 可使用 apt 安装所需组件。"
  done
  docker compose version >/dev/null 2>&1 || die "需要 Docker Compose V2（docker compose），参见 https://docs.docker.com/compose/install/"
  docker info >/dev/null 2>&1 || die "Docker daemon 无法连接，请安装并启动 Docker 服务。"
}

validate_install_dir() {
  [[ "$INSTALL_DIR" == /* && "$INSTALL_DIR" != "/" && "$INSTALL_DIR" != *"/../"* && "$INSTALL_DIR" != */.. && "$INSTALL_DIR" != *"/./"* && "$INSTALL_DIR" != */. ]] \
    || die "安装目录必须是安全的绝对路径。"
  [[ ! -L "$INSTALL_DIR" ]] || die "拒绝使用符号链接安装目录。"
}

validate_port() {
  [[ "$1" =~ ^[0-9]{4,5}$ ]] && ((10#$1 >= 1024 && 10#$1 <= 65535))
}

validate_bind_ip() {
  python3 - "$1" <<'PY'
import ipaddress
import sys
try:
    assert isinstance(ipaddress.ip_address(sys.argv[1]), ipaddress.IPv4Address)
except (ValueError, AssertionError):
    sys.exit(1)
PY
}

generate_config() {
  # Arguments: example, target, username, password, admin_path, three secrets, redis_password
  python3 - "$@" <<'PY'
import json
import pathlib
import re
import sys

source, target, username, password, admin_path, app_key, jwt_key, user_key, redis_key = sys.argv[1:]
replacements = {
    ("app", "secret_key"): app_key,
    ("server", "mode"): "release",
    ("jwt", "secret"): jwt_key,
    ("user_jwt", "secret"): user_key,
    ("bootstrap", "default_admin_username"): username,
    ("bootstrap", "default_admin_password"): password,
    ("redis", "enabled"): True,
    ("redis", "host"): "redis",
    ("redis", "password"): redis_key,
    ("queue", "enabled"): True,
    ("queue", "host"): "redis",
    ("queue", "password"): redis_key,
    ("email", "enabled"): False,
    ("web", "admin_path"): admin_path,
}
lines = pathlib.Path(source).read_text(encoding="utf-8").splitlines(keepends=True)
section = None
seen = set()
output = []
for line in lines:
    top = re.match(r"^([a-z_]+):(?:\s*#.*)?\s*$", line)
    if top:
        section = top.group(1)
    key = re.match(r"^  ([a-z_]+):", line)
    item = (section, key.group(1)) if key else None
    if item in replacements:
        if item in seen:
            raise ValueError("duplicate config field: " + str(item))
        seen.add(item)
        value = replacements[item]
        line = "  " + item[1] + ": " + json.dumps(value, ensure_ascii=False) + "\n"
    output.append(line)
if seen != set(replacements):
    raise ValueError("required config keys missing: " + str(set(replacements) - seen))
if len({app_key, jwt_key, user_key, redis_key}) != 4:
    raise ValueError("application secrets must be distinct")
pathlib.Path(target).write_text("".join(output), encoding="utf-8")
PY
}

render_compose() {
  cat <<'YAML'
services:
  app:
    image: dujiao-next-fork:local
    build:
      context: ./src
      dockerfile: Dockerfile
      args:
        APP_VERSION: "source"
        APP_BUILD_TYPE: "source"
    restart: unless-stopped
    depends_on:
      redis:
        condition: service_healthy
    ports:
      - "${DUJIAO_BIND}:${DUJIAO_PORT}:8080"
    volumes:
      - ./data/config.yml:/app/config.yml:ro
      - ./data/db:/app/db
      - ./data/uploads:/app/uploads
      - ./data/logs:/app/logs
    security_opt:
      - no-new-privileges:true
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/health"]
      interval: 10s
      timeout: 5s
      retries: 12
      start_period: 30s

  redis:
    image: redis:7-alpine
    command: ["redis-server", "--appendonly", "yes", "--requirepass", "${REDIS_PASSWORD}"]
    environment:
      REDIS_PASSWORD: "${REDIS_PASSWORD}"
    restart: unless-stopped
    volumes:
      - ./data/redis:/data
    healthcheck:
      test: ["CMD-SHELL", "REDISCLI_AUTH=\"$$REDIS_PASSWORD\" redis-cli ping | grep -q PONG"]
      interval: 5s
      timeout: 4s
      retries: 12
    security_opt:
      - no-new-privileges:true
YAML
}

require_installed() {
  [[ -f "$INSTALL_DIR/$MANAGED_MARKER" && -f "$INSTALL_DIR/.env" && -f "$INSTALL_DIR/compose.yaml" && -d "$INSTALL_DIR/src/.git" ]] \
    || die "没有发现本脚本管理的安装，拒绝修改现有目录。请先运行 install。"
}

install_manager_link() {
  local link="/usr/local/sbin/dujiao-fork"
  mkdir -p /usr/local/sbin
  chmod 0755 "$INSTALL_DIR/src/scripts/fork-deploy.sh"
  if [[ ! -e "$link" && ! -L "$link" ]]; then
    ln -s "$INSTALL_DIR/src/scripts/fork-deploy.sh" "$link"
  elif [[ "$(readlink -f "$link")" != "$INSTALL_DIR/src/scripts/fork-deploy.sh" ]]; then
    info "已存在其他 $link，未覆盖。仍可用安装目录中的脚本管理。"
  fi
}

wait_for_health() {
  local id state i
  for ((i=0; i<50; i++)); do
    id=$(compose ps -q app 2>/dev/null || true)
    if [[ -n "$id" ]]; then
      state=$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' "$id" 2>/dev/null || true)
      if [[ "$state" == "healthy" ]]; then
        return 0
      fi
      [[ "$state" != "unhealthy" ]] || break
    fi
    sleep 3
  done
  compose ps >&2 || true
  compose logs --tail=40 app >&2 || true
  return 1
}

check_compose_project_conflicts() {
  local existing
  existing=$(docker ps -a --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" --format '{{.ID}}') \
    || die "无法查询 Docker 容器，请检查 daemon 权限。"
  [[ -z "$existing" ]] || die "已有其他容器占用 Compose 项目名 $COMPOSE_PROJECT，拒绝接管。"
}

run_install() {
  local bind_ip="${DUJIAO_BIND:-127.0.0.1}"
  local port="${DUJIAO_PORT:-18080}"
  validate_bind_ip "$bind_ip" || die "DUJIAO_BIND 必须是合法 IPv4 地址。"
  validate_port "$port" || die "DUJIAO_PORT 必须是 1024-65535 的端口。"
  if [[ -e "$INSTALL_DIR" ]]; then
    require_installed
    info "检测到上次安装目录，仅重新启动现有部署，不重置数据库和密钥。"
    compose up -d --build
    wait_for_health || die "启动失败；请查看日志。"
    install_manager_link
    info "已恢复部署。实际监听地址请查看 $INSTALL_DIR/.env（没有修改已有配置）。"
    return
  fi
  check_compose_project_conflicts

  mkdir -p -- "$(dirname "$INSTALL_DIR")"
  STAGE_DIR=$(mktemp -d "${INSTALL_DIR}.staging.XXXXXX")
  git clone --quiet --depth 1 --branch main "$FORK_REPO" "$STAGE_DIR/src" \
    || die "克隆 fork 仓库失败。"
  mkdir -p "$STAGE_DIR/data/"{db,uploads,logs,redis}
  chmod 0700 "$STAGE_DIR" "$STAGE_DIR/data" "$STAGE_DIR/data/"{db,uploads,logs,redis}

  local s1 s2 s3 redis_secret admin_password admin_path
  s1=$(openssl rand -hex 32)
  s2=$(openssl rand -hex 32)
  s3=$(openssl rand -hex 32)
  redis_secret=$(openssl rand -hex 32)
  admin_password=$(random_admin_password)
  admin_path=$(random_admin_path)
  generate_config "$STAGE_DIR/src/config.yml.example" "$STAGE_DIR/data/config.yml" \
    "admin" "$admin_password" "$admin_path" "$s1" "$s2" "$s3" "$redis_secret"
  printf 'DUJIAO_BIND=%s\nDUJIAO_PORT=%s\nREDIS_PASSWORD=%s\n' \
    "$bind_ip" "$port" "$redis_secret" > "$STAGE_DIR/.env"
  render_compose > "$STAGE_DIR/compose.yaml"
  chmod 0600 "$STAGE_DIR/.env" "$STAGE_DIR/data/config.yml"
  : > "$STAGE_DIR/$MANAGED_MARKER"

  mv -- "$STAGE_DIR" "$INSTALL_DIR"
  STAGE_DIR=""
  info "正在从 fork 源码构建全栈镜像（首次构建需下载依赖）..."
  compose up -d --build
  wait_for_health || die "启动未通过健康检查；配置和数据已保留，修复后可重运行 install。"
  install_manager_link
  printf '\n安装成功（请妥善保管首次凭据）：\n访问地址：http://%s:%s\n后台地址：http://%s:%s%s\n管理员：admin\n初始密码：%s\n\n' \
    "$bind_ip" "$port" "$bind_ip" "$port" "$admin_path" "$admin_password"
  info "默认仅监听本机！生产环境请通过 HTTPS 反向代理公开访问，并在首次登录后修改管理员密码。"
}

run_backup() {
  require_installed
  mkdir -p "$BACKUP_DIR"
  chmod 0700 "$BACKUP_DIR"
  local ts archive tmp
  ts=$(date -u +%Y%m%dT%H%M%SZ)
  archive="$BACKUP_DIR/dujiao-next-fork-${ts}-$$.tar.gz"
  tmp="${archive}.tmp"
  # Stop writers so SQLite WAL, Redis AOF and uploaded files are consistent.
  RESTORE_AFTER_BACKUP=1
  compose stop app redis
  if ! tar -C "$INSTALL_DIR" -czf "$tmp" .env compose.yaml data; then
    rm -f -- "$tmp"
    die "备份失败，原数据未删除。"
  fi
  if ! tar -tzf "$tmp" >/dev/null; then
    rm -f -- "$tmp"
    die "备份校验失败，原数据未删除。"
  fi
  chmod 0600 "$tmp"
  mv -- "$tmp" "$archive"
  compose up -d --no-build
  RESTORE_AFTER_BACKUP=0
  printf '已备份到：%s\n' "$archive"
}

run_update() {
  require_installed
  local dirty
  dirty=$(git -C "$INSTALL_DIR/src" status --porcelain)
  [[ -z "$dirty" ]] || die "源码工作树有未提交修改，拒绝覆盖。请先提交或妥善保存。"
  info "先备份全部配置、SQLite、上传文件和 Redis 数据..."
  run_backup
  git -C "$INSTALL_DIR/src" fetch --prune origin main
  git -C "$INSTALL_DIR/src" merge --ff-only origin/main || die "不是快进更新，已保留原有运行容器。"
  compose build app || die "构建失败：现有容器未被替换，备份仍在。"
  compose up -d --no-build
  wait_for_health || die "新版本健康检查未通过；请查看日志和恢复备份。"
  info "fork 已更新，配置与数据保持不变。"
}

usage() {
  cat <<'HELP'
Dujiao-Next fork Docker 管理器
用法：sudo bash fork-deploy.sh install|update|status|logs|restart|backup|help
      成功安装后也可运行：sudo dujiao-fork <命令>

首次安装可指定：
  DUJIAO_BIND=127.0.0.1  (默认，仅本机监听；使用 HTTPS 反向代理)
  DUJIAO_PORT=18080      (默认)
  DUJIAO_FORK_DIR=/opt/dujiao-next-fork
本脚本不会删除数据库、上传文件、Redis 数据，也不会执行官方上游安装器。
HELP
}

main() {
  local command="${1:-help}"
  case "$command" in
    help|-h|--help) usage; return ;;
    install|update|status|logs|restart|backup) ;;
    *) usage; die "未知命令：$command" ;;
  esac
  require_root
  validate_install_dir
  require_tools
  exec 9>/run/dujiao-next-fork-manager.lock
  flock -n 9 || die "已有另一个 fork 管理进程在运行。"
  trap cleanup EXIT
  case "$command" in
    install) run_install ;;
    update) run_update ;;
    status) require_installed; compose ps ;;
    logs) require_installed; compose logs --no-color --tail=120 app redis ;;
    restart) require_installed; compose restart; wait_for_health || die "重启后健康检查未通过。" ;;
    backup) run_backup ;;
  esac
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
