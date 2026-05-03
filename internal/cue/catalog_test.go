package cue

import (
	"path/filepath"
	"testing"
)

func TestServiceCatalogFromModulesUsesModuleMetadataFallbacks(t *testing.T) {
	catalog, err := ServiceCatalogFromModules(filepath.Join("..", "..", "modules"))
	if err != nil {
		t.Fatalf("ServiceCatalogFromModules() error = %v", err)
	}

	entries := map[string]CatalogEntry{}
	for _, entry := range catalog {
		entries[entry.Key] = entry
	}

	tests := []struct {
		key         string
		displayName string
		description string
	}{
		{
			key:         "auth",
			displayName: "TinyAuth",
			description: "Lightweight authentication proxy with ForwardAuth, passkeys, and OAuth support",
		},
		{
			key:         "kuma",
			displayName: "Uptime Kuma",
			description: "Self-hosted uptime monitoring with status pages and notifications",
		},
		{
			key:         "dokploy",
			displayName: "Dokploy",
			description: "Self-hosted PaaS with PostgreSQL and Redis for deploying applications",
		},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			entry, ok := entries[tt.key]
			if !ok {
				t.Fatalf("catalog entry %q missing", tt.key)
			}
			if entry.DisplayName != tt.displayName {
				t.Fatalf("DisplayName = %q, want %q", entry.DisplayName, tt.displayName)
			}
			if entry.Description != tt.description {
				t.Fatalf("Description = %q, want %q", entry.Description, tt.description)
			}
		})
	}
}

func TestDomainEntriesFromModulesUsesMetadataForDashboard(t *testing.T) {
	entries, err := DomainEntriesFromModules(filepath.Join("..", "..", "modules"))
	if err != nil {
		t.Fatalf("DomainEntriesFromModules() error = %v", err)
	}

	for _, entry := range entries {
		if entry.Key != "dashboard" {
			continue
		}
		if entry.DisplayName != "Dashboard" {
			t.Fatalf("dashboard DisplayName = %q, want Dashboard", entry.DisplayName)
		}
		if entry.Description == "" {
			t.Fatal("dashboard Description is empty")
		}
		return
	}

	t.Fatal("dashboard domain entry missing")
}
