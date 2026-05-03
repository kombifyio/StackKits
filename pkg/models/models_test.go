// Package models tests
package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStackKitMetadata(t *testing.T) {
	t.Run("creates valid metadata", func(t *testing.T) {
		meta := StackKitMetadata{
			APIVersion:  "stackkit/v1",
			Kind:        "StackKit",
			Name:        "test-stackkit",
			Version:     "1.0.0",
			DisplayName: "Test StackKit",
			Description: "A test stackkit for unit testing",
			License:     "MIT",
		}

		assert.Equal(t, "stackkit/v1", meta.APIVersion)
		assert.Equal(t, "test-stackkit", meta.Name)
		assert.Equal(t, "1.0.0", meta.Version)
		assert.Equal(t, "MIT", meta.License)
	})

	t.Run("handles optional fields", func(t *testing.T) {
		meta := StackKitMetadata{
			Name:    "minimal",
			Version: "0.1.0",
			License: "Apache-2.0",
		}

		assert.Empty(t, meta.Author)
		assert.Empty(t, meta.Homepage)
		assert.Empty(t, meta.Repository)
		assert.Nil(t, meta.Tags)
	})
}

func TestStackSpec(t *testing.T) {
	t.Run("creates valid spec", func(t *testing.T) {
		spec := StackSpec{
			Name:     "my-homelab",
			StackKit: "base-kit",
			Mode:     "simple",
			Domain:   "homelab.local",
			Email:    "admin@example.com",
			Network: NetworkSpec{
				Mode:   "local",
				Subnet: "172.20.0.0/16",
			},
			Compute: ComputeSpec{
				Tier: "standard",
			},
		}

		assert.Equal(t, "my-homelab", spec.Name)
		assert.Equal(t, "base-kit", spec.StackKit)
		assert.Equal(t, "local", spec.Network.Mode)
		assert.Equal(t, "172.20.0.0/16", spec.Network.Subnet)
	})

	t.Run("supports multi-node configuration", func(t *testing.T) {
		spec := StackSpec{
			Name:     "ha-kit",
			StackKit: "ha-kit",
			Nodes: []NodeSpec{
				{Name: "control-1", Role: "control-plane", IP: "192.168.1.10"},
				{Name: "worker-1", Role: "worker", IP: "192.168.1.11"},
				{Name: "worker-2", Role: "worker", IP: "192.168.1.12"},
			},
		}

		assert.Len(t, spec.Nodes, 3)
		assert.Equal(t, "control-plane", spec.Nodes[0].Role)
		assert.Equal(t, "worker", spec.Nodes[1].Role)
	})

	t.Run("supports service configuration", func(t *testing.T) {
		spec := StackSpec{
			Name:     "custom",
			StackKit: "base-kit",
			Services: map[string]any{
				"traefik": map[string]any{
					"dashboard": true,
				},
			},
		}

		require.NotNil(t, spec.Services["traefik"])
	})
}

func TestDeploymentState(t *testing.T) {
	t.Run("creates valid deployment state", func(t *testing.T) {
		state := DeploymentState{
			StackKit:    "base-kit",
			Mode:        "simple",
			Status:      StatusRunning,
			LastApplied: time.Now(),
			Services: []ServiceState{
				{
					Name:      "traefik",
					Status:    ServiceStatusRunning,
					Container: "traefik-123",
					Health:    HealthStatusHealthy,
				},
			},
		}

		assert.Equal(t, StatusRunning, state.Status)
		assert.Len(t, state.Services, 1)
		assert.Equal(t, ServiceStatusRunning, state.Services[0].Status)
	})

	t.Run("status constants are correct", func(t *testing.T) {
		assert.Equal(t, DeploymentStatus("pending"), StatusPending)
		assert.Equal(t, DeploymentStatus("running"), StatusRunning)
		assert.Equal(t, DeploymentStatus("degraded"), StatusDegraded)
		assert.Equal(t, DeploymentStatus("error"), StatusError)
		assert.Equal(t, DeploymentStatus("removed"), StatusRemoved)
	})
}

