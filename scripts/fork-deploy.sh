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
  if (( NETWORK_PENDING == 1 )); then
    info "监听配置未成功应用，正在恢复此前的 .env 和应用容器..."
    if [[ -n "$NETWORK_BACKUP" && -f "$NETWORK_BACKUP" ]] &&
       install -m 0600 "$NETWORK_BACKUP" "$INSTALL_DIR/.env"; then
      if recreate_app_only && wait_for_health; then
        info "已恢复旧监听配置和应用健康状态。"
        rm -f -- "$NETWORK_BACKUP"
        NETWORK_BACKUP=""
        NETWORK_PENDING=0
      else
        info "警告：自动恢复容器未通过健康检查；旧配置已还原。请手动检查 sudo dujiao-fork status/logs。"
        info "备份仍保存在：$NETWORK_BACKUP（文件权限 0600）。"
      fi
    else
      info "警告：旧监听环境文件无法还原，请检查备份：$NETWORK_BACKUP。"
    fi
  fi
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

APT_UPDATED=0
APT_DISTRO=""
APT_CODENAME=""
APT_ARCH=""

has_command() { command -v "$1" >/dev/null 2>&1; }

platform_supported() {
  case "$1:$2:$3" in
    ubuntu:jammy:amd64|ubuntu:jammy:arm64|ubuntu:noble:amd64|ubuntu:noble:arm64|\
ubuntu:resolute:amd64|ubuntu:resolute:arm64|\
debian:bookworm:amd64|debian:bookworm:arm64|debian:trixie:amd64|debian:trixie:arm64)
      return 0 ;;
    *) return 1 ;;
  esac
}

detect_apt_platform() {
  [[ -r /etc/os-release ]] || die "无法识别操作系统；自动安装依赖只支持 Debian/Ubuntu。"
  local ID="" VERSION_CODENAME="" UBUNTU_CODENAME=""
  # shellcheck disable=SC1091
  source /etc/os-release
  APT_DISTRO="$ID"
  if [[ "$ID" == "ubuntu" ]]; then
    APT_CODENAME="${UBUNTU_CODENAME:-$VERSION_CODENAME}"
  else
    APT_CODENAME="$VERSION_CODENAME"
  fi
  has_command apt-get && has_command dpkg || die "系统缺少 apt-get/dpkg，无法安全自动安装依赖。"
  APT_ARCH=$(dpkg --print-architecture)
  platform_supported "$APT_DISTRO" "$APT_CODENAME" "$APT_ARCH" ||
    die "不支持自动安装：${APT_DISTRO}/${APT_CODENAME}/${APT_ARCH}。支持 Ubuntu 22.04/24.04/26.04 或 Debian 12/13（amd64/arm64）。"
}

apt_update_once() {
  if (( APT_UPDATED == 0 )); then
    info "更新 APT 软件包索引..."
    apt-get update || die "APT 软件包索引更新失败，请检查网络和软件源。"
    APT_UPDATED=1
  fi
}

apt_install() {
  (($# > 0)) || return 0
  info "自动安装缺少的软件包：$*"
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@" ||
    die "APT 依赖安装失败：$*（没有删除或重装现有 Docker）。"
}

ensure_flock_for_install() {
  has_command flock && return 0
  detect_apt_platform
  apt_update_once
  apt_install util-linux
  has_command flock || die "util-linux 安装后仍找不到 flock。"
}

missing_base_packages() {
  local needs_apt_repo_tools=${1:-false}
  local tool package
  for tool in git python3 openssl tar; do
    has_command "$tool" || printf '%s\n' "$tool"
  done
  # A NAS with a healthy Docker/Compose/Buildx needs no curl, GPG or APT changes.
  if [[ "$needs_apt_repo_tools" == "true" ]]; then
    for tool in curl gpg; do
      case "$tool" in
        gpg) package=gnupg ;;
        *) package="$tool" ;;
      esac
      has_command "$tool" || printf '%s\n' "$package"
    done
    has_command update-ca-certificates || printf '%s\n' ca-certificates
  fi
}

assert_no_conflicting_engine_packages() {
  local package
  for package in docker-ce docker-ce-cli containerd.io docker.io docker-compose docker-compose-v2 docker-doc docker-buildx podman-docker containerd runc; do
    if dpkg-query -W -f='${Status}' "$package" 2>/dev/null | grep -qx 'install ok installed'; then
      die "发现已安装的 ${package}，不能安全自动替换 Docker Engine；未做任何卸载。"
    fi
  done
}

