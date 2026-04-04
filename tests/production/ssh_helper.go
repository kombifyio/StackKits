//go:build production

package production

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHSession wraps an active SSH connection to a Sim node.
type SSHSession struct {
	client      *ssh.Client
	proxyClient *ssh.Client // non-nil when connected via ProxyJump
	node        Node
}

// NewSSHSession dials the node and returns an authenticated SSH session.
// When KOMBIFY_PROXY_JUMP is set (format "user@host" or "user@host:port"),
// the connection is routed through that bastion host — useful for CI runners
// outside the server network (e.g. GitHub Actions). Falls back to the
// ProxyJump hint returned in the node struct, then direct connection.
func NewSSHSession(node Node) (*SSHSession, error) {
	cfg, err := buildNodeSSHConfig(node)
	if err != nil {
		return nil, err
	}

	// Determine ProxyJump: env overrides node hint.
	proxyJump := os.Getenv("KOMBIFY_PROXY_JUMP")
	if proxyJump == "" {
		proxyJump = node.ProxyJump
	}

	// Try localhost:<port> first — works when tests run on the Sim server itself.
	localAddr := net.JoinHostPort("localhost", fmt.Sprintf("%d", node.SSHPort))
	if conn, err := net.DialTimeout("tcp", localAddr, 3*time.Second); err == nil {
		conn.Close()
		localNode := node
		localNode.SSHIP = "localhost"
		if client, err := ssh.Dial("tcp", localAddr, cfg); err == nil {
			return &SSHSession{client: client, node: localNode}, nil
		}
	}

	if proxyJump != "" {
		return newSSHSessionViaProxy(node, proxyJump, cfg)
	}
	return newSSHSessionDirect(node, cfg)
}

func buildNodeSSHConfig(node Node) (*ssh.ClientConfig, error) {
	user := node.SSHUser
	if user == "" {
		user = "root"
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test helper
		Timeout:         20 * time.Second,
	}

	keyPath := node.SSHKeyPath
	if keyPath == "" {
		keyPath = os.Getenv("KOMBIFY_SSH_KEY_PATH")
	}
	if keyPath != "" {
		keyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("read SSH key %s: %w", keyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse SSH key: %w", err)
		}
		cfg.Auth = append(cfg.Auth, ssh.PublicKeys(signer))
	}

	password := node.SSHPassword
	if password == "" {
		password = os.Getenv("KOMBIFY_SSH_PASSWORD")
	}
	if password != "" {
		cfg.Auth = append(cfg.Auth, ssh.Password(password))
	}

	if len(cfg.Auth) == 0 {
		return nil, fmt.Errorf("no SSH auth configured: set SSH key path or password")
	}

	return cfg, nil
}

// NewSSHSessionFromInfo opens an SSH connection using NodeSSHInfo returned by
// the Sim /nodes/:id/ssh endpoint. The keyPath must be the local path to the
// extracted PEM file (see extractNodeSSHKey). When running on the Sim server,
// a direct connection to localhost:<port> is tried first; the ProxyJump path
// is used as a fallback and for GitHub Actions runners.
func NewSSHSessionFromInfo(info NodeSSHInfo, localKeyPath string) (*SSHSession, error) {
	keyBytes, err := os.ReadFile(localKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read SSH key %s: %w", localKeyPath, err)
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse SSH key: %w", err)
	}

	user := info.User
	if user == "" {
		user = "kombify-sim"
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test helper
		Timeout:         20 * time.Second,
	}

	// Try direct localhost connection first (on-server runners).
	// The Sim always NAT-forwards the SSH port to 0.0.0.0:<port> on the host,
	// so localhost:<port> works when tests run on the same machine as Sim.
	proxyJump := os.Getenv("KOMBIFY_PROXY_JUMP")
	if proxyJump == "" {
		proxyJump = info.ProxyJump
	}

	directAddr := net.JoinHostPort("localhost", fmt.Sprintf("%d", info.Port))
	conn, err := net.DialTimeout("tcp", directAddr, 3*time.Second)
	if err == nil {
		conn.Close()
		// Port is reachable on localhost — connect directly (no bastion needed).
		client, dialErr := ssh.Dial("tcp", directAddr, cfg)
		if dialErr == nil {
			n := Node{SSHIP: "localhost", SSHPort: info.Port, SSHUser: user, ProxyJump: proxyJump}
			return &SSHSession{client: client, node: n}, nil
		}
	}

	// Fall back to ProxyJump (remote CI / GitHub Actions).
	if proxyJump != "" {
		n := Node{SSHIP: info.Host, SSHPort: info.Port, SSHUser: user, ProxyJump: proxyJump}
		return newSSHSessionViaProxy(n, proxyJump, cfg)
	}

	// Last resort: direct connection to the info.Host address.
	n := Node{SSHIP: info.Host, SSHPort: info.Port, SSHUser: user}
	return newSSHSessionDirect(n, cfg)
}

