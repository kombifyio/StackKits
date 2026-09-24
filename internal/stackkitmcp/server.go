package stackkitmcp

import (
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
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

	discoveryOnce  sync.Once
	discoveryTools []map[string]any
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

// StreamableHTTPHandler returns the stateless MCP 2026-07-28 Streamable HTTP
// handler. Each POST is served from that request alone: the SDK issues no
// Mcp-Session-Id, ignores one a client sends, and answers GET and DELETE with
// 405. Clients on older protocol versions are still served, one request at a
// time, with SDK-synthesized initialization defaults.
func (a *App) StreamableHTTPHandler() http.Handler {
	server := a.Server()
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless: true,
	})
}

// ProtectedStreamableHTTPHandler requires the dedicated MCP token on every
// request. Without a configured token it denies every request; it never falls
// back to an open endpoint or to another credential.
func (a *App) ProtectedStreamableHTTPHandler() http.Handler {
	return RequireMCPToken(a.opts.MCPToken, a.StreamableHTTPHandler())
}

// OpenMCP returns agent-native discovery metadata for the local MCP surface.
func (a *App) OpenMCP() map[string]any {
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
			"type":      "streamable-http",
			"endpoint":  "/mcp",
			"stateless": true,
		},
		"auth": map[string]any{
			"credential":      "dedicated MCP token as Authorization: Bearer or X-StackKit-MCP-Token; the stackkit-server API key is never accepted",
			"tokenSources":    []string{"--mcp-token", MCPTokenEnv, MCPTokenFileEnv},
			"serverEndpoint":  "stackkit-server POST /mcp denies every request until an MCP token is configured",
			"loopbackAdapter": "stackkit-mcp --transport http serves without a token only on a loopback listen address",
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
		"tools":      a.registeredTools(),
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
	mcp.AddTool(server, mcpTool("stackkit_application_delivery_compatibility", "Return the CUE-owned application compatibility matrix for Coolify, Komodo, and standalone Compose.", true, false, true), a.applicationDeliveryCompatibility)
	mcp.AddTool(server, mcpTool("stackkit_use_case_compute_tiers", "Legacy v2alpha1 compatibility facts only. Native intent uses stackkit_module_profiles, not a kit-wide tier.", true, false, true), a.useCaseComputeTiers)
	mcp.AddTool(server, mcpTool("stackkit_module_profiles", "List native module-owned compute, storage and accelerator profile contracts from embedded CUE authority. Missing resource axes stay unknown; no profile is selected or recommended.", true, false, false), a.moduleProfiles)
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
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Setup), a.stackkitSetupV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Backup), a.stackkitBackupV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.BackupScheduleEnable), a.stackkitBackupScheduleEnableV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.BackupScheduleDisable), a.stackkitBackupScheduleDisableV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Restore), a.stackkitRestoreV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.RestoreAbandon), a.stackkitRestoreAbandonV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Upgrade), a.stackkitUpgradeV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Remove), a.stackkitRemoveV2)
	a.addCatalogOperationTools(server, true)
}

func (a *App) addReadOnlyActions(server *mcp.Server) {
	if a.cliBinding == nil || !stackspecadmission.RejectOperationalV1(a.opts.Version) {
		return
	}
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Validate), a.stackkitValidateV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Plan), a.stackkitPlanV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Verify), a.stackkitVerifyV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Status), a.stackkitStatusV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.BackupScheduleStatus), a.stackkitBackupScheduleStatusV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.BackupStatus), a.stackkitBackupStatusV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Logs), a.stackkitLogsV2)
	mcp.AddTool(server, operationMCPTool(standaloneoperations.Drift), a.stackkitDriftV2)
	a.addCatalogOperationTools(server, false)
}

func operationMCPTool(id standaloneoperations.ID) *mcp.Tool {
	operation, ok := standaloneoperations.Lookup(id)
	if !ok {
		panic("missing standalone operation: " + string(id))
	}
	tool := mcpTool(operation.ToolName, operation.Description, !operation.Mutation, operation.Destructive, operation.Idempotent)
	tool.Annotations.OpenWorldHint = boolPtr(operation.OpenWorld)
	return tool
}

