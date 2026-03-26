#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  scripts/run-local-real.sh up [--auth-json /abs/path/to/auth.json] [--rebuild] [--restart] [--run-dir DIR]
  scripts/run-local-real.sh bootstrap [--auth-json /abs/path/to/auth.json] [--run-dir DIR]
  scripts/run-local-real.sh down [--run-dir DIR]
  scripts/run-local-real.sh status [--run-dir DIR]
  scripts/run-local-real.sh logs [manager|forwarders|tuwunel|both] [--run-dir DIR]
  scripts/run-local-real.sh reset [--run-dir DIR]

Defaults:
  run dir: /tmp/onboarding-local-real
  matrix URL: http://127.0.0.1:8008
  manager URL: http://127.0.0.1:8081
  server name: tuwunel.test
  auth.json: $HOME/.codex/auth.json

Environment overrides:
  ONBOARDING_LOCAL_RUN_DIR
  ONBOARDING_LOCAL_MATRIX_PORT
  ONBOARDING_LOCAL_MANAGER_PORT
  ONBOARDING_LOCAL_SERVER_NAME
  ONBOARDING_LOCAL_REGISTRATION_TOKEN
  ONBOARDING_LOCAL_BOOTSTRAP_ADMIN_USERNAME
  ONBOARDING_LOCAL_BOOTSTRAP_ADMIN_PASSWORD
  ONBOARDING_LOCAL_ONBOARDING_BOT_USERNAME
  ONBOARDING_LOCAL_ONBOARDING_BOT_PASSWORD
  ONBOARDING_LOCAL_ONBOARDING_MODEL
  ONBOARDING_LOCAL_ONBOARDING_REASONING_EFFORT
  ONBOARDING_LOCAL_DEFAULT_AGENT_MODEL
  ONBOARDING_LOCAL_DEFAULT_AGENT_REASONING_EFFORT
  ONBOARDING_LOCAL_AUTH_PROXY_SOURCE_URL
  ONBOARDING_LOCAL_AMBER_MANAGER_IMAGE
  ONBOARDING_LOCAL_CODEX_AUTH_JSON_PATH

Notes:
  - This script uses the real Codex auth-proxy path. By default it reads
    $HOME/.codex/auth.json unless you pass --auth-json or set
    ONBOARDING_LOCAL_CODEX_AUTH_JSON_PATH.
  - amber-manager runs in Docker with the host Docker socket mounted in.
  - The local onboarding repo is mounted into amber-manager so the product
    manifests are loaded directly from this checkout.
  - The default-agent manifest is always pulled from the published scenarios repo.
EOF
}

die() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

info() {
  printf '==> %s\n' "$*"
}

