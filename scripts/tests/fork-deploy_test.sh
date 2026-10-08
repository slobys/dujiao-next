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

menu_expected=(install status logs backup update restart help configure-network configure-domain https-status access exit)
menu_codes=(1 2 3 4 5 6 7 8 9 10 11 0)
for i in "${!menu_codes[@]}"; do
  actual=$(menu_command_for "${menu_codes[$i]}")
  if [[ "$actual" == "${menu_expected[$i]}" ]]; then
    pass "menu choice ${menu_codes[$i]} resolves to ${menu_expected[$i]}"
  else
    fail "menu choice ${menu_codes[$i]} resolves to ${menu_expected[$i]}"
  fi
done
if menu_command_for 12 >/dev/null; then
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
DUJIAO_BIND=0.0.0.0 DUJIAO_PORT=18088 DUJIAO_CONFIRM_PUBLIC=YES DUJIAO_PUBLIC_IP=8.8.8.8 run_configure_network
check "CLI can restore publicly bound port with explicit consent" assert_equal_network "0.0.0.0 18088"
check "CLI keeps Redis secret private" grep -Fxq 'REDIS_PASSWORD=do-not-print-secret' "$network_file"
check "no .env rollback artifacts remain" test -z "$(find "$INSTALL_DIR" -maxdepth 1 -name '.env.network-backup.*' -print -quit)"


# HTTPS add-on tests use mocked Compose and DNS. Nothing on the real host is
# bound, no CA requests are made, and no unrelated Docker service is changed.
check "valid domain passes HTTPS validation" validate_domain shop.naiyous.com
check "subdomain with hyphen passes" validate_domain shop-dev.example.com
reject "rejects wildcard domain" validate_domain '*.example.com'
reject "rejects domain with URL prefix" validate_domain 'https://shop.example.com'
reject "rejects public IP instead of domain" validate_domain '1.2.3.4'
reject "rejects local development domain" validate_domain 'shop.local'
reject "rejects domain with Caddyfile injection" validate_domain $'shop.example.com\nreverse_proxy evil'
reject "rejects leading label hyphen" validate_domain '-shop.example.com'
reject "rejects oversized domain label" validate_domain "$(printf 'a%.0s' {1..65}).example.com"
check "accepts optional ACME email" validate_acme_email alerts@example.com
check "supports empty optional ACME email" validate_acme_email ''
reject "rejects Caddyfile injection through email" validate_acme_email $'a@example.com\nreverse_proxy localhost'
reject "rejects shell-special contact email" validate_acme_email 'user@example.com{foo}'

render_https_compose > "$TEST_DIR/https-rendered.yaml"
check "HTTPS proxy uses official Caddy image" grep -Fq 'image: caddy:2-alpine' "$TEST_DIR/https-rendered.yaml"
check "HTTPS proxy publishes TCP port 80" grep -Fq '"80:80/tcp"' "$TEST_DIR/https-rendered.yaml"
check "HTTPS proxy publishes TCP port 443" grep -Fq '"443:443/tcp"' "$TEST_DIR/https-rendered.yaml"
check "HTTPS certificates persist across restarts" grep -Fq './data/caddy/data:/data' "$TEST_DIR/https-rendered.yaml"
check "HTTPS caddy config persists across restarts" grep -Fq './data/caddy/config:/config' "$TEST_DIR/https-rendered.yaml"
check "HTTPS proxy uses internal app service" bash -c '! grep -Eq "network_mode: host|privileged:" "$1"' _ "$TEST_DIR/https-rendered.yaml"
render_caddyfile shop.example.com alerts@example.com > "$TEST_DIR/rendered-Caddyfile"
check "Caddyfile uses exact domain" grep -Fxq 'shop.example.com {' "$TEST_DIR/rendered-Caddyfile"
check "Caddyfile uses app internal port" grep -Fxq '  reverse_proxy app:8080' "$TEST_DIR/rendered-Caddyfile"
check "Caddyfile includes ACME email" grep -Fxq '  email alerts@example.com' "$TEST_DIR/rendered-Caddyfile"

