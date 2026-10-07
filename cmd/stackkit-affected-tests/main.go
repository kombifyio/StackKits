package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	goPackageGraphTimeout = 30 * time.Second
	// commandTimeout is the hang guard around one planned subprocess.
	// Rule 8's 2-minute figure is the local-gate target, not this kill.
	commandTimeout = 5 * time.Minute
	// publisherCommandDir is the tag-gated publisher entry point that
	// `mise run build:publisher` compiles; the ordinary build excludes it.
	publisherCommandDir = "cmd/stackkit-publisher"
	// The packaged Architecture v2 contract proof that the public release
	// candidate runs (`stackkit contract-proof`, scripts/public/export-public.sh).
	contractProofPackage  = "internal/architecturecontractproof"
	contractProofCommand  = "cmd/stackkit/commands/contract_proof.go"
	contractProofManifest = "architecture/v2/fixtures/contract-fixtures.manifest.json"
	cliBuildScript        = "scripts/dev/build-go.mjs"
)

type options struct {
	repo       string
	baseRef    string
	mergeBase  string
	format     string
	maxReverse int
}

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "stackkit-affected-tests:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("stackkit-affected-tests", flag.ContinueOnError)
	flags.SetOutput(stderr)
	opts := options{}
	flags.StringVar(&opts.repo, "repo", ".", "repository root")
	flags.StringVar(&opts.baseRef, "base-ref", "origin/main", "ref used to calculate the merge base")
	flags.StringVar(&opts.mergeBase, "merge-base", "", "exact merge-base SHA (skips git merge-base)")
	flags.StringVar(&opts.format, "format", "json", "output format: json, shell, or execute")
	flags.IntVar(&opts.maxReverse, "max-reverse", 0, "maximum number of direct Go reverse dependents (0 keeps local planning graph-free)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	if opts.format != "json" && opts.format != "shell" && opts.format != "execute" {
		return fmt.Errorf("unsupported format %q (want json, shell, or execute)", opts.format)
	}
	if opts.maxReverse < 0 {
		return errors.New("--max-reverse must be zero or greater")
	}

	repo, err := filepath.Abs(opts.repo)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	if opts.mergeBase == "" {
		opts.mergeBase, err = gitOutput(repo, "merge-base", "HEAD", opts.baseRef)
		if err != nil {
			return fmt.Errorf("calculate merge base for %s: %w", opts.baseRef, err)
		}
	} else {
		opts.mergeBase, err = gitOutput(repo, "rev-parse", "--verify", opts.mergeBase+"^{commit}")
		if err != nil {
			return fmt.Errorf("verify merge base: %w", err)
		}
	}

	changed, err := changedFiles(repo, opts.mergeBase)
	if err != nil {
		return err
	}
	packages := []goPackage{}
	goListWarning := ""
	changedTests := map[string][]string{}
	changedTestTags := map[string][]string{}
	testDiscoveryWarning := ""
	if hasGoChanges(changed) {
		if opts.maxReverse > 0 {
			packages, err = loadGoPackages(repo)
			if err != nil {
				goListWarning = "Go package graph unavailable; testing changed package directories without reverse dependents: " + err.Error()
			}
		}
		changedTests, changedTestTags, testDiscoveryWarning = loadChangedTestNames(repo, opts.mergeBase, changed)
	}
	inertGoFiles := declarationFreeGoFiles(repo, opts.mergeBase, changed)
	publisherOnly, publisherWarning := publisherOnlyPackages(repo, opts.mergeBase, changed)
	if publisherWarning != "" {
		testDiscoveryWarning = strings.TrimSpace(testDiscoveryWarning + " " + publisherWarning)
	}
	publisherPresent, publisherClosure := false, map[string]struct{}(nil)
	if hasGoChanges(changed) {
		var closureErr error
		publisherPresent, publisherClosure, closureErr = publisherBuildClosure(repo)
		if closureErr != nil {
			testDiscoveryWarning = strings.TrimSpace(testDiscoveryWarning + " Publisher build closure unavailable; compiling the publisher for every Go change: " + closureErr.Error())
		}
	}
	contractProofPresent, contractProofDeps, closureErr := contractProofClosure(repo)
	if closureErr != nil {
		testDiscoveryWarning = strings.TrimSpace(testDiscoveryWarning + " Contract proof build closure unavailable; running the contract proof for every Go change: " + closureErr.Error())
	}
	statusSurfaceGate, err := statusSurfaceGateAvailability(repo)
	if err != nil {
		return err
	}

	plan := buildPlan(plannerInput{
		BaseRef:              opts.baseRef,
		MergeBase:            opts.mergeBase,
		ChangedFiles:         changed,
		CoreCUERoots:         existingCoreCUERoots(repo),
		StatusSurfaceGate:    statusSurfaceGate,
		WebsiteSource:        websiteSourcePresent(repo),
		GoPackages:           packages,
		MaxReverse:           opts.maxReverse,
		GoListWarning:        goListWarning,
		ChangedTests:         changedTests,
		ChangedTestTags:      changedTestTags,
		PublisherOnly:        publisherOnly,
		PublisherPresent:     publisherPresent,
		PublisherClosure:     publisherClosure,
		ContractProofPresent: contractProofPresent,
		ContractProofClosure: contractProofDeps,
		TestDiscoveryWarning: testDiscoveryWarning,
		InertGoFiles:         inertGoFiles,
	})
	plan.Commands, err = routeGoModuleCommands(repo, plan.Commands)
	if err != nil {
		return err
	}

	if opts.format != "json" {
		// A reduced or degraded slice must be visible where the plan runs, not
		// only in the JSON plan.
		for _, warning := range plan.Warnings {
			if _, err := fmt.Fprintln(stderr, "warning:", warning); err != nil {
				return err
			}
		}
	}
	if opts.format == "shell" {
		for _, command := range plan.Commands {
			if _, err := fmt.Fprintln(stdout, shellJoin(command.Argv)); err != nil {
				return err
			}
		}
		return nil
	}
	if opts.format == "execute" {
		return executePlan(repo, plan.Commands, stdout, stderr, executeCommand)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(plan)
}

