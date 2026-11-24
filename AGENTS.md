# Secure MCP Gateway Agent Instructions

## Purpose
This Go repository implements a development Secure MCP Gateway between AI/MCP clients and downstream MCP servers. Build incrementally; the final MVP adds bearer authentication, RBAC, audit logging, risk classification, approvals, and in-memory rate limiting. It is not production-ready.

## General Rules
- Keep Go simple, idiomatic, and readable; prefer the standard library.
- Add dependencies only when necessary; avoid premature abstractions.
- Make small, logically grouped changes and preserve working behavior.
- Explain major architectural decisions before implementing them.
- Do not rewrite unrelated files or silently ignore errors.

## MCP
- Use the official github.com/modelcontextprotocol/go-sdk package.
- Verify APIs against the resolved SDK version; never invent SDK APIs.
- Keep stdout reserved for MCP when using stdio transport.
- Gateway code must forward calls rather than duplicate downstream tool behavior.
- Cover proxy behavior with integration tests through both MCP connections.
- Preserve downstream tool results, close sessions and child processes, and never construct shell commands from tool arguments.
- Every upstream tool must map to exactly one downstream route. Reject duplicate route names instead of overwriting them.
- Keep routing explicit and testable; adding a downstream server requires integration coverage.
- Partial startup failures must clean up all already-started downstream resources.
- Authentication and authorization are separate; uncertain identity or policy fails closed.
- Denied, pending, and rate-limited calls must never reach downstream servers.
- High-risk tools require explicit approval by a separate operator credential and must be consumed once.
- Rate limiting runs before forwarding. Audit events must not contain tokens, API keys, passwords, or raw authorization headers.
- Live AI API calls are optional and must never be required by the normal test suite.
- Never use real destructive external actions in automated tests; use safe fixtures.

## Security
- Never hardcode, commit, or log credentials, tokens, passwords, or API keys.
- Validate untrusted inputs and tool arguments.
- Default to deny when authorization is uncertain; use least privilege.
- Keep authentication separate from authorization; enforce policy before forwarding.
- Use explicit timeouts for outbound network calls.
- Destructive actions should eventually require human approval.

## Quality
Before completing work, run:
```sh
gofmt -w .
go mod tidy
go vet ./...
go test ./...
go build ./...
```
Add meaningful tests for implemented behavior. Inspect for secrets, unused dependencies, generated clutter, dead code, and unrelated changes.

## Scope
The AgentGate workspace uses PostgreSQL, Redis, and a Compose file because that local deployment was explicitly requested. Gateway unit tests and `go test ./...` stay free of those services, Docker, Ollama, and API keys. Do not add OpenFGA, gRPC, Kubernetes, or a cloud host. OpenAI remains optional and off by default. Stop after the requested milestone.

## Communication
At completion, summarize files changed, working behavior, design decisions, commands and results, run instructions, limitations, and the suggested next milestone. Do not claim planned features already work.
