package stackkitmcp

import (
	"context"
	"fmt"

	"github.com/kombifyio/stackkits/internal/clibinding"
)

type cliBinaryBinding = clibinding.Binding

// SiblingStackkitBinary returns the packaged CLI beside stackkit-server or
// stackkit-mcp, through the shared exact-build local process binding.
func SiblingStackkitBinary() (string, error) { return clibinding.Sibling() }

func bindCLIBinary(opts Options) (*cliBinaryBinding, error) {
	return clibinding.Bind(context.Background(), opts.Binary, opts.Version, opts.GitCommit)
}

func (a *App) verifyCLIBinding() error {
	if a.cliBinding == nil {
		if a.cliBindingError != nil {
			return a.cliBindingError
		}
		return fmt.Errorf("process-backed MCP CLI is not bound")
	}
	return a.cliBinding.Verify()
}
