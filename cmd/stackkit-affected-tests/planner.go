package main

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

const planSchema = "kombify.stackkits/affected-test-plan/v1"

// focusedTestBatchSize bounds CUE-heavy changed-test execution without
// widening the existing 90-second process deadline. Each batch is also
// package-local so one package cannot retain compiler state while another
// package's focused tests run.
const focusedTestBatchSize = 8

const basementCatalogRendererTest = "TestProductBasementCoreFactoryAdmitsGeneratedStandardTarget"

var coreCUERoots = []string{
	"./base/...",
	"./basement-kit/...",
	"./cloud-kit/...",
	"./modern-homelab/...",
	"./addons/...",
}

// fileFocusedTests keeps focused production and shared-fixture slices explicit
// for packages whose historical full test suite is not a useful beta feedback
// gate. Adding or renaming a test in one of these slices must update this
// reviewable binding.
var fileFocusedTests = map[string][]string{
	"internal/backuplifecycle/snapshot_anchor_store.go": {
		"TestNativeV2LocalBackupConfigureAndSnapshotAnchor",
	},
	"internal/backupcustody/permissions_other.go": {
		"TestRequirePrivatePathAcceptsOnlyExpectedPrivateTypeAndMode",
	},
	"cmd/stackkit/commands/backup.go": {
		"TestBackupCommandContract",
		"TestLegacyV06BackupMigrationForwardsExactImporterArguments",
		"TestNativeV2RetiresLegacyBackupUtilityCommandsBeforeSideEffects",
		"TestNativeV2BackupCommandFailsBeforeSideEffectsOnTamperedAuthority",
		"TestBackupEmergencyExportWritesManifestAndRunbook",
		"TestBackupEmergencyExportRejectsUnsupportedFormat",
		"TestHumanSize",
		"TestTruncateBackup",
	},
	"cmd/stackkit/commands/backup_native_v2.go": {
		"TestNativeV2BackupCommandFailsBeforeSideEffectsOnTamperedAuthority",
		"TestNativeV2BackupRequestForwardsNormalizedRunAndRestoreAuthority",
		"TestNativeV2BackupRequestRejectsAuthorityMutationBetweenInspections",
	},
	"cmd/stackkit/commands/backup_native_v2_runtime.go": {
		"TestContinueNativeV2BackupProductionMapsEveryOperationToLifecycleInput",
		"TestNativeV2BackupOperationContextCapsDeadlineAtFifteenMinutes",
	},
	"cmd/stackkit/commands/backup_activation.go": {
		"TestNativeV2BackupCommandSurface",
		"TestApplicationLifecycleBackupRequiresSelectedAdapterSupport",
	},
	"cmd/stackkit/commands/init_architecture_v2.go": {
		"TestRunArchitectureV2InitMaterializesCanonicalProductSpecs",
		"TestRunArchitectureV2InitNormalizesWorkspaceNameAndHonorsExplicitName",
		"TestRunInitRoutesDevToEmbeddedV2BeforeLegacyDiscovery",
		"TestRunArchitectureV2InitFailsBeforeWriteForMissingRequiredDomain",
		"TestRunArchitectureV2InitRejectsLegacySemanticsAndLocalPathsBeforeWrite",
		"TestRunArchitectureV2InitUsesExpectedHashCASAndRejectsForce",
		"TestRunArchitectureV2InitUsesExistingSpecAliasWithoutCreatingSecondAuthority",
	},
	"cmd/stackkit/commands/wizard.go": {
		"TestPublicCommandTreeExcludesPublisherOperations",
	},
	"cmd/stackkit/commands/wizard_test.go": {
		"TestPublicCommandTreeExcludesPublisherOperations",
	},
	"cmd/stackkit/commands/publisher_boundary_publisher_test.go": {
		"TestPublicCommandTreeExcludesPublisherOperations",
	},
	"internal/resolvedplan/identity_trust.go": {
		"TestBuildResolvedIdentityTrustBindsGraphToStackAndExactSites",
		"TestBuildResolvedIdentityTrustLowersBasementWithoutCloudDistribution",
		"TestBuildResolvedIdentityTrustRejectsGraphAndAuthorityDrift",
		"TestBuildResolvedIdentityTrustSupportsExternalAuthorityWithoutExternalIssuance",
	},
	"internal/architecturev2/generation_execution_test.go": {
		"TestMaterializeInitialStackSpecUsesEmbeddedDefinitionAuthority",
	},
	"internal/architecturev2/output_transaction.go": {
		"TestRetiredOutputGCContractIsExplicitAndTwoPhase",
	},
	"internal/runtimeexecutorlocal/modern_identity_site_policy_test.go": {
		"TestModernIdentitySiteExecutorsKeepHomeAndCloudAuthoritySeparate",
		"TestModernIdentitySiteExecutorsRejectCrossSiteAndChannelSubstitution",
	},
	"internal/localevidence/executor_state_signature.go": {
		"TestOwnerExecutorStateSignatureIsDomainSeparatedAndOwnerBound",
	},
	"internal/localevidence/lifecycle_mutation_signature.go": {
		"TestOwnerLifecycleMutationSignatureRejectsTamper",
	},
	"internal/lifecyclemutation/journal.go": {
		"TestJournalSerializesMutationAndAdmitsOnlyExactJoinedPhase",
		"TestJournalRejectsReplayedSignedPhaseAndRepairsOnlyExplicitRecovery",
		"TestJournalFailsClosedOnDivergentImmutableHead",
		"TestJournalMissingPointerBlocksOrdinaryMutationUntilExplicitRecovery",
		"TestJournalExplicitRecoveryRepairsTerminalAndPriorPointerCrashWindows",
		"TestJournalIgnoresOnlyPrivateBoundedAtomicTemporaryArtifacts",
		"TestJournalExplicitRecoveryOwnsClaimRaceAndRejectsClaimedChild",
	},
	"internal/stackspecintent/store.go": {
		"TestPersistUsesCanonicalExpectedHashCAS",
		"TestPersistRejectsMissingInvalidAndLegacyTargetsWithoutCandidateWrite",
		"TestPersistRejectsPathEscapeBeforeWrite",
		"TestPersistFailsFastOnSharedWriterLockWithoutTargetMutation",
		"TestPersistRejectsSymlinkTargetWithoutFollowingIt",
	},
	"internal/upgradelifecycle/executor_state.go": {
		"TestExecutorStateStoreCapturesVerifiableIdempotentComposeRecoveryClosure",
		"TestExecutorStateStoreRejectsRuntimeComposeDriftBeforePersistence",
		"TestExecutorStateStoreRejectsConflictingOperationAndUnsupportedTarget",
		"TestExecutorStateStoreRejectsUnverifiedInputsBeforeCommit",
		"TestExecutorStateStoreUsesOperationMarkerAsCommitPoint",
		"TestExecutorStateStoreFailsFastWhileStoreLockIsHeld",
		"TestExecutorStateStoreRejectsPreexistingTornNamedCASObject",
	},
	"internal/upgradelifecycle/recovery.go": {
		"TestExecutorStateStoreRecoverRestoresAuthorityAndInvokesExactExecutable",
		"TestExecutorStateStoreRecoverRejectsTamperBeforeWritesOrCallback",
	},
	"internal/architecturev2/apply_result_verification.go": {
		"TestExecuteProductApplyCollectsFreshEvidenceThroughConstructionOwnedRuntimeGraph",
	},
	"internal/backupexec/docker.go": {
		"TestDockerV2Adapter",
	},
	"internal/resolvedplan/module_input_bindings.go": {
		"TestHomeBackupBindingProjectsOnlyLocalBackupRoot",
	},
	"internal/releaseindex/installed.go": {
		"TestInspectInstalledReturnsReverifiedCallbackScopedProof",
	},
	"cmd/stackkit/commands/release_commands_test.go": {
		"TestPublicUpgradeDryRunResolvesWithoutInstalling",
		"TestPublicUpgradeInstallAndOfflineVerifyUseSameReceipt",
		"TestKitListUsesPublishedReleaseIndex",
	},
	"cmd/stackkit/commands/upgrade_transaction.go": {
		"TestPublicUpgradeTransactionCommitsOnlyExactLiveVerifiedTarget",
		"TestPublicUpgradeTransactionFailureRestoresPriorRuntimeWithDataStaged",
		"TestPublicUpgradeTransactionTamperedCheckpointStopsBeforeTargetSideEffects",
		"TestPublicUpgradeSuccessProofIsRemovedBeforeRollback",
	},
	"cmd/stackkit/commands/upgrade_recover.go": {
		"TestPublicUpgradeExplicitRecoveryNeverReplaysAmbiguousChild",
	},
	"cmd/stackkit/commands/lifecycle_mutation.go": {
		"TestPublicUpgradeTransactionCommitsOnlyExactLiveVerifiedTarget",
		"TestPublicUpgradeExplicitRecoveryNeverReplaysAmbiguousChild",
	},
	"cmd/stackkit/commands/drift.go": {
		"TestDriftReconcileModesDenyBeforeLifecycleSideEffects",
	},
}

