// Package kombifynotify is the node-side custody of the opt-in "kombify
// notify" integration of Smart Home.
//
// StackKits is the node's custody holder. When the owner turns on the
// kombify-notify workload setting, Apply creates two files next to the access
// manifest, in <workspace>/.stackkit/kombify-notify/:
//
//   - token holds the bearer secret that Home Assistant presents to the local
//     Techstack agent. It is created once and never rotated by a re-apply.
//   - listen holds the single host:port the agent must bind. It is the
//     gateway address of the Docker network Home Assistant is attached to, so
//     the container can reach it and nothing else can.
//
// Both files are written by StackKits and only read by the agent. The token
// reaches Home Assistant only through the owner-only secrets file that Apply
// bind-mounts; it never appears in a plan, receipt, state file, log, command
// line or generated configuration.
package kombifynotify

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const (
	// Port is the node-local port the Techstack agent binds.
	Port = 18741
	// DirectoryRelPath is the custody directory beside .stackkit/access.json.
	DirectoryRelPath = ".stackkit/kombify-notify"
	// TokenFile and ListenFile are the two files the agent reads.
	TokenFile  = "token"
	ListenFile = "listen"

	tokenBytes = 32
	// tokenLength is the unpadded base64url length of tokenBytes.
	tokenLength = 43
)

// ErrCustodyMissing means the owner enabled the integration but Apply has not
// created its token yet. Inspection never creates it.
var ErrCustodyMissing = errors.New("kombify notify token custody is missing")

func tokenRelPath() string  { return DirectoryRelPath + "/" + TokenFile }
func listenRelPath() string { return DirectoryRelPath + "/" + ListenFile }

// ListenAddress renders the exact host:port line the agent binds.
func ListenAddress(gateway netip.Addr) (string, error) {
	if err := ValidateGateway(gateway); err != nil {
		return "", err
	}
	return netip.AddrPortFrom(gateway, Port).String(), nil
}

// ValidateGateway admits only a private IPv4 address of a container bridge.
// A public, loopback, link-local or unspecified address would either expose
// the agent or be unreachable from a bridged container.
func ValidateGateway(gateway netip.Addr) error {
	if !gateway.IsValid() || !gateway.Is4() || !gateway.IsPrivate() || gateway.IsLoopback() || gateway.IsUnspecified() {
		return errors.New("kombify notify listens only on the private gateway address of the Home Assistant container network")
	}
	return nil
}

// Ensure creates the token when absent, never replaces an existing one, and
// publishes the listen address. It returns the token for the caller's
// owner-only secrets file.
func Ensure(workspace string, gateway netip.Addr) (string, error) {
	listen, err := ListenAddress(gateway)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(workspace, filepath.FromSlash(DirectoryRelPath)), 0o700); err != nil {
		return "", fmt.Errorf("create kombify notify custody directory: %w", err)
	}
	if err := os.Chmod(filepath.Join(workspace, filepath.FromSlash(DirectoryRelPath)), 0o700); err != nil {
		return "", fmt.Errorf("restrict kombify notify custody directory: %w", err)
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return "", err
	}
	token, err := readToken(view)
	if errors.Is(err, ErrCustodyMissing) {
		token, err = createToken(view)
	}
	if err != nil {
		return "", err
	}
	if err := writeInstalled(view, listenRelPath(), []byte(listen+"\n")); err != nil {
		return "", fmt.Errorf("publish kombify notify listen address: %w", err)
	}
	return token, root.VerifyPathIdentity()
}

// Load reads the existing token without creating anything.
func Load(workspace string) (string, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return "", err
	}
	return readToken(view)
}

func createToken(view confinedfs.View) (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate kombify notify token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	clear(raw)
	result, err := view.WriteAtomic0600NoReplace(tokenRelPath(), []byte(token+"\n"))
	if err != nil {
		// A concurrent Apply may have published first; its token wins.
		if errors.Is(err, fs.ErrExist) {
			return readToken(view)
		}
		return "", fmt.Errorf("persist kombify notify token: %w", err)
	}
	if !result.Installed || !result.FileSynced {
		return "", errors.New("kombify notify token was not durably installed")
	}
	return token, nil
}

func readToken(view confinedfs.View) (string, error) {
	file, err := view.Open(tokenRelPath())
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", ErrCustodyMissing
		}
		return "", fmt.Errorf("open kombify notify token: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect kombify notify token: %w", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		// Tighten, never loosen: the token is a bearer secret.
		if err := file.Chmod(0o600); err != nil {
			return "", fmt.Errorf("restrict kombify notify token: %w", err)
		}
	}
	raw, err := io.ReadAll(io.LimitReader(file, 256))
	if err != nil {
		return "", fmt.Errorf("read kombify notify token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if !validToken(token) {
		// An unreadable value is never replaced silently: the owner decides
		// whether to delete it and let Apply issue a new one.
		return "", errors.New("kombify notify token is malformed; remove the file to issue a new one on the next Apply")
	}
	return token, nil
}

func validToken(token string) bool {
	if len(token) != tokenLength {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == tokenBytes
}

func writeInstalled(view confinedfs.View, relative string, data []byte) error {
	result, err := view.WriteAtomic0600(relative, data)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("file was not durably installed")
	}
	return nil
}