// newSSHSessionDirect opens a direct TCP connection to the node.
func newSSHSessionDirect(node Node, cfg *ssh.ClientConfig) (*SSHSession, error) {
	addr := net.JoinHostPort(node.SSHIP, fmt.Sprintf("%d", node.SSHPort))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s: %w", addr, err)
	}
	return &SSHSession{client: client, node: node}, nil
}

// newSSHSessionViaProxy connects to the node through a ProxyJump bastion.
// proxyJump format: "user@host" or "user@host:port" (default port 22).
// Proxy auth uses KOMBIFY_PROXY_JUMP_KEY (PEM file path) or
// KOMBIFY_PROXY_JUMP_PASSWORD as fallback.
func newSSHSessionViaProxy(node Node, proxyJump string, targetCfg *ssh.ClientConfig) (*SSHSession, error) {
	proxyUser, proxyAddr := parseProxyJump(proxyJump)

	proxyCfg := buildProxySSHConfig(proxyUser)

	proxyClient, err := ssh.Dial("tcp", proxyAddr, proxyCfg)
	if err != nil {
		return nil, fmt.Errorf("SSH dial proxy %s: %w", proxyAddr, err)
	}

	targetAddr := net.JoinHostPort(node.SSHIP, fmt.Sprintf("%d", node.SSHPort))
	conn, err := proxyClient.Dial("tcp", targetAddr)
	if err != nil {
		proxyClient.Close()
		return nil, fmt.Errorf("proxy tunnel to %s: %w", targetAddr, err)
	}

	ncc, chans, reqs, err := ssh.NewClientConn(conn, targetAddr, targetCfg)
	if err != nil {
		conn.Close()
		proxyClient.Close()
		return nil, fmt.Errorf("SSH handshake via proxy to %s: %w", targetAddr, err)
	}

	client := ssh.NewClient(ncc, chans, reqs)
	return &SSHSession{client: client, proxyClient: proxyClient, node: node}, nil
}

// parseProxyJump splits "user@host" or "user@host:port" into (user, host:port).
func parseProxyJump(proxyJump string) (user, addr string) {
	at := strings.LastIndex(proxyJump, "@")
	if at < 0 {
		user = "root"
		addr = proxyJump
	} else {
		user = proxyJump[:at]
		addr = proxyJump[at+1:]
	}
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "22")
	}
	return
}

// buildProxySSHConfig constructs SSH client config for the bastion host.
// Key file: KOMBIFY_PROXY_JUMP_KEY env (path to PEM).
// Password fallback: KOMBIFY_PROXY_JUMP_PASSWORD env.
func buildProxySSHConfig(user string) *ssh.ClientConfig {
	cfg := &ssh.ClientConfig{
		User:            user,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test helper
		Timeout:         20 * time.Second,
	}

	keyPath := os.Getenv("KOMBIFY_PROXY_JUMP_KEY")
	if keyPath != "" {
		if keyBytes, err := os.ReadFile(keyPath); err == nil {
			if signer, err := ssh.ParsePrivateKey(keyBytes); err == nil {
				cfg.Auth = append(cfg.Auth, ssh.PublicKeys(signer))
			}
		}
	}

	// Also try inline PEM from env (useful in CI where key is a secret value)
	if pemStr := os.Getenv("KOMBIFY_PROXY_JUMP_KEY_PEM"); pemStr != "" {
		pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")
		if signer, err := ssh.ParsePrivateKey([]byte(pemStr)); err == nil {
			cfg.Auth = append(cfg.Auth, ssh.PublicKeys(signer))
		}
	}

	if password := os.Getenv("KOMBIFY_PROXY_JUMP_PASSWORD"); password != "" {
		cfg.Auth = append(cfg.Auth, ssh.Password(password))
	}

	return cfg
}

// Close closes the SSH connection (and proxy connection if any).
func (s *SSHSession) Close() error {
	err := s.client.Close()
	if s.proxyClient != nil {
		s.proxyClient.Close()
	}
	return err
}

// Run executes a shell command on the remote node and returns combined output.
// Returns an error if the command exits non-zero.
func (s *SSHSession) Run(cmd string) (string, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("new SSH session: %w", err)
	}
	defer sess.Close()

	var buf strings.Builder
	sess.Stdout = &buf
	sess.Stderr = &buf

	if err := sess.Run(cmd); err != nil {
		return buf.String(), fmt.Errorf("command %q: %w\noutput: %s", cmd, err, buf.String())
	}
	return buf.String(), nil
}

