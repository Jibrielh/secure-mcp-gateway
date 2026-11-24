package store

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"
)

type Memory struct {
	mu            sync.Mutex
	users         map[string]User
	bySubject     map[string]string
	byUsername    map[string]string
	sessions      map[string]Session
	agents        map[string]Agent
	versions      map[string][]json.RawMessage
	integrations  map[string]Integration
	conversations map[string]Conversation
	messages      map[string][]Message
	runs          map[string]Run
	events        map[string][]RunEvent
	calls         map[string][]ToolCall
	approvals     map[string]Approval
	audits        []AuditEvent
	nextAudit     int64
	findings      map[string]Finding
	restrictions  map[string]Restriction
}

func NewMemory() *Memory {
	return &Memory{
		users: map[string]User{}, bySubject: map[string]string{}, byUsername: map[string]string{},
		sessions: map[string]Session{}, agents: map[string]Agent{}, versions: map[string][]json.RawMessage{},
		integrations: map[string]Integration{}, conversations: map[string]Conversation{}, messages: map[string][]Message{},
		runs: map[string]Run{}, events: map[string][]RunEvent{}, calls: map[string][]ToolCall{},
		approvals: map[string]Approval{}, findings: map[string]Finding{}, restrictions: map[string]Restriction{},
	}
}

func (m *Memory) Ping(context.Context) error { return nil }
func (m *Memory) Close()                     {}

func (m *Memory) UpsertUser(_ context.Context, user User) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	m.users[user.ID] = user
	m.bySubject[user.Subject] = user.ID
	m.byUsername[user.Username] = user.ID
	return nil
}
func (m *Memory) FindUser(_ context.Context, id string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	user, ok := m.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return user, nil
}
func (m *Memory) FindUserBySubject(_ context.Context, subject string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.bySubject[subject]
	if !ok {
		return User{}, ErrNotFound
	}
	return m.users[id], nil
}
func (m *Memory) FindUserByUsername(_ context.Context, username string) (User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byUsername[username]
	if !ok {
		return User{}, ErrNotFound
	}
	return m.users[id], nil
}

func (m *Memory) CreateSession(_ context.Context, session Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[session.ID] = session
	return nil
}
func (m *Memory) FindSession(_ context.Context, id string) (Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok || !time.Now().Before(session.ExpiresAt) {
		return Session{}, ErrNotFound
	}
	return session, nil
}
func (m *Memory) DeleteSession(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
	return nil
}

