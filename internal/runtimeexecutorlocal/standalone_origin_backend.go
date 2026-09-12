package runtimeexecutorlocal

import (
	"context"
	"time"

	"github.com/kombifyio/stackkits/internal/localorigin"
)

func (o *osStandaloneComposeWorkloadOperations) recordOriginBackend(ctx context.Context, project standaloneComposeProject, address string) error {
	// Unrouted applications cannot become an origin implicitly.
	if project.bundle.Route.ServiceRef == "" {
		return nil
	}
	raw, err := o.runner.Run(ctx, standaloneComposeArgs(project, "ps"), project.directory)
	if err != nil {
		return err
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil {
		return err
	}
	if _, err := observeStandaloneComposeComponents(project.bundle.Components, statuses); err != nil {
		return err
	}
	if err := validateStandaloneComposeRouteReadback(statuses[project.bundle.EntryComponent], project.bundle.Route); err != nil {
		return err
	}
	return localorigin.RecordBackend(o.workspaceRoot, localorigin.Backend{
		ModuleRef: project.bundle.ModuleRef, UnitRef: project.unitRef, InstanceRef: project.bundle.InstanceRef, NodeRef: project.bundle.NodeRef,
		ServiceRef: project.bundle.Route.ServiceRef, TargetPort: project.entry.HealthPort, Address: address,
		ContainerID: statuses[project.bundle.EntryComponent].ID, ObservedAt: time.Now().UTC(),
	})
}