var defaultReleaseContractSmokeTests = []string{
	"scripts/release/release-evidence.test.mjs",
	"scripts/release/render-release-index.test.mjs",
	"scripts/release/release-trust-workflow.test.mjs",
	"scripts/release/verify-trusted-root.test.mjs",
	"scripts/release/validate-release-archives.test.mjs",
	"scripts/release/check-fast-feedback-budget.test.mjs",
	"scripts/public/export-public-verification.test.mjs",
}

// releaseTestBindings keeps release feedback producer-specific. Directly
// changed *.test.mjs files select themselves; this table binds producers and
// declarative inputs to the smallest contract tests that own them. Unbound
// release paths retain the bounded historical smoke as a conservative fallback.
var releaseTestBindings = map[string][]string{
	".goreleaser.yaml": {
		"scripts/release/validate-release-archives.test.mjs",
	},
	"install.sh": {
		"scripts/release/install-latest-resolution.test.mjs",
	},
	"mise.toml": {
		"scripts/release/check-fast-feedback-budget.test.mjs",
	},
	"scripts/dev/architecture-v2-generation.mjs": {
		"scripts/dev/architecture-v2-generation.test.mjs",
	},
	".github/workflows/ci.yml": {
		"scripts/public/public-surface-policy.test.mjs",
	},
	".github/workflows/ci-fast.yml": {
		"scripts/release/check-fast-feedback-budget.test.mjs",
	},
	".github/workflows/delivery.yml": {
		"scripts/release/check-fast-feedback-budget.test.mjs",
	},
	".github/workflows/deployment-standards-gate.yml": {
		"scripts/release/check-fast-feedback-budget.test.mjs",
	},
	".github/workflows/os-matrix.yml": {
		"scripts/release/validate-os-matrix.test.mjs",
	},
	".github/workflows/publish-oss.yml": {
		"scripts/public/public-surface-policy.test.mjs",
		"scripts/release/check-fast-feedback-budget.test.mjs",
		"scripts/release/publish-oss-phase-boundary.test.mjs",
		"scripts/release/release-evidence.private.test.mjs",
		"scripts/release/release-trust-workflow.test.mjs",
	},
	"scripts/public/assert-safe-export-destination.mjs": {
		"scripts/public/assert-safe-export-destination.test.mjs",
	},
	"scripts/public/check-public-cli-boundary.mjs": {
		"scripts/public/check-public-cli-boundary.test.mjs",
	},
	"scripts/public/export-manifest.txt": {
		"scripts/public/public-surface-policy.test.mjs",
	},
	"scripts/public/export-public.ps1": {
		"scripts/public/export-public-verification.test.mjs",
	},
	"scripts/public/export-public.sh": {
		"scripts/public/export-public-verification.test.mjs",
	},
	"scripts/public/public-export-transaction.mjs": {
		"scripts/public/public-export-transaction.test.mjs",
	},
	"scripts/public/public-surface-policy.json": {
		"scripts/public/public-surface-policy.test.mjs",
	},
	"scripts/public/public-surface-policy.mjs": {
		"scripts/public/public-surface-policy.test.mjs",
	},
	"scripts/public/workflows/release.yml": {
		"scripts/release/check-fast-feedback-budget.test.mjs",
		"scripts/release/release-evidence.private.test.mjs",
		"scripts/release/release-trust-workflow.test.mjs",
	},
	"scripts/public/workflows/publish-image.yml": {
		"scripts/release/check-fast-feedback-budget.test.mjs",
		"scripts/release/release-trust-workflow.test.mjs",
	},
	"scripts/public/workflows/ci.yml": {
		"scripts/public/public-surface-policy.test.mjs",
	},
	"scripts/release/changelog.mjs": {
		"scripts/release/changelog.test.mjs",
	},
	"scripts/release/check-ci-race-shards.mjs": {
		"scripts/release/check-ci-race-shards.test.mjs",
	},
	"scripts/release/check-fast-feedback-budget.mjs": {
		"scripts/release/check-fast-feedback-budget.test.mjs",
	},
	"scripts/release/check-fast-numeric-delivery.mjs": {
		"scripts/release/check-fast-feedback-budget.test.mjs",
	},
	"scripts/release/check-l3-paas-contract.mjs": {
		"scripts/release/check-l3-paas-contract.test.mjs",
	},
	"scripts/release/check-node24-actions.mjs": {
		"scripts/release/check-node24-actions.test.mjs",
	},
	"scripts/release/check-timeout-budget.mjs": {
		"scripts/release/check-timeout-budget.test.mjs",
	},
	"scripts/release/exact-sha-release-gate.mjs": {
		"scripts/release/exact-sha-release-gate.test.mjs",
	},
	"scripts/release/finalize-release-dist.sh": {
		"scripts/release/finalize-release-dist.test.mjs",
	},
	"scripts/release/live-website-headers.mjs": {
		"scripts/release/live-website-headers.test.mjs",
	},
	"scripts/release/render-exact-sha-deploy.mjs": {
		"scripts/release/render-exact-sha-deploy.test.mjs",
	},
	"scripts/release/render-release-evidence.mjs": {
		"scripts/release/release-evidence.test.mjs",
	},
	"scripts/release/render-release-index.mjs": {
		"scripts/release/render-release-index.test.mjs",
	},
	"scripts/release/verify-trusted-root.mjs": {
		"scripts/release/verify-trusted-root.test.mjs",
	},
	"internal/releaseindex/release-trust-policy.json": {
		"scripts/release/release-trust-workflow.test.mjs",
		"scripts/release/verify-trusted-root.test.mjs",
	},
	"scripts/release/validate-os-matrix.mjs": {
		"scripts/release/validate-os-matrix.test.mjs",
	},
	"scripts/release/validate-release-archives.sh": {
		"scripts/release/validate-release-archives.test.mjs",
	},
	"scripts/release/validate-release-evidence.mjs": {
		"scripts/release/validate-release-evidence.test.mjs",
	},
	"scripts/release/validate-scenario-artifact.mjs": {
		"scripts/release/validate-scenario-artifact.test.mjs",
	},
	"scripts/release/verify-release-attestations.mjs": {
		"scripts/release/verify-release-attestations.test.mjs",
	},
}

