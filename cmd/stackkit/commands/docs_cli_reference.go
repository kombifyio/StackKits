package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	cliReferenceSchemaVersion = "stackkits-cli-reference/v1"
	cliReferenceDefaultOutput = "docs/data/cli-reference/latest.json"
)

// cliReferencePrivateOnlyCommands are top-level commands that the private
// default build compiles but the public release binary does not contain:
// scripts/public/export-public.sh deletes addon.go and its AddCommand line.
// Publisher-only commands need no entry because their sources carry the
// publisher build tag and never reach this build.
var cliReferencePrivateOnlyCommands = map[string]bool{
	"addon": true,
}

var (
	docsCLIReferenceOutput string
	docsCLIReferenceCheck  bool
)

var docsEmitCLIReferenceCmd = &cobra.Command{
	Use:   "emit-cli-reference",
	Short: "Emit the machine-readable stackkit CLI reference",
	Long: `Write every visible stackkit command, flag, and example as deterministic JSON.

The stackkit.cc CLI reference renders this file. With --check nothing is
written; the command fails when the file differs from the current command tree.`,
	Example: `  # Regenerate the committed reference after changing a command, flag, or example
  stackkit docs emit-cli-reference

  # Fail when the committed reference is stale
  stackkit docs emit-cli-reference --check`,
	Args:        cobra.NoArgs,
	Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	RunE:        runDocsEmitCLIReference,
}

func init() {
	docsEmitCLIReferenceCmd.Flags().StringVar(&docsCLIReferenceOutput, "out", cliReferenceDefaultOutput, "Reference JSON path, relative to --chdir")
	docsEmitCLIReferenceCmd.Flags().BoolVar(&docsCLIReferenceCheck, "check", false, "Write nothing; fail when the reference file is missing or stale")
	docsCmd.AddCommand(docsEmitCLIReferenceCmd)
}

type cliReference struct {
	SchemaVersion string                `json:"schemaVersion"`
	Program       string                `json:"program"`
	Usage         string                `json:"usage"`
	Short         string                `json:"short"`
	Description   string                `json:"description"`
	GlobalFlags   []cliReferenceFlag    `json:"globalFlags"`
	Groups        []cliReferenceGroup   `json:"groups"`
	Commands      []cliReferenceCommand `json:"commands"`
}

type cliReferenceGroup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type cliReferenceCommand struct {
	Path        string                `json:"path"`
	Name        string                `json:"name"`
	Parent      string                `json:"parent"`
	Aliases     []string              `json:"aliases"`
	Group       string                `json:"group"`
	Usage       string                `json:"usage"`
	Short       string                `json:"short"`
	Description string                `json:"description"`
	Runnable    bool                  `json:"runnable"`
	Examples    []cliReferenceExample `json:"examples"`
	Flags       []cliReferenceFlag    `json:"flags"`
	Subcommands []string              `json:"subcommands"`
	// Deprecated carries cobra's replacement notice. Deprecated commands still
	// run but are absent from --help.
	Deprecated string `json:"deprecated,omitempty"`
	// Legacy marks commands that root admission refuses on every build except
	// an exact v0.6 release.
	Legacy bool `json:"legacy,omitempty"`
}

type cliReferenceExample struct {
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
}