abs_path() {
  local target="$1"
  local dir base
  dir="$(cd "$(dirname "$target")" && pwd)"
  base="$(basename "$target")"
  printf '%s/%s\n' "$dir" "$base"
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

require_runtime_commands() {
  require_command docker
  require_command curl
  require_command rg
}

require_build_commands() {
  require_runtime_commands
  require_command go
}

ensure_directory_layout() {
  mkdir -p "$RUN_DIR" "$TUWUNEL_DIR" "$MANAGER_DATA_DIR" "$MANAGER_SOURCES_DIR"
}

ensure_local_artifacts() {
  require_build_commands

  ensure_pulled_image "$AMBER_MANAGER_IMAGE"
  ensure_pulled_image ghcr.io/rdi-foundation/amber-helper:v0.2
  ensure_pulled_image ghcr.io/rdi-foundation/amber-router:v0.1
  ensure_pulled_image ghcr.io/rdi-foundation/amber-provisioner:v0.1
  ensure_pulled_image ghcr.io/ricelines/matrix-mcp:v0.1
  ensure_pulled_image ghcr.io/ricelines/matrix-a2a-bridge:v0.1
  ensure_pulled_image ghcr.io/ricelines/codex-a2a:v0.1
  ensure_image ghcr.io/ricelines/onboarding:v0.1 "$ONBOARDING_ROOT/Dockerfile" "$ONBOARDING_ROOT"
}

ensure_pulled_image() {
  local tag="$1"
  if docker image inspect "$tag" >/dev/null 2>&1; then
    return
  fi
  info "pulling docker image $tag"
  docker pull "$tag"
}

ensure_image() {
  local tag="$1"
  local dockerfile="$2"
  local context_dir="$3"
  if [[ "$REBUILD" -eq 0 ]] && docker image inspect "$tag" >/dev/null 2>&1; then
    return
  fi
  info "building docker image $tag"
  docker build -t "$tag" -f "$dockerfile" "$context_dir"
}

cleanup_orphaned_amber_networks() {
  local name containers removed
  removed=0
  while IFS= read -r name; do
    [[ "$name" == amber_scn_* ]] || continue
    if ! containers="$(docker network inspect "$name" --format '{{len .Containers}}' 2>/dev/null)"; then
      continue
    fi
    if [[ "$containers" != "0" ]]; then
      continue
    fi
    if ! docker network rm "$name" >/dev/null 2>&1; then
      continue
    fi
    removed=$((removed + 1))
  done < <(docker network ls --format '{{.Name}}')

  if [[ "$removed" -gt 0 ]]; then
    info "removed $removed orphaned amber docker networks"
  fi
}

write_codex_config() {
  cat >"$CODEX_CONFIG_PATH" <<'EOF'
[features]
child_agents_md = true
EOF
}

write_tuwunel_config() {
  cat >"$TUWUNEL_CONFIG_PATH" <<EOF
[global]
server_name = "$SERVER_NAME"
database_path = "/data/database"
address = "0.0.0.0"
port = $MATRIX_PORT
new_user_displayname_suffix = ""
allow_registration = true
registration_token = "$REGISTRATION_TOKEN"
allow_guest_registration = false
allow_room_creation = true
lockdown_public_room_directory = false
allow_unlisted_room_search_by_id = false
allow_public_room_directory_without_auth = false
allow_encryption = true
encryption_enabled_by_default_for_room_type = "all"
allow_federation = false
federate_created_rooms = false
grant_admin_to_first_user = false
create_admin_room = false
allow_legacy_media = true
error_on_unknown_config_opts = true
query_trusted_key_servers_first = false
query_trusted_key_servers_first_on_join = false
trusted_servers = []
auto_join_rooms = ["#welcome:$SERVER_NAME"]

[global.well_known]
client = "http://127.0.0.1:$MATRIX_PORT"
server = "$SERVER_NAME:443"
EOF
}

write_manager_config() {
  cat >"$MANAGER_CONFIG_PATH" <<EOF
{
  "bindable_services": {
    "matrix": {
      "protocol": "http",
      "provider": {
        "kind": "direct_url",
        "url": "http://host.docker.internal:$MATRIX_PORT"
      }
    },
    "amber-manager-api": {
      "protocol": "http",
      "provider": {
        "kind": "direct_url",
        "url": "http://host.docker.internal:$MANAGER_PORT"
      }
    }
  },
  "scenario_source_allowlist": [
    "$PROVISIONER_SOURCE_URL",
    "$ONBOARDING_SOURCE_URL",
    "$DEFAULT_AGENT_SOURCE_URL",
    "$AUTH_PROXY_SOURCE_URL"
  ]
}
EOF
}

prepare_manager_sources() {
  rm -rf "$MANAGER_SOURCES_DIR"
  mkdir -p "$MANAGER_SOURCES_DIR/external"

  PROVISIONER_SOURCE_URL="file:///opt/onboarding/amber/agent-provisioner.json5"
  ONBOARDING_SOURCE_URL="file:///opt/onboarding/amber/onboarding-agent.json5"
  DEFAULT_AGENT_SOURCE_URL='https://raw.githubusercontent.com/ricelines/scenarios/refs/heads/main/amber/user-agent.json5'
  AUTH_PROXY_SOURCE_URL="$(normalize_manager_source_url "$AUTH_PROXY_SOURCE_URL_INPUT" codex-auth-proxy.json5)"
}

normalize_manager_source_url() {
  local input="$1"
  local staged_name="$2"
  case "$input" in
    http://*|https://*)
      printf '%s\n' "$input"
      ;;
    file://*)
      stage_manager_source "${input#file://}" "$staged_name"
      ;;
    *)
      stage_manager_source "$(abs_path "$input")" "$staged_name"
      ;;
  esac
}

