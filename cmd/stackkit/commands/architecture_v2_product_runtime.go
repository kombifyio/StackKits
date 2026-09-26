package commands

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/opentofu"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
)

const (
	architectureV2StandaloneApplicationAdapterRef       = "standalone-compose"
	architectureV2StandaloneApplicationAdapterModuleRef = "stackkits-standalone-compose-runtime"
)

type architectureV2ProductRuntimeAuthority struct {
	*architecturev2.Service
	journal *architecturev2.ProductApplyFileJournal
}

// architectureV2RuntimeOwnerRegistrationsOverride lets a test replace the
// local Operations behind each production selector with a stub. It is nil in
// every production composition.
var architectureV2RuntimeOwnerRegistrationsOverride func([]architecturev2.ProductRuntimeOwnerRegistration) []architecturev2.ProductRuntimeOwnerRegistration

func (a *architectureV2ProductRuntimeAuthority) Close() error {
	if a == nil || a.journal == nil {
		return nil
	}
	err := a.journal.Close()
	a.journal = nil
	return err
}

func (a *architectureV2ProductRuntimeAuthority) LoadAppliedRuntimeRequest(ctx context.Context, requestDigest string) (runtimeexecutor.ExecutionRequest, error) {
	if a == nil || a.journal == nil {
		return runtimeexecutor.ExecutionRequest{}, errors.New("Architecture v2 applied runtime custody is unavailable")
	}
	request, err := a.journal.LoadAppliedRuntimeRequest(ctx, requestDigest)
	if err != nil {
		return runtimeexecutor.ExecutionRequest{}, fmt.Errorf("load applied runtime request: %w", err)
	}
	return request, nil
}

// newArchitectureV2ProductRuntimeAuthority is the production CLI composition
// admission. The standalone binary constructs its own evidence collector from
// the local homelab owner's signing custody, so Apply stays available with no
// Kombify account, endpoint, or TechStack present.
//
// This is construction-owned evidence in the strict sense the contract
// requires: the collector is built by the composition root, never accepted
// from a caller or an Apply request. ADR-0029 places that construction at
// home — "Enrollment and signing happen there" — and requires conformance
// gates to reject remote enrollment/signing, so the local owner is the correct
// construction owner rather than an exception to it.
//
// An authenticated service integration still supplies its own collector
// through newArchitectureV2ProductRuntimeAuthorityWithCollector; both paths
// share one custody model.
//
// A joined Fleet member composes the same authority from its Home-certified
// member evidence key and executes only its own Site/node/channel tuple; a
// Foundation Node with certified members leaves their local targets to them.
func newArchitectureV2ProductRuntimeAuthority(workspaceRoot string, options architectureV2ExecutionCLIOptions) (architectureV2ExecutionAuthority, error) {
	local, err := loadArchitectureV2LocalExecution(workspaceRoot, time.Now())
	if err != nil {
		return nil, localApplyCustodyError(err)
	}
	collector, anchor, err := local.applyEvidence(workspaceRoot)
	if err != nil {
		return nil, err
	}
	options, err = bindArchitectureV2LocalExecutionOptions(options, local.binding)
	if err != nil {
		return nil, err
	}
	authority, err := newArchitectureV2ProductRuntimeAuthorityWithCollectorAndTrust(workspaceRoot, options, collector, []architecturev2.ProductApplyTrustAnchor{anchor})
	if err != nil {
		return nil, err
	}
	if err := local.bindScope(authority); err != nil {
		if closer, ok := authority.(interface{ Close() error }); ok {
			err = errors.Join(err, closer.Close())
		}
		return nil, err
	}
	return authority, nil
}

// localApplyCustodyError keeps the established guidance for a workspace that
// has neither owner nor member custody.
func localApplyCustodyError(err error) error {
	if errors.Is(err, localevidence.ErrOwnerCustodyMissing) {
		var memberDenial *localevidence.MemberSigningDenial
		if !errors.As(err, &memberDenial) {
			return fmt.Errorf(
				"this workspace has no local Apply evidence custody; run `stackkit init --owner-source=local` to establish the homelab owner before Apply",
			)
		}
	}
	return err
}

