#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_DIR=$(cd "$SCRIPT_DIR/../.." && pwd)
# shellcheck source=../fork-deploy.sh
source "$SCRIPT_DIR/../fork-deploy.sh"

TEST_DIR=$(mktemp -d)
TEST_TMP="$TEST_DIR"  # isolated mock host and action logs
trap 'rm -rf -- "$TEST_DIR"' EXIT
passed=0
failed=0

pass() { printf 'ok - %s\n' "$1"; passed=$((passed+1)); }
fail() { printf 'not ok - %s\n' "$1" >&2; failed=$((failed+1)); }
check() {
  local name=$1
  shift
  if "$@"; then pass "$name"; else fail "$name"; fi
}
reject() {
  local name=$1
  shift
  if "$@"; then fail "$name"; else pass "$name"; fi
}

assert_contains() {
  local name=$1 text=$2 wanted=$3
  if [[ "$text" == *"$wanted"* ]]; then pass "$name"; else fail "$name"; fi
}

check "accepts loopback bind" validate_bind_ip 127.0.0.1
check "accepts public IPv4 bind" validate_bind_ip 0.0.0.0
reject "rejects shell-like bind" validate_bind_ip '0.0.0.0;true'
reject "rejects IPv6 (compose mapping requires different syntax)" validate_bind_ip '::1'
check "accepts port 18080" validate_port 18080
reject "rejects system port" validate_port 80
reject "rejects invalid high port" validate_port 65536
reject "rejects nonsensical port" validate_port '18080:80'
reject "rejects IPv4 address with octet above 255" validate_bind_ip 256.10.10.10
reject "rejects IPv4 address with leading zero" validate_bind_ip 010.10.10.10


# These simulate Docker/APT with Bash functions. They must never install packages,
# change systemd state or reach the network on the test runner.
check "supports Ubuntu 22.04 amd64" platform_supported ubuntu jammy amd64
check "supports Ubuntu 24.04 arm64" platform_supported ubuntu noble arm64
check "supports Ubuntu 26.04 amd64" platform_supported ubuntu resolute amd64
check "supports Debian 12 amd64" platform_supported debian bookworm amd64
check "supports Debian 13 arm64" platform_supported debian trixie arm64
reject "rejects Ubuntu derivatives" platform_supported linuxmint noble amd64
reject "rejects unapproved Ubuntu version" platform_supported ubuntu oracular amd64
reject "rejects unsupported CPU architecture" platform_supported debian trixie riscv64

source_example=$(APT_DISTRO=debian APT_CODENAME=trixie APT_ARCH=arm64 docker_apt_source)
assert_contains "official Docker Debian HTTPS repository" "$source_example" "URIs: https://download.docker.com/linux/debian"
assert_contains "official Docker suite" "$source_example" "Suites: trixie"
assert_contains "Docker repository signed by keyring" "$source_example" "Signed-By: /etc/apt/keyrings/docker.asc"

if (
  dpkg-query() {
    if [[ "$*" == *"docker.io"* ]]; then
      printf 'install ok installed\n'
      return 0
    fi
    return 1
  }
  assert_no_conflicting_engine_packages
) >/dev/null 2>&1; then
  fail "refuses to replace distro Docker package"
else
  pass "refuses to replace distro Docker package"
fi

if (
  apt-get() { printf 'Inst docker-compose-plugin (2.4)\n'; }
  assert_plugin_install_does_not_modify_engine docker-compose-plugin
) >/dev/null 2>&1; then
  pass "allows installing Compose plugin without Docker changes"
else
  fail "allows installing Compose plugin without Docker changes"
fi
if (
  apt-get() { printf 'Remv docker.io\nInst docker-compose-plugin\n'; }
  assert_plugin_install_does_not_modify_engine docker-compose-plugin
) >/dev/null 2>&1; then
  fail "blocks APT removing packages when adding Compose"
else
  pass "blocks APT removing packages when adding Compose"
fi
if (
  apt-get() { printf 'Inst docker-ce (29.0)\nInst docker-compose-plugin\n'; }
  assert_plugin_install_does_not_modify_engine docker-compose-plugin
) >/dev/null 2>&1; then
  fail "blocks existing Docker Engine upgrades"
else
  pass "blocks existing Docker Engine upgrades"
fi

if (
  gpg() { printf 'fpr:::::::::9DC858229FC7DD38854AE2D88D81803C0EBFCD88:\n'; }
  verify_docker_gpg_key /dev/null
); then
  pass "accepts pinned official Docker apt signing fingerprint"
