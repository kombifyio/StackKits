package stackkitmcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/mcpagents"
)

// Dedicated MCP credential sources. The token file lets an installer mint the
// credential without placing it on a command line or in a process listing.
const (
	MCPTokenEnv     = "STACKKIT_MCP_TOKEN"
	MCPTokenFileEnv = "STACKKIT_MCP_TOKEN_FILE"
	mcpTokenHeader  = "X-StackKit-MCP-Token"

	// AgentCredentialEnv and AgentCredentialFileEnv carry the per-agent
	// credential of a directly attached (stdio) agent.
	AgentCredentialEnv     = "STACKKIT_MCP_AGENT_CREDENTIAL"
	AgentCredentialFileEnv = "STACKKIT_MCP_AGENT_CREDENTIAL_FILE"
)

// ResolveAgentCredential returns the per-agent credential of a directly
// attached agent from STACKKIT_MCP_AGENT_CREDENTIAL or the file named by
// STACKKIT_MCP_AGENT_CREDENTIAL_FILE. An empty result means none is
// configured; an unreadable or empty file is an error.
func ResolveAgentCredential() (string, error) {
	if token := strings.TrimSpace(os.Getenv(AgentCredentialEnv)); token != "" {
		return token, nil
	}
	path := strings.TrimSpace(os.Getenv(AgentCredentialFileEnv))
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", AgentCredentialFileEnv, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("%s: credential file %s is empty", AgentCredentialFileEnv, path)
	}
	return token, nil
}

// ResolveMCPToken returns the dedicated MCP token from the explicit flag value,
// STACKKIT_MCP_TOKEN, or the file named by STACKKIT_MCP_TOKEN_FILE, in that
// order. An empty result means no token is configured. A configured token file
// that cannot be read or is empty is an error, never a silent fallback.
func ResolveMCPToken(flagValue string) (string, error) {
	if token := FirstNonEmpty(flagValue, os.Getenv(MCPTokenEnv)); token != "" {
		return token, nil
	}
	path := strings.TrimSpace(os.Getenv(MCPTokenFileEnv))
	if path == "" {
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", MCPTokenFileEnv, err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("%s: token file %s is empty", MCPTokenFileEnv, path)
	}
	return token, nil
}

type agentContextKey struct{}

// AgentFromContext returns the per-agent credential that authenticated the
// request. A request admitted by the shared legacy token has none.
func AgentFromContext(ctx context.Context) (mcpagents.Agent, bool) {
	agent, ok := ctx.Value(agentContextKey{}).(mcpagents.Agent)
	return agent, ok
}

// RequireMCPToken admits only requests that present the configured MCP token
// as a bearer credential or X-StackKit-MCP-Token value.
func RequireMCPToken(token string, next http.Handler) http.Handler {
	return RequireMCPCredential(token, nil, next)
}

// RequireMCPCredential admits requests that present the shared MCP token or an
// active per-agent credential from the store, and it fails closed: with
// neither configured every request is denied with configuration guidance. An
// agent credential is re-checked on every request, so revoking it denies the
// next call.
func RequireMCPCredential(token string, agents *mcpagents.Store, next http.Handler) http.Handler {
	token = strings.TrimSpace(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" && (agents == nil || !agents.HasActive()) {
			writeMCPAuthDenial(w, "mcp_token_not_configured",
				"this StackKits MCP endpoint has no MCP credential configured and denies every request",
				"Mint an agent credential with 'stackkit agent credential mint --name <agent>', or write a shared token to a file named by STACKKIT_MCP_TOKEN_FILE (or set STACKKIT_MCP_TOKEN / --mcp-token), then restart the server",
				"The stackkit-server API key is never accepted as an MCP credential",
			)
			return
		}
		presented := presentedMCPToken(r)
		if mcpTokenMatches(token, presented) {
			next.ServeHTTP(w, r)
			return
		}
		if agents != nil {
			if agent, ok := agents.Authenticate(presented); ok {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), agentContextKey{}, agent)))
				return
			}
		}
		writeMCPAuthDenial(w, "mcp_token_required",
			"a valid StackKits MCP credential is required",
			"Send the agent credential as 'Authorization: Bearer <token>' or in the X-StackKit-MCP-Token header; a revoked or rotated-out credential no longer works",
			"The stackkit-server API key is not an MCP credential",
		)
	})
}

func presentedMCPToken(r *http.Request) string {
	scheme, value, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if ok && strings.EqualFold(scheme, "Bearer") {
		if token := strings.TrimSpace(value); token != "" {
			return token
		}
	}
	return strings.TrimSpace(r.Header.Get(mcpTokenHeader))
}

// mcpTokenMatches compares fixed-length digests in constant time, so neither
// the token contents nor its length leak through timing.
func mcpTokenMatches(expected, presented string) bool {
	expected = strings.TrimSpace(expected)
	presented = strings.TrimSpace(presented)
	if expected == "" || presented == "" {
		return false
	}
	expectedHash := sha256.Sum256([]byte(expected))
	presentedHash := sha256.Sum256([]byte(presented))
	return subtle.ConstantTimeCompare(expectedHash[:], presentedHash[:]) == 1
}

// mcpAuthDenial mirrors the stackkit-server structured error envelope, so the
// REST API and /mcp give callers one denial shape.
type mcpAuthDenial struct {
	Error mcpAuthDenialDetail `json:"error"`
}

type mcpAuthDenialDetail struct {
	Code        int      `json:"code"`
	Message     string   `json:"message"`
	Category    string   `json:"category"`
	ErrorCode   string   `json:"error_code"`
	Suggestions []string `json:"suggestions"`
}

func writeMCPAuthDenial(w http.ResponseWriter, errorCode, message string, suggestions ...string) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="stackkit-mcp"`)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(mcpAuthDenial{Error: mcpAuthDenialDetail{
		Code:        http.StatusUnauthorized,
		Message:     message,
		Category:    "auth",
		ErrorCode:   errorCode,
		Suggestions: suggestions,
	}})
}
