#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_DIR=$(cd "$SCRIPT_DIR/../.." && pwd)
# shellcheck source=../fork-deploy.sh
source "$SCRIPT_DIR/../fork-deploy.sh"

TEST_DIR=$(mktemp -d)
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

check "accepts loopback bind" validate_bind_ip 127.0.0.1
check "accepts public IPv4 bind" validate_bind_ip 0.0.0.0
reject "rejects shell-like bind" validate_bind_ip '0.0.0.0;true'
reject "rejects IPv6 (compose mapping requires different syntax)" validate_bind_ip '::1'
check "accepts port 18080" validate_port 18080
reject "rejects system port" validate_port 80
reject "rejects invalid high port" validate_port 65536
reject "rejects nonsensical port" validate_port '18080:80'

menu_expected=(install status logs backup update restart help exit)
menu_codes=(1 2 3 4 5 6 7 0)
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
printf '%d passed, %d failed\n' "$passed" "$failed"
((failed == 0))
