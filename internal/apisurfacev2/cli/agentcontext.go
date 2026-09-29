package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/apisurfacev2"
	"github.com/spf13/cobra"
)

// agentContext is the deterministic command reference printed by the
// agent-context command, as Markdown or compact JSON.
type agentContext struct {
	Product          string         `json:"product"`
	Binary           string         `json:"binary"`
	UniversalOptions []agentOption  `json:"universalOptions"`
	ExitCodes        []agentExit    `json:"exitCodes"`
	Commands         []agentCommand `json:"commands"`
}

type agentOption struct {
	Flag        string `json:"flag"`
	AppliesTo   string `json:"appliesTo"`
	Description string `json:"description"`
}

type agentExit struct {
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}

type agentCommand struct {
	Command      string     `json:"command"`
	Usage        string     `json:"usage"`
	Summary      string     `json:"summary,omitempty"`
	Arguments    []agentArg `json:"arguments,omitempty"`
	Mutating     bool       `json:"mutating"`
	Confirmation string     `json:"confirmation,omitempty"`
	Availability string     `json:"availability,omitempty"`
	Deprecated   bool       `json:"deprecated,omitempty"`
	MCPTool      string     `json:"mcpTool,omitempty"`
}

type agentArg struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

var (
	universalOptions = []agentOption{
		{"--fields <paths>", "all", "Comma-separated dotted paths to keep in the output; applied per element for arrays."},
		{"--ndjson", "all", "Print arrays as one compact JSON value per line."},
		{"--dry-run", "mutating", "Print the request as JSON without authorizing or sending it."},
		{"--idempotency-key <key>", "mutating", "Idempotency-Key header; defaults to a fresh UUID."},
		{"--body <json|@path|->", "JSON body", "Base request body; argument flags override its properties."},
		{"--file <path|->", "file body", "Request body file sent with the declared content type."},
		{"--yes", "confirmation", "Confirm without a prompt; required when not interactive."},
	}
	exitCodes = []agentExit{
		{0, "success"},
		{ExitFailure, "other failure"},
		{ExitInvalid, "invalid input (HTTP 400/422)"},
		{ExitUnauthorized, "not authenticated (HTTP 401)"},
		{ExitDenied, "denied, payment required or rate limited (HTTP 402/403/429)"},
		{ExitNotFound, "not found (HTTP 404)"},
		{ExitConflict, "conflict (HTTP 409)"},
		{ExitUnavailable, "server or transport failure (HTTP 5xx)"},
		{ExitConfirmation, "confirmation required or declined"},
	}
)

func agentContextCommand(root *cobra.Command, s *apisurface.Surface, cfg Config) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent-context",
		Short: "Print the command reference for AI agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := buildAgentContext(root, s)
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				return writeJSON(cfg.Out, ctx, false)
			}
			writeAgentMarkdown(cfg.Out, ctx)
			return nil
		},
	}
	cmd.Flags().Bool("json", false, "print compact JSON instead of Markdown")
	return cmd
}

func buildAgentContext(root *cobra.Command, s *apisurface.Surface) agentContext {
	binary := root.Name()
	if s.CLI != nil && s.CLI.Command != "" {
		binary = s.CLI.Command
	}
	ctx := agentContext{Product: s.Product, Binary: binary, UniversalOptions: universalOptions, ExitCodes: exitCodes}
	for i := range s.Operations {
		op := &s.Operations[i]
		if op.CLI.Hidden {
			continue
		}
		ctx.Commands = append(ctx.Commands, describe(binary, op))
	}
	sort.Slice(ctx.Commands, func(i, j int) bool { return ctx.Commands[i].Command < ctx.Commands[j].Command })
	return ctx
}

func describe(binary string, op *apisurface.Operation) agentCommand {
	c := agentCommand{
		Command:      strings.Join(op.CLI.Command, " "),
		Summary:      op.Summary,
		Mutating:     op.Mutating,
		Confirmation: op.Confirmation,
		Availability: op.Availability,
		Deprecated:   op.Deprecated,
	}
	if op.MCP != nil {
		c.MCPTool = op.MCP.ToolName
	}
	usage := []string{binary, c.Command}
	positional := map[string]bool{}
	byName := map[string]apisurface.Argument{}
	for _, a := range op.Arguments {
		byName[a.Name] = a
	}
	for _, name := range op.CLI.Args {
		a, ok := byName[name]
		if !ok {
			continue
		}
		positional[name] = true
		placeholder := "<" + apisurface.KebabCase(name) + ">"
		usage = append(usage, placeholder)
		c.Arguments = append(c.Arguments, agentArg{placeholder, schemaType(a.Schema), true, a.Description})
	}
	for _, a := range op.Arguments {
		switch {
		case positional[a.Name]:
		case wholeBody(a):
			c.Arguments = append(c.Arguments, agentArg{"--body", "json", a.Required, a.Description})
		default:
			c.Arguments = append(c.Arguments, agentArg{"--" + apisurface.KebabCase(a.Name), schemaType(a.Schema), a.Required, a.Description})
		}
	}
	if op.Body != nil && op.Body.Mode == apisurface.BodyFile {
		c.Arguments = append(c.Arguments, agentArg{"--file", op.Body.ContentType, op.Body.Required, "request body file"})
	}
	c.Usage = strings.Join(append(usage, "[flags]"), " ")
	return c
}

func writeAgentMarkdown(w io.Writer, ctx agentContext) {
	fmt.Fprintf(w, "# %s CLI agent context\n\n", ctx.Product)
	fmt.Fprintf(w, "Binary: `%s`. Commands print JSON to stdout; failures print the error to stderr and exit non-zero.\n\n", ctx.Binary)
	fmt.Fprintln(w, "## Universal options")
	fmt.Fprintln(w)
	for _, o := range ctx.UniversalOptions {
		fmt.Fprintf(w, "- `%s` (%s): %s\n", o.Flag, o.AppliesTo, o.Description)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Exit codes")
	fmt.Fprintln(w)
	for _, e := range ctx.ExitCodes {
		fmt.Fprintf(w, "- `%d`: %s\n", e.Code, e.Meaning)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Commands")
	for _, c := range ctx.Commands {
		fmt.Fprintf(w, "\n### %s\n\n`%s`\n\n", c.Command, c.Usage)
		if c.Summary != "" {
			fmt.Fprintf(w, "%s\n\n", c.Summary)
		}
		for _, a := range c.Arguments {
			req := "optional"
			if a.Required {
				req = "required"
			}
			line := fmt.Sprintf("- `%s` (%s, %s)", a.Name, a.Type, req)
			if a.Description != "" {
				line += ": " + strings.Join(strings.Fields(a.Description), " ")
			}
			fmt.Fprintln(w, line)
		}
		writeCommandFacts(w, c)
	}
}

func writeCommandFacts(w io.Writer, c agentCommand) {
	if c.Mutating {
		fmt.Fprintln(w, "- Mutating: yes")
	} else {
		fmt.Fprintln(w, "- Mutating: no")
	}
	if c.Confirmation != "" {
		fmt.Fprintf(w, "- Confirmation: %s Pass `--yes` when not interactive.\n", c.Confirmation)
	}
	if c.Availability != "" {
		fmt.Fprintf(w, "- Availability: %s\n", c.Availability)
	}
	if c.Deprecated {
		fmt.Fprintln(w, "- Deprecated: yes")
	}
	if c.MCPTool != "" {
		fmt.Fprintf(w, "- MCP tool: `%s`\n", c.MCPTool)
	}
}
