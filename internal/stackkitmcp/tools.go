package stackkitmcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	cuepkg "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/kombifyio/stackkits/pkg/models"
	"gopkg.in/yaml.v3"
)

const maxMCPActionDuration = 14*time.Minute + 30*time.Second

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

type stackkitCommandInput struct {
	BaseDir        string            `json:"base_dir,omitempty" jsonschema:"workspace directory"`
	SpecPath       string            `json:"spec_path,omitempty" jsonschema:"stack spec path"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty" jsonschema:"command timeout capped at 870 seconds"`
	ExtraEnv       map[string]string `json:"extra_env,omitempty" jsonschema:"additional environment variables"`
}

type initInput struct {
	stackkitCommandInput
	StackKit       string `json:"stackkit,omitempty" jsonschema:"StackKit slug, default basement-kit"`
	AdminEmail     string `json:"admin_email,omitempty" jsonschema:"operator or owner email"`
	Mode           string `json:"mode,omitempty" jsonschema:"install mode"`
	Context        string `json:"context,omitempty" jsonschema:"node context such as local, cloud, or pi"`
	Domain         string `json:"domain,omitempty" jsonschema:"domain override"`
	ComputeTier    string `json:"compute_tier,omitempty" jsonschema:"compute tier"`
	ServiceProfile string `json:"service_profile,omitempty" jsonschema:"BaseKit service profile"`
	Force          bool   `json:"force,omitempty" jsonschema:"overwrite existing stack-spec.yaml"`
	LocalDNS       bool   `json:"local_dns,omitempty" jsonschema:"use local DNS"`
	LocalName      string `json:"local_name,omitempty" jsonschema:"local DNS short name"`
}

// architectureV2CommandInput intentionally excludes environment injection and
// all legacy host/topology fields. Inventory and execution-channel selection
// are separate governed inputs, never MCP convenience flags.
type architectureV2CommandInput struct {
	BaseDir        string `json:"base_dir,omitempty" jsonschema:"workspace directory"`
	SpecPath       string `json:"spec_path,omitempty" jsonschema:"canonical StackSpec v2 path"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty" jsonschema:"command timeout capped at 870 seconds"`
}

func (in architectureV2CommandInput) commandInput() stackkitCommandInput {
	return stackkitCommandInput{BaseDir: in.BaseDir, SpecPath: in.SpecPath, TimeoutSeconds: in.TimeoutSeconds}
}

type architectureV2InitInput struct {
	architectureV2CommandInput
	KitProfile string `json:"kit_profile" jsonschema:"canonical StackKit profile"`
	Name       string `json:"name,omitempty" jsonschema:"deployment contract ID"`
	DomainBase string `json:"domain_base,omitempty" jsonschema:"CUE-governed network.domain.base override when required"`
}

type architectureV2PrepareInput struct {
	architectureV2CommandInput
	DryRun     bool `json:"dry_run,omitempty"`
	SkipDocker bool `json:"skip_docker,omitempty"`
	SkipTofu   bool `json:"skip_tofu,omitempty"`
}

type architectureV2ResolveInput struct {
	architectureV2CommandInput
	InventoryPath string `json:"inventory_path" jsonschema:"path to observed Inventory owned outside StackSpec"`
}

type architectureV2ApplyInput struct {
	architectureV2CommandInput
	AutoApprove bool `json:"auto_approve,omitempty"`
}

type prepareInput struct {
	stackkitCommandInput
	DryRun     bool   `json:"dry_run,omitempty"`
	Host       string `json:"host,omitempty"`
	User       string `json:"user,omitempty"`
	KeyPath    string `json:"key_path,omitempty"`
	SkipDocker bool   `json:"skip_docker,omitempty"`
	SkipTofu   bool   `json:"skip_tofu,omitempty"`
	Force      bool   `json:"force,omitempty"`
}

type generateInput struct {
	stackkitCommandInput
	Force     *bool  `json:"force,omitempty"`
	OutputDir string `json:"output_dir,omitempty"`
	Fragments bool   `json:"fragments,omitempty"`
}

type planInput struct {
	stackkitCommandInput
	OutFile string `json:"out_file,omitempty"`
	Destroy bool   `json:"destroy,omitempty"`
}

type applyInput struct {
	stackkitCommandInput
	PlanFile         string `json:"plan_file,omitempty"`
	AutoApprove      *bool  `json:"auto_approve,omitempty"`
	Verify           *bool  `json:"verify,omitempty"`
	VerifyHTTP       *bool  `json:"verify_http,omitempty"`
	VerifyStrict     bool   `json:"verify_strict,omitempty"`
	SkipPlatformApps *bool  `json:"skip_platform_apps,omitempty"`
}

type updateInput struct {
	stackkitCommandInput
	To          string   `json:"to,omitempty"`
	KitChannel  string   `json:"kit_channel,omitempty"`
	DryRun      bool     `json:"dry_run,omitempty"`
	AutoApprove *bool    `json:"auto_approve,omitempty"`
	Volumes     []string `json:"volumes,omitempty"`
}

type configSetInput struct {
	BaseDir          string `json:"base_dir,omitempty" jsonschema:"workspace directory"`
	SpecPath         string `json:"spec_path,omitempty" jsonschema:"target stack spec path"`
	YAML             string `json:"yaml" jsonschema:"complete StackSpec v2 content"`
	ExpectedSpecHash string `json:"expected_spec_hash,omitempty" jsonschema:"exact current CUE-normalized sha256 spec hash required when replacing existing intent"`
}

type rolloutInput struct {
	stackkitCommandInput
	StackKit          string `json:"stackkit,omitempty"`
	AdminEmail        string `json:"admin_email,omitempty"`
	Mode              string `json:"mode,omitempty"`
	Context           string `json:"context,omitempty"`
	Domain            string `json:"domain,omitempty"`
	ComputeTier       string `json:"compute_tier,omitempty"`
	ServiceProfile    string `json:"service_profile,omitempty"`
	Host              string `json:"host,omitempty"`
	User              string `json:"user,omitempty"`
	KeyPath           string `json:"key_path,omitempty"`
	LocalDNS          bool   `json:"local_dns,omitempty"`
	LocalName         string `json:"local_name,omitempty"`
	SkipInit          bool   `json:"skip_init,omitempty"`
	PrepareDryRun     bool   `json:"prepare_dry_run,omitempty"`
	PrepareSkipDocker bool   `json:"prepare_skip_docker,omitempty"`
	PrepareSkipTofu   bool   `json:"prepare_skip_tofu,omitempty"`
	ApplyAutoApprove  *bool  `json:"apply_auto_approve,omitempty"`
	SkipPlatformApps  *bool  `json:"skip_platform_apps,omitempty"`
	VerifyHTTP        *bool  `json:"verify_http,omitempty"`
}