else
  fail "accepts pinned official Docker apt signing fingerprint"
fi
if (
  gpg() { printf 'fpr:::::::::0000000000000000000000000000000000000000:\n'; }
  verify_docker_gpg_key /dev/null
); then
  fail "rejects untrusted Docker apt signing key"
else
  pass "rejects untrusted Docker apt signing key"
fi

# Installed, functioning Docker should trigger zero APT or repository mutations.
if (
  missing_base_packages() { :; }
  has_command() { return 0; }
  docker() { return 0; }
  apt-get() { printf 'unwanted\n' >> "$TEST_TMP/existing-apt"; return 1; }
  setup_official_docker_apt_repo() { printf 'unwanted\n' >> "$TEST_TMP/existing-repo"; return 1; }
  require_tools() { return 0; }
  ensure_install_dependencies
); then
  pass "fully provisioned host skips all APT changes"
else
  fail "fully provisioned host skips all APT changes"
fi
check "existing Docker received no apt-get calls" test ! -e "$TEST_TMP/existing-apt"
check "existing Docker repository remains untouched" test ! -e "$TEST_TMP/existing-repo"

# Existing NAS/Synology Docker installations may lack apt, curl or GPG; these
# packages are needed for provisioning Docker, not for reusing a working Engine.
no_repo_tools=$(
  has_command() {
    case "$1" in curl|gpg|update-ca-certificates) return 1 ;; *) return 0 ;; esac
  }
  missing_base_packages false
)
if [[ -z "$no_repo_tools" ]]; then
  pass "healthy Docker needs no apt repository tooling"
else
  fail "healthy Docker needs no apt repository tooling"
fi
needs_repo_tools=$(
  has_command() {
    case "$1" in curl|gpg|update-ca-certificates) return 1 ;; *) return 0 ;; esac
  }
  missing_base_packages true
)
assert_contains "provisioning installs missing curl" "$needs_repo_tools" "curl"
assert_contains "provisioning installs missing GPG" "$needs_repo_tools" "gnupg"
assert_contains "provisioning installs missing CA certificates" "$needs_repo_tools" "ca-certificates"
if (
  has_command() {
    case "$1" in curl|gpg|update-ca-certificates) return 1 ;; *) return 0 ;; esac
  }
  docker() { return 0; }
  apt-get() { printf "unexpected apt\n" >> "$TEST_TMP/healthy-nas-apt"; return 1; }
  detect_apt_platform() { printf "unexpected platform\n" >> "$TEST_TMP/healthy-nas-apt"; return 1; }
  require_tools() { return 0; }
  ensure_install_dependencies
); then
  pass "healthy non-APT NAS reuses Docker without installing optional tools"
else
  fail "healthy non-APT NAS reuses Docker without installing optional tools"
fi
check "healthy NAS had no APT platform checks" test ! -e "$TEST_TMP/healthy-nas-apt"

# On a fresh host, missing base packages are installed before the Engine. All
# commands below are mocked and logged to a temporary file.
if (
  MOCK_DOCKER_PRESENT=0
  has_command() {
    [[ "$1" != "docker" || "$MOCK_DOCKER_PRESENT" == "1" ]]
  }
  missing_base_packages() { printf 'git\npython3\nopenssl\n'; }
  detect_apt_platform() { APT_DISTRO=debian; APT_CODENAME=bookworm; APT_ARCH=amd64; }
  apt_update_once() { printf 'apt-update\n' >> "$TEST_TMP/fresh-calls"; }
  apt_install() {
    printf 'apt-install %s\n' "$*" >> "$TEST_TMP/fresh-calls"
    if [[ " $* " == *" docker-ce "* ]]; then MOCK_DOCKER_PRESENT=1; fi
  }
  setup_official_docker_apt_repo() { printf 'docker-repository\n' >> "$TEST_TMP/fresh-calls"; }
  assert_no_conflicting_engine_packages() { printf 'conflicts-checked\n' >> "$TEST_TMP/fresh-calls"; }
  docker() { return 0; }
  systemctl() { printf 'systemctl %s\n' "$*" >> "$TEST_TMP/fresh-calls"; }
  require_tools() { return 0; }
  ensure_install_dependencies
); then
  pass "fresh host bootstraps missing packages and Docker Engine"
else
  fail "fresh host bootstraps missing packages and Docker Engine"
