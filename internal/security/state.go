package security

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"sync"
	"time"
)

type rateWindow struct {
	start time.Time
	count int
}
type Limiter struct {
	mu      sync.Mutex
	windows map[string]rateWindow
	limit   int
	window  time.Duration
}

func NewLimiter(limit int, window time.Duration) *Limiter {
	return &Limiter{windows: map[string]rateWindow{}, limit: limit, window: window}
}
func (l *Limiter) Allow(identity string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.windows[identity]
	if w.start.IsZero() || !now.Before(w.start.Add(l.window)) {
		w = rateWindow{start: now}
	}
	if w.count >= l.limit {
		return false
	}
	w.count++
	l.windows[identity] = w
	return true
}

type Approval struct {
	ID               string          `json:"approval_id"`
	RequestID        string          `json:"request_id"`
	Identity         string          `json:"identity"`
	Role             string          `json:"role"`
	Tool             string          `json:"tool"`
	Arguments        json.RawMessage `json:"arguments"`
	Status           string          `json:"status"`
	ExpiresAt        time.Time       `json:"expires_at"`
	AgentID          string          `json:"agent_id,omitempty"`
	RunID            string          `json:"run_id,omitempty"`
	OperatorID       string          `json:"operator_id,omitempty"`
	ExecutionOutcome string          `json:"execution_outcome,omitempty"`
	digest           [32]byte
}
type Approvals struct {
	mu      sync.Mutex
	entries map[string]*Approval
}

func NewApprovals() *Approvals { return &Approvals{entries: map[string]*Approval{}} }

func CanonicalArguments(raw json.RawMessage) ([]byte, error) {
	return canonicalArguments(raw)
}

func canonicalArguments(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := rejectDuplicateKeys(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil || object == nil {
		return nil, errors.New("invalid_arguments")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("invalid_arguments")
	}
	return json.Marshal(object)
}

func rejectDuplicateKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkJSON(decoder); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid_arguments")
	}
	return nil
}

func walkJSON(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return errors.New("invalid_arguments")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return errors.New("invalid_arguments")
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid_arguments")
			}
			if seen[key] {
				return errors.New("duplicate_key")
			}
			seen[key] = true
			if err := walkJSON(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return errors.New("invalid_arguments")
		}
	case '[':
		for decoder.More() {
			if err := walkJSON(decoder); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return errors.New("invalid_arguments")
		}
	default:
		return errors.New("invalid_arguments")
	}
	return nil
}
func (a *Approvals) prune(now time.Time) {
	for id, entry := range a.entries {
		if !now.Before(entry.ExpiresAt) {
			delete(a.entries, id)
		}
	}
}
func (a *Approvals) Create(id string, identity Identity, tool string, args json.RawMessage, now time.Time) (string, error) {
	canonical, err := canonicalArguments(args)
	if err != nil {
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune(now)
	if len(a.entries) >= 1000 {
		return "", errors.New("approval_capacity_reached")
	}
	a.entries[id] = &Approval{ID: id, RequestID: id, Identity: identity.ID, Role: identity.Role, AgentID: identity.AgentID, RunID: identity.RunID, Tool: tool, Arguments: canonical, Status: "pending", ExpiresAt: now.Add(5 * time.Minute), ExecutionOutcome: "not_run", digest: sha256.Sum256(canonical)}
	return id, nil
}
func (a *Approvals) List(now time.Time) []Approval {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune(now)
	result := make([]Approval, 0, len(a.entries))
	for _, entry := range a.entries {
		copy := *entry
		copy.Arguments = append(json.RawMessage(nil), entry.Arguments...)
		result = append(result, copy)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func (a *Approvals) Decide(id, decision string, now time.Time) (Approval, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune(now)
	entry, ok := a.entries[id]
	if !ok {
		return Approval{}, errors.New("approval_not_found_or_expired")
	}
	if entry.Status != "pending" {
		return Approval{}, errors.New("approval_already_decided")
	}
	switch decision {
	case "approve":
		entry.Status = "approved"
	case "deny":
		entry.Status = "denied"
	default:
		return Approval{}, errors.New("invalid_decision")
	}
	copy := *entry
	copy.Arguments = append(json.RawMessage(nil), entry.Arguments...)
	return copy, nil
}

// Consume is atomic: concurrent retries cannot dispatch the same approval twice.
// A failed downstream call still consumes the approval; there is no automatic retry.
func (a *Approvals) Consume(id string, identity Identity, tool string, args json.RawMessage, now time.Time) string {
	canonical, err := canonicalArguments(args)
	if err != nil {
		return "invalid_arguments"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prune(now)
	entry, ok := a.entries[id]
	if !ok {
		return "approval_not_found_or_expired"
	}
	if entry.Identity != identity.ID || entry.Role != identity.Role || entry.Tool != tool || entry.digest != sha256.Sum256(canonical) {
		return "approval_mismatch"
	}
	switch entry.Status {
	case "pending":
		return "approval_required"
	case "denied":
		return "approval_denied"
	case "approved":
		entry.Status = "consumed"
		return ""
	default:
		return "approval_consumed"
	}
}