func (a *App) docsSearch(ctx context.Context, req *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, any, error) {
	query := strings.ToLower(strings.TrimSpace(in.Query))
	if query == "" {
		return TextResult("query is required"), nil, nil
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
	return JSONResult(hits), hits, nil
}

func (a *App) apiOverview(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	endpoints := openAPIEndpoints(openAPISpec())
	return JSONResult(endpoints), endpoints, nil
}

func (a *App) apiEndpoint(ctx context.Context, req *mcp.CallToolRequest, in endpointInput) (*mcp.CallToolResult, any, error) {
	snippet := endpointSnippet(openAPISpec(), in.Path)
	if snippet == "" {
		return TextResult("endpoint not found in OpenAPI spec"), nil, nil
	}
	return TextResult(snippet), map[string]string{"path": in.Path, "snippet": snippet}, nil
}

func (a *App) getOpenAPISpec(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return TextResult(openAPISpec()), nil, nil
}

func (a *App) installPlan(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	if stackspecadmission.RejectOperationalV1(a.opts.Version) {
		steps := []map[string]any{
			{"command": "stackkit version", "purpose": "require the exact native v0.7 candidate bundle and its packaged definitions", "mutation": false},
			{"command": "stackkit init basement-kit --non-interactive", "purpose": "materialize canonical StackSpec v2 from the embedded CUE authoring contract", "mutation": true},
			{"command": "stackkit prepare --dry-run", "purpose": "check local host prerequisites without selecting a provider or transport", "mutation": false},
			{"command": "stackkit validate", "purpose": "validate desired StackSpec v2 intent only", "mutation": false},
			{"command": "stackkit resolve --inventory <observed-inventory.json> --output deploy/.stackkit/resolved-plan.json", "purpose": "bind desired intent to externally observed Inventory", "mutation": true},
			{"command": "stackkit generate", "purpose": "render the exact authorized ResolvedPlan", "mutation": true},
			{"command": "stackkit plan", "purpose": "preview the exact persisted plan", "mutation": false},
			{"command": "stackkit apply", "purpose": "apply only when the ResolvedPlan reports readiness", "mutation": true},
			{"command": "stackkit verify --json", "purpose": "verify the exact v2 intent, plan, manifest, receipt, and outputs", "mutation": false},
		}
		out := map[string]any{
			"scenario": "architecture-v2-basement-rollout", "kit": "basement-kit", "source_version": stackspecmigration.SourceVersionV2Alpha1,
			"steps": steps,
			"notes": []string{
				"StackSpec never contains provider lifecycle, credentials, management addresses, or observed host facts.",
				"Spec validation is not generation or apply readiness; the Inventory-bound ResolvedPlan is authoritative.",
				"Do not hand-edit generated rollout artifacts.",
			},
		}
		return JSONResult(out), out, nil
	}
	steps := []map[string]any{
		{"command": "curl -sSL https://base.stackkit.cc | sh", "purpose": "install stackkit, stackkit-server, stackkit-mcp, packaged OpenTofu, and BaseKit definitions", "mutation": true},
		{"command": "stackkit init basement-kit --non-interactive --admin-email <operator-email>", "purpose": "write stack-spec.yaml", "mutation": true},
		{"command": "stackkit prepare --dry-run", "purpose": "check host prerequisites without mutation", "mutation": false},
		{"command": "stackkit validate", "purpose": "validate StackSpec and CUE", "mutation": false},
		{"command": "stackkit generate --force", "purpose": "generate deployment artifacts", "mutation": true},
		{"command": "stackkit plan", "purpose": "preview OpenTofu changes", "mutation": false},
		{"command": "stackkit apply", "purpose": "apply after operator approval", "mutation": true},
		{"command": "stackkit verify --http --json", "purpose": "produce functional evidence", "mutation": false},
	}
	out := map[string]any{
		"scenario": "basekit-autonomous-rollout",
		"kit":      "basement-kit",
		"hub_url":  "http://base.home.localhost",
		"steps":    steps,
		"notes": []string{
			"BaseKit is release-ready.",
			"Unreleased kit definitions are outside this public install plan.",
			"Do not hand-edit generated rollout artifacts.",
		},
	}
	return JSONResult(out), out, nil
}

func (a *App) selfCheckPlan(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	serverURL := firstNonEmpty(a.opts.ServerURL, "http://localhost:8082")
	verifyCommand := "stackkit verify --http --json"
	statusPurpose := "legacy node-local rollout status"
	if stackspecadmission.RejectOperationalV1(a.opts.Version) {
		verifyCommand = "stackkit verify --json"
		statusPurpose = "spec-only status; resolve-required must not be treated as operational readiness"
	}
	out := map[string]any{
		"server_url":     serverURL,
		"status_purpose": statusPurpose,
		"probes": []map[string]string{
			{"name": "cli", "command": "stackkit version"},
			{"name": "server", "command": "stackkit-server --help"},
			{"name": "mcp", "command": "stackkit-mcp --mode docs"},
			{"name": "api-health", "command": "curl -fsS " + serverURL + "/api/v1/health"},
			{"name": "api-status", "command": "curl -fsS " + serverURL + "/api/v1/status"},
			{"name": "verify", "command": verifyCommand},
		},
	}
	return JSONResult(out), out, nil
}

func (a *App) validateSpec(ctx context.Context, req *mcp.CallToolRequest, in specPathInput) (*mcp.CallToolResult, any, error) {
	baseDir, err := a.workspaceDir(in.BaseDir)
	if err != nil {
		out := map[string]any{"valid": false, "error": err.Error()}
		return JSONResult(out), out, nil
	}
	specPath := firstNonEmpty(in.SpecPath, config.GetDefaultSpecPath())
	loader := config.NewLoader(baseDir)
	loaded, err := loader.ReadStackSpecDocument(specPath)
	if err != nil {
		out := map[string]any{"valid": false, "error": err.Error()}
		return JSONResult(out), out, nil
	}
	if loaded.Document.Version == stackspecmigration.SourceVersionV2Alpha1 {
		service, serviceErr := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(a.opts.Version))
		if serviceErr != nil {
			out := map[string]any{"valid": false, "error": serviceErr.Error()}
			return JSONResult(out), out, nil
		}
		validation, validationErr := service.ValidateStackSpec(loaded.Document.Raw)
		if validationErr != nil {
			out := map[string]any{"valid": false, "source_version": loaded.Document.Version, "error": validationErr.Error()}
			return JSONResult(out), out, nil
		}
		out := map[string]any{
			"valid": true, "source_version": loaded.Document.Version,
			"stackkit": validation.KitProfile, "spec_hash": validation.SpecHash,
			"spec_accepted": true, "validation_scope": "spec-only",
		}
		return JSONResult(out), out, nil
	}
	spec, err := loader.LoadLegacyStackSpec(specPath)
	if err != nil {
		out := map[string]any{"valid": false, "source_version": loaded.Document.Version, "error": err.Error()}
		return JSONResult(out), out, nil
	}
	result, err := cuepkg.NewValidator(baseDir).ValidateSpec(spec)
	if err != nil {
		out := map[string]any{"valid": false, "source_version": loaded.Document.Version, "error": err.Error(), "stackkit": spec.StackKit}
		return JSONResult(out), out, nil
	}
	out := map[string]any{
		"valid": result.Valid, "errors": result.Errors, "warnings": result.Warnings,
		"source_version": loaded.Document.Version, "stackkit": spec.StackKit,
		"operational":        !stackspecadmission.RejectOperationalV1(a.opts.Version),
		"migration_required": stackspecadmission.RejectOperationalV1(a.opts.Version),
		"validation_scope":   "legacy-read-only",
	}
	return JSONResult(out), out, nil
}

