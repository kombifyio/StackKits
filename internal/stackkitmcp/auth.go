package stackkitmcp

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Dedicated MCP credential sources. The token file lets an installer mint the
// credential without placing it on a command line or in a process listing.
const (
	MCPTokenEnv     = "STACKKIT_MCP_TOKEN"
	MCPTokenFileEnv = "STACKKIT_MCP_TOKEN_FILE"
	mcpTokenHeader  = "X-StackKit-MCP-Token"
)

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

// RequireMCPToken admits only requests that present the configured MCP token
// as a bearer credential or X-StackKit-MCP-Token value. It fails closed: with
// no configured token every request is denied with configuration guidance.
func RequireMCPToken(token string, next http.Handler) http.Handler {
	token = strings.TrimSpace(token)
	if token == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeMCPAuthDenial(w, "mcp_token_not_configured",
				"this StackKits MCP endpoint has no MCP token configured and denies every request",
				"Configure a dedicated MCP token on the server: write it to a file named by STACKKIT_MCP_TOKEN_FILE (or set STACKKIT_MCP_TOKEN / --mcp-token), then restart the server",
				"The stackkit-server API key is never accepted as an MCP credential",
			)
		})
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !mcpTokenMatches(token, presentedMCPToken(r)) {
			writeMCPAuthDenial(w, "mcp_token_required",
				"a valid StackKits MCP token is required",
				"Send the dedicated MCP token as 'Authorization: Bearer <token>' or in the X-StackKit-MCP-Token header",
				"The stackkit-server API key is not an MCP credential",
			)
			return
		}
		next.ServeHTTP(w, r)
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
