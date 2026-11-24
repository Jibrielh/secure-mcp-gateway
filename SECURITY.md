# Security notes

AgentGate is a local development boundary. It is not a certification, and it does not claim to stop prompt injection or to make external effects exactly once.

## Trust

One workspace is assumed. Records distinguish the user, the agent, and the operator. A tool runs only when the user role, the agent allowlist, and the catalog policy all allow it. A restriction can remove a role or disable a tool. It cannot add a role the catalog does not grant or lower a tool's risk.

The browser session is an HttpOnly `SameSite=Lax` cookie. Mutations need the CSRF token from the session response. The model never receives that cookie, the operator credential, or a token that skips the gateway.

The workspace mints a short-lived HMAC token for each gateway call. The audience is `mcp-gateway`, the issuer is `agentgate-workspace`, and the token carries the user, agent, expiry, and tool scope. The gateway rejects a missing, expired, or wrong-audience token. An approver token cannot call tools. A token that combines another role with the approver flag is rejected. Legacy bearer tokens remain for the CLI and existing tests. They are not placed in the browser.

## Pipeline

The gateway order is authenticate, route, schema and size checks, authorization, agent scope, rate limit, concurrency, approval, audit intent, forward, then final audit. A denied, pending, or rate-limited call is not forwarded. If the intent audit cannot be stored, the call is not forwarded. If the final audit fails after a forward, the caller sees `outcome_unknown` and the call is not retried.

Child processes receive a filtered environment. `GATEWAY_`, `AGENTGATE_`, `OIDC_`, `DATABASE_`, `REDIS_`, `OPENAI_`, and other secret-like names are removed. The GitHub connector is the only child that may receive `GITHUB_TOKEN`, and only when that variable is set for the gateway process.

## Data

Postgres queries are parameterized. Migrations run from an embedded SQL file. Redis keys use the prefix `agentgate:rl:`. The default limit is 20 calls per minute for the identity, with separate agent and tool keys. Missing Redis fails closed only when Redis mode is selected. The historical CLI default remains the in-memory limiter.

Audit text is scrubbed for bearer tokens, long hex strings, `sk-` prefixes, and `password` or `api_key` assignments. Approval and request identifiers are shorter than the hex redaction threshold so they remain visible. Do not put secrets in tool arguments; arguments are stored for the operator view and are not a secret store.

## Identity

Development login is off unless `AGENTGATE_DEV_LOGIN=1`. The development password must be at least 12 characters and is compared as a SHA-256 digest with a constant-time compare. OIDC checks RS256, issuer, audience, expiry, and rejects a refresh token. JWKS responses are cached for five minutes. Tests use a local key and do not contact Keycloak.

## Operations

Listen addresses must be literal IPs. Non-loopback binds require `-listen-mode=container`. The Compose file does not publish the gateway on the host. Metrics use connector id and decision labels, not user ids. Readiness fails when a configured database or Redis is unavailable. Ollama is optional for readiness.