# Verify the new additive Compose service with the installed CLI when present.
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  cp "$TEST_DIR/https-rendered.yaml" "$INSTALL_DIR/compose.https.yaml"
  printf 'shop.example.com\n' > "$INSTALL_DIR/.https-domain"
  mkdir -p "$INSTALL_DIR/data/caddy/"{data,config}
  cp "$TEST_DIR/rendered-Caddyfile" "$INSTALL_DIR/data/caddy/Caddyfile"
  if docker compose --project-name dujiao-next-fork-https-test \
     --project-directory "$INSTALL_DIR" --env-file "$INSTALL_DIR/.env" \
     -f "$INSTALL_DIR/compose.yaml" -f "$INSTALL_DIR/compose.https.yaml" config --quiet; then
    pass "HTTPS Docker Compose override schema validates"
  else
    fail "HTTPS Docker Compose override schema validates"
  fi
  rm -f "$INSTALL_DIR/compose.https.yaml" "$INSTALL_DIR/.https-domain" "$INSTALL_DIR/data/caddy/Caddyfile"
else
  printf 'ok - HTTPS Docker Compose override # SKIP CLI unavailable\n'
fi

# A real port-collision precheck must reject other Docker containers before
# the later tests replace the function with a harmless deterministic mock.
if (
  docker() { printf '0.0.0.0:443->443/tcp\n'; }
  check_https_port_conflicts
) >/dev/null 2>&1; then
  fail "refuses takeover of existing Docker 443 binding"
else
  pass "refuses takeover of existing Docker 443 binding"
fi

# Further tests must not rely on the runner's real Docker daemon.
docker() { :; }

# HTTPS verification must reject redirects and TLS/connection failures,
# not merely a reachable Caddy container.
if ( curl() { printf '200'; }; verify_https shop.example.com ); then
  pass "HTTPS verifier accepts valid TLS with HTTP 200"
else
  fail "HTTPS verifier accepts valid TLS with HTTP 200"
fi
if ( curl() { printf '302'; }; verify_https shop.example.com ); then
  fail "HTTPS verifier rejects HTTP redirect instead of healthy backend"
else
  pass "HTTPS verifier rejects HTTP redirect instead of healthy backend"
fi
if ( curl() { return 60; }; verify_https shop.example.com ); then
  fail "HTTPS verifier rejects TLS validation error"
else
  pass "HTTPS verifier rejects TLS validation error"
fi

# Already managed: a success must retain the old app's database/upload files
# and must not recreate Redis, while keeping HTTPS metadata and ACME volumes.
network_env_write "$network_file" "127.0.0.1" "18080"
: > "$TEST_DIR/https-actions"
compose() { printf '%s\n' "$*" >> "$TEST_DIR/https-actions"; }
check_domain_dns() { printf 'mocked DNS A record\n'; }
check_https_port_conflicts() { printf 'checked ports 80/443\n' >> "$TEST_DIR/https-actions"; }
wait_for_https() { return 0; }
verify_https() { return 0; }
DUJIAO_DOMAIN=shop.example.com DUJIAO_ACME_EMAIL=alerts@example.com run_configure_https
check "HTTPS config marker created" grep -Fxq 'shop.example.com' "$INSTALL_DIR/.https-domain"
check "HTTPS Compose override is enabled" https_enabled
check "HTTPS Caddyfile is private" test "$(stat -c '%a' "$INSTALL_DIR/data/caddy/Caddyfile")" = "600"
check "HTTPS cert state directory is private" test "$(stat -c '%a' "$INSTALL_DIR/data/caddy/data")" = "700"
check "HTTPS CLI starts only Caddy service" grep -Fxq 'up -d --no-build --no-deps --force-recreate caddy' "$TEST_DIR/https-actions"
check "Caddy config validated before start" grep -Fxq 'run --rm --no-deps --entrypoint caddy caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile' "$TEST_DIR/https-actions"
check "HTTPS requires Compose schema validation" grep -Fxq 'config --quiet' "$TEST_DIR/https-actions"
check "HTTPS set-up doesn't touch Redis service" bash -c '! grep -Eq "(stop redis|force-recreate redis|stop app|force-recreate app)" "$1"' _ "$TEST_DIR/https-actions"
check "HTTPS leaves SQLite unmodified" grep -Fxq 'sqlite content' "$INSTALL_DIR/data/db/dujiao.db"
check "HTTPS leaves Redis secrets unmodified" grep -Fxq 'REDIS_PASSWORD=do-not-print-secret' "$network_file"
check "HTTPS leaves uploads unmodified" grep -Fxq 'upload content' "$INSTALL_DIR/data/uploads/file.txt"
check "success clears HTTPS rollback state" test -z "$(find "$INSTALL_DIR" -maxdepth 1 -name '.https-backup.*' -print -quit)"
check "HTTPS startup does not change app's old port" assert_equal_network "127.0.0.1 18080"
: > "$TEST_DIR/https-actions"
DUJIAO_DOMAIN=shop.example.com run_configure_https
check "reusing same domain doesn't recreate any containers" test ! -s "$TEST_DIR/https-actions"

