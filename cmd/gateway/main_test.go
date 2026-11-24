package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/mcpclient"
	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoInput struct {
	Message string `json:"message"`
}

// The test executable also serves as a downstream fixture, without adding a
// production tool or changing the real mock server's behavior.
func TestMain(m *testing.M) {
	switch os.Getenv("MCP_TEST_DOWNSTREAM") {
	case "exit":
		os.Exit(0)
	case "result":
		if os.Getenv("GATEWAY_ADMIN_TOKEN") != "" || os.Getenv("OPENAI_API_KEY") != "" {
			fmt.Fprintln(os.Stderr, "credential leaked to test downstream")
			os.Exit(1)
		}
		server := mcp.NewServer(&mcp.Implementation{Name: "test-downstream", Version: "1"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "echo"},
			func(_ context.Context, _ *mcp.CallToolRequest, input echoInput) (*mcp.CallToolResult, any, error) {
				if err := recordFixtureCall("echo"); err != nil {
					return nil, nil, err
				}
				return &mcp.CallToolResult{
					IsError:           input.Message == "fail",
					Content:           []mcp.Content{&mcp.TextContent{Text: "downstream received: " + input.Message}},
					StructuredContent: map[string]any{"received": input.Message, "source": "downstream"},
				}, nil, nil
			})
		mcp.AddTool(server, &mcp.Tool{Name: "mock_destructive_action"}, func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if err := recordFixtureCall("mock_destructive_action"); err != nil {
				return nil, nil, err
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "safe fixture action"}}}, nil, nil
		})
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		marker, err := os.OpenFile(os.Getenv("MCP_TEST_STOPPED"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if _, err := marker.WriteString("stopped\n"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := marker.Close(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	for _, key := range []string{"GATEWAY_VIEWER_TOKEN", "GATEWAY_DEVELOPER_TOKEN", "GATEWAY_ADMIN_TOKEN", "GATEWAY_APPROVER_TOKEN"} {
		var raw [32]byte
		rand.Read(raw[:])
		if err := os.Setenv(key, hex.EncodeToString(raw[:])); err != nil {
			panic(err)
		}
	}
	os.Exit(m.Run())
}

func buildExecutable(t *testing.T, pkg string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), filepath.Base(pkg))
	command := exec.CommandContext(ctx, "go", "build", "-o", binary, pkg)
	command.Dir = "../.."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, output)
	}
	return binary
}

func TestEchoThroughGateway(t *testing.T) {
	gateway := buildExecutable(t, "./cmd/gateway")
	mock := buildExecutable(t, "./cmd/mock-server")
	data := buildExecutable(t, "./cmd/data-server")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, gateway, "-downstream-command", mock, "-data-command", data)
	command.Dir = t.TempDir()
	command.Stderr = os.Stderr
	client := mcpclient.New("gateway-test", os.Getenv("GATEWAY_DEVELOPER_TOKEN"))
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 5 * time.Second}, nil)
	if err != nil {
		t.Fatalf("connect to gateway: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close gateway: %v", err)
		}
	}()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	assertGatewayTools(t, listed.Tools)
	for _, tc := range []struct{ name, message string }{
		{"ordinary", "hello through gateway"}, {"empty", ""},
		{"unicode", "Hello, 世界 👋"}, {"multiline", "first\nsecond\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"message": tc.message}})
			if err != nil {
				t.Fatalf("echo: %v", err)
			}
			assertText(t, result, tc.message, false)
		})
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"missing", map[string]any{}}, {"number", map[string]any{"message": 123}},
		{"null", map[string]any{"message": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: tc.args})
			if err != nil {
				t.Fatalf("expected SDK validation result, got transport/protocol error: %v", err)
			}
			if !result.IsError {
				t.Fatalf("expected invalid input rejection, got %+v", result)
			}
		})
	}
	t.Run("unknown_tool", func(t *testing.T) {
		if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "unknown", Arguments: map[string]any{}}); err == nil {
			t.Fatal("expected unknown tool error")
		}
	})
}

func assertText(t *testing.T, result *mcp.CallToolResult, want string, wantError bool) {
	t.Helper()
	if result == nil || result.IsError != wantError || len(result.Content) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok || text.Text != want {
		t.Fatalf("expected text %q, got %+v", want, result.Content[0])
	}
}

func TestGatewayPreservesDownstreamResult(t *testing.T) {
	gateway := buildExecutable(t, "./cmd/gateway")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stopped := filepath.Join(t.TempDir(), "downstream-stopped")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, gateway, "-downstream-command", helper, "-data-command", helper)
	command.Env = append(os.Environ(), "MCP_TEST_DOWNSTREAM=result", "MCP_TEST_STOPPED="+stopped)
	command.Stderr = os.Stderr
	client := mcpclient.New("result-test", os.Getenv("GATEWAY_DEVELOPER_TOKEN"))
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 5 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close gateway: %v", err)
		}
		if content, err := os.ReadFile(stopped); err != nil || string(content) != "stopped\nstopped\n" {
			t.Errorf("downstream did not shut down cleanly: %q, %v", content, err)
		}
	}()
	for _, message := range []string{"distinctive", "fail"} {
		t.Run(message, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "echo", Arguments: map[string]any{"message": message}})
			if err != nil {
				t.Fatal(err)
			}
			assertText(t, result, "downstream received: "+message, message == "fail")
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var structured map[string]string
			if err := json.Unmarshal(data, &structured); err != nil {
				t.Fatal(err)
			}
			if len(structured) != 2 || structured["received"] != message || structured["source"] != "downstream" {
				t.Fatalf("structured result changed: %s", data)
			}
		})
	}
}