fi
check "new host installs CLI prerequisites" grep -Fq 'apt-install git python3 openssl' "$TEST_TMP/fresh-calls"
check "new host checks Docker conflicts first" grep -Fq 'conflicts-checked' "$TEST_TMP/fresh-calls"
check "new host configures official Docker repository" grep -Fq 'docker-repository' "$TEST_TMP/fresh-calls"
check "new host installs Engine, Compose and Buildx" grep -Fq 'apt-install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin' "$TEST_TMP/fresh-calls"
check "new host starts and enables Docker service" grep -Fq 'systemctl enable --now docker' "$TEST_TMP/fresh-calls"

# Docker already exists, but only Buildx/Compose is missing: the installer must
# request exactly those plugins and not any engine package.
if (
  MOCK_PLUGINS_READY=0
  has_command() { return 0; }
  missing_base_packages() { :; }
  docker() {
    if [[ "$1" == "info" ]]; then return 0; fi
    [[ "$MOCK_PLUGINS_READY" == "1" ]]
  }
  detect_apt_platform() { APT_DISTRO=ubuntu; APT_CODENAME=noble; APT_ARCH=amd64; }
  setup_official_docker_apt_repo() { printf 'repo\n' >> "$TEST_TMP/plugin-calls"; }
  apt_update_once() { printf 'apt-update\n' >> "$TEST_TMP/plugin-calls"; }
  assert_plugin_install_does_not_modify_engine() {
    printf 'dry-run %s\n' "$*" >> "$TEST_TMP/plugin-calls"
  }
  apt_install() {
    printf 'install %s\n' "$*" >> "$TEST_TMP/plugin-calls"
    MOCK_PLUGINS_READY=1
  }
  require_tools() { return 0; }
  ensure_install_dependencies
); then
  pass "existing Docker receives missing Compose and Buildx plugins"
else
  fail "existing Docker receives missing Compose and Buildx plugins"
fi
check "missing Compose/Buildx only" grep -Fq 'install docker-compose-plugin docker-buildx-plugin' "$TEST_TMP/plugin-calls"
if grep -Eq '^install (docker-ce|docker.io|containerd)' "$TEST_TMP/plugin-calls"; then
  fail "existing Docker Engine is not replaced"
else
  pass "existing Docker Engine is not replaced"
fi

# This test simulates an installer run with a foreign installation directory.
# It must fail before automatic dependency installation starts.
mkdir -p "$TEST_TMP/foreign-project"
printf 'owned by something else\n' > "$TEST_TMP/foreign-project/foreign"
if (
  INSTALL_DIR="$TEST_TMP/foreign-project"
  validate_bind_ip() { return 0; }
  validate_port() { return 0; }
  ensure_install_dependencies() { printf 'wrong\n' >> "$TEST_TMP/foreign-apt"; }
  run_install
) >/dev/null 2>&1; then
  fail "foreign installation directory fails before package modifications"
else
  pass "foreign installation directory fails before package modifications"
fi
check "no apt on foreign installation conflict" test ! -e "$TEST_TMP/foreign-apt"


# Minimal Ubuntu/Debian images can lack flock. Bootstrap just util-linux
# without requiring Docker and without modifying /etc during the test.
if (
  MOCK_FLOCK_PRESENT=0
  has_command() {
    [[ "$1" != "flock" || "$MOCK_FLOCK_PRESENT" == "1" ]]
  }
  detect_apt_platform() { printf 'distro-checked\n' >> "$TEST_TMP/flock-calls"; }
  apt_update_once() { printf 'apt-update\n' >> "$TEST_TMP/flock-calls"; }
  apt_install() {
    printf 'apt-install %s\n' "$*" >> "$TEST_TMP/flock-calls"
    MOCK_FLOCK_PRESENT=1
  }
  ensure_flock_for_install
); then
  pass "installs only util-linux to bootstrap missing flock"
else
  fail "installs only util-linux to bootstrap missing flock"
fi
check "flock bootstrap used util-linux" grep -Fxq 'apt-install util-linux' "$TEST_TMP/flock-calls"
if (
  has_command() { return 0; }
  apt-get() { printf 'unwanted\n' >> "$TEST_TMP/flock-existing-apt"; return 1; }
  ensure_flock_for_install
); then
  pass "existing flock skips APT bootstrap"
else
  fail "existing flock skips APT bootstrap"
