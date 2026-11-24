package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/demo"
	"github.com/Jibrielh/secure-mcp-gateway/internal/mcpclient"
)

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) (err error) {
	flags := flag.NewFlagSet("demo-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	endpoint := flags.String("gateway-url", "http://127.0.0.1:8080/mcp", "local gateway MCP URL")
	model := flags.String("model", os.Getenv("OPENAI_MODEL"), "OpenAI model (default gpt-5-mini)")
	wait := flags.Bool("wait-approval", true, "wait for an operator to decide a pending approval, then retry once")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if os.Getenv("OPENAI_API_KEY") == "" {
		return errors.New("OPENAI_API_KEY is required; set it in your environment. Live API usage is billed separately")
	}
	if flags.NArg() == 0 {
		return errors.New("provide a natural-language prompt, e.g. 'Add 22 and 35.'")
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	session, err := mcpclient.Connect(connectCtx, *endpoint, os.Getenv("GATEWAY_TOKEN"))
	cancel()
	if err != nil {
		return fmt.Errorf("connect to gateway: %w", err)
	}
	defer func() { err = errors.Join(err, session.Close()) }()
	agent := demo.Agent{APIKey: os.Getenv("OPENAI_API_KEY"), Model: *model, MCP: session}
	if *wait {
		reader := bufio.NewReader(stdin)
		agent.WaitForApproval = func(id string) error {
			fmt.Fprintf(stderr, "Approval required: %s\nIn the separate operator terminal, review with gatewayctl -list-approvals, then approve or deny this ID. Press Enter here after the decision to retry once.\n", id)
			_, err := reader.ReadString('\n')
			if err != nil {
				return errors.New("no human confirmation received; action remains unexecuted")
			}
			return nil
		}
	}
	answer, err := agent.Run(ctx, strings.Join(flags.Args(), " "))
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, answer)
	return err
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
