package kombifyme

import (
	"testing"

	"github.com/kombifyio/stackkits/internal/servicecatalog"
)

func TestServiceRegistrationsFromCatalogUseCanonicalNamesAndLegacyAliases(t *testing.T) {
	registrations := ServiceRegistrationsFromCatalog(servicecatalog.Default(), "standard")
	byName := make(map[string]ServiceDef, len(registrations))
	for _, svc := range registrations {
		byName[svc.Name] = svc
	}

	for _, want := range []string{"base", "auth", "id", "dokploy", "kuma", "whoami", "vault", "media", "photos"} {
		if _, ok := byName[want]; !ok {
			t.Fatalf("missing canonical service registration %q; got %v", want, registrationNames(registrations))
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

func registrationNames(services []ServiceDef) []string {
	names := make([]string, 0, len(services))
	for _, svc := range services {
		names = append(names, svc.Name)
	}
	return names
}