type goPackage struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

type plannerInput struct {
	BaseRef              string
	MergeBase            string
	ChangedFiles         []string
	CoreCUERoots         []string
	GoPackages           []goPackage
	MaxReverse           int
	GoListWarning        string
	ChangedTests         map[string][]string
	TestDiscoveryWarning string
}

type classification struct {
	GoPackages        []string `json:"goPackages,omitempty"`
	GoShared          bool     `json:"goShared,omitempty"`
	CUEModules        []string `json:"cueModules,omitempty"`
	CUEKits           []string `json:"cueKits,omitempty"`
	CUEShared         bool     `json:"cueShared,omitempty"`
	Website           bool     `json:"website,omitempty"`
	OpenAPIProjection bool     `json:"openAPIProjection,omitempty"`
	ReleaseE2E        bool     `json:"releaseE2E,omitempty"`
	ReleaseGeneral    bool     `json:"releaseGeneral,omitempty"`
	Docs              bool     `json:"docs,omitempty"`
	Unknown           []string `json:"unknown,omitempty"`
}

type testCommand struct {
	Kind   string   `json:"kind"`
	Scope  string   `json:"scope"`
	Argv   []string `json:"argv"`
	Reason string   `json:"reason"`
}

type testPlan struct {
	SchemaVersion  string         `json:"schemaVersion"`
	BaseRef        string         `json:"baseRef"`
	MergeBase      string         `json:"mergeBase"`
	ChangedFiles   []string       `json:"changedFiles"`
	Classification classification `json:"classification"`
	Commands       []testCommand  `json:"commands"`
	Warnings       []string       `json:"warnings,omitempty"`
}

