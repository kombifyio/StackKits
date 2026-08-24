package main

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/productkits"
)

const planSchema = "kombify.stackkits/affected-test-plan/v1"

// focusedTestBatchSize bounds CUE-heavy changed-test execution without
// widening the existing 90-second process deadline. Each batch is also
// package-local so one package cannot retain compiler state while another
// package's focused tests run.
const focusedTestBatchSize = 8

const perKitTemplateParityTest = "TestPerKitTemplatesMatchCanonical"

const (
	kitInventoryParityTest   = "TestProductKitsMatchesCUEDerivedAuthorityProfiles"
	authorityBundleDriftTest = "TestEmbeddedAuthorityBundleHasNoSourceOrProjectionDrift"
)

// kitRoots are the directories whose contents define an active product kit.
var kitRoots = activeKitPaths("", "/")

// architectureAuthoritySources covers every input the embedded authority bundle
// declares in its own manifest sourceHashes. Keying on the "foundation/architecture_v2"
// name prefix missed foundation/application_lifecycle.cue, which is a bundle source
// too: changing it left the bundle and the canonical plan fixtures stale while
// the plan ran neither drift check.
var architectureAuthoritySources = []string{"foundation/", "cue.mod/"}

var coreCUERoots = append(
	[]string{"./foundation/..."},
	append(activeKitPaths("./", "/..."), "./addons/...", "./use-cases/...")...,
)

