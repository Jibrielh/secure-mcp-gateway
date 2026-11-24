package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func serveHTTP(ctx context.Context, address, mode string, server *mcp.Server, gate *security.Gate, diagnostics io.Writer) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("-listen must be a host and port")
	}
	ip := net.ParseIP(host)
	if mode == "loopback" {
		if ip == nil || !ip.IsLoopback() {
			return errors.New("-listen must bind only to a literal loopback IP")
		}
	} else if mode != "container" || ip == nil {
		return errors.New("-listen-mode must be loopback or container, and container mode still requires a literal IP")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if err := json.NewEncoder(diagnostics).Encode(map[string]string{"event": "listening", "address": listener.Addr().String()}); err != nil {
		return errors.Join(err, listener.Close())
	}
	gate.ApprovalEnabled = true
	httpServer := &http.Server{Handler: gate.HTTPHandler(server), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := httpServer.Shutdown(shutdownCtx)
		if err != nil {
			err = errors.Join(err, httpServer.Close())
		}
		<-done
		return err
	}
}