func TestValidationResult(t *testing.T) {
	t.Run("creates valid result", func(t *testing.T) {
		result := ValidationResult{
			Valid: true,
		}

		assert.True(t, result.Valid)
		assert.Empty(t, result.Errors)
		assert.Empty(t, result.Warnings)
	})

	t.Run("handles errors", func(t *testing.T) {
		result := ValidationResult{
			Valid: false,
			Errors: []ValidationError{
				{Path: "name", Message: "name is required", Code: "REQUIRED_FIELD"},
				{Path: "stackkit", Message: "stackkit is required", Code: "REQUIRED_FIELD"},
			},
		}

		assert.False(t, result.Valid)
		assert.Len(t, result.Errors, 2)
		assert.Equal(t, "name", result.Errors[0].Path)
	})

	t.Run("handles warnings", func(t *testing.T) {
		result := ValidationResult{
			Valid: true,
			Warnings: []ValidationError{
				{Path: "domain", Message: "domain recommended for public mode"},
			},
		}

		assert.True(t, result.Valid)
		assert.Len(t, result.Warnings, 1)
	})
}

func TestSystemInfo(t *testing.T) {
	t.Run("creates valid system info", func(t *testing.T) {
		info := SystemInfo{
			Hostname:      "homelab-01",
			OS:            "ubuntu",
			OSVersion:     "24.04",
			Arch:          "x86_64",
			CPUCores:      8,
			MemoryMB:      16384,
			DiskGB:        500,
			DockerVersion: "27.3.1",
			TofuVersion:   "1.6.2",
		}

		assert.Equal(t, "ubuntu", info.OS)
		assert.Equal(t, "24.04", info.OSVersion)
		assert.Equal(t, 8, info.CPUCores)
		assert.Equal(t, 16384, info.MemoryMB)
	})

	t.Run("handles missing optional tools", func(t *testing.T) {
		info := SystemInfo{
			Hostname:  "bare-metal",
			OS:        "debian",
			OSVersion: "12",
		}

		assert.Empty(t, info.DockerVersion)
		assert.Empty(t, info.TofuVersion)
	})
}

func TestRequirements(t *testing.T) {
	t.Run("defines resource requirements", func(t *testing.T) {
		req := Requirements{
			Minimum: ResourceSpec{
				CPU:  2,
				RAM:  4,
				Disk: 20,
			},
			Recommended: ResourceSpec{
				CPU:  4,
				RAM:  8,
				Disk: 50,
			},
		}

		assert.Equal(t, 2, req.Minimum.CPU)
		assert.Equal(t, 4, req.Minimum.RAM)
		assert.Greater(t, req.Recommended.CPU, req.Minimum.CPU)
		assert.Greater(t, req.Recommended.RAM, req.Minimum.RAM)
	})
}

func TestNetworkSpec(t *testing.T) {
	t.Run("local network mode", func(t *testing.T) {
		net := NetworkSpec{
			Mode:    "local",
			Subnet:  "172.20.0.0/16",
			Gateway: "172.20.0.1",
		}

		assert.Equal(t, "local", net.Mode)
	})

	t.Run("public network mode", func(t *testing.T) {
		net := NetworkSpec{
			Mode: "public",
		}

		assert.Equal(t, "public", net.Mode)
	})

	t.Run("hybrid network mode", func(t *testing.T) {
		net := NetworkSpec{
			Mode: "hybrid",
		}

		assert.Equal(t, "hybrid", net.Mode)
	})
}

func TestSSHSpec(t *testing.T) {
	t.Run("default SSH config", func(t *testing.T) {
		ssh := SSHSpec{
			User: "root",
			Port: 22,
		}

		assert.Equal(t, "root", ssh.User)
		assert.Equal(t, 22, ssh.Port)
	})

	t.Run("custom SSH config", func(t *testing.T) {
		ssh := SSHSpec{
			User:    "admin",
			Port:    2222,
			KeyPath: "/home/user/.ssh/id_ed25519",
		}

		assert.Equal(t, "admin", ssh.User)
		assert.Equal(t, 2222, ssh.Port)
		assert.NotEmpty(t, ssh.KeyPath)
	})
}

