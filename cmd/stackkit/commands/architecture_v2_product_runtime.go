package commands

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorlocal"
)

type architectureV2ProductRuntimeAuthority struct {
	*architecturev2.Service
	journal *architecturev2.ProductApplyFileJournal
}

func (a *architectureV2ProductRuntimeAuthority) Close() error {
	if a == nil || a.journal == nil {
		return nil
	}
	err := a.journal.Close()
	a.journal = nil
	return err
}

// newArchitectureV2ProductRuntimeAuthority is the production CLI composition
// admission. The standalone binary deliberately owns no inspection/signing
// key and therefore cannot manufacture a Product Apply collector. Integrations
// must construct the public productruntime.Composition or inject a real
// construction-owned collector through the internal adapter below.
func newArchitectureV2ProductRuntimeAuthority(workspaceRoot string, options architectureV2ExecutionCLIOptions) (architectureV2ExecutionAuthority, error) {
	return nil, fmt.Errorf(
		"Architecture v2 Product Apply requires a construction-owned evidence collector; standalone stackkit apply cannot accept caller evidence or create signing custody (use pkg/productruntime.Composition from the authenticated service integration)",
	)
}

// newArchitectureV2ProductRuntimeAuthorityWithCollector owns the provider-free
// Runtime Owner registry plus durable journal/recovery custody for one
// workspace and one real integration-owned collector. It never guesses that a
// planned target is local: the integration must bind one exact
// Site/node/channel tuple.
func newArchitectureV2ProductRuntimeAuthorityWithCollector(
	workspaceRoot string,
	options architectureV2ExecutionCLIOptions,
	collector architecturev2.ProductApplyEvidenceCollector,
) (architectureV2ExecutionAuthority, error) {
	runtimeVersion := architectureV2ComponentVersion(version)
	identity, err := architecturev2.NewProductRuntimeRootIdentity(runtimeVersion)
	if err != nil {
		return nil, fmt.Errorf("construct Architecture v2 product runtime identity: %w", err)
	}
	registrations, err := architectureV2LocalRuntimeOwnerRegistrations(runtimeVersion)
	if err != nil {
		return nil, err
	}
	channels, err := architectureV2ProductExecutionChannels(options)
	if err != nil {
		return nil, err
	}
	journal, err := architecturev2.NewProductApplyFileJournal(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("open Architecture v2 Product Apply custody: %w", err)
	}
	service, err := architecturev2.NewProductEmbeddedServiceWithRuntimeOwnersAndApplyEvidenceCollector(
		architecturev2.StackKitsV2Contract(version), identity,
		registrations, channels, journal, journal, collector,
	)
	if err != nil {
		return nil, errors.Join(err, journal.Close())
	}
	return &architectureV2ProductRuntimeAuthority{Service: service, journal: journal}, nil
}

func architectureV2LocalRuntimeOwnerRegistrations(runtimeVersion string) ([]architecturev2.ProductRuntimeOwnerRegistration, error) {
	constructors := []func() (architecturev2.ProductRuntimeOwnerRegistration, error){
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductSecurityBaselineRegistration(runtimeVersion)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductCoreHostBootstrapRegistration(runtimeVersion, runtimeexecutorlocal.NewOSCoreHostBootstrapOperations())
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductHomeBackupTargetRegistration(runtimeVersion, runtimeexecutorlocal.NewOSHomeBackupTargetOperations())
		},
	}
	registrations := make([]architecturev2.ProductRuntimeOwnerRegistration, 0, len(constructors))
	for index, construct := range constructors {
		registration, err := construct()
		if err != nil {
			return nil, fmt.Errorf("register Architecture v2 local runtime owner %d: %w", index, err)
		}
		registrations = append(registrations, registration)
	}
	return registrations, nil
}

func architectureV2ProductExecutionChannels(options architectureV2ExecutionCLIOptions) (architecturev2.ProductExecutionChannelFactory, error) {
	values := []string{options.localSiteRef, options.localNodeRef, options.localChannelRef}
	configured := 0
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			configured++
		}
	}
	if configured == 0 {
		return architectureV2UnavailableExecutionChannels{}, nil
	}
	if configured != len(values) {
		return nil, fmt.Errorf("Architecture v2 collector-integrated local execution requires exact Site, node, and execution-channel bindings together")
	}
	channels, err := architecturev2.NewProductLocalExecutionChannelFactory(architecturev2.ProductLocalExecutionChannelBinding{
		SiteRef: options.localSiteRef, NodeRef: options.localNodeRef, ChannelRef: options.localChannelRef,
	})
	if err != nil {
		return nil, fmt.Errorf("configure Architecture v2 local execution channel: %w", err)
	}
	return channels, nil
}

// architectureV2UnavailableExecutionChannels keeps resolution, generation,
// and authenticated evidence validation usable without granting mutation. A
// target can cross this boundary only after an explicit local binding or a
// separately constructed authenticated remote channel authority is present.
type architectureV2UnavailableExecutionChannels struct{}

func (architectureV2UnavailableExecutionChannels) AdmitExecutionChannel(request architecturev2.ProductExecutionChannelRequest) (architecturev2.ProductExecutionChannelAdmission, error) {
	return nil, fmt.Errorf(
		"execution channel %q is not admitted: collector-integrated local execution requires explicit Site/node/channel authority; service and multi-node execution require an authenticated external channel authority",
		request.ChannelRef,
	)
}

var (
	_ architectureV2ExecutionAuthority              = (*architectureV2ProductRuntimeAuthority)(nil)
	_ architectureV2ProductApplyAuthority           = (*architectureV2ProductRuntimeAuthority)(nil)
	_ architecturev2.ProductExecutionChannelFactory = architectureV2UnavailableExecutionChannels{}
)
