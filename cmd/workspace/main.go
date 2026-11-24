package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/agent"
	"github.com/Jibrielh/secure-mcp-gateway/internal/app"
	"github.com/Jibrielh/secure-mcp-gateway/internal/authn"
	"github.com/Jibrielh/secure-mcp-gateway/internal/metrics"
	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "loopback address for the workspace")
	listenMode := flag.String("listen-mode", "loopback", "loopback or container")
	webDir := flag.String("web-dir", "web/dist", "built frontend directory")
	storeMode := flag.String("store", "memory", "memory or postgres")
	providerName := flag.String("provider", "fake", "fake or ollama")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var db app.DB
	if *storeMode == "postgres" {
		opened, err := store.OpenPostgres(ctx, os.Getenv("DATABASE_URL"))
		if err != nil {
			log.Fatal(err)
		}
		defer opened.Close()
		db = opened
	} else if *storeMode == "memory" {
		db = store.NewMemory()
	} else {
		log.Fatal("store must be memory or postgres")
	}
	host, _, err := net.SplitHostPort(*listen)
	ip := net.ParseIP(host)
	if err != nil || (*listenMode == "loopback" && (ip == nil || !ip.IsLoopback())) || (*listenMode != "loopback" && (*listenMode != "container" || ip == nil)) {
		log.Fatal("workspace listen address must be a literal IP; non-loopback requires -listen-mode=container")
	}
	publicURL := env("AGENTGATE_PUBLIC_URL", "http://"+*listen)
	server := &app.Server{
		DB: db, DevLogin: os.Getenv("AGENTGATE_DEV_LOGIN") == "1", DevPassword: os.Getenv("AGENTGATE_DEV_PASSWORD"),
		PublicURL: publicURL, GatewayURL: os.Getenv("AGENTGATE_GATEWAY_URL"), ApproverToken: os.Getenv("GATEWAY_APPROVER_TOKEN"),
		ProviderName: *providerName, Model: env("OLLAMA_MODEL", "qwen2.5:3b"), Metrics: metrics.New(), WebDir: *webDir,
		SecureCookie: strings.HasPrefix(publicURL, "https://"),
	}
	if secret := os.Getenv("GATEWAY_INTERNAL_SECRET"); secret != "" {
		delegation, err := security.NewDelegation(secret)
		if err != nil {
			log.Fatal(err)
		}
		server.Delegation = delegation
	}
	if *providerName == "ollama" {
		ollama, err := agent.NewOllama(env("OLLAMA_URL", "http://127.0.0.1:11434"))
		if err != nil {
			log.Fatal(err)
		}
		server.Ollama = ollama
		server.Provider = ollama
	} else {
		server.Provider = agent.Fake{}
		server.ProviderName = "fake"
	}
	if os.Getenv("OIDC_ISSUER") != "" && os.Getenv("OIDC_CLIENT_ID") != "" {
		server.OIDC = &app.OIDC{
			Issuer: os.Getenv("OIDC_ISSUER"), BrowserIssuer: os.Getenv("OIDC_BROWSER_ISSUER"),
			ClientID: os.Getenv("OIDC_CLIENT_ID"), ClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
			RedirectURL: env("OIDC_REDIRECT_URL", publicURL+"/api/v1/auth/callback"),
			Validator:   authn.NewValidator(os.Getenv("OIDC_ISSUER"), env("OIDC_AUDIENCE", os.Getenv("OIDC_CLIENT_ID"))),
		}
	}
	if os.Getenv("REDIS_URL") != "" {
		server.RedisPing = func(ctx context.Context) error {
			return pingRedis(ctx, os.Getenv("REDIS_URL"))
		}
	}
	if err := server.SeedCatalog(ctx); err != nil || server.SeedDev(ctx) != nil {
		log.Fatal("seed failed")
	}
	httpServer := &http.Server{Addr: *listen, Handler: server.Handler(), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdown)
	}()
	log.Printf("workspace listening on http://%s", *listen)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