# New HTTPS domains are included in cold backups and HTTPS proxy is stopped
# along with the writers (certificates and account key should not be copied
# while Caddy is writing its data directory).
BACKUP_DIR="$TEST_DIR/https-backups"
run_backup
https_backup=$(find "$BACKUP_DIR" -maxdepth 1 -name '*.tar.gz' -type f -print -quit)
check "HTTPS backup includes domain marker" bash -c 'tar -tzf "$1" | grep -q "^.https-domain$"' _ "$https_backup"
check "HTTPS backup includes Compose override" bash -c 'tar -tzf "$1" | grep -q "^compose.https.yaml$"' _ "$https_backup"
check "HTTPS backup includes Caddyfile" bash -c 'tar -tzf "$1" | grep -q "^data/caddy/Caddyfile$"' _ "$https_backup"
check "HTTPS backup stops cert writer" grep -Fxq 'stop app redis caddy' "$TEST_DIR/https-actions"
check "HTTPS cold backup resumes services" grep -Fxq 'up -d --no-build' "$TEST_DIR/https-actions"

# Existing domain replacement, invalid config => rollback on EXIT.
: > "$TEST_DIR/https-bad-config-actions"
if (
  DUJIAO_DOMAIN=new.example.com
  DUJIAO_ACME_EMAIL=''
  compose() {
    printf '%s\n' "$*" >> "$TEST_DIR/https-bad-config-actions"
    [[ "$*" != "config --quiet" ]]
  }
  HTTPS_PENDING=0
  HTTPS_BACKUP_DIR=""
  HTTPS_CADDY_STARTED=0
  trap cleanup EXIT
  run_configure_https
) >/dev/null 2>&1; then
  fail "invalid HTTPS Compose config must not be considered success"
else
  pass "invalid HTTPS Compose config must not be considered success"
fi
check "invalid Compose HTTPS restores old domain" grep -Fxq 'shop.example.com' "$INSTALL_DIR/.https-domain"
check "invalid Compose HTTPS restores original Caddyfile" grep -Fxq 'shop.example.com {' "$INSTALL_DIR/data/caddy/Caddyfile"
check "no Caddy restart on preflight failure" bash -c '! grep -q "force-recreate caddy" "$1"' _ "$TEST_DIR/https-bad-config-actions"
check "invalid Compose cleanup removed private backup" test -z "$(find "$INSTALL_DIR" -maxdepth 1 -name '.https-backup.*' -print -quit)"

