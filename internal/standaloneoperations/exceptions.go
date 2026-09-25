package standaloneoperations

import (
	"fmt"
	"slices"
	"strings"
)

// Exception records one public stackkit CLI command that is deliberately not
// an MCP tool, with the reason, scope and removal criterion that
// API-FIRST-STANDARD §1.2 requires. StackKits owns every entry. A command is
// excepted only when it is not a product capability of a rolled-out server,
// when MCP already reaches the same capability through a projected tool, or
// when it would force secret values through the MCP transcript.
type Exception struct {
	Command []string `json:"command"`
	Reason  string   `json:"reason"`
	Scope   string   `json:"scope"`
	Removal string   `json:"removal"`
}

const (
	scopeCLIUX      = "CLI user experience"
	scopeRepository = "StackKits source repository tooling"
	scopeV06        = "exact-v0.6 compatibility builds"
	scopeSecret     = "secret-bearing input or output"
	scopeAlias      = "CLI alias of a projected operation"
	scopeAgent      = "agent bootstrap before an MCP connection exists"
	scopeTransport  = "signed execution-channel transport"
)

var exceptions = []Exception{
	// CLI user experience without a capability of its own.
	{Command: []string{"help"}, Reason: "Cobra help text; MCP clients discover tools, schemas and descriptions through tools/list.", Scope: scopeCLIUX, Removal: "Never; help is not a capability."},
	{Command: []string{"completion"}, Reason: "Shell completion script generator for interactive terminals.", Scope: scopeCLIUX, Removal: "Never; completion is not a capability."},
	{Command: []string{"version"}, Reason: "The MCP initialize result already carries the bound build version, and the connector refuses a CLI of another build.", Scope: scopeCLIUX, Removal: "Never; build identity is part of the MCP handshake."},
	{Command: []string{"operations"}, Reason: "Prints this operation catalog; MCP tools/list and /openmcp.json are the connector-native listing of the same contracts.", Scope: scopeCLIUX, Removal: "When MCP discovery stops deriving from this catalog."},
	{Command: []string{"backup"}, Reason: "Command group whose RunE only prints help; every backup subcommand is classified individually.", Scope: scopeCLIUX, Removal: "When the group gains behavior of its own."},
	{Command: []string{"backup", "schedule"}, Reason: "Command group whose RunE only prints help; enable, disable and status are projected.", Scope: scopeCLIUX, Removal: "When the group gains behavior of its own."},
	{Command: []string{"backup", "init"}, Reason: "Prints static first-run instructions without reading or changing state.", Scope: scopeCLIUX, Removal: "When it performs a real setup step."},
	{Command: []string{"use-cases", "pick"}, Reason: "Interactive terminal multi-select; stackkit_init takes the same selection as explicit use_cases input.", Scope: scopeCLIUX, Removal: "When it gains a non-interactive capability beyond init --use-case."},

	// Agent bootstrap and the connector itself.
	{Command: []string{"agent", "mcp-config"}, Reason: "Generates the MCP client configuration that creates this connection.", Scope: scopeAgent, Removal: "Never; it configures MCP rather than running through it."},
	{Command: []string{"agent", "install-plan"}, Reason: "Bootstrap plan for agents without the connector; MCP serves stackkit_install_plan natively.", Scope: scopeAgent, Removal: "When the CLI plan and stackkit_install_plan share one generator and the CLI command is removed."},
	{Command: []string{"agent", "prompt"}, Reason: "Prints agent prompts for clients without the connector; MCP serves the same scenarios as native prompts.", Scope: scopeAgent, Removal: "When the CLI and MCP prompts share one source and the CLI command is removed."},
	{Command: []string{"agent", "self-check"}, Reason: "Checks binaries on the caller's PATH before a connection exists; MCP serves stackkit_self_check_plan.", Scope: scopeAgent, Removal: "When self-check reports target-host facts that the connector cannot already observe."},

	// Aliases whose capability is projected under another tool.
	{Command: []string{"logs", "get"}, Reason: "Alias of `stackkit logs <run-id>`, projected as stackkit_logs_read with run_id.", Scope: scopeAlias, Removal: "When the alias is removed or its behavior diverges from stackkit logs."},
	{Command: []string{"logs", "latest"}, Reason: "Alias of `stackkit logs`, projected as stackkit_logs_read without run_id.", Scope: scopeAlias, Removal: "When the alias is removed or its behavior diverges from stackkit logs."},
	{Command: []string{"kit", "upgrade"}, Reason: "Deprecated alias of `stackkit upgrade`, projected as stackkit_upgrade.", Scope: scopeAlias, Removal: "When the deprecated alias is deleted."},
	{Command: []string{"kit", "verify"}, Reason: "Deprecated alias of `stackkit verify --offline`, projected as stackkit_verify with offline=true.", Scope: scopeAlias, Removal: "When the deprecated alias is deleted."},

	// Exact-v0.6 compatibility verbs, rejected on the native line.
	{Command: []string{"addon", "add"}, Reason: "Mutates a StackSpec v1; native builds reject it, and the MCP connector registers no v1 authoring.", Scope: scopeV06, Removal: "When the v0.6 compatibility verb is deleted, or a native add-on selection operation replaces it."},
	{Command: []string{"addon", "remove"}, Reason: "Mutates a StackSpec v1; native builds reject it, and the MCP connector registers no v1 authoring.", Scope: scopeV06, Removal: "When the v0.6 compatibility verb is deleted, or a native add-on selection operation replaces it."},
	{Command: []string{"app", "add"}, Reason: "v0.6 PaaS handoff that mutates a StackSpec v1 and takes arbitrary --env values; native builds reject it.", Scope: scopeV06, Removal: "When the v0.6 compatibility verb is deleted."},
	{Command: []string{"backup", "list"}, Reason: "Legacy Kopia adapter reachable only from exact-v0.6 builds; native snapshot state is served by stackkit_backup_status.", Scope: scopeV06, Removal: "When a native snapshot listing replaces the legacy adapter; project that operation then."},
	{Command: []string{"backup", "verify"}, Reason: "Legacy validate-provider adapter reachable only from exact-v0.6 builds.", Scope: scopeV06, Removal: "When a native repository verification replaces the legacy adapter; project that operation then."},
	{Command: []string{"backup", "migrate-from-restic"}, Reason: "One-shot v0.6 Restic importer reachable only from exact-v0.6 builds.", Scope: scopeV06, Removal: "When the v0.6 compatibility verb is deleted."},

	// Commands whose input or output is a secret value.
	{Command: []string{"secrets", "reveal"}, Reason: "Prints one workload secret value; MCP must never return secret values.", Scope: scopeSecret, Removal: "Never while the command's purpose is disclosing secret material."},
	{Command: []string{"backup", "target", "import"}, Reason: "Reads S3 access keys and the repository passphrase from stdin; MCP must never accept secret values.", Scope: scopeSecret, Removal: "When target import accepts a local custody reference instead of secret values."},
	{Command: []string{"cluster", "join-token"}, Reason: "Prints a bearer join token; MCP must never return secret values.", Scope: scopeSecret, Removal: "When the token is delivered only to a local file or custody reference and the command output carries no token."},
	{Command: []string{"user", "add"}, Reason: "Always prints the invited user's one-time passkey setup URL, a bearer enrollment link.", Scope: scopeSecret, Removal: "When invitation can deliver the setup URL out of band and return only the invitation status."},
	{Command: []string{"user", "owner", "activate"}, Reason: "Returns or reissues the owner's one-time passkey activation URL, a bearer enrollment link; stackkit_user_owner_status covers the non-secret state.", Scope: scopeSecret, Removal: "When activation can be reissued without returning the URL through the caller."},

	// Execution channel for signed bundles.
	{Command: []string{"runtime", "execute"}, Reason: "Receiver for a signed execution-channel bundle on stdin from an external executor; exposing it would add a second dispatch path. The lifecycle operations it runs are projected individually.", Scope: scopeTransport, Removal: "When the execution channel is replaced by the projected operations."},

	// Repository build and documentation tooling.
	{Command: []string{"compat", "emit-os-matrix"}, Reason: "Renders the public OS compatibility projection into the StackKits website and docs sources.", Scope: scopeRepository, Removal: "Never; it is release tooling, not a server capability."},
	{Command: []string{"docs", "emit-cli-reference"}, Reason: "Regenerates the committed CLI reference in the StackKits source tree.", Scope: scopeRepository, Removal: "Never; it is documentation tooling, not a server capability."},
	{Command: []string{"docs", "emit-advanced-operations"}, Reason: "Regenerates or checks the committed Advanced operations catalog in the StackKits source tree and release archive.", Scope: scopeRepository, Removal: "Never; it is documentation tooling, not a server capability."},
	{Command: []string{"docs", "emit-release-manifests"}, Reason: "Emits release-bound manifests during StackKits release engineering.", Scope: scopeRepository, Removal: "Never; it is release tooling, not a server capability."},
	{Command: []string{"docs", "emit-use-case-overview"}, Reason: "Renders the internal use-case development overview.", Scope: scopeRepository, Removal: "Never; it is documentation tooling, not a server capability."},
	{Command: []string{"registry", "artifacts"}, Reason: "Prints generated-artifact pathspecs from the repository ownership manifest.", Scope: scopeRepository, Removal: "Never; it is repository tooling, not a server capability."},
	{Command: []string{"registry", "bake-from-cue"}, Reason: "Rebuilds the embedded registry snapshot from a StackKits source checkout.", Scope: scopeRepository, Removal: "Never; it is build tooling, not a server capability."},
	{Command: []string{"registry", "emit-cue"}, Reason: "Renders generated foundation CUE from the registry snapshot into the source tree.", Scope: scopeRepository, Removal: "Never; it is build tooling, not a server capability."},
}