type cliReferenceFlag struct {
	Name        string `json:"name"`
	Shorthand   string `json:"shorthand,omitempty"`
	Type        string `json:"type"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
	// Persistent flags defined here also apply to every subcommand.
	Persistent bool `json:"persistent,omitempty"`
	// InheritedFrom names the ancestor command that defines a persistent flag.
	InheritedFrom string `json:"inheritedFrom,omitempty"`
}

func runDocsEmitCLIReference(cmd *cobra.Command, _ []string) error {
	data, err := renderCLIReference(rootCmd)
	if err != nil {
		return err
	}
	output := filepath.FromSlash(docsCLIReferenceOutput)
	if !filepath.IsAbs(output) {
		output = filepath.Join(getWorkDir(), output)
	}
	if docsCLIReferenceCheck {
		current, readErr := os.ReadFile(output)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if bytes.Equal(bytes.ReplaceAll(current, []byte("\r\n"), []byte("\n")), data) {
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s matches the current command tree\n", docsCLIReferenceOutput)
			return err
		}
		return fmt.Errorf("%s is missing or stale: regenerate it with `mise run docs:cli:generate` (stackkit docs emit-cli-reference) and commit the result", docsCLIReferenceOutput)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(output, data, 0o644); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", docsCLIReferenceOutput)
	return err
}

// renderCLIReference projects the command tree a public release binary exposes.
// The output is byte-stable across hosts: no timestamps, build versions, or
// OS-specific path separators.
func renderCLIReference(root *cobra.Command) ([]byte, error) {
	configureCommandGroups()
	_ = root.LocalFlags() // merge persistent flags so UseLine matches --help
	reference := cliReference{
		SchemaVersion: cliReferenceSchemaVersion,
		Program:       root.Name(),
		Usage:         root.UseLine(),
		Short:         root.Short,
		Description:   strings.TrimSpace(root.Long),
		GlobalFlags:   cliReferenceFlags(root.PersistentFlags(), false, ""),
		Groups:        []cliReferenceGroup{},
		Commands:      []cliReferenceCommand{},
	}
	for _, group := range root.Groups() {
		reference.Groups = append(reference.Groups, cliReferenceGroup{
			ID:    group.ID,
			Title: strings.TrimSuffix(strings.TrimSpace(group.Title), ":"),
		})
	}
	for _, child := range cliReferenceChildren(root) {
		entries, err := cliReferenceCommands(child, child.GroupID)
		if err != nil {
			return nil, err
		}
		reference.Commands = append(reference.Commands, entries...)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(reference); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// cliReferenceChildren returns the documented children in name order. Hidden
// commands drop their whole subtree; cobra's generated help command is
// documented once as the --help flag instead.
func cliReferenceChildren(command *cobra.Command) []*cobra.Command {
	children := []*cobra.Command{}
	for _, child := range command.Commands() {
		if child.Hidden {
			continue
		}
		if !command.HasParent() && (child.Name() == "help" || cliReferencePrivateOnlyCommands[child.Name()]) {
			continue
		}
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
	return children
}

func cliReferenceCommands(command *cobra.Command, group string) ([]cliReferenceCommand, error) {
	description, longExamples := cliReferenceSplitLong(command.Long)
	examples := cliReferenceExamples(command.Example)
	examples = append(examples, cliReferenceExamples(longExamples)...)
	aliases := append([]string{}, command.Aliases...)
	sort.Strings(aliases)
	// Collecting flags merges inherited flag sets; UseLine depends on that merge
	// and then matches the usage line --help prints.
	flags := cliReferenceCommandFlags(command)
	entry := cliReferenceCommand{
		Path:        command.CommandPath(),
		Name:        command.Name(),
		Parent:      command.Parent().CommandPath(),
		Aliases:     aliases,
		Group:       group,
		Usage:       command.UseLine(),
		Short:       strings.TrimSpace(command.Short),
		Description: description,
		Runnable:    command.Runnable(),
		Examples:    examples,
		Flags:       flags,
		Subcommands: []string{},
		Deprecated:  strings.TrimSpace(command.Deprecated),
		Legacy:      legacyV06OnlyOperation(command) != "",
	}
	children := cliReferenceChildren(command)
	for _, child := range children {
		entry.Subcommands = append(entry.Subcommands, child.CommandPath())
	}
	entries := []cliReferenceCommand{entry}
	for _, child := range children {
		descendants, err := cliReferenceCommands(child, group)
		if err != nil {
			return nil, err
		}
		entries = append(entries, descendants...)
	}
	return entries, nil
}

// cliReferenceCommandFlags lists everything a user can pass to one command
// except the root global flags: its own flags first, then persistent flags
// inherited from intermediate ancestors.
func cliReferenceCommandFlags(command *cobra.Command) []cliReferenceFlag {
	flags := cliReferenceFlags(command.LocalNonPersistentFlags(), false, "")
	flags = append(flags, cliReferenceFlags(command.PersistentFlags(), true, "")...)
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	seen := map[string]bool{}
	for _, flag := range flags {
		seen[flag.Name] = true
	}
	for ancestor := command.Parent(); ancestor != nil && ancestor.HasParent(); ancestor = ancestor.Parent() {
		for _, flag := range cliReferenceFlags(ancestor.PersistentFlags(), false, ancestor.CommandPath()) {
			if seen[flag.Name] {
				continue
			}
			seen[flag.Name] = true
			flags = append(flags, flag)
		}
	}
	return flags
}

func cliReferenceFlags(set *pflag.FlagSet, persistent bool, inheritedFrom string) []cliReferenceFlag {
	flags := []cliReferenceFlag{}
	set.VisitAll(func(flag *pflag.Flag) {
		// -h/--help exists on every command, but cobra adds it lazily to the
		// executed command only, so listing it would make the output depend on
		// which command rendered the reference.
		if flag.Hidden || flag.Name == "help" {
			return
		}
		valueName, usage := pflag.UnquoteUsage(flag)
		if valueName == "" {
			valueName = flag.Value.Type()
		}
		required := false
		if values, ok := flag.Annotations[cobra.BashCompOneRequiredFlag]; ok && len(values) > 0 && values[0] == "true" {
			required = true
		}
		flags = append(flags, cliReferenceFlag{
			Name:          flag.Name,
			Shorthand:     flag.Shorthand,
			Type:          valueName,
			Default:       cliReferenceDefault(flag),
			Description:   strings.TrimSpace(usage),
			Required:      required,
			Persistent:    persistent && inheritedFrom == "",
			InheritedFrom: inheritedFrom,
		})
	})
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
	return flags
}

// cliReferenceDefault mirrors --help: zero values are omitted. Defaults built
// with filepath.Join use forward slashes so Windows and Linux render the same.
func cliReferenceDefault(flag *pflag.Flag) string {
	value := flag.DefValue
	if flag.Value.Type() == "string" {
		return filepath.ToSlash(value)
	}
	switch value {
	case "", "false", "0", "0s", "[]", "map[]", "<nil>":
		return ""
	}
	return filepath.ToSlash(value)
}

var cliReferenceExamplesHeading = regexp.MustCompile(`^Examples?:\s*$`)

// cliReferenceSplitLong separates a free-text "Examples:" block from the
// description. The block holds the indented lines after the heading.
func cliReferenceSplitLong(long string) (string, string) {
	var description, examples []string
	inExamples := false
	for _, line := range strings.Split(strings.ReplaceAll(long, "\r\n", "\n"), "\n") {
		if cliReferenceExamplesHeading.MatchString(line) {
			inExamples = true
			continue
		}
		if inExamples && (strings.TrimSpace(line) == "" || line[0] == ' ' || line[0] == '\t') {
			examples = append(examples, line)
			continue
		}
		inExamples = false
		description = append(description, line)
	}
	return strings.TrimSpace(strings.Join(description, "\n")), strings.Join(examples, "\n")
}

var cliReferenceTwoColumnExample = regexp.MustCompile(`^(\S.*?\S)\s{2,}(\S.*)$`)

// cliReferenceExamples parses cobra Example text. "# text" lines describe the
// command lines that follow; consecutive command lines form one copyable
// example until a blank line or the next comment. A legacy two-column line
// ("command  description") is one example on its own.
func cliReferenceExamples(text string) []cliReferenceExample {
	examples := []cliReferenceExample{}
	var description, command []string
	flush := func() {
		if len(command) > 0 {
			examples = append(examples, cliReferenceExample{
				Command:     strings.Join(command, "\n"),
				Description: strings.Join(description, " "),
			})
			description = nil
		}
		command = nil
	}
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		continuation := len(command) > 0 && strings.HasSuffix(command[len(command)-1], "\\")
		switch {
		case continuation && line != "":
			command = append(command, "  "+line)
		case line == "":
			flush()
			description = nil
		case strings.HasPrefix(line, "#"):
			flush()
			description = append(description, strings.TrimSpace(strings.TrimPrefix(line, "#")))
		default:
			if match := cliReferenceTwoColumnExample.FindStringSubmatch(line); match != nil && !strings.HasSuffix(match[1], "\\") {
				flush()
				examples = append(examples, cliReferenceExample{Command: match[1], Description: match[2]})
				description = nil
				continue
			}
			command = append(command, line)
		}
	}
	flush()
	return examples
}
