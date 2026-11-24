package security

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const AuthorizationMeta = "secure-mcp-gateway/authorization"
const ApprovalMeta = "secure-mcp-gateway/approval_id"
const RequestIDMeta = "secure-mcp-gateway/request_id"

type Event struct {
	RequestID        string `json:"request_id"`
	Identity         string `json:"identity"`
	Role             string `json:"role,omitempty"`
	UserID           string `json:"user_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	Tool             string `json:"tool"`
	DownstreamServer string `json:"downstream_server,omitempty"`
	Decision         string `json:"decision"`
	Reason           string `json:"reason"`
	Risk             string `json:"risk,omitempty"`
	ApprovalID       string `json:"approval_id,omitempty"`
	DurationMS       int64  `json:"duration_ms"`
	Phase            string `json:"phase,omitempty"`
	Outcome          string `json:"outcome,omitempty"`
}
type Audit struct {
	mu      sync.Mutex
	encoder *json.Encoder
	Sink    func(Event) error
}

func NewAudit(w io.Writer) *Audit { return &Audit{encoder: json.NewEncoder(w)} }
func (a *Audit) Write(e Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	e = scrubEvent(e)
	if a.Sink != nil {
		if err := a.Sink(e); err != nil {
			return err
		}
	}
	if a.encoder == nil {
		return nil
	}
	return a.encoder.Encode(e)
}
func NewRequestID() string { var id [16]byte; rand.Read(id[:]); return hex.EncodeToString(id[:]) }

// SharedLimiter is a process-independent counter. An error means the limiter is unavailable and the call must stop.
type SharedLimiter interface {
	Allow(ctx context.Context, identity, agent, tool string, now time.Time) (bool, error)
}

// ApprovalBackend stores approvals. *Approvals implements it in memory.
type ApprovalBackend interface {
	Create(id string, identity Identity, tool string, args json.RawMessage, now time.Time) (string, error)
	List(now time.Time) []Approval
	Decide(id, decision string, now time.Time) (Approval, error)
	Consume(id string, identity Identity, tool string, args json.RawMessage, now time.Time) string
}

// OperatorDecider records the distinct operator on a decision.
type OperatorDecider interface {
	DecideBy(id, decision, operatorID string, now time.Time) (Approval, error)
}

// RestrictionSource narrows catalog policy. It cannot add permissions.
type RestrictionSource interface {
	Lookup(tool string) (disabled bool, roles []string, ok bool)
}

type Gate struct {
	Auth            Authenticator
	Audit           *Audit
	Limiter         *Limiter
	Shared          SharedLimiter
	Approvals       *Approvals
	Backend         ApprovalBackend
	Restrictions    RestrictionSource
	Concurrency     *Concurrency
	Policies        map[string]Policy
	Now             func() time.Time
	ApprovalEnabled bool
	MaxArguments    int
}

func NewGate(auth Authenticator, audit *Audit, limit int, policies map[string]Policy) *Gate {
	return &Gate{Auth: auth, Audit: audit, Limiter: NewLimiter(limit, time.Minute), Approvals: NewApprovals(), Policies: policies, Now: time.Now, MaxArguments: 16 * 1024, Concurrency: NewConcurrency(8)}
}

func (g *Gate) approvals() ApprovalBackend {
	if g.Backend != nil {
		return g.Backend
	}
	return g.Approvals
}
func authorization(req mcp.Request) string {
	if extra := req.GetExtra(); extra != nil && extra.Header != nil {
		return extra.Header.Get("Authorization")
	}
	if params := req.GetParams(); params != nil {
		header, _ := params.GetMeta()[AuthorizationMeta].(string)
		return header
	}
	return ""
}
func rejection(code, id, approvalID string) *mcp.CallToolResult {
	meta := mcp.Meta{RequestIDMeta: id}
	fields := map[string]any{"code": code, "request_id": id}
	if approvalID != "" {
		fields["approval_id"] = approvalID
		meta[ApprovalMeta] = approvalID
	}
	return &mcp.CallToolResult{Meta: meta, IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: code + " (request " + id + ")"}}, StructuredContent: fields}
}
func (g *Gate) Middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
		// Protocol lifecycle notifications carry no tool authority and must return
		// no result. Every request, including discovery, is authenticated.
		if strings.HasPrefix(method, "notifications/") {
			return next(ctx, method, req)
		}
		identity, authenticated := g.Auth.Authenticate(authorization(req))
		if method != "tools/call" {
			if !authenticated {
				return nil, errors.New("unauthenticated")
			}
			result, err := next(ctx, method, req)
			if method == "tools/list" && err == nil {
				if listed, ok := result.(*mcp.ListToolsResult); ok {
					listed.Tools = g.filterTools(identity, listed.Tools)
				}
			}
			return result, err
		}
		started := g.Now()
		id := NewRequestID()
		event := Event{RequestID: id, Identity: "anonymous", Tool: "<unknown>", Decision: "denied", Reason: "unauthenticated"}
		defer func() {
			event.DurationMS = g.Now().Sub(started).Milliseconds()
			event.Phase = "result"
			if auditErr := g.Audit.Write(event); auditErr != nil {
				if event.Decision == "allowed" || event.Decision == "error" {
					event.Outcome = "unknown"
					result = rejection("outcome_unknown", id, event.ApprovalID)
					err = nil
					return
				}
				result = nil
				err = errors.New("audit_write_failed (request " + id + ")")
			}
		}()
		if !authenticated {
			return rejection("unauthenticated", id, ""), nil
		}
		event.Identity = identity.ID
		event.Role = identity.Role
		event.UserID = identity.UserID
		event.AgentID = identity.AgentID
		call, ok := req.(*mcp.CallToolRequest)
		if !ok || call.Params == nil {
			event.Reason = "invalid_request"
			return rejection(event.Reason, id, ""), nil
		}
		policy, known := g.Policies[call.Params.Name]
		if !known {
			event.Reason = "unknown_tool"
			result, err = next(ctx, method, req)
			if err != nil {
				err = &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("unknown tool (request %s)", id)}
			}
			return result, err
		}
		event.Tool = call.Params.Name
		event.DownstreamServer = policy.Server
		event.Risk = policy.Risk
		policy = g.restrict(policy, call.Params.Name)
		if policy.Risk == "disabled" || !Authorized(identity, policy) || !agentAllows(identity, call.Params.Name) {
			event.Reason = "rbac_deny"
			if identity.Tools != nil && !agentAllows(identity, call.Params.Name) && Authorized(identity, policy) && policy.Risk != "disabled" {
				event.Reason = "agent_scope_deny"
			}
			return rejection("unauthorized", id, ""), nil
		}
		if len(call.Params.Arguments) > g.maxArguments() {
			event.Reason = "invalid_arguments"
			return rejection(event.Reason, id, ""), nil
		}
		if _, err := canonicalArguments(call.Params.Arguments); err != nil && len(call.Params.Arguments) > 0 {
			event.Reason = "invalid_arguments"
			return rejection(event.Reason, id, ""), nil
		}
		if g.Shared != nil {
			allowed, limitErr := g.Shared.Allow(ctx, identity.ID, identity.AgentID, call.Params.Name, g.Now())
			if limitErr != nil {
				event.Reason = "limiter_unavailable"
				return rejection(event.Reason, id, ""), nil
			}
			if !allowed {
				event.Reason = "rate_limited"
				return rejection(event.Reason, id, ""), nil
			}
		} else if !g.Limiter.Allow(identity.ID, g.Now()) {
			event.Reason = "rate_limited"
			return rejection(event.Reason, id, ""), nil
		}
		if g.Concurrency != nil && !g.Concurrency.Acquire(identity.ID) {
			event.Reason = "concurrency_limited"
			return rejection(event.Reason, id, ""), nil
		}
		if g.Concurrency != nil {
			defer g.Concurrency.Release(identity.ID)
		}
		if policy.Risk == "high" {
			if !g.ApprovalEnabled {
				event.Reason = "approval_unavailable"
				return rejection(event.Reason, id, ""), nil
			}
			approvalID, _ := call.Params.Meta[ApprovalMeta].(string)
			if approvalID == "" {
				approvalID, err = g.approvals().Create(id, identity, call.Params.Name, call.Params.Arguments, g.Now())
				if err != nil {
					event.Reason = err.Error()
					return rejection(event.Reason, id, ""), nil
				}
				event.ApprovalID = approvalID
				event.Decision = "pending"
				event.Reason = "approval_required"
				event.Outcome = "pending"
				return rejection(event.Reason, id, approvalID), nil
			}
			// Never log an unverified client-provided approval identifier.
			reason := g.approvals().Consume(approvalID, identity, call.Params.Name, call.Params.Arguments, g.Now())
			if reason != "" {
				event.Reason = reason
				return rejection(reason, id, ""), nil
			}
			event.ApprovalID = approvalID
		}
		// Write an intent event before dispatch; audit failure prevents forwarding.
		intent := event
		intent.Decision = "forwarding"
		intent.Reason = "security_checks_passed"
		intent.Phase = "intent"
		if err := g.Audit.Write(intent); err != nil {
			event.Reason = "audit_write_failed"
			return rejection(event.Reason, id, ""), nil
		}
		result, err = next(ctx, method, req)
		event.Decision = "allowed"
		event.Reason = "rbac_allow"
		if event.ApprovalID != "" {
			event.Reason = "human_approved"
		}
		if err != nil {
			event.Decision = "error"
			event.Reason = "downstream_unavailable"
			event.Outcome = "downstream_error"
			return nil, fmt.Errorf("downstream unavailable (request %s)", id)
		}
		if toolResult, ok := result.(*mcp.CallToolResult); ok {
			if toolResult.Meta == nil {
				toolResult.Meta = mcp.Meta{}
			}
			toolResult.Meta[RequestIDMeta] = id
			if toolResult.IsError {
				event.Decision = "error"
				event.Reason = "tool_error"
				event.Outcome = "downstream_error"
			} else {
				event.Outcome = "success"
			}
		} else {
			event.Outcome = "success"
		}
		return result, nil
	}
}

func (g *Gate) maxArguments() int {
	if g.MaxArguments <= 0 {
		return 16 * 1024
	}
	return g.MaxArguments
}

func (g *Gate) restrict(policy Policy, tool string) Policy {
	if g.Restrictions == nil {
		return policy
	}
	disabled, roles, ok := g.Restrictions.Lookup(tool)
	if !ok {
		return policy
	}
	if disabled {
		policy.Risk = "disabled"
		return policy
	}
	if roles == nil {
		return policy
	}
	allowed := map[string]bool{}
	for _, role := range policy.Roles {
		allowed[role] = true
	}
	next := make([]string, 0, len(roles))
	for _, role := range roles {
		if allowed[role] {
			next = append(next, role)
		}
	}
	policy.Roles = next
	return policy
}

func agentAllows(identity Identity, tool string) bool {
	if identity.Tools == nil {
		return true
	}
	for _, name := range identity.Tools {
		if name == tool {
			return true
		}
	}
	return false
}

func (g *Gate) filterTools(identity Identity, tools []*mcp.Tool) []*mcp.Tool {
	filtered := make([]*mcp.Tool, 0, len(tools))
	for _, tool := range tools {
		policy, known := g.Policies[tool.Name]
		if !known {
			continue
		}
		policy = g.restrict(policy, tool.Name)
		if policy.Risk == "disabled" || !Authorized(identity, policy) || !agentAllows(identity, tool.Name) {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}
