# AgentGate design

AgentGate is a local workspace on top of the existing Secure MCP Gateway. The gateway remains the only component that can invoke a tool. The browser, the model, and the workspace API cannot reach GitHub, the sample database, or workspace files except by an authenticated MCP call through that gateway.

This is one local workspace with separate user, agent, and operator identities. It is not a multi-tenant isolation boundary and it is not a production control plane.

## Processes

- `cmd/gateway` publishes the upstream MCP server and clients to downstream MCP servers.
- `cmd/workspace` is the browser backend: session, REST, server-sent events, and the agent loop.
- Downstream servers are fixed executables: utility, data, GitHub read-only, sample database, and workspace files.
- Ollama runs on the host. The workspace calls an allowlisted loopback URL. The browser never calls Ollama.
- PostgreSQL stores workspace state and, when configured, gateway audit and approvals.
- Redis stores shared rate-limit counters when `-limiter=redis` is set.

`stdio` and loopback HTTP keep the current bearer-token behavior so existing tests stay valid. That bearer mode is development authentication. The workspace profile does not put those tokens in the browser. It mints a short-lived HMAC token after the human session is checked.

An explicit `-listen-mode=container` flag is required before the gateway binds a non-loopback address. Compose does not publish that port on the host.

## Trust

| Actor | What it may do |
| --- | --- |
| Browser | Call the workspace on the same origin. Hold only an HttpOnly session cookie. |
| Signed-in user | Manage their agents, conversations, and runs. Read their own audit rows. |
| Agent identity | A server-side scope derived from the owner and the agent allowlist. It is not a value supplied by the model. |
| Operator | Approve or deny a pending action. Must be a different user than the requester. |
| Model | Propose text and tool calls. It does not hold credentials and does not enforce policy. |
| Gateway | Authenticate, authorize, limit, classify risk, require approval, audit, then forward. |

Effective tool permission is the intersection of the user role, the agent allowlist, and the catalog policy after administrator restrictions. Restrictions can remove access. They cannot add a tool, add a role the catalog does not allow, or lower a risk level.

High-risk calls pause. The operator reviews the canonical arguments. Approval does not run the tool as the operator. The original user and agent resume the same arguments. The approval is consumed once.

## Delegation token

`v1.<payload>.<hmac>` using HMAC-SHA256 and `GATEWAY_INTERNAL_SECRET`.

Payload claims: `iss=agentgate-workspace`, `aud=mcp-gateway`, `sub`, `role`, `agent_id`, `tools`, `exp`, `approver`. Tool-call tokens have `approver=false`. Approval tokens have `role=approver` and `approver=true`, and they are rejected for `tools/call`. Expired, wrong-audience, and combined admin-plus-approver tokens are rejected. The tool list is required. An empty list authorizes nothing.

Legacy bearer tokens do not carry an agent allowlist, so existing CLI clients keep their role policy.

## Request pipeline

1. Authenticate the bearer token, delegation token, or configured OIDC access token.
2. Resolve the route. Reject unknown names and duplicate registrations.
3. Authorize against the current role, agent allowlist, and restrictions.
4. Apply the identity rate limit and the concurrency limit.
5. Classify risk from catalog metadata.
6. For high risk, require an unconsumed approval bound to the requester, role, agent, tool, and argument digest.
7. Persist the audit intent. A write failure stops the call before forwarding.
8. Forward with a timeout, cancellation, and an output cap.
9. Persist the final outcome. If that write fails after a forward, the client receives `outcome_unknown` and the call is not retried.

Denied, pending, and rate-limited calls do not call the downstream server.

Provider function names replace `.` with `__` (`utility.add` becomes `utility__add`). The map is explicit and reverses to one route.

## Data

PostgreSQL migrations live in `internal/store/migrations`. Timestamps are `timestamptz` in UTC. Queries are parameterized.

| Table | Purpose |
| --- | --- |
| users | Local user, external subject, role |
| sessions | Server session and CSRF secret |
| agents, agent_versions | Owned agent configuration |
| integrations | Connector status, no remote secrets |
| conversations, messages | Chat history, deleted with the conversation |
| runs, run_events, tool_calls | Ordered run state and bounded tool output |
| approvals | Pending decision and single-use consumption |
| audit_events | Structured decisions without raw arguments or credentials |
| findings | Rule, severity, evidence ids, status |
| policy_restrictions | Narrower-than-catalog role sets |

The in-memory store implements the same operations for unit tests and for `-store=memory`. Memory mode is explicit. It does not survive a restart and it does not span processes.

Argument bytes are kept on the approval and tool-call rows so an operator can see the exact action. Audit rows store the digest, tool, decision, and ids only. A field scrubber redacts long token-like strings before an audit line is encoded.

