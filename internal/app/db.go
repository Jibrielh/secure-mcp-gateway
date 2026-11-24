package app

import (
	"context"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

// DB is the persistence surface used by the workspace. Memory and Postgres both implement it.
type DB interface {
	Ping(context.Context) error
	UpsertUser(context.Context, store.User) error
	FindUser(context.Context, string) (store.User, error)
	FindUserBySubject(context.Context, string) (store.User, error)
	FindUserByUsername(context.Context, string) (store.User, error)
	CreateSession(context.Context, store.Session) error
	FindSession(context.Context, string) (store.Session, error)
	DeleteSession(context.Context, string) error
	CreateAgent(context.Context, store.Agent) error
	UpdateAgent(context.Context, store.Agent) error
	GetAgent(context.Context, string) (store.Agent, error)
	ListAgents(context.Context, string) ([]store.Agent, error)
	SaveIntegration(context.Context, store.Integration) error
	ListIntegrations(context.Context) ([]store.Integration, error)
	CreateConversation(context.Context, store.Conversation) error
	RenameConversation(context.Context, string, string, string, time.Time) error
	ListConversations(context.Context, string) ([]store.Conversation, error)
	GetConversation(context.Context, string) (store.Conversation, error)
	DeleteConversation(context.Context, string) error
	AddMessage(context.Context, store.Message) error
	ListMessages(context.Context, string) ([]store.Message, error)
	CreateRun(context.Context, store.Run) error
	UpdateRun(context.Context, store.Run) error
	GetRun(context.Context, string) (store.Run, error)
	ListRuns(context.Context, string, string) ([]store.Run, error)
	AppendEvent(context.Context, store.RunEvent) (store.RunEvent, error)
	ListEvents(context.Context, string, int64) ([]store.RunEvent, error)
	SaveToolCall(context.Context, store.ToolCall) error
	QueryAudit(context.Context, store.AuditQuery) (store.AuditPage, error)
	Analytics(context.Context, store.AnalyticsQuery) (store.Analytics, error)
	SaveFinding(context.Context, store.Finding) error
	ListFindings(context.Context) ([]store.Finding, error)
	UpdateFinding(context.Context, string, string) (store.Finding, error)
	SetFindingExplanation(context.Context, string, string) error
	ListRestrictions(context.Context) ([]store.Restriction, error)
	SaveRestriction(context.Context, store.Restriction) error
	ListApprovals(context.Context, string) ([]store.Approval, error)
	GetApproval(context.Context, string) (store.Approval, error)
	DecideApproval(context.Context, string, string, string, time.Time) (store.Approval, error)
	WriteAudit(context.Context, store.AuditEvent) error
}

var (
	_ DB = (*store.Memory)(nil)
	_ DB = (*store.Postgres)(nil)
)

func devUsers(now time.Time) []store.User {
	return []store.User{
		{ID: "user-ada", Subject: "dev:ada", Username: "ada", DisplayName: "Ada Developer", Role: "developer", CreatedAt: now},
		{ID: "user-owen", Subject: "dev:owen", Username: "owen", DisplayName: "Owen Operator", Role: "approver", CreatedAt: now},
		{ID: "user-vera", Subject: "dev:vera", Username: "vera", DisplayName: "Vera Viewer", Role: "viewer", CreatedAt: now},
		{ID: "user-amy", Subject: "dev:amy", Username: "amy", DisplayName: "Amy Admin", Role: "admin", CreatedAt: now},
	}
}
