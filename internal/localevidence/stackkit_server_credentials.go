package localevidence

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// The Core stackkit-server credentials live in their own custody directory so
// the owner-signed runtime inventories stay closed. The mcp/ subdirectory is
// the credential store the server reads through STACKKIT_MCP_TOKEN_FILE;
// per-agent credentials can join it without another mount (ADR-0044).
const (
	stackKitServerCustodyRelDir = ".stackkit/custody/stackkit-server"
	stackKitServerMCPDir        = "mcp"
	stackKitServerMCPTokenFile  = "token"
	stackKitServerAPIKeyFile    = "api-key"
	stackKitServerSecretBytes   = 32
)

// StackKitServerCredentials locates the apply-minted credentials. It never
// carries a credential value.
type StackKitServerCredentials struct {
	MCPTokenPath string
	// MCPTokenMinted is true only when this call created the MCP token.
	MCPTokenMinted bool
}

// EnsureStackKitServerCredentials mints the dedicated MCP token and the
// server API key once, each from 32 bytes of crypto/rand, into owner-only
// files. Existing credentials are kept, so a re-apply never rotates them.
func EnsureStackKitServerCredentials(workspaceRoot string) (StackKitServerCredentials, error) {
	directory, err := confinedCustodyPath(workspaceRoot, stackKitServerCustodyRelDir)
	if err != nil {
		return StackKitServerCredentials{}, err
	}
	mcpDirectory := filepath.Join(directory, stackKitServerMCPDir)
	for _, dir := range []string{directory, mcpDirectory} {
		if err := ensurePrivateCustodyDirectory(dir); err != nil {
			return StackKitServerCredentials{}, err
		}
	}
	minted, err := ensureCustodySecretFile(mcpDirectory, stackKitServerMCPTokenFile, func(secret string) []byte {
		return []byte(secret + "\n")
	})
	if err != nil {
		return StackKitServerCredentials{}, fmt.Errorf("localevidence: StackKits MCP token: %w", err)
	}
	// stackkit-server refuses to start without an API key. The key guards its
	// REST API on the internal Core network; the router never publishes it.
	if _, err := ensureCustodySecretFile(directory, stackKitServerAPIKeyFile, func(secret string) []byte {
		return []byte(secret + "\n")
	}); err != nil {
		return StackKitServerCredentials{}, fmt.Errorf("localevidence: stackkit-server API key: %w", err)
	}
	return StackKitServerCredentials{
		MCPTokenPath:   filepath.Join(mcpDirectory, stackKitServerMCPTokenFile),
		MCPTokenMinted: minted,
	}, nil
}

// StackKitServerUserEnv names the Compose interpolation that carries the
// credential owner's uid:gid.
const StackKitServerUserEnv = "STACKKIT_SERVER_USER"

// StackKitServerComposeEnvironment returns the credential owner as Compose
// interpolation. The server container runs as that uid:gid, so it reads the
// owner-only files without any added capability. Before Apply has minted the
// credentials it returns nothing, and a Compose file that needs the value
// fails closed with guidance.
func StackKitServerComposeEnvironment(workspaceRoot string) []string {
	directory, err := confinedCustodyPath(workspaceRoot, stackKitServerCustodyRelDir)
	if err != nil {
		return nil
	}
	owner, ok := stackKitServerCredentialOwner(directory)
	if !ok {
		return nil
	}
	return []string{StackKitServerUserEnv + "=" + owner}
}

// StackKitServerMCPTokenPath returns the MCP token location for owner-facing
// output; it does not read or create the token.
func StackKitServerMCPTokenPath(workspaceRoot string) (string, error) {
	directory, err := confinedCustodyPath(workspaceRoot, stackKitServerCustodyRelDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, stackKitServerMCPDir, stackKitServerMCPTokenFile), nil
}

// RemoveStackKitServerCredentials deletes the credentials with the runtime
// that used them, so an uninstalled Core leaves no valid MCP credential.
func RemoveStackKitServerCredentials(workspaceRoot string) error {
	directory, err := confinedCustodyPath(workspaceRoot, stackKitServerCustodyRelDir)
	if err != nil {
		return err
	}
	if info, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("localevidence: inspect stackkit-server custody: %w", err)
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("localevidence: stackkit-server custody is not a plain directory")
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("localevidence: remove stackkit-server custody: %w", err)
	}
	return nil
}

func ensurePrivateCustodyDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("localevidence: create %s: %w", filepath.Base(directory), err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return fmt.Errorf("localevidence: inspect %s: %w", filepath.Base(directory), err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("localevidence: %s is not a plain directory", filepath.Base(directory))
	}
	return os.Chmod(directory, 0o700)
}

// ensureCustodySecretFile keeps a valid existing file and otherwise publishes
// a fresh secret without replacing a file that appeared concurrently.
func ensureCustodySecretFile(directory, name string, render func(string) []byte) (bool, error) {
	path := filepath.Join(directory, name)
	if _, err := os.Lstat(path); err == nil {
		if err := requirePrivateRuntimeFile(path); err != nil {
			return false, err
		}
		raw, err := os.ReadFile(path) //nolint:gosec // fixed custody path below the workspace
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(string(raw)) == "" {
			return false, fmt.Errorf("%s is empty; delete it to mint a new credential", path)
		}
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	secret, err := randomRuntimeSecret(stackKitServerSecretBytes, base64.RawURLEncoding)
	if err != nil {
		return false, err
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return false, err
	}
	result, err := view.WriteAtomic0600NoReplace(name, render(secret))
	if err != nil {
		return false, err
	}
	if !result.Installed || !result.FileSynced {
		return false, errors.New("credential write did not prove an installed private file")
	}
	return true, restrictFileToCurrentUser(path)
}