stage_manager_source() {
  local source_path="$1"
  local staged_name="$2"
  [[ -f "$source_path" ]] || die "manager source file not found: $source_path"
  cp "$source_path" "$MANAGER_SOURCES_DIR/external/$staged_name"
  printf 'file:///opt/onboarding-sources/external/%s\n' "$staged_name"
}

detect_docker_socket_path() {
  # Docker Desktop on macOS exposes the engine to the host CLI via a per-user
  # socket, but nested containers still need /var/run/docker.sock as the mount
  # source path.
  if [[ "$(uname -s)" == "Darwin" ]]; then
    printf '/var/run/docker.sock\n'
    return
  fi
  if [[ "${DOCKER_HOST:-}" == unix://* ]]; then
    local docker_host_path="${DOCKER_HOST#unix://}"
    if [[ -S "$docker_host_path" ]]; then
      printf '%s\n' "$docker_host_path"
      return
    fi
  fi
  if [[ -S "$HOME/.docker/run/docker.sock" ]]; then
    printf '%s\n' "$HOME/.docker/run/docker.sock"
    return
  fi
  if [[ -S /var/run/docker.sock ]]; then
    printf '/var/run/docker.sock\n'
    return
  fi
  die "failed to detect a reachable Docker socket from DOCKER_HOST, ~/.docker/run/docker.sock, or /var/run/docker.sock"
}

detect_docker_api_version() {
  docker version --format '{{.Server.APIVersion}}'
}

wait_for_http() {
  local url="$1"
  local description="$2"
  local deadline
  deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  die "timed out waiting for $description at $url"
}

wait_for_manager_ready() {
  local deadline
  deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    if ! manager_container_is_running; then
      printf '\n' >&2
      docker logs "$MANAGER_CONTAINER" >&2 || true
      die "amber-manager exited before becoming ready"
    fi
    if curl -fsS "http://127.0.0.1:$MANAGER_PORT/readyz" | rg -q '"ready":true'; then
      return
    fi
    sleep 1
  done
  printf '\n' >&2
  docker logs "$MANAGER_CONTAINER" >&2 || true
  die "timed out waiting for amber-manager readiness"
}

start_tuwunel() {
  if docker ps --format '{{.Names}}' | rg -x "$TUWUNEL_CONTAINER" >/dev/null 2>&1; then
    return
  fi
  if docker ps -a --format '{{.Names}}' | rg -x "$TUWUNEL_CONTAINER" >/dev/null 2>&1; then
    docker rm -f "$TUWUNEL_CONTAINER" >/dev/null
  fi

  write_tuwunel_config
  info "starting tuwunel in docker"
  docker run --rm -d \
    --name "$TUWUNEL_CONTAINER" \
    -e TUWUNEL_CONFIG=/data/tuwunel.toml \
    -v "$TUWUNEL_DIR:/data" \
    -p "127.0.0.1:$MATRIX_PORT:$MATRIX_PORT" \
    ghcr.io/matrix-construct/tuwunel:v1.5.1 >/dev/null

  wait_for_http "http://127.0.0.1:$MATRIX_PORT/_matrix/client/versions" "matrix homeserver"
}

start_manager_forwarders() {
  if forwarders_process_is_running; then
    return
  fi

  info "starting manager forwarders"
  rm -f "$FORWARDERS_LOG_PATH"
  nohup bash -lc "
    cd \"$ONBOARDING_ROOT\"
    exec go run ./cmd/onboarding-manager-forwarders \
      --manager-container \"$MANAGER_CONTAINER\" \
      --forwarder-image \"ghcr.io/ricelines/onboarding:v0.1\" \
      --name-prefix \"$FORWARDER_CONTAINER_PREFIX\"
  " >"$FORWARDERS_LOG_PATH" 2>&1 </dev/null &
  echo "$!" >"$FORWARDERS_PID_PATH"
}

start_manager() {
  if manager_container_is_running; then
    return
  fi
  if manager_container_exists; then
    docker rm -f "$MANAGER_CONTAINER" >/dev/null
  fi

  prepare_manager_sources
  write_manager_config

  DOCKER_SOCKET_PATH="$(detect_docker_socket_path)"
  DOCKER_API_VERSION="$(detect_docker_api_version)"

  info "starting amber-manager in docker"
  local -a args=(
    docker run --rm -d
    --name "$MANAGER_CONTAINER"
    -p "127.0.0.1:$MANAGER_PORT:4100"
    -p "127.0.0.1:$MANAGER_PROXY_PORT_RANGE_START-$MANAGER_PROXY_PORT_RANGE_END:$MANAGER_PROXY_PORT_RANGE_START-$MANAGER_PROXY_PORT_RANGE_END"
    --sysctl "net.ipv4.ip_local_port_range=$MANAGER_PROXY_PORT_RANGE_START $MANAGER_PROXY_PORT_RANGE_END"
    -e RUST_LOG=info
    -e DOCKER_HOST=unix:///var/run/docker.sock
    -e "DOCKER_API_VERSION=$DOCKER_API_VERSION"
    -e "AMBER_DOCKER_SOCK=$DOCKER_SOCKET_PATH"
    -v "$MANAGER_DATA_DIR:/var/lib/amber-manager"
    -v "$MANAGER_CONFIG_PATH:/etc/amber-manager/manager-config.json:ro"
    -v "$DOCKER_SOCKET_PATH:/var/run/docker.sock"
    -v "$ONBOARDING_ROOT:/opt/onboarding:ro"
  )
  if [[ "$(uname -s)" == "Linux" ]]; then
    args+=(--add-host host.docker.internal:host-gateway)
  fi
  if [[ -n "$(find "$MANAGER_SOURCES_DIR" -mindepth 1 -print -quit)" ]]; then
    args+=(-v "$MANAGER_SOURCES_DIR:/opt/onboarding-sources:ro")
  fi
  args+=(
    "$AMBER_MANAGER_IMAGE"
    --listen "0.0.0.0:4100"
    --data-dir /var/lib/amber-manager
    --config /etc/amber-manager/manager-config.json
  )
  "${args[@]}" >/dev/null

  wait_for_manager_ready
  start_manager_forwarders
}

run_bootstrap() {
  [[ -n "$CODEX_AUTH_JSON_PATH" ]] || die "auth.json path is empty"
  [[ -f "$CODEX_AUTH_JSON_PATH" ]] || die "auth.json not found: $CODEX_AUTH_JSON_PATH"

  write_codex_config

  info "bootstrapping onboarding stack"
  (
    cd "$ONBOARDING_ROOT"
    ONBOARDING_BOOTSTRAP_STATE_PATH="$BOOTSTRAP_STATE_PATH" \
    ONBOARDING_BOOTSTRAP_MATRIX_HOMESERVER_URL="http://127.0.0.1:$MATRIX_PORT" \
    ONBOARDING_BOOTSTRAP_MATRIX_SERVER_NAME="$SERVER_NAME" \
    ONBOARDING_BOOTSTRAP_REGISTRATION_TOKEN="$REGISTRATION_TOKEN" \
    ONBOARDING_BOOTSTRAP_MANAGER_URL="http://127.0.0.1:$MANAGER_PORT" \
    ONBOARDING_BOOTSTRAP_ADMIN_USERNAME="$BOOTSTRAP_ADMIN_USERNAME" \
    ONBOARDING_BOOTSTRAP_ADMIN_PASSWORD="$BOOTSTRAP_ADMIN_PASSWORD" \
    ONBOARDING_BOOTSTRAP_ONBOARDING_BOT_USERNAME="$ONBOARDING_BOT_USERNAME" \
    ONBOARDING_BOOTSTRAP_ONBOARDING_BOT_PASSWORD="$ONBOARDING_BOT_PASSWORD" \
    ONBOARDING_BOOTSTRAP_PROVISIONER_SOURCE_URL="$PROVISIONER_SOURCE_URL" \
    ONBOARDING_BOOTSTRAP_ONBOARDING_SOURCE_URL="$ONBOARDING_SOURCE_URL" \
    ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_SOURCE_URL="$DEFAULT_AGENT_SOURCE_URL" \
    ONBOARDING_BOOTSTRAP_AUTH_PROXY_SOURCE_URL="$AUTH_PROXY_SOURCE_URL" \
    ONBOARDING_BOOTSTRAP_CODEX_AUTH_JSON_PATH="$CODEX_AUTH_JSON_PATH" \
    ONBOARDING_BOOTSTRAP_ONBOARDING_MODEL="$ONBOARDING_MODEL" \
    ONBOARDING_BOOTSTRAP_ONBOARDING_MODEL_REASONING_EFFORT="$ONBOARDING_REASONING_EFFORT" \
    ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_MODEL="$DEFAULT_AGENT_MODEL" \
    ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_MODEL_REASONING_EFFORT="$DEFAULT_AGENT_REASONING_EFFORT" \
    ONBOARDING_BOOTSTRAP_ONBOARDING_DEVELOPER_INSTRUCTIONS_PATH="$ONBOARDING_ROOT/prompts/onboarding-developer-instructions.md" \
    ONBOARDING_BOOTSTRAP_ONBOARDING_AGENTS_MD_PATH="$ONBOARDING_ROOT/agents/onboarding-agent.md" \
    ONBOARDING_BOOTSTRAP_ONBOARDING_CONFIG_TOML_PATH="$CODEX_CONFIG_PATH" \
    ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_DEVELOPER_INSTRUCTIONS_PATH="$ONBOARDING_ROOT/prompts/default-user-agent-developer-instructions.md" \
    ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_AGENTS_MD_PATH="$ONBOARDING_ROOT/agents/default-user-agent.md" \
    ONBOARDING_BOOTSTRAP_DEFAULT_AGENT_CONFIG_TOML_PATH="$CODEX_CONFIG_PATH" \
    go run ./cmd/onboarding-bootstrap
  )
}

stop_manager_forwarders() {
  if forwarders_process_is_running; then
    info "stopping manager forwarders"
    local pid
    pid="$(cat "$FORWARDERS_PID_PATH")"
    kill -TERM "$pid" >/dev/null 2>&1 || true
    for _ in $(seq 1 50); do
      if ! kill -0 "$pid" >/dev/null 2>&1; then
        break
      fi
      sleep 0.1
    done
  fi
  rm -f "$FORWARDERS_PID_PATH"

  while IFS= read -r container_name; do
    [[ -n "$container_name" ]] || continue
    docker rm -f "$container_name" >/dev/null 2>&1 || true
  done < <(docker ps -a --format '{{.Names}}' | rg "^$FORWARDER_CONTAINER_PREFIX-")
}

stop_manager() {
  stop_manager_forwarders

  if manager_container_is_running; then
    info "stopping amber-manager"
    docker stop -t 1 "$MANAGER_CONTAINER" >/dev/null || true
    return
  fi
  if manager_container_exists; then
    docker rm -f "$MANAGER_CONTAINER" >/dev/null || true
  fi
}

stop_tuwunel() {
  if docker ps --format '{{.Names}}' | rg -x "$TUWUNEL_CONTAINER" >/dev/null 2>&1; then
    info "stopping tuwunel"
    docker stop -t 1 "$TUWUNEL_CONTAINER" >/dev/null
    return
  fi
  if docker ps -a --format '{{.Names}}' | rg -x "$TUWUNEL_CONTAINER" >/dev/null 2>&1; then
    docker rm -f "$TUWUNEL_CONTAINER" >/dev/null
  fi
}

show_status() {
  printf 'run dir: %s\n' "$RUN_DIR"
  printf 'matrix:  http://127.0.0.1:%s\n' "$MATRIX_PORT"
  printf 'manager: http://127.0.0.1:%s\n' "$MANAGER_PORT"
  if docker ps --format '{{.Names}}' | rg -x "$TUWUNEL_CONTAINER" >/dev/null 2>&1; then
    printf 'tuwunel: running (%s)\n' "$TUWUNEL_CONTAINER"
  else
    printf 'tuwunel: stopped\n'
  fi
  if manager_container_is_running; then
    printf 'amber-manager: running (%s)\n' "$MANAGER_CONTAINER"
  else
    printf 'amber-manager: stopped\n'
  fi
  local forwarder_count
  forwarder_count="$(running_forwarder_count)"
  if [[ "$forwarder_count" -gt 0 ]]; then
    printf 'manager forwarders: running (%s containers)\n' "$forwarder_count"
  elif forwarders_process_is_running; then
    printf 'manager forwarders: starting (pid %s)\n' "$(cat "$FORWARDERS_PID_PATH")"
  else
    printf 'manager forwarders: stopped\n'
  fi
  if [[ -f "$BOOTSTRAP_STATE_PATH" ]]; then
    printf 'bootstrap state: %s\n' "$BOOTSTRAP_STATE_PATH"
  else
    printf 'bootstrap state: not created yet\n'
  fi
}

show_logs() {
  local target="${1:-both}"
  case "$target" in
    manager)
      if manager_container_exists; then
        docker logs "$MANAGER_CONTAINER"
      else
        printf 'amber-manager container is not present\n'
      fi
      ;;
    forwarders)
      if [[ -f "$FORWARDERS_LOG_PATH" ]]; then
        cat "$FORWARDERS_LOG_PATH"
      else
        printf 'manager forwarders log is not present\n'
      fi
      ;;
    tuwunel)
      if docker ps -a --format '{{.Names}}' | rg -x "$TUWUNEL_CONTAINER" >/dev/null 2>&1; then
        docker logs "$TUWUNEL_CONTAINER"
      else
        printf 'tuwunel container is not present\n'
      fi
      ;;
    both)
      printf '=== amber-manager ===\n'
      if manager_container_exists; then
        docker logs "$MANAGER_CONTAINER"
      else
        printf 'amber-manager container is not present\n'
      fi
      printf '\n=== manager forwarders ===\n'
      if [[ -f "$FORWARDERS_LOG_PATH" ]]; then
        cat "$FORWARDERS_LOG_PATH"
      else
        printf 'manager forwarders log is not present\n'
      fi
      printf '\n=== tuwunel ===\n'
      if docker ps -a --format '{{.Names}}' | rg -x "$TUWUNEL_CONTAINER" >/dev/null 2>&1; then
        docker logs "$TUWUNEL_CONTAINER"
      else
        printf 'tuwunel container is not present\n'
      fi
      ;;
    *)
      die "unknown logs target: $target"
      ;;
  esac
}

