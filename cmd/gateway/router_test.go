package main

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRouteRegistration(t *testing.T) {
	r := make(router)
	target := route{Name: "utility.echo", ServerID: "utility", ToolName: "echo"}
	if err := r.register(target); err != nil {
		t.Fatal(err)
	}
	if got, ok := r["utility.echo"]; !ok || got.ServerID != "utility" || got.ToolName != "echo" {
		t.Fatalf("wrong route: %+v, %v", got, ok)
	}
	if _, ok := r["unknown.tool"]; ok {
		t.Fatal("unknown route found")
	}
	if err := r.register(route{Name: "utility.echo", ServerID: "data", ToolName: "lookup_record"}); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate rejection: %v", err)
	}
	if r["utility.echo"].ServerID != "utility" {
		t.Fatal("duplicate replaced original route")
	}
	if err := r.register(route{Name: "incomplete"}); err == nil {
		t.Fatal("accepted incomplete route")
	}
}

func TestGatewayRejectsInvalidRoutes(t *testing.T) {
	routes := defaultRoutes()
	// No requests are sent: validation must fail before tool registration.
	sessions := map[string]*mcp.ClientSession{"utility": new(mcp.ClientSession), "data": new(mcp.ClientSession)}
	if _, err := newGateway(sessions, append(routes, routes[0]), testGate(t)); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("expected duplicate configuration failure: %v", err)
	}
	delete(sessions, "data")
	if _, err := newGateway(sessions, routes, testGate(t)); err == nil || !strings.Contains(err.Error(), "unavailable server") {
		t.Fatalf("expected missing session failure: %v", err)
	}
}
