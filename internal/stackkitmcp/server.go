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

	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/kombifyio/stackkits/internal/standaloneoperations"
)

//go:embed assets/state-console.html
var stateConsoleHTML string

const stateConsoleResourceURI = "ui://stackkits/state-console.html"

// App owns the StackKits MCP registration shared by stackkit-mcp and stackkit-server.
type App struct {
	opts            Options
	docs            map[string]string
	cliBinding      *cliBinaryBinding
	cliBindingError error
}

// New creates a configured StackKits MCP app.
func New(opts Options) *App {
	opts = opts.normalized()
	app := &App{opts: opts, docs: loadDocs()}
	if opts.Modes["actions"] && (opts.AllowWrite || stackspecadmission.RejectOperationalV1(opts.Version)) {
		app.cliBinding, app.cliBindingError = bindCLIBinary(opts)
	}
	return app
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
		a.addServerTools(server, a.opts.Modes["actions"] && a.cliBinding != nil && stackspecadmission.RejectOperationalV1(a.opts.Version))
	}
	if a.opts.Modes["actions"] {
		a.addReadOnlyActions(server)
		if a.opts.AllowWrite {
			a.addActions(server)
		}
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
		toolDefinition("stackkit_state_console", true, false, true),
	}
	if a.opts.Modes["local"] {
		tools = append(tools,
			toolDefinition("stackkit_validate_spec", true, false, true),
			toolDefinition("stackkit_generate_preview", true, false, true),
			toolDefinition("stackkit_compat_check", true, false, true),
			toolDefinition("stackkit_config_get", true, false, true),
		)
	}
	nativeActions := a.opts.Modes["actions"] && a.cliBinding != nil && stackspecadmission.RejectOperationalV1(a.opts.Version)
	if a.opts.Modes["server"] && !nativeActions {
		tools = append(tools,
			toolDefinition("stackkit_status", true, false, true),
			toolDefinition("stackkit_logs_list", true, false, true),
			toolDefinition("stackkit_log_get", true, false, true),
		)
		if !stackspecadmission.RejectOperationalV1(a.opts.Version) {
			tools = append(tools,
				toolDefinition("stackkit_verify", true, false, false),
				toolDefinition("stackkit_doctor", true, false, false),
			)
		}
	}
	if nativeActions {
		for _, operation := range standaloneoperations.All() {
			if !operation.Mutation {
				tools = append(tools, operationToolDefinition(operation))
			}
		}
	}
	if a.opts.Modes["actions"] && a.opts.AllowWrite && stackspecadmission.RejectOperationalV1(a.opts.Version) {
		tools = append(tools, toolDefinition("stackkit_config_set", false, true, true))
		if a.cliBinding != nil {
			for _, operation := range standaloneoperations.All() {
				if operation.Mutation {
					tools = append(tools, operationToolDefinition(operation))
				}
			}
		}
	}
	return map[string]any{
		"schemaVersion": "2026-06-08",
		"name":          "stackkit",
		"version":       a.opts.Version,
		"title":         "StackKits Native MCP Connector",
		"description":   "One user-facing StackKits MCP connection for CUE-governed authoring, resolution guidance, and gated same-build StackKits operations.",
		"userModel": map[string]any{
			"connectionName":   "stackkit",
			"localEntrypoint":  "stackkit-mcp stdio or loopback adapter",
			"serverEntrypoint": "stackkit-server POST /mcp",
			"appAuthority":     "embedded CUE Definition metadata; not a second production connector",
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
			"localConnectorAuthority": "same-build sibling CLI required; mutating actions additionally require write mode",
			"managedServerless":       "out-of-scope",
			"providerLifecycle":       "out-of-scope; owned by TechStack or another external executor",
		},
		"modes":      enabledModes(a.opts.Modes),
		"allowWrite": a.opts.AllowWrite,
		"serverURL":  a.opts.ServerURL,
		"tools":      tools,
		"resources":  a.openMCPResources(),
		"prompts":    stackkitPrompts(),
		"appResources": []map[string]any{{
			"uri":                  stateConsoleResourceURI,
			"mimeType":             "text/html;profile=mcp-app",
			"description":          "StackKits State Console for local state review, planning, operation approval, and evidence",
			"steps":                []string{"workspace", "explicit-kit-configuration", "resolution-inputs", "review-and-plan", "operation-approval-and-evidence"},
			"callsToolsFromWidget": true,
			"appsSdkMetadata":      stateConsoleResourceMeta(),
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
	stateConsoleTool := mcpTool("stackkit_state_console", "Return StackKits State Console metadata.", true, false, true)
	stateConsoleTool.Meta["ui"] = map[string]any{"resourceUri": stateConsoleResourceURI}
	stateConsoleTool.Meta["openai/outputTemplate"] = stateConsoleResourceURI
	stateConsoleTool.Meta["openai/toolInvocation/invoking"] = "Opening StackKits State Console"
	stateConsoleTool.Meta["openai/toolInvocation/invoked"] = "StackKits State Console opened"
	mcp.AddTool(server, stateConsoleTool, a.stateConsole)

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
		Name:        "stackkits-state-console",
		Title:       "StackKits State Console",
		URI:         stateConsoleResourceURI,
		MIMEType:    "text/html;profile=mcp-app",
		Size:        int64(len(stateConsoleHTML)),
		Description: "MCP App for local state review, plan inspection, operation approval, and Owner evidence.",
		Meta:        stateConsoleResourceMeta(),
		Annotations: &mcp.Annotations{Audience: []mcp.Role{mcp.Role("assistant")}, Priority: 1.0},
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      req.Params.URI,
			MIMEType: "text/html;profile=mcp-app",
			Text:     stateConsoleHTML,
			Meta:     stateConsoleResourceMeta(),
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

func (a *App) addServerTools(server *mcp.Server, nativeActions bool) {
	if nativeActions {
		return
	}
	mcp.AddTool(server, mcpTool("stackkit_status", "GET /api/v1/status from stackkit-server.", true, false, true), a.status)
	mcp.AddTool(server, mcpTool("stackkit_logs_list", "GET /api/v1/logs from stackkit-server.", true, false, true), a.logsList)
	mcp.AddTool(server, mcpTool("stackkit_log_get", "GET /api/v1/logs/{runID} from stackkit-server.", true, false, true), a.logGet)
	if !stackspecadmission.RejectOperationalV1(a.opts.Version) {
		mcp.AddTool(server, mcpTool("stackkit_verify", "POST /api/v1/verify against the exact v0.6 stackkit-server.", true, false, false), a.verify)
		mcp.AddTool(server, mcpTool("stackkit_doctor", "POST /api/v1/doctor against the exact v0.6 stackkit-server.", true, false, false), a.doctor)
	}
}

func (a *App) addActions(server *mcp.Server) {
	if !stackspecadmission.RejectOperationalV1(a.opts.Version) {
		return
	}
	mcp.AddTool(server, mcpTool("stackkit_config_set", "Create or expected-spec-hash compare-and-swap a canonical CUE-validated StackSpec v2.", false, true, true), a.configSet)
	if a.cliBinding == nil {
		return
	}
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Init), a.stackkitInitV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Resolve), a.stackkitResolveV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Generate), a.stackkitGenerateV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Apply), a.stackkitApplyV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Backup), a.stackkitBackupV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Restore), a.stackkitRestoreV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Upgrade), a.stackkitUpgradeV2)
}