func (m *Memory) CreateAgent(_ context.Context, agent Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.agents[agent.ID]; ok {
		return ErrConflict
	}
	agent.Tools = append([]string(nil), agent.Tools...)
	m.agents[agent.ID] = agent
	encoded, _ := json.Marshal(agent)
	m.versions[agent.ID] = []json.RawMessage{encoded}
	return nil
}
func (m *Memory) UpdateAgent(_ context.Context, agent Agent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.agents[agent.ID]
	if !ok {
		return ErrNotFound
	}
	if current.OwnerUserID != agent.OwnerUserID {
		return ErrNotFound
	}
	agent.Version = current.Version + 1
	agent.Tools = append([]string(nil), agent.Tools...)
	m.agents[agent.ID] = agent
	encoded, _ := json.Marshal(agent)
	m.versions[agent.ID] = append(m.versions[agent.ID], encoded)
	return nil
}
func (m *Memory) GetAgent(_ context.Context, id string) (Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[id]
	if !ok {
		return Agent{}, ErrNotFound
	}
	agent.Tools = append([]string(nil), agent.Tools...)
	return agent, nil
}
func (m *Memory) ListAgents(_ context.Context, ownerID string) ([]Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []Agent
	for _, agent := range m.agents {
		if ownerID == "" || agent.OwnerUserID == ownerID {
			agent.Tools = append([]string(nil), agent.Tools...)
			result = append(result, agent)
		}
	}
	slices.SortFunc(result, func(a, b Agent) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return result, nil
}

func (m *Memory) SaveIntegration(_ context.Context, item Integration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.integrations[item.ID] = item
	return nil
}
func (m *Memory) ListIntegrations(_ context.Context) ([]Integration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Integration, 0, len(m.integrations))
	for _, item := range m.integrations {
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b Integration) int {
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return result, nil
}

func (m *Memory) CreateConversation(_ context.Context, conversation Conversation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conversations[conversation.ID] = conversation
	return nil
}
func (m *Memory) RenameConversation(_ context.Context, id, owner, title string, updated time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	conversation, ok := m.conversations[id]
	if !ok || conversation.OwnerUserID != owner {
		return ErrNotFound
	}
	conversation.Title = title
	conversation.UpdatedAt = updated
	m.conversations[id] = conversation
	return nil
}
func (m *Memory) ListConversations(_ context.Context, ownerID string) ([]Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []Conversation
	for _, conversation := range m.conversations {
		if conversation.OwnerUserID == ownerID {
			result = append(result, conversation)
		}
	}
	slices.SortFunc(result, func(a, b Conversation) int { return b.UpdatedAt.Compare(a.UpdatedAt) })
	return result, nil
}
func (m *Memory) GetConversation(_ context.Context, id string) (Conversation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	conversation, ok := m.conversations[id]
	if !ok {
		return Conversation{}, ErrNotFound
	}
	return conversation, nil
}
func (m *Memory) DeleteConversation(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.conversations, id)
	delete(m.messages, id)
	for runID, run := range m.runs {
		if run.ConversationID == id {
			delete(m.runs, runID)
			delete(m.events, runID)
			delete(m.calls, runID)
		}
	}
	return nil
}

func (m *Memory) AddMessage(_ context.Context, message Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.conversations[message.ConversationID]; !ok {
		return ErrNotFound
	}
	message.Seq = int64(len(m.messages[message.ConversationID]) + 1)
	m.messages[message.ConversationID] = append(m.messages[message.ConversationID], message)
	conversation := m.conversations[message.ConversationID]
	conversation.UpdatedAt = message.CreatedAt
	m.conversations[message.ConversationID] = conversation
	return nil
}
func (m *Memory) ListMessages(_ context.Context, conversationID string) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Message(nil), m.messages[conversationID]...), nil
}

func (m *Memory) CreateRun(_ context.Context, run Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(run.ProviderState) == 0 {
		run.ProviderState = json.RawMessage(`{}`)
	}
	m.runs[run.ID] = run
	return nil
}
func (m *Memory) UpdateRun(_ context.Context, run Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[run.ID]; !ok {
		return ErrNotFound
	}
	if len(run.ProviderState) == 0 {
		run.ProviderState = json.RawMessage(`{}`)
	}
	m.runs[run.ID] = run
	return nil
}
func (m *Memory) GetRun(_ context.Context, id string) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, ok := m.runs[id]
	if !ok {
		return Run{}, ErrNotFound
	}
	run.ProviderState = append(json.RawMessage(nil), run.ProviderState...)
	return run, nil
}
func (m *Memory) ListRuns(_ context.Context, ownerID, agentID string) ([]Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []Run
	for _, run := range m.runs {
		if (ownerID == "" || run.OwnerUserID == ownerID) && (agentID == "" || run.AgentID == agentID) {
			run.ProviderState = json.RawMessage(`{}`)
			result = append(result, run)
		}
	}
	slices.SortFunc(result, func(a, b Run) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return result, nil
}

func (m *Memory) AppendEvent(_ context.Context, event RunEvent) (RunEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.runs[event.RunID]; !ok {
		return RunEvent{}, ErrNotFound
	}
	event.Seq = int64(len(m.events[event.RunID]) + 1)
	if event.ID == "" {
		event.ID = NewID()
	}
	m.events[event.RunID] = append(m.events[event.RunID], event)
	return event, nil
}
func (m *Memory) ListEvents(_ context.Context, runID string, afterSeq int64) ([]RunEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []RunEvent
	for _, event := range m.events[runID] {
		if event.Seq > afterSeq {
			event.Payload = append(json.RawMessage(nil), event.Payload...)
			result = append(result, event)
		}
	}
	return result, nil
}

func (m *Memory) SaveToolCall(_ context.Context, call ToolCall) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	calls := m.calls[call.RunID]
	for i := range calls {
		if calls[i].ID == call.ID {
			m.calls[call.RunID][i] = call
			return nil
		}
	}
	m.calls[call.RunID] = append(calls, call)
	return nil
}
func (m *Memory) ListToolCalls(_ context.Context, runID string) ([]ToolCall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]ToolCall(nil), m.calls[runID]...), nil
}

