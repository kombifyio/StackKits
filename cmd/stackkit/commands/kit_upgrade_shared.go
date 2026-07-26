//go:build publisher

package commands

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

// confirm reads y/Y/yes from stdin. It is shared by upgrade and rollback
// transaction commands when --auto-approve is not set.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	fmt.Print(prompt)
	var response string
	if _, err := fmt.Fscanln(cmd.InOrStdin(), &response); err != nil {
		return false, nil
	}
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes", nil
}

func writeDeploymentState(path string, state *models.DeploymentState) error {
	loader := config.NewLoader(filepath.Dir(filepath.Dir(path)))
	return loader.SaveDeploymentState(state, path)
}