manager_container_is_running() {
  docker ps --format '{{.Names}}' | rg -x "$MANAGER_CONTAINER" >/dev/null 2>&1
}

manager_container_exists() {
  docker ps -a --format '{{.Names}}' | rg -x "$MANAGER_CONTAINER" >/dev/null 2>&1
}

forwarders_process_is_running() {
  if [[ ! -f "$FORWARDERS_PID_PATH" ]]; then
    return 1
  fi
  local pid
  pid="$(cat "$FORWARDERS_PID_PATH" 2>/dev/null || true)"
  [[ -n "$pid" ]] || return 1
  kill -0 "$pid" >/dev/null 2>&1
}

running_forwarder_count() {
  docker ps --format '{{.Names}}' | rg "^$FORWARDER_CONTAINER_PREFIX-" | wc -l | tr -d ' '
}

restart_if_requested() {
  if [[ "$RESTART" -eq 1 ]]; then
    stop_manager
    stop_tuwunel
  fi
}

ACTION=""
REBUILD=0
RESTART=0
CODEX_AUTH_JSON_PATH="${ONBOARDING_LOCAL_CODEX_AUTH_JSON_PATH:-$HOME/.codex/auth.json}"
RUN_DIR="${ONBOARDING_LOCAL_RUN_DIR:-/tmp/onboarding-local-real}"
MATRIX_PORT="${ONBOARDING_LOCAL_MATRIX_PORT:-8008}"
MANAGER_PORT="${ONBOARDING_LOCAL_MANAGER_PORT:-8081}"
SERVER_NAME="${ONBOARDING_LOCAL_SERVER_NAME:-tuwunel.test}"
REGISTRATION_TOKEN="${ONBOARDING_LOCAL_REGISTRATION_TOKEN:-invite-only-token}"
BOOTSTRAP_ADMIN_USERNAME="${ONBOARDING_LOCAL_BOOTSTRAP_ADMIN_USERNAME:-bootstrap-admin}"
BOOTSTRAP_ADMIN_PASSWORD="${ONBOARDING_LOCAL_BOOTSTRAP_ADMIN_PASSWORD:-bootstrap-admin-pass}"
ONBOARDING_BOT_USERNAME="${ONBOARDING_LOCAL_ONBOARDING_BOT_USERNAME:-onboarding}"
ONBOARDING_BOT_PASSWORD="${ONBOARDING_LOCAL_ONBOARDING_BOT_PASSWORD:-onboarding-pass}"
ONBOARDING_MODEL="${ONBOARDING_LOCAL_ONBOARDING_MODEL:-gpt-5.4-mini}"
ONBOARDING_REASONING_EFFORT="${ONBOARDING_LOCAL_ONBOARDING_REASONING_EFFORT:-medium}"
DEFAULT_AGENT_MODEL="${ONBOARDING_LOCAL_DEFAULT_AGENT_MODEL:-gpt-5.4-mini}"
DEFAULT_AGENT_REASONING_EFFORT="${ONBOARDING_LOCAL_DEFAULT_AGENT_REASONING_EFFORT:-medium}"
AMBER_MANAGER_IMAGE="${ONBOARDING_LOCAL_AMBER_MANAGER_IMAGE:-ghcr.io/rdi-foundation/amber-manager:v0.1}"
MANAGER_PROXY_PORT_RANGE_START=43000
MANAGER_PROXY_PORT_RANGE_END=43999