// newArchitectureV2ProductVerifyAuthority derives public verification trust
// from the established local key custody and owns the immutable product
// runtime identity. It opens existing runtime custody for read-only inspection
// without constructing an execution channel or mutating runtime owner.
func newArchitectureV2ProductVerifyAuthority(workspaceRoot string, _ architectureV2ExecutionCLIOptions) (architectureV2ExecutionAuthority, error) {
	local, err := loadArchitectureV2LocalExecution(workspaceRoot, time.Now())
	if err != nil {
		return nil, localApplyCustodyError(err)
	}
	_, anchor, err := local.applyEvidence(workspaceRoot)
	if err != nil {
		return nil, err
	}
	runtimeVersion := architectureV2ComponentVersion(version)
	identity, err := architecturev2.NewProductRuntimeRootIdentity(runtimeVersion)
	if err != nil {
		return nil, fmt.Errorf("construct read-only Architecture v2 product runtime identity: %w", err)
	}
	service, err := architecturev2.NewProductEmbeddedServiceWithLocalApplyVerification(
		architecturev2.StackKitsV2Contract(version), identity,
		[]architecturev2.ProductApplyTrustAnchor{anchor},
	)
	if err != nil {
		return nil, err
	}
	if local.scope != nil {
		if err := service.BindProductExecutionScope(*local.scope); err != nil {
			return nil, err
		}
	}
	journal, err := architecturev2.NewProductApplyFileJournal(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("open read-only Product Apply runtime custody: %w", err)
	}
	return &architectureV2ProductRuntimeAuthority{Service: service, journal: journal}, nil
}

// newLocalOwnerApplyEvidenceCollector binds Apply evidence to the owner
// identity this workspace already established. Custody is loaded, never
// silently minted at Apply time: a key appearing during Apply would mean the
// workspace had no owner to anchor evidence to.
func newLocalOwnerApplyEvidenceCollector(workspaceRoot string) (architecturev2.ProductApplyEvidenceCollector, architecturev2.ProductApplyTrustAnchor, localevidence.LocalBinding, error) {
	custody, err := localevidence.LoadOwnerCustody(workspaceRoot)
	if errors.Is(err, localevidence.ErrOwnerCustodyMissing) {
		return nil, architecturev2.ProductApplyTrustAnchor{}, localevidence.LocalBinding{}, fmt.Errorf(
			"this workspace has no local Apply evidence custody; run `stackkit init --owner-source=local` to establish the homelab owner before Apply",
		)
	}
	if err != nil {
		return nil, architecturev2.ProductApplyTrustAnchor{}, localevidence.LocalBinding{}, fmt.Errorf("load local owner custody: %w", err)
	}
	key, err := localevidence.LoadOwnerKey(workspaceRoot)
	if errors.Is(err, localevidence.ErrOwnerKeyMissing) {
		return nil, architecturev2.ProductApplyTrustAnchor{}, localevidence.LocalBinding{}, fmt.Errorf(
			"this workspace has no local Apply evidence custody; run `stackkit init --owner-source=local` to establish the homelab owner before Apply",
		)
	}
	if err != nil {
		return nil, architecturev2.ProductApplyTrustAnchor{}, localevidence.LocalBinding{}, fmt.Errorf("load local Apply evidence custody: %w", err)
	}
	observers, err := architectureV2LocalEvidenceObservers(workspaceRoot)
	if err != nil {
		return nil, architecturev2.ProductApplyTrustAnchor{}, localevidence.LocalBinding{}, err
	}
	collector, err := localevidence.NewOwnerCollector(localevidence.CollectorConfig{
		Key:       key,
		Version:   architectureV2ComponentVersion(version),
		Observers: observers,
	})
	if err != nil {
		return nil, architecturev2.ProductApplyTrustAnchor{}, localevidence.LocalBinding{}, fmt.Errorf("configure local Apply evidence collector: %w", err)
	}
	producer, publicKey, err := collector.ProducerTrust()
	if err != nil {
		return nil, architecturev2.ProductApplyTrustAnchor{}, localevidence.LocalBinding{}, err
	}
	return collector, architecturev2.ProductApplyTrustAnchor{
		Producer: generationartifact.ApplyEvidenceProducer{
			ID: producer.ID, Version: producer.Version, KeyID: producer.KeyID,
		},
		PublicKey: ed25519.PublicKey(publicKey), RequirementKinds: []string{"host", "secret"},
	}, custody.Binding, nil
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
	return newArchitectureV2ProductRuntimeAuthorityWithCollectorAndTrust(workspaceRoot, options, collector, nil)
}