# New domain container starts, but CA certificate cannot be verified:
# rollback retains the previously working domain and re-creates only Caddy.
: > "$TEST_DIR/https-bad-certificate-actions"
if (
  DUJIAO_DOMAIN=second.example.com
  DUJIAO_ACME_EMAIL=''
  compose() { printf '%s\n' "$*" >> "$TEST_DIR/https-bad-certificate-actions"; }
  wait_for_https() { return 1; }
  HTTPS_PENDING=0
  HTTPS_BACKUP_DIR=""
  HTTPS_CADDY_STARTED=0
  trap cleanup EXIT
  run_configure_https
) >/dev/null 2>&1; then
  fail "unverified HTTPS certificate must not be reported as success"
else
  pass "unverified HTTPS certificate must not be reported as success"
fi
check "failed certificate restores original domain" grep -Fxq 'shop.example.com' "$INSTALL_DIR/.https-domain"
check "failed certificate restores original Caddyfile" grep -Fxq 'shop.example.com {' "$INSTALL_DIR/data/caddy/Caddyfile"
check "failed certificate restores Caddy container" test "$(grep -Fc 'up -d --no-build --no-deps --force-recreate caddy' "$TEST_DIR/https-bad-certificate-actions")" = "2"
check "failed certificate does not restart app or Redis" bash -c '! grep -Eq "(force-recreate app|stop redis)" "$1"' _ "$TEST_DIR/https-bad-certificate-actions"

# Initial deployment failure removes just the new HTTPS configuration, without
# deleting Caddy's persistent /data or touching the existing commerce stack.
mkdir -p "$TEST_DIR/fresh-https/src/.git" "$TEST_DIR/fresh-https/data/caddy/"
cp "$INSTALL_DIR/compose.yaml" "$TEST_DIR/fresh-https/compose.yaml"
cp "$network_file" "$TEST_DIR/fresh-https/.env"
: > "$TEST_DIR/fresh-https/$MANAGED_MARKER"
mkdir -p "$TEST_DIR/fresh-https/data/caddy/data"
printf 'acme-account-state\n' > "$TEST_DIR/fresh-https/data/caddy/data/account.txt"
if (
  INSTALL_DIR="$TEST_DIR/fresh-https"
  DUJIAO_DOMAIN=first.example.com
  DUJIAO_ACME_EMAIL=''
  compose() { printf '%s\n' "$*" >> "$TEST_DIR/https-first-fail-actions"; }
  check_domain_dns() { return 0; }
  check_https_port_conflicts() { return 0; }
  wait_for_https() { return 1; }
  HTTPS_PENDING=0
  HTTPS_BACKUP_DIR=""
  HTTPS_CADDY_STARTED=0
  HTTPS_HAD_CONFIG=0
  trap cleanup EXIT
  run_configure_https
) >/dev/null 2>&1; then
  fail "initial HTTPS setup refuses failed certificate verification"
else
  pass "initial HTTPS setup refuses failed certificate verification"
fi
check "initial HTTPS failure removes domain marker" test ! -e "$TEST_DIR/fresh-https/.https-domain"
check "initial HTTPS failure removes addon Compose file" test ! -e "$TEST_DIR/fresh-https/compose.https.yaml"
check "initial HTTPS failure removes invalid Caddyfile" test ! -e "$TEST_DIR/fresh-https/data/caddy/Caddyfile"
check "initial HTTPS failure preserves ACME state data" grep -Fxq 'acme-account-state' "$TEST_DIR/fresh-https/data/caddy/data/account.txt"
check "initial HTTPS failure stops/removes only Caddy" grep -Fxq 'rm -s -f caddy' "$TEST_DIR/https-first-fail-actions"