func (a *App) generatePreview(ctx context.Context, req *mcp.CallToolRequest, in specPathInput) (*mcp.CallToolResult, any, error) {
	baseDir, err := a.workspaceDir(in.BaseDir)
	if err != nil {
		out := map[string]any{"dry_run": true, "writes": false, "ready": false, "error": err.Error()}
		return JSONResult(out), out, nil
	}
	specPath := firstNonEmpty(in.SpecPath, config.GetDefaultSpecPath())
	loader := config.NewLoader(baseDir)
	out := map[string]any{
		"dry_run": true,
		"writes":  false,
	}
	loaded, err := loader.ReadStackSpecDocument(specPath)
	if err != nil {
		out["ready"] = false
		out["error"] = err.Error()
		return JSONResult(out), out, nil
	}
	if loaded.Document.Version == stackspecmigration.SourceVersionV2Alpha1 {
		service, serviceErr := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(a.opts.Version))
		if serviceErr != nil {
			out["ready"], out["error"] = false, serviceErr.Error()
			return JSONResult(out), out, nil
		}
		validation, validationErr := service.ValidateStackSpec(loaded.Document.Raw)
		if validationErr != nil {
			out["ready"], out["error"] = false, validationErr.Error()
			return JSONResult(out), out, nil
		}
		out["ready"] = false
		out["spec_valid"] = true
		out["source_version"] = loaded.Document.Version
		out["stackkit"] = validation.KitProfile
		out["spec_hash"] = validation.SpecHash
		out["generation_status"] = "resolve-required"
		out["reason"] = "generation readiness requires a canonical ResolvedPlan bound to observed Inventory; spec-only validation cannot claim readiness"
		out["commands"] = []string{"stackkit validate", "stackkit resolve --inventory <inventory-file>", "stackkit generate"}
		return JSONResult(out), out, nil
	}
	if stackspecadmission.RejectOperationalV1(a.opts.Version) {
		out["ready"] = false
		out["source_version"] = loaded.Document.Version
		out["migration_required"] = true
		out["error"] = "StackSpec v1 is read-only migration input on the native v2 line"
		out["commands"] = []string{"stackkit migrate --complete-with <explicit-v2> --spec-output <stack-spec-v2.json>"}
		return JSONResult(out), out, nil
	}
	spec, err := loader.LoadLegacyStackSpec(specPath)
	if err != nil {
		out["ready"] = false
		out["error"] = err.Error()
		return JSONResult(out), out, nil
	}
	out["ready"] = true
	out["source_version"] = loaded.Document.Version
	out["stackkit"] = spec.StackKit
	out["mode"] = spec.Mode
	out["deploy_dir"] = filepath.Join(baseDir, config.GetDeployDir())
	out["commands"] = []string{"stackkit validate", "stackkit generate --force", "stackkit plan"}
	return JSONResult(out), out, nil
}

func (a *App) compatCheck(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	out := map[string]any{
		"command": "stackkit compat",
		"notes": []string{
			"Run on the target host before apply.",
			"Use the resulting virtualization, Docker, and disk checks as host-prerequisite evidence.",
		},
	}
	return JSONResult(out), out, nil
}

func (a *App) status(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodGet, "/api/v1/status", nil)
}

func (a *App) verify(ctx context.Context, req *mcp.CallToolRequest, in verifyInput) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodPost, "/api/v1/verify", in)
}

func (a *App) logsList(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodGet, "/api/v1/logs", nil)
}

func (a *App) logGet(ctx context.Context, req *mcp.CallToolRequest, in runIDInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.RunID) == "" {
		return TextResult("run_id is required"), nil, nil
	}
	return a.serverRequest(ctx, http.MethodGet, "/api/v1/logs/"+strings.TrimSpace(in.RunID), nil)
}

func (a *App) doctor(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return a.serverRequest(ctx, http.MethodPost, "/api/v1/doctor", map[string]any{})
}