func newArchitectureV2ProductRuntimeAuthorityWithCollectorAndTrust(
	workspaceRoot string,
	options architectureV2ExecutionCLIOptions,
	collector architecturev2.ProductApplyEvidenceCollector,
	trust []architecturev2.ProductApplyTrustAnchor,
) (architectureV2ExecutionAuthority, error) {
	runtimeVersion := architectureV2ComponentVersion(version)
	identity, err := architecturev2.NewProductRuntimeRootIdentity(runtimeVersion)
	if err != nil {
		return nil, fmt.Errorf("construct Architecture v2 product runtime identity: %w", err)
	}
	registrations, err := architectureV2RuntimeOwnerRegistrations(workspaceRoot, runtimeVersion, options)
	if err != nil {
		return nil, err
	}
	if architectureV2RuntimeOwnerRegistrationsOverride != nil {
		registrations = architectureV2RuntimeOwnerRegistrationsOverride(registrations)
	}
	channels, err := architectureV2ProductExecutionChannels(options)
	if err != nil {
		return nil, err
	}
	journal, err := architecturev2.NewProductApplyFileJournal(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("open Architecture v2 Product Apply custody: %w", err)
	}
	var service *architecturev2.Service
	if len(trust) == 0 {
		service, err = architecturev2.NewProductEmbeddedServiceWithRuntimeOwnersAndApplyEvidenceCollector(
			architecturev2.StackKitsV2Contract(version), identity,
			registrations, channels, journal, journal, collector,
		)
	} else {
		service, err = architecturev2.NewProductEmbeddedServiceWithRuntimeOwnersAndLocalApplyEvidence(
			architecturev2.StackKitsV2Contract(version), identity,
			registrations, channels, journal, journal, collector, trust,
		)
	}
	if err != nil {
		return nil, errors.Join(err, journal.Close())
	}
	return &architectureV2ProductRuntimeAuthority{Service: service, journal: journal}, nil
}

func bindArchitectureV2LocalExecutionOptions(options architectureV2ExecutionCLIOptions, binding localevidence.LocalBinding) (architectureV2ExecutionCLIOptions, error) {
	configured := []string{strings.TrimSpace(options.localSiteRef), strings.TrimSpace(options.localNodeRef), strings.TrimSpace(options.localChannelRef)}
	count := 0
	for _, value := range configured {
		if value != "" {
			count++
		}
	}
	if count == 0 {
		options.localSiteRef, options.localNodeRef, options.localChannelRef = binding.SiteRef, binding.NodeRef, binding.ChannelRef
		return options, nil
	}
	if count != len(configured) || configured[0] != binding.SiteRef || configured[1] != binding.NodeRef || configured[2] != binding.ChannelRef {
		return architectureV2ExecutionCLIOptions{}, fmt.Errorf(
			"explicit local Site/node/execution-channel flags must exactly match the CUE-owned persisted owner binding %s/%s/%s",
			binding.SiteRef, binding.NodeRef, binding.ChannelRef,
		)
	}
	return options, nil
}

func architectureV2LocalRuntimeOwnerRegistrations(workspaceRoot, runtimeVersion string) ([]architecturev2.ProductRuntimeOwnerRegistration, error) {
	return architectureV2RuntimeOwnerRegistrations(workspaceRoot, runtimeVersion, architectureV2ExecutionCLIOptions{})
}