func (a *App) addReadOnlyActions(server *mcp.Server) {
	if a.cliBinding == nil || !stackspecadmission.RejectOperationalV1(a.opts.Version) {
		return
	}
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Validate), a.stackkitValidateV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Plan), a.stackkitPlanV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Verify), a.stackkitVerifyV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Status), a.stackkitStatusV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Logs), a.stackkitLogsV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Drift), a.stackkitDriftV2)
}

func operationMCPTool(id standaloneoperations.ID) *mcp.Tool {
	operation, ok := standaloneoperations.Lookup(id)
	if !ok {
		panic("missing standalone operation: " + string(id))
	}
	return mcpTool(operation.ToolName, operation.Description, !operation.Mutation, operation.Destructive, operation.Idempotent)
}

func operationToolDefinition(operation standaloneoperations.Contract) map[string]any {
	definition := toolDefinition(operation.ToolName, !operation.Mutation, operation.Destructive, operation.Idempotent)
	definition["operation"] = operation.ID
	definition["command"] = operation.Command
	definition["ownerApproval"] = operation.OwnerApproval
	return definition
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

var stateConsoleToolNames = map[string]bool{
	"stackkit_state_console":    true,
	"stackkit_self_check_plan":  true,
	"stackkit_status":           true,
	"stackkit_config_get":       true,
	"stackkit_init":             true,
	"stackkit_config_set":       true,
	"stackkit_validate_spec":    true,
	"stackkit_validate":         true,
	"stackkit_generate_preview": true,
	"stackkit_resolve":          true,
	"stackkit_generate":         true,
	"stackkit_plan":             true,
	"stackkit_verify":           true,
	"stackkit_verify_plan":      true,
	"stackkit_backup":           true,
	"stackkit_restore":          true,
	"stackkit_upgrade":          true,
	"stackkit_drift":            true,
	"stackkit_logs":             true,
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
	return stateConsoleToolNames[name]
}

func stateConsoleResourceMeta() mcp.Meta {
	return mcp.Meta{
		"ui": map[string]any{
			"prefersBorder": true,
			"csp": map[string]any{
				"connectDomains":  []string{"http://localhost:8082"},
				"resourceDomains": []string{"https://stackkit.cc"},
			},
		},
		"openai/widgetDescription":      "Local StackKits state review, Inventory-bound planning, operation approval, and Owner evidence.",
		"openai/widgetPrefersBorder":    true,
		"openai/widgetAccessible":       true,
		"openai/resultCanProduceWidget": true,
		"openai/widgetCSP": map[string]any{
			"connect_domains":  []string{"http://localhost:8082"},
			"resource_domains": []string{"https://stackkit.cc"},
		},
		"openai/outputTemplate": stateConsoleResourceURI,
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
		{"uri": stateConsoleResourceURI, "mimeType": "text/html"},
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
