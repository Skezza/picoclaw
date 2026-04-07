#!/usr/bin/env bash
set -euo pipefail

SRC_DIR="${SRC_DIR:-}"
TARGET_NAME="${TARGET_NAME:-default}"
SERVICE_NAME="${SERVICE_NAME:-picoclaw.service}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:18790/ready}"
HEALTH_FALLBACK_URL="${HEALTH_FALLBACK_URL:-http://127.0.0.1:18790/health}"
ALLOW_HEALTH_FALLBACK="${ALLOW_HEALTH_FALLBACK:-false}"
INSTALL_ROOT="${INSTALL_ROOT:-$HOME/.local/lib/picoclaw}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
PICOCLAW_HOME="${PICOCLAW_HOME:-$HOME/.picoclaw}"
RELEASES_ROOT="${RELEASES_ROOT:-$PICOCLAW_HOME/releases}"
SELF_IMPROVE_RUNTIME_DIR="${SELF_IMPROVE_RUNTIME_DIR:-$PICOCLAW_HOME/runtime/self-improve}"
GO_BIN="${GO_BIN:-}"
RELEASE_KEEP_COUNT="${RELEASE_KEEP_COUNT:-5}"

if [[ -z "$GO_BIN" ]]; then
  if command -v go >/dev/null 2>&1; then
    GO_BIN="$(command -v go)"
  elif [[ -x "$HOME/.local/bin/go" ]]; then
    GO_BIN="$HOME/.local/bin/go"
  fi
fi

if [[ -z "$SRC_DIR" ]]; then
  echo "SRC_DIR is required" >&2
  exit 1
fi
if [[ ! -d "$SRC_DIR" ]]; then
  echo "SRC_DIR does not exist: $SRC_DIR" >&2
  exit 1
fi
if [[ -z "$GO_BIN" || ! -x "$GO_BIN" ]]; then
  echo "go binary not found; set GO_BIN or install go in PATH/~/.local/bin" >&2
  exit 1
fi

mkdir -p "$INSTALL_ROOT" "$BIN_DIR" "$RELEASES_ROOT" "$SELF_IMPROVE_RUNTIME_DIR"
export PATH="$HOME/.local/bin:${PATH:-}"
build_cache_root="${PICOCLAW_HOME}/build-cache"
mkdir -p "$build_cache_root/tmp" "$build_cache_root/go-build" "$build_cache_root/pkg/mod"
export TMPDIR="$build_cache_root/tmp"
export GOCACHE="$build_cache_root/go-build"
export GOMODCACHE="$build_cache_root/pkg/mod"

sha="$(git -C "$SRC_DIR" rev-parse --short=12 HEAD)"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
release_dir="$RELEASES_ROOT/${TARGET_NAME}-${sha}-${stamp}"
tmp_dir="${release_dir}.tmp"

cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

rm -rf "$tmp_dir"
mkdir -p "$tmp_dir"

discover_mcp_binaries() {
  local dir
  for dir in "$SRC_DIR"/cmd/picoclaw-mcp-*; do
    [[ -d "$dir" ]] || continue
    basename "$dir"
  done | sort
}

echo "Building PicoClaw release into $tmp_dir"
"$GO_BIN" -C "$SRC_DIR" generate ./...
CGO_ENABLED=0 "$GO_BIN" -C "$SRC_DIR" build -v -tags goolm,stdjson -o "$tmp_dir/picoclaw" ./cmd/picoclaw
CGO_ENABLED=0 "$GO_BIN" -C "$SRC_DIR" build -v -tags goolm,stdjson -o "$tmp_dir/picoclaw-launcher" ./web/backend
while IFS= read -r mcp_binary; do
  [[ -n "$mcp_binary" ]] || continue
  CGO_ENABLED=0 "$GO_BIN" -C "$SRC_DIR" build -v -tags goolm,stdjson -o "$tmp_dir/$mcp_binary" "./cmd/$mcp_binary"
done < <(discover_mcp_binaries)

mv "$tmp_dir" "$release_dir"

runtime_scripts=(
  "picoclaw_deploy_local.sh"
  "picoclaw_self_improve_poller.sh"
  "picoclaw_self_improve_install_target.sh"
)

binary_names=("picoclaw" "picoclaw-launcher")
while IFS= read -r mcp_binary; do
  [[ -n "$mcp_binary" ]] || continue
  binary_names+=("$mcp_binary")
done < <(find "$release_dir" -maxdepth 1 -type f -name 'picoclaw-mcp-*' -printf '%f\n' | sort)

resolve_install_target() {
  local name="$1"
  local bin_path="$BIN_DIR/$name"
  local resolved=""
  if [[ -L "$bin_path" ]]; then
    resolved="$(readlink -f "$bin_path" 2>/dev/null || true)"
    if [[ -n "$resolved" ]]; then
      echo "$resolved"
      return 0
    fi
  fi
  if [[ -f "$bin_path" ]]; then
    echo "$bin_path"
    return 0
  fi
  if [[ -f "$INSTALL_ROOT/$name" ]]; then
    echo "$INSTALL_ROOT/$name"
    return 0
  fi
  echo "$INSTALL_ROOT/$name"
}

