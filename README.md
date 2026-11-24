# Secure MCP Gateway

A Go project building a gateway between AI agents/MCP clients and downstream MCP servers.

AI agents can call powerful external tools through MCP. This gateway provides a centralized development security boundary between those agents and downstream MCP servers, so identity, authorization, approvals, rate limits, and audit events are handled in one place.

## Current status

**AgentGate workspace:** a local browser app in front of the same gateway. The gateway remains the only tool enforcement boundary. The workspace handles the session, agent loop, and REST/SSE API. See [DEMO.md](DEMO.md) for startup and [DESIGN.md](DESIGN.md) for the trust model. This is a local development system, not a production control.

**Original gateway MVP:** one upstream MCP connection routes tool calls to local downstream MCP processes through authentication, RBAC, risk checks, human approval, rate limiting, and audit logging. Stdio and loopback HTTP behavior is preserved.

Implemented:

- [x] Go project setup and official MCP Go SDK
- [x] Utility/mock MCP server with `echo` and `add`
- [x] Data MCP server with `lookup_record`
- [x] Gateway acting as an MCP server upstream and MCP client downstream
- [x] Multiple downstream sessions and deterministic tool routing
- [x] Duplicate route rejection and downstream result/error preservation
- [x] End-to-end multi-server tests and partial-startup cleanup coverage
- [x] Development bearer-token authentication
- [x] Tool-level RBAC with fail-closed decisions
- [x] Risk classification and safe high-risk mock action
- [x] Separate operator approval with single-use approval records
- [x] Per-identity in-memory rate limiting
- [x] Structured JSON audit events and request IDs
- [x] Optional OpenAI Responses function-calling demo agent

Also implemented for the local workspace:

- [x] React workspace with session, chat, agents, tools, approvals, audit, policy, insights, and settings
- [x] Short-lived HMAC delegation from the workspace to the gateway
- [x] OIDC authorization-code + PKCE client, with tests that mint JWTs locally
- [x] Postgres store and Redis token bucket, both optional for `go test ./...`
- [x] Read-only GitHub, sample analytics, and sandboxed workspace file connectors
- [x] Local Ollama agent loop, with a fake provider for tests
- [x] Compose file for Postgres, Redis, Keycloak, gateway, and workspace

Still not implemented:

- [ ] Slack or other write-capable external tools
- [ ] ReBAC / OpenFGA
- [ ] gRPC internal services
- [ ] Kubernetes or a cloud deployment
- [ ] A verified live Keycloak login on this machine

## Security pipeline

```text
authenticate -> route -> RBAC -> rate limit -> risk
  -> human approval when high-risk -> audit -> downstream
```

Missing or invalid credentials, denied tools, pending or denied approvals, and rate-limited calls never reach downstream. Audit events exclude tokens, API keys, passwords, raw authorization headers, and tool argument payloads.

## Architecture and routes

```text
                                +--------------------+
                                | Utility MCP Server |
                                | echo / add         |
                                +---------^----------+
                                          |
MCP Client --stdio--> Secure MCP Gateway --+
                                          |
                                          v
                                +--------------------+
                                | Data MCP Server    |
                                | lookup_record      |
                                +--------------------+
```

Both downstream connections also use stdio MCP. The gateway launches the processes, connects utility first and data second, validates explicit routes, and only then serves upstream. It forwards the original arguments and returns downstream content, structured content, and tool errors. It never performs echo, arithmetic, or record lookup locally.

| Upstream tool | Downstream session | Downstream tool | Example arguments | Text result |
| --- | --- | --- | --- | --- |
| `utility.echo` | utility | `echo` | `{"message":"hello"}` | `hello` |
| `utility.add` | utility | `add` | `{"a":4,"b":7}` | `11` |
| `data.lookup_record` | data | `lookup_record` | `{"id":2}` | `Bob` |
| `echo` (compatibility alias) | utility | `echo` | `{"message":"hello"}` | `hello` |
| `admin.mock_destructive_action` | utility | `mock_destructive_action` | `{"target":"demo"}` | approval required |

