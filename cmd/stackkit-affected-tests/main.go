package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
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
	commandTimeout        = 2 * time.Minute
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
	testDiscoveryWarning := ""
	if hasGoChanges(changed) {
		if opts.maxReverse > 0 {
			packages, err = loadGoPackages(repo)
			if err != nil {
				goListWarning = "Go package graph unavailable; testing changed package directories without reverse dependents: " + err.Error()
			}
		}
		changedTests, testDiscoveryWarning = loadChangedTestNames(repo, opts.mergeBase, changed)
	}

	plan := buildPlan(plannerInput{
		BaseRef:              opts.baseRef,
		MergeBase:            opts.mergeBase,
		ChangedFiles:         changed,
		GoPackages:           packages,
		MaxReverse:           opts.maxReverse,
		GoListWarning:        goListWarning,
		ChangedTests:         changedTests,
		TestDiscoveryWarning: testDiscoveryWarning,
	})

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

type lineRange struct {
	First int
	Last  int
}

var unifiedHunkHeader = regexp.MustCompile(`^@@ -[0-9]+(?:,[0-9]+)? \+([0-9]+)(?:,([0-9]+))? @@`)

func loadChangedTestNames(repo, mergeBase string, files []string) (map[string][]string, string) {
	result := map[string][]string{}
	warnings := []string{}
	for _, file := range files {
		if !strings.HasSuffix(file, "_test.go") {
			continue
		}
		fullPath := filepath.Join(repo, filepath.FromSlash(file))
		fileSet := token.NewFileSet()
		parsed, err := parser.ParseFile(fileSet, fullPath, nil, 0)
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
	return result, strings.Join(warnings, "; ")
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
			continue
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
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(output)))
	}
	return strings.TrimSpace(string(output)), nil
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
