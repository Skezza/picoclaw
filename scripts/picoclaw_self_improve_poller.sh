#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="${REPO_DIR:-}"
TARGET_NAME="${TARGET_NAME:-default}"
DEPLOY_BRANCH="${DEPLOY_BRANCH:-}"
STATE_DIR="${STATE_DIR:-${PICOCLAW_HOME:-$HOME/.picoclaw}/self-improve/${TARGET_NAME}}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-60}"
FAILED_RETRY_SECONDS="${FAILED_RETRY_SECONDS:-900}"
KEEP_WORKTREES="${KEEP_WORKTREES:-5}"
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
failed_sha_file="$STATE_DIR/last_failed_sha"
failed_count_file="$STATE_DIR/last_failed_count"
failed_next_retry_file="$STATE_DIR/last_failed_next_retry_at"
lock_dir="$STATE_DIR/deploy.lock"
lock_held=0

cleanup_lock() {
  if [[ "$lock_held" -eq 1 ]]; then
    rm -rf "$lock_dir"
    lock_held=0
  fi
}
trap cleanup_lock EXIT INT TERM

acquire_lock() {
  local stale_pid=""
  if mkdir "$lock_dir" 2>/dev/null; then
    printf '%s\n' "$$" >"$lock_dir/pid"
    lock_held=1
    return 0
  fi

  if [[ -f "$lock_dir/pid" ]]; then
    stale_pid="$(cat "$lock_dir/pid" 2>/dev/null || true)"
    if [[ -n "$stale_pid" ]] && ! kill -0 "$stale_pid" 2>/dev/null; then
      echo "Removing stale self-improve lock for $TARGET_NAME (pid $stale_pid)" >&2
      rm -rf "$lock_dir"
      if mkdir "$lock_dir" 2>/dev/null; then
        printf '%s\n' "$$" >"$lock_dir/pid"
        lock_held=1
        return 0
      fi
    fi
  fi
  return 1
}

note_failure() {
  local sha="$1"
  local now count next_retry previous_sha
  now="$(date +%s)"
  previous_sha="$(cat "$failed_sha_file" 2>/dev/null || true)"
  count=1
  if [[ "$previous_sha" == "$sha" ]]; then
    count="$(cat "$failed_count_file" 2>/dev/null || echo 0)"
    count=$((count + 1))
  fi
  next_retry=$((now + FAILED_RETRY_SECONDS))
  printf '%s\n' "$sha" >"$failed_sha_file"
  printf '%s\n' "$count" >"$failed_count_file"
  printf '%s\n' "$next_retry" >"$failed_next_retry_file"
}

clear_failure_state() {
  rm -f "$failed_sha_file" "$failed_count_file" "$failed_next_retry_file"
}

should_retry_sha() {
  local sha="$1"
  local failed_sha next_retry now
  failed_sha="$(cat "$failed_sha_file" 2>/dev/null || true)"
  if [[ "$sha" != "$failed_sha" || -z "$failed_sha" ]]; then
    return 0
  fi
  next_retry="$(cat "$failed_next_retry_file" 2>/dev/null || echo 0)"
  now="$(date +%s)"
  [[ "$now" -ge "$next_retry" ]]
}

prune_old_worktrees() {
  if [[ ! "$KEEP_WORKTREES" =~ ^[0-9]+$ ]] || [[ "$KEEP_WORKTREES" -le 0 ]]; then
    return 0
  fi
  mapfile -t old_worktrees < <(find "$STATE_DIR/worktrees" -mindepth 1 -maxdepth 1 -type d -printf '%T@ %p\n' | sort -nr | awk 'NR>'"$KEEP_WORKTREES"' {print $2}')
  if [[ "${#old_worktrees[@]}" -gt 0 ]]; then
    rm -rf "${old_worktrees[@]}"
  fi
}

while true; do
  if ! git -C "$REPO_DIR" fetch --prune origin >/dev/null 2>&1; then
    echo "self-improve poller fetch failed for $TARGET_NAME" >&2
    sleep "$POLL_INTERVAL_SECONDS"
    continue
  fi

  remote_ref="origin/$DEPLOY_BRANCH"
  sha="$(git -C "$REPO_DIR" rev-parse --verify "$remote_ref" 2>/dev/null || true)"
  if [[ -z "$sha" ]]; then
    sleep "$POLL_INTERVAL_SECONDS"
    continue
  fi

  last_seen="$(cat "$last_seen_file" 2>/dev/null || true)"
  last_deployed="$(cat "$last_deployed_file" 2>/dev/null || true)"
  if [[ "$sha" != "$last_seen" ]]; then
    printf '%s\n' "$sha" >"$last_seen_file"
  fi

  if [[ "$sha" == "$last_deployed" ]]; then
    sleep "$POLL_INTERVAL_SECONDS"
    continue
  fi

  if ! should_retry_sha "$sha"; then
    sleep "$POLL_INTERVAL_SECONDS"
    continue
  fi

  if ! acquire_lock; then
    sleep "$POLL_INTERVAL_SECONDS"
    continue
  fi

  worktree="$STATE_DIR/worktrees/$sha"
  if [[ ! -d "$worktree/.git" && ! -f "$worktree/.git" ]]; then
    rm -rf "$worktree"
    if ! git -C "$REPO_DIR" worktree add --detach "$worktree" "$sha" >/dev/null; then
      echo "self-improve poller failed to prepare worktree for $TARGET_NAME at $sha" >&2
      note_failure "$sha"
      cleanup_lock
      sleep "$POLL_INTERVAL_SECONDS"
      continue
    fi
  fi

  if TARGET_NAME="$TARGET_NAME" SRC_DIR="$worktree" /bin/bash "$DEPLOY_SCRIPT"; then
    printf '%s\n' "$sha" >"$last_deployed_file"
    clear_failure_state
    prune_old_worktrees
  else
    note_failure "$sha"
  fi

  cleanup_lock
  sleep "$POLL_INTERVAL_SECONDS"
done
