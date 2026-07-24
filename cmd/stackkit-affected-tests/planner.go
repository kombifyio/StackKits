package main

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

const planSchema = "kombify.stackkits/affected-test-plan/v1"

var coreCUERoots = []string{
	"./base/...",
	"./basement-kit/...",
	"./cloud-kit/...",
	"./modern-homelab/...",
	"./ha-kit/...",
	"./addons/...",
}

// fileFocusedTests keeps focused production and shared-fixture slices explicit
// for packages whose historical full test suite is not a useful beta feedback
// gate. Adding or renaming a test in one of these slices must update this
// reviewable binding.
var fileFocusedTests = map[string][]string{
	"cmd/stackkit/commands/init_architecture_v2.go": {
		"TestRunArchitectureV2InitMaterializesCanonicalProductSpecs",
		"TestRunArchitectureV2InitNormalizesWorkspaceNameAndHonorsExplicitName",
		"TestRunInitRoutesDevToEmbeddedV2BeforeLegacyDiscovery",
		"TestRunArchitectureV2InitFailsBeforeWriteForMissingRequiredDomain",
		"TestRunArchitectureV2InitRejectsLegacySemanticsAndLocalPathsBeforeWrite",
		"TestRunArchitectureV2InitUsesExpectedHashCASAndRejectsForce",
		"TestRunArchitectureV2InitUsesExistingSpecAliasWithoutCreatingSecondAuthority",
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
	"internal/runtimeexecutorlocal/modern_identity_site_policy_test.go": {
		"TestModernIdentitySiteExecutorsKeepHomeAndCloudAuthoritySeparate",
		"TestModernIdentitySiteExecutorsRejectCrossSiteAndChannelSubstitution",
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
	GoPackages []string `json:"goPackages,omitempty"`
	GoShared   bool     `json:"goShared,omitempty"`
	CUEModules []string `json:"cueModules,omitempty"`
	CUEKits    []string `json:"cueKits,omitempty"`
	CUEShared  bool     `json:"cueShared,omitempty"`
	Website    bool     `json:"website,omitempty"`
	Release    bool     `json:"release,omitempty"`
	Docs       bool     `json:"docs,omitempty"`
	Unknown    []string `json:"unknown,omitempty"`
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
	goPatterns := sortedUnique(append(append(append([]string(nil), goSelection.Changed...), goSelection.CompileOnly...), goSelection.Reverse...))
	classes.GoPackages = append([]string(nil), goPatterns...)
	commands = append(commands, affectedGoCommands(goSelection, focusedGoTests(files, input.ChangedTests))...)

	if classes.CUEShared {
		commands = append(commands, testCommand{
			Kind:   "cue",
			Scope:  "shared-contract-and-core-consumers",
			Argv:   append([]string{"cue", "vet"}, coreRoots...),
			Reason: "shared CUE changes can affect each core kit but do not require every catalog module",
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
	if classes.Release {
		commands = append(commands, testCommand{
			Kind:  "release",
			Scope: "release-contract-smoke",
			Argv: []string{
				"node", "--test",
				"scripts/release/release-evidence.test.mjs",
				"scripts/release/check-fast-feedback-budget.test.mjs",
				"scripts/public/export-public-verification.test.mjs",
			},
			Reason: "run the bounded release identity and evidence contract smoke",
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
			case "base", "cue.mod", "schemas", "architecture", "addons", "platforms":
				result.CUEShared = true
				known = true
			case "basement-kit", "cloud-kit", "modern-homelab", "ha-kit":
				kits[top] = struct{}{}
				known = true
			case "modules":
				if len(parts) > 1 {
					modules[parts[1]] = struct{}{}
					known = true
				}
			}
		}

		if top == "website" {
			result.Website = true
			known = true
		}
		if top == "docs" || file == "README.md" || file == "CONTRIBUTING.md" || file == "CHANGELOG.md" || file == "ROADMAP.md" || file == "STATUS.md" {
			result.Docs = true
			known = true
		}
		if isReleasePath(file) {
			result.Release = true
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

func isReleasePath(file string) bool {
	if file == ".goreleaser.yaml" || file == "install.sh" || file == "Dockerfile" || file == "mise.toml" || file == "scripts/sync-public.sh" {
		return true
	}
	return strings.HasPrefix(file, "scripts/release/") ||
		strings.HasPrefix(file, "scripts/public/") ||
		strings.HasPrefix(file, ".github/workflows/") ||
		strings.HasPrefix(file, ".depot/workflows/")
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
		if !strings.HasSuffix(file, ".go") {
			continue
		}
		dir := path.Dir(file)
		pattern := packagePattern(dir)
		changedPatterns[pattern] = struct{}{}
		if _, seen := generatedOnlyPatterns[pattern]; !seen {
			generatedOnlyPatterns[pattern] = true
		}
		if strings.HasSuffix(file, "_test.go") || !strings.HasSuffix(file, "_generated.go") {
			generatedOnlyPatterns[pattern] = false
		}
		if pkg, ok := dirToPackage[dir]; ok {
			changedImports[pkg.ImportPath] = struct{}{}
			if !strings.HasSuffix(file, "_test.go") {
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
	focusedPatterns := []string{}
	focusedTests := []string{}
	fullPatterns := []string{}
	for _, pattern := range selection.Changed {
		dir := strings.TrimPrefix(pattern, "./")
		if dir == "." {
			dir = "."
		}
		tests := sortedUnique(changedTests[dir])
		if len(tests) == 0 {
			fullPatterns = append(fullPatterns, pattern)
			continue
		}
		focusedPatterns = append(focusedPatterns, pattern)
		focusedTests = append(focusedTests, tests...)
	}

	commands := []testCommand{}
	if len(focusedPatterns) > 0 {
		args := []string{"go", "test", "-count=1", "-timeout=90s", "-run", exactTestRegex(focusedTests)}
		args = appendRequiredBuildTags(args, focusedPatterns)
		args = append(args, focusedPatterns...)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-test-functions", Argv: args,
			Reason: "compile changed packages and run only test functions changed in this slice",
		})
	}
	if len(fullPatterns) > 0 {
		args := appendRequiredBuildTags([]string{"go", "test", "-count=1", "-timeout=90s"}, fullPatterns)
		commands = append(commands, testCommand{
			Kind: "go", Scope: "changed-packages", Argv: append(args, fullPatterns...),
			Reason: "run changed packages that have no changed test-function boundary",
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
