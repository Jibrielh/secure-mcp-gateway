#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ ! -f .env ]; then
  secret=$(openssl rand -hex 32)
  umask 077
  cat > .env <<EOF
AGENTGATE_STORE=memory
AGENTGATE_LIMITER=memory
GATEWAY_INTERNAL_SECRET=$secret
GATEWAY_VIEWER_TOKEN=$(openssl rand -hex 32)
GATEWAY_DEVELOPER_TOKEN=$(openssl rand -hex 32)
GATEWAY_ADMIN_TOKEN=$(openssl rand -hex 32)
GATEWAY_APPROVER_TOKEN=$(openssl rand -hex 32)
AGENTGATE_DEV_LOGIN=1
AGENTGATE_DEV_PASSWORD=$(openssl rand -hex 18)
OLLAMA_URL=http://127.0.0.1:11434
OLLAMA_MODEL=${OLLAMA_MODEL:-qwen2.5:3b}
AGENTGATE_PUBLIC_URL=http://127.0.0.1:8090
AGENTGATE_GATEWAY_URL=http://127.0.0.1:8080/mcp
AGENTGATE_FILES_ROOT=fixtures/workspace
EOF
  echo "Wrote ignored .env. The development password is in that file, not in this output."
fi
set -a
. ./.env
set +a
make build
provider=fake
if curl -sf "$OLLAMA_URL/api/tags" >/dev/null; then
  provider=ollama
fi
./bin/gateway -listen 127.0.0.1:8080 -utility-command ./bin/mock-server -data-command ./bin/data-server -github-command ./bin/github-server -sampledb-command ./bin/sampledb-server -sampledb-dsn file:fixtures/sample.db -files-command ./bin/files-server -files-root fixtures/workspace -limiter "${AGENTGATE_LIMITER:-memory}" -store "${AGENTGATE_STORE:-memory}" &
gateway_pid=$!
trap 'kill $gateway_pid' EXIT INT TERM
./bin/workspace -listen 127.0.0.1:8090 -web-dir web/dist -store "${AGENTGATE_STORE:-memory}" -provider "$provider"
