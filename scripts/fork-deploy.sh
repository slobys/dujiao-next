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

https_enabled() {
  [[ -f "$INSTALL_DIR/.https-domain" &&
     -f "$INSTALL_DIR/compose.https.yaml" &&
     -f "$INSTALL_DIR/data/caddy/Caddyfile" ]]
}

compose() {
  local -a files=(-f "$INSTALL_DIR/compose.yaml")
  if [[ -e "$INSTALL_DIR/compose.https.yaml" ]]; then
    https_enabled || die "HTTPS 附加配置不完整，拒绝接管或修改容器。"
    files+=(-f "$INSTALL_DIR/compose.https.yaml")
  fi
  docker compose --project-name "$COMPOSE_PROJECT" \
    --project-directory "$INSTALL_DIR" \
    --env-file "$INSTALL_DIR/.env" "${files[@]}" "$@"
}

cleanup() {
  local original_exit=$?
  trap - EXIT
  if (( HTTPS_PENDING == 1 )); then
    info "HTTPS 启用/变更未完成，尝试恢复此前的代理配置..."
    rollback_https || info "HTTPS 自动恢复未完全成功，备份保留：$HTTPS_BACKUP_DIR"
  fi
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

# First install explicitly chooses whether the port should be reachable from
# outside the server. A noninteractive install still defaults to loopback.
install_mode_bind() {
  case "$1" in
    1) printf '0.0.0.0' ;;
    2) printf '127.0.0.1' ;;
    *) return 1 ;;
  esac
}

choose_install_bind() {
  # Explicit DUJIAO_BIND is an intentional operator override.
  if [[ -n "${DUJIAO_BIND+x}" ]]; then
    validate_bind_ip "$DUJIAO_BIND" || die "DUJIAO_BIND 必须是合法 IPv4 地址。"
    if [[ "$DUJIAO_BIND" != 127.* ]]; then
      info "已指定非回环监听 $DUJIAO_BIND，浏览器可能通过公网 HTTP 访问；请尽快启用 HTTPS 并限制来源。"
    fi
    printf '%s' "$DUJIAO_BIND"
    return 0
  fi

  local answer
  if [[ -r /dev/tty && -w /dev/tty ]]; then
    cat >/dev/tty <<'MENU'

========== 首次安装：选择访问方式 ==========
  1) 公网 IP + 端口访问（默认，可在外部浏览器打开）
     将监听 0.0.0.0，允许公网连接明文 HTTP。
     仅适合初始测试；正式使用请绑定域名、开启 HTTPS。
  2) 仅本机访问（127.0.0.1，更适合已有反向代理）
===========================================
MENU
    printf '请选择 [1]：' >/dev/tty
    IFS= read -r answer </dev/tty || die "没有完成安装访问模式选择。"
    answer=${answer:-1}
    install_mode_bind "$answer" || die "选项无效，仅允许 1 或 2；未开始安装。"
  else
    info "未检测到交互终端，安全默认仅监听 127.0.0.1；如需公网访问请明确设置 DUJIAO_BIND=0.0.0.0。"
    install_mode_bind 2
  fi
}

validate_global_ipv4() {
  python3 - "$1" <<'PY'
import ipaddress
import sys
try:
    address = ipaddress.IPv4Address(sys.argv[1])
except ipaddress.AddressValueError:
    sys.exit(1)
sys.exit(0 if address.is_global else 1)
PY
}

