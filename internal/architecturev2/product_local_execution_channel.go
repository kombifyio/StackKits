package architecturev2

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

// ProductLocalExecutionChannelBinding is an explicit declaration that this
// process owns one exact Site/node/channel tuple. It is intentionally singular:
// multi-node and hybrid execution require a channel authority that transports
// each request to the correct host rather than executing every target locally.
type ProductLocalExecutionChannelBinding struct {
	ChannelRef string
	SiteRef    string
	NodeRef    string
}

type productLocalExecutionChannelFactory struct {
	binding ProductLocalExecutionChannelBinding
}

type productLocalExecutionChannelAdmission struct{}

// NewProductLocalExecutionChannelFactory admits only the exact binding. The
// caller must source it from device-/orchestrator-owned configuration; a
// RuntimeTarget can never declare itself local merely by naming a channel.
func NewProductLocalExecutionChannelFactory(binding ProductLocalExecutionChannelBinding) (ProductExecutionChannelFactory, error) {
	if err := validateProductLocalExecutionChannelBinding(binding); err != nil {
		return nil, err
	}
	return &productLocalExecutionChannelFactory{binding: binding}, nil
}

func (f *productLocalExecutionChannelFactory) AdmitExecutionChannel(request ProductExecutionChannelRequest) (ProductExecutionChannelAdmission, error) {
	if f == nil {
		return nil, errors.New("local execution-channel factory is not initialized")
	}
	if err := validateProductLocalExecutionChannelBinding(f.binding); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("local execution-channel request is invalid: %w", err)
	}
	if request.ChannelRef != f.binding.ChannelRef || request.SiteRef != f.binding.SiteRef || request.NodeRef != f.binding.NodeRef {
		return nil, fmt.Errorf("execution channel %q is not the configured local Site/node binding", request.ChannelRef)
	}
	return productLocalExecutionChannelAdmission{}, nil
}

func (productLocalExecutionChannelAdmission) PrepareExecutionChannel(local ProductExecutionChannelLocalExecutor) (runtimeexecutor.Executor, error) {
	if local == nil {
		return nil, errors.New("local execution channel requires its bounded executor builder")
	}
	return local()
}

// NewProductRuntimeRootIdentity returns the stable product-runtime identity
// used by evidence, authorization, Journal, and recovery. It identifies the
// provider-free dispatcher contract and binary runtime version; exact target,
// artifact, owner, and channel bindings remain sealed into every request.
func NewProductRuntimeRootIdentity(runtimeVersion string) (runtimeexecutor.ExecutorIdentity, error) {
	if runtimeVersion == "" || runtimeVersion != strings.TrimSpace(runtimeVersion) {
		return runtimeexecutor.ExecutorIdentity{}, errors.New("product runtime root identity requires a runtime version")
	}
	canonical, err := resolvedplan.CanonicalJSON(struct {
		Contract string `json:"contract"`
		Version  string `json:"version"`
	}{Contract: "stackkits-product-runtime-root/v1", Version: runtimeVersion})
	if err != nil {
		return runtimeexecutor.ExecutorIdentity{}, err
	}
	identity := runtimeexecutor.ExecutorIdentity{
		ID: "stackkits-product-runtime", Version: runtimeVersion, Digest: productApplyDigest(canonical),
	}
	if err := generationartifact.ValidateApplyExecutorIdentity(generationartifact.ApplyExecutorIdentity{
		ID: identity.ID, Version: identity.Version, Digest: identity.Digest,
	}); err != nil {
		return runtimeexecutor.ExecutorIdentity{}, fmt.Errorf("product runtime root identity is invalid: %w", err)
	}
	return identity, nil
}

func validateProductLocalExecutionChannelBinding(binding ProductLocalExecutionChannelBinding) error {
	for _, field := range []struct {
		label string
		value string
	}{
		{label: "channelRef", value: binding.ChannelRef},
		{label: "siteRef", value: binding.SiteRef},
		{label: "nodeRef", value: binding.NodeRef},
	} {
		if field.value == "" || field.value != strings.TrimSpace(field.value) {
			return fmt.Errorf("local execution-channel %s is required and must be trimmed", field.label)
		}
	}
	return nil
}

var (
	_ ProductExecutionChannelFactory   = (*productLocalExecutionChannelFactory)(nil)
	_ ProductExecutionChannelAdmission = productLocalExecutionChannelAdmission{}
)
