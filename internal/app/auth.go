package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Jibrielh/secure-mcp-gateway/internal/authn"
	"github.com/Jibrielh/secure-mcp-gateway/internal/policy"
	"github.com/Jibrielh/secure-mcp-gateway/internal/store"
)

type OIDC struct {
	Issuer        string
	BrowserIssuer string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Validator     *authn.Validator
	Client        *http.Client
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	user, session, ok := s.user(r)
	if !ok {
		writeJSON(w, 200, map[string]any{"authenticated": false, "dev_login": s.DevLogin, "oidc": s.OIDC != nil})
		return
	}
	writeJSON(w, 200, map[string]any{"authenticated": true, "user": publicUser(user), "csrf_token": session.CSRFToken, "dev_login": s.DevLogin, "oidc": s.OIDC != nil})
}

func publicUser(user store.User) map[string]any {
	return map[string]any{"id": user.ID, "username": user.Username, "display_name": user.DisplayName, "role": user.Role, "approver": user.Role == "approver"}
}

func (s *Server) devLogin(w http.ResponseWriter, r *http.Request) {
	if !s.DevLogin {
		writeJSON(w, 404, map[string]string{"error": "dev_login_disabled"})
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &input); err != nil || !s.passwordOK(input.Password) {
		writeJSON(w, 401, map[string]string{"error": "invalid_login"})
		return
	}
	user, err := s.DB.FindUserByUsername(r.Context(), input.Username)
	if err != nil || !strings.HasPrefix(user.Subject, "dev:") {
		writeJSON(w, 401, map[string]string{"error": "invalid_login"})
		return
	}
	s.startSession(w, r, user)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user store.User) {
	_ = s.ensureAgents(r.Context(), user)
	session := store.Session{ID: randomToken(), UserID: user.ID, CSRFToken: randomToken(), ExpiresAt: time.Now().Add(12 * time.Hour).UTC(), CreatedAt: time.Now().UTC()}
	if err := s.DB.CreateSession(r.Context(), session); err != nil {
		writeJSON(w, 500, map[string]string{"error": "session_failed"})
		return
	}
	s.setSession(w, session)
	writeJSON(w, 200, map[string]any{"user": publicUser(user), "csrf_token": session.CSRFToken})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	user, session, ok := s.require(w, r)
	if !ok {
		return
	}
	_ = user
	_ = s.DB.DeleteSession(r.Context(), session.ID)
	http.SetCookie(w, &http.Cookie{Name: "agentgate_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.SecureCookie})
	writeJSON(w, 200, map[string]string{"status": "signed_out"})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		writeJSON(w, 404, map[string]string{"error": "oidc_not_configured"})
		return
	}
	state := randomToken()
	verifier := randomToken()
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	http.SetCookie(w, &http.Cookie{Name: "agentgate_oidc", Value: state + "." + verifier, Path: "/", HttpOnly: true, Secure: s.SecureCookie, MaxAge: 300, SameSite: http.SameSiteLaxMode})
	query := url.Values{}
	query.Set("response_type", "code")
	query.Set("client_id", s.OIDC.ClientID)
	query.Set("redirect_uri", s.OIDC.RedirectURL)
	query.Set("scope", "openid profile")
	query.Set("state", state)
	query.Set("code_challenge", challenge)
	query.Set("code_challenge_method", "S256")
	issuer := s.OIDC.BrowserIssuer
	if issuer == "" {
		issuer = s.OIDC.Issuer
	}
	http.Redirect(w, r, strings.TrimRight(issuer, "/")+"/protocol/openid-connect/auth?"+query.Encode(), http.StatusFound)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	if s.OIDC == nil {
		writeJSON(w, 404, map[string]string{"error": "oidc_not_configured"})
		return
	}
	cookie, err := r.Cookie("agentgate_oidc")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "oidc_state_missing"})
		return
	}
	state, verifier, ok := strings.Cut(cookie.Value, ".")
	if !ok || subtleMismatch(state, r.URL.Query().Get("state")) || r.URL.Query().Get("code") == "" {
		writeJSON(w, 400, map[string]string{"error": "oidc_state_mismatch"})
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "agentgate_oidc", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	token, err := s.OIDC.exchange(r.Context(), r.URL.Query().Get("code"), verifier)
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "oidc_token_rejected"})
		return
	}
	claims, err := s.OIDC.Validator.Validate(r.Context(), token)
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "oidc_token_rejected"})
		return
	}
	role, ok := authn.HighestRole(claims.Roles)
	if !ok {
		if authn.IsApprover(claims.Roles) {
			role = "approver"
		} else {
			writeJSON(w, 403, map[string]string{"error": "no_workspace_role"})
			return
		}
	}
	user, err := s.DB.FindUserBySubject(r.Context(), claims.Subject)
	if err != nil {
		username := claims.Username
		if username == "" {
			username = "oidc-" + store.NewID()[:8]
		}
		name := claims.Name
		if name == "" {
			name = username
		}
		user = store.User{ID: "user-" + store.NewID()[:12], Subject: claims.Subject, Username: username, DisplayName: name, Role: role, CreatedAt: time.Now().UTC()}
	} else {
		user.Role = role
		user.DisplayName = claims.Name
	}
	if err := s.DB.UpsertUser(r.Context(), user); err != nil {
		writeJSON(w, 500, map[string]string{"error": "user_persist_failed"})
		return
	}
	s.startSession(w, r, user)
}