while (($# > 0)); do
  case "$1" in
    up|down|status|logs|reset|bootstrap)
      if [[ -n "$ACTION" ]]; then
        die "only one action may be provided"
      fi
      ACTION="$1"
      shift
      ;;
    --auth-json)
      shift
      [[ $# -gt 0 ]] || die "--auth-json requires a path"
      CODEX_AUTH_JSON_PATH="$1"
      shift
      ;;
    --run-dir)
      shift
      [[ $# -gt 0 ]] || die "--run-dir requires a path"
      RUN_DIR="$1"
      shift
      ;;
    --rebuild)
      REBUILD=1
      shift
      ;;
    --restart)
      RESTART=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      if [[ "$ACTION" == "logs" && -z "${LOG_TARGET:-}" ]]; then
        LOG_TARGET="$1"
        shift
        continue
      fi
      die "unknown argument: $1"
      ;;
  esac
done

ACTION="${ACTION:-status}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ONBOARDING_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

TUWUNEL_DIR="$RUN_DIR/tuwunel"
TUWUNEL_CONFIG_PATH="$TUWUNEL_DIR/tuwunel.toml"
MANAGER_DATA_DIR="$RUN_DIR/manager-data"
MANAGER_SOURCES_DIR="$RUN_DIR/manager-sources"
MANAGER_CONFIG_PATH="$RUN_DIR/manager-config.json"
BOOTSTRAP_STATE_PATH="$RUN_DIR/bootstrap-state.json"
CODEX_CONFIG_PATH="$RUN_DIR/codex.toml"
FORWARDERS_PID_PATH="$RUN_DIR/manager-forwarders.pid"
FORWARDERS_LOG_PATH="$RUN_DIR/manager-forwarders.log"

RUN_ID="$(printf '%s' "$RUN_DIR" | cksum | awk '{print $1}')"
TUWUNEL_CONTAINER="onboarding-local-real-$RUN_ID"
MANAGER_CONTAINER="onboarding-local-manager-$RUN_ID"
FORWARDER_CONTAINER_PREFIX="onboarding-local-forwarder-$RUN_ID"

AUTH_PROXY_SOURCE_URL_INPUT="${ONBOARDING_LOCAL_AUTH_PROXY_SOURCE_URL:-https://raw.githubusercontent.com/ricelines/codex-a2a/refs/heads/main/amber/codex-auth-proxy.json5}"
PROVISIONER_SOURCE_URL=""
ONBOARDING_SOURCE_URL=""
DEFAULT_AGENT_SOURCE_URL=""
AUTH_PROXY_SOURCE_URL=""
DOCKER_SOCKET_PATH=""
DOCKER_API_VERSION=""

case "$ACTION" in
  up)
    ensure_directory_layout
    restart_if_requested
    ensure_local_artifacts
    cleanup_orphaned_amber_networks
    start_tuwunel
    start_manager
    run_bootstrap
    cat <<EOF

