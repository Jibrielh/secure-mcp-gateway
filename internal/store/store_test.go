package store

import (
	"os"
	"testing"
	"time"
)

func TestMemorySurvivesQueriesAndHidesOtherUsers(t *testing.T) {
	db := NewMemory()
	now := time.Now().UTC()
	_ = db.WriteAudit(t.Context(), AuditEvent{RequestID: "a", OccurredAt: now, UserID: "ada", Tool: "utility.add", Decision: "allowed", Reason: "rbac_allow", Outcome: "success", DurationMS: 12, Phase: "result"})
	_ = db.WriteAudit(t.Context(), AuditEvent{RequestID: "a", OccurredAt: now.Add(-time.Millisecond), UserID: "ada", Tool: "utility.add", Decision: "forwarding", Reason: "security_checks_passed", Phase: "intent"})
	_ = db.WriteAudit(t.Context(), AuditEvent{RequestID: "b", OccurredAt: now, UserID: "vera", Tool: "files.read", Decision: "denied", Reason: "rbac_deny", Outcome: "denied"})
	page, err := db.QueryAudit(t.Context(), AuditQuery{UserID: "ada", From: now.Add(-time.Hour), To: now.Add(time.Hour)})
	if err != nil || len(page.Events) != 2 {
		t.Fatal(err, len(page.Events))
	}
	analytics, err := db.Analytics(t.Context(), AnalyticsQuery{UserID: "ada", From: now.Add(-time.Hour), To: now.Add(time.Hour)})
	if err != nil || analytics.Total != 1 || analytics.Success != 1 || analytics.P95MS != nil {
		t.Fatalf("%+v %v", analytics, err)
	}
}

func TestPostgresMigration(t *testing.T) {
	dsn := os.Getenv("AGENTGATE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("AGENTGATE_TEST_DATABASE_URL is not set")
	}
	db, err := OpenPostgres(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	user := User{ID: "user-test", Subject: "test", Username: "test", DisplayName: "Test", Role: "developer", CreatedAt: time.Now().UTC()}
	if err := db.UpsertUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	db.Close()
	reopened, err := OpenPostgres(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	found, err := reopened.FindUser(t.Context(), user.ID)
	if err != nil || found.Username != "test" {
		t.Fatal(err, found)
	}
}