func (a *App) onboardingApp(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	out := map[string]any{
		"resource": onboardingResourceURI,
		"steps": []string{
			"contact-and-workspace",
			"stackkit-select",
			"domain-and-core-settings",
			"review-and-plan",
			"rollout-and-evidence",
		},
		"defaults": map[string]any{
			"workspace":       ".",
			"spec_path":       "stack-spec.yaml",
			"stackkit":        "basement-kit",
			"mode":            "bootstrapped",
			"context":         "local",
			"domain_strategy": "local",
			"domain":          "home.localhost",
			"compute_tier":    "standard",
		},
		"domain_options": []map[string]string{
			{"id": "local", "domain": "home.localhost"},
			{"id": "managed", "domain": "kombify.me"},
			{"id": "custom", "domain": "<custom-domain>"},
			{"id": "local_dns", "domain": "<local-name>.home"},
		},
		"stackkits": []map[string]string{
			{"id": "basement-kit", "status": "beta"},
			{"id": "cloud-kit", "status": "beta"},
		},
		"config_write_enabled": a.opts.AllowWrite,
		"cli_actions_enabled":  a.cliBinding != nil,
		"cli_binding_error": func() string {
			if a.cliBindingError != nil {
				return a.cliBindingError.Error()
			}
			return ""
		}(),
		"server_url": a.opts.ServerURL,
	}
	if stackspecadmission.RejectOperationalV1(a.opts.Version) {
		service, serviceErr := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(a.opts.Version))
		if serviceErr != nil {
			failure := errorOutput("stackkit_onboarding_app", fmt.Errorf("load embedded Architecture v2 authoring authority: %w", serviceErr))
			return errorJSONResult(failure), failure, nil
		}
		profiles := []stackspecmigration.KitProfile{
			stackspecmigration.KitProfileBasement,
			stackspecmigration.KitProfileCloud,
			stackspecmigration.KitProfileModern,
		}
		kitContracts := make([]map[string]any, 0, len(profiles))
		for _, profile := range profiles {
			contract, contractErr := service.InitialStackSpecAuthoringContract(profile)
			if contractErr != nil {
				failure := errorOutput("stackkit_onboarding_app", fmt.Errorf("load %s authoring contract: %w", profile, contractErr))
				return errorJSONResult(failure), failure, nil
			}
			kitContracts = append(kitContracts, map[string]any{
				"id": profile, "authoring_status": contract.Status,
				"authoring_contract_version": contract.ContractVersion,
				"required_overrides":         append([]string(nil), contract.RequiredOverrides...),
			})
		}
		out["source_version"] = stackspecmigration.SourceVersionV2Alpha1
		out["authoring_mode"] = "cue-initial-spec"
		out["steps"] = []string{
			"workspace-and-kit-profile",
			"cue-governed-required-overrides",
			"spec-only-validation",
			"inventory-and-resolve",
			"authorized-generation",
			"apply-and-evidence",
		}
		out["defaults"] = map[string]any{
			"workspace":   ".",
			"spec_path":   "stack-spec.yaml",
			"kit_profile": string(stackspecmigration.KitProfileBasement),
			"domain_base": "",
		}
		out["domain_options"] = []map[string]string{}
		out["stackkits"] = kitContracts
		out["legacy_fields_forbidden"] = []string{"admin_email", "mode", "context", "compute_tier", "service_profile", "local_dns", "local_name"}
	}
	result := JSONResult(out)
	result.StructuredContent = out
	result.Meta = mcp.Meta{"openai/outputTemplate": onboardingResourceURI}
	return result, out, nil
}

func (a *App) configGet(ctx context.Context, req *mcp.CallToolRequest, in specPathInput) (*mcp.CallToolResult, any, error) {
	baseDir, err := a.workspaceDir(in.BaseDir)
	if err != nil {
		out := errorOutput("stackkit_config_get", err)
		return errorJSONResult(out), out, nil
	}
	specPath := firstNonEmpty(in.SpecPath, config.GetDefaultSpecPath())
	fullPath, displayPath, err := resolveStackSpecReadPath(baseDir, specPath)
	if err != nil {
		out := errorOutput("stackkit_config_get", err)
		return errorJSONResult(out), out, nil
	}
	raw, err := os.ReadFile(fullPath)
	if err != nil {
		out := errorOutput("stackkit_config_get", err)
		return errorJSONResult(out), out, nil
	}
	document, err := stackspecmigration.Read(raw)
	if err != nil {
		out := errorOutput("stackkit_config_get", fmt.Errorf("classify StackSpec: %w", err))
		return errorJSONResult(out), out, nil
	}
	if document.Version == stackspecmigration.SourceVersionV1 && stackspecadmission.RejectOperationalV1(a.opts.Version) {
		_, report, _ := stackspecmigration.MigrateDocument(document, stackspecmigration.Options{})
		out := map[string]any{
			"base_dir":           baseDir,
			"path":               displayPath,
			"source_version":     document.Version,
			"operational":        false,
			"migration_required": true,
			"validation_scope":   "legacy-read-only",
			"migration_input":    string(raw),
			"migration_report":   report,
		}
		return JSONResult(out), out, nil
	}
	var parsed any
	specHash := ""
	if document.Version == stackspecmigration.SourceVersionV2Alpha1 {
		validation, canonical, validationErr := validateCanonicalStackSpecV2(string(raw), a.opts.Version)
		if validationErr != nil {
			out := errorOutput("stackkit_config_get", validationErr)
			out["validation"] = validation
			return errorJSONResult(out), out, nil
		}
		if err := json.Unmarshal(canonical, &parsed); err != nil {
			out := errorOutput("stackkit_config_get", fmt.Errorf("decode canonical StackSpec v2: %w", err))
			return errorJSONResult(out), out, nil
		}
		raw = canonical
		specHash, _ = validation["spec_hash"].(string)
	} else if err := yaml.Unmarshal(raw, &parsed); err != nil {
		out := errorOutput("stackkit_config_get", fmt.Errorf("decode StackSpec v1: %w", err))
		return errorJSONResult(out), out, nil
	}
	out := map[string]any{
		"base_dir":       baseDir,
		"path":           displayPath,
		"yaml":           string(raw),
		"parsed":         parsed,
		"source_version": document.Version,
	}
	if specHash != "" {
		out["spec_hash"] = specHash
		out["validation_scope"] = "spec-only"
	}
	return JSONResult(out), out, nil
}

