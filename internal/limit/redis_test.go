package limit

import (
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestSharedLimitAcrossClients(t *testing.T) {
	server := miniredis.RunT(t)
	options := &redis.Options{Addr: server.Addr()}
	first := NewRedis(redis.NewClient(options), 2, time.Minute)
	second := NewRedis(redis.NewClient(options), 2, time.Minute)
	now := time.Now()
	ctx := t.Context()
	ok, err := first.Allow(ctx, "ada", "agent-1", "utility.add", now)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	ok, err = second.Allow(ctx, "ada", "agent-1", "utility.add", now)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	ok, err = second.Allow(ctx, "ada", "agent-1", "utility.add", now)
	if err != nil || ok {
		t.Fatal("second client bypassed the shared limit", err, ok)
	}
	ok, err = first.Allow(ctx, "owen", "", "utility.add", now)
	if err != nil || !ok {
		t.Fatal("identities shared a limit", err, ok)
	}
	server.Close()
	if _, err := first.Allow(ctx, "ada", "", "utility.add", now); err == nil {
		t.Fatal("unavailable redis did not fail closed")
	}
}