// registeredTools lists exactly the tools Server() registers, read back over an
// in-memory MCP connection, so /openmcp.json cannot drift from registration.
// The result is computed once because the options are fixed after New.
func (a *App) registeredTools() []map[string]any {
	a.discoveryOnce.Do(func() {
		tools, err := a.listRegisteredTools()
		if err != nil {
			slog.Error("StackKits MCP discovery could not list registered tools", "error", err)
			tools = []map[string]any{}
		}
		a.discoveryTools = tools
	})
	return a.discoveryTools
}

func (a *App) listRegisteredTools() ([]map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := a.Server().Connect(ctx, serverTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = serverSession.Close() }()
	client := mcp.NewClient(&mcp.Implementation{Name: "stackkit-openmcp", Version: a.opts.Version}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = clientSession.Close() }()

	// Operation tools share names with the exact-v0.6 HTTP compatibility tools,
	// so contract metadata applies only when the native operations are the ones
	// registered.
	operations := map[string]standaloneoperations.Contract{}
	if a.opts.Modes["actions"] && a.cliBinding != nil && stackspecadmission.RejectOperationalV1(a.opts.Version) {
		for _, operation := range standaloneoperations.All() {
			operations[operation.ToolName] = operation
		}
	}
	tools := []map[string]any{}
	for tool, err := range clientSession.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		definition := registeredToolDefinition(tool)
		if operation, ok := operations[tool.Name]; ok {
			definition["operation"] = operation.ID
			definition["command"] = operation.Command
			definition["ownerApproval"] = operation.OwnerApproval
		}
		tools = append(tools, definition)
	}
	return tools, nil
}

func registeredToolDefinition(tool *mcp.Tool) map[string]any {
	annotations := tool.Annotations
	if annotations == nil {
		annotations = &mcp.ToolAnnotations{}
	}
	return map[string]any{
		"name":             tool.Name,
		"widgetAccessible": tool.Meta["openai/widgetAccessible"] == true,
		"annotations": map[string]any{
			"readOnly":    annotations.ReadOnlyHint,
			"destructive": hintValue(annotations.DestructiveHint, true),
			"idempotent":  annotations.IdempotentHint,
			"openWorld":   hintValue(annotations.OpenWorldHint, true),
		},
	}
}

// hintValue applies the MCP default for an absent optional annotation hint.
func hintValue(hint *bool, absent bool) bool {
	if hint == nil {
		return absent
	}
	return *hint
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
	"stackkit_state_console":           true,
	"stackkit_self_check_plan":         true,
	"stackkit_status":                  true,
	"stackkit_config_get":              true,
	"stackkit_init":                    true,
	"stackkit_config_set":              true,
	"stackkit_validate_spec":           true,
	"stackkit_validate":                true,
	"stackkit_generate_preview":        true,
	"stackkit_resolve":                 true,
	"stackkit_generate":                true,
	"stackkit_plan":                    true,
	"stackkit_verify":                  true,
	"stackkit_verify_plan":             true,
	"stackkit_backup":                  true,
	"stackkit_backup_status":           true,
	"stackkit_backup_schedule_enable":  true,
	"stackkit_backup_schedule_disable": true,
	"stackkit_backup_schedule_status":  true,
	"stackkit_restore":                 true,
	"stackkit_restore_abandon":         true,
	"stackkit_upgrade":                 true,
	"stackkit_remove":                  true,
	"stackkit_drift":                   true,
	"stackkit_logs":                    true,
	"stackkit_logs_list":               true,
	"stackkit_doctor":                  true,
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
				"connectDomains":  []string{DefaultLocalServerURL},
				"resourceDomains": []string{"https://stackkit.cc"},
			},
		},
		"openai/widgetDescription":      "Local StackKits state review, Inventory-bound planning, operation approval, and Owner evidence.",
		"openai/widgetPrefersBorder":    true,
		"openai/widgetAccessible":       true,
		"openai/resultCanProduceWidget": true,
		"openai/widgetCSP": map[string]any{
			"connect_domains":  []string{DefaultLocalServerURL},
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
