// Package standaloneoperations owns the public standalone lifecycle operation
// catalog shared by the StackKits CLI, MCP connector, and State Console.
package standaloneoperations

import (
	"fmt"
	"slices"
	"strings"
)

// ID is the stable identity used for operation-specific confirmation.
type ID string

const (
	Init                  ID = "stackkit.init"
	Validate              ID = "stackkit.validate"
	Resolve               ID = "stackkit.resolve"
	Generate              ID = "stackkit.generate"
	Plan                  ID = "stackkit.plan"
	Apply                 ID = "stackkit.apply"
	Verify                ID = "stackkit.verify"
	Status                ID = "stackkit.status"
	Setup                 ID = "stackkit.setup"
	Logs                  ID = "stackkit.logs"
	Backup                ID = "stackkit.backup"
	BackupStatus          ID = "stackkit.backup.status"
	BackupScheduleEnable  ID = "stackkit.backup.schedule.enable"
	BackupScheduleDisable ID = "stackkit.backup.schedule.disable"
	BackupScheduleStatus  ID = "stackkit.backup.schedule.status"
	Restore               ID = "stackkit.restore"
	RestoreAbandon        ID = "stackkit.restore.abandon"
	Upgrade               ID = "stackkit.upgrade"
	Drift                 ID = "stackkit.drift"
	Remove                ID = "stackkit.remove"
)

// Contract describes one operation without owning its lifecycle
// implementation. Command is the exact public CLI path that executes it.
// Destructive means the operation may replace, stop, revoke or delete
// existing state rather than only add to it. OpenWorld means it may reach
// systems beyond the local host.
type Contract struct {
	ID            ID         `json:"id"`
	ToolName      string     `json:"toolName"`
	Title         string     `json:"title"`
	Description   string     `json:"description"`
	Command       []string   `json:"command"`
	Mutation      bool       `json:"mutation"`
	Destructive   bool       `json:"destructive"`
	Idempotent    bool       `json:"idempotent"`
	OpenWorld     bool       `json:"openWorld"`
	OwnerApproval bool       `json:"ownerApproval"`
	Arguments     []Argument `json:"arguments,omitempty"`
}

// ArgumentKind is the JSON type of one typed operation input.
type ArgumentKind string

const (
	ArgumentString     ArgumentKind = "string"
	ArgumentBoolean    ArgumentKind = "boolean"
	ArgumentInteger    ArgumentKind = "integer"
	ArgumentStringList ArgumentKind = "string-list"
)

// Argument maps one typed, secret-free input onto the exact CLI argument.
// An empty Flag makes it a positional argument, appended in declaration
// order. Arguments carry values, file paths or references by name; they never
// carry secret values, so a command that needs one stays an Exception.
type Argument struct {
	Name        string       `json:"name"`
	Kind        ArgumentKind `json:"kind"`
	Flag        string       `json:"flag,omitempty"`
	Required    bool         `json:"required,omitempty"`
	Enum        []string     `json:"enum,omitempty"`
	Description string       `json:"description"`
}

// Adapter-owned inputs shared by every process-backed MCP tool; operation
// arguments must not reuse them.
var reservedArgumentNames = []string{
	"base_dir", "spec_path", "correlation_id", "timeout_seconds",
	"operation_confirmation", "owner_approved",
}

// Argument names that would suggest a secret value crossing the MCP
// transcript. File paths to local custody use neutral names (*_file).
var secretArgumentMarkers = []string{"password", "passphrase", "token", "secret", "api_key", "private_key", "credential"}