var statusSurfaceGateFiles = []string{
	"scripts/derive-status-surfaces.mjs",
	"scripts/derive-status-surfaces.test.mjs",
	"STATUS.md",
	"basement-kit/stackkit.yaml",
	"cloud-kit/stackkit.yaml",
	"modern-homelab/stackkit.yaml",
	".goreleaser.yaml",
	"README.md",
	"docs/RELEASE.md",
	"website/src/content/kit-maturity.generated.ts",
}

func statusSurfaceGateAvailability(repo string) (bool, error) {
	markerPath := filepath.Join(repo, ".stackkits-public-export.json")
	privateMarker := filepath.Join(repo, "scripts", "public", "export-manifest.txt")
	markerInfo, markerErr := os.Lstat(markerPath)
	privateInfo, privateErr := os.Lstat(privateMarker)
	markerExists := markerErr == nil
	privateExists := privateErr == nil
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return false, fmt.Errorf("read curated public export marker: %w", markerErr)
	}
	if privateErr != nil && !errors.Is(privateErr, os.ErrNotExist) {
		return false, fmt.Errorf("read private export authority: %w", privateErr)
	}
	if markerExists == privateExists {
		return false, fmt.Errorf("repository surface identity must contain exactly one of the curated public marker or private export authority")
	}
	if markerExists {
		if !markerInfo.Mode().IsRegular() {
			return false, fmt.Errorf("curated public export marker is not a regular file")
		}
		markerBytes, err := os.ReadFile(markerPath)
		if err != nil {
			return false, fmt.Errorf("read curated public export marker: %w", err)
		}
		var marker struct {
			SchemaVersion string `json:"schemaVersion"`
			Owner         string `json:"owner"`
			State         string `json:"state"`
		}
		if err := json.Unmarshal(markerBytes, &marker); err != nil {
			return false, fmt.Errorf("parse curated public export marker: %w", err)
		}
		if marker.SchemaVersion != "stackkits.public-export-destination/v1" || marker.Owner != "kombify-stackkits-public-exporter" || marker.State != "complete" {
			return false, fmt.Errorf("invalid curated public export marker identity")
		}
		return false, nil
	}
	if !privateInfo.Mode().IsRegular() {
		return false, fmt.Errorf("private export authority is not a regular file")
	}

	missing := []string{}
	for _, relative := range statusSurfaceGateFiles {
		info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative)))
		if err != nil || info.IsDir() {
			missing = append(missing, relative)
		}
	}
	if len(missing) > 0 {
		return false, fmt.Errorf("private status-surface contract is incomplete: missing %s", strings.Join(missing, ", "))
	}
	return true, nil
}

