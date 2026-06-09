package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kombifyio/stackkits/internal/stackkitmcp"
)

func main() {
	modeFlag := flag.String("mode", "docs", "comma-separated modes: docs,local,server,actions")
	serverURL := flag.String("server-url", stackkitmcp.FirstNonEmpty(os.Getenv("STACKKITS_SERVER_URL"), "http://localhost:8082"), "stackkit-server URL")
	apiKey := flag.String("api-key", "", "stackkit-server API key")
	transport := flag.String("transport", "stdio", "transport: stdio or http")
	addr := flag.String("addr", "127.0.0.1:8091", "HTTP listen address when --transport=http")
	mcpToken := flag.String("mcp-token", "", "Bearer token required by MCP HTTP transport")
	flag.Parse()

	opts := stackkitmcp.Options{
		Modes:      stackkitmcp.ParseModes(*modeFlag),
		ServerURL:  strings.TrimRight(*serverURL, "/"),
		APIKey:     stackkitmcp.FirstNonEmpty(*apiKey, os.Getenv("STACKKITS_API_KEY")),
		Transport:  strings.ToLower(strings.TrimSpace(*transport)),
		MCPToken:   stackkitmcp.FirstNonEmpty(*mcpToken, os.Getenv("STACKKIT_MCP_TOKEN")),
		AllowWrite: stackkitmcp.EnvBoolValue(os.Getenv("STACKKIT_MCP_ALLOW_WRITE")),
	}
	app := stackkitmcp.New(opts)

	switch opts.Transport {
	case "http":
		if (opts.Modes["server"] || opts.Modes["actions"]) && !stackkitmcp.IsLoopbackListenAddr(*addr) && opts.MCPToken == "" {
			log.Fatal("--mcp-token or STACKKIT_MCP_TOKEN is required when management or action tools are exposed over non-loopback HTTP")
		}
		log.Fatal(stackkitmcp.NewHTTPServer(*addr, app.ProtectedStreamableHTTPHandler()).ListenAndServe())
	default:
		if err := app.Server().Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatal(err)
		}
	}
}
