package stackkitmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Wiki keeps its native read tool. StackKits supplies only local discovery
// and the node-custodied bearer, never the incoming MCP caller's credential.
func (a *App) addWiki(server *mcp.Server) {
	if !nativehost.WikiMCPInstalled(a.opts.BaseDir) {
		return
	}
	server.AddTool(&mcp.Tool{
		Name: "wiki_read", Description: "Read this server's personal IT Wiki before consulting the Board. List pages, read a page, search sections or draw relations. Requires the optional Wiki module to be installed.",
		InputSchema: json.RawMessage(`{"type":"object","required":["view"],"properties":{"view":{"type":"string","enum":["pages","page","search","graph"]},"pageSlug":{"type":"string","maxLength":72},"query":{"type":"string","maxLength":500},"limit":{"type":"integer","minimum":1,"maximum":50},"focus":{"type":"string","maxLength":64},"depth":{"type":"integer","minimum":1,"maximum":3}},"additionalProperties":false}`),
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)},
	}, func(ctx context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		endpoint, bearer, err := nativehost.WikiMCPConnection(ctx, a.opts.BaseDir)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
		}
		transport := &http.Transport{Proxy: nil}
		defer transport.CloseIdleConnections()
		client := mcp.NewClient(&mcp.Implementation{Name: "stackkits-wiki", Version: a.opts.Version}, nil)
		session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &http.Client{
			Timeout: 30 * time.Second, Transport: wikiBearerTransport{base: transport, endpoint: endpoint, bearer: bearer},
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("Wiki MCP redirects are refused") },
		}}, nil)
		if err != nil {
			return nil, errors.New("the local Wiki MCP is unavailable")
		}
		defer session.Close()
		return session.CallTool(ctx, &mcp.CallToolParams{Name: "wiki_read", Arguments: request.Params.Arguments})
	})
}

type wikiBearerTransport struct {
	base     http.RoundTripper
	endpoint string
	bearer   string
}

func (t wikiBearerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.String() != t.endpoint {
		return nil, errors.New("Wiki MCP destination differs from the observed loopback endpoint")
	}
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+t.bearer)
	return t.base.RoundTrip(request)
}