func buildPlan(input plannerInput) testPlan {
	maxReverse := input.MaxReverse
	if maxReverse < 0 {
		maxReverse = 0
	}

	files := sortedUnique(normalizePaths(input.ChangedFiles))
	classes := classifyFiles(files)
	coreRoots := input.CoreCUERoots
	if coreRoots == nil {
		coreRoots = coreCUERoots
	}
	commands := []testCommand{{
		Kind:   "hygiene",
		Scope:  "changed-files",
		Argv:   []string{"git", "diff", "--check", input.MergeBase, "--"},
		Reason: "catch whitespace errors only in the candidate diff",
	}}
	if productBoundaryRelevant(files) {
		commands = append(commands, testCommand{
			Kind:   "release",
			Scope:  "stackkits-product-boundary",
			Argv:   []string{"node", "scripts/release/check-product-boundaries.mjs"},
			Reason: "reject Wizard/scoring ownership drift, external Standard Mode dependencies, provider credentials, and State Console lifecycle duplication",
		})
	}

	goSelection := affectedGoSelectionFor(files, input.GoPackages, maxReverse)
	if classes.GoShared {
		changed := make(map[string]struct{}, len(goSelection.Changed))
		for _, pattern := range goSelection.Changed {
			changed[pattern] = struct{}{}
		}
		for _, anchor := range []string{"./internal/architecturev2", "./internal/resolvedplan"} {
			if _, alreadyChanged := changed[anchor]; !alreadyChanged {
				goSelection.Reverse = append(goSelection.Reverse, anchor)
			}
		}
		goSelection.Reverse = sortedUnique(goSelection.Reverse)
	}
	focusedTests := focusedGoTests(files, input.ChangedTests)
	if slicesContain(files, "base/architecture_v2_catalog.cue") {
		const rendererPattern = "./internal/architecturev2"
		goSelection.Changed = sortedUnique(append(goSelection.Changed, rendererPattern))
		goSelection.CompileOnly = withoutString(goSelection.CompileOnly, rendererPattern)
		goSelection.Reverse = withoutString(goSelection.Reverse, rendererPattern)
		focusedTests["internal/architecturev2"] = sortedUnique(append(
			focusedTests["internal/architecturev2"],
			basementCatalogRendererTest,
		))
	}
	goPatterns := sortedUnique(append(append(append([]string(nil), goSelection.Changed...), goSelection.CompileOnly...), goSelection.Reverse...))
	classes.GoPackages = append([]string(nil), goPatterns...)
	commands = append(commands, affectedGoCommands(goSelection, focusedTests)...)

	if classes.CUEShared {
		commands = append(commands, testCommand{
			Kind:   "cue",
			Scope:  "shared-contract-and-core-consumers",
			Argv:   append([]string{"cue", "vet", "-c=false"}, coreRoots...),
			Reason: "shared CUE schemas can affect each core kit but intentionally remain incomplete until bound to concrete plans",
		})
	} else if len(classes.CUEKits) > 0 {
		args := []string{"cue", "vet"}
		for _, kit := range classes.CUEKits {
			args = append(args, "./"+kit+"/...")
		}
		commands = append(commands, testCommand{
			Kind:   "cue",
			Scope:  "changed-kits",
			Argv:   args,
			Reason: "validate only changed kit roots",
		})
	}
	if len(classes.CUEModules) > 0 {
		args := []string{"cue", "vet", "-c=false"}
		for _, module := range classes.CUEModules {
			args = append(args, "./modules/"+module+"/...")
		}
		commands = append(commands, testCommand{
			Kind:   "cue",
			Scope:  "changed-modules",
			Argv:   args,
			Reason: "validate only changed module slugs",
		})
	}

	if classes.Website {
		commands = append(commands,
			testCommand{
				Kind:   "website",
				Scope:  "source",
				Argv:   []string{"npm", "--prefix", "website", "run", "check"},
				Reason: "type-check and validate website source without reinstalling or building",
			},
			testCommand{
				Kind:   "website",
				Scope:  "public-boundary",
				Argv:   []string{"node", "scripts/release/check-website.mjs", "source"},
				Reason: "validate the private/public website source boundary",
			},
		)
	}
	if classes.OpenAPIProjection {
		commands = append(commands, testCommand{
			Kind:   "contract",
			Scope:  "stackaction-generated-openapi",
			Argv:   []string{"go", "run", "./internal/contractgen/stackactiongen/cmd", "-repo-root", ".", "-check"},
			Reason: "verify canonical and website OpenAPI projections without installing unrelated website tooling",
		})
	}
	if classes.ReleaseE2E {
		commands = append(commands, testCommand{
			Kind:  "release",
			Scope: "release-e2e-contracts",
			Argv: []string{
				"node", "--test",
				"scripts/e2e/validate-standalone-oss-e2e.test.mjs",
				"scripts/e2e/validate-standalone-runtime-e2e.test.mjs",
			},
			Reason: "run only the standalone OSS and runtime E2E evidence contracts",
		})
	}
	if classes.ReleaseGeneral {
		tests := affectedReleaseTests(files)
		commands = append(commands, testCommand{
			Kind:   "release",
			Scope:  "release-contract-smoke",
			Argv:   append([]string{"node", "--test"}, tests...),
			Reason: "run only release contract tests bound to changed producers and declarative inputs",
		})
	}

	warnings := []string{}
	if input.GoListWarning != "" {
		warnings = append(warnings, input.GoListWarning)
	}
	if input.TestDiscoveryWarning != "" {
		warnings = append(warnings, input.TestDiscoveryWarning)
	}
	if len(classes.Unknown) > 0 {
		warnings = append(warnings, "unknown paths receive hygiene checks only; the planner never falls back to go test ./...")
	}
	if len(files) == 0 {
		warnings = append(warnings, "no changes relative to the selected merge base or in the working tree")
	}

	return testPlan{
		SchemaVersion:  planSchema,
		BaseRef:        input.BaseRef,
		MergeBase:      input.MergeBase,
		ChangedFiles:   files,
		Classification: classes,
		Commands:       commands,
		Warnings:       warnings,
	}
}

