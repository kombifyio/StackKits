package main

import "strings"

const budgetedPackageScope = "changed-packages-budgeted"

// sliceBudgetSlowTests registers packages whose full test suite cannot fit the
// affected slice budget (AGENTS.md Kombify Development Standard rule 8: below
// two minutes target, five minutes hard) together with their measured slow
// top-level tests.
//
// A changed package without a focused selection normally runs its whole
// suite. A registered package runs with these tests skipped instead. A slow
// test still runs when it changes, when a changed file is bound to it, when a
// public boundary requires it, and in `mise run test:full`. A stale name only
// makes that fallback slower; it never removes a test that is still fast.
//
// cmd/stackkit/commands, measured 2026-09-16 on the two-vCPU Fast Gate runner
// with `go test -count=1 -json ./cmd/stackkit/commands`: a cold compile of the
// test binary took 126 seconds and the tests 446 seconds. The 51 registered
// tests evaluate the Architecture v2 CUE authority and took 444 of those
// seconds; the other 207 tests took about two. Refresh the list the same way on
// that runner class and register every top-level test that takes at least one
// second. Added 2026-09-25 from a local measurement (the runner run timed
// out): TestAdvancedChangeSetAdmissionIgnoresRemeasuredFreeDiskOnly (37 s),
// TestAgentInstallPlanInitStepInitializesAWorkspace (5 s) and
// TestAdvancedChangeSetCreateAddsFilesWorkload (58 s, two local CPUs).
var sliceBudgetSlowTests = map[string][]string{
	"cmd/stackkit/commands": {
		"TestAdvancedChangeSetAdmissionIgnoresRemeasuredFreeDiskOnly",
		"TestAdvancedChangeSetCreateAddsFilesWorkload",
		"TestAgentInstallPlanInitStepInitializesAWorkspace",
		"TestArchitectureV2AccessManifestProjectsRuntimeServiceMeaning",
		"TestArchitectureV2AccessSummaryPrintsSecureContextURLsForInternalTLS",
		"TestArchitectureV2AddonListRejectsV1AsMigrationInput",
		"TestArchitectureV2AddonListUsesEmbeddedCatalogWithoutSpec",
		"TestArchitectureV2AddonListValidatesAndFiltersCurrentSpec",
		"TestArchitectureV2HTTPProbeAccessIncludesPlatformCoreRoutes",
		"TestBuildArchitectureV2RuntimeObservationsProjectsLiveCloudServices",
		"TestExecuteNativeWorkloadRemovalDispatchesSealedComposeRequest",
		"TestFederationControlCLISignsExactHomeAction",
		"TestGenerateEmitsBasementStandardHomeAssistant",
		"TestGenerateRejectsV1OnExactV06BeforeWritingOutputOrState",
		"TestInitNativeMixedModuleProfilesResolveWithoutGlobalTier",
		"TestLocalRuntimeOwnersExecuteGeneratedApplicationWorkloads",
		"TestMigrateCompletionFailsClosedOnUnknownFieldsAndPlaintextSecretRefs",
		"TestMigrateDoesNotPublishSpecWhenAuditPublicationFails",
		"TestNativeV2BackupCommandFailsBeforeSideEffectsOnTamperedAuthority",
		"TestResolveCommandEmitsDeterministicCanonicalPlanToStdoutOrFile",
		"TestResolveCommandRejectsUnknownV2Field",
		"TestResolveCommandReturnsTypedV1MigrationBlockedError",
		"TestResolveCommandReturnsTypedV1MigrationErrorWithoutOutput",
		"TestResolveFilesystemAuthorityAcceptsProjectedPublicProfileSet",
		"TestRunArchitectureV2InitAcceptsBasementLowComputeTier",
		"TestRunArchitectureV2InitCandidateBindsOwnerCustodyToSelectedNode",
		"TestRunArchitectureV2InitCloudEstablishesLocalOwnerCustody",
		"TestRunArchitectureV2InitCloudWithoutDomainUsesKombifyMe",
		"TestRunArchitectureV2InitDefaultsToBasementAndEstablishesLocalOwnerCustody",
		"TestRunArchitectureV2InitDoesNotAdoptCloudContextOnLAN",
		"TestRunArchitectureV2InitFailsBeforeWriteForMissingRequiredDomain",
		"TestRunArchitectureV2InitFilesResumePreservesOmittedOwnerIdentity",
		"TestRunArchitectureV2InitLocalOwnerRequiresRealEmail",
		"TestRunArchitectureV2InitMaterializesCanonicalProductSpecs",
		"TestRunArchitectureV2InitModernEstablishesWorkloadSecretCustody",
		"TestRunArchitectureV2InitNormalizesWorkspaceNameAndHonorsExplicitName",
		"TestRunArchitectureV2InitPersistsHardwareProfilePi",
		"TestRunArchitectureV2InitRefusesLocalBasementOnPublicServer",
		"TestRunArchitectureV2InitRejectsMediaOnBasementLow",
		"TestRunArchitectureV2InitRejectsUndeclaredCloudLowComputeTierBeforeWrite",
		"TestRunArchitectureV2InitReplacesPlaceholderLocalOwnerEmail",
		"TestRunArchitectureV2InitUseCaseSelectsLocalRuntimeOwner",
		"TestRunArchitectureV2InitUsesExistingSpecAliasWithoutCreatingSecondAuthority",
		"TestRunArchitectureV2InitUsesExpectedHashCASAndRejectsForce",
		"TestRunInitRoutesDevToEmbeddedV2BeforeLegacyDiscovery",
		"TestSecretsRevealRequiresDeclaredOwnerCustody",
		"TestSupportExportCommandRetainsDiagnosticsAndRedactsSecrets",
		"TestValidateNativeFilesSpecWithoutTargetInventory",
	},
	// internal/architecturev2, measured 2026-09-25 locally with two CPUs
	// (GOMAXPROCS=2) in groups of six: the whole package exceeded the
	// five-minute hang guard on the Fast Gate runner when an import-path move
	// selected it. These 32 tests took at least one second each (181 seconds
	// together; the other 64 took under one second in total).
	// TestInitialStackSpecsResolveEveryAdvertisedGenerationTarget,
	// TestModernTerramateStacksAreSiteScopedAndFederationWaitsForBothSites and
	// TestTerramateStacksCoverEveryComposeBearingModuleInOrder were stopped
	// above 4.5 GB of memory before finishing and are registered as slow.
	"internal/architecturev2": {
		"TestAuthorizedRenderAndManagedOutputTransaction",
		"TestAuthorizedRenderRejectsSameRendererRefWithWrongTemplateHash",
		"TestExternalHomeAssistantGeneratesBoundInstanceWithoutLocalOrigin",
		"TestGenerationAuthorizationBindsHeldWorkspaceAndUsageLease",
		"TestInitialStackSpecsResolveEveryAdvertisedGenerationTarget",
		"TestListSupportedAddOnsRejectsInvalidProfile",
		"TestListSupportedAddOnsUsesEmbeddedCatalogOutsideCheckout",
		"TestMailServerResolvesOnlyOnOneDedicatedNode",
		"TestManagedOutputFailsFastWhenAnotherProcessOwnsOutputLock",
		"TestManagedOutputRejectsDotRootAndSymlinkedExistingTree",
		"TestMaterializeCloudInitialStackSpecRejectsUndeclaredLowComputeTier",
		"TestMaterializeInitialStackSpecCarriesAbsoluteHostStorageRoots",
		"TestMaterializeInitialStackSpecUsesEmbeddedDefinitionAuthority",
		"TestMaterializeModernInitialStackSpecResolves",
		"TestModernTerramateStacksAreSiteScopedAndFederationWaitsForBothSites",
		"TestNativeCatalogDefaultsPersistExplicitIntent",
		"TestPaperlessRenderedDeliveryBindsApplicationIndependentOfComponentOrder",
		"TestProductApplyRecoveryCapsulePersistsExactPreMutationAuthority",
		"TestProductBasementApplyBlocksWithoutAttestedCapacity",
		"TestProductBasementApplyReadyWithAttestedCapacity",
		"TestPublicRendererAuthorizationBindsInstallationToExactWorkspace",
		"TestRenderAndInstallCannotBeRedirectedByWorkspacePathReplacement",
		"TestResolveAmbiguousV1ReturnsBlockedMigrationReport",
		"TestResolveBasementLowSelectsLiteCore",
		"TestResolveBasementV2UsesCurrentCUEAuthorityAndIsByteDeterministic",
		"TestRoundcubeRenderedDeliveryKeepsTheSessionKeyInCustody",
		"TestTerramateStacksCoverEveryComposeBearingModuleInOrder",
		"TestValidateStackSpecDefaultsOmittedWorkloadPlacement",
		"TestVerifiedApplyAuthorizationOneShotExpiryAndTOCTOU",
		"TestVerifyCanonicalPlanRejectsRehashedFalseReadyState",
	},
	// internal/generationartifact, measured 2026-09-25 locally with two CPUs
	// (GOMAXPROCS=2): the package took 95 seconds and exceeded the five-minute
	// hang guard on the Fast Gate runner. Every registered test resolves or
	// verifies plans through the CUE authority and took at least one second.
	"internal/generationartifact": {
		"TestApplyEvidenceRequestSeparatesPreconditionsFromExecutorPostconditions",
		"TestArtifactHardLinkAliasIsDuplicate",
		"TestCanonicalReadersRejectUnknownDuplicateAndReformattedContracts",
		"TestEmbeddedServicePlanCanCrossGenerationVerificationBoundary",
		"TestExecutionGateBindsVerifiedObjectsToCanonicalControls",
		"TestGenerationGateFailsClosedForStalePlanChangedMissingAndWrongHashes",
		"TestInspectExecutionProjectsVerifiedGenerationDefensivelyAndDeterministically",
		"TestModernApplyHealthIsBoundToOneExactRuntime",
		"TestPlanPersistenceRejectsTargetAndParentSymlinks",
		"TestPlanPersistenceRequiresCUEAuthorityAndIsCanonicalAtomic0600",
		"TestSharedApplyEvidenceProducerContractInteroperatesWithVerifier",
		"TestVerifiedPlanCarriesStructuredReadinessAndPortableMetadataPaths",
		"TestVerifyApplyEvidenceBundleRequiresExactAuthenticatedFreshSet",
		"TestVerifyPlanRejectsRehashedProductAuthorityIssuerAndFingerprintTampering",
	},
	// internal/cue, measured 2026-09-25 locally under GOMAXPROCS=2/GOFLAGS=-p=1
	// with `go test -v ./internal/cue`: 100 seconds locally, but the PR #1411
	// Fast Gate run (two-vCPU runner) exceeded the 300s hard timeout on this
	// package alone. Every registered test resolves or verifies CUE authority
	// fixtures (module/service catalog extraction, Architecture v2 negative
	// fixtures, OTLP/monitoring/backup/drift contract closure) and took at
	// least one second locally.
	"internal/cue": {
		"TestArchitectureV2NegativeFixtures",
		"TestOptionalOTLPSignalLanesAreBoundedAndExternal",
		"TestBackupGrantV1SelectsCoverageAndCadence",
		"TestOTLPProcessorContractsMatchCollectorArtifacts",
		"TestResolvedGenerationPlanRejectsArtifactIdentityCollisions",
		"TestBackupPolicyV1IsClosedAndBounded",
		"TestMonitoringContractKeepsCollectorBaselineIndependentOfBackends",
		"TestArchitectureV2DashboardIntentIsExternalAndProviderFree",
		"TestArchitectureV2PublicDNSNameRejectsAddressLiterals",
		"TestArchitectureV2OTLPBaselineProjectionExcludesBackendAndCredentialAuthority",
		"TestArchitectureV2OptionalOTLPSignalProjectionIsClosedAndProfileBound",
		"TestArchitectureV2ModuleInputBindingContractIsClosedAndTyped",
		"TestDriftPolicyV1IsClosedAndEvidenceBound",
		"TestMonitoringProfilesBindClosedBudgetsAndRetention",
		"TestArchitectureV2ModuleInputBindingTargetMustBeDeclaredPublic",
		"TestArchitectureV2TimestampMatchesCanonicalRFC3339NanoUTC",
	},
}

func overSliceBudget(pattern string) bool {
	return len(sliceBudgetSlowTests[strings.TrimPrefix(pattern, "./")]) > 0
}

// budgetedPackageCommand is the package-slice fallback for a package over the
// slice budget. Tests that a public boundary requires stay in the run.
func budgetedPackageCommand(pattern string, required []string) testCommand {
	skip := sliceBudgetSlowTests[strings.TrimPrefix(pattern, "./")]
	for _, name := range required {
		skip = withoutString(skip, name)
	}
	args := []string{"go", "test", "-count=1", goTestTimeoutArg}
	if len(skip) > 0 {
		args = append(args, "-skip", exactTestRegex(skip))
	}
	return testCommand{
		Kind: "go", Scope: budgetedPackageScope, Argv: append(args, pattern),
		Reason: "run a changed package that is over the slice budget without its registered slow tests; they run when changed, bound, or required by a public boundary, and in mise run test:full",
	}
}
