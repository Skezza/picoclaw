#!/usr/bin/env bash
set -euo pipefail

SRC_DIR="${SRC_DIR:-}"
TARGET_NAME="${TARGET_NAME:-default}"
SERVICE_NAME="${SERVICE_NAME:-picoclaw.service}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:18790/ready}"
HEALTH_FALLBACK_URL="${HEALTH_FALLBACK_URL:-http://127.0.0.1:18790/health}"
INSTALL_ROOT="${INSTALL_ROOT:-$HOME/.local/lib/picoclaw}"
BIN_DIR="${BIN_DIR:-$HOME/.local/bin}"
PICOCLAW_HOME="${PICOCLAW_HOME:-$HOME/.picoclaw}"
RELEASES_ROOT="${RELEASES_ROOT:-$PICOCLAW_HOME/releases}"
GO_BIN="${GO_BIN:-}"

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

mkdir -p "$INSTALL_ROOT" "$BIN_DIR" "$RELEASES_ROOT"
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

echo "Building PicoClaw release into $tmp_dir"
"$GO_BIN" -C "$SRC_DIR" generate ./...
CGO_ENABLED=0 "$GO_BIN" -C "$SRC_DIR" build -v -tags goolm,stdjson -o "$tmp_dir/picoclaw" ./cmd/picoclaw
CGO_ENABLED=0 "$GO_BIN" -C "$SRC_DIR" build -v -tags goolm,stdjson -o "$tmp_dir/picoclaw-launcher" ./web/backend
if [[ -d "$SRC_DIR/cmd/picoclaw-mcp-fs" ]]; then
  CGO_ENABLED=0 "$GO_BIN" -C "$SRC_DIR" build -v -tags goolm,stdjson -o "$tmp_dir/picoclaw-mcp-fs" ./cmd/picoclaw-mcp-fs
fi

mv "$tmp_dir" "$release_dir"

old_picoclaw="$(readlink -f "$BIN_DIR/picoclaw" 2>/dev/null || true)"
old_launcher="$(readlink -f "$BIN_DIR/picoclaw-launcher" 2>/dev/null || true)"
old_mcp_fs="$(readlink -f "$BIN_DIR/picoclaw-mcp-fs" 2>/dev/null || true)"
if [[ -z "$old_mcp_fs" && -f "$BIN_DIR/picoclaw-mcp-fs" ]]; then
  old_mcp_fs="$BIN_DIR/picoclaw-mcp-fs"
fi

rollback_dir="$(mktemp -d)"
rollback() {
  echo "Deployment failed, rolling back." >&2
  if [[ -n "$old_picoclaw" ]]; then
    ln -sfn "$old_picoclaw" "$BIN_DIR/picoclaw"
  fi
  if [[ -n "$old_launcher" ]]; then
    ln -sfn "$old_launcher" "$BIN_DIR/picoclaw-launcher"
  fi
  if [[ -f "$rollback_dir/picoclaw-mcp-fs" ]]; then
    cp "$rollback_dir/picoclaw-mcp-fs" "$BIN_DIR/picoclaw-mcp-fs"
    chmod +x "$BIN_DIR/picoclaw-mcp-fs"
  elif [[ -n "$old_mcp_fs" ]]; then
    ln -sfn "$old_mcp_fs" "$BIN_DIR/picoclaw-mcp-fs"
  fi
  systemctl --user restart "$SERVICE_NAME" || true
}

if [[ -n "$old_mcp_fs" && -f "$old_mcp_fs" ]]; then
  cp "$old_mcp_fs" "$rollback_dir/picoclaw-mcp-fs"
fi

ln -sfn "$release_dir/picoclaw" "$BIN_DIR/picoclaw"
ln -sfn "$release_dir/picoclaw-launcher" "$BIN_DIR/picoclaw-launcher"
if [[ -f "$release_dir/picoclaw-mcp-fs" ]]; then
  ln -sfn "$release_dir/picoclaw-mcp-fs" "$BIN_DIR/picoclaw-mcp-fs"
fi
chmod +x "$release_dir/picoclaw" "$release_dir/picoclaw-launcher"
if [[ -f "$release_dir/picoclaw-mcp-fs" ]]; then
  chmod +x "$release_dir/picoclaw-mcp-fs"
fi

systemctl --user restart "$SERVICE_NAME"
systemctl --user is-active --quiet "$SERVICE_NAME"

check_health() {
  local url="$1"
  curl --silent --show-error --fail --max-time 5 "$url" >/dev/null
}

ok=0
for _ in $(seq 1 20); do
  if check_health "$HEALTH_URL"; then
    ok=1
    break
  fi
  if check_health "$HEALTH_FALLBACK_URL"; then
    ok=1
    break
  fi
  sleep 2
done

if [[ "$ok" -ne 1 ]]; then
  rollback
  exit 1
fi

echo "Deploy succeeded: $release_dir"