Local Matrix onboarding stack is up.

Run dir:      $RUN_DIR
Matrix URL:   http://127.0.0.1:$MATRIX_PORT
Manager URL:  http://127.0.0.1:$MANAGER_PORT
Server name:  $SERVER_NAME

Registration token: $REGISTRATION_TOKEN
Bootstrap admin:    $BOOTSTRAP_ADMIN_USERNAME / $BOOTSTRAP_ADMIN_PASSWORD
Onboarding bot:     @$ONBOARDING_BOT_USERNAME:$SERVER_NAME / $ONBOARDING_BOT_PASSWORD

Useful commands:
  $(abs_path "$0") status --run-dir "$RUN_DIR"
  $(abs_path "$0") logs manager --run-dir "$RUN_DIR"
  $(abs_path "$0") down --run-dir "$RUN_DIR"
EOF
    ;;
  bootstrap)
    ensure_directory_layout
    ensure_local_artifacts
    cleanup_orphaned_amber_networks
    start_tuwunel
    start_manager
    run_bootstrap
    ;;
  down)
    require_runtime_commands
    stop_manager
    stop_tuwunel
    ;;
  reset)
    require_runtime_commands
    stop_manager
    stop_tuwunel
    info "removing run dir $RUN_DIR"
    rm -rf "$RUN_DIR"
    ;;
  status)
    require_runtime_commands
    show_status
    ;;
  logs)
    require_runtime_commands
    show_logs "${LOG_TARGET:-both}"
    ;;
  *)
    die "unsupported action: $ACTION"
    ;;
esac