verify_docker_gpg_key() {
  local fingerprint
  fingerprint=$(gpg --batch --show-keys --with-colons "$1" 2>/dev/null | awk -F: '$1 == "fpr" {print $10; exit}') ||
    return 1
  [[ "$fingerprint" == "9DC858229FC7DD38854AE2D88D81803C0EBFCD88" ]]
}

docker_apt_source() {
  cat <<EOF
Types: deb
URIs: https://download.docker.com/linux/${APT_DISTRO}
Suites: ${APT_CODENAME}
Components: stable
Architectures: ${APT_ARCH}
Signed-By: /etc/apt/keyrings/docker.asc
EOF
}

setup_official_docker_apt_repo() {
  local source_file="/etc/apt/sources.list.d/dujiao-next-docker.sources"
  local key_file="/etc/apt/keyrings/docker.asc"
  if [[ -e "$source_file" ]]; then
    [[ -f "$source_file" ]] &&
      grep -Fxq "URIs: https://download.docker.com/linux/${APT_DISTRO}" "$source_file" &&
      grep -Fxq "Suites: ${APT_CODENAME}" "$source_file" &&
      grep -Fxq "Architectures: ${APT_ARCH}" "$source_file" &&
      grep -Fxq "Signed-By: ${key_file}" "$source_file" ||
      die "既有 Docker APT 源配置不符合预期：${source_file}，拒绝覆盖。"
    return 0
  fi
  if [[ -f /etc/apt/sources.list.d/docker.sources || -f /etc/apt/sources.list.d/docker.list ]]; then
    info "检测到原有 Docker 官方仓库文件，直接复用，不覆盖。"
    return 0
  fi
  install -m 0755 -d /etc/apt/keyrings
  if [[ -e "$key_file" ]]; then
    [[ -f "$key_file" && ! -L "$key_file" ]] &&
      verify_docker_gpg_key "$key_file" ||
      die "Docker 公钥与已知指纹不匹配；拒绝覆盖原有密钥。"
  else
    local temp_key
    temp_key=$(mktemp /etc/apt/keyrings/.dujiao-docker-key.XXXXXX)
    if ! curl --fail --silent --show-error --location \
      --proto '=https' --proto-redir '=https' --tlsv1.2 \
      --connect-timeout 10 --max-time 60 \
      "https://download.docker.com/linux/${APT_DISTRO}/gpg" -o "$temp_key"; then
      rm -f -- "$temp_key"
      die "下载 Docker 公钥失败，请检查网络。"
    fi
    if ! verify_docker_gpg_key "$temp_key"; then
      rm -f -- "$temp_key"
      die "Docker 公钥指纹校验失败，安装已停止。"
    fi
    install -m 0644 "$temp_key" "$key_file"
    rm -f -- "$temp_key"
  fi
  local temp_source
  temp_source=$(mktemp /etc/apt/sources.list.d/.dujiao-docker-source.XXXXXX)
  docker_apt_source > "$temp_source"
  install -m 0644 "$temp_source" "$source_file"
  rm -f -- "$temp_source"
  APT_UPDATED=0
  info "已配置 Docker 官方 APT 仓库（不执行 get.docker.com 安装脚本）。"
}

assert_plugin_install_does_not_modify_engine() {
  local plan
  plan=$(apt-get --assume-no --dry-run install --no-install-recommends "$@") ||
    die "APT 插件安装预检失败，为保护现有 Docker，拒绝继续。"
  if grep -Eq '^(Remv |Inst (docker-ce |docker-ce-cli |docker.io |containerd |containerd.io |runc |podman-docker ))' <<< "$plan"; then
    die "补装插件可能卸载/升级现有 Docker 或容器运行时，拒绝操作。"
  fi
}