fi
check "existing flock made no APT calls" test ! -e "$TEST_TMP/flock-existing-apt"

menu_expected=(install status logs backup update restart help configure-network exit)
menu_codes=(1 2 3 4 5 6 7 8 0)
for i in "${!menu_codes[@]}"; do
  actual=$(menu_command_for "${menu_codes[$i]}")
  if [[ "$actual" == "${menu_expected[$i]}" ]]; then
    pass "menu choice ${menu_codes[$i]} resolves to ${menu_expected[$i]}"
  else
    fail "menu choice ${menu_codes[$i]} resolves to ${menu_expected[$i]}"
  fi
done
if menu_command_for 9 >/dev/null; then
  fail "menu rejects unknown choices"
else
  pass "menu rejects unknown choices"
fi

password_one=$(random_admin_password)
password_two=$(random_admin_password)
path_one=$(random_admin_path)
path_two=$(random_admin_path)
if [[ "$password_one" =~ ^Dn-[a-f0-9]{32}-Aa9!$ && "$password_one" != "$password_two" ]]; then
  pass "generates unique strong admin bootstrap passwords"
else
  fail "generates unique strong admin bootstrap passwords"
fi
if [[ "$path_one" =~ ^/dj-[a-f0-9]{16}$ && "$path_one" != "$path_two" ]]; then
  pass "generates random valid admin paths"
else
  fail "generates random valid admin paths"
fi

INSTALL_DIR="$TEST_DIR/legitimate"
check "accepts isolated absolute path" validate_install_dir
INSTALL_DIR="/"
if (validate_install_dir) >/dev/null 2>&1; then
  fail "rejects root filesystem install dir"
else
  pass "rejects root filesystem install dir"
fi
INSTALL_DIR="$TEST_DIR/legitimate"

generate_config "$REPO_DIR/config.yml.example" "$TEST_DIR/config.yml" \
  "admin" 'Aa1:"danger#\value' "/dj-test123" \
  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" \
  "cccccccccccccccccccccccccccccccc" "dddddddddddddddddddddddddddddddd"

if python3 - "$TEST_DIR/config.yml" <<'PY'
import json
import pathlib
import re
import sys

text = pathlib.Path(sys.argv[1]).read_text()
section = None
values = {}
for line in text.splitlines():
    match = re.fullmatch(r"([a-z_]+):(?:\s*#.*)?", line)
    if match:
        section = match.group(1)
    match = re.match(r"  ([a-z_]+):\s*(.*)", line)
    if match:
        values[(section, match.group(1))] = match.group(2).split(" # ", 1)[0]
assert json.loads(values["app", "secret_key"]) == "a" * 32
assert json.loads(values["jwt", "secret"]) == "b" * 32
assert json.loads(values["user_jwt", "secret"]) == "c" * 32
assert json.loads(values["bootstrap", "default_admin_password"]) == 'Aa1:"danger#\\value'
assert json.loads(values["web", "admin_path"]) == "/dj-test123"
assert json.loads(values["server", "mode"]) == "release"
assert json.loads(values["redis", "password"]) == "d" * 32
assert json.loads(values["queue", "password"]) == "d" * 32
assert json.loads(values["redis", "host"]) == "redis"
assert json.loads(values["queue", "host"]) == "redis"
assert values["email", "enabled"] == "false"
assert pathlib.Path(sys.argv[1]).stat().st_mode & 0o777 == 0o600
PY
then
  pass "generates secure config without PyYAML and handles special characters"
else
  fail "generates secure config without PyYAML and handles special characters"
fi

sed '/^  default_admin_password:/d' "$REPO_DIR/config.yml.example" > "$TEST_DIR/missing.yml"
if generate_config "$TEST_DIR/missing.yml" "$TEST_DIR/bad.yml" \
    "admin" "Aa12345" "/dj-private" a b c d >/dev/null 2>&1; then
  fail "rejects unexpected config layout"
else
  pass "rejects unexpected config layout"
fi
check "failed config generation leaves no partial target" test ! -e "$TEST_DIR/bad.yml"

