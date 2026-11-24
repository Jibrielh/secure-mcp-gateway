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

	"github.com/Jibrielh/secure-mcp-gateway/internal/connectors/github"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	base := flag.String("base-url", "https://api.github.com", "GitHub API base URL")
	flag.Parse()
	client, err := github.New(*base, os.Getenv("GITHUB_TOKEN"))
	if err != nil {
		log.Fatal(err)
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "github-readonly", Version: "1.0.0"}, nil)
	text := func(value any) *mcp.CallToolResult {
		encoded, _ := json.Marshal(value)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}
	}
	mcp.AddTool(server, &mcp.Tool{Name: "list_issues", Description: "List open issues in a public repository"}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Limit int    `json:"limit"`
	}) (*mcp.CallToolResult, any, error) {
		issues, err := client.ListIssues(ctx, input.Owner, input.Repo, input.Limit)
		if err != nil {
			return nil, nil, err
		}
		return text(issues), map[string]any{"issues": issues}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_issue", Description: "Read one public issue"}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Owner  string `json:"owner"`
		Repo   string `json:"repo"`
		Number int    `json:"number"`
	}) (*mcp.CallToolResult, any, error) {
		issue, err := client.GetIssue(ctx, input.Owner, input.Repo, input.Number)
		if err != nil {
			return nil, nil, err
		}
		return text(issue), issue, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "get_file", Description: "Read a file from a public repository"}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Owner string `json:"owner"`
		Repo  string `json:"repo"`
		Path  string `json:"path"`
		Ref   string `json:"ref"`
	}) (*mcp.CallToolResult, any, error) {
		body, err := client.GetFile(ctx, input.Owner, input.Repo, input.Path, input.Ref)
		if err != nil {
			return nil, nil, err
		}
		return text(map[string]string{"content": body}), map[string]string{"content": body}, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "search_issues", Description: "Search public issues"}, func(ctx context.Context, _ *mcp.CallToolRequest, input struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}) (*mcp.CallToolResult, any, error) {
		issues, err := client.SearchIssues(ctx, input.Query, input.Limit)
		if err != nil {
			return nil, nil, err
		}
		return text(issues), map[string]any{"issues": issues}, nil
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
