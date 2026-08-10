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
	Init     ID = "stackkit.init"
	Validate ID = "stackkit.validate"
	Resolve  ID = "stackkit.resolve"
	Generate ID = "stackkit.generate"
	Plan     ID = "stackkit.plan"
	Apply    ID = "stackkit.apply"
	Verify   ID = "stackkit.verify"
	Status   ID = "stackkit.status"
	Logs     ID = "stackkit.logs"
	Backup   ID = "stackkit.backup"
	Restore  ID = "stackkit.restore"
	Upgrade  ID = "stackkit.upgrade"
	Drift    ID = "stackkit.drift"
	Remove   ID = "stackkit.remove"
)

// Contract describes one operation without owning its lifecycle
// implementation. Command is the exact public CLI path that executes it.
type Contract struct {
	ID            ID       `json:"id"`
	ToolName      string   `json:"toolName"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Command       []string `json:"command"`
	Mutation      bool     `json:"mutation"`
	Destructive   bool     `json:"destructive"`
	Idempotent    bool     `json:"idempotent"`
	OwnerApproval bool     `json:"ownerApproval"`
}

var catalog = []Contract{
	{ID: Init, ToolName: "stackkit_init", Title: "Init", Description: "Materialize a native StackSpec from the embedded CUE authoring contract.", Command: []string{"init"}, Mutation: true, Idempotent: false, OwnerApproval: true},
	{ID: Validate, ToolName: "stackkit_validate", Title: "Validate", Description: "Validate desired StackSpec intent without mutating lifecycle state.", Command: []string{"validate"}, Idempotent: true},
	{ID: Resolve, ToolName: "stackkit_resolve", Title: "Resolve", Description: "Resolve StackSpec and observed Inventory into the canonical ResolvedPlan.", Command: []string{"resolve"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: Generate, ToolName: "stackkit_generate", Title: "Generate", Description: "Generate rollout artifacts from the exact persisted ResolvedPlan.", Command: []string{"generate"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: Plan, ToolName: "stackkit_plan", Title: "Plan", Description: "Inspect the persisted ResolvedPlan and generated artifact closure.", Command: []string{"plan", "--json"}, Idempotent: true},
	{ID: Apply, ToolName: "stackkit_apply", Title: "Apply", Description: "Apply the exact persisted plan after explicit local Owner approval.", Command: []string{"apply", "--json"}, Mutation: true, Destructive: true, Idempotent: false, OwnerApproval: true},
	{ID: Verify, ToolName: "stackkit_verify", Title: "Verify", Description: "Verify desired, planned, applied, and observed local state.", Command: []string{"verify", "--json"}, Idempotent: true},
	{ID: Status, ToolName: "stackkit_status", Title: "Status", Description: "Read the current standalone StackKits lifecycle status.", Command: []string{"status", "--json"}, Idempotent: true},
	{ID: Logs, ToolName: "stackkit_logs", Title: "Logs", Description: "List local structured rollout logs.", Command: []string{"logs", "list", "--json"}, Idempotent: true},
	{ID: Backup, ToolName: "stackkit_backup", Title: "Backup", Description: "Create a local owner-custodied backup snapshot.", Command: []string{"backup", "run", "--json"}, Mutation: true, Idempotent: true, OwnerApproval: true},
	{ID: Restore, ToolName: "stackkit_restore", Title: "Restore", Description: "Verify and restore one signed snapshot into isolated staging.", Command: []string{"backup", "restore"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true},
	{ID: Upgrade, ToolName: "stackkit_upgrade", Title: "Upgrade", Description: "Resolve, verify, checkpoint, install, apply, and verify a StackKits release.", Command: []string{"upgrade", "--json"}, Mutation: true, Destructive: true, Idempotent: false, OwnerApproval: true},
	{ID: Drift, ToolName: "stackkit_drift", Title: "Drift", Description: "Observe desired-versus-applied standalone state drift.", Command: []string{"drift", "detect", "--json"}, Idempotent: true},
	{ID: Remove, ToolName: "stackkit_remove", Title: "Remove", Description: "Remove one governed workload from its exact applied ResolvedPlan after local Owner approval.", Command: []string{"remove", "--json"}, Mutation: true, Destructive: true, Idempotent: true, OwnerApproval: true},
}

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
		ids = append(ids, string(operation.ID))
		tools = append(tools, operation.ToolName)
	}
	if hasDuplicate(ids) {
		return fmt.Errorf("standalone operation catalog contains duplicate IDs")
	}
	if hasDuplicate(tools) {
		return fmt.Errorf("standalone operation catalog contains duplicate tool names")
	}
	return nil
}

func clone(operation Contract) Contract {
	operation.Command = slices.Clone(operation.Command)
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
