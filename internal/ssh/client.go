// Package ssh provides SSH operations for remote system management.
package ssh

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/pkg/models"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// Client handles SSH connections
type Client struct {
	host            string
	port            int
	user            string
	keyPath         string
	timeout         time.Duration
	client          *ssh.Client
	strictHostKey   bool
	knownHostsPath  string
	autoAddHostKeys bool
}

// ClientOption configures the SSH client
type ClientOption func(*Client)

// WithHost sets the SSH host
func WithHost(host string) ClientOption {
	return func(c *Client) {
		c.host = host
	}
}

// WithPort sets the SSH port
func WithPort(port int) ClientOption {
	return func(c *Client) {
		c.port = port
	}
}

// WithUser sets the SSH user
func WithUser(user string) ClientOption {
	return func(c *Client) {
		c.user = user
	}
}

// WithKeyPath sets the SSH key path
func WithKeyPath(keyPath string) ClientOption {
	return func(c *Client) {
		c.keyPath = keyPath
	}
}

// WithSSHTimeout sets the connection timeout
func WithSSHTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.timeout = timeout
	}
}

// WithStrictHostKey enables strict host key checking
func WithStrictHostKey(strict bool) ClientOption {
	return func(c *Client) {
		c.strictHostKey = strict
	}
}

// WithKnownHostsPath sets custom known_hosts file path
func WithKnownHostsPath(path string) ClientOption {
	return func(c *Client) {
		c.knownHostsPath = path
	}
}

// WithAutoAddHostKeys automatically adds unknown host keys
func WithAutoAddHostKeys(auto bool) ClientOption {
	return func(c *Client) {
		c.autoAddHostKeys = auto
	}
}

// NewClient creates a new SSH client
func NewClient(opts ...ClientOption) *Client {
	home, _ := os.UserHomeDir()

	c := &Client{
		port:            22,
		user:            "root",
		timeout:         30 * time.Second,
		strictHostKey:   true,
		knownHostsPath:  filepath.Join(home, ".ssh", "known_hosts"),
		autoAddHostKeys: false,
	}

	for _, opt := range opts {
		opt(c)
	}

	// Default key path
	if c.keyPath == "" {
		c.keyPath = filepath.Join(home, ".ssh", "id_ed25519")
		if _, err := os.Stat(c.keyPath); os.IsNotExist(err) {
			c.keyPath = filepath.Join(home, ".ssh", "id_rsa")
		}
	}

	return c
}

// shellQuote safely quotes a string for shell execution
func shellQuote(s string) string {
	// Use single quotes and escape any single quotes within
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// validatePath checks for path traversal attacks and injection attempts
func validatePath(path string) error {
	// Check for empty path
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	// Check for null bytes
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("path contains null byte")
	}

	// Check for newlines which could be used for injection
	if strings.ContainsAny(path, "\n\r") {
		return fmt.Errorf("path contains newline characters")
	}

	// Check for shell metacharacters that could cause injection
	dangerous := regexp.MustCompile(`[;&|$` + "`" + `\\<>(){}!*?\[\]~#]`)
	if dangerous.MatchString(path) {
		return fmt.Errorf("path contains potentially dangerous characters: %s", path)
	}

	return nil
}

