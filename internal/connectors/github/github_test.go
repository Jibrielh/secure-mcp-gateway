package github

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadOnlyClientUsesFixture(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatal(r.Method)
		}
		switch r.URL.Path {
		case "/repos/golang/go/issues":
			_, _ = w.Write([]byte(`[{"number":1,"title":"Example","state":"open","html_url":"http://example.test/1"}]`))
		case "/search/issues":
			_, _ = w.Write([]byte(`{"items":[{"number":2,"title":"Search","state":"open"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	issues, err := client.ListIssues(t.Context(), "golang", "go", 5)
	if err != nil || len(issues) != 1 || issues[0].Title != "Example" {
		t.Fatal(err, issues)
	}
	if _, err := client.ListIssues(t.Context(), "../etc", "go", 5); err == nil {
		t.Fatal("accepted unsafe owner")
	}
	if _, err := New("https://example.com", ""); err != nil {
		t.Fatal("public https base should be configurable")
	}
	if _, err := New("http://example.com", ""); err == nil {
		t.Fatal("accepted non-local http")
	}
}