var catalog = append([]Contract{
	{ID: Init, ToolName: "stackkit_init", Title: "Init", Description: "Materialize a native StackSpec from the embedded CUE authoring contract.", Command: []string{"init"}, Mutation: true, Idempotent: false, OwnerApproval: true},
	{ID: Validate, ToolName: "stackkit_validate", Title: "Validate", Description: "Validate desired StackSpec intent without mutating lifecycle state.", Command: []string{"validate"}, Idempotent: true},
	{ID: Resolve, ToolName: "stackkit_resolve", Title: "Resolve", Description: "Resolve StackSpec and observed Inventory into the canonical ResolvedPlan.", Command: []string{"resolve"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: Generate, ToolName: "stackkit_generate", Title: "Generate", Description: "Generate rollout artifacts from the exact persisted ResolvedPlan.", Command: []string{"generate"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: Plan, ToolName: "stackkit_plan", Title: "Plan", Description: "Inspect the persisted ResolvedPlan and generated artifact closure.", Command: []string{"plan", "--json"}, Idempotent: true},
	{ID: Apply, ToolName: "stackkit_apply", Title: "Apply", Description: "Apply the exact persisted plan after explicit local Owner approval.", Command: []string{"apply", "--json"}, Mutation: true, Destructive: true, Idempotent: false, OwnerApproval: true},
	{ID: Verify, ToolName: "stackkit_verify", Title: "Verify", Description: "Verify desired, planned, applied, and observed local state.", Command: []string{"verify", "--json"}, Idempotent: true},
	{ID: Status, ToolName: "stackkit_status", Title: "Status", Description: "Read the current standalone StackKits lifecycle status.", Command: []string{"status", "--json"}, Idempotent: true},
	{ID: Setup, ToolName: "stackkit_setup", Title: "Set up application", Description: "Set up and verify a Plan-declared app-local owner account on the existing application.", Command: []string{"setup", "--json"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: Logs, ToolName: "stackkit_logs", Title: "Logs", Description: "List local structured rollout logs.", Command: []string{"logs", "list", "--json"}, Idempotent: true},
	{ID: Backup, ToolName: "stackkit_backup", Title: "Backup", Description: "Create a local owner-custodied backup snapshot.", Command: []string{"backup", "run", "--json"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: BackupStatus, ToolName: "stackkit_backup_status", Title: "Backup status", Description: "Read repository readiness, authenticated receipt history, current snapshot availability and declared recovery objectives.", Command: []string{"backup", "status", "--json"}, Idempotent: true},
	{ID: BackupScheduleEnable, ToolName: "stackkit_backup_schedule_enable", Title: "Enable backup schedule", Description: "Enable the CUE-governed local backup timer after explicit local Owner approval.", Command: []string{"backup", "schedule", "enable", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: BackupScheduleDisable, ToolName: "stackkit_backup_schedule_disable", Title: "Disable backup schedule", Description: "Revoke the local backup timer after explicit local Owner approval.", Command: []string{"backup", "schedule", "disable", "--json", "--owner-approve"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: BackupScheduleStatus, ToolName: "stackkit_backup_schedule_status", Title: "Backup schedule status", Description: "Read the local backup timer and signed schedule authorization without changing either.", Command: []string{"backup", "schedule", "status", "--json"}, Idempotent: true},
	{ID: Restore, ToolName: "stackkit_restore", Title: "Restore", Description: "Verify and restore one signed snapshot into isolated staging.", Command: []string{"backup", "restore"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true},
	{ID: RestoreAbandon, ToolName: "stackkit_restore_abandon", Title: "Abandon restore", Description: "Close one pending or staged restore journal with explicit local Owner approval without touching repository or live volumes.", Command: []string{"backup", "restore", "abandon"}, Mutation: true, Destructive: false, Idempotent: true, OwnerApproval: true},
	{ID: Upgrade, ToolName: "stackkit_upgrade", Title: "Upgrade", Description: "Resolve, verify, checkpoint, install, apply, and verify a StackKits release.", Command: []string{"upgrade", "--json"}, Mutation: true, Destructive: true, Idempotent: false, OpenWorld: true, OwnerApproval: true},
	{ID: Drift, ToolName: "stackkit_drift", Title: "Drift", Description: "Observe desired-versus-applied standalone state drift.", Command: []string{"drift", "detect", "--json"}, Idempotent: true},
	{ID: Remove, ToolName: "stackkit_remove", Title: "Remove", Description: "Remove one governed workload from its exact applied ResolvedPlan after local Owner approval.", Command: []string{"remove", "--json"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true},
}, operatorOperations...)

// All returns fresh contracts in stable catalog order.
func All() []Contract {
	out := make([]Contract, len(catalog))
	for index, operation := range catalog {
		out[index] = clone(operation)
	}
	return out
}

// Lookup resolves one stable operation identity.
func Lookup(id ID) (Contract, bool) {
	for _, operation := range catalog {
		if operation.ID == id {
			return clone(operation), true
		}
	}
	return Contract{}, false
}

// LookupTool resolves one registered MCP tool name.
func LookupTool(name string) (Contract, bool) {
	name = strings.TrimSpace(name)
	for _, operation := range catalog {
		if operation.ToolName == name {
			return clone(operation), true
		}
	}
	return Contract{}, false
}

// ConfirmMutation enforces the exact operation confirmation and local Owner
// approval shared by every mutating MCP adapter.
func ConfirmMutation(id ID, confirmation string, ownerApproved bool) error {
	operation, ok := Lookup(id)
	if !ok {
		return fmt.Errorf("unknown standalone operation %q", id)
	}
	if !operation.Mutation {
		return fmt.Errorf("standalone operation %q is read-only", id)
	}
	if strings.TrimSpace(confirmation) != string(operation.ID) {
		return fmt.Errorf("operation confirmation must exactly equal %q", operation.ID)
	}
	if operation.OwnerApproval && !ownerApproved {
		return fmt.Errorf("local Owner approval is required for %q", operation.ID)
	}
	return nil
}

// ValidateCatalog checks the embedded authority without relying on callers.
func ValidateCatalog() error {
	ids := make([]string, 0, len(catalog))
	tools := make([]string, 0, len(catalog))
	projected := make(map[string]ID, len(catalog))
	for index, operation := range catalog {
		if strings.TrimSpace(string(operation.ID)) == "" ||
			strings.TrimSpace(operation.ToolName) == "" ||
			strings.TrimSpace(operation.Title) == "" ||
			strings.TrimSpace(operation.Description) == "" ||
			len(operation.Command) == 0 {
			return fmt.Errorf("standalone operation at index %d is incomplete", index)
		}
		if operation.Mutation != operation.OwnerApproval {
			return fmt.Errorf("standalone operation %q must require Owner approval exactly when it mutates", operation.ID)
		}
		if !strings.HasPrefix(string(operation.ID), "stackkit.") || !validToolName(operation.ToolName) {
			return fmt.Errorf("standalone operation %q needs a stackkit.* ID and a stackkit_* tool name of at most 64 characters", operation.ID)
		}
		if err := validateArguments(operation); err != nil {
			return err
		}
		path := strings.Join(operation.Path(), " ")
		if existing, ok := projected[path]; ok {
			return fmt.Errorf("standalone operations %q and %q bind the same CLI command %q", existing, operation.ID, path)
		}
		projected[path] = operation.ID
		ids = append(ids, string(operation.ID))
		tools = append(tools, operation.ToolName)
	}
	if hasDuplicate(ids) {
		return fmt.Errorf("standalone operation catalog contains duplicate IDs")
	}
	if hasDuplicate(tools) {
		return fmt.Errorf("standalone operation catalog contains duplicate tool names")
	}
	return validateExceptions(projected)
}

// Path returns the exact CLI command path, without the fixed flags.
func (operation Contract) Path() []string {
	for index, argument := range operation.Command {
		if strings.HasPrefix(argument, "-") {
			return slices.Clone(operation.Command[:index])
		}
	}
	return slices.Clone(operation.Command)
}

// FixedFlags returns the flags every invocation of the operation passes.
func (operation Contract) FixedFlags() []string {
	return slices.Clone(operation.Command[len(operation.Path()):])
}

func validToolName(name string) bool {
	if !strings.HasPrefix(name, "stackkit_") || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func validateArguments(operation Contract) error {
	names := map[string]bool{}
	for _, reserved := range reservedArgumentNames {
		names[reserved] = true
	}
	for _, argument := range operation.Arguments {
		if names[argument.Name] || !validArgumentName(argument.Name) || strings.TrimSpace(argument.Description) == "" {
			return fmt.Errorf("standalone operation %q has an invalid, reserved or duplicate argument %q", operation.ID, argument.Name)
		}
		names[argument.Name] = true
		for _, marker := range secretArgumentMarkers {
			if strings.Contains(argument.Name, marker) {
				return fmt.Errorf("standalone operation %q argument %q looks secret-bearing; secret values never cross MCP", operation.ID, argument.Name)
			}
		}
		switch argument.Kind {
		case ArgumentString, ArgumentBoolean, ArgumentInteger, ArgumentStringList:
		default:
			return fmt.Errorf("standalone operation %q argument %q has unknown kind %q", operation.ID, argument.Name, argument.Kind)
		}
		if argument.Flag == "" && argument.Kind != ArgumentString {
			return fmt.Errorf("standalone operation %q positional argument %q must be a string", operation.ID, argument.Name)
		}
		if argument.Flag != "" && (!strings.HasPrefix(argument.Flag, "--") || strings.Contains(argument.Flag, "=")) {
			return fmt.Errorf("standalone operation %q argument %q needs a long CLI flag", operation.ID, argument.Name)
		}
		if len(argument.Enum) > 0 && argument.Kind != ArgumentString {
			return fmt.Errorf("standalone operation %q argument %q may only enumerate strings", operation.ID, argument.Name)
		}
	}
	return nil
}

func validArgumentName(name string) bool {
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func clone(operation Contract) Contract {
	operation.Command = slices.Clone(operation.Command)
	if operation.Arguments != nil {
		arguments := make([]Argument, len(operation.Arguments))
		for index, argument := range operation.Arguments {
			argument.Enum = slices.Clone(argument.Enum)
			arguments[index] = argument
		}
		operation.Arguments = arguments
	}
	return operation
}

func hasDuplicate(values []string) bool {
	slices.Sort(values)
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}