func subtleMismatch(a, b string) bool { return a == "" || a != b }

func (o *OIDC) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", o.RedirectURL)
	form.Set("client_id", o.ClientID)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.Issuer, "/")+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(o.ClientID, o.ClientSecret)
	client := o.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var payload struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&payload); err != nil || res.StatusCode != 200 || payload.AccessToken == "" {
		return "", errors.New("token exchange failed")
	}
	return payload.AccessToken, nil
}

func (s *Server) ensureAgents(ctx context.Context, user store.User) error {
	existing, err := s.DB.ListAgents(ctx, user.ID)
	if err != nil || len(existing) > 0 || user.Role == "approver" {
		return err
	}
	now := time.Now().UTC()
	for _, template := range policy.Templates() {
		tools := template.Tools
		if user.Role == "viewer" {
			tools = []string{"utility.echo", "utility.add", "data.lookup_record", "files.list"}
			if template.Name != "Workspace Assistant" {
				continue
			}
		}
		agent := store.Agent{
			ID: "agent-" + store.NewID()[:12], OwnerUserID: user.ID, Name: template.Name, Description: template.Description,
			SystemPrompt: template.Prompt, Model: s.Model, Tools: tools, MaxSteps: 4, Enabled: true, Version: 1, CreatedAt: now, UpdatedAt: now,
		}
		if err := s.DB.CreateAgent(ctx, agent); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) SeedCatalog(ctx context.Context) error {
	now := time.Now().UTC()
	for _, item := range []store.Integration{
		{ID: "utility", Kind: "mcp", DisplayName: "Utility", Enabled: true, Status: "configured", Detail: "echo, add, and the safe high-risk mock"},
		{ID: "data", Kind: "mcp", DisplayName: "Records", Enabled: true, Status: "configured", Detail: "fixed lookup dataset"},
		{ID: "github", Kind: "mcp", DisplayName: "GitHub", Enabled: true, Status: "configured", Detail: "public read-only issues and files"},
		{ID: "sampledb", Kind: "mcp", DisplayName: "Sample analytics", Enabled: true, Status: "configured", Detail: "seeded read-only sqlite database"},
		{ID: "files", Kind: "mcp", DisplayName: "Workspace files", Enabled: true, Status: "configured", Detail: "demo workspace directory"},
	} {
		item.UpdatedAt = now
		if err := s.DB.SaveIntegration(ctx, item); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) SeedDev(ctx context.Context) error {
	if !s.DevLogin {
		return nil
	}
	now := time.Now().UTC()
	for _, user := range devUsers(now) {
		if err := s.DB.UpsertUser(ctx, user); err != nil {
			return err
		}
	}
	return nil
}
