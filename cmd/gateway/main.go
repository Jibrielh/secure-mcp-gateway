package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/bridge"
	"github.com/Jibrielh/secure-mcp-gateway/internal/mcpclient"
	"github.com/Jibrielh/secure-mcp-gateway/internal/policy"
	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const downstreamTimeout = 10 * time.Second

func newGateway(sessions map[string]*mcp.ClientSession, routes []route, gate *security.Gate) (*mcp.Server, error) {
	if gate == nil {
		return nil, errors.New("security configuration is required")
	}
	router := make(router)
	for _, target := range routes {
		if err := router.register(target); err != nil {
			return nil, err
		}
		if sessions[target.ServerID] == nil {
			return nil, fmt.Errorf("route %q references unavailable server %q", target.Name, target.ServerID)
		}
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "secure-mcp-gateway", Version: "0.4.0"}, nil)
	for _, target := range routes {
		mcp.AddTool(server, &mcp.Tool{Name: target.Name, Description: target.Description, InputSchema: target.InputSchema},
			func(ctx context.Context, req *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
				target, ok := router[req.Params.Name]
				if !ok {
					return nil, nil, errors.New("unknown tool route")
				}
				ctx, cancel := context.WithTimeout(ctx, downstreamTimeout)
				defer cancel()
				result, err := sessions[target.ServerID].CallTool(ctx, &mcp.CallToolParams{
					Name: target.ToolName, Arguments: req.Params.Arguments,
				})
				if err == nil && result != nil {
					result = limitToolResult(result, 64*1024)
				}
				if err != nil {
					// Transport/protocol errors can contain downstream details. Keep those
					// out of tool output; actual MCP tool-error results pass through below.
					if ctx.Err() != nil {
						return nil, nil, fmt.Errorf("downstream %s call interrupted: %w", target.Name, ctx.Err())
					}
					return nil, nil, fmt.Errorf("downstream %s call failed: connection or protocol error", target.Name)
				}
				return result, nil, nil
			})
	}
	gate.Policies = make(map[string]security.Policy)
	for _, target := range routes {
		gate.Policies[target.Name] = security.Policy{Server: target.ServerID, Risk: target.Risk, Roles: target.Roles}
	}
	server.AddReceivingMiddleware(gate.Middleware)
	return server, nil
}