func TestGatewayStartupFailure(t *testing.T) {
	gateway := buildExecutable(t, "./cmd/gateway")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, downstream string }{
		{"missing_executable", filepath.Join(t.TempDir(), "missing")},
		{"exits_before_handshake", helper},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, gateway, "-downstream-command", tc.downstream)
			command.Env = append(os.Environ(), "MCP_TEST_DOWNSTREAM=exit")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err == nil {
				t.Fatal("gateway should fail to start")
			}
			if ctx.Err() != nil {
				t.Fatalf("gateway did not exit promptly: %v", ctx.Err())
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "connect to downstream") {
				t.Fatalf("expected stderr startup diagnostic and empty stdout; stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func TestGatewayCLI(t *testing.T) {
	for _, args := range [][]string{{"-h"}, {"-downstream-command", ""}, {"-data-command", ""}, {"-utility-command", "a", "-downstream-command", "b"}, {"unexpected"}} {
		var stderr bytes.Buffer
		err := run(t.Context(), args, &stderr)
		if args[0] == "-h" {
			if err != nil || !strings.Contains(stderr.String(), "downstream-command") {
				t.Fatalf("help: %v, %q", err, stderr.String())
			}
		} else if err == nil {
			t.Fatalf("expected invalid arguments to fail: %v", args)
		}
	}
}

func assertGatewayTools(t *testing.T, tools []*mcp.Tool) {
	t.Helper()
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if len(tools) != 5 || !names["echo"] || !names["utility.echo"] || !names["utility.add"] || !names["data.lookup_record"] || !names["admin.mock_destructive_action"] {
		t.Fatalf("unexpected gateway tools: %+v", names)
	}
}

func TestGatewayRoutesAcrossMultipleServers(t *testing.T) {
	gateway := buildExecutable(t, "./cmd/gateway")
	utility := buildExecutable(t, "./cmd/mock-server")
	data := buildExecutable(t, "./cmd/data-server")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, gateway, "-utility-command", utility, "-data-command", data)
	command.Dir = t.TempDir()
	command.Stderr = os.Stderr
	client := mcpclient.New("routing-test", os.Getenv("GATEWAY_DEVELOPER_TOKEN"))
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command, TerminateDuration: 5 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertGatewayTools(t, listed.Tools)
	for _, tc := range []struct {
		name, tool       string
		args             map[string]any
		text, structured string
		wantError        bool
	}{
		{"utility_echo", "utility.echo", map[string]any{"message": "hello across servers"}, "hello across servers", "", false},
		{"utility_add", "utility.add", map[string]any{"a": 4, "b": 7}, "11", `{"sum":11}`, false},
		{"data_lookup", "data.lookup_record", map[string]any{"id": 2}, "Bob", `{"id":2,"name":"Bob"}`, false},
		{"data_error", "data.lookup_record", map[string]any{"id": 99}, "record not found", "", true},
		{"utility_after_data", "utility.echo", map[string]any{"message": "still utility"}, "still utility", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			assertText(t, result, tc.text, tc.wantError)
			if tc.structured != "" {
				encoded, err := json.Marshal(result.StructuredContent)
				if err != nil {
					t.Fatal(err)
				}
				if string(encoded) != tc.structured {
					t.Fatalf("expected %s, got %s", tc.structured, encoded)
				}
			}
		})
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"utility.add", map[string]any{"a": 4}},
		{"utility.add", map[string]any{"a": "4", "b": 7}},
		{"data.lookup_record", map[string]any{"id": 2.5}},
	} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if err != nil {
			t.Fatal(err)
		}
		if !result.IsError {
			t.Fatalf("expected invalid %s arguments to fail", tc.name)
		}
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "unknown.tool", Arguments: map[string]any{}}); err == nil {
		t.Fatal("expected unknown tool protocol error")
	}
}

func TestGatewayPartialStartupCleanup(t *testing.T) {
	gateway := buildExecutable(t, "./cmd/gateway")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stopped := filepath.Join(t.TempDir(), "utility-stopped")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, gateway, "-utility-command", helper, "-data-command", filepath.Join(t.TempDir(), "missing-data-server"))
	command.Env = append(os.Environ(), "MCP_TEST_DOWNSTREAM=result", "MCP_TEST_STOPPED="+stopped)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err == nil {
		t.Fatal("gateway must fail when data cannot start")
	}
	if ctx.Err() != nil {
		t.Fatalf("startup cleanup timed out: %v", ctx.Err())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "connect to downstream data") {
		t.Fatalf("unexpected startup output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if content, err := os.ReadFile(stopped); err != nil || string(content) != "stopped\n" {
		t.Fatalf("utility was not closed after data startup failure: %q, %v", content, err)
	}
}

func testGate(t *testing.T) *security.Gate {
	t.Helper()
	auth, err := security.NewAuth(map[string]string{"viewer": os.Getenv("GATEWAY_VIEWER_TOKEN"), "developer": os.Getenv("GATEWAY_DEVELOPER_TOKEN"), "admin": os.Getenv("GATEWAY_ADMIN_TOKEN")}, os.Getenv("GATEWAY_APPROVER_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	return security.NewGate(auth, security.NewAudit(io.Discard), 20, nil)
}

func recordFixtureCall(tool string) error {
	path := os.Getenv("MCP_TEST_CALLS")
	if path == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(tool + "\n")
	return errors.Join(err, file.Close())
}