`echo` still accepts empty strings, Unicode, and line breaks. `add` requires two JSON numbers, uses float64 arithmetic, and also returns structured content `{"sum":11}`; an overflowing sum produces a tool error. `lookup_record` requires an integer ID and uses the fixed dataset `1 → Alice`, `2 → Bob`, `3 → Charlie`. It also returns structured content such as `{"id":2,"name":"Bob"}`. Missing records return a `record not found` tool error.

Route definitions live in Go, with no configuration file or dynamic tool discovery. Duplicate upstream names fail configuration instead of overwriting an existing route. Unknown upstream tools receive the SDK's normal protocol error. Input schemas are validated with the SDK before forwarding, and downstream servers validate their own inputs as well.

## Run

Requires Go 1.27.1 or newer for development. From the repository directory:

```sh
go mod download
go build -o bin/mock-server ./cmd/mock-server
go build -o bin/data-server ./cmd/data-server
go run ./cmd/gateway
```

Rebuild a downstream binary after changing its source. The gateway manages both downstream processes; do not start them separately. It waits for an upstream MCP client on stdin/stdout. There is no HTTP endpoint or interactive text prompt. Typing ordinary text or just tool arguments into the terminal is not an MCP request. Diagnostics and CLI help go to stderr. Press Ctrl-C to stop a manually started gateway.

For built executables:

```sh
go build -o bin/gateway ./cmd/gateway
./bin/gateway -utility-command ./bin/mock-server -data-command ./bin/data-server
```

Command flags accept executable paths, not shell commands or arguments. Defaults are `./bin/mock-server` and `./bin/data-server`, relative to the working directory. The existing `-downstream-command` flag remains an alias for `-utility-command`; supplying both is rejected. Both servers are now required, including when calling the legacy `echo` alias.

Outside the repository, use explicit paths:

```sh
/absolute/path/to/bin/gateway \
  -utility-command /absolute/path/to/bin/mock-server \
  -data-command /absolute/path/to/bin/data-server
```

Configure a stdio-capable MCP client to launch that gateway executable with those arguments. Running the built binaries needs no Go installation, API key, or AI provider.

For development authentication, export four distinct random credentials before starting the gateway:

```sh
export GATEWAY_VIEWER_TOKEN="$(openssl rand -hex 32)"
export GATEWAY_DEVELOPER_TOKEN="$(openssl rand -hex 32)"
export GATEWAY_ADMIN_TOKEN="$(openssl rand -hex 32)"
export GATEWAY_APPROVER_TOKEN="$(openssl rand -hex 32)"
```

To enable standard HTTP bearer headers and approvals, add `-listen 127.0.0.1:8080`. The MCP endpoint is `http://127.0.0.1:8080/mcp`. Review and decide approvals with `cmd/gatewayctl` using only `GATEWAY_APPROVER_TOKEN`:

```sh
go run ./cmd/gatewayctl -gateway-url http://127.0.0.1:8080/mcp -list-approvals
go run ./cmd/gatewayctl -gateway-url http://127.0.0.1:8080/mcp -request APPROVAL_ID -decision approve
```

`gatewayctl` can also call a tool with `GATEWAY_TOKEN`:

```sh
GATEWAY_TOKEN="$GATEWAY_DEVELOPER_TOKEN" go run ./cmd/gatewayctl \
  -gateway-url http://127.0.0.1:8080/mcp -tool utility.add -args '{"a":22,"b":35}'
```

Each downstream connection setup and tool call has a 10-second timeout. Startup fails if either required server cannot initialize; previously connected sessions are closed. On shutdown, sessions close in reverse startup order. The SDK closes child stdin and escalates termination if needed. There is no restart, reconnect, or partial availability mode. Tool-error results pass through; connection/protocol failures return a generic tool error without exposing downstream details.

