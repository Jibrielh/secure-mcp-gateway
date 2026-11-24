package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")

func NewID() string {
	var raw [16]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

type User struct {
	ID          string    `json:"id"`
	Subject     string    `json:"subject"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
}

type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	CSRFToken string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

type Agent struct {
	ID           string    `json:"id"`
	OwnerUserID  string    `json:"owner_user_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt"`
	Model        string    `json:"model"`
	Tools        []string  `json:"tools"`
	MaxSteps     int       `json:"max_steps"`
	Enabled      bool      `json:"enabled"`
	Version      int       `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Integration struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	Status      string    `json:"status"`
	Detail      string    `json:"detail"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Conversation struct {
	ID          string    `json:"id"`
	OwnerUserID string    `json:"owner_user_id"`
	AgentID     string    `json:"agent_id"`
	Title       string    `json:"title"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Body           string    `json:"body"`
	Seq            int64     `json:"seq"`
	CreatedAt      time.Time `json:"created_at"`
}

type Run struct {
	ID             string          `json:"id"`
	ConversationID string          `json:"conversation_id"`
	OwnerUserID    string          `json:"owner_user_id"`
	AgentID        string          `json:"agent_id"`
	Status         string          `json:"status"`
	ProviderState  json.RawMessage `json:"provider_state"`
	Error          string          `json:"error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type RunEvent struct {
	ID        string          `json:"id"`
	RunID     string          `json:"run_id"`
	Seq       int64           `json:"seq"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

type ToolCall struct {
	ID              string          `json:"id"`
	RunID           string          `json:"run_id"`
	RequestID       string          `json:"request_id"`
	ToolName        string          `json:"tool_name"`
	ArgumentsDigest string          `json:"arguments_digest"`
	Arguments       json.RawMessage `json:"arguments"`
	Status          string          `json:"status"`
	ResultText      string          `json:"result_text"`
	ApprovalID      string          `json:"approval_id,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type Approval struct {
	ID               string          `json:"approval_id"`
	RequestID        string          `json:"request_id"`
	UserID           string          `json:"identity"`
	Role             string          `json:"role"`
	AgentID          string          `json:"agent_id,omitempty"`
	RunID            string          `json:"run_id,omitempty"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Digest           string          `json:"-"`
	Status           string          `json:"status"`
	ExecutionOutcome string          `json:"execution_outcome"`
	OperatorID       string          `json:"operator_id,omitempty"`
	ExpiresAt        time.Time       `json:"expires_at"`
	CreatedAt        time.Time       `json:"created_at"`
	DecidedAt        *time.Time      `json:"decided_at,omitempty"`
}

type AuditEvent struct {
	ID         int64     `json:"id"`
	RequestID  string    `json:"request_id"`
	OccurredAt time.Time `json:"occurred_at"`
	UserID     string    `json:"user_id"`
	Role       string    `json:"role"`
	AgentID    string    `json:"agent_id"`
	OperatorID string    `json:"operator_id"`
	Tool       string    `json:"tool"`
	Service    string    `json:"service"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason"`
	Risk       string    `json:"risk"`
	Phase      string    `json:"phase"`
	ApprovalID string    `json:"approval_id"`
	DurationMS int64     `json:"duration_ms"`
	Outcome    string    `json:"outcome"`
}

type AuditQuery struct {
	UserID   string
	AgentID  string
	Tool     string
	Outcome  string
	Risk     string
	From     time.Time
	To       time.Time
	AfterID  int64
	Limit    int
	AdminAll bool
}

type AuditPage struct {
	Events []AuditEvent `json:"events"`
	NextID int64        `json:"next_id,omitempty"`
}

type AnalyticsQuery struct {
	UserID   string
	From     time.Time
	To       time.Time
	AdminAll bool
}

type Analytics struct {
	From             time.Time   `json:"from"`
	To               time.Time   `json:"to"`
	Latency          string      `json:"latency"`
	Total            int         `json:"total"`
	Success          int         `json:"success"`
	Denied           int         `json:"denied"`
	RateLimited      int         `json:"rate_limited"`
	Pending          int         `json:"pending"`
	DownstreamError  int         `json:"downstream_error"`
	Unknown          int         `json:"unknown"`
	SuccessRate      *float64    `json:"success_rate"`
	P50MS            *float64    `json:"p50_ms"`
	P95MS            *float64    `json:"p95_ms"`
	P99MS            *float64    `json:"p99_ms"`
	Series           []Series    `json:"series"`
	Tools            []ToolCount `json:"tools"`
	PendingApprovals int         `json:"pending_approvals"`
}

type Series struct {
	Bucket time.Time `json:"bucket"`
	Count  int       `json:"count"`
}

type ToolCount struct {
	Tool  string `json:"tool"`
	Count int    `json:"count"`
}

type Finding struct {
	ID          string          `json:"id"`
	Rule        string          `json:"rule"`
	RuleVersion string          `json:"rule_version"`
	Severity    string          `json:"severity"`
	Status      string          `json:"status"`
	Summary     string          `json:"summary"`
	Evidence    json.RawMessage `json:"evidence"`
	DedupeKey   string          `json:"dedupe_key"`
	Explanation string          `json:"explanation"`
	WindowStart time.Time       `json:"window_start"`
	WindowEnd   time.Time       `json:"window_end"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Restriction struct {
	Tool      string    `json:"tool"`
	Disabled  bool      `json:"disabled"`
	Roles     []string  `json:"roles"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}