func productBoundaryRelevant(files []string) bool {
	for _, file := range files {
		if file == "architecture-snapshot.json" ||
			file == "architecture-snapshot.schema.json" ||
			file == "scripts/build-architecture-snapshot.py" ||
			file == "scripts/release/check-product-boundaries.mjs" ||
			strings.HasPrefix(file, "schemas/") ||
			strings.HasPrefix(file, "cmd/stackkit/") ||
			strings.HasPrefix(file, "internal/stackkitmcp/") ||
			strings.HasPrefix(file, "mcp-use/stackkits-app/") {
			return true
		}
	}
	return false
}

func slicesContain(values []string, want string) bool {
	index := sort.SearchStrings(values, want)
	return index < len(values) && values[index] == want
}

func withoutString(values []string, unwanted string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != unwanted {
			result = append(result, value)
		}
	}
	return result
}

func focusedGoTests(files []string, changedTests map[string][]string) map[string][]string {
	result := make(map[string][]string, len(changedTests))
	for dir, names := range changedTests {
		result[dir] = append([]string(nil), names...)
	}
	for _, file := range files {
		tests := fileFocusedTests[file]
		if len(tests) == 0 {
			continue
		}
		dir := path.Dir(file)
		result[dir] = append(result[dir], tests...)
	}
	for dir, names := range result {
		result[dir] = sortedUnique(names)
	}
	return result
}

