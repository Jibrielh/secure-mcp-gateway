package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type lookupInput struct {
	ID int `json:"id"`
}

type record struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

var records = map[int]string{1: "Alice", 2: "Bob", 3: "Charlie"}

func lookupRecord(_ context.Context, _ *mcp.CallToolRequest, input lookupInput) (*mcp.CallToolResult, record, error) {
	name, ok := records[input.ID]
	if !ok {
		return nil, record{}, errors.New("record not found")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: name}}}, record{ID: input.ID, Name: name}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	server := mcp.NewServer(&mcp.Implementation{Name: "data-server", Version: "0.3.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "lookup_record", Description: "Look up a record in the fixed demonstration dataset"}, lookupRecord)
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("data server failed: %v", err)
	}
}