func (a *App) configSet(ctx context.Context, req *mcp.CallToolRequest, in configSetInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.YAML) == "" {
		out := errorOutput("stackkit_config_set", fmt.Errorf("yaml is required"))
		return errorJSONResult(out), out, nil
	}
	if !a.opts.AllowWrite {
		out := writeDisabledOutput("stackkit_config_set")
		return errorJSONResult(out), out, nil
	}
	baseDir, err := a.workspaceDir(in.BaseDir)
	if err != nil {
		out := errorOutput("stackkit_config_set", err)
		return errorJSONResult(out), out, nil
	}
	validation, canonical, err := validateCanonicalStackSpecV2(in.YAML, a.opts.Version)
	if err != nil {
		out := errorOutput("stackkit_config_set", err)
		out["validation"] = validation
		return errorJSONResult(out), out, nil
	}
	specPath := firstNonEmpty(in.SpecPath, config.GetDefaultSpecPath())
	fullPath, err := resolveWorkspacePath(baseDir, specPath)
	if err != nil {
		out := errorOutput("stackkit_config_set", err)
		return errorJSONResult(out), out, nil
	}
	result, err := stackspecintent.Persist(stackspecintent.Request{
		WorkspaceRoot: baseDir, SpecPath: fullPath, Candidate: canonical,
		ExpectedSpecHash: in.ExpectedSpecHash, BuildVersion: a.opts.Version,
	})
	if err != nil {
		out := errorOutput("stackkit_config_set", err)
		out["validation"] = validation
		var storeErr *stackspecintent.Error
		if errors.As(err, &storeErr) {
			out["reason"] = string(storeErr.Code)
			if storeErr.CurrentSpecHash != "" {
				out["current_spec_hash"] = storeErr.CurrentSpecHash
			}
			if storeErr.CandidateSpecHash != "" {
				out["spec_hash"] = storeErr.CandidateSpecHash
			}
		}
		return errorJSONResult(out), out, nil
	}
	out := map[string]any{
		"tool":       "stackkit_config_set",
		"success":    true,
		"base_dir":   baseDir,
		"path":       specPath,
		"validation": validation,
		"spec_hash":  result.SpecHash,
		"outcome":    string(result.Outcome),
	}
	if result.PreviousSpecHash != "" {
		out["previous_spec_hash"] = result.PreviousSpecHash
	}
	return JSONResult(out), out, nil
}

func validateCanonicalStackSpecV2(raw, buildVersion string) (map[string]any, []byte, error) {
	document, err := stackspecmigration.Read([]byte(raw))
	if err != nil {
		return map[string]any{"valid": false}, nil, fmt.Errorf("classify StackSpec: %w", err)
	}
	if document.Version != stackspecmigration.SourceVersionV2Alpha1 || document.V2 == nil {
		return map[string]any{
			"valid":          false,
			"source_version": document.Version,
		}, nil, fmt.Errorf("stackkit_config_set accepts canonical StackSpec v2 only; migrate v1 explicitly first")
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(buildVersion))
	if err != nil {
		return map[string]any{"valid": false}, nil, err
	}
	result, err := service.ValidateStackSpec(document.Raw)
	if err != nil {
		return map[string]any{"valid": false, "stackkit": document.V2.KitProfile}, nil, err
	}
	return map[string]any{
		"valid":            true,
		"source_version":   document.Version,
		"stackkit":         result.KitProfile,
		"spec_hash":        result.SpecHash,
		"validation_scope": "spec-only",
	}, append([]byte(nil), result.CanonicalStackSpec...), nil
}

func (a *App) stackkitInit(ctx context.Context, req *mcp.CallToolRequest, in initInput) (*mcp.CallToolResult, any, error) {
	kit := firstNonEmpty(in.StackKit, "basement-kit")
	args := []string{"init", kit, "--non-interactive"}
	args = appendOptionalFlag(args, "--admin-email", in.AdminEmail)
	args = appendOptionalFlag(args, "--mode", in.Mode)
	args = appendOptionalFlag(args, "--context", in.Context)
	args = appendOptionalFlag(args, "--domain", in.Domain)
	args = appendOptionalFlag(args, "--compute-tier", in.ComputeTier)
	args = appendOptionalFlag(args, "--service-profile", in.ServiceProfile)
	args = appendOptionalFlag(args, "--local-name", in.LocalName)
	if in.Force {
		args = append(args, "--force")
	}
	if in.LocalDNS {
		args = append(args, "--local-dns")
	}
	return a.runStackkitTool(ctx, "stackkit_init", in.stackkitCommandInput, appendSpecFlag(args, in.SpecPath), nil)
}

func (a *App) stackkitInitV2(ctx context.Context, req *mcp.CallToolRequest, in architectureV2InitInput) (*mcp.CallToolResult, any, error) {
	kit := strings.TrimSpace(in.KitProfile)
	if kit == "" {
		out := errorOutput("stackkit_init", fmt.Errorf("kit_profile is required for native Architecture v2 authoring"))
		return errorJSONResult(out), out, nil
	}
	args := []string{"init", kit, "--non-interactive"}
	args = appendOptionalFlag(args, "--name", in.Name)
	args = appendOptionalFlag(args, "--domain", in.DomainBase)
	return a.runStackkitTool(ctx, "stackkit_init", in.commandInput(), args, nil)
}

func (a *App) stackkitPrepareV2(ctx context.Context, req *mcp.CallToolRequest, in architectureV2PrepareInput) (*mcp.CallToolResult, any, error) {
	args := []string{"prepare"}
	if in.DryRun {
		args = append(args, "--dry-run")
	}
	if in.SkipDocker {
		args = append(args, "--skip-docker")
	}
	if in.SkipTofu {
		args = append(args, "--skip-tofu")
	}
	return a.runStackkitTool(ctx, "stackkit_prepare", in.commandInput(), args, nil)
}

func (a *App) stackkitGenerateV2(ctx context.Context, req *mcp.CallToolRequest, in architectureV2CommandInput) (*mcp.CallToolResult, any, error) {
	return a.runStackkitTool(ctx, "stackkit_generate", in.commandInput(), []string{"generate"}, nil)
}

func (a *App) stackkitResolveV2(ctx context.Context, req *mcp.CallToolRequest, in architectureV2ResolveInput) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(in.InventoryPath) == "" {
		out := errorOutput("stackkit_resolve", fmt.Errorf("inventory_path is required; native resolution never invents observed host facts"))
		return errorJSONResult(out), out, nil
	}
	args := []string{"resolve"}
	args = appendOptionalFlag(args, "--inventory", in.InventoryPath)
	args = append(args, "--output", "deploy/.stackkit/resolved-plan.json")
	return a.runStackkitTool(ctx, "stackkit_resolve", in.commandInput(), args, nil)
}

