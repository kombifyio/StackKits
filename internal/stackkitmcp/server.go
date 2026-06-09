package stackkitmcp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

//go:embed assets/onboarding.html
var onboardingHTML string

const onboardingResourceURI = "ui://stackkits/onboarding.html"

// App owns the StackKits MCP registration shared by stackkit-mcp and stackkit-server.
type App struct {
	opts Options
	docs map[string]string
}

// New creates a configured StackKits MCP app.
func New(opts Options) *App {
	opts = opts.normalized()
	return &App{opts: opts, docs: loadDocs()}
}

// NewHTTPServer creates a hardened Streamable HTTP server. WriteTimeout stays
// disabled because MCP Streamable HTTP may keep responses open.
func NewHTTPServer(addr string, handler http.Handler) *http.Server {
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

// Server builds a Model Context Protocol server with the configured modes.
func (a *App) Server() *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "stackkit-mcp", Version: a.opts.Version}, nil)
	if a.opts.Modes["docs"] {
		a.addDocs(server)
	}
	if a.opts.Modes["local"] {
		a.addLocal(server)
	}
	if a.opts.Modes["server"] {
		a.addServerTools(server)
	}
	if a.opts.Modes["actions"] && a.opts.AllowWrite {
		a.addActions(server)
	}
	return server
}

// StreamableHTTPHandler returns a Streamable HTTP MCP handler.
func (a *App) StreamableHTTPHandler() http.Handler {
	server := a.Server()
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		SessionTimeout: 30 * time.Minute,
	})
}

// ProtectedStreamableHTTPHandler wraps the MCP handler with token auth when configured.
func (a *App) ProtectedStreamableHTTPHandler() http.Handler {
	handler := a.StreamableHTTPHandler()
	if a.opts.MCPToken != "" {
		handler = RequireMCPToken(a.opts.MCPToken, handler)
	}
	return handler
}

// OpenMCP returns agent-native discovery metadata for the local MCP surface.
func (a *App) OpenMCP() map[string]any {
	tools := []map[string]any{
		toolDefinition("stackkit_docs_search", true, false, true),
		toolDefinition("stackkit_api_overview", true, false, true),
		toolDefinition("stackkit_api_endpoint", true, false, true),
		toolDefinition("stackkit_get_openapi_spec", true, false, true),
		toolDefinition("stackkit_install_plan", true, false, true),
		toolDefinition("stackkit_self_check_plan", true, false, true),
		toolDefinition("stackkit_onboarding_app", true, false, true),
	}
	if a.opts.Modes["local"] {
		tools = append(tools,
			toolDefinition("stackkit_validate_spec", true, false, true),
			toolDefinition("stackkit_generate_preview", true, false, true),
			toolDefinition("stackkit_compat_check", true, false, true),
			toolDefinition("stackkit_config_get", true, false, true),
		)
	}
	if a.opts.Modes["server"] {
		tools = append(tools,
			toolDefinition("stackkit_status", true, false, true),
			toolDefinition("stackkit_verify", true, false, false),
			toolDefinition("stackkit_logs_list", true, false, true),
			toolDefinition("stackkit_log_get", true, false, true),
			toolDefinition("stackkit_doctor", true, false, false),
		)
	}
	if a.opts.Modes["actions"] && a.opts.AllowWrite {
		tools = append(tools,
			toolDefinition("stackkit_init", false, false, false),
			toolDefinition("stackkit_prepare", false, false, false),
			toolDefinition("stackkit_generate", false, false, false),
			toolDefinition("stackkit_plan", false, false, true),
			toolDefinition("stackkit_apply", false, true, false),
			toolDefinition("stackkit_update", false, true, false),
			toolDefinition("stackkit_config_set", false, true, false),
			toolDefinition("stackkit_rollout", false, true, false),
		)
	}
	return map[string]any{
		"schemaVersion": "2026-06-08",
		"name":          "stackkit",
		"title":         "StackKits Native MCP Connector",
		"description":   "One user-facing StackKits MCP connection for docs, host checks, rollout guidance, and gated CLI-equivalent StackKits operations.",
		"userModel": map[string]any{
			"connectionName":   "stackkit",
			"localEntrypoint":  "stackkit-mcp stdio or loopback adapter",
			"serverEntrypoint": "stackkit-server POST /mcp",
			"mcpUse":           "authoring/build layer for the onboarding app, not a second production connector",
		},
		"transport": map[string]any{
			"type":     "streamable-http",
			"endpoint": "/mcp",
		},
		"auth": map[string]any{
			"loopbackDefault": "token optional for loopback-only local use",
			"nonLoopback":     "Bearer token or X-StackKit-MCP-Token required when configured beyond loopback",
			"writeGate":       "STACKKIT_MCP_ALLOW_WRITE=true",
		},
		"policy": map[string]any{
			"websiteSurface":          "read-only discovery only",
			"localConnectorAuthority": "same authority as a user running stackkit on this server when write mode is enabled",
			"managedServerless":       "out-of-scope",
			"appDay2Orchestration":    "out-of-scope; MCP apply skips platform app lifecycle by default",
		},
		"modes":      enabledModes(a.opts.Modes),
		"allowWrite": a.opts.AllowWrite,
		"serverURL":  a.opts.ServerURL,
		"tools":      tools,
		"resources":  a.openMCPResources(),
		"prompts":    stackkitPrompts(),
		"appResources": []map[string]any{{
			"uri":                  onboardingResourceURI,
			"mimeType":             "text/html;profile=mcp-app",
			"description":          "Stateful StackKits MCP App onboarding widget",
			"steps":                []string{"contact-and-workspace", "stackkit-select", "domain-and-core-settings", "review-and-plan", "rollout-and-evidence"},
			"callsToolsFromWidget": true,
			"appsSdkMetadata":      onboardingResourceMeta(),
		}},
	}
}