# A successful first HTTPS activation must also close the formerly public
# plaintext app binding, without touching Redis or changing the app image.
if (
  INSTALL_DIR="$TEST_DIR/fresh-https"
  DUJIAO_DOMAIN=shop.example.com
  DUJIAO_ACME_EMAIL=''
  network_env_write "$INSTALL_DIR/.env" 0.0.0.0 18080
  compose() { printf '%s\n' "$*" >> "$TEST_DIR/https-isolation-actions"; }
  verify_https() { return 0; }
  wait_for_https() { return 0; }
  wait_for_health() { return 0; }
  HTTPS_PENDING=0
  HTTPS_CADDY_STARTED=0
  HTTPS_HAD_CONFIG=0
  run_configure_https
  [[ "$(network_env_current)" == '127.0.0.1 18080' ]]
); then
  pass "HTTPS success automatically isolates previous public HTTP port"
else
  fail "HTTPS success automatically isolates previous public HTTP port"
fi
check "HTTPS auto-isolation recreates app without rebuilding" grep -Fxq 'up -d --no-build --no-deps --force-recreate app' "$TEST_DIR/https-isolation-actions"
if grep -Eq '(^stop redis$|--force-recreate redis|--build app)' "$TEST_DIR/https-isolation-actions"; then
  fail "HTTPS auto-isolation must not restart Redis/build app"
else
  pass "HTTPS auto-isolation must not restart Redis/build app"
fi

# Existing but degraded HTTPS should be repairable by rerunning the same
# configure-domain command after DNS/firewall has been fixed.
: > "$TEST_DIR/https-retry-actions"
if (
  INSTALL_DIR="$TEST_DIR/fresh-https"
  DUJIAO_DOMAIN=shop.example.com
  DUJIAO_ACME_EMAIL=''
  compose() { printf '%s\n' "$*" >> "$TEST_DIR/https-retry-actions"; }
  verify_https() { return 1; }
  wait_for_https() { return 0; }
  HTTPS_PENDING=0
  HTTPS_HAD_CONFIG=0
  HTTPS_CADDY_STARTED=0
  run_configure_https
); then
  pass "degraded HTTPS can retry same-domain provisioning"
else
  fail "degraded HTTPS can retry same-domain provisioning"
fi
check "same-domain retry restarts proxy only" grep -Fxq 'up -d --no-build --no-deps --force-recreate caddy' "$TEST_DIR/https-retry-actions"


# Domain DNS errors should fail before any Compose or file mutation.
: > "$TEST_DIR/dns-failed-actions"
if (
  DUJIAO_DOMAIN=unresolved.example.com
  check_domain_dns() { return 1; }
  compose() { printf '%s\n' "$*" >> "$TEST_DIR/dns-failed-actions"; }
  run_configure_https
) >/dev/null 2>&1; then
  fail "unresolved domain must fail without changing HTTPS"
else
  pass "unresolved domain must fail without changing HTTPS"
fi
check "DNS failure avoids Compose modifications" test ! -s "$TEST_DIR/dns-failed-actions"
check "DNS failure retains previously enabled domain" grep -Fxq shop.example.com "$INSTALL_DIR/.https-domain"

# Avoid directory-level symlink traversal for the Caddy certificate store.
mkdir -p "$TEST_DIR/https-link-install/data"
ln -s "$INSTALL_DIR/data/caddy" "$TEST_DIR/https-link-install/data/caddy"
if (
  INSTALL_DIR="$TEST_DIR/https-link-install"
  https_files_safe
) >/dev/null 2>&1; then
  fail "symlinked Caddy storage path is rejected"
else
  pass "symlinked Caddy storage path is rejected"
fi

# Installation UX and public-IP link output regression tests. No external
# HTTP, Docker, or public infrastructure is contacted by these test cases.
if [[ "$(install_mode_bind 1)" == "0.0.0.0" ]]; then
  pass "first install mode 1 binds all IPv4 interfaces"
else
  fail "first install mode 1 binds all IPv4 interfaces"
fi
if [[ "$(install_mode_bind 2)" == "127.0.0.1" ]]; then
  pass "first install mode 2 binds loopback only"
else
  fail "first install mode 2 binds loopback only"
fi
reject "first install rejects unlisted choice" install_mode_bind 3
if [[ "$(DUJIAO_BIND=0.0.0.0 choose_install_bind)" == "0.0.0.0" ]]; then
  pass "explicit public bind skips interactive prompt"
