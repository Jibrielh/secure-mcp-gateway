package main

import (
	"context"

	"github.com/redis/go-redis/v9"
)

func pingRedis(ctx context.Context, rawURL string) error {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return err
	}
	client := redis.NewClient(options)
	defer client.Close()
	return client.Ping(ctx).Err()
}