func (m *Memory) CreateApproval(_ context.Context, approval Approval) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.approvals[approval.ID]; ok {
		return ErrConflict
	}
	approval.Arguments = append(json.RawMessage(nil), approval.Arguments...)
	m.approvals[approval.ID] = approval
	return nil
}
func (m *Memory) ListApprovals(_ context.Context, status string) ([]Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var result []Approval
	for id, approval := range m.approvals {
		if approval.Status == "pending" && !now.Before(approval.ExpiresAt) {
			approval.Status = "expired"
			m.approvals[id] = approval
		}
		if status == "" || approval.Status == status {
			approval.Arguments = append(json.RawMessage(nil), approval.Arguments...)
			result = append(result, approval)
		}
	}
	slices.SortFunc(result, func(a, b Approval) int {
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return result, nil
}
func (m *Memory) GetApproval(_ context.Context, id string) (Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	approval, ok := m.approvals[id]
	if !ok {
		return Approval{}, ErrNotFound
	}
	if approval.Status == "pending" && !time.Now().UTC().Before(approval.ExpiresAt) {
		approval.Status = "expired"
		m.approvals[id] = approval
	}
	approval.Arguments = append(json.RawMessage(nil), approval.Arguments...)
	return approval, nil
}
func (m *Memory) DecideApproval(_ context.Context, id, decision, operatorID string, now time.Time) (Approval, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	approval, ok := m.approvals[id]
	if !ok || (approval.Status == "pending" && !now.Before(approval.ExpiresAt)) {
		if ok {
			approval.Status = "expired"
			m.approvals[id] = approval
		}
		return Approval{}, ErrNotFound
	}
	if approval.UserID == operatorID {
		return Approval{}, ErrConflict
	}
	if approval.Status != "pending" {
		return Approval{}, ErrConflict
	}
	switch decision {
	case "approve":
		approval.Status = "approved"
	case "deny":
		approval.Status = "denied"
		approval.ExecutionOutcome = "not_run"
	default:
		return Approval{}, ErrConflict
	}
	decided := now.UTC()
	approval.DecidedAt = &decided
	approval.OperatorID = operatorID
	m.approvals[id] = approval
	approval.Arguments = append(json.RawMessage(nil), approval.Arguments...)
	return approval, nil
}
func (m *Memory) ConsumeApproval(_ context.Context, id, userID, role, agentID, tool, digest string, now time.Time) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	approval, ok := m.approvals[id]
	if !ok || !now.Before(approval.ExpiresAt) {
		return "approval_not_found_or_expired", nil
	}
	if approval.UserID != userID || approval.Role != role || approval.Tool != tool || approval.Digest != digest || (approval.AgentID != "" && approval.AgentID != agentID) {
		return "approval_mismatch", nil
	}
	switch approval.Status {
	case "pending":
		return "approval_required", nil
	case "denied":
		return "approval_denied", nil
	case "approved":
		approval.Status = "consumed"
		approval.ExecutionOutcome = "started"
		m.approvals[id] = approval
		return "", nil
	default:
		return "approval_consumed", nil
	}
}
func (m *Memory) FinishApproval(_ context.Context, id, outcome string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	approval, ok := m.approvals[id]
	if !ok {
		return ErrNotFound
	}
	approval.ExecutionOutcome = outcome
	m.approvals[id] = approval
	return nil
}

func (m *Memory) WriteAudit(_ context.Context, event AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextAudit++
	event.ID = m.nextAudit
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	m.audits = append(m.audits, event)
	return nil
}
func (m *Memory) QueryAudit(_ context.Context, query AuditQuery) (AuditPage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var matched []AuditEvent
	for _, event := range m.audits {
		if event.ID <= query.AfterID {
			continue
		}
		if !query.AdminAll && query.UserID != "" && event.UserID != query.UserID {
			continue
		}
		if query.AgentID != "" && event.AgentID != query.AgentID {
			continue
		}
		if query.Tool != "" && event.Tool != query.Tool {
			continue
		}
		if query.Risk != "" && event.Risk != query.Risk {
			continue
		}
		if query.Outcome != "" && Classify(event) != query.Outcome && event.Outcome != query.Outcome {
			continue
		}
		if !query.From.IsZero() && event.OccurredAt.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && !event.OccurredAt.Before(query.To) {
			continue
		}
		matched = append(matched, event)
	}
	if len(matched) > limit {
		matched = matched[:limit]
	}
	page := AuditPage{Events: matched}
	if len(matched) == limit {
		page.NextID = matched[len(matched)-1].ID
	}
	return page, nil
}
func (m *Memory) Analytics(_ context.Context, query AnalyticsQuery) (Analytics, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var events []AuditEvent
	pending := 0
	now := time.Now().UTC()
	for _, approval := range m.approvals {
		if approval.Status == "pending" && now.Before(approval.ExpiresAt) && (query.AdminAll || approval.UserID == query.UserID) {
			pending++
		}
	}
	for _, event := range m.audits {
		if !query.AdminAll && event.UserID != query.UserID {
			continue
		}
		if !query.From.IsZero() && event.OccurredAt.Before(query.From) {
			continue
		}
		if !query.To.IsZero() && !event.OccurredAt.Before(query.To) {
			continue
		}
		events = append(events, event)
	}
	return Aggregate(events, query.From, query.To, pending), nil
}

func (m *Memory) SaveFinding(_ context.Context, finding Finding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.findings {
		if existing.DedupeKey == finding.DedupeKey {
			return nil
		}
	}
	m.findings[finding.ID] = finding
	return nil
}
func (m *Memory) ListFindings(context.Context) ([]Finding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Finding, 0, len(m.findings))
	for _, finding := range m.findings {
		result = append(result, finding)
	}
	slices.SortFunc(result, func(a, b Finding) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return result, nil
}
func (m *Memory) UpdateFinding(_ context.Context, id, status string) (Finding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	finding, ok := m.findings[id]
	if !ok {
		return Finding{}, ErrNotFound
	}
	if status != "acknowledged" && status != "dismissed" && status != "open" {
		return Finding{}, ErrConflict
	}
	finding.Status = status
	m.findings[id] = finding
	return finding, nil
}
func (m *Memory) SetFindingExplanation(_ context.Context, id, explanation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	finding, ok := m.findings[id]
	if !ok {
		return ErrNotFound
	}
	finding.Explanation = explanation
	m.findings[id] = finding
	return nil
}

func (m *Memory) ListRestrictions(context.Context) ([]Restriction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Restriction, 0, len(m.restrictions))
	for _, item := range m.restrictions {
		item.Roles = append([]string(nil), item.Roles...)
		result = append(result, item)
	}
	return result, nil
}
func (m *Memory) SaveRestriction(_ context.Context, item Restriction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item.Roles = append([]string(nil), item.Roles...)
	m.restrictions[item.Tool] = item
	return nil
}