func (a *App) stackkitPlanV2(ctx context.Context, req *mcp.CallToolRequest, in architectureV2CommandInput) (*mcp.CallToolResult, any, error) {
	return a.runStackkitReadOnlyTool(ctx, "stackkit_plan", in.commandInput(), []string{"plan", "--json"}, nil)
}

func (a *App) stackkitApplyV2(ctx context.Context, req *mcp.CallToolRequest, in architectureV2ApplyInput) (*mcp.CallToolResult, any, error) {
	args := []string{"apply"}
	if in.AutoApprove {
		args = append(args, "--auto-approve")
	}
	return a.runStackkitTool(ctx, "stackkit_apply", in.commandInput(), args, nil)
}

func (a *App) stackkitVerifyV2(ctx context.Context, req *mcp.CallToolRequest, in architectureV2CommandInput) (*mcp.CallToolResult, any, error) {
	return a.runStackkitTool(ctx, "stackkit_verify_plan", in.commandInput(), []string{"verify", "--json"}, nil)
}

func (a *App) stackkitPrepare(ctx context.Context, req *mcp.CallToolRequest, in prepareInput) (*mcp.CallToolResult, any, error) {
	args := []string{"prepare"}
	args = appendOptionalFlag(args, "--spec", in.SpecPath)
	args = appendOptionalFlag(args, "--host", in.Host)
	args = appendOptionalFlag(args, "--user", in.User)
	args = appendOptionalFlag(args, "--key", in.KeyPath)
	if in.DryRun {
		args = append(args, "--dry-run")
	}
	if in.SkipDocker {
		args = append(args, "--skip-docker")
	}
	if in.SkipTofu {
		args = append(args, "--skip-tofu")
	}
	if in.Force {
		args = append(args, "--force")
	}
	return a.runStackkitTool(ctx, "stackkit_prepare", in.stackkitCommandInput, args, nil)
}

func (a *App) stackkitGenerate(ctx context.Context, req *mcp.CallToolRequest, in generateInput) (*mcp.CallToolResult, any, error) {
	args := []string{"generate"}
	args = appendOptionalFlag(args, "--spec", in.SpecPath)
	args = appendOptionalFlag(args, "--output", in.OutputDir)
	if boolDefault(in.Force, true) {
		args = append(args, "--force")
	}
	if in.Fragments {
		args = append(args, "--fragments")
	}
	return a.runStackkitTool(ctx, "stackkit_generate", in.stackkitCommandInput, args, nil)
}

func (a *App) stackkitPlan(ctx context.Context, req *mcp.CallToolRequest, in planInput) (*mcp.CallToolResult, any, error) {
	args := []string{"plan"}
	args = appendOptionalFlag(args, "--spec", in.SpecPath)
	args = appendOptionalFlag(args, "--out", in.OutFile)
	if in.Destroy {
		args = append(args, "--destroy")
	}
	return a.runStackkitTool(ctx, "stackkit_plan", in.stackkitCommandInput, args, nil)
}

func (a *App) stackkitApply(ctx context.Context, req *mcp.CallToolRequest, in applyInput) (*mcp.CallToolResult, any, error) {
	args := []string{"apply"}
	args = appendOptionalFlag(args, "--spec", in.SpecPath)
	if boolDefault(in.AutoApprove, true) {
		args = append(args, "--auto-approve")
	}
	if boolDefault(in.Verify, true) {
		args = append(args, "--verify")
	}
	if boolDefault(in.VerifyHTTP, true) {
		args = append(args, "--verify-http")
	}
	if in.VerifyStrict {
		args = append(args, "--verify-strict")
	}
	skipPlatformApps := boolDefault(in.SkipPlatformApps, true)
	if skipPlatformApps {
		args = append(args, "--skip-platform-apps")
	}
	args = appendOptionalArg(args, in.PlanFile)
	env := map[string]string{}
	if skipPlatformApps {
		env["STACKKIT_SKIP_PLATFORM_APPS"] = "true"
	}
	return a.runStackkitTool(ctx, "stackkit_apply", in.stackkitCommandInput, args, env)
}

func (a *App) stackkitUpdate(ctx context.Context, req *mcp.CallToolRequest, in updateInput) (*mcp.CallToolResult, any, error) {
	args := []string{"kit", "upgrade"}
	args = appendOptionalFlag(args, "--spec", in.SpecPath)
	args = appendOptionalFlag(args, "--to", in.To)
	args = appendOptionalFlag(args, "--kit-channel", in.KitChannel)
	if in.DryRun {
		args = append(args, "--dry-run")
	}
	if boolDefault(in.AutoApprove, true) {
		args = append(args, "--auto-approve")
	}
	if len(in.Volumes) > 0 {
		args = append(args, "--volumes", strings.Join(in.Volumes, ","))
	}
	return a.runStackkitTool(ctx, "stackkit_update", in.stackkitCommandInput, args, map[string]string{"STACKKIT_SKIP_PLATFORM_APPS": "true"})
}

