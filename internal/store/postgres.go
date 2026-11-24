package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Postgres struct{ pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	store := &Postgres{pool: pool}
	if err := store.Migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (p *Postgres) Close()                         { p.pool.Close() }
func (p *Postgres) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

func (p *Postgres) Migrate(ctx context.Context) error {
	if _, err := p.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		var applied bool
		if err := p.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, entry.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, now())`, entry.Name()); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) UpsertUser(ctx context.Context, user User) error {
	if user.CreatedAt.IsZero() {
		user.CreatedAt = time.Now().UTC()
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO users (id, subject, username, display_name, role, created_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (id) DO UPDATE SET display_name=EXCLUDED.display_name, role=EXCLUDED.role, username=EXCLUDED.username, subject=EXCLUDED.subject`,
		user.ID, user.Subject, user.Username, user.DisplayName, user.Role, user.CreatedAt.UTC())
	return err
}
func (p *Postgres) FindUser(ctx context.Context, id string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT id, subject, username, display_name, role, created_at FROM users WHERE id=$1`, id))
}
func (p *Postgres) FindUserBySubject(ctx context.Context, subject string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT id, subject, username, display_name, role, created_at FROM users WHERE subject=$1`, subject))
}
func (p *Postgres) FindUserByUsername(ctx context.Context, username string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT id, subject, username, display_name, role, created_at FROM users WHERE username=$1`, username))
}
func scanUser(row pgx.Row) (User, error) {
	var user User
	err := row.Scan(&user.ID, &user.Subject, &user.Username, &user.DisplayName, &user.Role, &user.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (p *Postgres) CreateSession(ctx context.Context, session Session) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO sessions (id, user_id, csrf_token, expires_at, created_at) VALUES ($1,$2,$3,$4,$5)`,
		session.ID, session.UserID, session.CSRFToken, session.ExpiresAt.UTC(), session.CreatedAt.UTC())
	return err
}
func (p *Postgres) FindSession(ctx context.Context, id string) (Session, error) {
	var session Session
	err := p.pool.QueryRow(ctx, `SELECT id, user_id, csrf_token, expires_at, created_at FROM sessions WHERE id=$1 AND expires_at > now()`, id).
		Scan(&session.ID, &session.UserID, &session.CSRFToken, &session.ExpiresAt, &session.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	return session, err
}
func (p *Postgres) DeleteSession(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	return err
}

func (p *Postgres) CreateAgent(ctx context.Context, agent Agent) error {
	return p.insertAgent(ctx, agent, false)
}
func (p *Postgres) UpdateAgent(ctx context.Context, agent Agent) error {
	current, err := p.GetAgent(ctx, agent.ID)
	if err != nil {
		return err
	}
	if current.OwnerUserID != agent.OwnerUserID {
		return ErrNotFound
	}
	agent.Version = current.Version + 1
	tag, err := p.pool.Exec(ctx, `UPDATE agents SET name=$2, description=$3, system_prompt=$4, model=$5, tools=$6, max_steps=$7, enabled=$8, version=$9, updated_at=$10
		WHERE id=$1 AND owner_user_id=$11`,
		agent.ID, agent.Name, agent.Description, agent.SystemPrompt, agent.Model, mustJSON(agent.Tools), agent.MaxSteps, agent.Enabled, agent.Version, agent.UpdatedAt.UTC(), agent.OwnerUserID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO agent_versions (agent_id, version, config, created_at) VALUES ($1,$2,$3,$4)`,
		agent.ID, agent.Version, mustJSON(agent), agent.UpdatedAt.UTC())
	return err
}
func (p *Postgres) insertAgent(ctx context.Context, agent Agent, overwrite bool) error {
	if agent.Version == 0 {
		agent.Version = 1
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO agents (id, owner_user_id, name, description, system_prompt, model, tools, max_steps, enabled, version, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		agent.ID, agent.OwnerUserID, agent.Name, agent.Description, agent.SystemPrompt, agent.Model, mustJSON(agent.Tools), agent.MaxSteps, agent.Enabled, agent.Version, agent.CreatedAt.UTC(), agent.UpdatedAt.UTC())
	if err != nil {
		if overwrite {
			return err
		}
		return err
	}
	_, err = p.pool.Exec(ctx, `INSERT INTO agent_versions (agent_id, version, config, created_at) VALUES ($1,$2,$3,$4)`, agent.ID, agent.Version, mustJSON(agent), agent.CreatedAt.UTC())
	return err
}
func (p *Postgres) GetAgent(ctx context.Context, id string) (Agent, error) {
	return scanAgent(p.pool.QueryRow(ctx, `SELECT id, owner_user_id, name, description, system_prompt, model, tools, max_steps, enabled, version, created_at, updated_at FROM agents WHERE id=$1`, id))
}
func (p *Postgres) ListAgents(ctx context.Context, ownerID string) ([]Agent, error) {
	query := `SELECT id, owner_user_id, name, description, system_prompt, model, tools, max_steps, enabled, version, created_at, updated_at FROM agents`
	args := []any{}
	if ownerID != "" {
		query += ` WHERE owner_user_id=$1`
		args = append(args, ownerID)
	}
	query += ` ORDER BY name`
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Agent
	for rows.Next() {
		agent, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, agent)
	}
	return result, rows.Err()
}
func scanAgent(row pgx.Row) (Agent, error) {
	var agent Agent
	var tools []byte
	err := row.Scan(&agent.ID, &agent.OwnerUserID, &agent.Name, &agent.Description, &agent.SystemPrompt, &agent.Model, &tools, &agent.MaxSteps, &agent.Enabled, &agent.Version, &agent.CreatedAt, &agent.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if len(tools) > 0 {
		if err := json.Unmarshal(tools, &agent.Tools); err != nil {
			return Agent{}, err
		}
	}
	return agent, nil
}

func (p *Postgres) SaveIntegration(ctx context.Context, item Integration) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO integrations (id, kind, display_name, enabled, status, detail, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (id) DO UPDATE SET enabled=EXCLUDED.enabled, status=EXCLUDED.status, detail=EXCLUDED.detail, updated_at=EXCLUDED.updated_at`,
		item.ID, item.Kind, item.DisplayName, item.Enabled, item.Status, item.Detail, item.UpdatedAt.UTC())
	return err
}
func (p *Postgres) ListIntegrations(ctx context.Context) ([]Integration, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, kind, display_name, enabled, status, detail, updated_at FROM integrations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Integration
	for rows.Next() {
		var item Integration
		if err := rows.Scan(&item.ID, &item.Kind, &item.DisplayName, &item.Enabled, &item.Status, &item.Detail, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (p *Postgres) CreateConversation(ctx context.Context, conversation Conversation) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO conversations (id, owner_user_id, agent_id, title, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6)`,
		conversation.ID, conversation.OwnerUserID, conversation.AgentID, conversation.Title, conversation.CreatedAt.UTC(), conversation.UpdatedAt.UTC())
	return err
}
func (p *Postgres) RenameConversation(ctx context.Context, id, owner, title string, updated time.Time) error {
	tag, err := p.pool.Exec(ctx, `UPDATE conversations SET title=$1, updated_at=$2 WHERE id=$3 AND owner_user_id=$4`, title, updated.UTC(), id, owner)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (p *Postgres) ListConversations(ctx context.Context, ownerID string) ([]Conversation, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, owner_user_id, agent_id, title, created_at, updated_at FROM conversations WHERE owner_user_id=$1 ORDER BY updated_at DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectConversations(rows)
}
func (p *Postgres) GetConversation(ctx context.Context, id string) (Conversation, error) {
	var conversation Conversation
	err := p.pool.QueryRow(ctx, `SELECT id, owner_user_id, agent_id, title, created_at, updated_at FROM conversations WHERE id=$1`, id).
		Scan(&conversation.ID, &conversation.OwnerUserID, &conversation.AgentID, &conversation.Title, &conversation.CreatedAt, &conversation.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Conversation{}, ErrNotFound
	}
	return conversation, err
}
func (p *Postgres) DeleteConversation(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM conversations WHERE id=$1`, id)
	return err
}
func collectConversations(rows pgx.Rows) ([]Conversation, error) {
	defer rows.Close()
	var result []Conversation
	for rows.Next() {
		var conversation Conversation
		if err := rows.Scan(&conversation.ID, &conversation.OwnerUserID, &conversation.AgentID, &conversation.Title, &conversation.CreatedAt, &conversation.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, conversation)
	}
	return result, rows.Err()
}

func (p *Postgres) AddMessage(ctx context.Context, message Message) error {
	err := p.pool.QueryRow(ctx, `INSERT INTO messages (id, conversation_id, role, body, seq, created_at)
		VALUES ($1,$2,$3,$4, COALESCE((SELECT MAX(seq) FROM messages WHERE conversation_id=$2), 0)+1, $5)
		RETURNING seq`, message.ID, message.ConversationID, message.Role, message.Body, message.CreatedAt.UTC()).Scan(&message.Seq)
	if err != nil {
		return err
	}
	_, err = p.pool.Exec(ctx, `UPDATE conversations SET updated_at=$2 WHERE id=$1`, message.ConversationID, message.CreatedAt.UTC())
	return err
}
func (p *Postgres) ListMessages(ctx context.Context, conversationID string) ([]Message, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, conversation_id, role, body, seq, created_at FROM messages WHERE conversation_id=$1 ORDER BY seq`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Message
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.ConversationID, &message.Role, &message.Body, &message.Seq, &message.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, message)
	}
	return result, rows.Err()
}

func (p *Postgres) CreateRun(ctx context.Context, run Run) error {
	if len(run.ProviderState) == 0 {
		run.ProviderState = json.RawMessage(`{}`)
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO runs (id, conversation_id, owner_user_id, agent_id, status, provider_state, error, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, run.ID, run.ConversationID, run.OwnerUserID, run.AgentID, run.Status, []byte(run.ProviderState), run.Error, run.CreatedAt.UTC(), run.UpdatedAt.UTC())
	return err
}
func (p *Postgres) UpdateRun(ctx context.Context, run Run) error {
	if len(run.ProviderState) == 0 {
		run.ProviderState = json.RawMessage(`{}`)
	}
	tag, err := p.pool.Exec(ctx, `UPDATE runs SET status=$2, provider_state=$3, error=$4, updated_at=$5 WHERE id=$1`,
		run.ID, run.Status, []byte(run.ProviderState), run.Error, run.UpdatedAt.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (p *Postgres) GetRun(ctx context.Context, id string) (Run, error) {
	var run Run
	var state []byte
	err := p.pool.QueryRow(ctx, `SELECT id, conversation_id, owner_user_id, agent_id, status, provider_state, error, created_at, updated_at FROM runs WHERE id=$1`, id).
		Scan(&run.ID, &run.ConversationID, &run.OwnerUserID, &run.AgentID, &run.Status, &state, &run.Error, &run.CreatedAt, &run.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	run.ProviderState = state
	return run, err
}
func (p *Postgres) ListRuns(ctx context.Context, ownerID, agentID string) ([]Run, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, conversation_id, owner_user_id, agent_id, status, '{}'::jsonb, error, created_at, updated_at FROM runs
		WHERE ($1='' OR owner_user_id=$1) AND ($2='' OR agent_id=$2) ORDER BY created_at DESC`, ownerID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Run
	for rows.Next() {
		var run Run
		var state []byte
		if err := rows.Scan(&run.ID, &run.ConversationID, &run.OwnerUserID, &run.AgentID, &run.Status, &state, &run.Error, &run.CreatedAt, &run.UpdatedAt); err != nil {
			return nil, err
		}
		run.ProviderState = state
		result = append(result, run)
	}
	return result, rows.Err()
}

func (p *Postgres) AppendEvent(ctx context.Context, event RunEvent) (RunEvent, error) {
	if event.ID == "" {
		event.ID = NewID()
	}
	err := p.pool.QueryRow(ctx, `INSERT INTO run_events (id, run_id, seq, event_type, payload, created_at)
		VALUES ($1,$2, COALESCE((SELECT MAX(seq) FROM run_events WHERE run_id=$2),0)+1, $3, $4, $5) RETURNING seq`,
		event.ID, event.RunID, event.Type, []byte(event.Payload), event.CreatedAt.UTC()).Scan(&event.Seq)
	return event, err
}
func (p *Postgres) ListEvents(ctx context.Context, runID string, afterSeq int64) ([]RunEvent, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, run_id, seq, event_type, payload, created_at FROM run_events WHERE run_id=$1 AND seq>$2 ORDER BY seq`, runID, afterSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []RunEvent
	for rows.Next() {
		var event RunEvent
		var payload []byte
		if err := rows.Scan(&event.ID, &event.RunID, &event.Seq, &event.Type, &payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Payload = payload
		result = append(result, event)
	}
	return result, rows.Err()
}

func (p *Postgres) SaveToolCall(ctx context.Context, call ToolCall) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO tool_calls (id, run_id, request_id, tool_name, arguments_digest, arguments, status, result_text, approval_id, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET status=EXCLUDED.status, result_text=EXCLUDED.result_text, approval_id=EXCLUDED.approval_id, request_id=EXCLUDED.request_id`,
		call.ID, call.RunID, call.RequestID, call.ToolName, call.ArgumentsDigest, []byte(call.Arguments), call.Status, call.ResultText, call.ApprovalID, call.CreatedAt.UTC())
	return err
}
func (p *Postgres) ListToolCalls(ctx context.Context, runID string) ([]ToolCall, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, run_id, request_id, tool_name, arguments_digest, arguments, status, result_text, approval_id, created_at FROM tool_calls WHERE run_id=$1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ToolCall
	for rows.Next() {
		var call ToolCall
		var args []byte
		if err := rows.Scan(&call.ID, &call.RunID, &call.RequestID, &call.ToolName, &call.ArgumentsDigest, &args, &call.Status, &call.ResultText, &call.ApprovalID, &call.CreatedAt); err != nil {
			return nil, err
		}
		call.Arguments = args
		result = append(result, call)
	}
	return result, rows.Err()
}

func (p *Postgres) CreateApproval(ctx context.Context, approval Approval) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO approvals (id, request_id, user_id, role, agent_id, run_id, tool_name, arguments, digest, status, execution_outcome, operator_id, expires_at, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		approval.ID, approval.RequestID, approval.UserID, approval.Role, approval.AgentID, approval.RunID, approval.Tool, []byte(approval.Arguments), approval.Digest, approval.Status, approval.ExecutionOutcome, approval.OperatorID, approval.ExpiresAt.UTC(), approval.CreatedAt.UTC())
	return err
}
func (p *Postgres) ListApprovals(ctx context.Context, status string) ([]Approval, error) {
	_, _ = p.pool.Exec(ctx, `UPDATE approvals SET status='expired' WHERE status='pending' AND expires_at <= now()`)
	rows, err := p.pool.Query(ctx, `SELECT id, request_id, user_id, role, agent_id, run_id, tool_name, arguments, digest, status, execution_outcome, operator_id, expires_at, created_at, decided_at
		FROM approvals WHERE ($1='' OR status=$1) ORDER BY id`, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectApprovals(rows)
}
func (p *Postgres) GetApproval(ctx context.Context, id string) (Approval, error) {
	_, _ = p.pool.Exec(ctx, `UPDATE approvals SET status='expired' WHERE id=$1 AND status='pending' AND expires_at <= now()`, id)
	rows, err := p.pool.Query(ctx, `SELECT id, request_id, user_id, role, agent_id, run_id, tool_name, arguments, digest, status, execution_outcome, operator_id, expires_at, created_at, decided_at FROM approvals WHERE id=$1`, id)
	if err != nil {
		return Approval{}, err
	}
	items, err := collectApprovals(rows)
	if err != nil {
		return Approval{}, err
	}
	if len(items) == 0 {
		return Approval{}, ErrNotFound
	}
	return items[0], nil
}
func collectApprovals(rows pgx.Rows) ([]Approval, error) {
	defer rows.Close()
	var result []Approval
	for rows.Next() {
		var approval Approval
		var args []byte
		if err := rows.Scan(&approval.ID, &approval.RequestID, &approval.UserID, &approval.Role, &approval.AgentID, &approval.RunID, &approval.Tool, &args, &approval.Digest, &approval.Status, &approval.ExecutionOutcome, &approval.OperatorID, &approval.ExpiresAt, &approval.CreatedAt, &approval.DecidedAt); err != nil {
			return nil, err
		}
		approval.Arguments = args
		result = append(result, approval)
	}
	return result, rows.Err()
}
func (p *Postgres) DecideApproval(ctx context.Context, id, decision, operatorID string, now time.Time) (Approval, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return Approval{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	approval, err := lockApproval(ctx, tx, id)
	if err != nil {
		return Approval{}, err
	}
	if !now.Before(approval.ExpiresAt) && approval.Status == "pending" {
		_, _ = tx.Exec(ctx, `UPDATE approvals SET status='expired' WHERE id=$1`, id)
		_ = tx.Commit(ctx)
		return Approval{}, ErrNotFound
	}
	if approval.UserID == operatorID || approval.Status != "pending" || (decision != "approve" && decision != "deny") {
		return Approval{}, ErrConflict
	}
	status := "approved"
	outcome := approval.ExecutionOutcome
	if decision == "deny" {
		status = "denied"
		outcome = "not_run"
	}
	decided := now.UTC()
	_, err = tx.Exec(ctx, `UPDATE approvals SET status=$2, operator_id=$3, decided_at=$4, execution_outcome=$5 WHERE id=$1`, id, status, operatorID, decided, outcome)
	if err != nil {
		return Approval{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Approval{}, err
	}
	approval.Status = status
	approval.OperatorID = operatorID
	approval.DecidedAt = &decided
	approval.ExecutionOutcome = outcome
	return approval, nil
}
func lockApproval(ctx context.Context, tx pgx.Tx, id string) (Approval, error) {
	var approval Approval
	var args []byte
	err := tx.QueryRow(ctx, `SELECT id, request_id, user_id, role, agent_id, run_id, tool_name, arguments, digest, status, execution_outcome, operator_id, expires_at, created_at, decided_at FROM approvals WHERE id=$1 FOR UPDATE`, id).
		Scan(&approval.ID, &approval.RequestID, &approval.UserID, &approval.Role, &approval.AgentID, &approval.RunID, &approval.Tool, &args, &approval.Digest, &approval.Status, &approval.ExecutionOutcome, &approval.OperatorID, &approval.ExpiresAt, &approval.CreatedAt, &approval.DecidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, ErrNotFound
	}
	approval.Arguments = args
	return approval, err
}
func (p *Postgres) ConsumeApproval(ctx context.Context, id, userID, role, agentID, tool, digest string, now time.Time) (string, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	approval, err := lockApproval(ctx, tx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "approval_not_found_or_expired", nil
		}
		return "", err
	}
	if !now.Before(approval.ExpiresAt) {
		_, _ = tx.Exec(ctx, `UPDATE approvals SET status='expired' WHERE id=$1 AND status='pending'`, id)
		_ = tx.Commit(ctx)
		return "approval_not_found_or_expired", nil
	}
	if approval.UserID != userID || approval.Role != role || approval.Tool != tool || approval.Digest != digest || (approval.AgentID != "" && approval.AgentID != agentID) {
		_ = tx.Commit(ctx)
		return "approval_mismatch", nil
	}
	switch approval.Status {
	case "pending":
		_ = tx.Commit(ctx)
		return "approval_required", nil
	case "denied":
		_ = tx.Commit(ctx)
		return "approval_denied", nil
	case "approved":
		if _, err := tx.Exec(ctx, `UPDATE approvals SET status='consumed', execution_outcome='started' WHERE id=$1 AND status='approved'`, id); err != nil {
			return "", err
		}
		return "", tx.Commit(ctx)
	default:
		_ = tx.Commit(ctx)
		return "approval_consumed", nil
	}
}
func (p *Postgres) FinishApproval(ctx context.Context, id, outcome string) error {
	_, err := p.pool.Exec(ctx, `UPDATE approvals SET execution_outcome=$2 WHERE id=$1`, id, outcome)
	return err
}

func (p *Postgres) WriteAudit(ctx context.Context, event AuditEvent) error {
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	return p.pool.QueryRow(ctx, `INSERT INTO audit_events (request_id, occurred_at, user_id, role, agent_id, operator_id, tool_name, service, decision, reason, risk, phase, approval_id, duration_ms, outcome)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) RETURNING id`,
		event.RequestID, event.OccurredAt.UTC(), event.UserID, event.Role, event.AgentID, event.OperatorID, event.Tool, event.Service, event.Decision, event.Reason, event.Risk, event.Phase, event.ApprovalID, event.DurationMS, event.Outcome).Scan(&event.ID)
}
func (p *Postgres) QueryAudit(ctx context.Context, query AuditQuery) (AuditPage, error) {
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	userFilter := query.UserID
	if query.AdminAll {
		userFilter = ""
	}
	rows, err := p.pool.Query(ctx, `SELECT id, request_id, occurred_at, user_id, role, agent_id, operator_id, tool_name, service, decision, reason, risk, phase, approval_id, duration_ms, outcome
		FROM audit_events
		WHERE id > $1
		  AND ($2='' OR user_id=$2)
		  AND ($3='' OR agent_id=$3)
		  AND ($4='' OR tool_name=$4)
		  AND ($5='' OR risk=$5)
		  AND ($6='' OR outcome=$6)
		  AND occurred_at >= $7 AND occurred_at < $8
		ORDER BY id LIMIT $9`, query.AfterID, userFilter, query.AgentID, query.Tool, query.Risk, query.Outcome, query.From.UTC(), query.To.UTC(), limit)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(&event.ID, &event.RequestID, &event.OccurredAt, &event.UserID, &event.Role, &event.AgentID, &event.OperatorID, &event.Tool, &event.Service, &event.Decision, &event.Reason, &event.Risk, &event.Phase, &event.ApprovalID, &event.DurationMS, &event.Outcome); err != nil {
			return AuditPage{}, err
		}
		events = append(events, event)
	}
	page := AuditPage{Events: events}
	if len(events) == limit {
		page.NextID = events[len(events)-1].ID
	}
	return page, rows.Err()
}
func (p *Postgres) Analytics(ctx context.Context, query AnalyticsQuery) (Analytics, error) {
	userFilter := query.UserID
	if query.AdminAll {
		userFilter = ""
	}
	rows, err := p.pool.Query(ctx, `SELECT DISTINCT ON (request_id) id, request_id, occurred_at, user_id, role, agent_id, operator_id, tool_name, service, decision, reason, risk, phase, approval_id, duration_ms, outcome
		FROM audit_events
		WHERE ($1='' OR user_id=$1) AND occurred_at >= $2 AND occurred_at < $3
		ORDER BY request_id, occurred_at DESC, id DESC`, userFilter, query.From.UTC(), query.To.UTC())
	if err != nil {
		return Analytics{}, err
	}
	defer rows.Close()
	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		if err := rows.Scan(&event.ID, &event.RequestID, &event.OccurredAt, &event.UserID, &event.Role, &event.AgentID, &event.OperatorID, &event.Tool, &event.Service, &event.Decision, &event.Reason, &event.Risk, &event.Phase, &event.ApprovalID, &event.DurationMS, &event.Outcome); err != nil {
			return Analytics{}, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return Analytics{}, err
	}
	var pending int
	if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM approvals WHERE status='pending' AND expires_at > now() AND ($1='' OR user_id=$1)`, userFilter).Scan(&pending); err != nil {
		return Analytics{}, err
	}
	return Aggregate(events, query.From, query.To, pending), nil
}

func (p *Postgres) SaveFinding(ctx context.Context, finding Finding) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO findings (id, rule_name, rule_version, severity, status, summary, evidence, dedupe_key, explanation, window_start, window_end, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT (dedupe_key) DO NOTHING`,
		finding.ID, finding.Rule, finding.RuleVersion, finding.Severity, finding.Status, finding.Summary, []byte(finding.Evidence), finding.DedupeKey, finding.Explanation, finding.WindowStart.UTC(), finding.WindowEnd.UTC(), finding.CreatedAt.UTC())
	return err
}
func (p *Postgres) ListFindings(ctx context.Context) ([]Finding, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, rule_name, rule_version, severity, status, summary, evidence, dedupe_key, explanation, window_start, window_end, created_at FROM findings ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Finding
	for rows.Next() {
		var finding Finding
		var evidence []byte
		if err := rows.Scan(&finding.ID, &finding.Rule, &finding.RuleVersion, &finding.Severity, &finding.Status, &finding.Summary, &evidence, &finding.DedupeKey, &finding.Explanation, &finding.WindowStart, &finding.WindowEnd, &finding.CreatedAt); err != nil {
			return nil, err
		}
		finding.Evidence = evidence
		result = append(result, finding)
	}
	return result, rows.Err()
}
func (p *Postgres) UpdateFinding(ctx context.Context, id, status string) (Finding, error) {
	if status != "acknowledged" && status != "dismissed" && status != "open" {
		return Finding{}, ErrConflict
	}
	tag, err := p.pool.Exec(ctx, `UPDATE findings SET status=$2 WHERE id=$1`, id, status)
	if err != nil {
		return Finding{}, err
	}
	if tag.RowsAffected() == 0 {
		return Finding{}, ErrNotFound
	}
	items, err := p.ListFindings(ctx)
	if err != nil {
		return Finding{}, err
	}
	for _, finding := range items {
		if finding.ID == id {
			return finding, nil
		}
	}
	return Finding{}, ErrNotFound
}
func (p *Postgres) SetFindingExplanation(ctx context.Context, id, explanation string) error {
	tag, err := p.pool.Exec(ctx, `UPDATE findings SET explanation=$2 WHERE id=$1`, id, explanation)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (p *Postgres) ListRestrictions(ctx context.Context) ([]Restriction, error) {
	rows, err := p.pool.Query(ctx, `SELECT tool_name, disabled, roles, updated_by, updated_at FROM policy_restrictions ORDER BY tool_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Restriction
	for rows.Next() {
		var item Restriction
		var roles []byte
		if err := rows.Scan(&item.Tool, &item.Disabled, &roles, &item.UpdatedBy, &item.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(roles, &item.Roles)
		result = append(result, item)
	}
	return result, rows.Err()
}
func (p *Postgres) SaveRestriction(ctx context.Context, item Restriction) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO policy_restrictions (tool_name, disabled, roles, updated_by, updated_at) VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (tool_name) DO UPDATE SET disabled=EXCLUDED.disabled, roles=EXCLUDED.roles, updated_by=EXCLUDED.updated_by, updated_at=EXCLUDED.updated_at`,
		item.Tool, item.Disabled, mustJSON(item.Roles), item.UpdatedBy, item.UpdatedAt.UTC())
	return err
}

func mustJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil || encoded == nil {
		return []byte(`[]`)
	}
	return encoded
}
