//go:build publisher

package commands

import (
	"context"

	"github.com/kombifyio/stackkits/internal/managedentitlement"
)

func requireManagedTenantDeployment(ctx context.Context) error {
	return managedentitlement.Evaluate(ctx, managedentitlement.CapabilityTenantDeployment)
}