func (a *App) stackkitRollout(ctx context.Context, req *mcp.CallToolRequest, in rolloutInput) (*mcp.CallToolResult, any, error) {
	if !a.opts.AllowWrite {
		out := writeDisabledOutput("stackkit_rollout")
		return errorJSONResult(out), out, nil
	}
	rolloutCtx, cancel := context.WithTimeout(ctx, commandTimeout(in.stackkitCommandInput))
	defer cancel()

	baseInput := in.stackkitCommandInput
	steps := make([]map[string]any, 0, 7)
	runStep := func(name string, args []string, env map[string]string) bool {
		out := a.runStackkitCommand(rolloutCtx, name, baseInput, args, env, true)
		steps = append(steps, out)
		success, _ := out["success"].(bool)
		return success
	}

	if !in.SkipInit {
		initArgs := []string{"init", firstNonEmpty(in.StackKit, "basement-kit"), "--non-interactive"}
		initArgs = appendOptionalFlag(initArgs, "--admin-email", in.AdminEmail)
		initArgs = appendOptionalFlag(initArgs, "--mode", in.Mode)
		initArgs = appendOptionalFlag(initArgs, "--context", in.Context)
		initArgs = appendOptionalFlag(initArgs, "--domain", in.Domain)
		initArgs = appendOptionalFlag(initArgs, "--compute-tier", in.ComputeTier)
		initArgs = appendOptionalFlag(initArgs, "--service-profile", in.ServiceProfile)
		initArgs = appendOptionalFlag(initArgs, "--local-name", in.LocalName)
		if in.LocalDNS {
			initArgs = append(initArgs, "--local-dns")
		}
		if !runStep("stackkit_init", appendSpecFlag(initArgs, in.SpecPath), nil) {
			return rolloutResult(steps, false), map[string]any{"success": false, "steps": steps}, nil
		}
	}
	prepareArgs := []string{"prepare"}
	prepareArgs = appendOptionalFlag(prepareArgs, "--spec", in.SpecPath)
	prepareArgs = appendOptionalFlag(prepareArgs, "--host", in.Host)
	prepareArgs = appendOptionalFlag(prepareArgs, "--user", in.User)
	prepareArgs = appendOptionalFlag(prepareArgs, "--key", in.KeyPath)
	if in.PrepareDryRun {
		prepareArgs = append(prepareArgs, "--dry-run")
	}
	if in.PrepareSkipDocker {
		prepareArgs = append(prepareArgs, "--skip-docker")
	}
	if in.PrepareSkipTofu {
		prepareArgs = append(prepareArgs, "--skip-tofu")
	}
	if !runStep("stackkit_prepare", prepareArgs, nil) {
		return rolloutResult(steps, false), map[string]any{"success": false, "steps": steps}, nil
	}
	if !runStep("stackkit_validate", appendSpecFlag([]string{"validate"}, in.SpecPath), nil) {
		return rolloutResult(steps, false), map[string]any{"success": false, "steps": steps}, nil
	}
	if !runStep("stackkit_generate", appendSpecFlag([]string{"generate", "--force"}, in.SpecPath), nil) {
		return rolloutResult(steps, false), map[string]any{"success": false, "steps": steps}, nil
	}
	if !runStep("stackkit_plan", appendSpecFlag([]string{"plan"}, in.SpecPath), nil) {
		return rolloutResult(steps, false), map[string]any{"success": false, "steps": steps}, nil
	}
	applyArgs := []string{"apply"}
	applyArgs = appendOptionalFlag(applyArgs, "--spec", in.SpecPath)
	if boolDefault(in.ApplyAutoApprove, true) {
		applyArgs = append(applyArgs, "--auto-approve")
	}
	if boolDefault(in.VerifyHTTP, true) {
		applyArgs = append(applyArgs, "--verify", "--verify-http")
	}
	skipPlatformApps := boolDefault(in.SkipPlatformApps, true)
	if skipPlatformApps {
		applyArgs = append(applyArgs, "--skip-platform-apps")
	}
	env := map[string]string{}
	if skipPlatformApps {
		env["STACKKIT_SKIP_PLATFORM_APPS"] = "true"
	}
	if !runStep("stackkit_apply", applyArgs, env) {
		return rolloutResult(steps, false), map[string]any{"success": false, "steps": steps}, nil
	}
	verifyArgs := []string{"verify", "--json"}
	verifyArgs = appendOptionalFlag(verifyArgs, "--spec", in.SpecPath)
	if boolDefault(in.VerifyHTTP, true) {
		verifyArgs = append(verifyArgs, "--http")
	}
	success := runStep("stackkit_verify", verifyArgs, nil)
	return rolloutResult(steps, success), map[string]any{"success": success, "steps": steps}, nil
}

