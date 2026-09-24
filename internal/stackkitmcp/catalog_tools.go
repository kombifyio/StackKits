package stackkitmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kombifyio/stackkits/internal/stackspecadmission"
	"github.com/kombifyio/stackkits/internal/standaloneoperations"
)

// typedOperationAdapters keeps the catalog operations that addActions and
// addReadOnlyActions register with hand-written typed adapters in tools.go.
// Every other catalog operation is projected by addCatalogOperationTools, so
// a new catalog contract reaches MCP without a new adapter.
var typedOperationAdapters = map[standaloneoperations.ID]bool{
	standaloneoperations.Init: true, standaloneoperations.Validate: true,
	standaloneoperations.Resolve: true, standaloneoperations.Generate: true,
	standaloneoperations.Plan: true, standaloneoperations.Apply: true,
	standaloneoperations.Verify: true, standaloneoperations.Status: true,
	standaloneoperations.Setup: true, standaloneoperations.Logs: true,
	standaloneoperations.Backup: true, standaloneoperations.BackupStatus: true,
	standaloneoperations.BackupScheduleEnable: true, standaloneoperations.BackupScheduleDisable: true,
	standaloneoperations.BackupScheduleStatus: true, standaloneoperations.Restore: true,
	standaloneoperations.RestoreAbandon: true, standaloneoperations.Upgrade: true,
	standaloneoperations.Drift: true, standaloneoperations.Remove: true,
}

// addCatalogOperationTools registers the catalog-projected tools of one
// access class. It runs only where the typed adapters of that class register,
// so mutations stay behind the same write gate and CLI binding.
func (a *App) addCatalogOperationTools(server *mcp.Server, mutations bool) {
	if a.cliBinding == nil || !stackspecadmission.RejectOperationalV1(a.opts.Version) {
		return
	}
	if mutations && !a.opts.AllowWrite {
		return
	}
	for _, operation := range standaloneoperations.All() {
		if typedOperationAdapters[operation.ID] || operation.Mutation != mutations {
			continue
		}
		operation := operation
		mcp.AddTool(server, catalogOperationTool(operation),
			func(ctx context.Context, _ *mcp.CallToolRequest, in map[string]any) (*mcp.CallToolResult, any, error) {
				return a.runCatalogOperation(ctx, operation, in)
			})
	}
}

func catalogOperationTool(operation standaloneoperations.Contract) *mcp.Tool {
	tool := mcpTool(operation.ToolName, operation.Description, !operation.Mutation, operation.Destructive, operation.Idempotent)
	tool.Title = operation.Title
	tool.Annotations.Title = operation.Title
	tool.Annotations.OpenWorldHint = boolPtr(operation.OpenWorld)
	tool.InputSchema = catalogInputSchema(operation)
	return tool
}

