package mcpclient

import (
	"reflect"
	"testing"
)

func TestDownstreamEnvironment(t *testing.T) {
	got := DownstreamEnv([]string{"PATH=/bin", "GATEWAY_ADMIN_TOKEN=private", "GATEWAY_APPROVER_TOKEN=private", "GATEWAY_TOKEN=private", "OPENAI_API_KEY=private", "GITHUB_TOKEN=private", "MCP_TEST_CALLS=fixture"})
	if !reflect.DeepEqual(got, []string{"PATH=/bin", "MCP_TEST_CALLS=fixture"}) {
		t.Fatal("application credentials leaked to downstream environment")
	}
}
func TestLocalGatewayURL(t *testing.T) {
	for _, url := range []string{"http://127.0.0.1:8080/mcp", "http://[::1]:8080/mcp"} {
		if err := ValidateURL(url); err != nil {
			t.Fatal(err)
		}
	}
	for _, url := range []string{"http://example.com/mcp", "https://example.com/mcp", "http://user:pass@127.0.0.1/mcp", "http://127.0.0.1/mcp?token=private"} {
		if err := ValidateURL(url); err == nil {
			t.Fatal("accepted nonlocal or credential-bearing URL")
		}
	}
}