func affectedReleaseTests(files []string) []string {
	selected := map[string]struct{}{}
	needsFallback := false
	productBoundaryTaskChange := slicesContain(files, "mise.toml") &&
		slicesContain(files, "scripts/release/check-product-boundaries.mjs")
	for _, file := range files {
		if !isGeneralReleasePath(file) {
			continue
		}
		if strings.HasSuffix(file, ".test.mjs") {
			selected[file] = struct{}{}
			continue
		}
		if file == "mise.toml" && productBoundaryTaskChange {
			selected["scripts/release/check-product-boundaries.test.mjs"] = struct{}{}
			selected["scripts/release/check-fast-feedback-budget.test.mjs"] = struct{}{}
			continue
		}
		if tests, ok := releaseTestBindings[file]; ok {
			for _, test := range tests {
				selected[test] = struct{}{}
			}
			continue
		}
		needsFallback = true
	}
	if needsFallback {
		for _, test := range defaultReleaseContractSmokeTests {
			selected[test] = struct{}{}
		}
	}
	return sortedKeys(selected)
}

func classifyFiles(files []string) classification {
	result := classification{}
	modules := map[string]struct{}{}
	kits := map[string]struct{}{}
	unknown := map[string]struct{}{}

	for _, file := range files {
		parts := strings.Split(file, "/")
		top := parts[0]
		known := false

		if strings.HasSuffix(file, ".go") {
			known = true
		}
		if file == "go.mod" || file == "go.sum" {
			result.GoShared = true
			known = true
		}

		if strings.HasSuffix(file, ".cue") {
			switch top {
			case "base", "cue.mod", "schemas", "architecture", "addons", "platforms":
				result.CUEShared = true
				known = true
			case "basement-kit", "cloud-kit", "modern-homelab":
				kits[top] = struct{}{}
				known = true
			case "modules":
				if len(parts) > 1 {
					modules[parts[1]] = struct{}{}
					known = true
				}
			}
		}

		if file == "api/openapi/stackkits-v1.yaml" || file == "website/public/api/openapi.v1.yaml" {
			result.OpenAPIProjection = true
			known = true
		} else if top == "website" {
			result.Website = true
			known = true
		}
		if top == "docs" || file == "README.md" || file == "CONTRIBUTING.md" || file == "CHANGELOG.md" || file == "ROADMAP.md" || file == "STATUS.md" {
			result.Docs = true
			known = true
		}
		if isReleaseE2EPath(file) {
			result.ReleaseE2E = true
			known = true
		}
		if isGeneralReleasePath(file) {
			result.ReleaseGeneral = true
			known = true
		}

		if !known {
			unknown[file] = struct{}{}
		}
	}

	result.CUEModules = sortedKeys(modules)
	result.CUEKits = sortedKeys(kits)
	result.Unknown = sortedKeys(unknown)
	return result
}

