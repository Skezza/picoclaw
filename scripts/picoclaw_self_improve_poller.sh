#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-}"
TARGET_NAME="${TARGET_NAME:-default}"
DEPLOY_BRANCH="${DEPLOY_BRANCH:-}"
STATE_DIR="${STATE_DIR:-${PICOCLAW_HOME:-$HOME/.picoclaw}/self-improve/${TARGET_NAME}}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-60}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_SCRIPT="${DEPLOY_SCRIPT:-$SCRIPT_DIR/picoclaw_deploy_local.sh}"

if [[ -z "$REPO_DIR" ]]; then
  echo "REPO_DIR is required" >&2
  exit 1
fi
if [[ -z "$DEPLOY_BRANCH" ]]; then
  echo "DEPLOY_BRANCH is required" >&2
  exit 1
fi
if [[ ! -d "$REPO_DIR" ]]; then
  echo "REPO_DIR does not exist: $REPO_DIR" >&2
  exit 1
fi
if [[ ! -f "$DEPLOY_SCRIPT" ]]; then
  echo "Deploy script is missing: $DEPLOY_SCRIPT" >&2
  exit 1
fi

mkdir -p "$STATE_DIR/worktrees"
last_seen_file="$STATE_DIR/last_seen_sha"
last_deployed_file="$STATE_DIR/last_deployed_sha"
lock_dir="$STATE_DIR/deploy.lock"

while true; do
  git -C "$REPO_DIR" fetch --prune origin >/dev/null 2>&1 || true
  remote_ref="origin/$DEPLOY_BRANCH"
  sha="$(git -C "$REPO_DIR" rev-parse --verify "$remote_ref" 2>/dev/null || true)"
  if [[ -z "$sha" ]]; then
    sleep "$POLL_INTERVAL_SECONDS"
    continue
  fi

  last_seen="$(cat "$last_seen_file" 2>/dev/null || true)"
  if [[ "$sha" == "$last_seen" ]]; then
    sleep "$POLL_INTERVAL_SECONDS"
    continue
  fi

  if ! mkdir "$lock_dir" 2>/dev/null; then
    sleep "$POLL_INTERVAL_SECONDS"
    continue
  fi

  worktree="$STATE_DIR/worktrees/$sha"
  if [[ ! -d "$worktree/.git" && ! -f "$worktree/.git" ]]; then
    rm -rf "$worktree"
    git -C "$REPO_DIR" worktree add --detach "$worktree" "$sha" >/dev/null
  fi

  if TARGET_NAME="$TARGET_NAME" SRC_DIR="$worktree" /bin/bash "$DEPLOY_SCRIPT"; then
    printf '%s\n' "$sha" >"$last_seen_file"
    printf '%s\n' "$sha" >"$last_deployed_file"
  fi

  rmdir "$lock_dir" 2>/dev/null || true
  sleep "$POLL_INTERVAL_SECONDS"
done