// RunOutput executes a command and returns output even on non-zero exit.
func (s *SSHSession) RunOutput(cmd string) (string, int, error) {
	sess, err := s.client.NewSession()
	if err != nil {
		return "", -1, fmt.Errorf("new SSH session: %w", err)
	}
	defer sess.Close()

	var buf strings.Builder
	sess.Stdout = &buf
	sess.Stderr = &buf

	runErr := sess.Run(cmd)
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*ssh.ExitError); ok {
			exitCode = exitErr.ExitStatus()
		} else {
			return buf.String(), -1, runErr
		}
	}
	return buf.String(), exitCode, nil
}

// RunWithEnv executes a shell command with additional environment variables.
func (s *SSHSession) RunWithEnv(env map[string]string, cmd string) (string, error) {
	var exports strings.Builder
	for k, v := range env {
		exports.WriteString(fmt.Sprintf("export %s=%q; ", k, v))
	}
	return s.Run(exports.String() + cmd)
}

// Upload copies a local file to a remote path via cat-over-SSH.
func (s *SSHSession) Upload(localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", localPath, err)
	}
	defer f.Close()

	return s.uploadReader(f, remotePath)
}

// UploadBytes writes byte content to a remote path.
func (s *SSHSession) UploadBytes(content []byte, remotePath string) error {
	sess, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("new SSH session: %w", err)
	}
	defer sess.Close()

	w, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	if err := sess.Start(fmt.Sprintf("cat > %s && chmod +x %s", remotePath, remotePath)); err != nil {
		return fmt.Errorf("start remote write: %w", err)
	}

	if _, err := w.Write(content); err != nil {
		return err
	}
	w.Close()

	return sess.Wait()
}

func (s *SSHSession) uploadReader(r io.Reader, remotePath string) error {
	sess, err := s.client.NewSession()
	if err != nil {
		return fmt.Errorf("new SSH session: %w", err)
	}
	defer sess.Close()

	w, err := sess.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	if err := sess.Start(fmt.Sprintf("cat > %s", remotePath)); err != nil {
		return fmt.Errorf("start remote cat: %w", err)
	}

	if _, err := io.Copy(w, r); err != nil {
		return err
	}
	w.Close()

	return sess.Wait()
}

// ForwardPort opens an SSH tunnel: traffic to localhost:localPort on the test
// runner is forwarded to remoteHost:remotePort on the node.
// The returned closer stops the tunnel.
func (s *SSHSession) ForwardPort(localPort int, remoteHost string, remotePort int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return nil, fmt.Errorf("listen on local port %d: %w", localPort, err)
	}

	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return
			}
			remote, err := s.client.Dial("tcp", fmt.Sprintf("%s:%d", remoteHost, remotePort))
			if err != nil {
				local.Close()
				continue
			}
			go io.Copy(remote, local)  //nolint:errcheck
			go io.Copy(local, remote)  //nolint:errcheck
		}
	}()

	return ln, nil
}

// WaitForSSH polls until SSH is reachable or timeout expires.
// Uses the same ProxyJump logic as NewSSHSession.
func WaitForSSH(node Node, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if canReach(node) {
			return nil
		}
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("SSH not reachable after %s", timeout)
}

func canReach(node Node) bool {
	proxyJump := os.Getenv("KOMBIFY_PROXY_JUMP")
	if proxyJump == "" {
		proxyJump = node.ProxyJump
	}

	// Always try localhost:<port> first — Sim always NAT-forwards the SSH port
	// to 0.0.0.0:<port> on the host, so this works when running on the Sim server.
	localAddr := net.JoinHostPort("localhost", fmt.Sprintf("%d", node.SSHPort))
	if conn, err := net.DialTimeout("tcp", localAddr, 3*time.Second); err == nil {
		conn.Close()
		return true
	}

	if proxyJump != "" {
		_, proxyAddr := parseProxyJump(proxyJump)
		proxyCfg := buildProxySSHConfig("root")
		proxy, err := ssh.Dial("tcp", proxyAddr, proxyCfg)
		if err != nil {
			return false
		}
		defer proxy.Close()
		targetAddr := net.JoinHostPort(node.SSHIP, fmt.Sprintf("%d", node.SSHPort))
		conn, err := proxy.Dial("tcp", targetAddr)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}

	addr := net.JoinHostPort(node.SSHIP, fmt.Sprintf("%d", node.SSHPort))
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
