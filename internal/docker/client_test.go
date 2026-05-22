// Package docker tests
package docker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/stretchr/testify/assert"
)

func TestClient(t *testing.T) {
	t.Run("creates client", func(t *testing.T) {
		client := NewClient()
		assert.NotNil(t, client)
	})
}

func TestContainerInfo(t *testing.T) {
	t.Run("creates container info", func(t *testing.T) {
		info := ContainerInfo{
			ID:    "abc123def456",
			Name:  "traefik",
			Image: "traefik:v3.0",
			State: ContainerState{
				Status:  "Up 2 hours",
				Running: true,
			},
			Labels: map[string]string{
				"managed-by": "stackkit",
			},
		}

		assert.Equal(t, "abc123def456", info.ID)
		assert.Equal(t, "traefik", info.Name)
		assert.True(t, info.State.Running)
	})
}

func TestContainerState(t *testing.T) {
	t.Run("running state", func(t *testing.T) {
		state := ContainerState{
			Status:  "Up 2 hours",
			Running: true,
			Paused:  false,
		}

		assert.True(t, state.Running)
		assert.False(t, state.Paused)
	})

	t.Run("healthy container", func(t *testing.T) {
		state := ContainerState{
			Running: true,
			Health: &HealthState{
				Status: "healthy",
			},
		}

		assert.NotNil(t, state.Health)
		assert.Equal(t, "healthy", state.Health.Status)
	})
}

func TestGetServiceStatus(t *testing.T) {
	t.Run("running container", func(t *testing.T) {
		container := &ContainerInfo{
			State: ContainerState{
				Running: true,
			},
		}

		status := GetServiceStatus(container)

		assert.Equal(t, models.ServiceStatusRunning, status)
	})

	t.Run("stopped container", func(t *testing.T) {
		container := &ContainerInfo{
			State: ContainerState{
				Running: false,
			},
		}

		status := GetServiceStatus(container)

		assert.Equal(t, models.ServiceStatusStopped, status)
	})

	t.Run("restarting container", func(t *testing.T) {
		container := &ContainerInfo{
			State: ContainerState{
				Running:    false,
				Restarting: true,
			},
		}

		status := GetServiceStatus(container)

		assert.Equal(t, models.ServiceStatusStarting, status)
	})

	t.Run("nil container", func(t *testing.T) {
		status := GetServiceStatus(nil)

		assert.Equal(t, models.ServiceStatusUnknown, status)
	})
}

func TestContainerPort(t *testing.T) {
	t.Run("creates port mapping", func(t *testing.T) {
		port := ContainerPort{
			PrivatePort: 80,
			PublicPort:  8080,
			Type:        "tcp",
		}

		assert.Equal(t, 80, port.PrivatePort)
		assert.Equal(t, 8080, port.PublicPort)
		assert.Equal(t, "tcp", port.Type)
	})
}

func TestParseDockerPSOutput(t *testing.T) {
	output := []byte("abc123\tstackkit-hub\tghcr.io/kombifyio/stackkit-server:latest\trunning\tUp 2 minutes (healthy)\t8080/tcp\nstopped1\told\tnginx:alpine\texited\tExited (0) 1 hour ago\t\n")

	containers := parseDockerPSOutput(output)

	assert.Len(t, containers, 2)
	assert.Equal(t, "abc123", containers[0].ID)
	assert.Equal(t, "stackkit-hub", containers[0].Name)
	assert.Equal(t, "ghcr.io/kombifyio/stackkit-server:latest", containers[0].Image)
	assert.True(t, containers[0].State.Running)
	assert.Equal(t, "Up 2 minutes (healthy)", containers[0].State.Status)
	assert.False(t, containers[1].State.Running)
}