func run(ctx context.Context, args []string, stderr io.Writer) (err error) {
	flags := flag.NewFlagSet("gateway", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", "", "optional loopback HTTP address for /mcp and /approvals (otherwise stdio)")
	listenMode := flags.String("listen-mode", "loopback", "loopback or container; container is required for a non-loopback bind and must not be published on the host")
	limit := flags.Int("rate-limit", 20, "authorized tool attempts per identity per minute")
	limiterMode := flags.String("limiter", "memory", "memory or redis; redis fails closed when unavailable")
	storeMode := flags.String("store", "memory", "memory or postgres")
	var utilityCommand string
	flags.StringVar(&utilityCommand, "utility-command", "./bin/mock-server", "path to the utility MCP executable")
	flags.StringVar(&utilityCommand, "downstream-command", "./bin/mock-server", "compatibility alias for -utility-command")
	dataCommand := flags.String("data-command", "./bin/data-server", "path to the data MCP executable")
	githubCommand := flags.String("github-command", "", "optional read-only GitHub MCP executable")
	githubBase := flags.String("github-base-url", "https://api.github.com", "GitHub API base URL passed to the GitHub connector")
	sampleCommand := flags.String("sampledb-command", "", "optional sample analytics MCP executable")
	sampleDSN := flags.String("sampledb-dsn", "file:fixtures/sample.db", "sqlite DSN for the sample analytics connector")
	filesCommand := flags.String("files-command", "", "optional workspace files MCP executable")
	filesRoot := flags.String("files-root", "fixtures/workspace", "demo workspace root passed to the files connector")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	utilityFlags := 0
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "utility-command" || f.Name == "downstream-command" {
			utilityFlags++
		}
	})
	if utilityFlags > 1 {
		return errors.New("use either -utility-command or -downstream-command, not both")
	}
	if flags.NArg() != 0 || utilityCommand == "" || *dataCommand == "" {
		return errors.New("provide nonempty downstream executable paths and no positional arguments")
	}

	if *limit < 1 {
		return errors.New("rate-limit must be positive")
	}
	auth, authErr := security.NewAuth(map[string]string{"viewer": os.Getenv("GATEWAY_VIEWER_TOKEN"), "developer": os.Getenv("GATEWAY_DEVELOPER_TOKEN"), "admin": os.Getenv("GATEWAY_ADMIN_TOKEN")}, os.Getenv("GATEWAY_APPROVER_TOKEN"))
	if authErr != nil {
		return fmt.Errorf("configure GATEWAY_VIEWER_TOKEN, GATEWAY_DEVELOPER_TOKEN, GATEWAY_ADMIN_TOKEN and GATEWAY_APPROVER_TOKEN: %w", authErr)
	}
	var authenticator security.Authenticator = auth
	if secret := os.Getenv("GATEWAY_INTERNAL_SECRET"); secret != "" {
		delegation, err := security.NewDelegation(secret)
		if err != nil {
			return err
		}
		authenticator = security.Chain{Primary: auth, Extra: []security.Authenticator{delegation}}
	}
	audit := security.NewAudit(stderr)
	gate := security.NewGate(authenticator, audit, *limit, nil)
	if *limiterMode == "redis" {
		limiter, err := openRedisLimiter(os.Getenv("REDIS_URL"), *limit)
		if err != nil {
			return err
		}
		defer limiter.Close()
		if err := limiter.Ping(ctx); err != nil {
			return fmt.Errorf("redis is required for -limiter=redis: %w", err)
		}
		gate.Shared = limiter
	} else if *limiterMode != "memory" {
		return errors.New("limiter must be memory or redis")
	}
	if *storeMode == "postgres" {
		db, err := openGatewayStore(ctx, os.Getenv("DATABASE_URL"))
		if err != nil {
			return fmt.Errorf("postgres is required for -store=postgres: %w", err)
		}
		defer db.Close()
		gate.Backend = bridge.Approvals{DB: db}
		gate.Restrictions = bridge.Restrictions{DB: db}
		audit.Sink = bridge.AuditSink(db)
	} else if *storeMode != "memory" {
		return errors.New("store must be memory or postgres")
	}
	type downstream struct {
		id, command string
		args        []string
		extra       []string
	}
	configs := []downstream{{id: "utility", command: utilityCommand}, {id: "data", command: *dataCommand}}
	if *githubCommand != "" {
		extra := []string{}
		if token := os.Getenv("GITHUB_TOKEN"); token != "" {
			extra = append(extra, "GITHUB_TOKEN="+token)
		}
		configs = append(configs, downstream{id: "github", command: *githubCommand, args: []string{"-base-url", *githubBase}, extra: extra})
	}
	if *sampleCommand != "" {
		configs = append(configs, downstream{id: "sampledb", command: *sampleCommand, args: []string{"-db", *sampleDSN}})
	}
	if *filesCommand != "" {
		configs = append(configs, downstream{id: "files", command: *filesCommand, args: []string{"-root", *filesRoot}})
	}
	sessions := make(map[string]*mcp.ClientSession)
	defer func() {
		// Also runs if a later downstream fails to initialize.
		for i := len(configs) - 1; i >= 0; i-- {
			id := configs[i].id
			if session := sessions[id]; session != nil {
				if closeErr := session.Close(); closeErr != nil && !childStopped(closeErr) {
					err = errors.Join(err, fmt.Errorf("close %s downstream session: %w", id, closeErr))
				}
			}
		}
	}()
	for _, config := range configs {
		// CommandTransport owns process shutdown; connection timeout is not
		// the child process lifetime. No shell interprets the executable path.
		command := exec.Command(config.command, config.args...)
		command.Stderr = stderr
		command.Env = mcpclient.ChildEnv(os.Environ(), config.extra)
		client := mcp.NewClient(&mcp.Implementation{Name: "gateway-" + config.id + "-client", Version: "0.4.0"}, nil)
		connectCtx, cancel := context.WithTimeout(ctx, downstreamTimeout)
		session, connectErr := client.Connect(connectCtx, &mcp.CommandTransport{
			Command: command, TerminateDuration: time.Second,
		}, nil)
		cancel()
		if connectErr != nil {
			return fmt.Errorf("connect to downstream %s at %q (build its binary or set its command flag): %w", config.id, config.command, connectErr)
		}
		sessions[config.id] = session
	}

	routes := defaultRoutes()
	if *githubCommand != "" || *sampleCommand != "" || *filesCommand != "" {
		for _, tool := range policy.Connectors() {
			switch tool.ServerID {
			case "github":
				if *githubCommand != "" {
					routes = append(routes, routesFrom([]policy.Tool{tool})...)
				}
			case "sampledb":
				if *sampleCommand != "" {
					routes = append(routes, routesFrom([]policy.Tool{tool})...)
				}
			case "files":
				if *filesCommand != "" {
					routes = append(routes, routesFrom([]policy.Tool{tool})...)
				}
			}
		}
	}
	server, err := newGateway(sessions, routes, gate)
	if err != nil {
		return err
	}
	if *listen != "" {
		return serveHTTP(ctx, *listen, *listenMode, server, gate, stderr)
	}
	err = server.Run(ctx, &mcp.StdioTransport{})
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func childStopped(err error) bool {
	if errors.Is(err, context.Canceled) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		status, ok := exitErr.Sys().(syscall.WaitStatus)
		return ok && status.Signaled()
	}
	return false
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		log.Fatalf("gateway failed: %v", err)
	}
}
