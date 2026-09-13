package localevidence

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ComposeInterpolationEnvironment is the process environment docker compose
// needs to parse generated Core compose files. Apply and restore-activate
// share it so stop/up never invent a second interpolation path.
func ComposeInterpolationEnvironment(workspaceRoot string) ([]string, error) {
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve owner email for Compose interpolation: %w", err)
	}
	email := strings.TrimSpace(owner.PocketID.Email)
	if email == "" {
		return nil, errors.New("owner custody carries no contact email for Compose interpolation")
	}
	return []string{
		"LANG=C", "LC_ALL=C",
		"STACKKIT_CUSTODY_DIR=" + filepath.Join(workspaceRoot, ".stackkit", "custody"),
		"STACKKIT_OWNER_EMAIL=" + email,
	}, nil
}