else
  fail "explicit public bind skips interactive prompt"
fi
if [[ "$(DUJIAO_BIND=127.0.0.1 choose_install_bind)" == "127.0.0.1" ]]; then
  pass "explicit private bind skips interactive prompt"
else
  fail "explicit private bind skips interactive prompt"
fi
check "public IPv4 validator accepts global IP" validate_global_ipv4 8.8.8.8
reject "public IPv4 rejects loopback" validate_global_ipv4 127.0.0.1
reject "public IPv4 rejects private address" validate_global_ipv4 192.168.2.1
reject "public IPv4 rejects documentation block" validate_global_ipv4 203.0.113.10
reject "public IPv4 rejects all-interfaces bind" validate_global_ipv4 0.0.0.0
reject "public IPv4 rejects malformed address" validate_global_ipv4 '8.8.8.8;evil'
public_echo=$(
  has_command() { [[ "$1" == curl ]]; }
  curl() { printf '8.8.8.8\n'; }
  detect_public_ipv4
)
if [[ "$public_echo" == "8.8.8.8" ]]; then
  pass "consistent HTTPS IP providers yield real public IP"
else
  fail "consistent HTTPS IP providers yield real public IP"
fi
if (
  has_command() { [[ "$1" == curl ]]; }
  curl() {
    if [[ "$*" == *"api.ipify.org"* ]]; then printf '8.8.8.8'; else printf '1.1.1.1'; fi
  }
  detect_public_ipv4
) >/dev/null 2>&1; then
  fail "different IP providers must not be silently trusted"
else
  pass "different IP providers must not be silently trusted"
fi
if (
  has_command() { [[ "$1" == curl ]]; }
  curl() { printf '192.168.1.12'; }
  detect_public_ipv4
) >/dev/null 2>&1; then
  fail "private IPv4 echoed by endpoint is rejected"
else
  pass "private IPv4 echoed by endpoint is rejected"
fi
ip_from_route=$(
  has_command() { [[ "$1" == ip ]]; }
  ip() { printf '1.1.1.1 via 8.8.8.254 dev eth0 src 8.8.8.8\n'; }
  detect_public_ipv4
)
if [[ "$ip_from_route" == "8.8.8.8" ]]; then
  pass "server network route provides public IPv4 fallback"
else
  fail "server network route provides public IPv4 fallback"
fi
if (
  DUJIAO_PUBLIC_IP=192.168.1.1
  detect_public_ipv4
) >/dev/null 2>&1; then
  fail "invalid manual public IPv4 is refused"
else
  pass "invalid manual public IPv4 is refused"
fi
if [[ "$(DUJIAO_PUBLIC_IP=8.8.8.8 detect_public_ipv4)" == "8.8.8.8" ]]; then
  pass "explicit validated public IPv4 override works"
else
  fail "explicit validated public IPv4 override works"
fi

# Ensure both URLs are actually usable strings; 0.0.0.0 must never appear
# as a browser address. Domain is preferred only with a verified TLS cert.
public_links=$(
  https_enabled() { return 1; }
  detect_public_ipv4() { printf '8.8.8.8'; }
  print_access_links 0.0.0.0 18080 /dj-random-admin
)
assert_contains "public IP produces copyable storefront URL" "$public_links" '商城地址：http://8.8.8.8:18080'
assert_contains "public IP produces copyable admin URL" "$public_links" '后台地址：http://8.8.8.8:18080/dj-random-admin'
if [[ "$public_links" == *'http://0.0.0.0'* ]]; then
  fail "must not print wildcard address as browser URL"
else
  pass "must not print wildcard address as browser URL"
