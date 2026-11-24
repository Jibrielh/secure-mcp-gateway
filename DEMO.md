# AgentGate local demo

This is a development workspace. The passwords below are placeholders for a local demo. They are not production credentials.

## What you need

- Go 1.27.1 or newer
- Node 22 or newer and npm
- Optional: Ollama with `qwen2.5:3b`, PostgreSQL 16, and Redis on loopback
- Optional: Docker with Compose, for the container path

`go test ./...` does not need any of the optional services.

## Native startup

From the repository root:

```sh
make dev
```

The first run writes an ignored `.env` and prints that the development password is in that file. It does not print the password. The workspace listens on `http://127.0.0.1:8090` and the gateway on `http://127.0.0.1:8080`. Open the workspace URL in a browser.

Development usernames, enabled only when `AGENTGATE_DEV_LOGIN=1`:

| Username | Role |
| --- | --- |
| ada | developer |
| owen | approver |
| vera | viewer |
| amy | admin |

Sign in as ada. Open Chat, choose Workspace Assistant, and ask it to add 22 and 35. The answer comes through the gateway. For a high-risk pause, create an agent whose only tool is `admin.mock_destructive_action`, ask it to run maintenance, then sign in as owen and approve the exact action. Ada resumes the original run. Owen cannot call tools with the approver credential, and ada cannot approve her own request.

The in-memory default does not share approvals between the two processes. For that demo, point both processes at the same Postgres database and the same `GATEWAY_INTERNAL_SECRET`, and set `GATEWAY_APPROVER_TOKEN` on the workspace. Redis is required only when `-limiter=redis`. If Redis is selected and down, tool calls fail closed. Readiness fails when a configured database or Redis is down. Ollama is not required for readiness.

## Model

The recorded local tag is `qwen2.5:3b` (Ollama id `357c53fb659c`, Qwen2, 3.1B, Q4_K_M). `ollama show` reports a tools capability. A live `/api/chat` call from `TestOllamaToolCallLive` returned one `utility__add` tool call. No second model was pulled. Set `OLLAMA_URL=http://127.0.0.1:11434` and start the workspace with `-provider=ollama`. If Ollama is down, the fake provider is the explicit fallback and the settings page says so.

## Container path

`deploy/compose.yaml` builds the workspace and gateway, runs Postgres, Redis, and Keycloak, and publishes only `127.0.0.1:8090` and `127.0.0.1:8081`. The gateway has no host port. No service uses privileged mode or the Docker socket. Volumes are not deleted by the Makefile.

Copy `.env.example` to an ignored env file and replace the placeholders before `docker compose -f deploy/compose.yaml up`. The Keycloak realm import uses the development-only client secret `dev-only-not-for-production` and the same placeholder as each demo user's password. This machine did not have a running Docker daemon or a Compose plugin, so `docker compose config` was not validated.

## Checks

```sh
make verify
cd web && npm run test:e2e
```

The Playwright flow uses the fake provider and development fixture passwords. It is not a live model test.
