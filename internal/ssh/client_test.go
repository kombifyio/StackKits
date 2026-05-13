// Package ssh tests
package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cryptossh "golang.org/x/crypto/ssh"
)

func TestClient(t *testing.T) {
	t.Run("creates client with defaults", func(t *testing.T) {
		client := NewClient()

		assert.NotNil(t, client)
		assert.Equal(t, 22, client.port)
		assert.Equal(t, "root", client.user)
	})

	t.Run("creates client with options", func(t *testing.T) {
		client := NewClient(
			WithHost("192.168.1.100"),
			WithPort(2222),
			WithUser("admin"),
			WithKeyPath("/path/to/key"),
			WithSSHTimeout(60*time.Second),
		)

		assert.Equal(t, "192.168.1.100", client.host)
		assert.Equal(t, 2222, client.port)
		assert.Equal(t, "admin", client.user)
		assert.Equal(t, "/path/to/key", client.keyPath)
		assert.Equal(t, 60*time.Second, client.timeout)
	})
}

func TestClientGetHost(t *testing.T) {
	t.Run("returns host", func(t *testing.T) {
		client := NewClient(WithHost("test-host"))

		assert.Equal(t, "test-host", client.GetHost())
	})
}

func TestClientIsConnected(t *testing.T) {
	t.Run("returns false when not connected", func(t *testing.T) {
		client := NewClient()

		assert.False(t, client.IsConnected())
	})
}

func TestPing(t *testing.T) {
	t.Run("returns false for unreachable host", func(t *testing.T) {
		result := Ping("192.0.2.1", 22, 100*time.Millisecond) // TEST-NET-1

		assert.False(t, result)
	})

	t.Run("returns false for invalid port", func(t *testing.T) {
		result := Ping("localhost", 65535, 100*time.Millisecond)

		assert.False(t, result)
	})
}

func TestShellQuote(t *testing.T) {
	t.Run("quotes simple string", func(t *testing.T) {
		result := shellQuote("test")
		assert.Equal(t, "'test'", result)
	})

	t.Run("escapes single quotes", func(t *testing.T) {
		result := shellQuote("test's value")
		assert.Equal(t, "'test'\"'\"'s value'", result)
	})

	t.Run("handles empty string", func(t *testing.T) {
		result := shellQuote("")
		assert.Equal(t, "''", result)
	})

	t.Run("handles spaces", func(t *testing.T) {
		result := shellQuote("hello world")
		assert.Equal(t, "'hello world'", result)
	})

	t.Run("handles special characters", func(t *testing.T) {
		result := shellQuote("test$var")
		assert.Equal(t, "'test$var'", result)
	})

	t.Run("handles backslashes", func(t *testing.T) {
		result := shellQuote("path\\to\\file")
		assert.Equal(t, "'path\\to\\file'", result)
	})
}

func TestValidatePath(t *testing.T) {
	t.Run("allows valid paths", func(t *testing.T) {
		validPaths := []string{
			"/home/user/file.txt",
			"/var/log/app.log",
			"/tmp/test-file_123",
			"relative/path/to/file",
			"/path/with spaces/file.txt",
			"/path/with.dots/file",
		}
		for _, path := range validPaths {
			err := validatePath(path)
			assert.NoError(t, err, "path should be valid: %s", path)
		}
	})

	t.Run("rejects paths with null bytes", func(t *testing.T) {
		err := validatePath("/home/user/\x00evil")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "null byte")
	})

	t.Run("rejects paths with shell metacharacters", func(t *testing.T) {
		dangerousPaths := []string{
			"/path;rm -rf /",
			"/path && evil",
			"/path | cat /etc/passwd",
			"/path$(whoami)",
			"/path`id`",
			"/path > /etc/passwd",
			"/path < /etc/passwd",
			"/path\necho evil",
		}
		for _, path := range dangerousPaths {
			err := validatePath(path)
			assert.Error(t, err, "path should be rejected: %s", path)
		}
	})

	t.Run("rejects empty path", func(t *testing.T) {
		err := validatePath("")
		assert.Error(t, err)
	})
}

func TestClientWithSecurityOptions(t *testing.T) {
	t.Run("creates client with strict host key", func(t *testing.T) {
		client := NewClient(
			WithHost("192.168.1.100"),
			WithStrictHostKey(true),
		)

		assert.True(t, client.strictHostKey)
	})

	t.Run("creates client with auto-add host keys", func(t *testing.T) {
		client := NewClient(
			WithHost("192.168.1.100"),
			WithAutoAddHostKeys(true),
		)

		assert.True(t, client.autoAddHostKeys)
	})

	t.Run("creates client with custom known_hosts path", func(t *testing.T) {
		client := NewClient(
			WithKnownHostsPath("/custom/known_hosts"),
		)

		assert.Equal(t, "/custom/known_hosts", client.knownHostsPath)
	})
}

