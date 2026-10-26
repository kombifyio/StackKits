package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kombifyio/stackkits/internal/mcpagents"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
	"github.com/kombifyio/stackkits/internal/stackkitmcp"
)

// Build identity is injected by GoReleaser. Local builds remain explicitly
// unbound so write-capable action tools cannot impersonate a release.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func main() {
	if err := runtimeprofile.Boot(); err != nil {
		log.Fatal(err)
	}
	modeFlag := flag.String("mode", "docs", "comma-separated modes: docs,local,server,actions")
	serverURL := flag.String("server-url", stackkitmcp.FirstNonEmpty(os.Getenv("STACKKITS_SERVER_URL"), stackkitmcp.DefaultLocalServerURL), "stackkit-server URL")
	apiKey := flag.String("api-key", "", "stackkit-server API key")
	transport := flag.String("transport", "stdio", "transport: stdio or http")
	addr := flag.String("addr", "127.0.0.1:8091", "HTTP listen address when --transport=http")
	mcpToken := flag.String("mcp-token", "", "Bearer token required by MCP HTTP transport (or set STACKKIT_MCP_TOKEN or STACKKIT_MCP_TOKEN_FILE)")
	agentWorkspace := flag.String("agent-workspace", ".", "workspace whose owner-minted agent credentials (.stackkit/mcp/agents.json) authenticate STACKKIT_MCP_AGENT_CREDENTIAL[_FILE]")
	flag.Parse()

	transportName := strings.ToLower(strings.TrimSpace(*transport))
	token := ""
	if transportName == "http" {
		var err error
		if token, err = stackkitmcp.ResolveMCPToken(*mcpToken); err != nil {
			log.Fatal(err)
		}
	}
	cliBinary, _ := stackkitmcp.SiblingStackkitBinary()
	opts := stackkitmcp.Options{
		Modes:      stackkitmcp.ParseModes(*modeFlag),
		ServerURL:  strings.TrimRight(*serverURL, "/"),
		APIKey:     stackkitmcp.FirstNonEmpty(*apiKey, os.Getenv("STACKKITS_API_KEY")),
		Transport:  transportName,
		MCPToken:   token,
		AllowWrite: stackkitmcp.EnvBoolValue(os.Getenv("STACKKIT_MCP_ALLOW_WRITE")),
		Binary:     cliBinary,
		Version:    Version,
		GitCommit:  GitCommit,
	}
	// A directly attached agent that presents its own credential gets per-call
	// owner approval for every write instead of the process-wide switch.
	agentToken, err := stackkitmcp.ResolveAgentCredential()
	if err != nil {
		log.Fatal(err)
	}
	if agentToken != "" && transportName == "http" {
		log.Fatalf("%s identifies a stdio attachment; an HTTP agent presents its credential per request to stackkit-server", stackkitmcp.AgentCredentialEnv)
	}
	if agentToken != "" {
		store := mcpagents.Open(*agentWorkspace)
		agent, ok := store.Authenticate(agentToken)
		if !ok {
			log.Fatalf("%s: the agent credential is unknown, rotated or revoked in %s", stackkitmcp.AgentCredentialEnv, *agentWorkspace)
		}
		opts.Agents = store
		opts.AgentToken = agentToken
		opts.AllowWrite = false
		log.Printf("attached as agent %s (%s); every mutating call needs owner approval", agent.Name, agent.ID)
	}
	app := stackkitmcp.New(opts)

	switch opts.Transport {
	case "http":
		handler := app.ProtectedStreamableHTTPHandler()
		if opts.MCPToken == "" {
			// Only a loopback listener may run without a token; every
			// non-loopback listener fails closed regardless of mode.
			if !stackkitmcp.IsLoopbackListenAddr(*addr) {
				log.Fatal("--mcp-token, STACKKIT_MCP_TOKEN or STACKKIT_MCP_TOKEN_FILE is required for a non-loopback MCP HTTP listener")
			}
			log.Printf("serving MCP HTTP on loopback %s without a token", *addr)
			handler = app.StreamableHTTPHandler()
		}
		log.Fatal(stackkitmcp.NewHTTPServer(*addr, handler).ListenAndServe())
	default:
		if err := app.Server().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatal(err)
		}
	}
}
