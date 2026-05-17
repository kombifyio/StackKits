package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type stackkitMCP struct {
	opts serverOptions
	docs map[string]string
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

func (a *stackkitMCP) newServer() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "stackkit-mcp", Version: "0.1.0"}, nil)
	if a.opts.modes["docs"] {
		a.addDocs(server)
	}
	if a.opts.modes["local"] {
		a.addLocal(server)
	}
	if a.opts.modes["server"] {
		a.addServerTools(server)
	}
	if a.opts.modes["actions"] && a.opts.allowWrite {
		a.addActions(server)
	}
	return server
}

func (a *stackkitMCP) addDocs(server *mcp.Server) {
	mcp.AddTool(server, mcpTool("stackkit_docs_search", "Search embedded StackKits docs, prompts, and OpenAPI snippets.", true, false, true), a.docsSearch)
	mcp.AddTool(server, mcpTool("stackkit_api_overview", "List StackKits API endpoints from the embedded OpenAPI spec.", true, false, true), a.apiOverview)
	mcp.AddTool(server, mcpTool("stackkit_api_endpoint", "Return OpenAPI details for one StackKits endpoint path.", true, false, true), a.apiEndpoint)
	mcp.AddTool(server, mcpTool("stackkit_get_openapi_spec", "Return the StackKits OpenAPI YAML.", true, false, true), a.getOpenAPISpec)
	mcp.AddTool(server, mcpTool("stackkit_install_plan", "Return a safe BaseKit install plan for agents.", true, false, true), a.installPlan)
	mcp.AddTool(server, mcpTool("stackkit_self_check_plan", "Return ordered StackKits agent self-check probes.", true, false, true), a.selfCheckPlan)

	for uri, body := range a.docs {
		uri := uri
		body := body
		server.AddResource(&mcp.Resource{
			Name:        uri,
			Title:       resourceTitle(uri),
			URI:         "stackkit://" + uri,
			MIMEType:    mimeTypeForResource(uri),
			Size:        int64(len(body)),
			Description: "Embedded StackKits agent documentation",
			Annotations: &mcp.Annotations{Audience: []mcp.Role{mcp.Role("assistant")}, Priority: resourcePriority(uri)},
		}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: mimeTypeForResource(uri), Text: body}}}, nil
		})
	}
	for _, prompt := range []string{
		"stackkit_basekit_autonomous_rollout",
		"stackkit_inspect_existing_rollout",
		"stackkit_diagnose_failed_rollout",
		"stackkit_enable_monitoring_addon",
		"stackkit_ssh_rollout",
	} {
		prompt := prompt
		server.AddPrompt(&mcp.Prompt{Name: prompt, Description: "StackKits agent prompt"}, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Description: prompt,
				Messages: []*mcp.PromptMessage{{
					Role:    "user",
					Content: &mcp.TextContent{Text: promptText(prompt)},
				}},
			}, nil
		})
	}
}

func (a *stackkitMCP) addLocal(server *mcp.Server) {
	mcp.AddTool(server, mcpTool("stackkit_validate_spec", "Validate a local stack-spec.yaml without mutating rollout artifacts.", true, false, true), a.validateSpec)
	mcp.AddTool(server, mcpTool("stackkit_generate_preview", "Preview local generation readiness without writing files.", true, false, true), a.generatePreview)
	mcp.AddTool(server, mcpTool("stackkit_compat_check", "Return a compatibility-check command plan.", true, false, true), a.compatCheck)
}

func (a *stackkitMCP) addServerTools(server *mcp.Server) {
	mcp.AddTool(server, mcpTool("stackkit_status", "GET /api/v1/status from stackkit-server.", true, false, true), a.status)
	mcp.AddTool(server, mcpTool("stackkit_verify", "POST /api/v1/verify against stackkit-server.", true, false, false), a.verify)
	mcp.AddTool(server, mcpTool("stackkit_logs_list", "GET /api/v1/logs from stackkit-server.", true, false, true), a.logsList)
	mcp.AddTool(server, mcpTool("stackkit_log_get", "GET /api/v1/logs/{runID} from stackkit-server.", true, false, true), a.logGet)
	mcp.AddTool(server, mcpTool("stackkit_doctor", "POST /api/v1/doctor against stackkit-server.", true, false, false), a.doctor)
}

func (a *stackkitMCP) addActions(server *mcp.Server) {
	mcp.AddTool(server, mcpTool("stackkit_init", "Return an operator-approved stackkit init command plan.", false, false, false), a.actionPlan)
	mcp.AddTool(server, mcpTool("stackkit_generate", "Return an operator-approved stackkit generate command plan.", false, false, false), a.actionPlan)
	mcp.AddTool(server, mcpTool("stackkit_apply", "Return an operator-approved stackkit apply command plan.", false, true, false), a.actionPlan)
	mcp.AddTool(server, mcpTool("stackkit_addon_add", "Return an operator-approved stackkit addon add command plan.", false, false, false), a.actionPlan)
	mcp.AddTool(server, mcpTool("stackkit_remove", "Return an operator-approved stackkit remove command plan.", false, true, false), a.actionPlan)
}

func mcpTool(name, description string, readOnly, destructive, idempotent bool) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Title:       strings.TrimPrefix(strings.ReplaceAll(name, "_", " "), "stackkit "),
		Description: description,
		Annotations: &mcp.ToolAnnotations{
			Title:           strings.TrimPrefix(strings.ReplaceAll(name, "_", " "), "stackkit "),
			ReadOnlyHint:    readOnly,
			DestructiveHint: boolPtr(destructive),
			IdempotentHint:  idempotent,
			OpenWorldHint:   boolPtr(false),
		},
	}
}

func boolPtr(v bool) *bool {
	return &v
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = strings.TrimSpace(strings.Split(addr, ":")[0])
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

func requireMCPToken(token string, next http.Handler) http.Handler {
	token = strings.TrimSpace(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if got == "" {
			got = strings.TrimSpace(r.Header.Get("X-StackKit-MCP-Token"))
		}
		if !mcpTokenMatches(token, got) {
			http.Error(w, "mcp token required", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

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
