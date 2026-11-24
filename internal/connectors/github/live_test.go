package github

import (
	"os"
	"testing"
)

func TestPublicRepositoryLive(t *testing.T) {
	if os.Getenv("AGENTGATE_GITHUB_LIVE") == "" {
		t.Skip("set AGENTGATE_GITHUB_LIVE=1 to read a public repository")
	}
	client, err := New("https://api.github.com", os.Getenv("GITHUB_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	issues, err := client.ListIssues(t.Context(), "golang", "go", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number < 1 || issues[0].Title == "" {
		t.Fatalf("%+v", issues)
	}
}
