package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/Jibrielh/secure-mcp-gateway/internal/agent"
	"github.com/Jibrielh/secure-mcp-gateway/internal/metrics"
	"github.com/Jibrielh/secure-mcp-gateway/internal/security"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

type Server struct {
	DB            DB
	DevLogin      bool
	DevPassword   string
	PublicURL     string
	OIDC          *OIDC
	Delegation    *security.Delegation
	GatewayURL    string
	ApproverToken string
	Provider      agent.Provider
	ProviderName  string
	Model         string
	Ollama        *agent.Ollama
	Metrics       *metrics.Registry
	RedisPing     func(context.Context) error
	WebDir        string
	SecureCookie  bool
	Caller        agent.Caller
	broker        broker
	cancels       sync.Map
}

type broker struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

func (b *broker) notify(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[runID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
func (b *broker) subscribe(runID string) chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[string]map[chan struct{}]struct{}{}
	}
	ch := make(chan struct{}, 1)
	if b.subs[runID] == nil {
		b.subs[runID] = map[chan struct{}]struct{}{}
	}
	b.subs[runID][ch] = struct{}{}
	return ch
}
func (b *broker) unsubscribe(runID string, ch chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs[runID], ch)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/session", s.session)
	mux.HandleFunc("GET /api/v1/auth/login", s.login)
	mux.HandleFunc("GET /api/v1/auth/callback", s.callback)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
	mux.HandleFunc("POST /api/v1/auth/dev-login", s.devLogin)
	mux.HandleFunc("GET /api/v1/agents", s.listAgents)
	mux.HandleFunc("POST /api/v1/agents", s.createAgent)
	mux.HandleFunc("GET /api/v1/agents/{id}", s.getAgent)
	mux.HandleFunc("PATCH /api/v1/agents/{id}", s.updateAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/clone", s.cloneAgent)
	mux.HandleFunc("POST /api/v1/agents/{id}/disable", s.disableAgent)
	mux.HandleFunc("GET /api/v1/tools", s.tools)
	mux.HandleFunc("GET /api/v1/integrations", s.integrations)
	mux.HandleFunc("POST /api/v1/integrations/{id}", s.setIntegration)
	mux.HandleFunc("GET /api/v1/conversations", s.listConversations)
	mux.HandleFunc("POST /api/v1/conversations", s.createConversation)
	mux.HandleFunc("GET /api/v1/conversations/{id}", s.getConversation)
	mux.HandleFunc("DELETE /api/v1/conversations/{id}", s.deleteConversation)
	mux.HandleFunc("POST /api/v1/conversations/{id}/runs", s.startRun)
	mux.HandleFunc("GET /api/v1/runs/{id}", s.getRun)
	mux.HandleFunc("GET /api/v1/runs/{id}/events", s.events)
	mux.HandleFunc("POST /api/v1/runs/{id}/cancel", s.cancelRun)
	mux.HandleFunc("POST /api/v1/runs/{id}/resume", s.resumeRun)
	mux.HandleFunc("GET /api/v1/approvals", s.listApprovals)
	mux.HandleFunc("GET /api/v1/approvals/{id}", s.getApproval)
	mux.HandleFunc("POST /api/v1/approvals/{id}/decision", s.decideApproval)
	mux.HandleFunc("GET /api/v1/policies", s.getPolicies)
	mux.HandleFunc("PUT /api/v1/policies", s.putPolicies)
	mux.HandleFunc("GET /api/v1/audit", s.audit)
	mux.HandleFunc("GET /api/v1/analytics", s.analytics)
	mux.HandleFunc("GET /api/v1/findings", s.listFindings)
	mux.HandleFunc("POST /api/v1/findings/scan", s.scanFindings)
	mux.HandleFunc("POST /api/v1/findings/{id}/status", s.findingStatus)
	mux.HandleFunc("POST /api/v1/findings/{id}/explain", s.explainFinding)
	mux.HandleFunc("GET /api/v1/settings", s.settings)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]string{"status": "live"}) })
	mux.HandleFunc("GET /readyz", s.ready)
	if s.Metrics != nil {
		mux.Handle("GET /metrics", s.Metrics.Handler())
	}
	mux.Handle("/", http.FileServer(http.Dir(s.WebDir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		if origin := r.Header.Get("Origin"); origin != "" && origin != strings.TrimRight(s.PublicURL, "/") {
			writeJSON(w, 403, map[string]string{"error": "origin_rejected"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func readJSON(r *http.Request, dest any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing json")
	}
	return nil
}

func (s *Server) user(r *http.Request) (store.User, store.Session, bool) {
	cookie, err := r.Cookie("agentgate_session")
	if err != nil || cookie.Value == "" {
		return store.User{}, store.Session{}, false
	}
	session, err := s.DB.FindSession(r.Context(), cookie.Value)
	if err != nil {
		return store.User{}, store.Session{}, false
	}
	user, err := s.DB.FindUser(r.Context(), session.UserID)
	if err != nil {
		return store.User{}, store.Session{}, false
	}
	return user, session, true
}

func (s *Server) require(w http.ResponseWriter, r *http.Request) (store.User, store.Session, bool) {
	user, session, ok := s.user(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthenticated"})
		return store.User{}, store.Session{}, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRFToken)) != 1 {
			writeJSON(w, 403, map[string]string{"error": "csrf_required"})
			return store.User{}, store.Session{}, false
		}
	}
	return user, session, true
}

func (s *Server) setSession(w http.ResponseWriter, session store.Session) {
	http.SetCookie(w, &http.Cookie{
		Name: "agentgate_session", Value: session.ID, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.SecureCookie, Expires: session.ExpiresAt,
	})
}

func (s *Server) passwordOK(password string) bool {
	if !s.DevLogin || len(s.DevPassword) < 12 {
		return false
	}
	got := sha256.Sum256([]byte(password))
	want := sha256.Sum256([]byte(s.DevPassword))
	return subtle.ConstantTimeCompare(got[:], want[:]) == 1
}

func randomToken() string {
	var raw [32]byte
	_, _ = rand.Read(raw[:])
	return hex.EncodeToString(raw[:])
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.Ping(r.Context()); err != nil {
		writeJSON(w, 503, map[string]string{"status": "not_ready", "dependency": "database"})
		return
	}
	if s.RedisPing != nil {
		if err := s.RedisPing(r.Context()); err != nil {
			writeJSON(w, 503, map[string]string{"status": "not_ready", "dependency": "redis"})
			return
		}
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

func owns(user store.User, ownerID string) bool {
	return user.Role == "admin" || user.ID == ownerID
}
