package security

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// A disconnected HTTP client cannot be repaired here; no application action
	// depends on successfully writing its response.
	_ = json.NewEncoder(w).Encode(value)
}

func (g *Gate) HTTPHandler(server *mcp.Server) http.Handler {
	stream := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: 1 << 20, PropagateRequestCancellation: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// This is a local CLI API, not a browser application. Reject browser origins.
		if r.Header.Get("Origin") != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "browser_origin_not_allowed"})
			return
		}
		if r.URL.Path == "/mcp" {
			if _, ok := g.Auth.Authenticate(r.Header.Get("Authorization")); !ok {
				id := NewRequestID()
				if err := g.Audit.Write(Event{RequestID: id, Identity: "anonymous", Tool: "<unresolved>", Decision: "denied", Reason: "unauthenticated"}); err != nil {
					writeJSON(w, 500, map[string]string{"error": "audit_write_failed", "request_id": id})
					return
				}
				w.Header().Set("WWW-Authenticate", "Bearer")
				writeJSON(w, 401, map[string]string{"error": "unauthenticated", "request_id": id})
				return
			}
			stream.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/approvals" && !strings.HasPrefix(r.URL.Path, "/approvals/") {
			http.NotFound(w, r)
			return
		}
		operator, approver := g.Auth.Approver(r.Header.Get("Authorization"))
		if !approver {
			writeJSON(w, 401, map[string]string{"error": "approver_authentication_required"})
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/approvals" {
			writeJSON(w, 200, g.approvals().List(g.Now()))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path == "/approvals" {
			writeJSON(w, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		var input struct {
			Decision string `json:"decision"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid_decision"})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF || (input.Decision != "approve" && input.Decision != "deny") {
			writeJSON(w, 400, map[string]string{"error": "invalid_decision"})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/approvals/")
		// IDs are generated server-side; reject arbitrary strings before auditing.
		if len(id) != 32 || strings.Trim(id, "0123456789abcdef") != "" {
			writeJSON(w, 400, map[string]string{"error": "invalid_approval_id"})
			return
		}
		if err := g.Audit.Write(Event{RequestID: id, Identity: operator.ID, Tool: "<approval>", Decision: "decision_requested", Reason: input.Decision, ApprovalID: id}); err != nil {
			writeJSON(w, 500, map[string]string{"error": "audit_write_failed"})
			return
		}
		for _, pending := range g.approvals().List(g.Now()) {
			if pending.ID == id && pending.Identity == operator.ID {
				writeJSON(w, 403, map[string]string{"error": "self_approval_forbidden"})
				return
			}
		}
		var entry Approval
		var err error
		if decided, ok := g.approvals().(OperatorDecider); ok {
			entry, err = decided.DecideBy(id, input.Decision, operator.ID, g.Now())
		} else {
			entry, err = g.approvals().Decide(id, input.Decision, g.Now())
		}
		if err != nil {
			writeJSON(w, 409, map[string]string{"error": err.Error()})
			return
		}
		if err := g.Audit.Write(Event{RequestID: entry.RequestID, Identity: operator.ID, Tool: entry.Tool, Decision: entry.Status, Reason: "human_decision", Risk: "high", ApprovalID: entry.ID}); err != nil {
			writeJSON(w, 500, map[string]string{"error": "audit_write_failed"})
			return
		}
		writeJSON(w, 200, entry)
	})
}
