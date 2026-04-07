#!/usr/bin/env bash
set -euo pipefail

TARGET_NAME="${TARGET_NAME:-}"
PICOCLAW_HOME="${PICOCLAW_HOME:-$HOME/.picoclaw}"
REPO_DIR="${REPO_DIR:-}"
DEPLOY_BRANCH="${DEPLOY_BRANCH:-}"
SERVICE_NAME="${SERVICE_NAME:-picoclaw.service}"
POLL_INTERVAL_SECONDS="${POLL_INTERVAL_SECONDS:-60}"
INSTALL_ROOT="${INSTALL_ROOT:-$HOME/.local/lib/picoclaw}"
HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:18790/ready}"
HEALTH_FALLBACK_URL="${HEALTH_FALLBACK_URL:-http://127.0.0.1:18790/health}"
GO_BIN="${GO_BIN:-}"

if [[ -z "$TARGET_NAME" ]]; then
  echo "TARGET_NAME is required" >&2
  exit 1
fi
if [[ -z "$REPO_DIR" ]]; then
  echo "REPO_DIR is required" >&2
  exit 1
fi
if [[ -z "$DEPLOY_BRANCH" ]]; then
  echo "DEPLOY_BRANCH is required" >&2
  exit 1
fi

runtime_dir="$PICOCLAW_HOME/runtime/self-improve"
poller_script="$runtime_dir/picoclaw_self_improve_poller.sh"
deploy_script="$runtime_dir/picoclaw_deploy_local.sh"
state_dir="$PICOCLAW_HOME/self-improve/$TARGET_NAME"
env_file="$state_dir/poller.env"
unit_dir="$HOME/.config/systemd/user"
unit_name="picoclaw-self-improve-$TARGET_NAME.service"
unit_path="$unit_dir/$unit_name"

if [[ ! -f "$poller_script" ]]; then
  echo "Poller script not found: $poller_script" >&2
  exit 1
fi
if [[ ! -f "$deploy_script" ]]; then
  echo "Deploy script not found: $deploy_script" >&2
  exit 1
fi

mkdir -p "$state_dir" "$unit_dir"

cat >"$env_file" <<EOF
TARGET_NAME=$TARGET_NAME
PICOCLAW_HOME=$PICOCLAW_HOME
REPO_DIR=$REPO_DIR
DEPLOY_BRANCH=$DEPLOY_BRANCH
STATE_DIR=$state_dir
POLL_INTERVAL_SECONDS=$POLL_INTERVAL_SECONDS
SERVICE_NAME=$SERVICE_NAME
INSTALL_ROOT=$INSTALL_ROOT
HEALTH_URL=$HEALTH_URL
HEALTH_FALLBACK_URL=$HEALTH_FALLBACK_URL
GO_BIN=$GO_BIN
DEPLOY_SCRIPT=$deploy_script
EOF
chmod 600 "$env_file"

cat >"$unit_path" <<EOF
[Unit]
Description=PicoClaw self-improve poller ($TARGET_NAME)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$env_file
WorkingDirectory=$PICOCLAW_HOME
ExecStart=/bin/bash $poller_script
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now "$unit_name"
echo "Installed $unit_name"
