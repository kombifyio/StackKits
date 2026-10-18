package stackkitmcp

import (
	"strings"

	"github.com/kombifyio/stackkits/internal/mcpagents"
)

// Options configures the StackKits MCP server surface.
type Options struct {
	Modes      map[string]bool
	ServerURL  string
	APIKey     string
	Transport  string
	MCPToken   string
	AllowWrite bool
	// Agents holds per-agent MCP credentials. When set, a request that
	// authenticates with an agent credential may call mutating tools only
	// after the owner approves that exact call; AllowWrite no longer applies
	// to it and stays the switch for the shared token and an anonymous stdio
	// attachment.
	Agents *mcpagents.Store
	// AgentToken identifies a directly attached (stdio) agent by its per-agent
	// credential. The credential is re-checked against Agents on every
	// mutating call, so every write of that agent needs per-call owner
	// approval and a revoke takes effect on its next call.
	AgentToken string
	BaseDir    string
	// PinBaseDir confines every tool to BaseDir. A remote caller then cannot
	// point base_dir at other paths of the host or container.
	PinBaseDir bool
	// JSONResponse answers Streamable HTTP calls with application/json instead
	// of an event stream; stackkit-server sets it because its tools never stream.
	JSONResponse bool
	Binary       string
	Version      string
	GitCommit    string
}

// writeEnabled reports whether mutating tools are registered at all.
func (o Options) writeEnabled() bool { return o.AllowWrite || o.Agents != nil }

func (o Options) normalized() Options {
	if len(o.Modes) == 0 {
		o.Modes = ParseModes("docs")
	}
	o.ServerURL = strings.TrimRight(FirstNonEmpty(o.ServerURL, DefaultLocalServerURL), "/")
	o.Transport = strings.ToLower(strings.TrimSpace(FirstNonEmpty(o.Transport, "stdio")))
	o.MCPToken = strings.TrimSpace(o.MCPToken)
	o.APIKey = strings.TrimSpace(o.APIKey)
	o.AgentToken = strings.TrimSpace(o.AgentToken)
	o.BaseDir = FirstNonEmpty(o.BaseDir, ".")
	o.Binary = strings.TrimSpace(o.Binary)
	// An unbound local development build is intentionally identified as dev.
	// Never invent an older release line: version-gated admission must fail
	// closed until the producing binary supplies its actual build version.
	o.Version = FirstNonEmpty(o.Version, "dev")
	o.GitCommit = FirstNonEmpty(o.GitCommit, "unknown")
	return o
}

// ParseModes parses a comma-separated MCP mode list.
func ParseModes(raw string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out[part] = true
		}
	}
	if len(out) == 0 {
		out["docs"] = true
	}
	return out
}

// FirstNonEmpty returns the first non-empty value after trimming whitespace.
func FirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	return FirstNonEmpty(values...)
}

// EnvBoolValue parses common truthy environment values.
func EnvBoolValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