Backup is `pg_dump` of the AgentGate database. Deleting a conversation removes its messages and runs. Audit rows are append-only inside the application; the product does not claim tamper-proof logs. There is no automatic retry of an unknown downstream result.

## API

Browser calls are `/api/v1/...` on the workspace origin. Mutating requests require the CSRF token returned by `GET /api/v1/session`. Cookies are `HttpOnly` and `SameSite=Lax`. `Secure` is enabled when the public URL is HTTPS. Local HTTP is the documented exception.

| Method and path | Behavior |
| --- | --- |
| GET /api/v1/session | Current user, roles, CSRF token |
| GET /api/v1/auth/login | Redirect to the OIDC authorization endpoint with PKCE. `OIDC_BROWSER_ISSUER` is the browser-facing issuer when it differs from the internal `OIDC_ISSUER` used for token exchange and JWKS. |
| GET /api/v1/auth/callback | Validate the token, create a session |
| POST /api/v1/auth/logout | End the session |
| POST /api/v1/auth/dev-login | Development-only password login, disabled unless enabled |
| GET, POST /api/v1/agents | List and create |
| GET, PATCH /api/v1/agents/{id} | Read and edit |
| POST /api/v1/agents/{id}/clone and /disable | Clone or disable an owned agent |
| GET /api/v1/tools and /integrations | Catalog, health, enablement |
| GET, POST /api/v1/conversations | List and create |
| GET, DELETE /api/v1/conversations/{id} | History and deletion |
| POST /api/v1/conversations/{id}/runs | Start a run |
| GET /api/v1/runs/{id} | Inspect |
| GET /api/v1/runs/{id}/events | SSE stream |
| POST /api/v1/runs/{id}/cancel and /resume | Cancel, or resume after approval |
| GET /api/v1/approvals and /approvals/{id} | Operator queue and history |
| POST /api/v1/approvals/{id}/decision | Approve or deny |
| GET, PUT /api/v1/policies | Effective policy and restricting edits |
| GET /api/v1/audit | Paginated filters |
| GET /api/v1/analytics | Aggregates for a bounded range |
| GET, POST /api/v1/findings | List, scan, acknowledge, explain |
| GET /api/v1/settings | Models, provider status, limits |

SSE event names: `run.started`, `assistant.delta`, `tool.proposed`, `tool.allowed`, `tool.blocked`, `approval.required`, `tool.completed`, `tool.failed`, `run.completed`, `run.failed`, `run.cancelled`. `id` is the persisted sequence. `Last-Event-ID` replays later events for the owning user. Heartbeats are comments and are not persisted.

Run states: `queued`, `running`, `waiting_for_approval`, `completed`, `failed`, `cancelled`.

Approval states: `pending`, `approved`, `denied`, `expired`, `consumed`. Execution outcome is stored on the tool call and the final audit row, not as a substitute for the approval state.

## Analytics and findings

A logical request is one `request_id`. Intent and result rows are collapsed to the latest row. Outcomes stay distinct: authorization denial, rate limit, approval pending, downstream error, success, and unknown. Latency is the gateway duration on that final row, including downstream time and excluding model inference. p50 is returned when at least one sample exists, p95 at 20 samples, and p99 at 100. Missing percentiles are null.

Detectors, rule version `1`:

- `repeated_auth_denials`: 5 denials in 10 minutes
- `request_burst`: 30 requests in 1 minute
- `repeated_high_risk`: 3 high-risk requests in 15 minutes
- `downstream_failures`: 4 downstream errors in 10 minutes

The local summary sends the finding title, counts, and evidence ids to Ollama. It does not send prompts, arguments, or credentials. The rule text remains available when Ollama is down.

## Metrics

`/metrics` and `/readyz` bind on the workspace listen address, which defaults to loopback. Readiness fails when the configured database or Redis is down. Ollama is reported in settings and is not required for readiness. Metric labels use the connector id and decision, not user ids or raw URLs.

## Configuration

See `.env.example`. Secrets are generated into an ignored `.env`. Demo passwords exist only for the development login and the Compose demo realm, and that login stays off unless `AGENTGATE_DEV_LOGIN=1`.

OpenAI remains available through `cmd/demo-agent` and is not called by the workspace.

## Failure semantics

- Missing policy, unknown tool, bad credentials, rate limit, and missing approval never forward.
- Approval replay, changed arguments, expiry, denial, or a different requester never forward.
- Redis is required when `-limiter=redis`. If Redis is down, the call is `limiter_unavailable`.
- The historical default `-limiter=memory` remains so the existing CLI and tests keep working. The workspace Make target and Compose file set Redis explicitly.
- A failed intent write blocks forwarding. A failed result write after forwarding surfaces `outcome_unknown` and does not retry.
- The product does not claim exactly-once effects at a third-party service, prompt-injection immunity, or protection from a compromised host.
