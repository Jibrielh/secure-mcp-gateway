package analytics

import (
	"testing"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

func TestDenialBurstCreatesFinding(t *testing.T) {
	now := time.Date(2026, 11, 24, 15, 0, 0, 0, time.UTC)
	var events []store.AuditEvent
	for i := range 5 {
		events = append(events, store.AuditEvent{RequestID: "req", UserID: "user-ada", OccurredAt: now.Add(-time.Duration(i) * time.Second), Decision: "denied", Reason: "rbac_deny", Tool: "files.read"})
	}
	events = append(events, store.AuditEvent{RequestID: "other", UserID: "user-vera", OccurredAt: now, Decision: "allowed", Reason: "rbac_allow", Outcome: "success", Tool: "utility.echo"})
	findings := Scan(events, now)
	if len(findings) != 1 || findings[0].Rule != "repeated_auth_denials" {
		t.Fatalf("%+v", findings)
	}
	if findings[0].Explanation != "" {
		t.Fatal("scan invented an explanation")
	}
}
