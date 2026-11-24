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

	"github.com/Jibrielh/secure-mcp-gateway/internal/connectors/files"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	rootFlag := flag.String("root", "fixtures/workspace", "demo workspace directory")
	flag.Parse()
	root, err := files.Open(*rootFlag)
	if err != nil {
		log.Fatal(err)
	}
	defer root.Close()
	server := mcp.NewServer(&mcp.Implementation{Name: "workspace-files", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "list", Description: "List files in the demo workspace"}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		Path string `json:"path"`
	}) (*mcp.CallToolResult, any, error) {
		names, err := files.List(root, input.Path)
		if err != nil {
			return nil, nil, err
		}
		encoded, _ := json.Marshal(names)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, map[string]any{"entries": names}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "read", Description: "Read one file in the demo workspace"}, func(_ context.Context, _ *mcp.CallToolRequest, input struct {
		Path string `json:"path"`
	}) (*mcp.CallToolResult, any, error) {
		body, err := files.Read(root, input.Path)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: body}}}, map[string]any{"path": input.Path, "content": body}, nil
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