func websiteSourcePresent(repo string) bool {
	_, err := os.Stat(filepath.Join(repo, "website", "package.json"))
	return err == nil
}

func existingCoreCUERoots(repo string) []string {
	roots := make([]string, 0, len(coreCUERoots))
	for _, pattern := range coreCUERoots {
		relative := strings.TrimSuffix(strings.TrimPrefix(pattern, "./"), "/...")
		info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative)))
		if err == nil && info.IsDir() {
			roots = append(roots, pattern)
		}
	}
	return roots
}

type commandExecutor func(repo string, argv []string, stdout, stderr io.Writer) error

func executePlan(repo string, commands []testCommand, stdout, stderr io.Writer, execute commandExecutor) error {
	for _, command := range commands {
		if len(command.Argv) == 0 {
			return fmt.Errorf("empty command for %s/%s", command.Kind, command.Scope)
		}
		if _, err := fmt.Fprintf(stdout, "==> [%s/%s] %s\n", command.Kind, command.Scope, shellJoin(command.Argv)); err != nil {
			return err
		}
		if err := execute(repo, command.Argv, stdout, stderr); err != nil {
			return fmt.Errorf("%s/%s failed: %w", command.Kind, command.Scope, err)
		}
	}
	return nil
}

func executeCommand(repo string, argv []string, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Dir = repo
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("%s exceeded %s", shellJoin(argv), commandTimeout)
		}
		return fmt.Errorf("%s: %w", shellJoin(argv), err)
	}
	return nil
}

func changedFiles(repo, mergeBase string) ([]string, error) {
	tracked, err := gitLines(repo, "diff", "--name-only", "--diff-filter=ACMR", mergeBase, "--")
	if err != nil {
		return nil, fmt.Errorf("list tracked changes: %w", err)
	}
	untracked, err := gitLines(repo, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("list untracked changes: %w", err)
	}
	return sortedUnique(append(tracked, untracked...)), nil
}

func hasGoChanges(files []string) bool {
	for _, file := range files {
		if strings.HasSuffix(file, ".go") || file == "go.mod" || file == "go.sum" {
			return true
		}
	}
	return false
}

// publisherBuildClosure reports whether the tag-gated publisher entry point
// exists and which repository package directories its publisher build
// compiles. A nil closure with present=true means the graph is unknown and the
// caller must compile the publisher conservatively.
func publisherBuildClosure(repo string) (bool, map[string]struct{}, error) {
	if !regularFileExists(repo, publisherCommandDir+"/main.go") {
		return false, nil, nil
	}
	closure, err := goBuildClosure(repo, packagePattern(publisherCommandDir), "-tags", "publisher")
	return true, closure, err
}

// contractProofClosure reports whether the packaged Architecture v2 contract
// proof exists and which repository package directories its verifier builds.
// A nil closure with present=true means the graph is unknown and the caller
// must run the proof conservatively.
func contractProofClosure(repo string) (bool, map[string]struct{}, error) {
	for _, required := range []string{contractProofManifest, contractProofCommand, cliBuildScript} {
		if !regularFileExists(repo, required) {
			return false, nil, nil
		}
	}
	closure, err := goBuildClosure(repo, packagePattern(contractProofPackage))
	return true, closure, err
}

func regularFileExists(repo, relative string) bool {
	info, err := os.Stat(filepath.Join(repo, filepath.FromSlash(relative)))
	return err == nil && info.Mode().IsRegular()
}