// Connect establishes an SSH connection
func (c *Client) Connect() error {
	key, err := os.ReadFile(c.keyPath)
	if err != nil {
		return fmt.Errorf("failed to read SSH key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return fmt.Errorf("failed to parse SSH key: %w", err)
	}

	// Configure host key callback
	var hostKeyCallback ssh.HostKeyCallback
	if c.strictHostKey {
		// Try to use known_hosts file
		if _, statErr := os.Stat(c.knownHostsPath); statErr == nil {
			hostKeyCallback, err = knownhosts.New(c.knownHostsPath)
			if err != nil {
				return fmt.Errorf("failed to load known_hosts: %w", err)
			}
		} else if c.autoAddHostKeys {
			// Create a callback that adds unknown keys to known_hosts
			hostKeyCallback = c.createAutoAddCallback()
		} else {
			return fmt.Errorf("strict host key checking enabled but known_hosts not found: %s", c.knownHostsPath)
		}
	} else {
		fmt.Fprintf(os.Stderr, "WARNING: SSH host key verification is disabled for %s:%d. This is insecure and should only be used for testing/development.\n", c.host, c.port)
		hostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec // G106: InsecureIgnoreHostKey is the fallback when no known_hosts file exists
	}

	config := &ssh.ClientConfig{
		User: c.user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         c.timeout,
	}

	addr := fmt.Sprintf("%s:%d", c.host, c.port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("failed to connect to %s: %w", addr, err)
	}

	c.client = client
	return nil
}

// createAutoAddCallback creates a callback that adds unknown hosts
func (c *Client) createAutoAddCallback() ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		// Ensure known_hosts directory exists
		dir := filepath.Dir(c.knownHostsPath)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("failed to create .ssh directory: %w", err)
		}

		// Check if host already exists
		if _, err := os.Stat(c.knownHostsPath); err == nil {
			callback, err := knownhosts.New(c.knownHostsPath)
			if err == nil {
				// If no error from callback, host is known
				if callback(hostname, remote, key) == nil {
					return nil
				}
			}
		}

		// Add the new host key
		f, err := os.OpenFile(c.knownHostsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("failed to open known_hosts: %w", err)
		}
		defer func() { _ = f.Close() }()

		// Format the host key entry
		line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key)
		if _, err := f.WriteString(line + "\n"); err != nil {
			return fmt.Errorf("failed to write to known_hosts: %w", err)
		}

		return nil
	}
}

// Close closes the SSH connection
func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// Run executes a command on the remote host
func (c *Client) Run(ctx context.Context, command string) (string, string, error) {
	if c.client == nil {
		return "", "", fmt.Errorf("not connected")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("failed to create session: %w", err)
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	// Handle context cancellation
	done := make(chan error)
	go func() {
		done <- session.Run(command)
	}()

	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGKILL)
		return "", "", ctx.Err()
	case err := <-done:
		return stdout.String(), stderr.String(), err
	}
}

// RunWithSudo runs a command with sudo.
// Multi-line commands are wrapped in sudo bash -c to ensure all lines run with elevated privileges.
func (c *Client) RunWithSudo(ctx context.Context, command string) (string, string, error) {
	if strings.Contains(command, "\n") {
		// Escape single quotes in the command for safe embedding
		escaped := strings.ReplaceAll(command, "'", "'\"'\"'")
		return c.Run(ctx, "sudo bash -c '"+escaped+"'")
	}
	return c.Run(ctx, "sudo "+command)
}

// CopyFile copies a file to the remote host
func (c *Client) CopyFile(ctx context.Context, localPath, remotePath string) error {
	// Validate remote path
	if err := validatePath(remotePath); err != nil {
		return fmt.Errorf("invalid remote path: %w", err)
	}

	if c.client == nil {
		return fmt.Errorf("not connected")
	}

	// Read local file
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read local file: %w", err)
	}

	return c.WriteFile(ctx, remotePath, data, 0644)
}

// WriteFile writes content to a file on the remote host
func (c *Client) WriteFile(ctx context.Context, remotePath string, content []byte, mode os.FileMode) error {
	// Validate remote path to prevent command injection
	if err := validatePath(remotePath); err != nil {
		return fmt.Errorf("invalid remote path: %w", err)
	}

	if c.client == nil {
		return fmt.Errorf("not connected")
	}

	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer func() { _ = session.Close() }()

	// Use cat to write the file with properly quoted path
	go func() {
		w, _ := session.StdinPipe()
		defer w.Close()
		_, _ = io.Copy(w, bytes.NewReader(content))
	}()

	// Use properly quoted path to prevent command injection
	cmd := fmt.Sprintf("cat > %s && chmod %o %s", shellQuote(remotePath), mode, shellQuote(remotePath))
	return session.Run(cmd)
}

// ReadFile reads a file from the remote host
func (c *Client) ReadFile(ctx context.Context, remotePath string) ([]byte, error) {
	// Validate remote path to prevent command injection
	if err := validatePath(remotePath); err != nil {
		return nil, fmt.Errorf("invalid remote path: %w", err)
	}

	stdout, _, err := c.Run(ctx, "cat "+shellQuote(remotePath))
	if err != nil {
		return nil, err
	}
	return []byte(stdout), nil
}