func activeKitPaths(prefix, suffix string) []string {
	slugs := productkits.Slugs()
	paths := make([]string, len(slugs))
	for index, slug := range slugs {
		paths[index] = prefix + slug + suffix
	}
	return paths
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
		"TestRunInitRoutesDevToEmbeddedV2BeforeLegacyDiscovery",
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
	ChangedTestTags      map[string][]string
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
	// Architecture v2 CUE changes must reach the embedded authority boundary and
	// keep the renderer buildable. A CUE-only slice therefore runs the named
	// bundle-drift behavior and compile-checks the renderer instead of executing
	// unrelated historical package tests. Direct Go changes retain their normal
	// changed-package or focused-test selection.
	if anyPathUnder(files, architectureAuthoritySources...) {
		const authorityPackage = "internal/architecturev2"
		const authorityPattern = "./" + authorityPackage
		if !anyGoPathUnder(files, authorityPackage+"/") {
			goSelection.Changed = sortedUnique(append(goSelection.Changed, authorityPattern))
			goSelection.CompileOnly = withoutString(goSelection.CompileOnly, authorityPattern)
			goSelection.Reverse = withoutString(goSelection.Reverse, authorityPattern)
			focusedTests[authorityPackage] = []string{authorityBundleDriftTest}
		} else if len(focusedTests[authorityPackage]) > 0 {
			focusedTests[authorityPackage] = sortedUnique(append(focusedTests[authorityPackage], authorityBundleDriftTest))
		}

		const rendererPackage = "internal/architecturev2renderer"
		const rendererPattern = "./" + rendererPackage
		if !anyGoPathUnder(files, rendererPackage+"/") {
			goSelection.Changed = withoutString(goSelection.Changed, rendererPattern)
			goSelection.Reverse = withoutString(goSelection.Reverse, rendererPattern)
			goSelection.CompileOnly = sortedUnique(append(goSelection.CompileOnly, rendererPattern))
		}
	}
	// A kit root holds the three documents that define what a kit IS: its
	// KitDefinition (stackfile.cue), its exported metadata (stackkit.yaml) and
	// its reality grading (mode_matrix.cue). Every parity test guarding them
	// lives elsewhere, so without this rule a kit change ran `cue vet` at best
	// and hygiene alone for the YAML — which is how cloud-kit once shipped a
	// byte copy of basement-kit's manifest.
	//
	// internal/cue is cheap and holds all of the kit metadata, mode-matrix and
	// document-identity parity tests, so it runs in full. internal/architecturev2
	// is the expensive package, so only the two assertions a kit change can
	// actually break are selected: the CUE-derived kit inventory parity and the
	// embedded authority bundle drift check, whose own mise task runs in no
	// workflow.
	if anyPathUnder(files, kitRoots...) {
		const cuePattern = "./internal/cue"
		goSelection.Changed = sortedUnique(append(goSelection.Changed, cuePattern))
		goSelection.CompileOnly = withoutString(goSelection.CompileOnly, cuePattern)
		goSelection.Reverse = withoutString(goSelection.Reverse, cuePattern)
		delete(focusedTests, "internal/cue")

		if !slicesContain(goSelection.Changed, "./internal/architecturev2") {
			goSelection.Changed = sortedUnique(append(goSelection.Changed, "./internal/architecturev2"))
			goSelection.CompileOnly = withoutString(goSelection.CompileOnly, "./internal/architecturev2")
			goSelection.Reverse = withoutString(goSelection.Reverse, "./internal/architecturev2")
			focusedTests["internal/architecturev2"] = sortedUnique(append(
				focusedTests["internal/architecturev2"],
				kitInventoryParityTest, authorityBundleDriftTest,
			))
		}
	}
	// Per-kit templates are generated copies of foundation/templates. Editing either
	// side is the only way they can diverge, and nothing else in the plan would
	// notice: template files are not Go and not CUE, so they would otherwise
	// classify as unknown and receive hygiene checks only.
	if anyPathUnder(files, "foundation/templates/", "basement-kit/templates/", "cloud-kit/templates/") {
		const templatesPattern = "./internal/kittemplates"
		goSelection.Changed = sortedUnique(append(goSelection.Changed, templatesPattern))
		goSelection.CompileOnly = withoutString(goSelection.CompileOnly, templatesPattern)
		goSelection.Reverse = withoutString(goSelection.Reverse, templatesPattern)
		focusedTests["internal/kittemplates"] = sortedUnique(append(
			focusedTests["internal/kittemplates"],
			perKitTemplateParityTest,
		))
	}
	goPatterns := sortedUnique(append(append(append([]string(nil), goSelection.Changed...), goSelection.CompileOnly...), goSelection.Reverse...))
	classes.GoPackages = append([]string(nil), goPatterns...)
	commands = append(commands, affectedGoCommands(goSelection, focusedTests, input.ChangedTestTags)...)

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

func slicesContain(values []string, want string) bool {
	index := sort.SearchStrings(values, want)
	return index < len(values) && values[index] == want
}

// anyPathUnder reports whether any changed file lives under one of the given
// directory prefixes. Paths are repository-relative and slash-separated.
func anyPathUnder(files []string, prefixes ...string) bool {
	for _, file := range files {
		for _, prefix := range prefixes {
			if strings.HasPrefix(file, prefix) {
				return true
			}
		}
	}
	return false
}

func anyGoPathUnder(files []string, prefix string) bool {
	for _, file := range files {
		if strings.HasPrefix(file, prefix) && strings.HasSuffix(file, ".go") {
			return true
		}
	}
	return false
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
			case "foundation", "base", "cue.mod", "schemas", "architecture", "addons", "platforms":
				result.CUEShared = true
				known = true
			case "modules":
				if len(parts) > 1 {
					modules[parts[1]] = struct{}{}
					known = true
				}
			default:
				if productkits.IsActive(top) {
					kits[top] = struct{}{}
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
	if file == ".goreleaser.yaml" || file == "install.sh" || file == "Dockerfile" || file == "scripts/sync-public.sh" ||
		file == "scripts/dev/architecture-v2-generation.mjs" ||
		file == "internal/releaseindex/release-trust-policy.json" {
		return true
	}
	return strings.HasPrefix(file, "scripts/release/") ||
		strings.HasPrefix(file, "scripts/public/") ||
		strings.HasPrefix(file, ".github/workflows/")
}

type affectedGoSelection struct {
	Changed     []string
	TestOnly    []string
	CompileOnly []string
	Reverse     []string
}

func affectedGoSelectionFor(files []string, packages []goPackage, maxReverse int) affectedGoSelection {
	dirToPackage := map[string]goPackage{}
	changedImports := map[string]struct{}{}
	changedPatterns := map[string]struct{}{}
	generatedOnlyPatterns := map[string]bool{}
	testOnlyPatterns := map[string]bool{}
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
			testOnlyPatterns[pattern] = true
		}
		if embeddedReleaseTrustPolicy || strings.HasSuffix(file, "_test.go") || !isGeneratedGoProjection(file) {
			generatedOnlyPatterns[pattern] = false
		}
		if embeddedReleaseTrustPolicy || !strings.HasSuffix(file, "_test.go") {
			testOnlyPatterns[pattern] = false
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
	testPatterns := map[string]struct{}{}
	for pattern, generatedOnly := range generatedOnlyPatterns {
		if generatedOnly {
			delete(changedPatterns, pattern)
			compileOnlyPatterns[pattern] = struct{}{}
			delete(reversePatterns, pattern)
		} else if testOnlyPatterns[pattern] {
			delete(changedPatterns, pattern)
			testPatterns[pattern] = struct{}{}
			delete(reversePatterns, pattern)
		}
	}
	return affectedGoSelection{
		Changed: sortedKeys(changedPatterns), TestOnly: sortedKeys(testPatterns),
		CompileOnly: sortedKeys(compileOnlyPatterns), Reverse: sortedKeys(reversePatterns),
	}
}

func isGeneratedGoProjection(file string) bool {
	return strings.HasSuffix(file, "_gen.go") || strings.HasSuffix(file, "_generated.go")
}

func affectedGoPatterns(files []string, packages []goPackage, maxReverse int) []string {
	selection := affectedGoSelectionFor(files, packages, maxReverse)
	return sortedUnique(append(append(selection.Changed, selection.CompileOnly...), selection.Reverse...))
}

func affectedGoCommands(selection affectedGoSelection, changedTests, changedTestTags map[string][]string) []testCommand {
	type focusedSelection struct {
		pattern string
		tests   []string
		tags    []string
	}
	focusedSelections := []focusedSelection{}
	fullPatterns := []string{}
	compileOnlyPatterns := append([]string(nil), selection.CompileOnly...)
	taggedCompilePatterns := map[string][]string{}
	selectTests := func(pattern string, fallback *[]string) {
		dir := strings.TrimPrefix(pattern, "./")
		if dir == "." {
			dir = "."
		}
		tests := sortedUnique(changedTests[dir])
		if len(tests) == 0 {
			if tags := sortedUnique(changedTestTags[dir]); len(tags) > 0 {
				key := strings.Join(tags, ",")
				taggedCompilePatterns[key] = append(taggedCompilePatterns[key], pattern)
				return
			}
			*fallback = append(*fallback, pattern)
			return
		}
		focusedSelections = append(focusedSelections, focusedSelection{pattern: pattern, tests: tests, tags: sortedUnique(changedTestTags[dir])})
	}
	for _, pattern := range selection.Changed {
		selectTests(pattern, &fullPatterns)
	}
	for _, pattern := range selection.TestOnly {
		selectTests(pattern, &compileOnlyPatterns)
	}

	commands := []testCommand{}
	for _, focused := range focusedSelections {
		for start := 0; start < len(focused.tests); start += focusedTestBatchSize {
			end := min(start+focusedTestBatchSize, len(focused.tests))
			batch := focused.tests[start:end]
			args := []string{"go", "test", "-count=1", "-timeout=90s"}
			if len(focused.tags) > 0 {
				args = append(args, "-tags", strings.Join(focused.tags, ","))
			}
			args = append(args, "-run", exactTestRegex(batch))
			args = append(args, focused.pattern)
			commands = append(commands, testCommand{
				Kind: "go", Scope: "changed-test-functions", Argv: args,
				Reason: "compile one changed package and run a bounded batch of only test functions changed in this slice",
			})
		}
	}
	if len(fullPatterns) > 0 {
		args := []string{"go", "test", "-count=1", "-timeout=90s"}
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-packages", Argv: append(args, fullPatterns...),
			Reason: "run changed packages that have no changed test-function boundary",
		})
	}
	if len(compileOnlyPatterns) > 0 {
		args := []string{"go", "test", "-count=1", "-timeout=90s", "-run", "^$"}
		args = append(args, sortedUnique(compileOnlyPatterns)...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-compile", Argv: args,
			Reason: "compile changed generated authority or test-only packages without running unrelated historical tests",
		})
	}
	tagSets := make([]string, 0, len(taggedCompilePatterns))
	for tags := range taggedCompilePatterns {
		tagSets = append(tagSets, tags)
	}
	sort.Strings(tagSets)
	for _, tags := range tagSets {
		args := []string{"go", "test", "-count=1", "-timeout=90s", "-tags", tags, "-run", "^$"}
		args = append(args, sortedUnique(taggedCompilePatterns[tags])...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-compile", Argv: args,
			Reason: "compile changed build-tagged test packages without running unrelated tests",
		})
	}
	if len(selection.Reverse) > 0 {
		args := []string{"go", "test", "-count=1", "-timeout=90s", "-run", "^$"}
		args = append(args, selection.Reverse...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "reverse-dependent-compile", Argv: args,
			Reason: "compile bounded direct reverse dependents without running unrelated test suites",
		})
	}
	return commands
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