fi
tls_links=$(
  https_enabled() { return 0; }
  https_current_domain() { printf 'shop.example.com'; }
  verify_https() { return 0; }
  detect_public_ipv4() { printf '8.8.8.8'; }
  print_access_links 0.0.0.0 18080 /dj-admin
)
assert_contains "verified HTTPS domain takes precedence" "$tls_links" '商城地址：https://shop.example.com'
assert_contains "HTTPS domain prints complete admin path" "$tls_links" '后台地址：https://shop.example.com/dj-admin'
if [[ "$tls_links" == *'http://8.8.8.8'* ]]; then
  fail "verified HTTPS link should not be replaced by insecure IP URL"
else
  pass "verified HTTPS link should not be replaced by insecure IP URL"
fi
failed_tls_links=$(
  https_enabled() { return 0; }
  https_current_domain() { printf 'shop.example.com'; }
  verify_https() { return 1; }
  detect_public_ipv4() { printf '8.8.8.8'; }
  print_access_links 0.0.0.0 18080 /dj-admin
)
if [[ "$failed_tls_links" == *'https://shop.example.com'* ]]; then
  fail "must not present unverified HTTPS domain as working"
else
  pass "must not present unverified HTTPS domain as working"
fi
local_links=$(
  https_enabled() { return 1; }
  detect_public_ipv4() { printf '8.8.8.8'; }
  print_access_links 127.0.0.1 18080 /dj-admin
)
assert_contains "loopback URL clearly warns local-only" "$local_links" '此地址仅能在服务器本机使用'
if [[ "$local_links" == *'http://8.8.8.8'* ]]; then
  fail "private-only listener must not print public IP URL"
else
  pass "private-only listener must not print public IP URL"
fi
if (
  https_enabled() { return 1; }
  detect_public_ipv4() { return 1; }
  print_access_links 0.0.0.0 18080 /dj-admin
) > "$TEST_DIR/no-public-ip-links"; then
  pass "public IPv4 detection failure prints clear fallback guidance"
else
  fail "public IPv4 detection failure prints clear fallback guidance"
fi
if grep -Eq 'http://(0\.0\.0\.0|127\.0\.0\.1):18080' "$TEST_DIR/no-public-ip-links"; then
  fail "public listener without IP must not return a misleading URL"
else
  pass "public listener without IP must not return a misleading URL"
fi
if [[ "$(read_admin_path)" == "/dj-test123" ]]; then
  pass "admin path is derived from saved config, not guessed"
else
  fail "admin path is derived from saved config, not guessed"
fi


# Simulate a real fresh installation from menu choice 1, without network,
# package managers or Docker. Assert that the selected public bind is the one
# persisted to Compose and that the FINAL URL contains the detected public IP.
if (
  INSTALL_DIR="$TEST_DIR/fresh-public-install"
  unset DUJIAO_BIND
  DUJIAO_PORT=18080
  choose_install_bind() { printf '0.0.0.0'; }
  ensure_install_dependencies() { :; }
  check_compose_project_conflicts() { :; }
  git() {
    [[ "$1" == clone ]] || return 1
    local dest="${!#}"
    mkdir -p "$dest/.git"
    cp "$REPO_DIR/config.yml.example" "$dest/config.yml.example"
  }
  compose() { printf '%s\n' "$*" >> "$TEST_DIR/fresh-public-compose-calls"; }
  wait_for_health() { return 0; }
  install_manager_link() { :; }
  detect_public_ipv4() { printf '8.8.8.8'; }
  run_install
) > "$TEST_DIR/fresh-public-install.output" 2>&1; then
  pass "fresh install can finish with public-access mode"
else
  fail "fresh install can finish with public-access mode"
fi
check "fresh public setup persists 0.0.0.0 binding" grep -Fxq 'DUJIAO_BIND=0.0.0.0' "$TEST_DIR/fresh-public-install/.env"
check "fresh public Docker Compose config uses selected bind" grep -Fq '"${DUJIAO_BIND}:${DUJIAO_PORT}:8080"' "$TEST_DIR/fresh-public-install/compose.yaml"
check "fresh public install prints copy-ready public storefront URL" grep -Fxq '商城地址：http://8.8.8.8:18080' "$TEST_DIR/fresh-public-install.output"
check "fresh public install prints copy-ready random admin URL" grep -Eq '^后台地址：http://8\.8\.8\.8:18080/dj-[a-f0-9]{16}$' "$TEST_DIR/fresh-public-install.output"
if grep -Eq '(访问地址|后台地址|商城地址)：http://0\.0\.0\.0' "$TEST_DIR/fresh-public-install.output"; then
  fail "fresh install must never show 0.0.0.0 as browser address"