// FileExists checks if a file exists on the remote host
func (c *Client) FileExists(ctx context.Context, remotePath string) bool {
	// Validate path - return false for invalid paths
	if err := validatePath(remotePath); err != nil {
		return false
	}
	_, _, err := c.Run(ctx, "test -f "+shellQuote(remotePath))
	return err == nil
}

// DirExists checks if a directory exists on the remote host
func (c *Client) DirExists(ctx context.Context, remotePath string) bool {
	// Validate path - return false for invalid paths
	if err := validatePath(remotePath); err != nil {
		return false
	}
	_, _, err := c.Run(ctx, "test -d "+shellQuote(remotePath))
	return err == nil
}

// MkdirAll creates a directory and parents on the remote host
func (c *Client) MkdirAll(ctx context.Context, remotePath string) error {
	// Validate remote path to prevent command injection
	if err := validatePath(remotePath); err != nil {
		return fmt.Errorf("invalid remote path: %w", err)
	}

	_, _, err := c.Run(ctx, "mkdir -p "+shellQuote(remotePath))
	return err
}

// GetSystemInfo retrieves system information from the remote host
func (c *Client) GetSystemInfo(ctx context.Context) (*models.SystemInfo, error) {
	info := &models.SystemInfo{}

	// Get hostname
	stdout, _, err := c.Run(ctx, "hostname")
	if err == nil {
		info.Hostname = strings.TrimSpace(stdout)
	}

	// Get OS info
	stdout, _, err = c.Run(ctx, "cat /etc/os-release | grep -E '^(ID|VERSION_ID)=' | head -2")
	if err == nil {
		for _, line := range strings.Split(stdout, "\n") {
			if strings.HasPrefix(line, "ID=") {
				info.OS = strings.Trim(strings.TrimPrefix(line, "ID="), "\"")
			}
			if strings.HasPrefix(line, "VERSION_ID=") {
				info.OSVersion = strings.Trim(strings.TrimPrefix(line, "VERSION_ID="), "\"")
			}
		}
	}

	// Get architecture
	stdout, _, err = c.Run(ctx, "uname -m")
	if err == nil {
		info.Arch = strings.TrimSpace(stdout)
	}

	// Get CPU cores
	stdout, _, err = c.Run(ctx, "nproc")
	if err == nil {
		_, _ = fmt.Sscanf(strings.TrimSpace(stdout), "%d", &info.CPUCores)
	}

	// Get memory
	stdout, _, err = c.Run(ctx, "free -m | awk '/^Mem:/ {print $2}'")
	if err == nil {
		_, _ = fmt.Sscanf(strings.TrimSpace(stdout), "%d", &info.MemoryMB)
	}

	// Get disk space
	stdout, _, err = c.Run(ctx, "df -BG / | awk 'NR==2 {print $4}' | tr -d 'G'")
	if err == nil {
		_, _ = fmt.Sscanf(strings.TrimSpace(stdout), "%d", &info.DiskGB)
	}

	// Get Docker version
	stdout, _, err = c.Run(ctx, "docker --version 2>/dev/null | awk '{print $3}' | tr -d ','")
	if err == nil && stdout != "" {
		info.DockerVersion = strings.TrimSpace(stdout)
	}

	// Get OpenTofu version
	stdout, _, err = c.Run(ctx, "tofu version 2>/dev/null | head -1 | awk '{print $2}' | tr -d 'v'")
	if err == nil && stdout != "" {
		info.TofuVersion = strings.TrimSpace(stdout)
	}

	return info, nil
}

// CheckPort checks if a port is available
func (c *Client) CheckPort(ctx context.Context, port int) bool {
	stdout, _, err := c.Run(ctx, fmt.Sprintf("ss -tuln | grep -q ':%d ' && echo 'in_use' || echo 'free'", port))
	if err != nil {
		return false
	}
	return strings.TrimSpace(stdout) == "free"
}

// Ping checks if the host is reachable
func Ping(host string, port int, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// IsConnected returns whether the client is connected
func (c *Client) IsConnected() bool {
	return c.client != nil
}

// GetHost returns the host
func (c *Client) GetHost() string {
	return c.host
}
