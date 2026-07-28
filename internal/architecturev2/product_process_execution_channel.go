package architecturev2

import (
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/runtimeexecutorprocess"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

type ProductProcessExecutionChannelBinding struct {
	ChannelRef       string
	SiteRef          string
	NodeRef          string
	Executable       string
	ExecutableSHA256 string
}

type productProcessExecutionChannelFactory struct {
	runtimeVersion string
	bindings       map[string]ProductProcessExecutionChannelBinding
}

type productProcessExecutionChannelAdmission struct {
	runtimeVersion string
	binding        ProductProcessExecutionChannelBinding
}

func NewProductProcessExecutionChannelFactory(
	runtimeVersion string,
	bindings []ProductProcessExecutionChannelBinding,
) (ProductExecutionChannelFactory, error) {
	if runtimeVersion == "" || len(bindings) == 0 {
		return nil, errors.New("process execution-channel factory requires a runtime version and bindings")
	}
	closed := make(map[string]ProductProcessExecutionChannelBinding, len(bindings))
	scopes := make(map[[2]string]struct{}, len(bindings))
	for index, binding := range bindings {
		processBinding := runtimeexecutorprocess.Binding{
			ChannelRef: binding.ChannelRef, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef,
			Executable: binding.Executable, ExecutableSHA256: binding.ExecutableSHA256,
		}
		if err := runtimeexecutorprocess.ValidateBinding(processBinding); err != nil {
			return nil, fmt.Errorf("process execution-channel binding %d: %w", index, err)
		}
		scope := [2]string{binding.SiteRef, binding.NodeRef}
		if _, duplicate := closed[binding.ChannelRef]; duplicate {
			return nil, fmt.Errorf("process execution channel %q is bound more than once", binding.ChannelRef)
		}
		if _, duplicate := scopes[scope]; duplicate {
			return nil, fmt.Errorf("process Site/node %q/%q is bound more than once", binding.SiteRef, binding.NodeRef)
		}
		closed[binding.ChannelRef] = binding
		scopes[scope] = struct{}{}
	}
	return &productProcessExecutionChannelFactory{runtimeVersion: runtimeVersion, bindings: closed}, nil
}

func (f *productProcessExecutionChannelFactory) AdmitExecutionChannel(
	request ProductExecutionChannelRequest,
) (ProductExecutionChannelAdmission, error) {
	if f == nil || f.runtimeVersion == "" || len(f.bindings) == 0 {
		return nil, errors.New("process execution-channel factory is not initialized")
	}
	if err := request.Validate(); err != nil {
		return nil, fmt.Errorf("process execution-channel request is invalid: %w", err)
	}
	binding, exists := f.bindings[request.ChannelRef]
	if !exists || request.SiteRef != binding.SiteRef || request.NodeRef != binding.NodeRef {
		return nil, fmt.Errorf("execution channel %q is not the configured process Site/node binding", request.ChannelRef)
	}
	return productProcessExecutionChannelAdmission{runtimeVersion: f.runtimeVersion, binding: binding}, nil
}

func (f *productProcessExecutionChannelFactory) executionChannelFor(siteRef, nodeRef string) (string, error) {
	if f == nil || len(f.bindings) == 0 {
		return "", errors.New("process execution-channel factory is not initialized")
	}
	for channelRef, binding := range f.bindings {
		if binding.SiteRef == siteRef && binding.NodeRef == nodeRef {
			return channelRef, nil
		}
	}
	return "", fmt.Errorf("Site/node %q/%q has no configured process execution channel", siteRef, nodeRef)
}

func (a productProcessExecutionChannelAdmission) PrepareExecutionChannel(
	_ ProductExecutionChannelLocalExecutor,
) (runtimeexecutor.Executor, error) {
	// This is the security boundary: configured process channels never invoke
	// the local builder, even when a selector also has a local implementation.
	return runtimeexecutorprocess.New(a.runtimeVersion, runtimeexecutorprocess.Binding{
		ChannelRef: a.binding.ChannelRef, SiteRef: a.binding.SiteRef, NodeRef: a.binding.NodeRef,
		Executable: a.binding.Executable, ExecutableSHA256: a.binding.ExecutableSHA256,
	})
}

var (
	_ ProductExecutionChannelFactory      = (*productProcessExecutionChannelFactory)(nil)
	_ productExecutionChannelTargetBinder = (*productProcessExecutionChannelFactory)(nil)
	_ ProductExecutionChannelAdmission    = productProcessExecutionChannelAdmission{}
)
