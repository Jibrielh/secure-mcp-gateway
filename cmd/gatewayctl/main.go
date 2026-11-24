package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/mcpclient"
	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func run(ctx context.Context, args []string, stdout, stderr io.Writer) (err error) {
	flags := flag.NewFlagSet("gatewayctl", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("gateway-url", "http://127.0.0.1:8080/mcp", "local gateway MCP URL")
	tool := flags.String("tool", "", "MCP tool to call")
	arguments := flags.String("args", "{}", "JSON object of tool arguments")
	approval := flags.String("approval", "", "approval ID for a retry of the same tool and arguments")
	list := flags.Bool("list-approvals", false, "operator: review approval records")
	decision := flags.String("decision", "", "operator: approve or deny")
	request := flags.String("request", "", "operator: approval request ID")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	if err := mcpclient.ValidateURL(*endpoint); err != nil {
		return err
	}
	if *list || *decision != "" {
		if *tool != "" || *approval != "" || (*list && *decision != "") {
			return errors.New("choose a single tool or approval operation")
		}
		token := os.Getenv("GATEWAY_APPROVER_TOKEN")
		if token == "" {
			return errors.New("GATEWAY_APPROVER_TOKEN is required in the operator terminal")
		}
		parsed, _ := url.Parse(*endpoint)
		parsed.Path = "/approvals"
		method := http.MethodGet
		var body io.Reader
		if *decision != "" {
			if (*decision != "approve" && *decision != "deny") || len(*request) != 32 || strings.Trim(*request, "0123456789abcdef") != "" {
				return errors.New("provide -decision approve|deny and a valid -request ID")
			}
			parsed.Path += "/" + *request
			method = http.MethodPost
			encoded, _ := json.Marshal(map[string]string{"decision": *decision})
			body = bytes.NewReader(encoded)
		}
		req, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := mcpclient.HTTPClient(token).Do(req)
		if err != nil {
			return errors.New("approval API request failed")
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("approval API returned HTTP %d", res.StatusCode)
		}
		_, err = io.Copy(stdout, io.LimitReader(res.Body, 1<<20))
		return err
	}
	if *tool == "" {
		return errors.New("provide -tool, -list-approvals, or -decision")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(*arguments), &object); err != nil || object == nil {
		return errors.New("-args must be a JSON object")
	}
	session, err := mcpclient.Connect(ctx, *endpoint, os.Getenv("GATEWAY_TOKEN"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	params := &mcp.CallToolParams{Name: *tool, Arguments: json.RawMessage(*arguments)}
	if *approval != "" {
		params.Meta = mcp.Meta{security.ApprovalMeta: *approval}
	}
	result, err := session.CallTool(ctx, params)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return err
	}
	if result.IsError {
		return errors.New("gateway tool request rejected or failed; see result and request ID")
	}
	return nil
}
func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