detect_public_ipv4() {
  local candidate url first="" second=""
  if [[ -n "${DUJIAO_PUBLIC_IP:-}" ]]; then
    validate_global_ipv4 "$DUJIAO_PUBLIC_IP" ||
      die "DUJIAO_PUBLIC_IP 不是有效的公网 IPv4，拒绝输出错误的公网链接。"
    printf '%s\n' "$DUJIAO_PUBLIC_IP"
    return 0
  fi
  # Only HTTPS endpoints and no env proxies; do not assume the Docker bind
  # address or the server's private NIC is an internet-reachable IP.
  if has_command curl; then
    for url in https://api.ipify.org https://ipv4.icanhazip.com; do
      candidate=$(curl --noproxy '*' -4fsS --connect-timeout 3 --max-time 6 "$url" 2>/dev/null || true)
      candidate=${candidate//$'\r'/}
      candidate=${candidate//$'\n'/}
      if validate_global_ipv4 "$candidate" 2>/dev/null; then
        if [[ -z "$first" ]]; then
          first="$candidate"
        else
          second="$candidate"
        fi
      fi
    done
  fi
  if [[ -n "$first" ]]; then
    if [[ -n "$second" && "$first" != "$second" ]]; then
      info "两个独立公网 IP 检测服务返回不一致，拒绝猜测。"
      return 1
    fi
    printf '%s\n' "$first"
    return 0
  fi
  # On bare VPSes a global IPv4 may be assigned to the local NIC directly.
  if has_command ip; then
    candidate=$(ip -4 route get 1.1.1.1 2>/dev/null | awk '{
      for (i=1; i<=NF; i++) if ($i == "src") {print $(i+1); exit}
    }' || true)
    if validate_global_ipv4 "$candidate" 2>/dev/null; then
      printf '%s\n' "$candidate"
      return 0
    fi
  fi
  return 1
}

read_admin_path() {
  python3 - "$INSTALL_DIR/data/config.yml" <<'PY'
import json
import pathlib
import re
import sys

lines = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8").splitlines()
in_web = False
found = []
for line in lines:
    if re.match(r"^web:\s*(?:#.*)?$", line):
        in_web = True
        continue
    if re.match(r"^[a-z_]+:\s*(?:#.*)?$", line):
        in_web = False
    if in_web:
        match = re.match(r"^  admin_path:\s*(.+?)\s*$", line)
        if match:
            try:
                found.append(json.loads(match.group(1)))
            except (json.JSONDecodeError, TypeError):
                raise SystemExit("后台路径配置无法解析")
if len(found) != 1 or not isinstance(found[0], str):
    raise SystemExit("后台路径不存在或重复")
path = found[0]
if not re.fullmatch(r"/[a-zA-Z0-9_/-]{1,150}", path) or "//" in path:
    raise SystemExit("后台路径格式异常")
print(path)
PY
}

print_access_links() {
  local bind=$1 port=$2 admin_path=$3 base="" domain="" public_ip=""
  validate_bind_ip "$bind" && validate_port "$port" ||
    die "无法显示链接：当前监听地址或端口格式错误。"
  if https_enabled; then
    domain=$(https_current_domain)
    if verify_https "$domain"; then
      base="https://$domain"
    else
      info "配置了 HTTPS 标识 $domain，但 TLS 信任验证尚未通过，不输出未经验证的 HTTPS 链接。"
    fi
  fi
  if [[ -z "$base" ]]; then
    if [[ "$bind" == "0.0.0.0" ]]; then
      if public_ip=$(detect_public_ipv4); then
        base="http://$public_ip:$port"
      else
        info "暂时无法可靠取得公网 IP；请在云服务商控制台核对公网 IPv4 并访问 http://公网IP:$port"
      fi
    else
      base="http://$bind:$port"
    fi
  fi

  printf '\n========== 商城访问链接 ==========\n'
  if [[ -n "$base" ]]; then
    printf '商城地址：%s\n' "$base"
    if [[ -n "$admin_path" ]]; then
      printf '后台地址：%s%s\n' "$base" "$admin_path"
    else
      printf '后台地址：暂时无法从配置读取，请检查 data/config.yml。\n'
    fi
  else
    printf '商城地址：尚未取得可信公网 IP，请先核对云服务器实例的公网 IPv4。\n'
    printf '后台路径：%s\n' "$admin_path"
  fi
  if [[ "$base" == http://127.* ]]; then
    printf '说明：此地址仅能在服务器本机使用，浏览器从外网无法打开。\n'
  elif [[ "$base" == http://* ]]; then
    printf '提示：当前是明文 HTTP，仅用于临时调试。请使用菜单 9 配置域名及 HTTPS。\n'
    printf '提示：已检测到地址，不代表外网端口已放行；如打不开请检查云服务商安全组、防火墙及端口映射。\n'
  fi
  printf '==================================\n'
}

show_installed_links() {
  require_installed
  local bind port admin_path
  read -r bind port < <(network_env_current)
  if ! admin_path=$(read_admin_path 2>/dev/null); then
    admin_path=""
    info "无法读取后台路径，仅显示商城访问地址；请检查 data/config.yml。"
  fi
  print_access_links "$bind" "$port" "$admin_path"
}

run_install() {
  local bind_ip="${DUJIAO_BIND:-127.0.0.1}"
  local port="${DUJIAO_PORT:-18080}"
  # Refuse foreign installation directories before presenting new-install
  # prompts or changing host packages. Existing installations keep their bind.
  if [[ -e "$INSTALL_DIR" ]]; then
    require_installed
  else
    bind_ip=$(choose_install_bind)
  fi
  validate_bind_ip "$bind_ip" || die "DUJIAO_BIND 必须是合法 IPv4 地址。"
  validate_port "$port" || die "DUJIAO_PORT 必须是 1024-65535 的端口。"
  ensure_install_dependencies
  if [[ -e "$INSTALL_DIR" ]]; then
    require_installed
    info "检测到上次安装目录，仅重新启动现有部署，不重置数据库和密钥。"
    compose up -d --build
    wait_for_health || die "启动失败；请查看日志。"
    install_manager_link
    info "已恢复部署。实际监听地址请查看 $INSTALL_DIR/.env（没有修改已有配置）。"
    show_installed_links
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
  printf '\n安装成功（请妥善保管首次凭据）：\n管理员：admin\n初始密码：%s\n' "$admin_password"
  info "请立即修改初始密码、启用 2FA；公网访问请优先配置 HTTPS。"
  print_access_links "$bind_ip" "$port" "$admin_path"
}

run_backup() {
  require_installed
  mkdir -p "$BACKUP_DIR"
  chmod 0700 "$BACKUP_DIR"
  local ts archive tmp
  ts=$(date -u +%Y%m%dT%H%M%SZ)
  archive="$BACKUP_DIR/dujiao-next-fork-${ts}-$$.tar.gz"
  tmp="${archive}.tmp"
  # Stop writers, including the HTTPS proxy's certificate state if enabled.
  local -a services=(app redis) backup_files=(.env compose.yaml data)
  if https_enabled; then
    services+=(caddy)
    backup_files+=(compose.https.yaml .https-domain)
  fi
  RESTORE_AFTER_BACKUP=1
  compose stop "${services[@]}"
  if ! tar -C "$INSTALL_DIR" -czf "$tmp" "${backup_files[@]}"; then
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
  printf '\n[警告] 将商城绑定到非回环 IP：%s，可能允许其它机器访问管理后台。\n请先限制云服务商安全组和系统防火墙来源，并配置 HTTPS 反向代理。\n确认继续请输入 PUBLIC：' "$bind" >/dev/tty
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
  show_installed_links
}


HTTPS_PENDING=0
HTTPS_HAD_CONFIG=0
HTTPS_CADDY_STARTED=0
HTTPS_BACKUP_DIR=""

validate_domain() {
  python3 - "$1" <<'PY'
import re
import sys
domain = sys.argv[1]
labels = domain.split(".")
valid = (
    4 <= len(domain) <= 253
    and len(labels) >= 2
    and all(re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?", part)
            for part in labels)
    and len(labels[-1]) >= 2
    and labels[-1].isalpha()
    and domain.lower() == domain
    and not domain.endswith((".local", ".localhost", ".internal", ".test", ".invalid"))
)
sys.exit(0 if valid else 1)
PY
}

# Only public routable IPv4 is supported by the current Docker HTTPS
# mapping; private, loopback, documentation, CGNAT and multicast are refused.
validate_public_ipv4() {
  python3 - "$1" <<'PY'
import ipaddress
import sys
try:
    ip = ipaddress.IPv4Address(sys.argv[1])
except ipaddress.AddressValueError:
    raise SystemExit(1)
# Align with the backend's MCP HTTPS IPv4 validator; Python's
# is_global() alone can accept rare special-purpose allocations.
blocked = [
    "0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16",
    "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24",
    "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
    "224.0.0.0/4", "240.0.0.0/4",
]
safe = (ip.is_global and not ip.is_multicast and not ip.is_reserved
        and all(ip not in ipaddress.IPv4Network(cidr) for cidr in blocked))
raise SystemExit(0 if safe else 1)
PY
}

validate_https_subject() {
  validate_domain "$1" || validate_public_ipv4 "$1"
}

https_subject_type() {
  if validate_public_ipv4 "$1"; then
    printf 'ip\n'
  elif validate_domain "$1"; then
    printf 'domain\n'
  else
    return 1
  fi
}

validate_acme_email() {
  [[ -z "$1" ]] && return 0
  python3 - "$1" <<'PY'
import re
import sys
email = sys.argv[1]
sys.exit(0 if len(email) <= 254 and re.fullmatch(
    r"[A-Za-z0-9._%+\-]+@[A-Za-z0-9](?:[A-Za-z0-9.\-]*[A-Za-z0-9])?",
    email) and "\n" not in email else 1)
PY
}

check_domain_dns() {
  python3 - "$1" <<'PY'
import socket
import sys
try:
    addresses = {row[4][0] for row in
                 socket.getaddrinfo(sys.argv[1], 443, family=socket.AF_INET,
                                    type=socket.SOCK_STREAM)}
except (socket.gaierror, OSError):
    addresses = set()
if not addresses:
    sys.exit(1)
print("域名 A 记录解析结果：" + ", ".join(sorted(addresses)))
PY
}

check_https_port_conflicts() {
  # Docker's NAT port allocation can evade ordinary socket listeners.
  local published
  published=$(docker ps --format '{{.Ports}}') ||
    die "无法检查 Docker 端口占用状态，拒绝接管其他服务。"
  if grep -Eq '(^|[[:space:],])[^[:space:],]*:(80|443)->' <<< "$published"; then
    die "Docker 容器已占用 80/443（如 NPM/Nginx）；请使用已有反向代理，本脚本不会接管它。"
  fi
  if ! python3 - <<'PY'
import socket
for port in (80, 443):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        try:
            sock.bind(("0.0.0.0", port))
        except OSError:
            raise SystemExit(1)
PY
  then
    die "80/443 被系统进程占用，请检查 Nginx/Caddy/Apache。"
  fi
}

render_https_compose() {
  local caddy_image="caddy:2-alpine"
  # Public IP certificates need Caddy's recent ACME profile support.
  # Keep the existing domain image choice for backward compatibility.
  if [[ "${1:-domain}" == "ip" ]]; then
    caddy_image="caddy:2.11.7-alpine"
  fi
  cat <<YAML
services:
  caddy:
    image: ${caddy_image}
    restart: unless-stopped
    depends_on:
      app:
        condition: service_healthy
    ports:
      - "80:80/tcp"
      - "443:443/tcp"
    volumes:
      - ./data/caddy/Caddyfile:/etc/caddy/Caddyfile:ro
      - ./data/caddy/data:/data
      - ./data/caddy/config:/config
    security_opt:
      - no-new-privileges:true
YAML
}

render_caddyfile() {
  local domain=$1 email=$2
  validate_https_subject "$domain" || die "HTTPS 标识必须是真实域名或公网 IPv4。"
  validate_acme_email "$email" || die "证书联系邮箱格式无效。"
  if [[ -n "$email" ]]; then
    printf '{\n  email %s\n}\n\n' "$email"
  fi
  if validate_public_ipv4 "$domain"; then
    # Without this explicit issuer Caddy normally uses its LOCAL CA
    # for IP hosts (browser-untrusted). LE IP certificates require
    # ACME profile 'shortlived' with 160h lifetime.
    printf '%s {\n  tls {\n    issuer acme https://acme-v02.api.letsencrypt.org/directory {\n      profile shortlived\n      disable_tlsalpn_challenge\n    }\n  }\n' "$domain"
    printf '  encode zstd gzip\n  reverse_proxy app:8080\n  header X-Content-Type-Options "nosniff"\n}\n'
  else
    printf '%s {\n  encode zstd gzip\n  reverse_proxy app:8080\n  header X-Content-Type-Options "nosniff"\n}\n' "$domain"
  fi
}

https_current_domain() {
  https_enabled || die "尚未配置 HTTPS。"
  local domain
  domain=$(cat "$INSTALL_DIR/.https-domain")
  # Legacy marker filename is retained so existing domain deployments,
  # backups, migration and rollback remain compatible.
  validate_https_subject "$domain" || die "HTTPS 域名/IP 标记无效，拒绝操作。"
  printf '%s\n' "$domain"
}

https_files_safe() {
  local path
  for path in "$INSTALL_DIR/data/caddy" "$INSTALL_DIR/data/caddy/data" \
              "$INSTALL_DIR/data/caddy/config" \
              "$INSTALL_DIR/compose.https.yaml" "$INSTALL_DIR/.https-domain" \
              "$INSTALL_DIR/data/caddy/Caddyfile"; do
    [[ ! -L "$path" ]] || die "HTTPS 配置含符号链接，拒绝修改：$path"
  done
  if [[ -e "$INSTALL_DIR/compose.https.yaml" ||
        -e "$INSTALL_DIR/.https-domain" ||
        -e "$INSTALL_DIR/data/caddy/Caddyfile" ]]; then
    https_enabled || die "发现不完整的 HTTPS 配置，拒绝覆盖既有文件。"
  fi
}

verify_https() {
  local domain=$1 status
  # Connect to local Caddy while verifying public certificate chain and SNI.
  # Never use -k/--insecure. A redirect is not a healthy application.
  status=$(curl --noproxy '*' --silent --show-error --output /dev/null \
    --write-out '%{http_code}' --connect-timeout 3 --max-time 7 \
    --resolve "$domain:443:127.0.0.1" "https://${domain}/health" 2>/dev/null) ||
    return 1
  [[ "$status" == "200" ]]
}

wait_for_https() {
  local domain=$1 i
  for ((i=0; i<40; i++)); do
    verify_https "$domain" && return 0
    sleep 3
  done
  compose logs --tail=35 caddy >&2 || true
  return 1
}

rollback_https() {
  (( HTTPS_PENDING == 1 )) || return 0
  if (( HTTPS_HAD_CONFIG == 1 )); then
    local file
    for file in .https-domain compose.https.yaml; do
      install -m 0600 "$HTTPS_BACKUP_DIR/$file" "$INSTALL_DIR/$file" ||
        return 1
    done
    install -m 0600 "$HTTPS_BACKUP_DIR/Caddyfile" "$INSTALL_DIR/data/caddy/Caddyfile" ||
      return 1
    if (( HTTPS_CADDY_STARTED == 1 )); then
      compose up -d --no-build --no-deps --force-recreate caddy ||
        return 1
    fi
  else
    if (( HTTPS_CADDY_STARTED == 1 )); then
      compose rm -s -f caddy || return 1
    fi
    rm -f -- "$INSTALL_DIR/.https-domain" "$INSTALL_DIR/compose.https.yaml" \
      "$INSTALL_DIR/data/caddy/Caddyfile" || return 1
    # Never remove persistent CA account/certificate state.
  fi
  HTTPS_PENDING=0
  rm -rf -- "$HTTPS_BACKUP_DIR"
  HTTPS_BACKUP_DIR=""
  info "HTTPS 配置已恢复；商城和 Redis 数据没有改变。"
}

run_configure_https() {
  require_installed
  https_files_safe
  local mode=${1:-domain} domain="" email=${DUJIAO_ACME_EMAIL:-}
  case "$mode" in
    domain)
      [[ -z "${DUJIAO_IP:-}" ]] || die "域名模式不能同时设置 DUJIAO_IP。"
      domain=${DUJIAO_DOMAIN:-}
      if [[ -z "$domain" ]]; then
        [[ -r /dev/tty && -w /dev/tty ]] ||
          die "非交互域名模式请设置 DUJIAO_DOMAIN=shop.example.com。"
        printf '请输入商城域名（须已设置 A 记录）：' >/dev/tty
        IFS= read -r domain </dev/tty || die "输入中断。"
        printf '证书联系邮箱（可选，回车跳过）：' >/dev/tty
        IFS= read -r email </dev/tty || die "输入中断。"
      fi
      domain=${domain,,}
      validate_domain "$domain" ||
        die "请输入合法公网域名；不接受 IP、URL、通配符或保留域名。"
      info "正在检查域名 IPv4 A 记录..."
      check_domain_dns "$domain" ||
        die "域名还没有 IPv4 A 记录，请先修改 DNS 再重试。"
      ;;
    ip)
      [[ -z "${DUJIAO_DOMAIN:-}" ]] || die "IP 模式不能同时设置 DUJIAO_DOMAIN。"
      domain=${DUJIAO_IP:-}
      if [[ -z "$domain" ]]; then
        [[ -r /dev/tty && -w /dev/tty ]] ||
          die "非交互 IP 模式请设置 DUJIAO_IP=实际公网IPv4。"
        printf '请输入本云服务器公网 IPv4（不支持私网、NAT 内网 IP 或 IPv6）：' >/dev/tty
        IFS= read -r domain </dev/tty || die "输入中断。"
        printf '证书联系邮箱（可选，回车跳过）：' >/dev/tty
        IFS= read -r email </dev/tty || die "输入中断。"
      fi
      validate_public_ipv4 "$domain" ||
        die "IP 证书只支持真实公网 IPv4；拒绝私网、回环、CGNAT、文档保留 IP 或 IPv6。"
      info "IP 证书使用 Let's Encrypt shortlived（160 小时），由持续运行的 Caddy 自动续期。"
      info "需公网 TCP 80（HTTP-01）及 443 能到达本机 Caddy；无须域名/DNS A 记录。"
      local detected=""
      if detected=$(detect_public_ipv4 2>/dev/null); then
        if [[ "$detected" != "$domain" ]]; then
          info "警告：当前出口公网 IPv4 为 $detected，申请目标为 $domain；如有入站 NAT/映射，请确认 80/443 能到达此机。"
        fi
      else
        info "无法可靠自动识别公网 IPv4，将以 ACME 公网验证及严格 TLS 检查为准。"
      fi
      ;;
    *)
      die "HTTPS 类型只能是 domain 或 ip。"
      ;;
  esac
  validate_acme_email "$email" || die "ACME 联系邮箱无效。"
  has_command curl || die "证书验证需要 curl，请先安装 curl。"
  if https_enabled; then
    local current
    current=$(https_current_domain)
    if [[ "$domain" == "$current" && -z "$email" ]]; then
      if verify_https "$domain"; then
        info "https://$domain 证书与 /health 均验证通过，Caddy 会自动续期。"
        show_installed_links
        return 0
      fi
      info "此前配置的 HTTPS 证书或容器不可用，将保留旧配置备份并重新尝试启动 Caddy。"
    fi
  else
    # A stopped foreign Caddy with our Compose project label must not be
    # silently re-adopted even if ports 80/443 currently appear free.
    local foreign_caddy
    foreign_caddy=$(docker ps -a \
      --filter "label=com.docker.compose.project=$COMPOSE_PROJECT" \
      --filter "label=com.docker.compose.service=caddy" --format '{{.ID}}') ||
      die "无法检查历史 Caddy 容器。"
    [[ -z "$foreign_caddy" ]] ||
      die "已有不属于本脚本配置的 Caddy 容器，拒绝接管。"
    check_https_port_conflicts
  fi

  HTTPS_BACKUP_DIR=$(mktemp -d "$INSTALL_DIR/.https-backup.XXXXXXXX")
  HTTPS_HAD_CONFIG=0
  if https_enabled; then
    HTTPS_HAD_CONFIG=1
    cp "$INSTALL_DIR/.https-domain" "$INSTALL_DIR/compose.https.yaml" "$HTTPS_BACKUP_DIR/"
    cp "$INSTALL_DIR/data/caddy/Caddyfile" "$HTTPS_BACKUP_DIR/Caddyfile"
  fi
  HTTPS_PENDING=1
  HTTPS_CADDY_STARTED=0
  mkdir -p "$INSTALL_DIR/data/caddy/"{data,config}
  chmod 0700 "$INSTALL_DIR/data/caddy" "$INSTALL_DIR/data/caddy/"{data,config}
  render_caddyfile "$domain" "$email" > "$INSTALL_DIR/data/caddy/Caddyfile"
  render_https_compose "$mode" > "$INSTALL_DIR/compose.https.yaml"
  printf '%s\n' "$domain" > "$INSTALL_DIR/.https-domain"
  chmod 0600 "$INSTALL_DIR/data/caddy/Caddyfile" "$INSTALL_DIR/.https-domain" \
    "$INSTALL_DIR/compose.https.yaml"

  compose config --quiet || die "HTTPS Compose 校验失败，正在恢复旧配置。"
  # A short-lived Caddy validation container does not publish any host ports.
  compose run --rm --no-deps --entrypoint caddy caddy validate \
    --config /etc/caddy/Caddyfile --adapter caddyfile ||
    die "Caddyfile 语法验证失败，正在恢复旧配置。"
  HTTPS_CADDY_STARTED=1
  compose up -d --no-build --no-deps --force-recreate caddy ||
    die "Caddy 启动失败，正在恢复旧配置。"
  info "正在等待 Let's Encrypt/公信 CA 证书签发与 HTTPS 实际验证..."
  wait_for_https "$domain" ||
    die "HTTPS 证书未通过公网可信验证，请确认云服务商安全组 TCP 80/443、服务器端口映射与 Caddy 日志；域名模式还需检查 DNS。"

  HTTPS_PENDING=0
  rm -rf -- "$HTTPS_BACKUP_DIR"
  HTTPS_BACKUP_DIR=""
  info "已验证公信 CA HTTPS：https://$domain；Caddy 自动续期和 HTTP 跳转已启用。"
  if [[ "$mode" == "ip" ]]; then
    info "已启用 160 小时 IP 短期证书的 Caddy 内置自动续期（Caddy 须保持运行且验证端口开放）。"
  fi

  local old_bind old_port
  read -r old_bind old_port < <(network_env_current)
  if [[ "$old_bind" != 127.* ]]; then
    info "HTTPS 已可用，尝试收紧原来对公网开放的明文 HTTP 端口..."
    if (apply_network_configuration "127.0.0.1" "$old_port"); then
      info "应用的 HTTP 端口现在仅监听本机。"
    else
      info "警告：未能收紧明文 HTTP 端口 $old_port；请手动通过 configure-network 或防火墙限制访问。"
    fi
  fi
  show_installed_links
}

run_configure_https_menu() {
  if [[ -n "${DUJIAO_DOMAIN:-}" && -n "${DUJIAO_IP:-}" ]]; then
    die "DUJIAO_DOMAIN 与 DUJIAO_IP 不能同时设置。"
  fi
  if [[ -n "${DUJIAO_DOMAIN:-}" ]]; then
    run_configure_https domain
    return
  fi
  if [[ -n "${DUJIAO_IP:-}" ]]; then
    run_configure_https ip
    return
  fi
  [[ -r /dev/tty && -w /dev/tty ]] ||
    die "非交互执行请设置 DUJIAO_DOMAIN=域名 或 DUJIAO_IP=公网IPv4。"
  cat >/dev/tty <<'MENU'
请选择 HTTPS 公网可信证书类型：
  1) 域名证书（Caddy 自动申请及续期）
  2) 公网 IPv4 证书（Let's Encrypt 160 小时短期证书，Caddy 自动续期）
MENU
  local choice
  printf '请选择 [1]：' >/dev/tty
  IFS= read -r choice </dev/tty || die "未能读取 HTTPS 类型。"
  case "${choice:-1}" in
    1) run_configure_https domain ;;
    2) run_configure_https ip ;;
    *) die "只能选择 1 或 2；未变更现有 HTTPS 配置。" ;;
  esac
}