func (a *App) serverRequest(ctx context.Context, method, path string, payload any) (*mcp.CallToolResult, any, error) {
	base := strings.TrimRight(firstNonEmpty(a.opts.ServerURL, "http://localhost:8082"), "/")
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
	if strings.TrimSpace(a.opts.APIKey) != "" {
		httpReq.Header.Set("X-API-Key", strings.TrimSpace(a.opts.APIKey))
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
		return TextResult(fmt.Sprintf("stackkit-server returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))), nil, nil
	}
	var structured any
	if json.Valid(raw) {
		_ = json.Unmarshal(raw, &structured)
		return TextResult(string(raw)), structured, nil
	}
	return TextResult(string(raw)), string(raw), nil
}

func (a *App) runStackkitTool(ctx context.Context, tool string, in stackkitCommandInput, args []string, env map[string]string) (*mcp.CallToolResult, any, error) {
	if !a.opts.AllowWrite {
		out := writeDisabledOutput(tool)
		return errorJSONResult(out), out, nil
	}
	return a.runStackkitBoundTool(ctx, tool, in, args, env, true)
}

func (a *App) runStackkitReadOnlyTool(ctx context.Context, tool string, in stackkitCommandInput, args []string, env map[string]string) (*mcp.CallToolResult, any, error) {
	return a.runStackkitBoundTool(ctx, tool, in, args, env, false)
}

func (a *App) runStackkitBoundTool(ctx context.Context, tool string, in stackkitCommandInput, args []string, env map[string]string, createWorkspace bool) (*mcp.CallToolResult, any, error) {
	runCtx, cancel := context.WithTimeout(ctx, commandTimeout(in))
	defer cancel()
	out := a.runStackkitCommand(runCtx, tool, in, args, env, createWorkspace)
	result := JSONResult(out)
	if success, _ := out["success"].(bool); !success {
		result.IsError = true
	}
	return result, out, nil
}

func (a *App) runStackkitCommand(ctx context.Context, tool string, in stackkitCommandInput, args []string, env map[string]string, createWorkspace bool) map[string]any {
	if err := a.verifyCLIBinding(); err != nil {
		return errorOutput(tool, err)
	}
	baseDir, err := a.workspaceDir(in.BaseDir)
	if err != nil {
		return errorOutput(tool, err)
	}
	if createWorkspace {
		if err := os.MkdirAll(baseDir, 0750); err != nil {
			return errorOutput(tool, err)
		}
	} else {
		info, err := os.Stat(baseDir)
		if err != nil {
			return errorOutput(tool, fmt.Errorf("read-only workspace must already exist: %w", err))
		}
		if !info.IsDir() {
			return errorOutput(tool, fmt.Errorf("read-only workspace is not a directory: %s", baseDir))
		}
	}
	args = appendSpecFlag(args, in.SpecPath)
	start := time.Now()
	cmd := exec.CommandContext(ctx, a.cliBinding.path, args...) // #nosec G204 -- executable is the identity-bound packaged CLI and args are assembled from typed MCP inputs.
	cmd.Dir = baseDir
	cmd.Env = commandEnv(in.ExtraEnv, env)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	duration := time.Since(start)
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if ok := errorAs(err, &exitErr); ok {
			exitCode = exitErr.ExitCode()
		}
	}
	out := map[string]any{
		"tool":        tool,
		"success":     err == nil,
		"exit_code":   exitCode,
		"command":     append([]string{a.cliBinding.path}, args...),
		"cwd":         baseDir,
		"duration_ms": duration.Milliseconds(),
		"stdout":      truncateOutput(stdout.String()),
		"stderr":      truncateOutput(stderr.String()),
		"timed_out":   ctx.Err() == context.DeadlineExceeded,
	}
	if err != nil {
		out["error"] = err.Error()
	}
	return out
}

func (a *App) workspaceDir(raw string) (string, error) {
	base := firstNonEmpty(raw, a.opts.BaseDir, ".")
	abs, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	return filepath.Clean(abs), nil
}

func commandTimeout(in stackkitCommandInput) time.Duration {
	if in.TimeoutSeconds <= 0 {
		return maxMCPActionDuration
	}
	timeout := time.Duration(in.TimeoutSeconds) * time.Second
	if timeout > maxMCPActionDuration {
		return maxMCPActionDuration
	}
	if timeout < time.Second {
		return time.Second
	}
	return timeout
}

func commandEnv(extra, forced map[string]string) []string {
	env := os.Environ()
	merged := map[string]string{
		"STACKKIT_MCP_ALLOW_WRITE": "true",
	}
	for key, value := range extra {
		key = strings.TrimSpace(key)
		if key != "" {
			merged[key] = value
		}
	}
	for key, value := range forced {
		key = strings.TrimSpace(key)
		if key != "" {
			merged[key] = value
		}
	}
	for key, value := range merged {
		env = append(env, key+"="+value)
	}
	return env
}

func appendOptionalFlag(args []string, flagName, value string) []string {
	if strings.TrimSpace(value) == "" {
		return args
	}
	return append(args, flagName, strings.TrimSpace(value))
}

func appendOptionalArg(args []string, value string) []string {
	if strings.TrimSpace(value) == "" {
		return args
	}
	return append(args, strings.TrimSpace(value))
}

func appendSpecFlag(args []string, specPath string) []string {
	specPath = strings.TrimSpace(specPath)
	if specPath == "" || containsArg(args, "--spec") || containsArg(args, "-s") {
		return args
	}
	return append(args, "--spec", specPath)
}

func containsArg(args []string, needle string) bool {
	for _, arg := range args {
		if arg == needle {
			return true
		}
	}
	return false
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func truncateOutput(value string) string {
	const limit = 12000
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n...(truncated)"
}

func writeDisabledOutput(tool string) map[string]any {
	return map[string]any{
		"tool":     tool,
		"success":  false,
		"enabled":  false,
		"executes": false,
		"error":    "write tools are disabled; set STACKKIT_MCP_ALLOW_WRITE=true or the equivalent server flag",
	}
}

func errorOutput(tool string, err error) map[string]any {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	return map[string]any{
		"tool":    tool,
		"success": false,
		"error":   msg,
	}
}

func errorJSONResult(out map[string]any) *mcp.CallToolResult {
	result := JSONResult(out)
	result.IsError = true
	return result
}

func rolloutResult(steps []map[string]any, success bool) *mcp.CallToolResult {
	out := map[string]any{"success": success, "steps": steps}
	result := JSONResult(out)
	if !success {
		result.IsError = true
	}
	return result
}

func resolveWorkspacePath(baseDir, relPath string) (string, error) {
	relPath = firstNonEmpty(relPath, config.GetDefaultSpecPath())
	if strings.Contains(relPath, "\x00") {
		return "", fmt.Errorf("path contains invalid null byte")
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	var target string
	if filepath.IsAbs(relPath) {
		target = filepath.Clean(relPath)
	} else {
		target = filepath.Clean(filepath.Join(baseAbs, relPath))
	}
	if !pathWithin(baseAbs, target) {
		return "", fmt.Errorf("path %s escapes workspace %s", target, baseAbs)
	}
	return target, nil
}

func resolveStackSpecReadPath(baseDir, specPath string) (string, string, error) {
	target, err := resolveWorkspacePath(baseDir, specPath)
	if err != nil {
		return "", "", err
	}
	display := firstNonEmpty(specPath, config.GetDefaultSpecPath())
	if _, err := os.Stat(target); err == nil {
		return target, display, nil
	} else if !os.IsNotExist(err) {
		return "", "", err
	}
	if display == config.GetDefaultSpecPath() {
		alias, aliasErr := resolveWorkspacePath(baseDir, config.GetSpecAliasPath())
		if aliasErr != nil {
			return "", "", aliasErr
		}
		if _, err := os.Stat(alias); err == nil {
			return alias, config.GetSpecAliasPath(), nil
		}
	}
	return "", "", fmt.Errorf("stack spec not found: %s", display)
}

func pathWithin(base, target string) bool {
	base = filepath.Clean(base)
	target = filepath.Clean(target)
	if target == base {
		return true
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func validateStackSpecYAML(baseDir, raw string) (map[string]any, error) {
	var spec models.StackSpec
	if err := yaml.Unmarshal([]byte(raw), &spec); err != nil {
		return map[string]any{"valid": false}, fmt.Errorf("parse stack spec yaml: %w", err)
	}
	if strings.TrimSpace(spec.StackKit) == "" {
		return map[string]any{"valid": false}, fmt.Errorf("stackkit is required")
	}
	result, err := cuepkg.NewValidator(baseDir).ValidateSpec(&spec)
	if err != nil {
		return map[string]any{"valid": false, "stackkit": spec.StackKit}, err
	}
	out := map[string]any{
		"valid":    result.Valid,
		"stackkit": spec.StackKit,
		"mode":     spec.Mode,
		"errors":   result.Errors,
		"warnings": result.Warnings,
	}
	if !result.Valid {
		return out, fmt.Errorf("stack spec validation failed")
	}
	return out, nil
}

func errorAs(err error, target any) bool {
	switch t := target.(type) {
	case **exec.ExitError:
		if exitErr, ok := err.(*exec.ExitError); ok {
			*t = exitErr
			return true
		}
	}
	return false
}
