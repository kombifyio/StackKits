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
// letting one package retain compiler state while another package's
// focused tests run.
const focusedTestBatchSize = 8

// goTestTimeoutArg is the hang guard for one `go test` process. Full
// Architecture v2 catalog evaluation in ./internal/cue is past 90s after
// the Media workload; cmd/stackkit/commands compile plus a generate proof
// also exceeds 90s of test-binary time on a cold Windows cache.
// ./internal/resolvedplan with nested t.Parallel() exceeds 180s on 2-core
// CI when batched with ./internal/architecturev2renderer (PR 1075 Fast Gate).
const goTestTimeoutArg = "-timeout=300s"

const kitInventoryParityTest = "TestProductKitsMatchesCUEDerivedAuthorityProfiles"

// rendererRegistryParityTest proves every catalog render unit hash equals the
// production renderer registry. It lives in ./internal/architecturev2, which a
// renderer-only change leaves compile-only (#2195 merged a stale catalog hash).
const rendererRegistryParityTest = "TestModuleCatalogRenderUnitsAreRegisteredExactlyByTheProductionRegistry"

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

// Cross-package owners use the same explicit file-to-test selection, with the
// public test's package retained instead of assuming it lives beside the source.
var filePublicTestBoundaries = map[string]struct {
	Package string
	Tests   []string
	Tags    []string
}{
	// Documentation adapters execute their CUE-derived flow through the catalog
	// owner and deterministic CLI parity below; unrelated CLI journeys stay outside it.
	"cmd/stackkit/commands/federation_control_runtime.go": {
		Package: "cmd/stackkit/commands", Tests: []string{"TestFederationControlCLISignsExactHomeAction"},
	},
	"internal/architecturev2/federation_action.go": {
		Package: "cmd/stackkit/commands", Tests: []string{"TestFederationControlCLISignsExactHomeAction"},
	},
	"internal/resolvedplan/federation_action.go": {
		Package: "cmd/stackkit/commands", Tests: []string{"TestFederationControlCLISignsExactHomeAction"},
	},
	"internal/localevidence/remote_action_signature.go": {
		Package: "cmd/stackkit/commands", Tests: []string{"TestFederationControlCLISignsExactHomeAction"},
	},
	// OpenTofu workload roots (P1.1 follow-up W): the native standalone
	// Compose halves and the wrapper renderer are exercised through the
	// OpenTofu executor's public boundary.
}

var historicalSourceInspectionTests = []string{
	"TestHistoricalCloudSourceRequiresSignedApply",
	"TestMarketplaceUpgradeRequiresSignedApplyCheckpoint",
}