# Inspect the actual certificate served locally, not the files in Caddy's
# ACME cache. curl verify_https above checks system trust and the DNS/IP SAN.
https_show_certificate_expiry() {
  local subject=$1 certificate="" expires=""
  has_command openssl || {
    info "未找到 OpenSSL，无法显示证书到期时间。"
    return 0
  }
  local -a sni=()
  # Domain certificates need proper SNI. IP literals should omit DNS SNI.
  if ! validate_public_ipv4 "$subject"; then
    sni=(-servername "$subject")
  fi
  certificate=$(openssl s_client -connect 127.0.0.1:443 "${sni[@]}" -showcerts </dev/null 2>/dev/null) || {
    info "无法提取当前 TLS 证书到期时间。"
    return 0
  }
  expires=$(openssl x509 -noout -enddate <<< "$certificate" 2>/dev/null) || {
    info "无法解析当前 TLS 证书到期时间。"
    return 0
  }
  printf '证书到期时间（UTC）：%s\n' "${expires#notAfter=}"
  if validate_public_ipv4 "$subject"; then
    printf "IP 证书：Let's Encrypt shortlived（160 小时）；Caddy 持续自动续期。\n"
    if ! openssl x509 -checkend 86400 -noout <<< "$certificate" >/dev/null 2>&1; then
      info "警告：IP 证书将在 24 小时内到期，请立即检查 Caddy 自动续期日志以及公网 TCP 80/443。"
      return 1
    fi
  fi
}