ensure_install_dependencies() {
  local -a basic=() plugins=()
  local package needs_apt_repo_tools=false
  if ! has_command docker; then
    needs_apt_repo_tools=true
  elif ! docker compose version >/dev/null 2>&1 || ! docker buildx version >/dev/null 2>&1; then
    needs_apt_repo_tools=true
  fi
  while IFS= read -r package; do
    [[ -z "$package" ]] || basic+=("$package")
  done < <(missing_base_packages "$needs_apt_repo_tools")
  if ((${#basic[@]} > 0)); then
    detect_apt_platform
    apt_update_once
    apt_install "${basic[@]}"
  fi

  if ! has_command docker; then
    detect_apt_platform
    has_command systemctl || die "此服务器无法通过 systemctl 管理 Docker daemon；自动全新安装需要 systemd。"
    assert_no_conflicting_engine_packages
    setup_official_docker_apt_repo
    apt_update_once
    apt_install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
    systemctl enable --now docker ||
      die "Docker 已安装，但启动失败；请检查 systemctl status docker。"
  else
    docker compose version >/dev/null 2>&1 || plugins+=(docker-compose-plugin)
    docker buildx version >/dev/null 2>&1 || plugins+=(docker-buildx-plugin)
    if ((${#plugins[@]} > 0)); then
      detect_apt_platform
      setup_official_docker_apt_repo
      apt_update_once
      assert_plugin_install_does_not_modify_engine "${plugins[@]}"
      apt_install "${plugins[@]}"
    else
      info "Docker、Compose V2 和 Buildx 已存在，直接复用，不升级 Docker。"
    fi
    if ! docker info >/dev/null 2>&1 && has_command systemctl; then
      info "Docker 已安装但暂不可连接，尝试启动服务（不重启正在运行的容器）。"
      systemctl start docker || true
    fi
  fi
  require_tools
  docker buildx version >/dev/null 2>&1 ||
    die "Docker Buildx 不可用，无法从源码构建。"
}

require_tools() {
  local tool
  for tool in docker git python3 openssl tar flock; do
    has_command "$tool" || die "缺少 $tool；请运行 dujiao-fork install 自动补装。"
  done
  docker compose version >/dev/null 2>&1 ||
    die "Docker Compose V2 不可用，请运行 dujiao-fork install。"
  docker info >/dev/null 2>&1 ||
    die "Docker daemon 不可用，请检查 systemctl status docker。"
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
  local ip=$1 octet
  [[ "$ip" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || return 1
  local -a octets
  IFS=. read -r -a octets <<< "$ip"
  for octet in "${octets[@]}"; do
    [[ "$octet" == "0" || "$octet" != 0* ]] || return 1
    ((10#$octet <= 255)) || return 1
  done
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
  # Refuse foreign installation directories before changing host packages.
  if [[ -e "$INSTALL_DIR" ]]; then
    require_installed
  fi
  ensure_install_dependencies
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


# Keep the generated .env as data; never source it, expose Redis secrets or
# replace the entire Compose application when only the published port changes.
NETWORK_BACKUP=""
NETWORK_PENDING=0

network_env_current() {
  local env_file="$INSTALL_DIR/.env"
  [[ -f "$env_file" && ! -L "$env_file" ]] || die "环境文件不存在或是符号链接，拒绝修改。"
  local current
  current=$(awk '
    /^DUJIAO_BIND=/ {bind=substr($0,13); binds++}
    /^DUJIAO_PORT=/ {port=substr($0,13); ports++}
    END {
      if (binds != 1 || ports != 1) exit 1
      printf "%s %s\n", bind, port
    }' "$env_file") || die "环境文件的监听配置不存在或出现重复字段，拒绝修改。"
  local bind port
  read -r bind port <<< "$current"
  validate_bind_ip "$bind" && validate_port "$port" ||
    die "现有环境文件监听 IP/端口无效，拒绝修改。"
  printf '%s %s\n' "$bind" "$port"
}

network_env_write() {
  local env_file=$1 bind=$2 port=$3
  validate_bind_ip "$bind" && validate_port "$port" || die "监听 IP/端口不合法。"
  python3 - "$env_file" "$bind" "$port" <<'PY'
import os
import pathlib
import stat
import sys
import tempfile

path = pathlib.Path(sys.argv[1])
bind, port = sys.argv[2:]
if not stat.S_ISREG(os.lstat(path).st_mode):
    raise SystemExit("拒绝修改非普通文件或符号链接")
original = path.read_text(encoding="utf-8")
lines = original.splitlines(keepends=True)
replacements = {"DUJIAO_BIND": bind, "DUJIAO_PORT": port}
counts = {key: 0 for key in replacements}
updated = []
for line in lines:
    key, delimiter, _ = line.partition("=")
    if delimiter and key in replacements:
        counts[key] += 1
        updated.append(f"{key}={replacements[key]}\n")
    else:
        updated.append(line)
if any(count != 1 for count in counts.values()):
    raise SystemExit("监听字段缺失或重复，原文件未修改")
tmp_name = None
try:
    with tempfile.NamedTemporaryFile("w", encoding="utf-8",
                                     prefix=".env.network.", dir=path.parent,
                                     delete=False) as handle:
        tmp_name = handle.name
        os.fchmod(handle.fileno(), 0o600)
        handle.write("".join(updated))
        handle.flush()
        os.fsync(handle.fileno())
    os.replace(tmp_name, path)
except BaseException:
    if tmp_name and os.path.exists(tmp_name):
        os.unlink(tmp_name)
    raise
PY
}

confirm_public_bind() {
  local bind=$1 answer
  [[ "$bind" == 127.* ]] && return 0
  if [[ "${DUJIAO_CONFIRM_PUBLIC:-}" == "YES" ]]; then
    return 0
  fi
  [[ -r /dev/tty && -w /dev/tty ]] ||
    die "公网监听需明确确认。自动化请设置 DUJIAO_CONFIRM_PUBLIC=YES，并确保有 HTTPS 和防火墙策略。"
  printf '\n[警告] 将商城绑定到非回环 IP：%s，可能允许其它机器访问管理后台。\n请先限制 Vultr/系统防火墙来源，并配置 HTTPS 反向代理。\n确认继续请输入 PUBLIC：' "$bind" >/dev/tty
  IFS= read -r answer </dev/tty || die "操作已取消。"
  [[ "$answer" == "PUBLIC" ]] || die "没有获得公网监听确认，未修改配置。"
}

recreate_app_only() {
  # Compose restart does not update port bindings; only recreate app.
  compose up -d --no-build --no-deps --force-recreate app
}

apply_network_configuration() {
  local bind=$1 port=$2 previous_bind previous_port
  require_installed
  read -r previous_bind previous_port < <(network_env_current)
  validate_bind_ip "$bind" || die "监听 IP 无效：$bind。仅支持 IPv4 地址。"
  validate_port "$port" || die "监听端口无效：$port。必须在 1024-65535 之间。"
  if [[ "$bind" == "$previous_bind" && "$port" == "$previous_port" ]]; then
    info "当前已监听 ${bind}:${port}，无需重建容器。"
    return 0
  fi
  confirm_public_bind "$bind"

  NETWORK_BACKUP=$(mktemp "${INSTALL_DIR}/.env.network-backup.XXXXXXXX")
  if ! install -m 0600 "$INSTALL_DIR/.env" "$NETWORK_BACKUP"; then
    rm -f -- "$NETWORK_BACKUP"
    NETWORK_BACKUP=""
    die "无法保护旧环境文件，拒绝应用变更。"
  fi
  NETWORK_PENDING=1
  network_env_write "$INSTALL_DIR/.env" "$bind" "$port" ||
    die "生成网络配置失败，正在恢复。"
  compose config --quiet || die "Docker Compose 校验未通过，正在恢复。"
  recreate_app_only || die "应用容器重新创建失败，正在恢复旧监听配置。"
  wait_for_health || die "应用重建后健康检查失败，正在恢复旧监听配置。"

  NETWORK_PENDING=0
  rm -f -- "$NETWORK_BACKUP"
  NETWORK_BACKUP=""
  info "监听已变更：${previous_bind}:${previous_port} → ${bind}:${port}。"
  info "仅重建商城应用容器，未重建 Redis 或更改数据库、商品信息。"
  if [[ "$bind" != 127.* ]]; then
    info "警告：商城已绑定非回环 IP。请使用 HTTPS 反向代理，并限制公网端口来源。"
  fi
}

prompt_network_configuration() {
  [[ -r /dev/tty && -w /dev/tty ]] ||
    die "交互式配置需要真实终端。非交互模式请设置 DUJIAO_BIND 与 DUJIAO_PORT。"
  local old_bind old_port bind port input
  read -r old_bind old_port < <(network_env_current)
  printf '\n当前监听：%s:%s\n新监听 IP（回车保持原值，0.0.0.0 表示所有 IPv4 接口）[%s]：' \
    "$old_bind" "$old_port" "$old_bind" >/dev/tty
  IFS= read -r input </dev/tty || die "输入中断，未修改配置。"
  bind=${input:-$old_bind}
  printf '新端口（回车保持原值）[%s]：' "$old_port" >/dev/tty
  IFS= read -r input </dev/tty || die "输入中断，未修改配置。"
  port=${input:-$old_port}
  apply_network_configuration "$bind" "$port"
}

run_configure_network() {
  require_installed
  if [[ -n "${DUJIAO_BIND+x}" || -n "${DUJIAO_PORT+x}" ]]; then
    local old_bind old_port
    read -r old_bind old_port < <(network_env_current)
    apply_network_configuration "${DUJIAO_BIND:-$old_bind}" "${DUJIAO_PORT:-$old_port}"
  else
    prompt_network_configuration
  fi
}

menu_command_for() {
  case "${1:-}" in
    1) printf 'install' ;;
    2) printf 'status' ;;
    3) printf 'logs' ;;
    4) printf 'backup' ;;
    5) printf 'update' ;;
    6) printf 'restart' ;;
    7) printf 'help' ;;
    8) printf 'configure-network' ;;
    0|q|Q) printf 'exit' ;;
    *) return 1 ;;
  esac
}

run_menu() {
  [[ -r /dev/tty && -w /dev/tty ]] || die "交互菜单需要真实终端；自动化请使用 dujiao-fork <命令>。"
  local choice action
  while true; do
    cat >/dev/tty <<'MENU'

========== Dujiao-Next Fork 管理 ==========
  1) 一键安装（自动补齐 Docker 等依赖） / 恢复安装
  2) 查看服务状态
  3) 查看运行日志
  4) 立即冷备份（会短暂停止服务）
  5) 更新源码并构建（先备份）
  6) 重启服务
  7) 命令帮助
  8) 修改监听 IP / 端口（自动重建应用，失败恢复）
  0) 退出
