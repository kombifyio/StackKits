package commands

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	cueval "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/registry"
	"github.com/kombifyio/stackkits/internal/servicecatalog"
	"github.com/kombifyio/stackkits/pkg/models"
)

func loadCanonicalServiceCatalog(wd string, spec *models.StackSpec) []servicecatalog.Service {
	if services := loadRegistryServiceCatalog(); len(services) > 0 {
		return services
	}
	return loadCUEServiceCatalog(wd, spec)
}

func loadRegistryServiceCatalog() []servicecatalog.Service {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snap, err := registry.AutoClient().Snapshot(ctx)
	if err != nil {
		deployLog.Warn("service_catalog.registry",
			slog.String("status", "fallback"),
			slog.String("error", err.Error()),
		)
		return nil
	}
	return servicecatalog.WithDefaultFallbacks(servicecatalog.FromRegistry(snap.Services))
}

func loadCUEServiceCatalog(wd string, spec *models.StackSpec) []servicecatalog.Service {
	loader := config.NewLoader(wd)
	stackkitDir, err := loader.FindStackKitDir(spec.StackKit)
	if err != nil {
		parentLoader := config.NewLoader(filepath.Dir(wd))
		stackkitDir, err = parentLoader.FindStackKitDir(spec.StackKit)
	}
	if err != nil {
		return servicecatalog.Default()
	}

	modulesDir := resolveModulesDir(stackkitDir, wd)
	domains, err := cueval.DomainEntriesFromModules(modulesDir)
	if err != nil {
		return servicecatalog.Default()
	}
	return servicecatalog.FromCUE(domains)
}