func architectureV2RuntimeOwnerRegistrations(workspaceRoot, runtimeVersion string, options architectureV2ExecutionCLIOptions) ([]architecturev2.ProductRuntimeOwnerRegistration, error) {
	policies, err := nativehost.NewOSBasementPolicyOperations(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("configure owner-bound local policy operations: %w", err)
	}
	basementCoreOperations, err := nativehost.NewOSBasementCoreOperations(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("configure local Basement core operations: %w", err)
	}
	// The standard OpenTofu executor (ADR-0045 Stage 1): Core roots, workload
	// roots, and edge and federation contract roots under the opentofu and
	// terramate targets. Under compose every owner stays native (S-F).
	nativeCompose, err := nativehost.NewOSNativeComposeRuntime(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("configure the native Verify owner for OpenTofu roots: %w", err)
	}
	openTofuRuntime := opentofu.Runtime{WorkspaceRoot: workspaceRoot, Native: nativeCompose}
	constructors := []func() (architecturev2.ProductRuntimeOwnerRegistration, error){
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return newArchitectureV2CloudBackupRegistration(workspaceRoot, runtimeVersion)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductHostAdmissionRegistration(runtimeVersion)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductSecurityBaselineRegistration(runtimeVersion)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductCoreHostBootstrapRegistration(runtimeVersion, nativehost.NewOSCoreHostBootstrapOperations())
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductHomeBackupTargetRegistration(runtimeVersion, nativehost.NewOSHomeBackupTargetOperations())
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			operations, err := nativehost.NewOSInternalPKIOperations(workspaceRoot)
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.NewProductInternalPKIRegistration(runtimeVersion, operations, operations, operations, operations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductBasementCoreRegistration(runtimeVersion, basementCoreOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductBasementCoreLiteRegistration(runtimeVersion, basementCoreOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			operations, err := nativehost.NewOSCloudCoreOperations(workspaceRoot)
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.NewProductCloudCoreRegistration(runtimeVersion, operations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			operations, err := nativehost.NewOSCloudStandaloneCoreOperations(workspaceRoot)
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.NewProductCloudStandaloneCoreRegistration(runtimeVersion, operations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			constructor := nativehost.NewOSCloudHostSecurityOperations
			if architectureV2DispatchedLocalChannel(options) {
				constructor = nativehost.NewOSCloudHostSecurityOperationsForDispatchedChannel
			}
			operations, err := constructor(workspaceRoot)
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.NewProductCloudHostSecurityRegistration(runtimeVersion, operations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			operations, err := nativehost.NewOSCloudPublicEdgeOperations(workspaceRoot)
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			registration, err := architecturev2.NewProductCloudPublicEdgeRegistration(runtimeVersion, operations)
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.WithProductOpenTofuContractRoot(registration, openTofuRuntime)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			operations, err := nativehost.NewOSPublicTLSOperations(workspaceRoot)
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.NewProductPublicTLSRegistration(runtimeVersion, operations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			operations, err := nativehost.NewOSCloudIdentityTrustPolicyOperations(workspaceRoot)
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.NewProductCloudIdentityTrustRegistration(runtimeVersion, operations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductBasementIdentityTrustRegistration(runtimeVersion, policies)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductHomeDeviceAuthorityRegistration(runtimeVersion, policies)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductHomeAccessRegistration(runtimeVersion, policies)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductLocalAutonomyRegistration(runtimeVersion, policies)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			registration, err := architecturev2.NewProductBridgeOriginMTLSRegistration(runtimeVersion, nativehost.NewOSBridgeOriginMTLSOperations(workspaceRoot))
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.WithProductOpenTofuContractRoot(registration, openTofuRuntime)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			registration, err := architecturev2.NewProductFederationLinkRegistration(runtimeVersion, nativehost.NewOSFederationLinkOperations(workspaceRoot))
			if err != nil {
				return architecturev2.ProductRuntimeOwnerRegistration{}, err
			}
			return architecturev2.WithProductOpenTofuContractRoot(registration, openTofuRuntime)
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
	// The standard OpenTofu executor owns the opentofu and terramate units of
	// the same Core modules whose compose unit stays on the native executor
	// above (S-F).
	openTofu, err := architecturev2.NewProductOpenTofuRegistrations(runtimeVersion, openTofuRuntime, opentofu.DefaultModuleBindings())
	if err != nil {
		return nil, fmt.Errorf("register OpenTofu runtime owners: %w", err)
	}
	registrations = append(registrations, openTofu...)
	nativeStandalone, err := nativehost.NewOSStandaloneComposeWorkloadOperations(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("configure standalone Compose application adapter: %w", err)
	}
	// Workload bundles keep one selector per application under every target;
	// the operations owner applies the Compose project natively under compose
	// and through its OpenTofu wrapper root under opentofu and terramate.
	standaloneOperations, err := opentofu.NewWorkloadOperations(nativeStandalone, openTofuRuntime)
	if err != nil {
		return nil, fmt.Errorf("configure OpenTofu standalone Compose application roots: %w", err)
	}
	standaloneApplications := []func() (architecturev2.ProductRuntimeOwnerRegistration, error){
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductEmbySelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductPassboltSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductNextcloudSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductNavidromeSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductAudiobookshelfSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductEsphomeSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductEuroofficeSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductMosquittoSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductZigbee2mqttSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductImmichPublicProxySelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductImmichKioskSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductImmichPowerToolsSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductGiteaSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductForgejoSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductPaperlessSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductPterodactylSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductRoundcubeSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductStalwartSelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductPrivateAISelectedPaaSRegistration(runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductImmichSelectedPaaSRegistration(
				runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations,
			)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductCloudreveSelectedPaaSRegistration(
				runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations,
			)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductVaultwardenSelectedPaaSRegistration(
				runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations,
			)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductJellyfinSelectedPaaSRegistration(
				runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations,
			)
		},
		func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
			return architecturev2.NewProductHomeAssistantSelectedPaaSRegistration(
				runtimeVersion, architectureV2StandaloneApplicationAdapterRef, architectureV2StandaloneApplicationAdapterModuleRef, standaloneOperations,
			)
		},
	}
	for _, application := range standaloneApplications {
		registration, err := application()
		if err != nil {
			return nil, fmt.Errorf("register local standalone Compose application owner: %w", err)
		}
		registrations = append(registrations, registration)
	}
	_, active, err := architectureV2ConfiguredStandardRuntimeFromInventory(options)
	if err != nil {
		return nil, err
	}
	if active {
		// Public TLS is a StackKits-owned edge operation. Keep it out of the
		// remote static set so an Inventory-backed hybrid channel cannot route
		// certificate verification through an unbound Techstack adapter.
		remote, err := architecturev2.NewProductRemoteStaticRuntimeOwnerRegistrations(architectureV2ProcessDispatchedOwners...)
		if err != nil {
			return nil, fmt.Errorf("register Modern process runtime owners: %w", err)
		}
		registrations = append(registrations, remote...)
		for _, adapter := range []struct {
			ref, moduleRef string
		}{
			{ref: "coolify", moduleRef: "stackkits-coolify-runtime"},
			{ref: "komodo", moduleRef: "stackkits-komodo-core-runtime"},
		} {
			applications := []func() (architecturev2.ProductRuntimeOwnerRegistration, error){
				func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
					return architecturev2.NewProductRemoteImmichSelectedPaaSRegistration(adapter.ref, adapter.moduleRef)
				},
				func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
					return architecturev2.NewProductRemoteCloudreveSelectedPaaSRegistration(adapter.ref, adapter.moduleRef)
				},
				func() (architecturev2.ProductRuntimeOwnerRegistration, error) {
					return architecturev2.NewProductRemoteVaultwardenSelectedPaaSRegistration(adapter.ref, adapter.moduleRef)
				},
			}
			for _, application := range applications {
				registration, err := application()
				if err != nil {
					return nil, fmt.Errorf("register %s application-adapter runtime owner: %w", adapter.ref, err)
				}
				registrations = append(registrations, registration)
			}
		}
	}
	return registrations, nil
}

func architectureV2ProductExecutionChannels(options architectureV2ExecutionCLIOptions) (architecturev2.ProductExecutionChannelFactory, error) {
	configuredRuntime, active, err := architectureV2ConfiguredStandardRuntimeFromInventory(options)
	if err != nil {
		return nil, err
	}
	if active {
		channels, err := architecturev2.NewProductProcessExecutionChannelFactory(
			architectureV2ComponentVersion(version), configuredRuntime.bindings,
		)
		if err != nil {
			return nil, fmt.Errorf("configure multi-Site standard execution channels: %w", err)
		}
		return channels, nil
	}
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
	_ architectureV2ProductVerifyAuthority          = (*architectureV2ProductRuntimeAuthority)(nil)
	_ architecturev2.ProductExecutionChannelFactory = architectureV2UnavailableExecutionChannels{}
)