// goBuildClosure lists the repository package directories one package's build
// compiles, including itself.
func goBuildClosure(repo, pattern string, buildFlags ...string) (map[string]struct{}, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goPackageGraphTimeout)
	defer cancel()
	args := append([]string{"list", "-e", "-deps"}, buildFlags...)
	args = append(args, "-f", "{{if not .Standard}}{{.Dir}}{{end}}", pattern)
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = repo
	output, err := command.Output()
	if err != nil {
		return nil, err
	}
	// go list may report the symlink-resolved checkout path.
	roots := []string{repo}
	if resolved, err := filepath.EvalSymlinks(repo); err == nil && resolved != repo {
		roots = append(roots, resolved)
	}
	closure := map[string]struct{}{}
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		for _, root := range roots {
			if dir, err := filepath.Rel(root, line); line != "" && err == nil && !strings.HasPrefix(dir, "..") {
				closure[filepath.ToSlash(dir)] = struct{}{}
				break
			}
		}
	}
	return closure, nil
}

type lineRange struct {
	First int
	Last  int
}

var unifiedHunkHeader = regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,([0-9]+))? @@`)

func loadChangedTestNames(repo, mergeBase string, files []string) (map[string][]string, map[string][]string, string) {
	result := map[string][]string{}
	tags := map[string][]string{}
	warnings := []string{}
	for _, file := range files {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}
		fullPath := filepath.Join(repo, filepath.FromSlash(file))
		source, err := os.ReadFile(fullPath)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot read changed tests in %s: %v", file, err))
			continue
		}
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, fullPath, source, 0)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot discover changed tests in %s; its package will use the full package slice: %v", file, err))
			continue
		}
		changedLines, selectAll, err := changedLineRanges(repo, mergeBase, file)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("cannot map changed lines in %s; its package will use the full package slice: %v", file, err))
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(file))
		tags[dir] = append(tags[dir], publisherBuildTags(source)...)
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || !isGoTestName(function.Name.Name) {
				continue
			}
			if !selectAll {
				start := fileSet.Position(function.Pos()).Line
				end := fileSet.Position(function.End()).Line
				if !intersectsChangedLines(start, end, changedLines) {
					continue
				}
			}
			result[dir] = append(result[dir], function.Name.Name)
		}
	}
	for dir, names := range result {
		result[dir] = sortedUnique(names)
	}
	for dir, names := range tags {
		tags[dir] = sortedUnique(names)
	}
	return result, tags, strings.Join(warnings, "; ")
}

// publisherOnlyPackages uses Go's selected file sets, not directory ownership,
// to distinguish private publisher changes from ordinary production changes.
// Unknown or mixed constraints retain the existing conservative package slice.
func publisherOnlyPackages(repo, base string, changed []string) (map[string][]string, string) {
	production := map[string][]string{}
	for _, file := range changed {
		if strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") {
			dir := filepath.ToSlash(filepath.Dir(file))
			production[dir] = append(production[dir], file)
		}
	}
	if len(production) == 0 {
		return nil, ""
	}
	patterns := []string{}
	for dir := range production {
		patterns = append(patterns, packagePattern(dir))
	}
	patterns = sortedUnique(patterns)
	type buildPackage struct {
		Dir                                          string
		GoFiles, CgoFiles, TestGoFiles, XTestGoFiles []string
		Error                                        *struct{ Err string }
	}
	load := func(tags bool) (map[string]buildPackage, error) {
		ctx, cancel := context.WithTimeout(context.Background(), goPackageGraphTimeout)
		defer cancel()
		args := []string{"list", "-e", "-json=Dir,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles,Error"}
		if tags {
			args = append(args, "-tags", "publisher")
		}
		args = append(args, patterns...)
		command := exec.CommandContext(ctx, "go", args...)
		command.Dir = repo
		output, err := command.Output()
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		result := map[string]buildPackage{}
		for {
			var pkg buildPackage
			if err := decoder.Decode(&pkg); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return nil, err
			}
			dir, err := filepath.Rel(repo, pkg.Dir)
			if err != nil {
				return nil, err
			}
			result[filepath.ToSlash(dir)] = pkg
		}
		return result, nil
	}
	ordinary, err := load(false)
	if err != nil {
		return nil, "Go build topology unavailable; retaining ordinary package checks: " + err.Error()
	}
	publisher, err := load(true)
	if err != nil {
		return nil, "Publisher Go build topology unavailable; retaining ordinary package checks: " + err.Error()
	}
	result := map[string][]string{}
	for dir, files := range production {
		normal, normalOK := ordinary[dir]
		tagged, taggedOK := publisher[dir]
		if !normalOK || !taggedOK || tagged.Error != nil {
			continue
		}
		exclusive := true
		for _, file := range files {
			name := filepath.Base(file)
			if slicesContain(normal.GoFiles, name) || slicesContain(normal.CgoFiles, name) ||
				!(slicesContain(tagged.GoFiles, name) || slicesContain(tagged.CgoFiles, name)) {
				exclusive = false
				break
			}
			current, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
			if err != nil || !exclusivePublisherConstraint(current) {
				exclusive = false
				break
			}
			previous, err := gitOutput(repo, "show", base+":"+file)
			if err == nil && !exclusivePublisherConstraint([]byte(previous)) {
				exclusive = false
				break
			}
		}
		if !exclusive {
			continue
		}
		for _, file := range changed {
			if filepath.ToSlash(filepath.Dir(file)) == dir && strings.HasSuffix(file, "_test.go") &&
				(slicesContain(normal.TestGoFiles, filepath.Base(file)) || slicesContain(normal.XTestGoFiles, filepath.Base(file))) {
				exclusive = false
				break
			}
		}
		if !exclusive {
			continue
		}
		tests := []string{}
		for _, name := range append(append([]string(nil), tagged.TestGoFiles...), tagged.XTestGoFiles...) {
			if slicesContain(normal.TestGoFiles, name) || slicesContain(normal.XTestGoFiles, name) {
				continue
			}
			fullPath := filepath.Join(repo, filepath.FromSlash(dir), name)
			parsed, err := parser.ParseFile(token.NewFileSet(), fullPath, nil, 0)
			if err != nil {
				exclusive = false
				break
			}
			for _, declaration := range parsed.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if ok && function.Recv == nil && isGoTestName(function.Name.Name) {
					tests = append(tests, function.Name.Name)
				}
			}
		}
		if exclusive {
			result[dir] = sortedUnique(tests)
		}
	}
	return result, ""
}

func exclusivePublisherConstraint(source []byte) bool {
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		if strings.HasPrefix(line, "//go:build ") {
			expression, err := constraint.Parse(line)
			tag, ok := expression.(*constraint.TagExpr)
			return err == nil && ok && tag.Tag == "publisher"
		}
	}
	return false
}

func publisherBuildTags(source []byte) []string {
	for _, line := range strings.Split(string(source), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			break
		}
		if !strings.HasPrefix(line, "//go:build ") {
			continue
		}
		expression, err := constraint.Parse(line)
		if err != nil {
			return nil
		}
		for _, tag := range positiveTags(expression, false) {
			if tag == "publisher" {
				return []string{tag}
			}
		}
		return nil
	}
	return nil
}

func positiveTags(expression constraint.Expr, negated bool) []string {
	switch value := expression.(type) {
	case *constraint.TagExpr:
		if !negated {
			return []string{value.Tag}
		}
	case *constraint.NotExpr:
		return positiveTags(value.X, !negated)
	case *constraint.AndExpr:
		return append(positiveTags(value.X, negated), positiveTags(value.Y, negated)...)
	case *constraint.OrExpr:
		return append(positiveTags(value.X, negated), positiveTags(value.Y, negated)...)
	}
	return nil
}

func changedLineRanges(repo, mergeBase, file string) ([]lineRange, bool, error) {
	if strings.TrimSpace(mergeBase) == "" {
		return nil, true, nil
	}
	if _, err := gitOutput(repo, "cat-file", "-e", mergeBase+":"+file); err != nil {
		// New and renamed test files have no base-side path. Every test in the
		// current file is part of the changed slice.
		return nil, true, nil
	}
	diff, err := gitOutput(repo, "diff", "--unified=0", "--no-color", mergeBase, "--", file)
	if err != nil {
		return nil, false, err
	}
	return parseUnifiedChangedLines(diff), false, nil
}

func parseUnifiedChangedLines(diff string) []lineRange {
	ranges := []lineRange{}
	for _, line := range strings.Split(diff, "\n") {
		match := unifiedHunkHeader.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		first, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		count := 1
		if match[2] != "" {
			count, err = strconv.Atoi(match[2])
			if err != nil {
				continue
			}
		}
		if count <= 0 {
			count = 1
			first = max(1, first)
		}
		ranges = append(ranges, lineRange{First: first, Last: first + count - 1})
	}
	return ranges
}

func intersectsChangedLines(start, end int, ranges []lineRange) bool {
	for _, changed := range ranges {
		if start <= changed.Last && end >= changed.First {
			return true
		}
	}
	return false
}

func isGoTestName(name string) bool {
	if !strings.HasPrefix(name, "Test") || len(name) == len("Test") {
		return false
	}
	next := rune(name[len("Test")])
	return next < 'a' || next > 'z'
}

// declarationFreeGoFiles returns changed Go files whose selected base revision
// and current revision contain only a package clause, ordinary comments and
// generate directives (go:generate). Other compiler directives retain normal
// tests.
// Read/parse failures or declarations on either side also retain normal tests.
func declarationFreeGoFiles(repo, mergeBase string, files []string) []string {
	result := []string{}
	for _, file := range files {
		if !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			continue
		}
		current, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(file)))
		if err != nil || !isDeclarationFreeGoSource(current, file) {
			continue
		}
		basePath, err := gitOutput(repo, "ls-tree", "-r", "--name-only", mergeBase, "--", ":(literal)"+file)
		if err != nil {
			continue
		}
		if basePath != "" {
			base, err := gitOutput(repo, "cat-file", "-p", mergeBase+":"+file)
			if err != nil || !isDeclarationFreeGoSource([]byte(base), file) {
				continue
			}
		}
		result = append(result, file)
	}
	return result
}

// isDeclarationFreeGoSource excludes compiler directives such as go:debug,
// which can change runtime behavior even without a Go declaration.
func isDeclarationFreeGoSource(source []byte, filename string) bool {
	parsed, err := parser.ParseFile(token.NewFileSet(), filename, source, parser.SkipObjectResolution|parser.ParseComments)
	if err != nil {
		return false
	}
	for _, group := range parsed.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, "//go:") && !strings.HasPrefix(comment.Text, "//go:generate ") {
				return false
			}
		}
	}
	return len(parsed.Decls) == 0
}

func loadGoPackages(repo string) ([]goPackage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), goPackageGraphTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "list", "-json", "./...")
	command.Dir = repo
	output, err := command.Output()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("go list exceeded %s", goPackageGraphTimeout)
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("go list: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("go list: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	packages := []goPackage{}
	for {
		var raw struct {
			ImportPath   string
			Dir          string
			Imports      []string
			TestImports  []string
			XTestImports []string
		}
		if err := decoder.Decode(&raw); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode go list: %w", err)
		}
		dir, err := filepath.Rel(repo, raw.Dir)
		if err != nil || strings.HasPrefix(dir, "..") {
			continue
		}
		packages = append(packages, goPackage{
			ImportPath:   raw.ImportPath,
			Dir:          filepath.ToSlash(dir),
			Imports:      raw.Imports,
			TestImports:  raw.TestImports,
			XTestImports: raw.XTestImports,
		})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ImportPath < packages[j].ImportPath })
	return packages, nil
}

func gitOutput(repo string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = repo
	output, err := commandOutput(command)
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

// commandOutput keeps successful stderr diagnostics out of machine-readable
// stdout. Git for Windows can emit CRLF advice while returning success; using
// CombinedOutput would make that advice look like changed filenames.
func commandOutput(command *exec.Cmd) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return "", errors.New(detail)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func gitLines(repo string, args ...string) ([]string, error) {
	output, err := gitOutput(repo, args...)
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, nil
	}
	return strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n"), nil
}

func shellJoin(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuote(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune("_./:@%+=,-", r)
	}) == -1 {
		return value
	}
	if runtime.GOOS == "windows" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
