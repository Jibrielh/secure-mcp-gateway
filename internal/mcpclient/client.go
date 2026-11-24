package mcpclient

import (
	"context"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func New(name, token string) *mcp.Client {
	client := mcp.NewClient(&mcp.Implementation{Name: name, Version: "0.4.0"}, nil)
	client.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if params := req.GetParams(); params != nil && !strings.HasPrefix(method, "notifications/") {
				meta := maps.Clone(params.GetMeta())
				if meta == nil {
					meta = map[string]any{}
				}
				meta[security.AuthorizationMeta] = "Bearer " + token
				params.SetMeta(meta)
			}
			return next(ctx, method, req)
		}
	})
	return client
}

type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (t bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header = req.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}
func HTTPClient(token string) *http.Client {
	return &http.Client{Transport: bearerTransport{token, http.DefaultTransport}, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirects are disabled") }}
}

func ValidateURL(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("gateway URL must be a loopback HTTP URL without credentials, query, or fragment")
	}
	host := net.ParseIP(parsed.Hostname())
	if parsed.Scheme != "http" || host == nil || !host.IsLoopback() {
		return errors.New("gateway URL must use http and a literal loopback IP")
	}
	return nil
}
func Connect(ctx context.Context, endpoint, token string) (*mcp.ClientSession, error) {
	if token == "" {
		return nil, errors.New("GATEWAY_TOKEN is required")
	}
	if err := ValidateURL(endpoint); err != nil {
		return nil, err
	}
	// HTTP uses the standard Authorization header, never MCP metadata for auth.
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-demo-client", Version: "0.4.0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: HTTPClient(token)}, nil)
}

// Do not pass this application's credentials to local downstream subprocesses.
// This is not an OS sandbox or a guarantee about arbitrary unrelated env vars.
func DownstreamEnv(env []string) []string {
	result := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strippedEnvKey(key) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func strippedEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	switch {
	case strings.HasPrefix(upper, "GATEWAY_"), strings.HasPrefix(upper, "OPENAI_"), strings.HasPrefix(upper, "GITHUB_"):
		return true
	case strings.HasPrefix(upper, "AGENTGATE_"), strings.HasPrefix(upper, "OIDC_"), strings.HasPrefix(upper, "DATABASE_"):
		return true
	case strings.HasPrefix(upper, "REDIS_"), strings.HasPrefix(upper, "POSTGRES"), strings.HasPrefix(upper, "AWS_"):
		return true
	case strings.Contains(upper, "PASSWORD"), strings.Contains(upper, "SECRET"), strings.HasSuffix(upper, "_TOKEN"):
		return true
	default:
		return false
	}
}

// ChildEnv starts from the filtered parent environment and appends connector-specific variables.
func ChildEnv(base []string, extra []string) []string {
	env := DownstreamEnv(base)
	for _, entry := range extra {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \t") || strings.ContainsAny(value, "\r\n") {
			continue
		}
		env = append(env, key+"="+value)
	}
	return env
}
