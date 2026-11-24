package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Jibrielh/secure-mcp-gateway/internal/connectors/sampledb"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	dsn := flag.String("db", "file:fixtures/sample.db", "sqlite DSN for the sample analytics database")
	flag.Parse()
	db, err := sampledb.Open(*dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	server := mcp.NewServer(&mcp.Implementation{Name: "sample-analytics", Version: "1.0.0"}, nil)
	text := func(value any) *mcp.CallToolResult {
		encoded, _ := json.Marshal(value)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
	}
	mcp.AddTool(server, &mcp.Tool{Name: "list_regions", Description: "List sample regions"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		names, err := db.ListRegions(ctx)
		if err != nil {
			return nil, nil, err
		}
		return text(names), map[string]any{"regions": names}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "revenue_by_region", Description: "Summarize sample revenue"}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Region string `json:"region"`
	}) (*mcp.CallToolResult, any, error) {
		rows, err := db.RevenueByRegion(ctx, input.Region)
		if err != nil {
			return nil, nil, err
		}
		return text(rows), map[string]any{"rows": rows}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "top_products", Description: "List top sample products"}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Limit int `json:"limit"`
	}) (*mcp.CallToolResult, any, error) {
		rows, err := db.TopProducts(ctx, input.Limit)
		if err != nil {
			return nil, nil, err
		}
		return text(rows), map[string]any{"products": rows}, nil
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