ensure_bin_link() {
  local name="$1"
  local target="$2"
  local bin_path="$BIN_DIR/$name"
  if [[ "$bin_path" == "$target" ]]; then
    return 0
  fi
  ln -sfn "$target" "$bin_path"
}

rollback_dir="$(mktemp -d)"
declare -a install_targets=()

rollback() {
  echo "Deployment failed, rolling back." >&2
  systemctl --user stop "$SERVICE_NAME" || true

  local name target backup_path runtime_path
  for name in "${binary_names[@]}"; do
    target="$(resolve_install_target "$name")"
    backup_path="$rollback_dir/binaries/$name"
    if [[ -f "$backup_path" ]]; then
      mkdir -p "$(dirname "$target")"
      cat "$backup_path" > "$target"
      chmod +x "$target"
      ensure_bin_link "$name" "$target"
    else
      rm -f "$target"
      if [[ -L "$BIN_DIR/$name" ]]; then
        rm -f "$BIN_DIR/$name"
      fi
    fi
  done

  for name in "${runtime_scripts[@]}"; do
    runtime_path="$SELF_IMPROVE_RUNTIME_DIR/$name"
    backup_path="$rollback_dir/runtime/$name"
    if [[ -f "$backup_path" ]]; then
      install -m 755 "$backup_path" "$runtime_path"
    else
      rm -f "$runtime_path"
    fi
  done

  systemctl --user start "$SERVICE_NAME" || true
}

mkdir -p "$rollback_dir/binaries" "$rollback_dir/runtime"
for name in "${binary_names[@]}"; do
  target="$(resolve_install_target "$name")"
  install_targets+=("$target")
  if [[ -f "$target" ]]; then
    cp "$target" "$rollback_dir/binaries/$name"
  fi
done
for name in "${runtime_scripts[@]}"; do
  runtime_path="$SELF_IMPROVE_RUNTIME_DIR/$name"
  if [[ -f "$runtime_path" ]]; then
    cp "$runtime_path" "$rollback_dir/runtime/$name"
  fi
done

chmod +x "$release_dir/picoclaw" "$release_dir/picoclaw-launcher"
for name in "${binary_names[@]}"; do
  if [[ -f "$release_dir/$name" ]]; then
    chmod +x "$release_dir/$name"
  fi
done
for name in "${runtime_scripts[@]}"; do
  if [[ -f "$SRC_DIR/scripts/$name" ]]; then
    install -m 755 "$SRC_DIR/scripts/$name" "$SELF_IMPROVE_RUNTIME_DIR/$name"
  fi
done

systemctl --user stop "$SERVICE_NAME"
for name in "${binary_names[@]}"; do
  target="$(resolve_install_target "$name")"
  mkdir -p "$(dirname "$target")"
  if [[ -f "$release_dir/$name" ]]; then
    cat "$release_dir/$name" > "$target"
    chmod +x "$target"
    ensure_bin_link "$name" "$target"
  fi
done

systemctl --user start "$SERVICE_NAME"
if ! systemctl --user is-active --quiet "$SERVICE_NAME"; then
  rollback
  exit 1
fi

check_health() {
  local url="$1"
  [[ -n "$url" ]] || return 1
  curl --silent --show-error --fail --max-time 5 "$url" >/dev/null
}

health_fallback_allowed=0
case "${ALLOW_HEALTH_FALLBACK,,}" in
  1|true|yes|on)
    health_fallback_allowed=1
    ;;
esac

ok=0
used_fallback=0
for _ in $(seq 1 20); do
  if check_health "$HEALTH_URL"; then
    ok=1
    break
  fi
  if [[ "$health_fallback_allowed" -eq 1 ]] && check_health "$HEALTH_FALLBACK_URL"; then
    ok=1
    used_fallback=1
    break
  fi
  sleep 2
done

if [[ "$ok" -ne 1 ]]; then
  rollback
  exit 1
fi

if [[ "$used_fallback" -eq 1 ]]; then
  echo "Deploy health check passed via fallback endpoint: $HEALTH_FALLBACK_URL" >&2
fi

if [[ "$RELEASE_KEEP_COUNT" =~ ^[0-9]+$ ]] && [[ "$RELEASE_KEEP_COUNT" -gt 0 ]]; then
  mapfile -t old_releases < <(find "$RELEASES_ROOT" -maxdepth 1 -mindepth 1 -type d -name "${TARGET_NAME}-*" -printf '%T@ %p\n' | sort -nr | awk 'NR>'"$RELEASE_KEEP_COUNT"' {print $2}')
  if [[ "${#old_releases[@]}" -gt 0 ]]; then
    rm -rf "${old_releases[@]}"
  fi
fi

echo "Deploy succeeded: $release_dir"
