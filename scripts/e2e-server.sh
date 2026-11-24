#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ ! -f web/dist/index.html ]; then
  echo "web/dist is missing. Run npm run build in web/ first." >&2
  exit 1
fi
export AGENTGATE_DEV_LOGIN=1
export AGENTGATE_DEV_PASSWORD=dev-only-password-value
export GATEWAY_INTERNAL_SECRET=dev-only-internal-secret-0123456789abcdef
export GATEWAY_VIEWER_TOKEN=dev-only-viewer-token-0123456789abcdef
export GATEWAY_DEVELOPER_TOKEN=dev-only-developer-token-0123456789abcd
export GATEWAY_ADMIN_TOKEN=dev-only-admin-token-0123456789abcdef
export GATEWAY_APPROVER_TOKEN=dev-only-approver-token-0123456789abcd
export AGENTGATE_PUBLIC_URL=http://127.0.0.1:18090
export AGENTGATE_GATEWAY_URL=http://127.0.0.1:18080/mcp
export AGENTGATE_FILES_ROOT=fixtures/workspace
mkdir -p bin
go build -o bin/gateway ./cmd/gateway
go build -o bin/workspace ./cmd/workspace
go build -o bin/mock-server ./cmd/mock-server
go build -o bin/data-server ./cmd/data-server
go build -o bin/github-server ./cmd/github-server
go build -o bin/sampledb-server ./cmd/sampledb-server
go build -o bin/files-server ./cmd/files-server
gateway_log=/tmp/agentgate-e2e-gateway.log
: >"$gateway_log"
./bin/gateway \
  -listen 127.0.0.1:18080 \
  -utility-command ./bin/mock-server \
  -data-command ./bin/data-server \
  -github-command ./bin/github-server \
  -sampledb-command ./bin/sampledb-server \
  -sampledb-dsn file:fixtures/sample.db \
  -files-command ./bin/files-server \
  -files-root fixtures/workspace \
  -limiter memory \
  -store memory >"$gateway_log" 2>&1 &
gateway_pid=$!
trap 'kill "$gateway_pid" 2>/dev/null || true' EXIT INT TERM
i=0
while ! grep -q '"event":"listening"' "$gateway_log" 2>/dev/null; do
  if ! kill -0 "$gateway_pid" 2>/dev/null; then
    echo "gateway exited before listening" >&2
    cat "$gateway_log" >&2
    exit 1
  fi
  i=$((i + 1))
  if [ "$i" -gt 100 ]; then
    echo "gateway did not listen" >&2
    cat "$gateway_log" >&2
    exit 1
  fi
  sleep 0.1
done
./bin/workspace -listen 127.0.0.1:18090 -web-dir web/dist -store memory -provider fake