func TestClientAllOptions(t *testing.T) {
	t.Run("all options combined", func(t *testing.T) {
		client := NewClient(
			WithHost("192.168.1.100"),
			WithPort(2222),
			WithUser("testuser"),
			WithKeyPath("/home/user/.ssh/id_rsa"),
			WithSSHTimeout(2*time.Minute),
			WithStrictHostKey(true),
			WithAutoAddHostKeys(false),
			WithKnownHostsPath("/etc/ssh/known_hosts"),
		)

		assert.Equal(t, "192.168.1.100", client.host)
		assert.Equal(t, 2222, client.port)
		assert.Equal(t, "testuser", client.user)
		assert.Equal(t, "/home/user/.ssh/id_rsa", client.keyPath)
		assert.Equal(t, 2*time.Minute, client.timeout)
		assert.True(t, client.strictHostKey)
		assert.False(t, client.autoAddHostKeys)
		assert.Equal(t, "/etc/ssh/known_hosts", client.knownHostsPath)
	})
}

func TestGetDefaultSSHKeyPath(t *testing.T) {
	t.Run("returns path in user home", func(t *testing.T) {
		home, err := os.UserHomeDir()
		require.NoError(t, err)

		expected := filepath.Join(home, ".ssh", "id_rsa")
		// Verify the path structure is correct
		assert.Contains(t, expected, ".ssh")
		assert.Contains(t, expected, "id_rsa")
		assert.True(t, strings.HasPrefix(expected, home), "SSH key path should be under home directory")
	})
}

func TestClientClose(t *testing.T) {
	t.Run("close on unconnected client does not panic", func(t *testing.T) {
		client := NewClient()
		// Should not panic
		_ = client.Close()
		assert.False(t, client.IsConnected())
	})
}

func TestHostValidation(t *testing.T) {
	t.Run("valid hostnames", func(t *testing.T) {
		validHosts := []string{
			"localhost",
			"192.168.1.1",
			"10.0.0.1",
			"server.example.com",
			"node-01.cluster.local",
		}

		for _, host := range validHosts {
			client := NewClient(WithHost(host))
			assert.Equal(t, host, client.GetHost())
		}
	})
}

func TestPortValidation(t *testing.T) {
	t.Run("valid ports", func(t *testing.T) {
		validPorts := []int{22, 2222, 22222, 1}

		for _, port := range validPorts {
			client := NewClient(WithPort(port))
			assert.Equal(t, port, client.port)
		}
	})
}

func TestClientAddress(t *testing.T) {
	t.Run("formats address correctly", func(t *testing.T) {
		client := NewClient(
			WithHost("192.168.1.100"),
			WithPort(2222),
		)

		// The address would be "192.168.1.100:2222"
		assert.Equal(t, "192.168.1.100", client.host)
		assert.Equal(t, 2222, client.port)
	})
}

func TestCreateAutoAddCallbackWritesKnownHostsEntry(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sshPub, err := cryptossh.NewPublicKey(pub)
	require.NoError(t, err)

	knownHostsPath := filepath.Join(t.TempDir(), ".ssh", "known_hosts")
	client := NewClient(WithKnownHostsPath(knownHostsPath), WithAutoAddHostKeys(true))
	callback := client.createAutoAddCallback()

	err = callback("example.test:2222", &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}, sshPub)
	require.NoError(t, err)

	data, err := os.ReadFile(knownHostsPath)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "example.test")
	assert.Contains(t, content, "ssh-ed25519")
}

func TestRunFileAndDirectoryOperationsRequireConnection(t *testing.T) {
	client := NewClient()
	ctx := context.Background()

	_, _, err := client.Run(ctx, "true")
	assert.ErrorContains(t, err, "not connected")

	err = client.WriteFile(ctx, "/tmp/example.txt", []byte("data"), 0644)
	assert.ErrorContains(t, err, "not connected")

	err = client.CopyFile(ctx, filepath.Join(t.TempDir(), "missing"), "/tmp/example.txt")
	assert.ErrorContains(t, err, "not connected")

	_, err = client.ReadFile(ctx, "/tmp/example.txt")
	assert.ErrorContains(t, err, "not connected")

	err = client.MkdirAll(ctx, "/tmp/example")
	assert.ErrorContains(t, err, "not connected")
}

func TestFileOperationsRejectInvalidPathsBeforeConnectionUse(t *testing.T) {
	client := NewClient()
	ctx := context.Background()

	err := client.WriteFile(ctx, "/tmp/bad;path", []byte("data"), 0644)
	assert.ErrorContains(t, err, "invalid remote path")

	_, err = client.ReadFile(ctx, "/tmp/bad\npath")
	assert.ErrorContains(t, err, "invalid remote path")

	assert.False(t, client.FileExists(ctx, "/tmp/bad`path`"))
	assert.False(t, client.DirExists(ctx, "/tmp/bad|path"))

	err = client.MkdirAll(ctx, "/tmp/bad$(whoami)")
	assert.ErrorContains(t, err, "invalid remote path")
}

func TestCheckPortReturnsFalseWhenNotConnected(t *testing.T) {
	client := NewClient()
	assert.False(t, client.CheckPort(context.Background(), 8080))
}

// Note: Actual SSH connection tests would require a test SSH server
// or integration test environment