// catalogInputSchema derives the closed input schema from the catalog
// contract: shared workspace inputs, the operation's typed arguments and, for
// mutations, the exact confirmation plus local Owner approval.
func catalogInputSchema(operation standaloneoperations.Contract) *jsonschema.Schema {
	properties := map[string]*jsonschema.Schema{
		"base_dir":        {Type: "string", Description: "workspace directory"},
		"spec_path":       {Type: "string", Description: "stack spec path"},
		"correlation_id":  {Type: "string", Description: "validated caller correlation ID recorded in local evidence"},
		"timeout_seconds": {Type: "integer", Description: "command timeout capped at 870 seconds"},
	}
	order := []string{"base_dir", "spec_path", "correlation_id", "timeout_seconds"}
	var required []string
	for _, argument := range operation.Arguments {
		properties[argument.Name] = argumentSchema(argument)
		order = append(order, argument.Name)
		if argument.Required {
			required = append(required, argument.Name)
		}
	}
	if operation.Mutation {
		properties["operation_confirmation"] = &jsonschema.Schema{Type: "string", Description: "exact registered operation ID " + string(operation.ID)}
		properties["owner_approved"] = &jsonschema.Schema{Type: "boolean", Description: "explicit local Owner approval"}
		order = append(order, "operation_confirmation", "owner_approved")
		required = append(required, "operation_confirmation", "owner_approved")
	}
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		PropertyOrder:        order,
		Required:             required,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

func argumentSchema(argument standaloneoperations.Argument) *jsonschema.Schema {
	schema := &jsonschema.Schema{Description: argument.Description}
	switch argument.Kind {
	case standaloneoperations.ArgumentBoolean:
		schema.Type = "boolean"
	case standaloneoperations.ArgumentInteger:
		schema.Type = "integer"
	case standaloneoperations.ArgumentStringList:
		schema.Type = "array"
		schema.Items = &jsonschema.Schema{Type: "string", MinLength: intPtr(1)}
		if argument.Required {
			schema.MinItems = intPtr(1)
		}
	default:
		schema.Type = "string"
		if argument.Required {
			schema.MinLength = intPtr(1)
		}
		for _, value := range argument.Enum {
			schema.Enum = append(schema.Enum, value)
		}
	}
	return schema
}

func (a *App) runCatalogOperation(ctx context.Context, operation standaloneoperations.Contract, in map[string]any) (*mcp.CallToolResult, any, error) {
	common := stackkitCommandInput{
		BaseDir:        stringInput(in, "base_dir"),
		SpecPath:       stringInput(in, "spec_path"),
		CorrelationID:  stringInput(in, "correlation_id"),
		TimeoutSeconds: int(integerInput(in, "timeout_seconds")),
	}
	args, err := catalogCommandArgs(operation, in)
	if err != nil {
		out := errorOutput(operation.ToolName, err)
		return errorJSONResult(out), out, nil
	}
	if operation.Mutation {
		approved, _ := in["owner_approved"].(bool)
		return a.runApprovedStackkitTool(ctx, operation.ID, stringInput(in, "operation_confirmation"), approved, common, args, nil)
	}
	return a.runRegisteredReadOnlyTool(ctx, operation.ID, common, args, nil)
}

// catalogCommandArgs renders typed inputs as the exact CLI argv: command
// path, positional arguments, fixed flags, then optional flags. Flag values
// use --flag=value so a value can never be parsed as another flag.
func catalogCommandArgs(operation standaloneoperations.Contract, in map[string]any) ([]string, error) {
	args := operation.Path()
	var flags []string
	for _, argument := range operation.Arguments {
		value, present := in[argument.Name]
		if argument.Flag == "" {
			text := strings.TrimSpace(stringInput(in, argument.Name))
			if text == "" {
				if argument.Required {
					return nil, fmt.Errorf("%s is required", argument.Name)
				}
				continue
			}
			if strings.HasPrefix(text, "-") || strings.ContainsRune(text, 0) {
				return nil, fmt.Errorf("%s must not start with '-' or contain NUL", argument.Name)
			}
			args = append(args, text)
			continue
		}
		if !present {
			if argument.Required {
				return nil, fmt.Errorf("%s is required", argument.Name)
			}
			continue
		}
		switch argument.Kind {
		case standaloneoperations.ArgumentBoolean:
			if enabled, _ := value.(bool); enabled {
				flags = append(flags, argument.Flag)
			}
		case standaloneoperations.ArgumentInteger:
			flags = append(flags, argument.Flag+"="+strconv.FormatInt(integerInput(in, argument.Name), 10))
		case standaloneoperations.ArgumentStringList:
			items, _ := value.([]any)
			for _, item := range items {
				text, _ := item.(string)
				if strings.ContainsRune(text, 0) {
					return nil, fmt.Errorf("%s must not contain NUL", argument.Name)
				}
				flags = append(flags, argument.Flag+"="+text)
			}
		default:
			text := strings.TrimSpace(stringInput(in, argument.Name))
			if strings.ContainsRune(text, 0) {
				return nil, fmt.Errorf("%s must not contain NUL", argument.Name)
			}
			if text == "" {
				if argument.Required {
					return nil, fmt.Errorf("%s is required", argument.Name)
				}
				continue
			}
			flags = append(flags, argument.Flag+"="+text)
		}
	}
	args = append(args, operation.FixedFlags()...)
	return append(args, flags...), nil
}

func stringInput(in map[string]any, name string) string {
	value, _ := in[name].(string)
	return value
}

// integerInput reads a schema-validated JSON integer. JSON numbers decode as
// float64, which is exact up to 2^53; larger magnitudes are clamped there.
func integerInput(in map[string]any, name string) int64 {
	const exact = 1 << 53
	switch value := in[name].(type) {
	case float64:
		return int64(math.Max(-exact, math.Min(exact, math.Trunc(value))))
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return 0
	}
}

func intPtr(value int) *int { return &value }
