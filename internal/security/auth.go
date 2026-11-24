package security

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

type Identity struct {
	ID       string   `json:"identity"`
	Role     string   `json:"role"`
	UserID   string   `json:"user_id,omitempty"`
	AgentID  string   `json:"agent_id,omitempty"`
	RunID    string   `json:"-"`
	Tools    []string `json:"-"`
	Approver bool     `json:"-"`
}

// Authenticator resolves a caller from an Authorization header.
// Tool callers and approvers are separate. A token that fails either check is denied.
type Authenticator interface {
	Authenticate(header string) (Identity, bool)
	IsApprover(header string) bool
	Approver(header string) (Identity, bool)
}
type credential struct {
	hash     [32]byte
	identity Identity
}
type Auth struct {
	credentials []credential
	approver    [32]byte
}

func NewAuth(tokens map[string]string, approver string) (*Auth, error) {
	a := &Auth{}
	seen := map[[32]byte]bool{}
	for _, role := range []string{"viewer", "developer", "admin"} {
		token := tokens[role]
		if len(token) < 32 || strings.ContainsAny(token, " \t\r\n") {
			return nil, errors.New("each gateway role token must contain at least 32 characters and no whitespace")
		}
		hash := sha256.Sum256([]byte(token))
		if seen[hash] {
			return nil, errors.New("gateway credentials must be distinct")
		}
		seen[hash] = true
		a.credentials = append(a.credentials, credential{hash: hash, identity: Identity{ID: role + "-user", Role: role}})
	}
	if len(approver) < 32 || strings.ContainsAny(approver, " \t\r\n") {
		return nil, errors.New("approver token must contain at least 32 characters and no whitespace")
	}
	a.approver = sha256.Sum256([]byte(approver))
	if seen[a.approver] {
		return nil, errors.New("approver credential must be separate from MCP credentials")
	}
	return a, nil
}

func bearer(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	return token, ok && strings.EqualFold(scheme, "Bearer") && token != "" && !strings.ContainsAny(token, " \t\r\n")
}

func (a *Auth) Authenticate(header string) (Identity, bool) {
	token, ok := bearer(header)
	if !ok {
		return Identity{}, false
	}
	hash := sha256.Sum256([]byte(token))
	for _, c := range a.credentials {
		if subtle.ConstantTimeCompare(hash[:], c.hash[:]) == 1 {
			return c.identity, true
		}
	}
	return Identity{}, false
}

func (a *Auth) IsApprover(header string) bool {
	_, ok := a.Approver(header)
	return ok
}

func (a *Auth) Approver(header string) (Identity, bool) {
	token, ok := bearer(header)
	if !ok {
		return Identity{}, false
	}
	hash := sha256.Sum256([]byte(token))
	if subtle.ConstantTimeCompare(hash[:], a.approver[:]) != 1 {
		return Identity{}, false
	}
	return Identity{ID: "human-approver", Role: "approver", Approver: true}, true
}

type Policy struct {
	Server string
	Risk   string
	Roles  []string
}

func Authorized(identity Identity, policy Policy) bool {
	if identity.ID == "" || (policy.Risk != "low" && policy.Risk != "medium" && policy.Risk != "high") {
		return false
	}
	switch identity.Role {
	case "admin":
		return true
	case "viewer", "developer":
	default:
		return false
	}
	for _, role := range policy.Roles {
		if role == identity.Role {
			return true
		}
	}
	return false
}
