package kombifyme

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	systemKeyPath = "/etc/kombify/api-key"
	keyDirName    = ".kombify"
	keyFileName   = "api-key"
)

// LoadAPIKey loads the API key from the first available source:
// 1. KOMBIFY_API_KEY environment variable
// 2. /etc/kombify/api-key (system-wide, root installs)
// 3. ~/.kombify/api-key (user-level)
func LoadAPIKey() (string, error) {
	// 1. Environment variable
	if key := os.Getenv("KOMBIFY_API_KEY"); key != "" {
		return key, nil
	}

	// 2. System-wide key (Linux only)
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile(systemKeyPath); err == nil {
			key := strings.TrimSpace(string(data))
			if key != "" {
				return key, nil
			}
		}
	}

	// 3. User-level key
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("no API key found (checked KOMBIFY_API_KEY env, %s)", systemKeyPath)
	}
	userKeyPath := filepath.Join(home, keyDirName, keyFileName)
	if data, err := os.ReadFile(userKeyPath); err == nil {
		key := strings.TrimSpace(string(data))
		if key != "" {
			return key, nil
		}
	}

	return "", fmt.Errorf("no API key found (checked KOMBIFY_API_KEY env, %s, %s)", systemKeyPath, userKeyPath)
}

// SaveAPIKey persists the API key to disk.
// If running as root on Linux, saves to /etc/kombify/api-key.
// Otherwise saves to ~/.kombify/api-key.
func SaveAPIKey(apiKey string) (string, error) {
	var keyPath string

	if runtime.GOOS == "linux" && os.Getuid() == 0 {
		// Root: save system-wide
		dir := filepath.Dir(systemKeyPath)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", fmt.Errorf("create %s: %w", dir, err)
		}
		keyPath = systemKeyPath
	} else {
		// User-level
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		dir := filepath.Join(home, keyDirName)
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", fmt.Errorf("create %s: %w", dir, err)
		}
		keyPath = filepath.Join(dir, keyFileName)
	}

	if err := os.WriteFile(keyPath, []byte(apiKey+"\n"), 0600); err != nil {
		return keyPath, fmt.Errorf("write API key to %s: %w", keyPath, err)
	}

	return keyPath, nil
}
