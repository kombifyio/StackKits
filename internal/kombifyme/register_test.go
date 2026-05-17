package kombifyme

import (
	"testing"

	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

func TestServiceRegistrationsFromCatalogUseCanonicalNamesAndLegacyAliases(t *testing.T) {
	registrations := ServiceRegistrationsFromCatalog(servicecatalog.Default(), "standard")
	byName := make(map[string]ServiceDef, len(registrations))
	for _, svc := range registrations {
		byName[svc.Name] = svc
	}

	for _, want := range []string{"base", "home", "auth", "id", "coolify", "kuma", "whoami", "vault", "photos"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("missing canonical service registration %q; got %v", want, registrationNames(registrations))
		}
	}
	for _, notWant := range []string{"dokploy", "media", "traefik"} {
		if _, ok := byName[notWant]; ok {
			t.Fatalf("unexpected default service registration %q; got %v", notWant, registrationNames(registrations))
		}
	}

	if _, ok := byName["dash"]; !ok {
		t.Fatalf("missing legacy alias registration dash; got %v", registrationNames(registrations))
	}
	if _, ok := byName["tinyauth"]; !ok {
		t.Fatalf("missing legacy alias registration tinyauth; got %v", registrationNames(registrations))
	}
	if byName["dash"].Primary {
		t.Fatal("legacy alias dash must not be marked primary")
	}
	if !byName["base"].Primary {
		t.Fatal("canonical base service must be marked primary")
	}
}

func TestServiceRegistrationsFromCatalogPreserveExplicitDokploy(t *testing.T) {
	registrations := ServiceRegistrationsFromCatalogForSpec(servicecatalog.Default(), &models.StackSpec{
		StackKit: "base-kit",
		PAAS:     models.PAASDokploy,
		Compute:  models.ComputeSpec{Tier: models.ComputeTierStandard},
	})
	byName := make(map[string]ServiceDef, len(registrations))
	for _, svc := range registrations {
		byName[svc.Name] = svc
	}

	if _, ok := byName["dokploy"]; !ok {
		t.Fatalf("missing explicit dokploy service registration; got %v", registrationNames(registrations))
	}
	if _, ok := byName["coolify"]; ok {
		t.Fatalf("explicit dokploy registration must not include coolify; got %v", registrationNames(registrations))
	}
}

func TestMergeServiceRegistrationsAddsAppServicesWithoutDuplicates(t *testing.T) {
	merged := MergeServiceRegistrations(
		[]ServiceDef{
			{Name: "base", Description: "Dashboard", Primary: true},
			{Name: "web", Description: "Existing app", Primary: true},
		},
		[]ServiceDef{
			{Name: "web", Description: "Duplicate app", Primary: true},
			{Name: "studio", Description: "StackKit app studio", Primary: true},
		},
	)

	byName := make(map[string]ServiceDef, len(merged))
	for _, svc := range merged {
		byName[svc.Name] = svc
	}

	if len(merged) != 3 {
		t.Fatalf("merged service count = %d, want 3: %v", len(merged), registrationNames(merged))
	}
	if byName["studio"].Description != "StackKit app studio" {
		t.Fatalf("studio app registration missing: %#v", byName["studio"])
	}
	if byName["web"].Description != "Existing app" {
		t.Fatalf("duplicate app registration should preserve first service, got %#v", byName["web"])
	}
}

func registrationNames(services []ServiceDef) []string {
	names := make([]string, 0, len(services))
	for _, svc := range services {
		names = append(names, svc.Name)
	}
	return names
}