else
  pass "fresh install must never show 0.0.0.0 as browser address"
fi
check "fresh setup preserves SQLite volume" grep -Fq './data/db:/app/db' "$TEST_DIR/fresh-public-install/compose.yaml"
check "fresh setup persists admin secret safely" test "$(stat -c %a "$TEST_DIR/fresh-public-install/data/config.yml")" = "600"

# Simulate menu choice 2; no extra HTTP probe and never output a public URL.
if (
  INSTALL_DIR="$TEST_DIR/fresh-private-install"
  unset DUJIAO_BIND
  DUJIAO_PORT=18080
  choose_install_bind() { printf '127.0.0.1'; }
  ensure_install_dependencies() { :; }
  check_compose_project_conflicts() { :; }
  git() {
    [[ "$1" == clone ]] || return 1
    local dest="${!#}"
    mkdir -p "$dest/.git"
    cp "$REPO_DIR/config.yml.example" "$dest/config.yml.example"
  }
  compose() { :; }
  wait_for_health() { return 0; }
  install_manager_link() { :; }
  detect_public_ipv4() { printf 'UNEXPECTED_PUBLIC_IP'; return 1; }
  run_install
) > "$TEST_DIR/fresh-private-install.output" 2>&1; then
  pass "fresh install can finish with localhost-only mode"
else
  fail "fresh install can finish with localhost-only mode"
fi
check "fresh private setup remains loopback-only" grep -Fxq 'DUJIAO_BIND=127.0.0.1' "$TEST_DIR/fresh-private-install/.env"
check "fresh private links warn browser cannot access from internet" grep -Fq '浏览器从外网无法打开' "$TEST_DIR/fresh-private-install.output"
check "fresh private install prints correct localhost storefront" grep -Fxq '商城地址：http://127.0.0.1:18080' "$TEST_DIR/fresh-private-install.output"

# Resuming an existing managed deployment must retain the chosen original
# binding, avoid regenerating JWT/Redis keys, and reprint correct URLs.
cp "$TEST_DIR/fresh-public-install/.env" "$TEST_DIR/fresh-public-before.env"
if (
  INSTALL_DIR="$TEST_DIR/fresh-public-install"
  DUJIAO_BIND=127.0.0.1 # ignored for existing managed installations
  ensure_install_dependencies() { :; }
  compose() { printf '%s\n' "$*" >> "$TEST_DIR/fresh-resume-calls"; }
  wait_for_health() { return 0; }
  install_manager_link() { :; }
  detect_public_ipv4() { printf '8.8.8.8'; }
  run_install
) > "$TEST_DIR/fresh-resume.output" 2>&1; then
  pass "repeat install reuses existing managed deployment safely"
else
  fail "repeat install reuses existing managed deployment safely"
fi
check "repeat install does not overwrite public bind or Redis password" cmp -s "$TEST_DIR/fresh-public-before.env" "$TEST_DIR/fresh-public-install/.env"
check "repeat install reprints the detected public IP" grep -Fxq '商城地址：http://8.8.8.8:18080' "$TEST_DIR/fresh-resume.output"

if grep -Eqi 'Vultr|AWS|腾讯云|阿里云' "$REPO_DIR/scripts/fork-deploy.sh"; then
  fail "manager runtime messages are cloud-provider neutral"
else
  pass "manager runtime messages are cloud-provider neutral"
fi

printf '%d passed, %d failed\n' "$passed" "$failed"
((failed == 0))
