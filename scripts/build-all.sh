#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT="${OUTPUT:-cli-proxy-api}"
SKIP_FRONTEND=0
SKIP_BACKEND=0
USE_LDFLAGS=1

usage() {
  cat <<'EOF'
Usage: ./scripts/build-all.sh [options]

Builds the resource console frontend and the CLIProxyAPI server binary.

Options:
  -o, --output <path>   Output binary path (default: cli-proxy-api)
      --skip-frontend  Skip web/resource-console build
      --skip-backend   Skip Go server build
      --no-ldflags     Build without embedding git version metadata
  -h, --help            Show this help

Environment:
  OUTPUT=<path>         Same as --output
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -o|--output)
      if [[ $# -lt 2 || -z "${2:-}" ]]; then
        echo "missing value for $1" >&2
        exit 1
      fi
      OUTPUT="$2"
      shift 2
      ;;
    --skip-frontend)
      SKIP_FRONTEND=1
      shift
      ;;
    --skip-backend)
      SKIP_BACKEND=1
      shift
      ;;
    --no-ldflags)
      USE_LDFLAGS=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

cd "$ROOT_DIR"

if [[ "$SKIP_FRONTEND" -eq 0 ]]; then
  echo "==> Building resource console"
  if [[ -f web/resource-console/package-lock.json ]]; then
    npm ci --prefix web/resource-console
  else
    npm install --prefix web/resource-console
  fi
  npm run build --prefix web/resource-console
else
  echo "==> Skipping resource console build"
fi

if [[ "$SKIP_BACKEND" -eq 0 ]]; then
  echo "==> Building server binary: $OUTPUT"
  if [[ "$USE_LDFLAGS" -eq 1 ]]; then
    VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
    COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
    BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    go build \
      -ldflags "-X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
      -o "$OUTPUT" ./cmd/server
  else
    go build -o "$OUTPUT" ./cmd/server
  fi
else
  echo "==> Skipping server build"
fi

echo "==> Build complete"
