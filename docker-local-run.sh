#!/usr/bin/env bash
#
# Build the local Docker image and run CLIProxyAPI with persistent local data.
#
# Defaults:
#   host port: 28317
#   container port: read from config.yaml port, fallback 8317
#   bind address: 0.0.0.0
#   data dir: ./docker-data
#   logs dir: ./docker-logs
#
# Override examples:
#   HOST_PORT=28318 ./docker-local-run.sh
#   HOST_BIND_IP=127.0.0.1 ./docker-local-run.sh
#   DATA_DIR=/root/cli-proxy-data ./docker-local-run.sh

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
IMAGE_NAME="${IMAGE_NAME:-cli-proxy-api:local}"
CONTAINER_NAME="${CONTAINER_NAME:-cli-proxy-api-local}"
HOST_BIND_IP="${HOST_BIND_IP:-0.0.0.0}"
HOST_PORT="${HOST_PORT:-28317}"
CONTAINER_PORT="${CONTAINER_PORT:-}"
DATA_DIR="${DATA_DIR:-${ROOT_DIR}/docker-data}"
LOG_DIR="${LOG_DIR:-${ROOT_DIR}/docker-logs}"

VERSION="${VERSION:-$(git -C "${ROOT_DIR}" describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git -C "${ROOT_DIR}" rev-parse --short HEAD 2>/dev/null || echo none)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

mkdir -p "${DATA_DIR}" "${LOG_DIR}"

if [[ ! -f "${DATA_DIR}/config.yaml" ]]; then
  if [[ -f "${ROOT_DIR}/config.yaml" ]]; then
    cp "${ROOT_DIR}/config.yaml" "${DATA_DIR}/config.yaml"
    echo "Copied config.yaml to ${DATA_DIR}/config.yaml"
  else
    cp "${ROOT_DIR}/config.example.yaml" "${DATA_DIR}/config.yaml"
    echo "Created ${DATA_DIR}/config.yaml from config.example.yaml"
  fi
fi

for pool_file in claude-api-pool.db claude-api-pool.db-shm claude-api-pool.db-wal claude-api-pool.yaml; do
  if [[ -f "${ROOT_DIR}/${pool_file}" && ! -f "${DATA_DIR}/${pool_file}" ]]; then
    cp "${ROOT_DIR}/${pool_file}" "${DATA_DIR}/${pool_file}"
    echo "Copied ${pool_file} to ${DATA_DIR}/${pool_file}"
  fi
done

if [[ -z "${CONTAINER_PORT}" ]]; then
  CONTAINER_PORT="$(awk '
    /^[[:space:]]*#/ { next }
    /^[[:space:]]*port:[[:space:]]*/ {
      value=$0
      sub(/^[[:space:]]*port:[[:space:]]*/, "", value)
      sub(/[[:space:]]*#.*/, "", value)
      gsub(/["'\''[:space:]]/, "", value)
      if (value != "") {
        print value
        exit
      }
    }
  ' "${DATA_DIR}/config.yaml")"
  CONTAINER_PORT="${CONTAINER_PORT:-8317}"
fi

echo "Building ${IMAGE_NAME}"
docker build \
  --build-arg VERSION="${VERSION}" \
  --build-arg COMMIT="${COMMIT}" \
  --build-arg BUILD_DATE="${BUILD_DATE}" \
  -t "${IMAGE_NAME}" \
  "${ROOT_DIR}"

if docker ps -a --format '{{.Names}}' | grep -Fxq "${CONTAINER_NAME}"; then
  echo "Removing existing container ${CONTAINER_NAME}"
  docker rm -f "${CONTAINER_NAME}" >/dev/null
fi

echo "Starting ${CONTAINER_NAME}"
docker run -d \
  --name "${CONTAINER_NAME}" \
  --restart unless-stopped \
  -e TZ=Asia/Shanghai \
  -p "${HOST_BIND_IP}:${HOST_PORT}:${CONTAINER_PORT}" \
  -v "${DATA_DIR}:/CLIProxyAPI/data" \
  -v "${LOG_DIR}:/CLIProxyAPI/logs" \
  "${IMAGE_NAME}" \
  ./CLIProxyAPI --config /CLIProxyAPI/data/config.yaml --no-browser

echo
echo "Started."
echo "API:  http://${HOST_BIND_IP}:${HOST_PORT}"
echo "Port: ${HOST_BIND_IP}:${HOST_PORT}->${CONTAINER_PORT}"
echo "Logs: docker logs -f ${CONTAINER_NAME}"
echo "Data: ${DATA_DIR}"
