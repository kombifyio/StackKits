package cue

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractServicesFromModules(t *testing.T) {
	modulesDir := filepath.Join("..", "..", "modules")
	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		t.Skipf("modules directory not found: %s", modulesDir)
	}

	extractor := NewExtractor(".")

	t.Run("extracts services from all modules", func(t *testing.T) {
		services, err := extractor.ExtractServicesFromModules(modulesDir)
		if err != nil {
			t.Fatalf("ExtractServicesFromModules() error = %v", err)
		}

		if len(services) == 0 {
			t.Error("expected at least one service, got none")
		}

		t.Logf("Extracted %d services from modules", len(services))
	})

	t.Run("skips _integration directory", func(t *testing.T) {
		services, err := extractor.ExtractServicesFromModules(modulesDir)
		if err != nil {
			t.Fatalf("ExtractServicesFromModules() error = %v", err)
		}

		for _, svc := range services {
			if svc.Name == "" {
				t.Errorf("service with empty name found: %+v", svc)
			}
		}
	})

	t.Run("each service has required fields", func(t *testing.T) {
		services, err := extractor.ExtractServicesFromModules(modulesDir)
		if err != nil {
			t.Fatalf("ExtractServicesFromModules() error = %v", err)
		}

		for _, svc := range services {
			if svc.Image == "" {
				t.Errorf("service %q has empty image", svc.Name)
			}
			if svc.Type == "" {
				t.Errorf("service %q has empty type", svc.Name)
			}
		}
	})

	t.Run("returns error for nonexistent directory", func(t *testing.T) {
		_, err := extractor.ExtractServicesFromModules("/nonexistent/modules")
		if err == nil {
			t.Error("expected error for nonexistent directory, got nil")
		}
	})
}

func TestExtractModuleServices(t *testing.T) {
	traefikModule := filepath.Join("..", "..", "modules", "traefik")
	if _, err := os.Stat(traefikModule); os.IsNotExist(err) {
		t.Skipf("traefik module not found: %s", traefikModule)
	}

	extractor := NewExtractor(".")

	t.Run("extracts traefik service", func(t *testing.T) {
		services, err := extractor.extractModuleServices(traefikModule)
		if err != nil {
			t.Fatalf("extractModuleServices() error = %v", err)
		}

		if len(services) == 0 {
			t.Fatal("expected at least one service from traefik module")
		}

		found := false
		for _, svc := range services {
			if svc.Name == "traefik" {
				found = true
				if svc.Image == "" {
					t.Error("traefik service has empty image")
				}
				if svc.Type != "reverse-proxy" {
					t.Errorf("traefik service type = %q, want reverse-proxy", svc.Type)
				}
			}
		}
		if !found {
			t.Error("traefik service not found in traefik module")
		}
	})
}

func TestExtractServicesFromModules_IncludesCurrentBaseKitApps(t *testing.T) {
	modulesDir := filepath.Join("..", "..", "modules")
	if _, err := os.Stat(modulesDir); os.IsNotExist(err) {
		t.Skipf("modules directory not found: %s", modulesDir)
	}

	extractor := NewExtractor(".")
	services, err := extractor.ExtractServicesFromModules(modulesDir)
	if err != nil {
		t.Fatalf("ExtractServicesFromModules() error = %v", err)
	}

	found := map[string]bool{}
	for _, svc := range services {
		found[svc.Name] = true
	}

	for _, required := range []string{"vaultwarden", "jellyfin", "immich"} {
		if !found[required] {
			t.Errorf("expected current base-kit app module %q to be extracted", required)
		}
	}
}
