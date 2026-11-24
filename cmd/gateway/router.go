package main

import (
	"encoding/json"
	"fmt"

	"github.com/Jibrielh/secure-mcp-gateway/internal/policy"
)

type route struct {
	Name        string
	ServerID    string
	ToolName    string
	Risk        string
	Roles       []string
	Description string
	InputSchema json.RawMessage
}

type router map[string]route

func (r router) register(target route) error {
	if target.Name == "" || target.ServerID == "" || target.ToolName == "" {
		return fmt.Errorf("route requires an upstream name, server ID, and downstream tool name")
	}
	if _, exists := r[target.Name]; exists {
		return fmt.Errorf("duplicate upstream tool route %q", target.Name)
	}
	r[target.Name] = target
	return nil
}

func routesFrom(tools []policy.Tool) []route {
	routes := make([]route, 0, len(tools))
	for _, tool := range tools {
		routes = append(routes, route{
			Name: tool.Name, ServerID: tool.ServerID, ToolName: tool.ToolName,
			Risk: tool.Risk, Roles: append([]string(nil), tool.Roles...),
			Description: tool.Description, InputSchema: tool.InputSchema,
		})
	}
	return routes
}

func defaultRoutes() []route {
	return routesFrom(policy.Builtin())
}