render_compose > "$TEST_DIR/compose.yaml"
check "fork builds source type" grep -Fq 'APP_BUILD_TYPE: "source"' "$TEST_DIR/compose.yaml"
check "fork retains application source build context" grep -Fq 'context: ./src' "$TEST_DIR/compose.yaml"
check "persistent database bind" grep -Fq './data/db:/app/db' "$TEST_DIR/compose.yaml"
check "private config bind is read-only" grep -Fq './data/config.yml:/app/config.yml:ro' "$TEST_DIR/compose.yaml"
check "Redis password is injected by Compose" grep -Fq '"${REDIS_PASSWORD}"' "$TEST_DIR/compose.yaml"
if grep -A20 '^  redis:' "$TEST_DIR/compose.yaml" | grep -q '    ports:'; then
  fail "Redis has no published host ports"
else
  pass "Redis has no published host ports"
fi
check "health checks enabled" grep -Fq 'http://127.0.0.1:8080/health' "$TEST_DIR/compose.yaml"

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  mkdir -p "$TEST_DIR/src" "$TEST_DIR/data/"{db,uploads,logs,redis}
  printf 'DUJIAO_BIND=127.0.0.1\nDUJIAO_PORT=18080\nREDIS_PASSWORD=test-secret\n' > "$TEST_DIR/.env"
  if docker compose --project-name dujiao-next-fork-test \
      --project-directory "$TEST_DIR" --env-file "$TEST_DIR/.env" \
      -f "$TEST_DIR/compose.yaml" config --quiet; then
    pass "Docker Compose schema validates"
  else
    fail "Docker Compose schema validates"
  fi
else
  printf 'ok - Docker Compose schema # SKIP Docker Compose V2 unavailable\n'
fi



if ( docker() { printf "unrelated-compose-container\n"; }; check_compose_project_conflicts ) >/dev/null 2>&1; then
  fail "refuses to take over an existing Compose project"
else
  pass "refuses to take over an existing Compose project"
fi
if ( docker() { :; }; check_compose_project_conflicts ) >/dev/null 2>&1; then
  pass "allows a free Compose project name"
else
  fail "allows a free Compose project name"
fi
# Exercise a cold-backup lifecycle against fake Compose without touching containers.
INSTALL_DIR="$TEST_DIR/simulated-install"
BACKUP_DIR="$TEST_DIR/private-backups"
mkdir -p "$INSTALL_DIR/src/.git" "$INSTALL_DIR/data/"{db,uploads,logs,redis}
cp "$TEST_DIR/compose.yaml" "$INSTALL_DIR/compose.yaml"
cp "$TEST_DIR/config.yml" "$INSTALL_DIR/data/config.yml"
printf "REDIS_PASSWORD=secret\n" > "$INSTALL_DIR/.env"
printf "sqlite content" > "$INSTALL_DIR/data/db/dujiao.db"
printf "redis content" > "$INSTALL_DIR/data/redis/appendonly.aof"
printf "upload content" > "$INSTALL_DIR/data/uploads/file.txt"
: > "$INSTALL_DIR/$MANAGED_MARKER"
compose() { printf "%s\n" "$*" >> "$TEST_DIR/compose-calls"; }
run_backup
backup_file=$(find "$BACKUP_DIR" -maxdepth 1 -name "*.tar.gz" -type f -print -quit)
check "backup archive exists" test -n "$backup_file"
check "backup archive validates" tar -tzf "$backup_file"
check "backup includes application config" bash -c 'tar -tzf "$1" | grep -q "^data/config.yml$"' _ "$backup_file"
check "backup includes SQLite" bash -c 'tar -tzf "$1" | grep -q "^data/db/dujiao.db$"' _ "$backup_file"
check "backup includes Redis data" bash -c 'tar -tzf "$1" | grep -q "^data/redis/appendonly.aof$"' _ "$backup_file"
check "backup includes uploads" bash -c 'tar -tzf "$1" | grep -q "^data/uploads/file.txt$"' _ "$backup_file"
check "backup stops app and redis before archiving" grep -Fq "stop app redis" "$TEST_DIR/compose-calls"
check "backup restarts services" grep -Fq "up -d --no-build" "$TEST_DIR/compose-calls"
backup_mode=$(stat -c "%a" "$backup_file")
if [[ "$backup_mode" == "600" && "$(stat -c "%a" "$BACKUP_DIR")" == "700" ]]; then
  pass "backup file and directory permissions are private"
else
  fail "backup file and directory permissions are private"
fi

