package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/kombifyio/stackkits/internal/netenv"
	"github.com/kombifyio/stackkits/pkg/models"
)

// loadDockerCapabilities reads the host observation written by the legacy
// preparation surface. Native callers use it only as optional observed input;
// it is never deployment intent or provider authority.
func loadDockerCapabilities() *models.DockerCapabilities {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(home, ".stackkits", "capabilities.json"))
	if err != nil {
		return nil
	}
	var caps models.DockerCapabilities
	if err := json.Unmarshal(data, &caps); err != nil {
		return nil
	}
	return &caps
}

// resolveNodeContextFromCaps resolves a local context from caller-supplied
// intent first and otherwise from optional observed host capabilities.
func resolveNodeContextFromCaps(caps *models.DockerCapabilities, spec *models.StackSpec) models.NodeContext {
	if spec.Context != "" {
		return models.NodeContext(spec.Context)
	}
	if caps != nil && caps.ResolvedContext != "" {
		return caps.ResolvedContext
	}

	detected := netenv.Detect(context.Background())
	if caps == nil {
		caps = &models.DockerCapabilities{}
	}
	caps.NetworkEnv = detected.Environment
	caps.PublicIP = detected.PublicIP
	caps.PrivateIP = detected.PrivateIP
	caps.IsNAT = detected.IsNAT
	caps.HasPublicInterface = detected.HasPublicInterface

	resolved := netenv.ResolveFromResult(detected, caps.CPUCores, caps.MemoryGB)
	caps.ResolvedContext = resolved
	return resolved
}