// Integration tests that require Docker
func TestClientIntegration(t *testing.T) {
	client := NewClient()

	// Skip if Docker is not installed
	if !client.IsInstalled() {
		t.Skip("Docker not installed, skipping integration tests")
	}

	ctx := context.Background()

	t.Run("checks if running", func(t *testing.T) {
		// Just verify it doesn't panic
		_ = client.IsRunning(ctx)
	})

	t.Run("gets version", func(t *testing.T) {
		if !client.IsRunning(ctx) {
			t.Skip("Docker not running")
		}

		version, err := client.Version(ctx)

		assert.NoError(t, err)
		assert.NotEmpty(t, version)
	})

	t.Run("lists containers", func(t *testing.T) {
		if !client.IsRunning(ctx) {
			t.Skip("Docker not running")
		}

		containers, err := client.ListContainers(ctx, false)

		assert.NoError(t, err)
		// May or may not have containers
		_ = containers
	})
}

func TestValidateName(t *testing.T) {
	t.Run("allows valid names", func(t *testing.T) {
		validNames := []string{
			"traefik",
			"my-container",
			"app_1",
			"container.service",
			"A123",
		}
		for _, name := range validNames {
			err := validateName(name)
			assert.NoError(t, err, "name should be valid: %s", name)
		}
	})

	t.Run("rejects empty name", func(t *testing.T) {
		err := validateName("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "cannot be empty")
	})

	t.Run("rejects invalid characters", func(t *testing.T) {
		invalidNames := []string{
			"container;rm -rf",
			"name && evil",
			"test|cat",
			"$(whoami)",
			"path/name",
			" leading-space",
		}
		for _, name := range invalidNames {
			err := validateName(name)
			assert.Error(t, err, "name should be invalid: %s", name)
		}
	})

	t.Run("rejects too long names", func(t *testing.T) {
		longName := ""
		for i := 0; i < 260; i++ {
			longName += "a"
		}
		err := validateName(longName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too long")
	})
}

func TestValidateNameOrID(t *testing.T) {
	t.Run("allows valid container IDs", func(t *testing.T) {
		validIDs := []string{
			"abc123def456", // short ID
			"abc123def456789012345678901234567890123456789012345678901234567890", // full 64-char ID
		}
		for _, id := range validIDs {
			err := validateNameOrID(id)
			assert.NoError(t, err, "ID should be valid: %s", id)
		}
	})

	t.Run("allows valid names", func(t *testing.T) {
		err := validateNameOrID("my-container")
		assert.NoError(t, err)
	})
}

func TestValidateImageName(t *testing.T) {
	t.Run("allows valid image names", func(t *testing.T) {
		validImages := []string{
			"nginx",
			"nginx:latest",
			"library/nginx:1.25",
			"ghcr.io/owner/repo:v1.0.0",
			"gcr.io/project/image:sha-abc123",
			"docker.io/library/alpine:3.18",
		}
		for _, image := range validImages {
			err := validateImageName(image)
			assert.NoError(t, err, "image should be valid: %s", image)
		}
	})

	t.Run("rejects empty image name", func(t *testing.T) {
		err := validateImageName("")
		assert.Error(t, err)
	})

	t.Run("rejects invalid image names", func(t *testing.T) {
		invalidImages := []string{
			"image;rm -rf /",
			"image && evil",
		}
		for _, image := range invalidImages {
			err := validateImageName(image)
			assert.Error(t, err, "image should be invalid: %s", image)
		}
	})
}

func TestClientOptions(t *testing.T) {
	t.Run("sets custom binary", func(t *testing.T) {
		client := NewClient(WithBinary("podman"))
		assert.Equal(t, "podman", client.binary)
	})

	t.Run("sets custom timeout", func(t *testing.T) {
		client := NewClient(WithTimeout(60 * 1000000000)) // 60 seconds
		assert.Equal(t, 60*1000000000, int(client.timeout))
	})
}

func TestContainerInfoFull(t *testing.T) {
	t.Run("full container info", func(t *testing.T) {
		info := ContainerInfo{
			ID:      "abcdef123456",
			Name:    "test-container",
			Image:   "nginx:alpine",
			Created: "2024-01-01T00:00:00Z",
			Ports: []ContainerPort{
				{PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
				{PrivatePort: 443, PublicPort: 8443, Type: "tcp"},
			},
			State: ContainerState{
				Status:     "Up 5 minutes",
				Running:    true,
				Paused:     false,
				Restarting: false,
				Health: &HealthState{
					Status: "healthy",
				},
			},
			Labels: map[string]string{
				"managed-by":     "stackkit",
				"stackkit.name":  "web",
				"traefik.enable": "true",
			},
		}

		assert.Equal(t, "abcdef123456", info.ID)
		assert.Equal(t, "test-container", info.Name)
		assert.Equal(t, "nginx:alpine", info.Image)
		assert.Len(t, info.Ports, 2)
		assert.True(t, info.State.Running)
		assert.Equal(t, "healthy", info.State.Health.Status)
		assert.Contains(t, info.Labels, "managed-by")
	})
}

func TestHealthState(t *testing.T) {
	t.Run("healthy status", func(t *testing.T) {
		health := &HealthState{
			Status: "healthy",
		}
		assert.Equal(t, "healthy", health.Status)
	})

	t.Run("unhealthy status", func(t *testing.T) {
		health := &HealthState{
			Status: "unhealthy",
		}
		assert.Equal(t, "unhealthy", health.Status)
	})

	t.Run("starting status", func(t *testing.T) {
		health := &HealthState{
			Status: "starting",
		}
		assert.Equal(t, "starting", health.Status)
	})
}

func TestNetworkInfo(t *testing.T) {
	// NetworkInfo is not currently exported in the client
	// This test validates ContainerPort which is available
	t.Run("container port info", func(t *testing.T) {
		port := ContainerPort{
			PrivatePort: 80,
			PublicPort:  8080,
			Type:        "tcp",
		}

		assert.Equal(t, 80, port.PrivatePort)
		assert.Equal(t, 8080, port.PublicPort)
		assert.Equal(t, "tcp", port.Type)
	})
}

func TestGetServiceStatusWithHealth(t *testing.T) {
	t.Run("healthy container", func(t *testing.T) {
		container := &ContainerInfo{
			State: ContainerState{
				Running: true,
				Health: &HealthState{
					Status: "healthy",
				},
			},
		}

		status := GetServiceStatus(container)
		assert.Equal(t, models.ServiceStatusRunning, status)
	})

	t.Run("unhealthy container", func(t *testing.T) {
		container := &ContainerInfo{
			State: ContainerState{
				Running: true,
				Health: &HealthState{
					Status: "unhealthy",
				},
			},
		}

		// Still running but health check failing
		status := GetServiceStatus(container)
		assert.Equal(t, models.ServiceStatusRunning, status)
	})

	t.Run("paused container", func(t *testing.T) {
		container := &ContainerInfo{
			State: ContainerState{
				Running: true,
				Paused:  true,
			},
		}

		status := GetServiceStatus(container)
		// Paused containers are still technically running
		assert.Equal(t, models.ServiceStatusRunning, status)
	})
}

func TestValidateImageNameAdvanced(t *testing.T) {
	t.Run("image with digest", func(t *testing.T) {
		err := validateImageName("nginx@sha256:abc123def456")
		assert.NoError(t, err)
	})

	t.Run("private registry", func(t *testing.T) {
		err := validateImageName("my-registry.example.com:5000/my-image:v1.0")
		assert.NoError(t, err)
	})

	t.Run("image with port", func(t *testing.T) {
		err := validateImageName("localhost:5000/myimage")
		assert.NoError(t, err)
	})
}

func TestIsInstalled(t *testing.T) {
	t.Run("checks binary availability", func(t *testing.T) {
		// This will return true if Docker is installed, false otherwise
		client := NewClient()
		_ = client.IsInstalled()
	})

	t.Run("custom binary not found", func(t *testing.T) {
		client := NewClient(WithBinary("nonexistent-docker-binary-xyz"))
		assert.False(t, client.IsInstalled())
	})
}

func TestListContainersParsesDockerPSRowsAndSkipsMalformedRows(t *testing.T) {
	client := NewClient(WithBinary(fakeDockerBinary(t,
		`@echo off
echo abc123	web	nginx:alpine	running	Up 1 second	80/tcp
echo malformed-row
exit /b 0
`,
		`#!/bin/sh
printf '%s\n' 'abc123	web	nginx:alpine	running	Up 1 second	80/tcp'
printf '%s\n' 'malformed-row'
`)))

	containers, err := client.ListContainers(context.Background(), true)
	assert.NoError(t, err)
	assert.Len(t, containers, 1)
	assert.Equal(t, "web", containers[0].Name)
	assert.Equal(t, "nginx:alpine", containers[0].Image)
	assert.True(t, containers[0].State.Running)
}

func TestGetStackKitContainersReturnsEmptyOnDockerError(t *testing.T) {
	client := NewClient(WithBinary(fakeDockerBinary(t,
		`@echo off
echo docker failed 1>&2
exit /b 1
`,
		`#!/bin/sh
echo docker failed >&2
exit 1
`)))

	containers, err := client.GetStackKitContainers(context.Background())
	assert.NoError(t, err)
	assert.Empty(t, containers)
}

func TestRemoveResourceTreatsNotFoundAsSuccess(t *testing.T) {
	client := NewClient(WithBinary(fakeDockerBinary(t,
		`@echo off
echo No such container: missing
exit /b 1
`,
		`#!/bin/sh
echo 'No such container: missing'
exit 1
`)))

	err := client.removeResource(context.Background(), []string{"rm", "-f", "missing"}, "container", "missing")
	assert.NoError(t, err)
}

func TestListByLabelParsesNames(t *testing.T) {
	client := NewClient(WithBinary(fakeDockerBinary(t,
		`@echo off
echo frontend
echo backend
exit /b 0
`,
		`#!/bin/sh
printf '%s\n' frontend backend
`)))

	names, err := client.ListNetworksByLabel(context.Background(), "stackkit.layer")
	assert.NoError(t, err)
	assert.Equal(t, []string{"frontend", "backend"}, names)
}

func TestParseReclaimedSpace(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want int64
	}{
		{"gigabytes", "Total reclaimed space: 1.5GB", int64(1.5 * 1024 * 1024 * 1024)},
		{"megabytes", "Total reclaimed space: 12MB", 12 * 1024 * 1024},
		{"kilobytes", "Total reclaimed space: 7kB", 7 * 1024},
		{"bytes", "Total reclaimed space: 42B", 42},
		{"missing", "nothing reclaimed", 0},
		{"invalid", "Total reclaimed space: nope", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseReclaimedSpace(tt.out))
		})
	}
}

func TestRemoveMethodsRejectInvalidNamesBeforeExecuting(t *testing.T) {
	client := NewClient(WithBinary("docker"))
	assert.Error(t, client.RemoveContainer(context.Background(), "bad;name"))
	assert.Error(t, client.RemoveNetwork(context.Background(), "bad/name"))
	assert.Error(t, client.RemoveVolume(context.Background(), ""))
	assert.Error(t, client.RemoveImage(context.Background(), "bad && image"))
}

func fakeDockerBinary(t *testing.T, windowsScript, unixScript string) string {
	t.Helper()

	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "docker.cmd")
		if !strings.HasSuffix(windowsScript, "\r\n") {
			windowsScript += "\r\n"
		}
		if err := os.WriteFile(path, []byte(windowsScript), 0700); err != nil {
			t.Fatalf("write fake docker: %v", err)
		}
		return path
	}

	path := filepath.Join(dir, "docker")
	if !strings.HasSuffix(unixScript, "\n") {
		unixScript += "\n"
	}
	if err := os.WriteFile(path, []byte(unixScript), 0700); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
	return path
}
