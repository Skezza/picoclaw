#!/usr/bin/env bash
set -euo pipefail

HA_CONFIG_DIR="${HA_CONFIG_DIR:-/mnt/1tb/home-assistant/config}"
HA_IMAGE="${HA_IMAGE:-ghcr.io/home-assistant/home-assistant:stable}"
HA_CONTAINER_NAME="${HA_CONTAINER_NAME:-home-assistant}"
HA_TZ="${HA_TZ:-Europe/London}"

mkdir -p "$HA_CONFIG_DIR"

if docker ps -a --format '{{.Names}}' | grep -Fxq "$HA_CONTAINER_NAME"; then
  docker rm -f "$HA_CONTAINER_NAME" >/dev/null
fi

docker run -d \
  --name "$HA_CONTAINER_NAME" \
  --restart unless-stopped \
  --network host \
  -e TZ="$HA_TZ" \
  -v "$HA_CONFIG_DIR:/config" \
  "$HA_IMAGE"

echo "Home Assistant container started."
echo "Config directory: $HA_CONFIG_DIR"
echo "Expected LAN URL: http://quanta.local:8123"