func isReleaseE2EPath(file string) bool {
	return strings.HasPrefix(file, "scripts/e2e/") ||
		file == "schemas/standalone-oss-e2e-receipt.schema.json"
}

func isGeneralReleasePath(file string) bool {
	if file == ".goreleaser.yaml" || file == "install.sh" || file == "Dockerfile" || file == "mise.toml" || file == "scripts/sync-public.sh" ||
		file == "scripts/dev/architecture-v2-generation.mjs" || file == "scripts/dev/architecture-v2-generation.test.mjs" ||
		file == "internal/releaseindex/release-trust-policy.json" {
		return true
	}
	return strings.HasPrefix(file, "scripts/release/") ||
		strings.HasPrefix(file, "scripts/public/") ||
		strings.HasPrefix(file, ".github/workflows/")
}

type affectedGoSelection struct {
	Changed     []string
	CompileOnly []string
	Reverse     []string
}

func affectedGoSelectionFor(files []string, packages []goPackage, maxReverse int) affectedGoSelection {
	dirToPackage := map[string]goPackage{}
	changedImports := map[string]struct{}{}
	changedPatterns := map[string]struct{}{}
	generatedOnlyPatterns := map[string]bool{}
	reversePatterns := map[string]struct{}{}
	productionChange := map[string]struct{}{}

	for _, pkg := range packages {
		dir := strings.Trim(strings.ReplaceAll(pkg.Dir, "\\", "/"), "/")
		if dir == "" {
			dir = "."
		}
		pkg.Dir = dir
		dirToPackage[dir] = pkg
	}

	for _, file := range files {
		embeddedReleaseTrustPolicy := file == "internal/releaseindex/release-trust-policy.json"
		if !strings.HasSuffix(file, ".go") && !embeddedReleaseTrustPolicy {
			continue
		}
		dir := path.Dir(file)
		pattern := packagePattern(dir)
		changedPatterns[pattern] = struct{}{}
		if _, seen := generatedOnlyPatterns[pattern]; !seen {
			generatedOnlyPatterns[pattern] = true
		}
		if embeddedReleaseTrustPolicy || strings.HasSuffix(file, "_test.go") || !strings.HasSuffix(file, "_generated.go") {
			generatedOnlyPatterns[pattern] = false
		}
		if pkg, ok := dirToPackage[dir]; ok {
			changedImports[pkg.ImportPath] = struct{}{}
			if embeddedReleaseTrustPolicy || !strings.HasSuffix(file, "_test.go") {
				productionChange[pkg.ImportPath] = struct{}{}
			}
		}
	}

	if maxReverse > 0 && len(productionChange) > 0 {
		dependents := []string{}
		for _, pkg := range packages {
			if _, alreadyChanged := changedImports[pkg.ImportPath]; alreadyChanged {
				continue
			}
			if importsAny(pkg, productionChange) {
				dependents = append(dependents, packagePattern(pkg.Dir))
			}
		}
		dependents = sortedUnique(dependents)
		if len(dependents) > maxReverse {
			dependents = dependents[:maxReverse]
		}
		for _, dependent := range dependents {
			reversePatterns[dependent] = struct{}{}
		}
	}

	compileOnlyPatterns := map[string]struct{}{}
	for pattern, generatedOnly := range generatedOnlyPatterns {
		if generatedOnly {
			delete(changedPatterns, pattern)
			compileOnlyPatterns[pattern] = struct{}{}
			delete(reversePatterns, pattern)
		}
	}
	return affectedGoSelection{Changed: sortedKeys(changedPatterns), CompileOnly: sortedKeys(compileOnlyPatterns), Reverse: sortedKeys(reversePatterns)}
}

func affectedGoPatterns(files []string, packages []goPackage, maxReverse int) []string {
	selection := affectedGoSelectionFor(files, packages, maxReverse)
	return sortedUnique(append(append(selection.Changed, selection.CompileOnly...), selection.Reverse...))
}

