package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kombifyio/stackkits/cmd/stackkit-mcp/internal/util"
	"github.com/kombifyio/stackkits/internal/config"
	cuepkg "github.com/kombifyio/stackkits/internal/cue"
)

func main() {
	modeFlag := flag.String("mode", "docs", "comma-separated modes: docs,local,server,actions")
	serverURL := flag.String("server-url", firstNonEmpty(os.Getenv("STACKKITS_SERVER_URL"), "http://localhost:8082"), "stackkit-server URL")
	apiKey := flag.String("api-key", "", "stackkit-server API key")
	transport := flag.String("transport", "stdio", "transport: stdio or http")
	addr := flag.String("addr", "127.0.0.1:8091", "HTTP listen address when --transport=http")
	mcpToken := flag.String("mcp-token", "", "Bearer token required by MCP HTTP transport")
	flag.Parse()

	opts := serverOptions{
		modes:      parseModes(*modeFlag),
		serverURL:  strings.TrimRight(*serverURL, "/"),
		apiKey:     firstNonEmpty(*apiKey, os.Getenv("STACKKITS_API_KEY")),
		transport:  strings.ToLower(strings.TrimSpace(*transport)),
		mcpToken:   firstNonEmpty(*mcpToken, os.Getenv("STACKKIT_MCP_TOKEN")),
		allowWrite: envBoolValue(os.Getenv("STACKKIT_MCP_ALLOW_WRITE")),
	}
	app := &stackkitMCP{opts: opts, docs: loadDocs()}
	server := app.newServer()

	switch opts.transport {
	case "http":
		if (opts.modes["server"] || opts.modes["actions"]) && !isLoopbackListenAddr(*addr) && opts.mcpToken == "" {
			log.Fatal("--mcp-token or STACKKIT_MCP_TOKEN is required when management or action tools are exposed over non-loopback HTTP")
		}
		handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
			SessionTimeout: 30 * time.Minute,
		})
		var httpHandler http.Handler = handler
		if opts.mcpToken != "" {
			httpHandler = requireMCPToken(opts.mcpToken, httpHandler)
		}
		log.Fatal(newHTTPServer(*addr, httpHandler).ListenAndServe())
	default:
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatal(err)
		}
	}
}

type queryInput struct {
	Query string `json:"query" jsonschema:"search query"`
}

type endpointInput struct {
	Path string `json:"path" jsonschema:"HTTP path such as /api/v1/status"`
}

type specPathInput struct {
	SpecPath string `json:"spec_path,omitempty" jsonschema:"path to stack-spec.yaml"`
	BaseDir  string `json:"base_dir,omitempty" jsonschema:"workspace or kit catalog root"`
}

type verifyInput struct {
	HTTP   bool `json:"http,omitempty"`
	Strict bool `json:"strict,omitempty"`
}

type runIDInput struct {
	RunID string `json:"run_id" jsonschema:"run ID such as 20260517-120000"`
}

type actionInput struct {
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
}

func (a *stackkitMCP) docsSearch(ctx context.Context, req *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, any, error) {
	query := strings.ToLower(strings.TrimSpace(in.Query))
	if query == "" {
		return util.TextResult("query is required"), nil, nil
	}
	type hit struct {
		Path    string `json:"path"`
		Snippet string `json:"snippet"`
	}
	var hits []hit
	for path, body := range a.docs {
		lower := strings.ToLower(body)
		if idx := strings.Index(lower, query); idx >= 0 {
			start := max(0, idx-120)
			end := min(len(body), idx+240)
			hits = append(hits, hit{Path: path, Snippet: strings.TrimSpace(body[start:end])})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Path < hits[j].Path })
	return util.JSONResult(hits), hits, nil
}

func (a *stackkitMCP) apiOverview(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	endpoints := openAPIEndpoints(openAPISpec())
	return util.JSONResult(endpoints), endpoints, nil
}

func (a *stackkitMCP) apiEndpoint(ctx context.Context, req *mcp.CallToolRequest, in endpointInput) (*mcp.CallToolResult, any, error) {
	snippet := endpointSnippet(openAPISpec(), in.Path)
	if snippet == "" {
		return util.TextResult("endpoint not found in OpenAPI spec"), nil, nil
	}
	return util.TextResult(snippet), map[string]string{"path": in.Path, "snippet": snippet}, nil
}

func (a *stackkitMCP) getOpenAPISpec(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return util.TextResult(openAPISpec()), nil, nil
}

func (a *stackkitMCP) installPlan(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	steps := []map[string]any{
		{"command": "curl -sSL https://base.stackkit.cc | sh", "purpose": "install stackkit, stackkit-server, stackkit-mcp, packaged OpenTofu, and BaseKit definitions", "mutation": true},
		{"command": "stackkit init base-kit --non-interactive --admin-email <operator-email>", "purpose": "write stack-spec.yaml", "mutation": true},
		{"command": "stackkit prepare --dry-run", "purpose": "check host prerequisites without mutation", "mutation": false},
		{"command": "stackkit validate", "purpose": "validate StackSpec and CUE", "mutation": false},
		{"command": "stackkit generate --force", "purpose": "generate deployment artifacts", "mutation": true},
		{"command": "stackkit plan", "purpose": "preview OpenTofu changes", "mutation": false},
		{"command": "stackkit apply", "purpose": "apply after operator approval", "mutation": true},
		{"command": "stackkit verify --http --json", "purpose": "produce functional evidence", "mutation": false},
	}
	out := map[string]any{
		"scenario": "basekit-autonomous-rollout",
		"kit":      "base-kit",
		"hub_url":  "http://base.home.localhost",
		"steps":    steps,
		"notes": []string{
			"BaseKit is release-ready.",
			"Modern Homelab and HA Kit remain alpha/scaffolding.",
			"Do not hand-edit generated rollout artifacts.",
		},
	}
	return util.JSONResult(out), out, nil
}

