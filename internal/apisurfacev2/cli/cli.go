// Package cli mounts an apisurface.Surface as cobra commands. Every command
// calls the product's HTTP API exactly as the OpenAPI contract describes it;
// products supply the base URL and authorization, embed their generated
// api-surface.json and call Mount from their root command.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/apisurfacev2"
	"github.com/spf13/cobra"
)

// Exit codes returned through ExitError.
const (
	ExitFailure      = 1 // any other failure
	ExitInvalid      = 2 // invalid input or HTTP 400/422
	ExitUnauthorized = 3 // HTTP 401
	ExitDenied       = 4 // HTTP 402/403/429
	ExitNotFound     = 5 // HTTP 404
	ExitConflict     = 6 // HTTP 409
	ExitUnavailable  = 7 // HTTP 5xx or transport failure
	ExitConfirmation = 9 // confirmation required or declined
)

// Config binds the generated commands to a product runtime.
type Config struct {
	// BaseURL returns the API origin, e.g. "https://api.kombify.io/v1/techstack".
	BaseURL func(ctx context.Context) (string, error)
	// ResolvePath optionally maps the contract path (parameters substituted)
	// to the path under BaseURL, e.g. to strip a prefix the edge re-adds.
	ResolvePath func(path string) string
	// HTTPClient defaults to http.DefaultClient.
	HTTPClient *http.Client
	// Authorize adds credentials to an outgoing request; nil sends none. It
	// is never called for --dry-run or refused confirmations.
	Authorize func(*http.Request) error
	// In, Out and Err default to the process streams.
	In       io.Reader
	Out, Err io.Writer
	// Interactive reports whether a human can answer a confirmation prompt;
	// nil means non-interactive, so confirmations fail closed.
	Interactive func() bool
}

// ExitError carries the process exit code for a failed command.
type ExitError struct {
	Code int
	Err  error
	// reported is set when the runtime already wrote the failure to Err.
	reported bool
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode maps an error returned by cobra's Execute to a process exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return ExitFailure
}

// LoadSurface decodes an embedded api-surface.json.
func LoadSurface(data []byte) (*apisurface.Surface, error) { return apisurface.Parse(data) }

// Mount adds one command per surface operation under root, creating
// intermediate group commands, plus an agent-context command unless root
// already has one. It fails when a generated command collides with an
// existing command.
func Mount(root *cobra.Command, s *apisurface.Surface, cfg Config) error {
	if root == nil || s == nil {
		return errors.New("cli: Mount requires a root command and a surface")
	}
	if cfg.BaseURL == nil {
		return errors.New("cli: Config.BaseURL is required")
	}
	cfg = cfg.withDefaults()
	groups := map[*cobra.Command]bool{}
	for i := range s.Operations {
		op := &s.Operations[i]
		words := op.CLI.Command
		if len(words) == 0 {
			return fmt.Errorf("cli: operation %s has no command", op.OperationID)
		}
		parent, err := group(root, words[:len(words)-1], groups)
		if err != nil {
			return err
		}
		leaf := newCommand(op, cfg)
		for _, name := range append([]string{leaf.Name()}, leaf.Aliases...) {
			if c := child(parent, name); c != nil {
				return fmt.Errorf("cli: command %q for %s collides with an existing command", strings.Join(append(pathOf(parent), name), " "), op.OperationID)
			}
		}
		parent.AddCommand(leaf)
	}
	if child(root, "agent-context") == nil {
		root.AddCommand(agentContextCommand(root, s, cfg))
	}
	return nil
}

func (c Config) withDefaults() Config {
	if c.HTTPClient == nil {
		c.HTTPClient = http.DefaultClient
	}
	if c.In == nil {
		c.In = os.Stdin
	}
	if c.Out == nil {
		c.Out = os.Stdout
	}
	if c.Err == nil {
		c.Err = os.Stderr
	}
	if c.Interactive == nil {
		c.Interactive = func() bool { return false }
	}
	if c.ResolvePath == nil {
		c.ResolvePath = func(p string) string { return p }
	}
	return c
}

// group returns the command at words below root, creating group commands.
// Only groups created by Mount may be shared.
func group(root *cobra.Command, words []string, created map[*cobra.Command]bool) (*cobra.Command, error) {
	cur := root
	for _, w := range words {
		next := child(cur, w)
		switch {
		case next == nil:
			next = &cobra.Command{Use: w, Short: w + " commands"}
			created[next] = true
			cur.AddCommand(next)
		case !created[next]:
			return nil, fmt.Errorf("cli: command group %q collides with an existing command", strings.Join(append(pathOf(cur), w), " "))
		}
		cur = next
	}
	return cur, nil
}

func child(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}

func pathOf(c *cobra.Command) []string {
	var words []string
	for ; c != nil && c.HasParent(); c = c.Parent() {
		words = append([]string{c.Name()}, words...)
	}
	return words
}
