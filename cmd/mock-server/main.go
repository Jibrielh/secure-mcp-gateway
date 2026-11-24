package main

import (
	"context"
	"errors"
	"log"
	"math"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoInput struct {
	Message string `json:"message" jsonschema:"The message to echo unchanged"`
}

func echo(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: input.Message}},
	}, nil, nil
}

type addInput struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

type addOutput struct {
	Sum float64 `json:"sum"`
}

func add(_ context.Context, _ *mcp.CallToolRequest, input addInput) (*mcp.CallToolResult, addOutput, error) {
	sum := input.A + input.B
	if math.IsInf(sum, 0) || math.IsNaN(sum) {
		return nil, addOutput{}, errors.New("sum is outside the supported numeric range")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: strconv.FormatFloat(sum, 'g', -1, 64)}}}, addOutput{Sum: sum}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := mcp.NewServer(&mcp.Implementation{Name: "mock-server", Version: "0.4.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "Return the supplied message unchanged"}, echo)
	mcp.AddTool(server, &mcp.Tool{Name: "add", Description: "Add two numbers"}, add)
	mcp.AddTool(server, &mcp.Tool{Name: "mock_destructive_action", Description: "Safe simulated maintenance; no real data is changed"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct {
		Target string `json:"target,omitempty" jsonschema:"Optional label for the simulated action"`
	}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Mock maintenance completed; no real data was changed."}}}, nil, nil
	})
	// Stdout belongs exclusively to the MCP transport; log writes to stderr.
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("mock server failed: %v", err)
	}
}