func (a *stackkitMCP) selfCheckPlan(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	serverURL := firstNonEmpty(a.opts.serverURL, "http://localhost:8082")
	out := map[string]any{
		"server_url": serverURL,
		"probes": []map[string]string{
			{"name": "cli", "command": "stackkit version"},
			{"name": "server", "command": "stackkit-server --help"},
			{"name": "mcp", "command": "stackkit-mcp --mode docs"},
			{"name": "api-health", "command": "curl -fsS " + serverURL + "/api/v1/health"},
			{"name": "api-status", "command": "curl -fsS " + serverURL + "/api/v1/status"},
			{"name": "verify", "command": "stackkit verify --http --json"},
		},
	}
	return util.JSONResult(out), out, nil
}

func (a *stackkitMCP) validateSpec(ctx context.Context, req *mcp.CallToolRequest, in specPathInput) (*mcp.CallToolResult, any, error) {
	baseDir := firstNonEmpty(in.BaseDir, ".")
	specPath := firstNonEmpty(in.SpecPath, config.GetDefaultSpecPath())
	loader := config.NewLoader(baseDir)
	spec, err := loader.LoadStackSpec(specPath)
	if err != nil {
		out := map[string]any{"valid": false, "error": err.Error()}
		return util.JSONResult(out), out, nil
	}
	result, err := cuepkg.NewValidator(baseDir).ValidateSpec(spec)
	if err != nil {
		out := map[string]any{"valid": false, "error": err.Error(), "stackkit": spec.StackKit}
		return util.JSONResult(out), out, nil
	}
	return util.JSONResult(result), result, nil
}

func (a *stackkitMCP) generatePreview(ctx context.Context, req *mcp.CallToolRequest, in specPathInput) (*mcp.CallToolResult, any, error) {
	baseDir := firstNonEmpty(in.BaseDir, ".")
	specPath := firstNonEmpty(in.SpecPath, config.GetDefaultSpecPath())
	loader := config.NewLoader(baseDir)
	spec, err := loader.LoadStackSpec(specPath)
	out := map[string]any{
		"dry_run": true,
		"writes":  false,
		"commands": []string{
			"stackkit validate",
			"stackkit generate --force",
			"stackkit plan",
		},
	}
	if err != nil {
		out["ready"] = false
		out["error"] = err.Error()
		return util.JSONResult(out), out, nil
	}
	out["ready"] = true
	out["stackkit"] = spec.StackKit
	out["mode"] = spec.Mode
	out["deploy_dir"] = filepath.Join(baseDir, config.GetDeployDir())
	return util.JSONResult(out), out, nil
}

func (a *stackkitMCP) compatCheck(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	out := map[string]any{
		"command": "stackkit compat",
		"notes": []string{
			"Run on the target host before apply.",
			"Use the resulting virtualization, Docker, and disk checks as host-prerequisite evidence.",
		},
	}
	return util.JSONResult(out), out, nil
}

func (a *stackkitMCP) status(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodGet, "/api/v1/status", nil)
}

func (a *stackkitMCP) verify(ctx context.Context, req *mcp.CallToolRequest, in verifyInput) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodPost, "/api/v1/verify", in)
}

func (a *stackkitMCP) logsList(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodGet, "/api/v1/logs", nil)
}

func (a *stackkitMCP) logGet(ctx context.Context, req *mcp.CallToolRequest, in runIDInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.RunID) == "" {
		return util.TextResult("run_id is required"), nil, nil
	}
	return a.serverRequest(ctx, http.MethodGet, "/api/v1/logs/"+strings.TrimSpace(in.RunID), nil)
}

func (a *stackkitMCP) doctor(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodPost, "/api/v1/doctor", map[string]any{})
}

func (a *stackkitMCP) actionPlan(ctx context.Context, req *mcp.CallToolRequest, in actionInput) (*mcp.CallToolResult, any, error) {
	command := strings.TrimSpace(in.Command)
	if command == "" && req != nil {
		command = strings.TrimPrefix(req.Params.Name, "stackkit_")
		command = "stackkit " + strings.ReplaceAll(command, "_", " ")
	}
	if len(in.Args) > 0 {
		command += " " + strings.Join(in.Args, " ")
	}
	out := map[string]any{
		"enabled":  a.opts.allowWrite,
		"command":  command,
		"executes": false,
		"note":     "This MCP tool returns an operator-approved command plan; execute through the CLI after review.",
	}
	return util.JSONResult(out), out, nil
}

func (a *stackkitMCP) serverRequest(ctx context.Context, method, path string, payload any) (*mcp.CallToolResult, any, error) {
	base := strings.TrimRight(firstNonEmpty(a.opts.serverURL, "http://localhost:8082"), "/")
	var body io.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		body = bytes.NewReader(raw)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, base+path, body)
	if err != nil {
		return nil, nil, err
	}
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(a.opts.apiKey) != "" {
		httpReq.Header.Set("X-API-Key", strings.TrimSpace(a.opts.apiKey))
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(httpReq)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		return util.TextResult(fmt.Sprintf("stackkit-server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))), nil, nil
	}
	var structured any
	if json.Valid(raw) {
		_ = json.Unmarshal(raw, &structured)
		return util.TextResult(string(raw)), structured, nil
	}
	return util.TextResult(string(raw)), string(raw), nil
}
