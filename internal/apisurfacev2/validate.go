package apisurface

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Problem is one contract violation. OperationID is empty for document-level
// problems.
type Problem struct {
	OperationID string
	Rule        string
	Message     string
}

func (p Problem) String() string {
	if p.OperationID == "" {
		return fmt.Sprintf("[%s] %s", p.Rule, p.Message)
	}
	return fmt.Sprintf("%s: [%s] %s", p.OperationID, p.Rule, p.Message)
}

// ValidationError reports every contract violation found in one generation.
type ValidationError struct {
	Problems []Problem
}

func newValidationError(problems []Problem) *ValidationError {
	sorted := append([]Problem(nil), problems...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].OperationID != sorted[j].OperationID {
			return sorted[i].OperationID < sorted[j].OperationID
		}
		if sorted[i].Rule != sorted[j].Rule {
			return sorted[i].Rule < sorted[j].Rule
		}
		return sorted[i].Message < sorted[j].Message
	})
	return &ValidationError{Problems: sorted}
}

func (e *ValidationError) Error() string {
	lines := make([]string, 0, len(e.Problems)+1)
	lines = append(lines, fmt.Sprintf("apisurface: %d contract violation(s):", len(e.Problems)))
	for _, p := range e.Problems {
		lines = append(lines, "  "+p.String())
	}
	return strings.Join(lines, "\n")
}