The downstream servers remain independently runnable for MCP clients:

```sh
go run ./cmd/mock-server
go run ./cmd/data-server
```

## Optional AI demo

The demo agent is a small OpenAI Responses API function-calling client. OpenAI's [function-calling documentation](https://developers.openai.com/api/docs/guides/function-calling) describes the multi-step exchange used here: the application executes a function and returns a `function_call_output` item. The [gpt-5-mini model reference](https://developers.openai.com/api/docs/models/gpt-5-mini) lists Responses and function calling support.

OpenAI API access requires `OPENAI_API_KEY`, model access, and may incur separate API charges. The key is read from the environment, never printed, committed, or sent to downstream MCP processes. Live calls are not part of `go test ./...`.

Start the HTTP gateway, then run natural-language prompts:

```sh
export OPENAI_API_KEY="..."
export GATEWAY_TOKEN="$GATEWAY_DEVELOPER_TOKEN"
go run ./cmd/demo-agent "Add 22 and 35."
go run ./cmd/demo-agent "Look up record 2."
```

The model discovers gateway tools and chooses `utility.add` or `data.lookup_record`. For a high-risk prompt, the agent prints the pending approval ID and waits for a separate operator; it cannot approve its own request.

## Test

Direct downstream tests:

```sh
go test -v ./cmd/mock-server -run TestEchoStdio -count=1
go test -v ./cmd/mock-server -run TestAddStdio -count=1
go test -v ./cmd/data-server -run TestLookupRecordStdio -count=1
```

Gateway routing and the Milestone 2 regression:

```sh
go test -v ./cmd/gateway -run TestGatewayRoutesAcrossMultipleServers -count=1
go test -v ./cmd/gateway -run TestEchoThroughGateway -count=1
```

The multi-server test builds all three binaries in temporary directories and connects only to the gateway. Security tests cover authentication, RBAC, denied-call isolation, approvals, rate limits, audit redaction, the AI function-calling loop, and all earlier routing behavior. The normal test suite needs no OpenAI key, internet access, database, or paid API calls.

Full checks:

```sh
gofmt -w .
go mod tidy
go vet ./...
go test ./...
go build ./...
```

## Workspace

`make dev` writes an ignored `.env`, builds the binaries and the UI, and starts the gateway and workspace on loopback. The default store and limiter are in-memory, so a high-risk approval does not survive two separate processes. Set `AGENTGATE_STORE=postgres`, `AGENTGATE_LIMITER=redis`, and the matching URLs when the gateway and workspace must share state. `make verify` runs formatting, Go tests, the frontend typecheck, Vitest, and the production UI build.

The selected local model is `qwen2.5:3b`. It is used only when Ollama is running and `-provider=ollama`. The fake provider is deterministic and is what the normal tests use. OpenAI stays in `cmd/demo-agent` and is not called by the workspace.

## Limitations

The high-risk tool is a safe in-process demonstration. The product does not claim production certification, exactly-once external effects, or prompt-injection immunity. ReBAC, gRPC, Kubernetes, and cloud deployment are out of scope. Keycloak realm import is defined, and a live browser login against Keycloak was not executed here. Docker Compose config was not validated because the local daemon and Compose plugin were not available. Replace the development login before any shared use.

## Development and next milestone

Use the official VS Code Go extension (`golang.go`) and `gopls`. Follow `AGENTS.md`. Keep credentials out of source and logs; `.env` files and build outputs under `bin/` are ignored.

The final MVP milestone is complete. The project does not claim to prevent prompt injection or provide production security.

Resume summary candidates:

- Built a Go MCP gateway with bearer authentication, tool-level RBAC, rate limiting, multi-server routing, and downstream result preservation.
- Added structured security auditing, request IDs, risk classification, single-use human approval, and tests proving denied calls never reach downstream tools.
- Built an optional OpenAI Responses function-calling demo that executes natural-language requests through the gateway security boundary without live API calls in the test suite.