// fileFocusedTests keeps focused production and shared-fixture slices explicit
// for packages whose historical full test suite is not a useful beta feedback
// gate. Adding or renaming a test in one of these slices must update this
// reviewable binding.
var fileFocusedTests = map[string][]string{
	"cmd/stackkit/commands/address.go":         {},
	"cmd/stackkit/commands/address_session.go": {},
	"cmd/stackkit/commands/module_scaffold.go": {},
	"internal/usecasecatalog/truth_table.go":   {},
	// Capacity extraction changes only profile demand and numeric admission.
	// The unrelated historical full compiler suite exceeds the bounded gate
	// while repeatedly evaluating Modern topology policies under CUE.
	"internal/resolvedplan/module_profiles.go": {
		"TestNativeModuleProfileRejectsUnsupportedNodeArchitecture",
		"TestNativeComputeProfileRejectsUnmaterializedComponentGraph",
		"TestCatalogRejectsProfileResourceWithoutProvenance",
		"TestNativeUnrealizedProfilesCannotInheritModuleExecution",
		"TestNativeModuleReservationsBlockOvercommittedNode",
		"TestNativePartialAxisProfileKeepsModuleUnverified",
		"TestCatalogRejectsAxisProfilesWithoutComputeRealization",
		"TestModuleProfileDemandBindingRejectsRehashedTampering",
	},
	"internal/resolvedplan/modules.go": {
		"TestMissingInventoryFactsBlockApplyNotGeneration",
		"TestUndersizedInventoryFactsBlockApply",
		"TestAttestedInventoryFactsKeepApplyReady",
		"TestModuleRequiredInventoryFactsBlockUntilObserved",
		"TestCanonicalPlanRejectsForgedRuntimeAdmission",
	},
	"cmd/stackkit/commands/capacity.go":                        {},
	"cmd/stackkit/commands/machine_output.go":                  {},
	"cmd/stackkit/commands/apply_progress.go":                  {},
	"cmd/stackkit/commands/application_lifecycle_execution.go": {},
	// Source preparation owns public cache admission and historical Owner
	// inspection; recovery consumes that same verified cache and executable.
	"cmd/stackkit/commands/source_generation.go": {},
	// These seams authenticate immutable source data; full upgrade execution
	// remains in its own owners. Keep the shared bridge type's additive source
	// context on the same cache/Owner/Inventory/signature regression boundary.
	"cmd/stackkit/commands/cloud_session_source.go":     historicalSourceInspectionTests,
	"cmd/stackkit/commands/upgrade_attested_current.go": historicalSourceInspectionTests,
	"cmd/stackkit/commands/upgrade_beta4_bridge.go":     historicalSourceInspectionTests,
	// These reviewed setup consumers exercise the shared native admission and
	// Files/automatic callers plus the complete HA custody journey. They do not
	// qualify other native application branches or cover unrelated CLI sources.
	"cmd/stackkit/commands/setup_native.go":                           {},
	"cmd/stackkit/commands/setup_native_actions.go":                   {},
	"cmd/stackkit/commands/setup_home_assistant_connector.go":         {},
	"cmd/stackkit/commands/setup_home_assistant_connector_command.go": {},
	"cmd/stackkit/commands/setup_automatic.go":                        {},
	"cmd/stackkit/commands/use_cases_add.go":                          {},
	"internal/architecturev2/addons.go": {
		"TestListSupportedAddOnsUsesEmbeddedCatalogOutsideCheckout",
		"TestSupportedAddOnCatalogEntriesFiltersAndSortsDeterministically",
		"TestListSupportedAddOnsRejectsInvalidProfile",
		"TestListSupportedAddOnsRejectsNonEmbeddedAuthority",
		"TestSupportedAddOnCatalogEntriesRejectsMalformedAuthority",
	},
	"internal/backuplifecycle/snapshot_anchor_store.go": {},
	"internal/backupcustody/permissions_other.go": {
		"TestRequirePrivatePathAcceptsOnlyExpectedPrivateTypeAndMode",
	},
	"cmd/stackkit/commands/backup.go":                    {},
	"cmd/stackkit/commands/backup_emergency.go":          {},
	"cmd/stackkit/commands/architecture_v2_execution.go": {},
	"cmd/stackkit/commands/root.go":                      {},
	"cmd/stackkit/commands/banner.go":                    {},
	"cmd/stackkit/commands/apply.go":                     {},
	"cmd/stackkit/commands/generate.go":                  {},
	"internal/backuplifecycle/emergency_contract.go":     {},
	"internal/backupplan/plan.go":                        {},
	"internal/backupexec/hooks.go":                       {},
	"cmd/stackkit/commands/backup_native_v2.go":          {},
	"cmd/stackkit/commands/backup_selected_runtime.go":   {},
	"cmd/stackkit/commands/backup_native_v2_runtime.go":  {},
	"cmd/stackkit/commands/backup_activation.go":         {},
	"cmd/stackkit/commands/game_server_hold.go":          {},
	"cmd/stackkit/commands/init.go":                      {},
	"cmd/stackkit/commands/init_placement.go":            {},
	"cmd/stackkit/commands/init_architecture_v2.go":      {},
	"cmd/stackkit/commands/wizard.go":                    {},
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
	"internal/localevidence/executor_state_signature.go": {
		"TestOwnerExecutorStateSignatureIsDomainSeparatedAndOwnerBound",
	},
	"internal/localevidence/lifecycle_mutation_signature.go": {
		"TestOwnerLifecycleMutationSignatureRejectsTamper",
	},
	"internal/lifecyclemutation/journal.go": {},
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
		"TestExecutorStateOpenTofuCheckpointRestoresCapturedStateOnRecover",
	},
	"internal/upgradelifecycle/recovery.go": {
		"TestExecutorStateStoreRecoverRestoresAuthorityAndInvokesExactExecutable",
		"TestExecutorStateStoreRecoverRejectsTamperBeforeWritesOrCallback",
		"TestExecutorStateOpenTofuCheckpointRestoresCapturedStateOnRecover",
	},
	"internal/architecturev2/product_opentofu_factory.go": {
		"TestProductRuntimeOwnerFactoriesConstructWithNonEmptySelectors",
	},
	"internal/architecturev2renderer/compose_payload_opentofu.go": {
		"TestComposePayloadOpenTofuEmbedsTheByteIdenticalComposeArtifact",
	},
	"internal/runtimeexecutor/nativehost/selected_paas_workload.go":      {},
	"internal/runtimeexecutor/nativehost/standalone_compose_workload.go": {},
	"internal/runtimeexecutor/nativehost/standalone_compose_halves.go":   {},
	"internal/runtimeexecutor/nativehost/generation_target.go":           {},
	"internal/upgradelifecycle/executor_state_opentofu.go": {
		"TestExecutorStateOpenTofuCheckpointRestoresCapturedStateOnRecover",
		"TestExecutorStateCheckpointRestoresWorkloadAndContractRootStates",
	},
	"internal/runtimeexecutor/nativehost/basement_core_os.go":        {},
	"internal/runtimeexecutor/nativehost/cloud_core_os.go":           {},
	"internal/runtimeexecutor/nativehost/native_compose_observer.go": {},
	"internal/backupexec/docker.go":                                  {},
	"internal/releaseindex/installed.go": {
		"TestInspectInstalledReturnsReverifiedCallbackScopedProof",
	},
	// The historical public-upgrade tests were removed. Keep these boundaries
	// explicitly compile-only instead of selecting nonexistent tests or falling
	// back to the unrelated command suite. Shared recovery policy has its own
	// lifecyclemutation behavior tests; compilation is not runtime evidence.
	"cmd/stackkit/commands/upgrade_transaction.go": {},
	"cmd/stackkit/commands/upgrade_checkpoint.go":  {},
	"cmd/stackkit/commands/upgrade_recover.go":     {},
	"cmd/stackkit/commands/lifecycle_mutation.go":  {},
	// This composition wrapper delegates capsule reads to Architecture-v2,
	// whose changed public custody tests cover the behavior. Its command tests
	// were retired; retain an explicit compile boundary for the thin adapter.
	"cmd/stackkit/commands/architecture_v2_product_runtime.go": {},
	"cmd/stackkit/commands/drift.go":                           {},
}