var (
	confirmationPattern = regexp.MustCompile(`^This operation .+\.$`)
	argumentNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	dimensionPattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)+$`)
	availabilities      = map[string]bool{"alpha": true, "beta": true, "preview": true, "ga": true}
	// Flags the CLI runtime adds itself; a flag-bound argument must not use them.
	reservedFlags = map[string]bool{
		"fields": true, "ndjson": true, "dry-run": true, "idempotency-key": true,
		"body": true, "file": true, "yes": true, "help": true,
	}
)

func validate(s *Surface, g *generator) {
	seenIDs := map[string]bool{}
	for i := range s.Operations {
		op := &s.Operations[i]
		if op.OperationID == "" {
			g.add(op.Method+" "+op.Path, "operation-id", "operationId is required")
		} else if seenIDs[op.OperationID] {
			g.add(op.OperationID, "operation-id", "operationId is not unique")
		}
		seenIDs[op.OperationID] = true
		validateArguments(op, g)
		validateSafety(op, g)
		validateActionClass(op, g)
		validateResourceBinding(op, g)
	}
	validateToolNames(s, g)
	validateCommands(s, g)
	if s.MCP == nil {
		for _, op := range s.Operations {
			if op.MCP != nil {
				g.add("", "surface", "operations expose MCP tools but x-kombify-surface.mcp is missing")
				break
			}
		}
	}
}

func validateArguments(op *Operation, g *generator) {
	names := map[string]bool{}
	for _, a := range op.Arguments {
		if names[a.Name] {
			g.add(op.OperationID, "argument-name", "argument %q is defined more than once; rename one with x-kombify-name", a.Name)
		}
		names[a.Name] = true
		if !argumentNamePattern.MatchString(a.Name) {
			g.add(op.OperationID, "argument-name", "argument %q must match %s", a.Name, argumentNamePattern)
		}
	}
	positional := map[string]bool{}
	for _, name := range op.CLI.Args {
		if !names[name] {
			g.add(op.OperationID, "cli-args", "x-kombify-cli.args references unknown argument %q", name)
		}
		positional[name] = true
	}
	for _, a := range op.Arguments {
		wholeBody := a.In == InBody && a.WireName == ""
		if flag := KebabCase(a.Name); reservedFlags[flag] && !positional[a.Name] && !wholeBody {
			g.add(op.OperationID, "reserved-flag", "argument %q collides with the runtime flag --%s; rename it with x-kombify-name or bind it in x-kombify-cli.args", a.Name, flag)
		}
	}
}

func validateSafety(op *Operation, g *generator) {
	if op.Confirmation != "" && !confirmationPattern.MatchString(op.Confirmation) {
		g.add(op.OperationID, "confirmation", "x-kombify-confirmation must match %s", confirmationPattern)
	}
	if op.Method == methodDelete && op.Confirmation == "" {
		g.add(op.OperationID, "confirmation", "DELETE operations require x-kombify-confirmation")
	}
	if op.Availability != "" && !availabilities[op.Availability] {
		g.add(op.OperationID, "availability", "x-kombify-availability %q must be one of alpha, beta, preview, ga", op.Availability)
	}
	if op.MCP == nil {
		return
	}
	if op.MCP.Annotations.ReadOnlyHint && op.Method != methodGet && op.Method != methodHead && !readOnlyPost(op) {
		g.add(op.OperationID, "annotations",
			"readOnlyHint is true on a %s operation; only GET, HEAD and an explicitly annotated side-effect-free POST without confirmation may be read-only", op.Method)
	}
	if op.MCP.Annotations.DestructiveHint && op.Confirmation == "" {
		g.add(op.OperationID, "annotations", "destructiveHint requires x-kombify-confirmation")
	}
}

// validateResourceBinding checks that the bound argument exists on the tool.
func validateResourceBinding(op *Operation, g *generator) {
	if op.MCP == nil || op.MCP.ResourceBinding == nil {
		return
	}
	rb := op.MCP.ResourceBinding
	if !slices.ContainsFunc(op.Arguments, func(a Argument) bool { return a.Name == rb.Argument }) {
		g.add(op.OperationID, "resource-binding", "x-kombify-mcp.resourceBinding.argument %q is not an argument of the tool", rb.Argument)
	}
	if !dimensionPattern.MatchString(rb.Dimension) {
		g.add(op.OperationID, "resource-binding", "x-kombify-mcp.resourceBinding.dimension %q must be a dotted key matching %s", rb.Dimension, dimensionPattern)
	}
}

// validateActionClass checks an explicit action class; with a Gateway catalog
// every tool, derived classes included, must pass the Gateway's class rules.
func validateActionClass(op *Operation, g *generator) {
	if op.MCP == nil || (op.MCP.ActionClass == "" && g.catalog == nil) {
		return
	}
	if msg := actionClassProblem(op.MCP); msg != "" {
		g.add(op.OperationID, "action-class", "%s", msg)
	}
}

func validateToolNames(s *Surface, g *generator) {
	owner := map[string]string{}
	for _, op := range s.Operations {
		if op.MCP == nil {
			continue
		}
		if prev, dup := owner[op.MCP.ToolName]; dup {
			g.add(op.OperationID, "tool-name", "MCP toolName %q is already used by %s", op.MCP.ToolName, prev)
			continue
		}
		owner[op.MCP.ToolName] = op.OperationID
	}
}

// validateCommands checks that command paths and aliases are unique among
// siblings and that no leaf is also a group.
func validateCommands(s *Surface, g *generator) {
	leaves := map[string]string{}
	groups := map[string]string{}
	for _, op := range s.Operations {
		cmd := op.CLI.Command
		if len(cmd) == 0 {
			continue
		}
		parent := strings.Join(cmd[:len(cmd)-1], " ")
		for _, name := range append([]string{cmd[len(cmd)-1]}, op.CLI.Aliases...) {
			key := strings.TrimSpace(parent + " " + name)
			if prev, dup := leaves[key]; dup {
				g.add(op.OperationID, "cli-command", "command or alias %q is already used by %s", key, prev)
				continue
			}
			leaves[key] = op.OperationID
		}
		for i := 1; i < len(cmd); i++ {
			groups[strings.Join(cmd[:i], " ")] = op.OperationID
		}
	}
	for key, id := range leaves {
		if other, isGroup := groups[key]; isGroup {
			g.add(id, "cli-command", "command %q is also a command group used by %s", key, other)
		}
	}
}