# Network settings are changed in .env only, without touching Redis secrets,
# user uploads, SQLite, payment configuration or the Redis container.
network_file="$INSTALL_DIR/.env"
printf 'DUJIAO_BIND=127.0.0.1\nDUJIAO_PORT=18080\nREDIS_PASSWORD=do-not-print-secret\nUNRELATED_OPTION=keep-this\n' > "$network_file"
chmod 0600 "$network_file"
assert_equal_network() {
  [[ "$(network_env_current)" == "$1" ]]
}
check "parses existing network without sourcing Redis password" assert_equal_network "127.0.0.1 18080"
network_env_write "$network_file" "0.0.0.0" "18081"
check "atomic write changes listening IP" grep -Fxq 'DUJIAO_BIND=0.0.0.0' "$network_file"
check "atomic write changes listening port" grep -Fxq 'DUJIAO_PORT=18081' "$network_file"
check "atomic write retains Redis secret exactly" grep -Fxq 'REDIS_PASSWORD=do-not-print-secret' "$network_file"
check "atomic write preserves other env settings" grep -Fxq 'UNRELATED_OPTION=keep-this' "$network_file"
check "atomic .env has 0600 permissions" test "$(stat -c '%a' "$network_file")" = "600"
check "updated IP and port can be reread" assert_equal_network "0.0.0.0 18081"
printf 'DUJIAO_BIND=127.0.0.1\nDUJIAO_BIND=0.0.0.0\nDUJIAO_PORT=18080\nREDIS_PASSWORD=keep\n' > "$TEST_DIR/duplicate-network.env"
cp "$TEST_DIR/duplicate-network.env" "$TEST_DIR/duplicate-network.before"
if network_env_write "$TEST_DIR/duplicate-network.env" "0.0.0.0" "19999" >/dev/null 2>&1; then
  fail "rejects duplicate bind variables without changing file"
else
  pass "rejects duplicate bind variables without changing file"
fi
check "duplicate failure retained exact original env" cmp -s "$TEST_DIR/duplicate-network.env" "$TEST_DIR/duplicate-network.before"

printf 'DUJIAO_BIND=127.0.0.1\nDUJIAO_PORT=18080\nREDIS_PASSWORD=do-not-print-secret\nUNRELATED_OPTION=keep-this\n' > "$network_file"
chmod 0600 "$network_file"
: > "$TEST_DIR/network-actions"
compose() { printf '%s\n' "$*" >> "$TEST_DIR/network-actions"; }
wait_for_health() { return 0; }
NETWORK_PENDING=0
NETWORK_BACKUP=""
check "unchanged binding skips app recreation" apply_network_configuration "127.0.0.1" "18080"
check "unchanged binding leaves Compose untouched" test ! -s "$TEST_DIR/network-actions"
if ( apply_network_configuration "0.0.0.0" "99999" ) >/dev/null 2>&1; then
  fail "rejects invalid requested public port before modifying env"
else
  pass "rejects invalid requested public port before modifying env"
fi
check "invalid port leaves original env" assert_equal_network "127.0.0.1 18080"
DUJIAO_CONFIRM_PUBLIC=YES apply_network_configuration "0.0.0.0" "18088"
check "successful change updates bind and port" assert_equal_network "0.0.0.0 18088"
check "only app container is force recreated" grep -Fxq 'up -d --no-build --no-deps --force-recreate app' "$TEST_DIR/network-actions"
check "Compose config is checked before recreation" grep -Fxq 'config --quiet' "$TEST_DIR/network-actions"
if grep -Eq '(stop redis|up -d redis|--force-recreate redis)' "$TEST_DIR/network-actions"; then
  fail "network changes must not restart Redis"
else
  pass "network changes must not restart Redis"
fi
check "success deletes temporary .env backup" test -z "$(find "$INSTALL_DIR" -maxdepth 1 -name '.env.network-backup.*' -print -quit)"
check "success preserves Redis password" grep -Fxq 'REDIS_PASSWORD=do-not-print-secret' "$network_file"
check "success leaves SQLite untouched" grep -Fxq 'sqlite content' "$INSTALL_DIR/data/db/dujiao.db"

# A failed force-recreate must return nonzero AND restore the original .env;
# the cleanup EXIT trap recreates the old app, never manipulating Redis.
: > "$TEST_DIR/network-failure-actions"
if (
  DUJIAO_CONFIRM_PUBLIC=YES
  compose() {
    printf '%s\n' "$*" >> "$TEST_DIR/network-failure-actions"
    if [[ "$*" == "up -d --no-build --no-deps --force-recreate app" ]]; then
      local count
      count=$(grep -Fc 'up -d --no-build --no-deps --force-recreate app' "$TEST_DIR/network-failure-actions")
      ((count > 1))
    fi
  }
  wait_for_health() { return 0; }
  NETWORK_PENDING=0
  NETWORK_BACKUP=""
  trap cleanup EXIT
  apply_network_configuration "0.0.0.0" "18089"
) >/dev/null 2>&1; then
  fail "failed recreate must not claim success"