run_https_status() {
  require_installed
  if ! https_enabled; then
    info "尚未配置 HTTPS；运行 sudo dujiao-fork 选择菜单 9（域名或公网 IP）。"
    return 0
  fi
  local domain mode
  domain=$(https_current_domain)
  mode=$(https_subject_type "$domain")
  if [[ "$mode" == "ip" ]]; then
    printf 'HTTPS 公网 IPv4：https://%s\n' "$domain"
  else
    printf 'HTTPS 域名：https://%s\n' "$domain"
  fi
  compose ps caddy
  if verify_https "$domain"; then
    info "公信 CA 证书链、DNS/IP 标识和商城 /health 已通过严格 TLS 验证。"
    https_show_certificate_expiry "$domain" ||
      die "IP 证书续期健康检查失败，请查看 sudo dujiao-fork logs。"
    info "自动续期由 Caddy 管理，证书与 ACME 账户在持久化存储中。"
    show_installed_links
  else
    die "HTTPS 证书或商城健康检查失败，请检查 DNS/IP、TCP 80/443、证书自动续期和 Caddy 日志。"
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
    9) printf 'configure-https' ;;
    10) printf 'https-status' ;;
    11) printf 'access' ;;
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
  9) 申请 HTTPS 证书：选择域名 / 公网 IPv4（自动续期）
 10) 查看 HTTPS 证书与访问状态
 11) 显示可复制的商城和后台访问链接
  0) 退出