func affectedGoCommands(selection affectedGoSelection, changedTests map[string][]string) []testCommand {
	type focusedSelection struct {
		pattern string
		tests   []string
	}
	focusedSelections := []focusedSelection{}
	fullPatterns := []string{}
	productionCompilePatterns := []string{}
	for _, pattern := range selection.Changed {
		if pattern == "./tests/production" {
			productionCompilePatterns = append(productionCompilePatterns, pattern)
			continue
		}
		dir := strings.TrimPrefix(pattern, "./")
		if dir == "." {
			dir = "."
		}
		tests := sortedUnique(changedTests[dir])
		if len(tests) == 0 {
			fullPatterns = append(fullPatterns, pattern)
			continue
		}
		focusedSelections = append(focusedSelections, focusedSelection{pattern: pattern, tests: tests})
	}

	commands := []testCommand{}
	for _, focused := range focusedSelections {
		for start := 0; start < len(focused.tests); start += focusedTestBatchSize {
			end := min(start+focusedTestBatchSize, len(focused.tests))
			batch := focused.tests[start:end]
			args := []string{"go", "test", "-count=1", "-timeout=90s", "-run", exactTestRegex(batch)}
			args = appendRequiredBuildTags(args, []string{focused.pattern})
			args = append(args, focused.pattern)
			commands = append(commands, testCommand{
				Kind: "go", Scope: "changed-test-functions", Argv: args,
				Reason: "compile one changed package and run a bounded batch of only test functions changed in this slice",
			})
		}
	}
	if len(fullPatterns) > 0 {
		args := appendRequiredBuildTags([]string{"go", "test", "-count=1", "-timeout=90s"}, fullPatterns)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-packages", Argv: append(args, fullPatterns...),
			Reason: "run changed packages that have no changed test-function boundary",
		})
	}
	if len(productionCompilePatterns) > 0 {
		args := []string{"go", "test", "-count=1", "-timeout=90s", "-run", "^$"}
		args = appendRequiredBuildTags(args, productionCompilePatterns)
		args = append(args, productionCompilePatterns...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "production-package-compile", Argv: args,
			Reason: "compile production-tagged live-test sources without running target-dependent tests in the fast path",
		})
	}
	if len(selection.CompileOnly) > 0 {
		args := []string{"go", "test", "-count=1", "-timeout=90s", "-run", "^$"}
		args = appendRequiredBuildTags(args, selection.CompileOnly)
		args = append(args, selection.CompileOnly...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-generated-compile", Argv: args,
			Reason: "compile changed generated authority without running unrelated historical package tests",
		})
	}
	if len(selection.Reverse) > 0 {
		args := []string{"go", "test", "-count=1", "-timeout=90s", "-run", "^$"}
		args = appendRequiredBuildTags(args, selection.Reverse)
		args = append(args, selection.Reverse...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "reverse-dependent-compile", Argv: args,
			Reason: "compile bounded direct reverse dependents without running unrelated test suites",
		})
	}
	return commands
}

func appendRequiredBuildTags(args, patterns []string) []string {
	for _, pattern := range patterns {
		if pattern == "./tests/production" {
			return append(args, "-tags", "production")
		}
	}
	return args
}

func exactTestRegex(names []string) string {
	names = sortedUnique(names)
	escaped := make([]string, 0, len(names))
	for _, name := range names {
		escaped = append(escaped, regexp.QuoteMeta(name))
	}
	return "^(" + strings.Join(escaped, "|") + ")$"
}

func importsAny(pkg goPackage, targets map[string]struct{}) bool {
	imports := make([]string, 0, len(pkg.Imports)+len(pkg.TestImports)+len(pkg.XTestImports))
	imports = append(imports, pkg.Imports...)
	imports = append(imports, pkg.TestImports...)
	imports = append(imports, pkg.XTestImports...)
	for _, imported := range imports {
		if _, ok := targets[imported]; ok {
			return true
		}
	}
	return false
}

func packagePattern(dir string) string {
	if dir == "." || dir == "" {
		return "."
	}
	return "./" + strings.Trim(strings.ReplaceAll(dir, "\\", "/"), "/")
}

func normalizePaths(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
		value = strings.TrimPrefix(value, "./")
		value = strings.Trim(value, "/")
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func sortedUnique(values []string) []string {
	set := map[string]struct{}{}
	for _, value := range values {
		if value != "" {
			set[value] = struct{}{}
		}
	}
	return sortedKeys(set)
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