func TestServiceStatus(t *testing.T) {
	t.Run("service status values", func(t *testing.T) {
		assert.Equal(t, ServiceStatus("running"), ServiceStatusRunning)
		assert.Equal(t, ServiceStatus("stopped"), ServiceStatusStopped)
		assert.Equal(t, ServiceStatus("starting"), ServiceStatusStarting)
		assert.Equal(t, ServiceStatus("error"), ServiceStatusError)
		assert.Equal(t, ServiceStatus("unknown"), ServiceStatusUnknown)
	})
}

func TestHealthStatus(t *testing.T) {
	t.Run("health status values", func(t *testing.T) {
		assert.Equal(t, HealthStatus("healthy"), HealthStatusHealthy)
		assert.Equal(t, HealthStatus("unhealthy"), HealthStatusUnhealthy)
		assert.Equal(t, HealthStatus("starting"), HealthStatusStarting)
		assert.Equal(t, HealthStatus("none"), HealthStatusNone)
	})
}

func TestFeatures(t *testing.T) {
	t.Run("feature flags", func(t *testing.T) {
		features := Features{
			MultiNode:    true,
			VPNOverlay:   true,
			PublicAccess: false,
		}

		assert.True(t, features.MultiNode)
		assert.True(t, features.VPNOverlay)
		assert.False(t, features.PublicAccess)
	})
}

func TestResolvePAASForContext(t *testing.T) {
	t.Run("legacy resolver still honors explicit paas", func(t *testing.T) {
		spec := &StackSpec{
			PAAS:    PAASCoolify,
			Compute: ComputeSpec{Tier: ComputeTierLow},
		}
		assert.Equal(t, PAASCoolify, spec.ResolvePAAS())
	})

	t.Run("explicit paas wins", func(t *testing.T) {
		spec := &StackSpec{PAAS: PAASCoolify}
		assert.Equal(t, PAASCoolify, spec.ResolvePAASForContext(ContextCloud))
	})

	t.Run("local standard defaults to dokploy", func(t *testing.T) {
		spec := &StackSpec{
			Compute: ComputeSpec{Tier: ComputeTierStandard},
		}
		assert.Equal(t, PAASDokploy, spec.ResolvePAASForContext(ContextLocal))
	})

	t.Run("local low tier defaults to dockge", func(t *testing.T) {
		spec := &StackSpec{
			Compute: ComputeSpec{Tier: ComputeTierLow},
		}
		assert.Equal(t, PAASDockge, spec.ResolvePAASForContext(ContextLocal))
	})

	t.Run("cloud without domain defaults to dokploy", func(t *testing.T) {
		spec := &StackSpec{}
		assert.Equal(t, PAASDokploy, spec.ResolvePAASForContext(ContextCloud))
	})

	t.Run("cloud with kombify.me defaults to dokploy", func(t *testing.T) {
		spec := &StackSpec{Domain: DomainKombifyMe}
		assert.Equal(t, PAASDokploy, spec.ResolvePAASForContext(ContextCloud))
	})

	t.Run("cloud with custom domain defaults to coolify", func(t *testing.T) {
		spec := &StackSpec{Domain: "apps.example.com"}
		assert.Equal(t, PAASCoolify, spec.ResolvePAASForContext(ContextCloud))
	})

	t.Run("cloud with local domain still defaults to dokploy", func(t *testing.T) {
		spec := &StackSpec{Domain: "lab.homebase"}
		assert.Equal(t, PAASDokploy, spec.ResolvePAASForContext(ContextCloud))
	})
}

func TestResolveReverseProxyForPAAS(t *testing.T) {
	assert.Equal(t, ReverseProxyStandalone, ResolveReverseProxyForPAAS(PAASDokploy))
	assert.Equal(t, ReverseProxyCoolify, ResolveReverseProxyForPAAS(PAASCoolify))
	assert.Equal(t, ReverseProxyStandalone, ResolveReverseProxyForPAAS(PAASDockge))
	assert.Equal(t, ReverseProxyStandalone, ResolveReverseProxyForPAAS(""))
}
