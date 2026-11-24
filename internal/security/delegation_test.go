package security

import (
	"testing"
	"time"
)

func TestDelegationTokenBoundaries(t *testing.T) {
	delegation, err := NewDelegation("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	delegation.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	token, err := delegation.Mint(Identity{ID: "user-ada", Role: "developer", AgentID: "agent-1", Tools: []string{"utility.add"}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	identity, ok := delegation.Authenticate("Bearer " + token)
	if !ok || identity.ID != "user-ada" || len(identity.Tools) != 1 {
		t.Fatalf("%+v %v", identity, ok)
	}
	if _, ok := delegation.Authenticate("Bearer " + token + "tamper"); ok {
		t.Fatal("accepted a tampered token")
	}
	expired := delegation
	expired.now = func() time.Time { return time.Unix(1_700_000_000, 0).Add(2 * time.Minute) }
	if _, ok := expired.Authenticate("Bearer " + token); ok {
		t.Fatal("accepted an expired token")
	}
	approver, err := delegation.Mint(Identity{ID: "user-owen", Role: "approver", Approver: true, Tools: []string{}}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	delegation.now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	if _, ok := delegation.Authenticate("Bearer " + approver); ok {
		t.Fatal("approver token was accepted for tool calls")
	}
	operator, ok := delegation.Approver("Bearer " + approver)
	if !ok || operator.ID != "user-owen" {
		t.Fatal("approver token rejected")
	}
	if _, err := delegation.Mint(Identity{ID: "user-amy", Role: "admin", Approver: true, Tools: []string{}}, time.Minute); err == nil {
		t.Fatal("combined admin and approver token")
	}
}