===========================================
MENU
    printf '请输入编号: ' >/dev/tty
    IFS= read -r choice </dev/tty || return 0
    if ! action=$(menu_command_for "$choice"); then
      info "无效的菜单编号：请使用 0-11。"
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
    logs) require_tools; require_installed; if https_enabled; then compose logs --no-color --tail=120 app redis caddy; else compose logs --no-color --tail=120 app redis; fi ;;
    restart) require_tools; require_installed; compose restart; wait_for_health || die "重启后健康检查未通过。" ;;
    backup) require_tools; run_backup ;;
    configure-network) require_tools; run_configure_network ;;
    configure-domain) require_tools; run_configure_https domain ;;
    configure-ip) require_tools; run_configure_https ip ;;
    configure-https) require_tools; run_configure_https_menu ;;
    https-status) require_tools; run_https_status ;;
    access) require_tools; show_installed_links ;;
    help) usage ;;
    *) die "未知操作。";;
  esac
}

usage() {
  cat <<'HELP'
Dujiao-Next fork Docker 管理器
用法：sudo bash fork-deploy.sh [menu|install|update|status|logs|restart|backup|configure-network|configure-https|configure-domain|configure-ip|https-status|access|help]
      成功安装后可运行：sudo dujiao-fork （无参数打开交互菜单）
      也支持：sudo dujiao-fork <命令> （供自动化脚本调用）
修改 IP/端口：sudo dujiao-fork configure-network （交互式）
自动化配置：sudo env DUJIAO_BIND=127.0.0.1 DUJIAO_PORT=18080 dujiao-fork configure-network
公网监听需在终端输入 PUBLIC 或额外指定 DUJIAO_CONFIRM_PUBLIC=YES（不建议裸露 HTTP）。
HTTPS 二选一：sudo dujiao-fork （菜单 9 选择域名/IP）
域名 HTTPS：sudo dujiao-fork configure-domain
公网 IPv4 HTTPS：sudo dujiao-fork configure-ip（短证书自动续期）
无人值守域名：sudo env DUJIAO_DOMAIN=shop.example.com DUJIAO_ACME_EMAIL=admin@example.com dujiao-fork configure-domain
无人值守 IP：sudo env DUJIAO_IP=实际公网IPv4 dujiao-fork configure-ip
证书校验：sudo dujiao-fork https-status（Caddy 自动续期，不需要手动续签）
访问链接：sudo dujiao-fork access（优先验证已配置的域名/IP HTTPS，否则检测公网 IPv4）
域名模式需预先设置 DNS A 记录；公网 IPv4 模式不需域名。两者均需 TCP 80/443 可访问。

首次安装可指定：
  DUJIAO_BIND=0.0.0.0    (可选：明确使用公网监听，跳过首次安装选择)
  DUJIAO_PUBLIC_IP=...   (可选：自动检测失败时手动指定真实公网 IPv4)
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
    menu|install|update|status|logs|restart|backup|configure-network|configure-https|configure-domain|configure-ip|https-status|access) ;;
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
