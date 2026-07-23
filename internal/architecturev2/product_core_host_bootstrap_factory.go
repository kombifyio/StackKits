package architecturev2

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const productCoreHostBootstrapAdapterID = "stackkits-core-host-bootstrap-local"

type productCoreHostBootstrapFactory struct {
	runtimeVersion string
	operations     runtimeexecutorlocal.CoreHostBootstrapOperations
}

// NewProductCoreHostBootstrapRegistration binds the exact Core host-bootstrap
// selector to one construction-owned node-local operations capability. The
// factory derives Site, node, and execution-channel scope only from the
// already verified RuntimeTarget passed by ProductRuntimeOwnerRegistry.
func NewProductCoreHostBootstrapRegistration(runtimeVersion string, operations runtimeexecutorlocal.CoreHostBootstrapOperations) (ProductRuntimeOwnerRegistration, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) || nilProductRuntimeOwnerValue(operations) {
		return ProductRuntimeOwnerRegistration{}, errors.New("Core host-bootstrap product registration requires a runtime version and operations owner")
	}
	return ProductRuntimeOwnerRegistration{
		Selector: productCoreHostBootstrapSelector(),
		Factory:  &productCoreHostBootstrapFactory{runtimeVersion: runtimeVersion, operations: operations},
	}, nil
}

func (f *productCoreHostBootstrapFactory) PrepareRuntimeOwner(request ProductRuntimeOwnerRequest) (runtimeexecutor.Executor, error) {
	if f == nil || strings.TrimSpace(f.runtimeVersion) == "" || nilProductRuntimeOwnerValue(f.operations) {
		return nil, errors.New("Core host-bootstrap product factory is not initialized")
	}
	target := cloneProductRuntimeTarget(request.Target)
	health := cloneProductHealthTargets(request.HealthTargets)
	if productRuntimeOwnerSelectorForTarget(target) != productCoreHostBootstrapSelector() ||
		len(target.SiteRefs) != 1 || len(target.NodeRefs) != 1 || strings.TrimSpace(target.ExecutionChannelRef) == "" || len(health) != 1 ||
		!productHealthTargetsRuntime(health[0], target) {
		return nil, errors.New("Core host-bootstrap product factory requires one exact channel-bound target and health contract")
	}
	identity, err := productRuntimeOwnerAdapterIdentity(productCoreHostBootstrapAdapterID, f.runtimeVersion, target, health)
	if err != nil {
		return nil, err
	}
	return runtimeexecutorlocal.NewCoreHostBootstrapExecutor(identity, runtimeexecutorlocal.LocalTargetBinding{
		SiteRef: target.SiteRefs[0], NodeRef: target.NodeRefs[0], ExecutionChannelRef: target.ExecutionChannelRef,
	}, f.operations), nil
}

func productCoreHostBootstrapSelector() ProductRuntimeOwnerSelector {
	return ProductRuntimeOwnerSelector{
		OwnerKind: "module", OwnerRef: "stackkits-core-host-bootstrap",
		ProviderRef: "stackkits-core-host-bootstrap", ModuleRef: "stackkits-core-host-bootstrap", UnitRef: "host-policy",
		RuntimeKind: "host", RuntimeDelivery: "stackkit",
	}
}

func productRuntimeOwnerAdapterIdentity(adapterID, runtimeVersion string, target runtimeexecutor.RuntimeTarget, health []runtimeexecutor.HealthTarget) (runtimeexecutor.ExecutorIdentity, error) {
	canonical, err := resolvedplan.CanonicalJSON(struct {
		Contract       string                         `json:"contract"`
		AdapterID      string                         `json:"adapterId"`
		RuntimeVersion string                         `json:"runtimeVersion"`
		Target         runtimeexecutor.RuntimeTarget  `json:"target"`
		Health         []runtimeexecutor.HealthTarget `json:"health"`
	}{
		Contract: "stackkits-product-runtime-owner/v1", AdapterID: adapterID, RuntimeVersion: runtimeVersion,
		Target: cloneProductRuntimeTarget(target), Health: cloneProductHealthTargets(health),
	})
	if err != nil {
		return runtimeexecutor.ExecutorIdentity{}, err
	}
	digest := sha256.Sum256(canonical)
	identity := runtimeexecutor.ExecutorIdentity{ID: adapterID, Version: runtimeVersion, Digest: "sha256:" + hex.EncodeToString(digest[:])}
	if err := generationartifact.ValidateApplyExecutorIdentity(generationartifact.ApplyExecutorIdentity{
		ID: identity.ID, Version: identity.Version, Digest: identity.Digest,
	}); err != nil {
		return runtimeexecutor.ExecutorIdentity{}, err
	}
	return identity, nil
}

var _ ProductRuntimeOwnerFactory = (*productCoreHostBootstrapFactory)(nil)
