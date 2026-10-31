package main

import (
	"errors"
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// contractPackageRoots are the directories whose packages form the published
// contract surface. Every other top-level directory with Go source must be a
// non-contract root, so a new package cannot bypass the import and fixture
// rules by being left unclassified.
var contractPackageRoots = []string{
	"advancedcapability",
	"applyevidence",
	"datasubject",
	"internal/referenceid",
	"providerexecutor",
	"runtimeapply",
	"runtimeexecutor",
	"runtimeinventory",
	"runtimelease",
	"stackaction",
}

// nonContractRoots hold contract tooling that never ships as contract API.
var nonContractRoots = map[string]bool{"internal": true}

// forbiddenContractImports keep transport, persistence, process execution and
// memory-unsafe code out of non-test contract sources. Contract values are
// validated in memory; the consuming runtime owns every side effect.
// Each entry also forbids its subpackages; "net" itself is matched exactly so
// that value parsing through net/url and net/netip stays available.
var forbiddenContractImports = []string{
	"database/sql", "net/http", "net/rpc", "net/smtp", "os/exec", "plugin", "syscall", "unsafe",
}

// URL literals in contract sources, docs and fixtures may only name reserved
// example hosts (RFC 2606 and RFC 6761), never a real service endpoint. The
// JSON Schema meta-schema identifier is a name, not an endpoint.
var urlLiteral = regexp.MustCompile(`(?i)\bhttps?://([a-z0-9.-]+)`)

var reservedHosts = []string{"example", "test", "invalid", "localhost", "example.com", "example.net", "example.org"}

// contractModule is the import prefix of the contract packages inside the
// StackKits module. Contract sources may import only the standard library,
// github.com/google/uuid and contract packages below this prefix, which keeps
// the contracts dependency-light although the surrounding module is not.
const contractModule = "github.com/kombifyio/stackkits/contracts"

// checkContractBoundary rejects retired public namespaces and applies the
// contract source rules below root. Package metadata recognizes retired
// sources even behind build tags.
func checkContractBoundary(root string) error {
	for _, retired := range []string{"vmlease", "runtimeaction", "serverruntime"} {
		directory := filepath.Join(root, retired)
		if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				if path == directory && errors.Is(walkErr, os.ErrNotExist) {
					return nil
				}
				return walkErr
			}
			if !entry.IsDir() {
				return nil
			}
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			pkg, err := build.Default.ImportDir(path, 0)
			var noGo *build.NoGoError
			if err != nil && !errors.As(err, &noGo) {
				return fmt.Errorf("inspect contract package %s: %w", path, err)
			}
			if pkg != nil && len(pkg.GoFiles)+len(pkg.CgoFiles)+len(pkg.IgnoredGoFiles) != 0 {
				return fmt.Errorf("retired contract package %s belongs in a consuming runtime", filepath.ToSlash(path))
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return checkContractSources(root, contractModule)
}

// checkContractSources applies the public-module rules to every contract
// package root below root: imports stay within the standard library, uuid and
// this module's contract packages; non-test sources carry no transport,
// persistence or process execution; URL literals name only reserved hosts.
// Every Go file is parsed regardless of build tags.
func checkContractSources(root, mainModule string) error {
	if err := checkRootsClassified(root); err != nil {
		return err
	}
	for _, contractRoot := range contractPackageRoots {
		directory := filepath.Join(root, filepath.FromSlash(contractRoot))
		if _, err := os.Stat(directory); err != nil {
			return fmt.Errorf("contract package root %s: %w", contractRoot, err)
		}
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if entry.IsDir() {
				return nil
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := checkURLLiterals(relative, content); err != nil {
				return err
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			return checkContractImports(relative, content, mainModule)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func checkRootsClassified(root string) error {
	contract := map[string]bool{}
	for _, contractRoot := range contractPackageRoots {
		contract[strings.SplitN(contractRoot, "/", 2)[0]] = true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var unclassified []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || contract[name] || nonContractRoots[name] {
			continue
		}
		hasGo := false
		_ = filepath.WalkDir(filepath.Join(root, name), func(path string, child os.DirEntry, walkErr error) error {
			if walkErr == nil && !child.IsDir() && filepath.Ext(path) == ".go" {
				hasGo = true
				return filepath.SkipAll
			}
			return walkErr
		})
		if hasGo {
			unclassified = append(unclassified, name)
		}
	}
	if len(unclassified) != 0 {
		sort.Strings(unclassified)
		return fmt.Errorf("unclassified Go package roots %s: add them to the contract package roots or remove them", strings.Join(unclassified, ", "))
	}
	return nil
}

func checkContractImports(file string, content []byte, mainModule string) error {
	parsed, err := parser.ParseFile(token.NewFileSet(), file, content, parser.ImportsOnly)
	if err != nil {
		return fmt.Errorf("parse contract source %s: %w", file, err)
	}
	isTest := strings.HasSuffix(file, "_test.go")
	for _, spec := range parsed.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return fmt.Errorf("contract source %s: %w", file, err)
		}
		if err := checkContractImport(importPath, mainModule, isTest); err != nil {
			return fmt.Errorf("contract source %s: %w", file, err)
		}
	}
	return nil
}

func checkContractImport(importPath, mainModule string, isTest bool) error {
	first := strings.SplitN(importPath, "/", 2)[0]
	switch {
	case !strings.Contains(first, "."):
		if isTest {
			return nil
		}
		if importPath == "net" {
			return fmt.Errorf("import %q belongs in a consuming runtime, not in a contract package", importPath)
		}
		for _, forbidden := range forbiddenContractImports {
			if importPath == forbidden || strings.HasPrefix(importPath, forbidden+"/") {
				return fmt.Errorf("import %q belongs in a consuming runtime, not in a contract package", importPath)
			}
		}
		return nil
	case importPath == "github.com/google/uuid":
		return nil
	case strings.HasPrefix(importPath, mainModule+"/"):
		local := strings.TrimPrefix(importPath, mainModule+"/")
		for _, contractRoot := range contractPackageRoots {
			if local == contractRoot || strings.HasPrefix(local, contractRoot+"/") {
				return nil
			}
		}
		return fmt.Errorf("import %q is repository tooling, not a contract package", importPath)
	default:
		return fmt.Errorf("import %q is outside the standard library, github.com/google/uuid and the contract packages", importPath)
	}
}

func checkURLLiterals(file string, content []byte) error {
	for _, match := range urlLiteral.FindAllSubmatch(content, -1) {
		host := strings.ToLower(strings.TrimRight(string(match[1]), "."))
		reserved := host == "json-schema.org"
		for _, base := range reservedHosts {
			if host == base || strings.HasSuffix(host, "."+base) {
				reserved = true
				break
			}
		}
		if !reserved {
			return fmt.Errorf("contract file %s names URL host %q; contract sources and fixtures may only use reserved example hosts", file, host)
		}
	}
	return nil
}
