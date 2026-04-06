#!/usr/bin/env bash
set -euo pipefail

PICO_HOME="${PICOCLAW_HOME:-/mnt/1tb/picoclaw-home}"
WORK_ROOT="${WORK_ROOT:-$PICO_HOME/workspace}"
LEGACY_SRC_DIR="${LEGACY_SRC_DIR:-/home/joe/agent/picoclaw-checkout}"
SRC_DIR="${SRC_DIR:-$WORK_ROOT/src/agent/picoclaw-checkout}"
INSTALL_DIR="${INSTALL_DIR:-/home/joe/.local/lib/picoclaw/v0.2.5}"
BIN_DIR="${BIN_DIR:-/home/joe/.local/bin}"
GO_HOME="${GO_HOME:-/home/joe/.local/go}"

BUILD_DIR="$WORK_ROOT/.build"
GOCACHE_DIR="$WORK_ROOT/.cache/go-build"
GOMODCACHE_DIR="$WORK_ROOT/.cache/go-mod"
GOTMP_DIR="$WORK_ROOT/.cache/go-tmp"
BACKUP_DIR="$WORK_ROOT/.backup/binaries"
SMOKE_DIR="$WORK_ROOT/.tmp/deploy-smoke"

if [[ ! -d "$SRC_DIR" && -d "$LEGACY_SRC_DIR" ]]; then
  SRC_DIR="$LEGACY_SRC_DIR"
fi

export PATH="$GO_HOME/bin:$BIN_DIR:/home/joe/.npm-global/bin:$PATH"
export GOCACHE="$GOCACHE_DIR"
export GOMODCACHE="$GOMODCACHE_DIR"
export GOTMPDIR="$GOTMP_DIR"

for cmd in go systemctl; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "missing required command: $cmd" >&2
    exit 1
  fi
done

if [[ ! -d "$SRC_DIR" ]]; then
  echo "source checkout not found: $SRC_DIR" >&2
  exit 1
fi

mkdir -p "$BUILD_DIR" "$GOCACHE_DIR" "$GOMODCACHE_DIR" "$GOTMP_DIR" "$BACKUP_DIR" \
  "$INSTALL_DIR" "$BIN_DIR" "$SMOKE_DIR"

cd "$SRC_DIR"

echo "[1/5] Building picoclaw binaries"
CGO_ENABLED=0 go build -tags goolm,stdjson -o "$BUILD_DIR/picoclaw-custom" ./cmd/picoclaw
if [[ -d ./cmd/picoclaw-mcp-fs ]]; then
  CGO_ENABLED=0 go build -tags goolm,stdjson -o "$BUILD_DIR/picoclaw-mcp-fs-custom" ./cmd/picoclaw-mcp-fs
fi
CGO_ENABLED=0 go build -tags goolm,stdjson -o "$BUILD_DIR/picoclaw-launcher-custom" ./web/backend

ts="$(date +%Y%m%d%H%M%S)"
echo "[2/5] Backing up current binaries ($ts)"
for src in \
  "$INSTALL_DIR/picoclaw" \
  "$INSTALL_DIR/picoclaw-launcher" \
  "$BIN_DIR/picoclaw-mcp-fs"; do
  if [[ -f "$src" ]]; then
    cp -f "$src" "$BACKUP_DIR/$(basename "$src").$ts"
  fi
done

echo "[3/5] Installing new binaries"
cp -f "$BUILD_DIR/picoclaw-custom" "$INSTALL_DIR/picoclaw"
cp -f "$BUILD_DIR/picoclaw-launcher-custom" "$INSTALL_DIR/picoclaw-launcher"
if [[ -f "$BUILD_DIR/picoclaw-mcp-fs-custom" ]]; then
  cp -f "$BUILD_DIR/picoclaw-mcp-fs-custom" "$BIN_DIR/picoclaw-mcp-fs"
  chmod 755 "$BIN_DIR/picoclaw-mcp-fs"
fi
chmod 755 "$INSTALL_DIR/picoclaw" "$INSTALL_DIR/picoclaw-launcher"

echo "[4/5] Restarting user service"
systemctl --user restart picoclaw.service
sleep 2

if [[ "$(systemctl --user is-active picoclaw.service)" != "active" ]]; then
  echo "picoclaw.service failed to become active" >&2
  systemctl --user status picoclaw.service --no-pager -l >&2 || true
  exit 1
fi

echo "[5/5] Smoke checks"
PICOCLAW_HOME="$PICO_HOME" "$BIN_DIR/picoclaw" agent -m '/codex projects' --session "cli:deploy-smoke:$ts" \
  >"$SMOKE_DIR/picoclaw-deploy-smoke.out" 2>"$SMOKE_DIR/picoclaw-deploy-smoke.err" || true
if ! grep -q "Codex sessions:\|No codex sessions yet" "$SMOKE_DIR/picoclaw-deploy-smoke.out"; then
  echo "warning: /codex smoke output was unexpected" >&2
  sed -n '1,80p' "$SMOKE_DIR/picoclaw-deploy-smoke.out" >&2 || true
  sed -n '1,80p' "$SMOKE_DIR/picoclaw-deploy-smoke.err" >&2 || true
fi

systemctl --user status picoclaw.service --no-pager -l | sed -n '1,20p'
echo "deploy complete (backup timestamp: $ts)"
