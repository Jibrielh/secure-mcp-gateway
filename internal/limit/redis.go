package limit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const script = `
local current = redis.call("INCR", KEYS[1])
if current == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
if current > tonumber(ARGV[2]) then
  return 0
end
return 1
`

type Redis struct {
	client *redis.Client
	limit  int
	window time.Duration
}

func NewRedis(client *redis.Client, limit int, window time.Duration) *Redis {
	return &Redis{client: client, limit: limit, window: window}
}

func Open(url string, limit int, window time.Duration) (*Redis, error) {
	options, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return NewRedis(redis.NewClient(options), limit, window), nil
}

func (r *Redis) Close() error { return r.client.Close() }

func (r *Redis) Ping(ctx context.Context) error { return r.client.Ping(ctx).Err() }

func (r *Redis) Allow(ctx context.Context, identity, agent, tool string, now time.Time) (bool, error) {
	if identity == "" {
		return false, fmt.Errorf("limiter identity is required")
	}
	keys := []string{"agentgate:rl:identity:" + identity}
	if agent != "" {
		keys = append(keys, "agentgate:rl:agent:"+agent)
	}
	if tool != "" {
		keys = append(keys, "agentgate:rl:tool:"+identity+":"+tool)
	}
	for _, key := range keys {
		value, err := r.client.Eval(ctx, script, []string{key}, r.window.Milliseconds(), r.limit).Int()
		if err != nil {
			return false, err
		}
		if value == 0 {
			return false, nil
		}
	}
	_ = now
	return true, nil
}
