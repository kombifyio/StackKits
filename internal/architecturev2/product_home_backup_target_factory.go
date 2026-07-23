package architecturev2

import (
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productHomeBackupTargetAdapterID = "stackkits-home-backup-target-local"

type productHomeBackupTargetFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.HomeBackupTargetOperations
}

// NewProductHomeBackupTargetRegistration binds the exact Home backup-target
// selector to one construction-owned observation capability. Core owns
// directory creation; this owner can only verify the already prepared target.
func NewProductHomeBackupTargetRegistration(runtimeVersion string, operations runtimeexecutorlocal.HomeBackupTargetOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Home backup-target product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productHomeBackupTargetSelector(),
		Factory:  &productHomeBackupTargetFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productHomeBackupTargetFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Home backup-target product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productHomeBackupTargetSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 ||
		!productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Home backup-target product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productHomeBackupTargetAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewHomeBackupTargetExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, f.operations), nil
}

func productHomeBackupTargetSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-home-backup-target",
		ProviderRef: "stackkits-home-backup-target", ModuleRef: "stackkits-home-backup-target", UnitRef: "backup-policy",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

var _ ProductRuntimeOwnerFactory = (*productHomeBackupTargetFactory)(nil)
