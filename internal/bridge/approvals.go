package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

type ApprovalDB interface {
	CreateApproval(context.Context, store.Approval) error
	ListApprovals(context.Context, string) ([]store.Approval, error)
	DecideApproval(context.Context, string, string, string, time.Time) (store.Approval, error)
	ConsumeApproval(context.Context, string, string, string, string, string, string, time.Time) (string, error)
	FinishApproval(context.Context, string, string) error
	WriteAudit(context.Context, store.AuditEvent) error
	ListRestrictions(context.Context) ([]store.Restriction, error)
}

type Approvals struct{ DB ApprovalDB }

func (a Approvals) Create(id string, identity security.Identity, tool string, args json.RawMessage, now time.Time) (string, error) {
	canonical, err := security.CanonicalArguments(args)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	record := store.Approval{
		ID: id, RequestID: id, UserID: identity.ID, Role: identity.Role, AgentID: identity.AgentID, RunID: identity.RunID,
		Tool: tool, Arguments: canonical, Digest: hex.EncodeToString(sum[:]), Status: "pending", ExecutionOutcome: "not_run",
		ExpiresAt: now.Add(5 * time.Minute), CreatedAt: now.UTC(),
	}
	if err := a.DB.CreateApproval(context.Background(), record); err != nil {
		return "", err
	}
	return id, nil
}
func (a Approvals) List(now time.Time) []security.Approval {
	records, err := a.DB.ListApprovals(context.Background(), "")
	if err != nil {
		return nil
	}
	result := make([]security.Approval, 0, len(records))
	for _, record := range records {
		if record.Status == "pending" && !now.Before(record.ExpiresAt) {
			continue
		}
		result = append(result, toSecurity(record))
	}
	return result
}
func (a Approvals) Decide(id, decision string, now time.Time) (security.Approval, error) {
	return a.DecideBy(id, decision, "human-approver", now)
}
func (a Approvals) DecideBy(id, decision, operatorID string, now time.Time) (security.Approval, error) {
	record, err := a.DB.DecideApproval(context.Background(), id, decision, operatorID, now)
	if err != nil {
		if err == store.ErrNotFound {
			return security.Approval{}, errString("approval_not_found_or_expired")
		}
		return security.Approval{}, errString("approval_already_decided")
	}
	return toSecurity(record), nil
}
func (a Approvals) Consume(id string, identity security.Identity, tool string, args json.RawMessage, now time.Time) string {
	canonical, err := security.CanonicalArguments(args)
	if err != nil {
		return "invalid_arguments"
	}
	sum := sha256.Sum256(canonical)
	reason, err := a.DB.ConsumeApproval(context.Background(), id, identity.ID, identity.Role, identity.AgentID, tool, hex.EncodeToString(sum[:]), now)
	if err != nil {
		return "approval_not_found_or_expired"
	}
	return reason
}

func toSecurity(record store.Approval) security.Approval {
	return security.Approval{
		ID: record.ID, RequestID: record.RequestID, Identity: record.UserID, Role: record.Role, Tool: record.Tool,
		Arguments: append(json.RawMessage(nil), record.Arguments...), Status: record.Status, ExpiresAt: record.ExpiresAt,
		AgentID: record.AgentID, RunID: record.RunID, OperatorID: record.OperatorID, ExecutionOutcome: record.ExecutionOutcome,
	}
}

type errString string

func (e errString) Error() string { return string(e) }

type Restrictions struct{ DB ApprovalDB }

func (r Restrictions) Lookup(tool string) (bool, []string, bool) {
	items, err := r.DB.ListRestrictions(context.Background())
	if err != nil {
		return false, nil, false
	}
	for _, item := range items {
		if item.Tool == tool {
			return item.Disabled, append([]string(nil), item.Roles...), true
		}
	}
	return false, nil, false
}

func AuditSink(db ApprovalDB) func(security.Event) error {
	return func(event security.Event) error {
		userID := event.UserID
		if userID == "" {
			userID = event.Identity
		}
		return db.WriteAudit(context.Background(), store.AuditEvent{
			RequestID: event.RequestID, OccurredAt: time.Now().UTC(), UserID: userID, Role: event.Role, AgentID: event.AgentID,
			Tool: event.Tool, Service: event.DownstreamServer, Decision: event.Decision, Reason: event.Reason, Risk: event.Risk,
			Phase: event.Phase, ApprovalID: event.ApprovalID, DurationMS: event.DurationMS, Outcome: event.Outcome,
		})
	}
}