// Exceptions returns fresh exception records in stable order.
func Exceptions() []Exception {
	out := make([]Exception, len(exceptions))
	for index, exception := range exceptions {
		exception.Command = slices.Clone(exception.Command)
		out[index] = exception
	}
	return out
}

// LookupException resolves the exception for one exact CLI command path.
func LookupException(path []string) (Exception, bool) {
	for _, exception := range exceptions {
		if slices.Equal(exception.Command, path) {
			exception.Command = slices.Clone(exception.Command)
			return exception, true
		}
	}
	return Exception{}, false
}

func validateExceptions(projected map[string]ID) error {
	seen := map[string]bool{}
	for index, exception := range exceptions {
		key := strings.Join(exception.Command, " ")
		if len(exception.Command) == 0 ||
			strings.TrimSpace(exception.Reason) == "" ||
			strings.TrimSpace(exception.Scope) == "" ||
			strings.TrimSpace(exception.Removal) == "" {
			return fmt.Errorf("MCP exception at index %d needs a command, reason, scope and removal criterion", index)
		}
		if seen[key] {
			return fmt.Errorf("MCP exception %q is listed twice", key)
		}
		if id, ok := projected[key]; ok {
			return fmt.Errorf("CLI command %q is both projected as %q and excepted", key, id)
		}
		seen[key] = true
	}
	return nil
}
