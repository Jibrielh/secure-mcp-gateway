package policy

import "testing"

func TestProviderNamesAreReversible(t *testing.T) {
	seen := map[string]string{}
	for _, tool := range All() {
		if tool.ProviderName != ProviderName(tool.Name) {
			t.Fatalf("%s provider name", tool.Name)
		}
		if other, ok := seen[tool.ProviderName]; ok {
			t.Fatalf("provider collision %s and %s", other, tool.Name)
		}
		seen[tool.ProviderName] = tool.Name
		got, ok := ByProvider(tool.ProviderName)
		if !ok || got.Name != tool.Name {
			t.Fatalf("reverse %s", tool.ProviderName)
		}
	}
	if _, ok := RouteName("utility.add"); ok {
		t.Fatal("accepted a raw MCP name as a provider name")
	}
	if _, ok := ByProvider("no_such__tool"); ok {
		t.Fatal("unknown provider mapped to a route")
	}
}

func TestBuiltinRiskAndRoles(t *testing.T) {
	tool, ok := ByName("admin.mock_destructive_action")
	if !ok || tool.Risk != "high" {
		t.Fatal("high-risk mock missing")
	}
	for _, name := range []string{"utility.add", "files.read", "github.list_issues"} {
		if _, ok := ByName(name); !ok {
			t.Fatal(name)
		}
	}
}
