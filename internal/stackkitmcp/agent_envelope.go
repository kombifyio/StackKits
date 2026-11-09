package stackkitmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/kombifyio/stackkits/internal/mcpagents"
)

// AgentEnvelopeHeader is where the Access Gate hands the endpoint the agent
// envelope it verified (homelab identity target §7). The router replaces any
// client copy with the gate's answer, so the endpoint only ever sees an
// envelope the gate admitted on this request.
const AgentEnvelopeHeader = "Stackkit-Agent-Envelope"

// ReadOnlyAgentCapability is the lease capability whose ceiling is the
// read-only tools.
const ReadOnlyAgentCapability = "techstack.inventory.read"

const maxAgentEnvelopeBodyBytes = 4 << 20

// AgentEnvelopePrincipal is a verified agent call: the Homelab account it acts
// for, the agent instance, the lease and the capability.
type AgentEnvelopePrincipal struct {
	Account       string
	AgentInstance string
	LeaseID       string
	Capability    string
	// LinkRef names the pinned agent link (generation and install time).
	LinkRef string
}

// AgentEnvelopeVerifier verifies, at the endpoint, an envelope the gate
// admitted: the signature and claims again, the method and path as received,
// the hash of the exact body, and the gate's single admission, which it
// spends.
type AgentEnvelopeVerifier interface {
	ConsumeAgentEnvelope(envelope, method, path string, body []byte) (AgentEnvelopePrincipal, error)
}

type envelopeAgentContextKey struct{}

// EnvelopeAgentFromContext returns the envelope agent that authenticated the
// request.
func EnvelopeAgentFromContext(ctx context.Context) (AgentEnvelopePrincipal, bool) {
	principal, ok := ctx.Value(envelopeAgentContextKey{}).(AgentEnvelopePrincipal)
	return principal, ok
}

// requireAgentEnvelopeOrCredential admits a request that carries the gate's
// envelope only through the envelope verifier, never falling back to another
// credential, and every other request through next (the MCP token or a
// per-agent credential). An envelope agent's mutating calls need the owner's
// approval of each exact call, recorded under its agent instance; a read
// lease reaches read-only tools only.
func requireAgentEnvelopeOrCredential(verifier AgentEnvelopeVerifier, next, credential http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelopes := r.Header.Values(AgentEnvelopeHeader)
		if len(envelopes) == 0 {
			credential.ServeHTTP(w, r)
			return
		}
		deny := func() {
			writeMCPAuthDenial(w, "mcp_agent_envelope_refused",
				"the agent envelope is not valid for this request on this node",
				"Ask the kombify Gateway for a new envelope for exactly this request; an envelope is used once and lives at most 60 seconds",
			)
		}
		if verifier == nil || len(envelopes) != 1 {
			deny()
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxAgentEnvelopeBodyBytes+1))
		_ = r.Body.Close()
		if err != nil || len(body) > maxAgentEnvelopeBodyBytes {
			deny()
			return
		}
		principal, err := verifier.ConsumeAgentEnvelope(envelopes[0], r.Method, r.URL.Path, body)
		if err != nil {
			slog.Info("agent envelope refused at the MCP endpoint", "path", r.URL.Path)
			deny()
			return
		}
		// The tool ceiling is decided here, once, for every message: a tool
		// that is neither allowlisted nor a permitted catalog operation never
		// reaches a handler (agent_ceiling.go).
		if reason := envelopeToolCallDenial(body, principal.Capability); reason != "" {
			slog.Info("agent envelope tool call refused", "reason", reason, "agent", principal.AgentInstance)
			writeAgentToolRefusal(w, reason)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		ctx := context.WithValue(r.Context(), envelopeAgentContextKey{}, principal)
		// Approvals bind the pinned link (generation and install time), the
		// agent instance and the account it acts for: after the link is
		// removed no envelope verifies, and after any re-install, re-key or
		// remap an earlier approval names another agent ID and is never
		// spent (it expires with its TTL).
		ctx = context.WithValue(ctx, agentContextKey{}, mcpagents.Agent{
			ID:   mcpagents.GatewayAgentPrefix + principal.LinkRef + ":" + principal.AgentInstance + "/" + principal.Account,
			Name: "kombify agent " + principal.AgentInstance + " for " + principal.Account,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// writeAgentToolRefusal answers 403 for a call an envelope agent may not make.
func writeAgentToolRefusal(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error_code": "mcp_agent_tool_refused", "reason_code": reason,
		"message": "a kombify agent may not make this call on this node; identity, secrets, vault and recovery stay with the Owner",
	})
}