===========================================
MENU
    printf '请输入编号: ' >/dev/tty
    IFS= read -r choice </dev/tty || return 0
    if ! action=$(menu_command_for "$choice"); then
      info "无效的菜单编号：请使用 0-8。"
      continue
    fi
    [[ "$action" != "exit" ]] || return 0
    execute_command "$action"
  done
}

execute_command() {
  case "$1" in
    install) run_install ;;
    update) require_tools; run_update ;;
    status) require_tools; require_installed; compose ps ;;
    logs) require_tools; require_installed; compose logs --no-color --tail=120 app redis ;;
    restart) require_tools; require_installed; compose restart; wait_for_health || die "重启后健康检查未通过。" ;;
    backup) require_tools; run_backup ;;
    configure-network) require_tools; run_configure_network ;;
    help) usage ;;
    *) die "未知操作。";;
  esac
}

usage() {
  cat <<'HELP'
Dujiao-Next fork Docker 管理器
用法：sudo bash fork-deploy.sh [menu|install|update|status|logs|restart|backup|configure-network|help]
      成功安装后可运行：sudo dujiao-fork （无参数打开交互菜单）
      也支持：sudo dujiao-fork <命令> （供自动化脚本调用）
修改 IP/端口：sudo dujiao-fork configure-network （交互式）
自动化配置：sudo env DUJIAO_BIND=127.0.0.1 DUJIAO_PORT=18080 dujiao-fork configure-network
公网监听需在终端输入 PUBLIC 或额外指定 DUJIAO_CONFIRM_PUBLIC=YES（不建议裸露 HTTP）。

首次安装可指定：
  DUJIAO_BIND=127.0.0.1  (默认，仅本机监听；使用 HTTPS 反向代理)
  DUJIAO_PORT=18080      (默认)
  DUJIAO_FORK_DIR=/opt/dujiao-next-fork
安装流程自动检测并补齐 Debian/Ubuntu 的 Docker Engine、Compose V2、Buildx 及其他缺失依赖。
如已部署 Docker，仅补装缺失 CLI 插件，拒绝可能影响现有容器的升级/卸载。
自动安装仅针对 Ubuntu 22.04/24.04/26.04 与 Debian 12/13（amd64/arm64）。
本脚本不会删除数据库、上传文件、Redis 数据，也不会执行官方上游安装器。
HELP
}

main() {
  local command="${1:-menu}"
  case "$command" in
    help|-h|--help) usage; return ;;
    menu|install|update|status|logs|restart|backup|configure-network) ;;
    *) usage; die "未知命令：$command" ;;
  esac
  require_root
  validate_install_dir
  if [[ "$command" == "install" && -e "$INSTALL_DIR" ]]; then
    require_installed
  fi
  if [[ "$command" == "install" || "$command" == "menu" ]]; then
    ensure_flock_for_install
  else
    has_command flock || die "缺少 flock，运行 install 可自动补装 util-linux。"
  fi
  exec 9>/run/dujiao-next-fork-manager.lock
  flock -n 9 || die "已有另一个 fork 管理进程在运行。"
  trap cleanup EXIT
  if [[ "$command" == "menu" ]]; then
    run_menu
  else
    execute_command "$command"
  fi
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