else
  pass "failed recreate must not claim success"
fi
check "failed recreate restores prior listen IP and port" assert_equal_network "0.0.0.0 18088"
check "failed recreate triggers old app recreation" test "$(grep -Fc 'up -d --no-build --no-deps --force-recreate app' "$TEST_DIR/network-failure-actions")" = "2"
check "rollback protects Redis password" grep -Fxq 'REDIS_PASSWORD=do-not-print-secret' "$network_file"
check "rollback backup removed after successful recovery" test -z "$(find "$INSTALL_DIR" -maxdepth 1 -name '.env.network-backup.*' -print -quit)"

# Fail after Compose recreates app but its health-check fails. The former
# binding and the corresponding healthy app should be brought back.
: > "$TEST_DIR/network-health-actions"
if (
  DUJIAO_CONFIRM_PUBLIC=YES
  HEALTH_CALLS=0
  compose() { printf '%s\n' "$*" >> "$TEST_DIR/network-health-actions"; }
  wait_for_health() {
    HEALTH_CALLS=$((HEALTH_CALLS + 1))
    ((HEALTH_CALLS > 1))
  }
  NETWORK_PENDING=0
  NETWORK_BACKUP=""
  trap cleanup EXIT
  apply_network_configuration "0.0.0.0" "18090"
) >/dev/null 2>&1; then
  fail "failed health-check must not claim success"
else
  pass "failed health-check must not claim success"
fi
check "failed health-check restores original network" assert_equal_network "0.0.0.0 18088"
check "failed health-check reverts app container" test "$(grep -Fc 'up -d --no-build --no-deps --force-recreate app' "$TEST_DIR/network-health-actions")" = "2"
check "both rollback paths leave no stale backups" test -z "$(find "$INSTALL_DIR" -maxdepth 1 -name '.env.network-backup.*' -print -quit)"


# Reject redirecting secrets through symlinked .env files.
ln -s "$network_file" "$TEST_DIR/symlinked-env"
if network_env_write "$TEST_DIR/symlinked-env" "127.0.0.1" "19990" >/dev/null 2>&1; then
  fail "rejects symlinked .env file"
else
  pass "rejects symlinked .env file"
fi
check "symlink rejection leaves active env unchanged" assert_equal_network "0.0.0.0 18088"

# Compose preflight failures must also roll back immediately.
: > "$TEST_DIR/network-config-actions"
if (
  DUJIAO_CONFIRM_PUBLIC=YES
  compose() {
    printf '%s\n' "$*" >> "$TEST_DIR/network-config-actions"
    [[ "$*" != "config --quiet" ]]
  }
  wait_for_health() { return 0; }
  NETWORK_PENDING=0
  NETWORK_BACKUP=""
  trap cleanup EXIT
  apply_network_configuration "0.0.0.0" "18091"
) >/dev/null 2>&1; then
  fail "Compose validation failure must not report success"
else
  pass "Compose validation failure must not report success"
fi
check "Compose validation failure restores .env" assert_equal_network "0.0.0.0 18088"
check "Compose validation failure restores healthy old app" grep -Fxq 'up -d --no-build --no-deps --force-recreate app' "$TEST_DIR/network-config-actions"

# Explicit environment mode remains usable without a tty for automation.
DUJIAO_BIND=127.0.0.1 DUJIAO_PORT=18088 run_configure_network
check "CLI changes public binding to loopback" assert_equal_network "127.0.0.1 18088"
DUJIAO_BIND=0.0.0.0 DUJIAO_PORT=18088 DUJIAO_CONFIRM_PUBLIC=YES run_configure_network
check "CLI can restore publicly bound port with explicit consent" assert_equal_network "0.0.0.0 18088"
check "CLI keeps Redis secret private" grep -Fxq 'REDIS_PASSWORD=do-not-print-secret' "$network_file"
check "no .env rollback artifacts remain" test -z "$(find "$INSTALL_DIR" -maxdepth 1 -name '.env.network-backup.*' -print -quit)"

printf '%d passed, %d failed\n' "$passed" "$failed"
((failed == 0))