// OpenMCPJSON returns pretty JSON discovery metadata.
func (a *App) OpenMCPJSON() []byte {
	raw, err := json.MarshalIndent(a.OpenMCP(), "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return append(raw, '\n')
}

func (a *App) addDocs(server *mcp.Server) {
	mcp.AddTool(server, mcpTool("stackkit_docs_search", "Search embedded StackKits docs, prompts, and OpenAPI snippets.", true, false, true), a.docsSearch)
	mcp.AddTool(server, mcpTool("stackkit_api_overview", "List StackKits API endpoints from the embedded OpenAPI spec.", true, false, true), a.apiOverview)
	mcp.AddTool(server, mcpTool("stackkit_api_endpoint", "Return OpenAPI details for one StackKits endpoint path.", true, false, true), a.apiEndpoint)
	mcp.AddTool(server, mcpTool("stackkit_get_openapi_spec", "Return the StackKits OpenAPI YAML.", true, false, true), a.getOpenAPISpec)
	mcp.AddTool(server, mcpTool("stackkit_install_plan", "Return a safe BaseKit install plan for agents.", true, false, true), a.installPlan)
	mcp.AddTool(server, mcpTool("stackkit_self_check_plan", "Return ordered StackKits agent self-check probes.", true, false, true), a.selfCheckPlan)
	onboardingTool := mcpTool("stackkit_onboarding_app", "Return the StackKits MCP App onboarding widget metadata.", true, false, true)
	onboardingTool.Meta["ui"] = map[string]any{"resourceUri": onboardingResourceURI}
	onboardingTool.Meta["openai/outputTemplate"] = onboardingResourceURI
	onboardingTool.Meta["openai/toolInvocation/invoking"] = "Opening StackKits onboarding"
	onboardingTool.Meta["openai/toolInvocation/invoked"] = "StackKits onboarding opened"
	mcp.AddTool(server, onboardingTool, a.onboardingApp)

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
	server.AddResource(&mcp.Resource{
		Name:        "stackkits-onboarding",
		Title:       "StackKits Onboarding",
		URI:         onboardingResourceURI,
		MIMEType:    "text/html;profile=mcp-app",
		Size:        int64(len(onboardingHTML)),
		Description: "MCP App onboarding widget for guided StackKits host check, workspace selection, rollout, and evidence review.",
		Meta:        onboardingResourceMeta(),
		Annotations: &mcp.Annotations{Audience: []mcp.Role{mcp.Role("assistant")}, Priority: 1.0},
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: "text/html;profile=mcp-app",
			Text:     onboardingHTML,
			Meta:     onboardingResourceMeta(),
		}}}, nil
	})
	for _, prompt := range stackkitPrompts() {
		promptName, _ := prompt["name"].(string)
		if promptName == "" {
			continue
		}
		prompt := promptName
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

func (a *App) addLocal(server *mcp.Server) {
	mcp.AddTool(server, mcpTool("stackkit_validate_spec", "Validate a local stack-spec.yaml without mutating rollout artifacts.", true, false, true), a.validateSpec)
	mcp.AddTool(server, mcpTool("stackkit_generate_preview", "Preview local generation readiness without writing files.", true, false, true), a.generatePreview)
	mcp.AddTool(server, mcpTool("stackkit_compat_check", "Return a compatibility-check command plan.", true, false, true), a.compatCheck)
	mcp.AddTool(server, mcpTool("stackkit_config_get", "Read the local stack-spec.yaml or kombination.yaml without mutation.", true, false, true), a.configGet)
}

func (a *App) addServerTools(server *mcp.Server) {
	mcp.AddTool(server, mcpTool("stackkit_status", "GET /api/v1/status from stackkit-server.", true, false, true), a.status)
	mcp.AddTool(server, mcpTool("stackkit_verify", "POST /api/v1/verify against stackkit-server.", true, false, false), a.verify)
	mcp.AddTool(server, mcpTool("stackkit_logs_list", "GET /api/v1/logs from stackkit-server.", true, false, true), a.logsList)
	mcp.AddTool(server, mcpTool("stackkit_log_get", "GET /api/v1/logs/{runID} from stackkit-server.", true, false, true), a.logGet)
	mcp.AddTool(server, mcpTool("stackkit_doctor", "POST /api/v1/doctor against stackkit-server.", true, false, false), a.doctor)
}

func (a *App) addActions(server *mcp.Server) {
	mcp.AddTool(server, mcpTool("stackkit_init", "Run stackkit init on the local server workspace.", false, false, false), a.stackkitInit)
	mcp.AddTool(server, mcpTool("stackkit_prepare", "Run stackkit prepare on the local server workspace.", false, false, false), a.stackkitPrepare)
	mcp.AddTool(server, mcpTool("stackkit_generate", "Run stackkit generate on the local server workspace.", false, false, false), a.stackkitGenerate)
	mcp.AddTool(server, mcpTool("stackkit_plan", "Run stackkit plan on the local server workspace.", false, false, true), a.stackkitPlan)
	mcp.AddTool(server, mcpTool("stackkit_apply", "Run stackkit apply on the local server workspace; platform app lifecycle is skipped by default.", false, true, false), a.stackkitApply)
	mcp.AddTool(server, mcpTool("stackkit_update", "Run stackkit kit upgrade on the local server workspace.", false, true, false), a.stackkitUpdate)
	mcp.AddTool(server, mcpTool("stackkit_config_set", "Replace stack-spec.yaml after validation in the local workspace.", false, true, false), a.configSet)
	mcp.AddTool(server, mcpTool("stackkit_rollout", "Run init, prepare, validate, generate, plan, apply, and verify as one bounded local StackKits rollout.", false, true, false), a.stackkitRollout)
}

func mcpTool(name, description string, readOnly, destructive, idempotent bool) *mcp.Tool {
	return &mcp.Tool{
		Meta:        toolMeta(name),
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

var onboardingWidgetToolNames = map[string]bool{
	"stackkit_onboarding_app":   true,
	"stackkit_self_check_plan":  true,
	"stackkit_status":           true,
	"stackkit_config_get":       true,
	"stackkit_init":             true,
	"stackkit_config_set":       true,
	"stackkit_validate_spec":    true,
	"stackkit_generate_preview": true,
	"stackkit_prepare":          true,
	"stackkit_generate":         true,
	"stackkit_plan":             true,
	"stackkit_rollout":          true,
	"stackkit_verify":           true,
	"stackkit_logs_list":        true,
	"stackkit_doctor":           true,
}

func toolMeta(name string) mcp.Meta {
	if !isWidgetAccessibleTool(name) {
		return mcp.Meta{}
	}
	return mcp.Meta{"openai/widgetAccessible": true}
}

func isWidgetAccessibleTool(name string) bool {
	return onboardingWidgetToolNames[name]
}

func onboardingResourceMeta() mcp.Meta {
	return mcp.Meta{
		"ui": map[string]any{
			"prefersBorder": true,
			"csp": map[string]any{
				"connectDomains":  []string{"http://localhost:8082"},
				"resourceDomains": []string{"https://stackkit.cc"},
			},
		},
		"openai/widgetDescription":      "Guided StackKits onboarding for email, kit selection, core settings, domain choice, rollout progress, and evidence review.",
		"openai/widgetPrefersBorder":    true,
		"openai/widgetAccessible":       true,
		"openai/resultCanProduceWidget": true,
		"openai/widgetCSP": map[string]any{
			"connect_domains":  []string{"http://localhost:8082"},
			"resource_domains": []string{"https://stackkit.cc"},
		},
		"openai/outputTemplate": onboardingResourceURI,
	}
}

func boolPtr(v bool) *bool {
	return &v
}

// IsLoopbackListenAddr reports whether an HTTP listen address is loopback-only.
func IsLoopbackListenAddr(addr string) bool {
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

func isLoopbackListenAddr(addr string) bool {
	return IsLoopbackListenAddr(addr)
}

// RequireMCPToken requires a bearer or X-StackKit-MCP-Token value.
func RequireMCPToken(token string, next http.Handler) http.Handler {
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

func requireMCPToken(token string, next http.Handler) http.Handler {
	return RequireMCPToken(token, next)
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

func (a *App) openMCPResources() []map[string]any {
	resources := []map[string]any{
		{"uri": "api/openapi.v1.yaml", "mimeType": "application/yaml"},
		{"uri": "docs/agent/stackkit-mcp.md", "mimeType": "text/markdown"},
		{"uri": onboardingResourceURI, "mimeType": "text/html"},
	}
	for uri := range a.docs {
		if uri == "api/openapi.v1.yaml" || uri == "docs/agent/stackkit-mcp.md" {
			continue
		}
		if strings.HasSuffix(uri, ".md") && strings.Contains(uri, "agent/") {
			resources = append(resources, map[string]any{"uri": "stackkit://" + uri, "mimeType": mimeTypeForResource(uri)})
		}
	}
	return resources
}

func enabledModes(modes map[string]bool) []string {
	order := []string{"docs", "local", "server", "actions"}
	out := make([]string, 0, len(order))
	for _, mode := range order {
		if modes[mode] {
			out = append(out, mode)
		}
	}
	return out
}

func stackkitPrompts() []map[string]any {
	return []map[string]any{
		{"name": "stackkit_basekit_autonomous_rollout"},
		{"name": "stackkit_inspect_existing_rollout"},
		{"name": "stackkit_diagnose_failed_rollout"},
		{"name": "stackkit_enable_monitoring_addon"},
		{"name": "stackkit_ssh_rollout"},
	}
}

func toolDefinition(name string, readOnly, destructive, idempotent bool) map[string]any {
	return map[string]any{
		"name":             name,
		"widgetAccessible": isWidgetAccessibleTool(name),
		"annotations": map[string]any{
			"readOnly":    readOnly,
			"destructive": destructive,
			"idempotent":  idempotent,
			"openWorld":   false,
		},
	}
}
