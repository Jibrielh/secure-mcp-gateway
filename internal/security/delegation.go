package security

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const delegationIssuer = "agentgate-workspace"
const delegationAudience = "mcp-gateway"

type delegationClaims struct {
	Issuer   string   `json:"iss"`
	Audience string   `json:"aud"`
	Subject  string   `json:"sub"`
	Role     string   `json:"role"`
	AgentID  string   `json:"agent_id"`
	RunID    string   `json:"run_id"`
	Tools    []string `json:"tools"`
	Expires  int64    `json:"exp"`
	Approver bool     `json:"approver"`
}

// Delegation exchanges a server-side session for a short-lived gateway credential.
type Delegation struct {
	secret []byte
	now    func() time.Time
}

func NewDelegation(secret string) (*Delegation, error) {
	if len(secret) < 32 {
		return nil, errors.New("delegation secret must contain at least 32 characters")
	}
	return &Delegation{secret: []byte(secret), now: time.Now}, nil
}

func (d *Delegation) Mint(identity Identity, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > 15*time.Minute {
		return "", errors.New("delegation ttl must be between 1ns and 15m")
	}
	if identity.Approver && identity.Role != "approver" {
		return "", errors.New("approver tokens cannot carry another role")
	}
	claims := delegationClaims{
		Issuer: delegationIssuer, Audience: delegationAudience, Subject: identity.ID,
		Role: identity.Role, AgentID: identity.AgentID, RunID: identity.RunID,
		Tools: identity.Tools, Expires: d.now().Add(ttl).Unix(), Approver: identity.Approver,
	}
	if claims.Tools == nil {
		claims.Tools = []string{}
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, d.secret)
	mac.Write([]byte(encoded))
	return "v1." + encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (d *Delegation) Authenticate(header string) (Identity, bool) {
	token, ok := bearer(header)
	if !ok {
		return Identity{}, false
	}
	claims, ok := d.parse(token)
	if !ok || claims.Approver {
		return Identity{}, false
	}
	return identityFromClaims(claims), true
}

func (d *Delegation) IsApprover(header string) bool {
	_, ok := d.Approver(header)
	return ok
}

func (d *Delegation) Approver(header string) (Identity, bool) {
	token, ok := bearer(header)
	if !ok {
		return Identity{}, false
	}
	claims, ok := d.parse(token)
	if !ok || !claims.Approver || claims.Role != "approver" {
		return Identity{}, false
	}
	identity := identityFromClaims(claims)
	identity.Approver = true
	return identity, true
}

func identityFromClaims(claims delegationClaims) Identity {
	tools := claims.Tools
	if tools == nil {
		tools = []string{}
	}
	return Identity{ID: claims.Subject, Role: claims.Role, UserID: claims.Subject, AgentID: claims.AgentID, RunID: claims.RunID, Tools: append([]string(nil), tools...)}
}

func (d *Delegation) parse(token string) (delegationClaims, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "v1" {
		return delegationClaims{}, false
	}
	mac := hmac.New(sha256.New, d.secret)
	mac.Write([]byte(parts[1]))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(expected, got) {
		return delegationClaims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return delegationClaims{}, false
	}
	var claims delegationClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return delegationClaims{}, false
	}
	if claims.Issuer != delegationIssuer || claims.Audience != delegationAudience || claims.Subject == "" {
		return delegationClaims{}, false
	}
	if claims.Expires < d.now().Unix() {
		return delegationClaims{}, false
	}
	switch claims.Role {
	case "viewer", "developer", "admin", "approver":
	default:
		return delegationClaims{}, false
	}
	if claims.Approver != (claims.Role == "approver") {
		return delegationClaims{}, false
	}
	if claims.Tools == nil {
		return delegationClaims{}, false
	}
	return claims, true
}

// Chain tries the legacy bearer credentials and then additional authenticators.
type Chain struct {
	Primary Authenticator
	Extra   []Authenticator
}

func (c Chain) Authenticate(header string) (Identity, bool) {
	if c.Primary != nil {
		if identity, ok := c.Primary.Authenticate(header); ok {
			return identity, true
		}
	}
	for _, extra := range c.Extra {
		if identity, ok := extra.Authenticate(header); ok {
			return identity, true
		}
	}
	return Identity{}, false
}

func (c Chain) IsApprover(header string) bool {
	_, ok := c.Approver(header)
	return ok
}

func (c Chain) Approver(header string) (Identity, bool) {
	if c.Primary != nil {
		if identity, ok := c.Primary.Approver(header); ok {
			return identity, true
		}
	}
	for _, extra := range c.Extra {
		if identity, ok := extra.Approver(header); ok {
			return identity, true
		}
	}
	return Identity{}, false
}