type goPackage struct {
	ImportPath   string
	Dir          string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

type plannerInput struct {
	BaseRef           string
	MergeBase         string
	ChangedFiles      []string
	CoreCUERoots      []string
	StatusSurfaceGate bool
	// WebsiteSource is false in the curated public export, which ships the
	// installers but not website/ or its dependency scripts.
	WebsiteSource   bool
	GoPackages      []goPackage
	MaxReverse      int
	GoListWarning   string
	ChangedTests    map[string][]string
	ChangedTestTags map[string][]string
	// PublisherOnly records packages whose changed production files are excluded
	// by the ordinary Go build and owned exclusively by the publisher build.
	// Values are the actual publisher-only tests discovered from Go metadata.
	PublisherOnly map[string][]string
	// PublisherPresent records that the tag-gated publisher entry point
	// exists. PublisherClosure holds the repository package directories its
	// publisher build compiles; nil means the graph is unknown.
	PublisherPresent bool
	PublisherClosure map[string]struct{}
	// ContractProofPresent records that the packaged Architecture v2 contract
	// proof exists. ContractProofClosure holds the repository package
	// directories its verifier builds; nil means the graph is unknown.
	ContractProofPresent bool
	ContractProofClosure map[string]struct{}
	TestDiscoveryWarning string
	// InertGoFiles are changed Go files that both the selected base and the
	// current revision parse as declaration-free metadata (for example a
	// go:generate comment carrier). They cannot change package behavior, so
	// their package is compile-checked instead of running the unrelated suite.
	InertGoFiles []string
}

type classification struct {
	GoPackages        []string `json:"goPackages,omitempty"`
	GoShared          bool     `json:"goShared,omitempty"`
	CUEModules        []string `json:"cueModules,omitempty"`
	CUEKits           []string `json:"cueKits,omitempty"`
	CUEShared         bool     `json:"cueShared,omitempty"`
	Website           bool     `json:"website,omitempty"`
	WebMCP            bool     `json:"webMcp,omitempty"`
	OpenAPIProjection bool     `json:"openAPIProjection,omitempty"`
	AdvancedContract  bool     `json:"advancedContract,omitempty"`
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
	goSelection := affectedGoSelectionWithInert(files, input.GoPackages, maxReverse, input.InertGoFiles)
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
	publicBoundaryCommands := applyPublicTestBoundaries(files, input.InertGoFiles, &goSelection, focusedTests, input.PublisherOnly)
	// This executable is js/wasm-only. The WebMCP build compiles and executes
	// its real target; a native go test has no buildable source files.
	const capacityWasmPattern = "./cmd/stackkit-capacity-wasm"
	goSelection.Changed = withoutString(goSelection.Changed, capacityWasmPattern)
	goSelection.CompileOnly = withoutString(goSelection.CompileOnly, capacityWasmPattern)
	goSelection.Reverse = withoutString(goSelection.Reverse, capacityWasmPattern)
	testTags := make(map[string][]string, len(input.ChangedTestTags))
	for dir, tags := range input.ChangedTestTags {
		testTags[dir] = tags
	}
	for dir, tests := range input.PublisherOnly {
		delete(goSelection.Required, dir)
		focusedTests[dir] = sortedUnique(append(focusedTests[dir], tests...))
		testTags[dir] = []string{"publisher"}
	}
	// Architecture v2 CUE changes must keep the embedded authority and renderer
	// buildable. Bundle drift is `mise run generate:architecture-v2`. A CUE-only
	// slice therefore compile-checks those packages instead of executing
	// unrelated historical tests. Direct Go changes retain their normal
	// changed-package or focused-test selection.
	if anyPathUnder(files, architectureAuthoritySources...) {
		const authorityPackage = "internal/architecturev2"
		const authorityPattern = "./" + authorityPackage
		if !anyGoPathUnder(files, authorityPackage+"/") {
			// Bundle drift is `mise run generate:architecture-v2`. Compile the
			// embed so a missing generated file still fails closed. Do not
			// select a Go test that does not exist.
			goSelection.Changed = withoutString(goSelection.Changed, authorityPattern)
			goSelection.Reverse = withoutString(goSelection.Reverse, authorityPattern)
			goSelection.CompileOnly = sortedUnique(append(goSelection.CompileOnly, authorityPattern))
			delete(focusedTests, authorityPackage)
		}

		const rendererPackage = "internal/architecturev2renderer"
		const rendererPattern = "./" + rendererPackage
		if !anyGoPathUnder(files, rendererPackage+"/") {
			goSelection.Changed = withoutString(goSelection.Changed, rendererPattern)
			goSelection.Reverse = withoutString(goSelection.Reverse, rendererPattern)
			goSelection.CompileOnly = sortedUnique(append(goSelection.CompileOnly, rendererPattern))
		}
	}
	// A kit root holds the documents that define what a kit IS: its
	// KitDefinition (stackfile.cue), its exported metadata (stackkit.yaml) and
	// its reality grading (mode_matrix.cue). CUE vet validates them; a kit edit
	// additionally runs the Go product-kit inventory parity boundary. Bundle
	// drift stays on `mise run generate:architecture-v2`.
	if anyPathUnder(files, kitRoots...) {
		const authorityPattern = "./internal/architecturev2"
		goSelection.CompileOnly = withoutString(goSelection.CompileOnly, authorityPattern)
		goSelection.Reverse = withoutString(goSelection.Reverse, authorityPattern)
		if !slicesContain(goSelection.Changed, authorityPattern) {
			goSelection.Changed = sortedUnique(append(goSelection.Changed, authorityPattern))
		}
		focusedTests["internal/architecturev2"] = sortedUnique(append(
			focusedTests["internal/architecturev2"],
			kitInventoryParityTest,
		))
	}
	// A renderer contract hash and its catalog literal change on opposite sides
	// of the package boundary: a renderer schema edit or a catalog edit runs the
	// parity proof, unless the whole authority package already runs.
	if anyGoPathUnder(files, "internal/architecturev2renderer/") || anyPathUnder(files, "foundation/architecture_v2") {
		const authorityPattern = "./internal/architecturev2"
		// An explicit empty focus is the compile-only marker, not a full run.
		_, focused := focusedTests["internal/architecturev2"]
		fullyRun := slicesContain(goSelection.Changed, authorityPattern) && !focused
		if !fullyRun {
			goSelection.CompileOnly = withoutString(goSelection.CompileOnly, authorityPattern)
			goSelection.Reverse = withoutString(goSelection.Reverse, authorityPattern)
			goSelection.TestOnly = withoutString(goSelection.TestOnly, authorityPattern)
			if !slicesContain(goSelection.Changed, authorityPattern) {
				goSelection.Changed = sortedUnique(append(goSelection.Changed, authorityPattern))
			}
			focusedTests["internal/architecturev2"] = sortedUnique(append(
				focusedTests["internal/architecturev2"],
				rendererRegistryParityTest,
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
	}
	goPatterns := sortedUnique(append(append(append([]string(nil), goSelection.Changed...), goSelection.CompileOnly...), goSelection.Reverse...))
	classes.GoPackages = append([]string(nil), goPatterns...)
	commands = append(commands, affectedGoCommands(goSelection, focusedTests, testTags)...)
	commands = append(commands, publicBoundaryCommands...)
	if input.PublisherPresent && publisherBuildAffected(files, classes.GoShared, input.PublisherClosure) {
		commands = append(commands, testCommand{
			Kind:  "go",
			Scope: "publisher-compile",
			Argv:  []string{"go", "test", "-count=1", goTestTimeoutArg, "-tags", "publisher", "-run", "^$", packagePattern(publisherCommandDir)},
			Reason: "compile the tag-gated publisher (mise run build:publisher) when go.mod, go.sum or a package in its build closure changes; " +
				"the ordinary build never sees its publisher-only sources",
		})
	}
	if input.ContractProofPresent && contractProofAffected(files, classes.GoShared, input.ContractProofClosure) {
		commands = append(commands,
			testCommand{
				Kind:   "contract",
				Scope:  "contract-proof-cli",
				Argv:   []string{"node", cliBuildScript, "--target", "stackkit"},
				Reason: "build the development CLI (mise run build:cli) for the packaged contract proof",
			},
			testCommand{
				Kind:   "contract",
				Scope:  "contract-proof",
				Argv:   []string{"./build/stackkit", "contract-proof", "--repo-root", "."},
				Reason: "reproduce the packaged Architecture v2 contract fixture the public release candidate verifies, after a module, Foundation CUE, fixture, authority or verifier change",
			},
		)
	}

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

	if classes.Website && input.WebsiteSource {
		commands = append(commands,
			testCommand{
				Kind:   "website",
				Scope:  "dependencies",
				Argv:   []string{"node", "scripts/ensure-website-deps.mjs"},
				Reason: "prepare locked website and linked WebMCP dependencies when absent or changed",
			},
			testCommand{
				Kind:   "website",
				Scope:  "source",
				Argv:   []string{"pnpm", "--dir", "website", "run", "check"},
				Reason: "type-check and validate website source without building",
			},
			testCommand{
				Kind:   "website",
				Scope:  "public-boundary",
				Argv:   []string{"node", "scripts/release/check-website.mjs", "source"},
				Reason: "validate the private/public website source boundary",
			},
		)
		if websiteBuildAffected(files) {
			// CI-CD-PLATFORM-STANDARD §4.6: a bundler config or dependency
			// change can break only the production build, which typechecking
			// never runs. Same command Render's buildCommand runs in website/.
			commands = append(commands, testCommand{
				Kind:   "website",
				Scope:  "production-build",
				Argv:   []string{"env", "NODE_ENV=production", "pnpm", "--dir", "website", "run", "build"},
				Reason: "run the deploy's production build when bundler config or dependency manifests change",
			})
		}
	}
	if classes.WebMCP {
		commands = append(commands,
			testCommand{
				Kind:   "website",
				Scope:  "webmcp-dependencies",
				Argv:   []string{"pnpm", "--dir", "webmcp", "install", "--frozen-lockfile"},
				Reason: "install the locked public WebMCP contract toolchain on a clean runner",
			},
			testCommand{
				Kind:   "website",
				Scope:  "webmcp-contract",
				Argv:   []string{"pnpm", "--dir", "webmcp", "run", "check"},
				Reason: "build the generated schemas, authority projection and browser-tool contract",
			},
			testCommand{
				Kind:   "website",
				Scope:  "webmcp-reference-dependencies",
				Argv:   []string{"pnpm", "--dir", "webmcp/reference-host", "install", "--frozen-lockfile"},
				Reason: "install the locked public reference-host toolchain on a clean runner",
			},
			testCommand{
				Kind:   "website",
				Scope:  "webmcp-reference-check",
				Argv:   []string{"pnpm", "--dir", "webmcp/reference-host", "run", "check"},
				Reason: "type-check the public reference host against the shared planner component",
			},
			testCommand{
				Kind:   "website",
				Scope:  "webmcp-reference-build",
				Argv:   []string{"pnpm", "--dir", "webmcp/reference-host", "run", "build"},
				Reason: "build the independently reproducible public reference host",
			},
		)
	}
	if classes.OpenAPIProjection {
		commands = append(commands, testCommand{
			Kind:   "contract",
			Scope:  "stackaction-generated-openapi",
			Argv:   []string{"go", "run", "./internal/contractgen/stackactiongen/cmd", "-repo-root", ".", "-check"},
			Reason: "verify the CUE-owned StackAction regions of the canonical OpenAPI contract",
		})
	}
	statusSurfacesChanged := anyPathUnder(files,
		"scripts/derive-status-surfaces.mjs",
		"README.md",
		"STATUS.md",
		"docs/RELEASE.md",
		".goreleaser.yaml",
		"website/src/content/kit-maturity.generated.ts",
		"basement-kit/stackkit.yaml",
		"cloud-kit/stackkit.yaml",
		"modern-homelab/stackkit.yaml",
	)
	warnings := []string{}
	if classes.Website && !input.WebsiteSource {
		warnings = append(warnings, "website checks are not applicable to a repository surface without website/")
	}
	if statusSurfacesChanged && !input.StatusSurfaceGate {
		warnings = append(warnings, "private-only status-surface gate is not applicable to this curated repository surface")
	}
	if input.GoListWarning != "" {
		warnings = append(warnings, input.GoListWarning)
	}
	if input.TestDiscoveryWarning != "" {
		warnings = append(warnings, input.TestDiscoveryWarning)
	}
	if len(classes.Unknown) > 0 {
		warnings = append(warnings, "unknown paths receive hygiene checks only; the planner never falls back to go test ./...")
	}
	if input.StatusSurfaceGate && useCaseTruthTableAffected(files) {
		commands = append(commands, testCommand{
			Kind: "node", Scope: "use-case-table-parity",
			Argv:   []string{"node", "scripts/docs/use-case-overview.mjs", "--check", "--tables-only"},
			Reason: "execute the documentation CLI and check shared CUE-derived tables without runtime or test claims",
		})
	}

	for _, command := range commands {
		if command.Scope == budgetedPackageScope {
			warnings = append(warnings, command.Argv[len(command.Argv)-1]+" is over the affected slice budget: this slice skips its unselected registered slow tests, which run in mise run test:full")
		}
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
// websiteBuildAffected reports changes that only the production build can
// prove: framework/bundler config and dependency manifests. Component and
// library edits stay on the type-check.
func websiteBuildAffected(files []string) bool {
	for _, file := range files {
		switch file {
		case "website/package.json", "website/pnpm-lock.yaml", "website/pnpm-workspace.yaml":
			return true
		}
		if strings.HasPrefix(file, "website/svelte.config.") || strings.HasPrefix(file, "website/vite.config.") {
			return true
		}
	}
	return false
}

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

// Match the whole executable-product provenance scope; private facts and receipt
// changes also need projection. Public repositories have no private table contract.
func useCaseTruthTableAffected(files []string) bool {
	for _, file := range files {
		if file == "AGENTS.md" || file == "README.md" || file == "CHANGELOG.md" || strings.HasPrefix(file, ".beads/") {
			continue
		}
		if strings.HasPrefix(file, "docs/") && !anyPathUnder([]string{file}, "docs/USE_CASE_PACKAGES.md", "docs/use-case-expansion/portfolio.md", "docs/data/use-case-truth-table/", "docs/data/use-case-runtime-evidence/") {
			continue
		}
		return true
	}
	return false
}

func focusedGoTests(files []string, changedTests map[string][]string) map[string][]string {
	result := make(map[string][]string, len(changedTests))
	for dir, names := range changedTests {
		result[dir] = append([]string(nil), names...)
	}
	for _, file := range files {
		tests, focused := fileFocusedTests[file]
		if !focused {
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

func applyPublicTestBoundaries(files, inertFiles []string, selection *affectedGoSelection, focused map[string][]string, publisherOnly map[string][]string) []testCommand {
	// A registered boundary covers only its mapped production file. Unmapped
	// production must keep its independent package coverage, even when the same
	// diff also edits a test and has no registered public boundary.
	unmappedProduction := map[string]bool{}
	taggedTests := map[string][]string{}
	inertFiles = sortedUnique(inertFiles)
	for _, file := range files {
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") || isGeneratedGoProjection(file) || slicesContain(inertFiles, file) {
			continue
		}
		if _, registered := filePublicTestBoundaries[file]; registered {
			continue
		}
		if _, registered := fileFocusedTests[file]; !registered {
			unmappedProduction[path.Dir(file)] = true
		}
	}
	// The package slice replaces a dropped focused run. A package over the slice
	// budget skips slow tests in that slice, so it must still execute every test
	// the dropped focus or the boundary selected.
	fallBackToPackageSlice := func(dir string, tests []string) {
		if selection.Required == nil {
			selection.Required = map[string][]string{}
		}
		selection.Required[dir] = sortedUnique(append(append(selection.Required[dir], focused[dir]...), tests...))
		delete(focused, dir)
	}
	for _, file := range files {
		boundary, registered := filePublicTestBoundaries[file]
		if !registered {
			continue
		}
		source := path.Dir(file)
		if unmappedProduction[source] {
			fallBackToPackageSlice(source, nil)
		} else if _, alreadyFocused := focused[source]; !alreadyFocused {
			// Explicit empty selection compiles the source; the real behavior is
			// executed below through its public owner boundary.
			focused[source] = nil
		}
		if len(boundary.Tags) > 0 {
			// Mixed ordinary/publisher edits need separate real Go builds. The
			// metadata path already executes all tests for publisher-only edits.
			if _, covered := publisherOnly[boundary.Package]; !covered {
				key := boundary.Package + ":" + strings.Join(sortedUnique(boundary.Tags), ",")
				taggedTests[key] = append(taggedTests[key], boundary.Tests...)
			}
			continue
		}
		if unmappedProduction[boundary.Package] {
			fallBackToPackageSlice(boundary.Package, boundary.Tests)
		} else {
			focused[boundary.Package] = sortedUnique(append(focused[boundary.Package], boundary.Tests...))
		}
		target := packagePattern(boundary.Package)
		selection.Changed = sortedUnique(append(selection.Changed, target))
		selection.TestOnly = withoutString(selection.TestOnly, target)
		selection.CompileOnly = withoutString(selection.CompileOnly, target)
		selection.Reverse = withoutString(selection.Reverse, target)
	}
	for dir := range unmappedProduction {
		fallBackToPackageSlice(dir, nil)
	}
	commands := []testCommand{}
	keys := make([]string, 0, len(taggedTests))
	for key := range taggedTests {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts := strings.SplitN(key, ":", 2)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "tagged-public-owner-boundary",
			Argv:   []string{"go", "test", "-count=1", goTestTimeoutArg, "-tags", parts[1], "-run", exactTestRegex(sortedUnique(taggedTests[key])), packagePattern(parts[0])},
			Reason: "execute the mapped owner behavior in its actual tagged build without substituting the ordinary build",
		})
	}
	return commands
}

// publisherBuildAffected selects the publisher compile for module metadata
// changes and for production Go changes inside its build closure. An unknown
// closure selects it for every production Go change.
func publisherBuildAffected(files []string, moduleChanged bool, closure map[string]struct{}) bool {
	if moduleChanged {
		return true
	}
	for _, file := range files {
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			continue
		}
		if closure == nil {
			return true
		}
		if _, ok := closure[path.Dir(file)]; ok {
			return true
		}
	}
	return false
}

// contractProofAffected selects the packaged contract proof for module
// metadata, Foundation CUE, contract fixtures, the embedded Architecture v2
// authority and production Go changes inside the verifier's build closure. An
// unknown closure selects it for every production Go change.
func contractProofAffected(files []string, moduleChanged bool, closure map[string]struct{}) bool {
	if moduleChanged {
		return true
	}
	for _, file := range files {
		switch {
		case strings.HasPrefix(file, "foundation/") && strings.HasSuffix(file, ".cue"),
			strings.HasPrefix(file, "architecture/v2/fixtures/"),
			strings.HasPrefix(file, "internal/architecturev2/"),
			strings.HasPrefix(file, contractProofPackage+"/"),
			file == contractProofCommand:
			return true
		case !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go"):
			continue
		case closure == nil:
			return true
		}
		if _, ok := closure[path.Dir(file)]; ok {
			return true
		}
	}
	return false
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
		if strings.HasPrefix(file, "cmd/stackkit-capacity-wasm/") || strings.HasPrefix(file, "internal/capacityfit/") {
			result.WebMCP = true
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

		if isAdvancedContractPath(file) {
			result.AdvancedContract = true
			known = true
		}
		if file == "api/openapi/stackkits-v1.yaml" {
			result.OpenAPIProjection = true
			known = true
		} else if file == "install.sh" || file == "base-install.sh" || file == "cloud-install.sh" || file == "install-host-bootstrap.sh" {
			result.Website = true
			known = true
		} else if top == "website" {
			result.Website = true
			known = true
		} else if top == "webmcp" {
			result.WebMCP = true
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

// isAdvancedContractPath reports the machine contracts an orchestrator
// consumes: the stackkit.* JSON Schemas and the Advanced operations catalog.
func isAdvancedContractPath(file string) bool {
	return (strings.HasPrefix(file, "schemas/stackkit-") && strings.HasSuffix(file, ".json")) ||
		strings.HasPrefix(file, "docs/data/advanced-operations/")
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
	// Required keeps the tests a dropped focused selection or public boundary
	// selected in a package that fell back to the package slice.
	Required map[string][]string
}

func affectedGoSelectionFor(files []string, packages []goPackage, maxReverse int) affectedGoSelection {
	return affectedGoSelectionWithInert(files, packages, maxReverse, nil)
}

// affectedGoSelectionWithInert reuses the generated-projection compile-only
// path for declaration-free metadata files. A metadata file stays inert only
// when no real production file shares its package; any production change in the
// same package restores ordinary changed-package or changed-test selection.
func affectedGoSelectionWithInert(files []string, packages []goPackage, maxReverse int, inertFiles []string) affectedGoSelection {
	dirToPackage := map[string]goPackage{}
	changedImports := map[string]struct{}{}
	changedPatterns := map[string]struct{}{}
	generatedOnlyPatterns := map[string]bool{}
	testOnlyPatterns := map[string]bool{}
	reversePatterns := map[string]struct{}{}
	productionChange := map[string]struct{}{}
	inert := make(map[string]struct{}, len(inertFiles))
	for _, file := range inertFiles {
		inert[file] = struct{}{}
	}

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
		_, isInert := inert[file]
		dir := path.Dir(file)
		pattern := packagePattern(dir)
		changedPatterns[pattern] = struct{}{}
		if _, seen := generatedOnlyPatterns[pattern]; !seen {
			generatedOnlyPatterns[pattern] = true
			testOnlyPatterns[pattern] = true
		}
		if embeddedReleaseTrustPolicy || strings.HasSuffix(file, "_test.go") || (!isGeneratedGoProjection(file) && !isInert) {
			generatedOnlyPatterns[pattern] = false
		}
		if embeddedReleaseTrustPolicy || (!strings.HasSuffix(file, "_test.go") && !isInert) {
			testOnlyPatterns[pattern] = false
		}
		if pkg, ok := dirToPackage[dir]; ok {
			changedImports[pkg.ImportPath] = struct{}{}
			if embeddedReleaseTrustPolicy || (!strings.HasSuffix(file, "_test.go") && !isInert) {
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

func affectedGoCommands(selection affectedGoSelection, changedTests, changedTestTags map[string][]string) []testCommand {
	type focusedSelection struct {
		pattern string
		tests   []string
		tags    []string
	}
	focusedSelections := []focusedSelection{}
	fullPatterns := []string{}
	budgetedPatterns := []string{}
	compileOnlyPatterns := append([]string(nil), selection.CompileOnly...)
	taggedCompilePatterns := map[string][]string{}
	selectTests := func(pattern string, fallback *[]string) {
		dir := strings.TrimPrefix(pattern, "./")
		if dir == "." {
			dir = "."
		}
		selected, focused := changedTests[dir]
		tags := sortedUnique(changedTestTags[dir])
		if required, packageFallback := selection.Required[dir]; packageFallback {
			*fallback = append(*fallback, pattern)
			if len(tags) == 0 {
				return
			}
			// The default package slice cannot execute publisher-tagged tests.
			// Keep their dropped focus as well as the independent package slice.
			selected, focused = required, true
		}
		tests := sortedUnique(selected)
		if len(tests) == 0 {
			if len(tags) > 0 {
				key := strings.Join(tags, ",")
				taggedCompilePatterns[key] = append(taggedCompilePatterns[key], pattern)
				return
			}
			if focused {
				compileOnlyPatterns = append(compileOnlyPatterns, pattern)
				return
			}
			*fallback = append(*fallback, pattern)
			return
		}
		focusedSelections = append(focusedSelections, focusedSelection{pattern: pattern, tests: tests, tags: tags})
	}
	for _, pattern := range selection.Changed {
		if overSliceBudget(pattern) {
			selectTests(pattern, &budgetedPatterns)
		} else {
			selectTests(pattern, &fullPatterns)
		}
	}
	for _, pattern := range selection.TestOnly {
		selectTests(pattern, &compileOnlyPatterns)
	}

	commands := []testCommand{}
	for _, focused := range focusedSelections {
		for start := 0; start < len(focused.tests); start += focusedTestBatchSize {
			end := min(start+focusedTestBatchSize, len(focused.tests))
			batch := focused.tests[start:end]
			args := []string{"go", "test", "-count=1", goTestTimeoutArg}
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
	for _, pattern := range fullPatterns {
		// The executor's hang guard is per subprocess, while go test's timeout
		// is per package. Combining independently bounded packages incorrectly
		// charges all of them against one package's five-minute allowance.
		args := []string{"go", "test", "-count=1", goTestTimeoutArg}
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-packages", Argv: append(args, pattern),
			Reason: "run one changed package under its own unchanged timeout; production changes have no complete explicit test boundary",
		})
	}
	for _, pattern := range budgetedPatterns {
		commands = append(commands, budgetedPackageCommand(pattern, selection.Required[strings.TrimPrefix(pattern, "./")]))
	}
	if len(compileOnlyPatterns) > 0 {
		args := []string{"go", "test", "-count=1", goTestTimeoutArg, "-run", "^$"}
		args = append(args, sortedUnique(compileOnlyPatterns)...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-compile", Argv: args,
			Reason: "compile changed packages with an explicit compile-only boundary; this is not behavioral execution evidence",
		})
	}
	tagSets := make([]string, 0, len(taggedCompilePatterns))
	for tags := range taggedCompilePatterns {
		tagSets = append(tagSets, tags)
	}
	sort.Strings(tagSets)
	for _, tags := range tagSets {
		args := []string{"go", "test", "-count=1", goTestTimeoutArg, "-tags", tags, "-run", "^$"}
		args = append(args, sortedUnique(taggedCompilePatterns[tags])...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-compile", Argv: args,
			Reason: "compile changed build-tagged test packages without running unrelated tests",
		})
	}
	if len(selection.Reverse) > 0 {
		args := []string{"go", "test", "-count=1", goTestTimeoutArg, "-run", "^$"}
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
