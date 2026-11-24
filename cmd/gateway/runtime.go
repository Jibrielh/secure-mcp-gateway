package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/limit"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func openRedisLimiter(rawURL string, rate int) (*limit.Redis, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("REDIS_URL is required")
	}
	return limit.Open(rawURL, rate, time.Minute)
}

func openGatewayStore(ctx context.Context, dsn string) (*store.Postgres, error) {
	if dsn == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	return store.OpenPostgres(ctx, dsn)
}

func limitToolResult(result *mcp.CallToolResult, max int) *mcp.CallToolResult {
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok || len(text.Text) <= max {
			continue
		}
		text.Text = text.Text[:max] + "\n[truncated]"
	}
	return result
}
