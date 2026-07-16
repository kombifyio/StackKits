package commands

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	skcue "github.com/kombifyio/stackkits/internal/cue"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/spf13/cobra"
)

type moduleVersionGuardOptions struct {
	modulesDir   string
	baselineRef  string
	baselineTree string
}

type moduleContractIdentity struct {
	Slug    string
	Version string
	Hash    string
}

type moduleVersionGuardStatus string

const (
	moduleVersionUnchanged moduleVersionGuardStatus = "unchanged"
	moduleVersionAdvanced  moduleVersionGuardStatus = "advanced"
	moduleVersionNew       moduleVersionGuardStatus = "new"
	maxBaselineEntryBytes                           = int64(4 << 20)
	maxBaselineTreeBytes                            = int64(64 << 20)
)

type moduleVersionGuardResult struct {
	Slug            string
	Status          moduleVersionGuardStatus
	BaselineVersion string
	CurrentVersion  string
}

type moduleVersionGuardError struct {
	violations []string
}

func (e *moduleVersionGuardError) Error() string {
	return "module version guard failed:\n  - " + strings.Join(e.violations, "\n  - ")
}

func newModuleVerifyVersionBumpsCmd() *cobra.Command {
	opts := moduleVersionGuardOptions{}
	cmd := &cobra.Command{
		Use:   "verify-version-bumps",
		Short: "Require a higher module SemVer whenever its canonical contract changes",
		Long: `Compare the current CUE module contracts with a baseline Git ref or
baseline tree. New modules are allowed. Existing modules whose canonical
contract hash changed must declare a semantic version strictly greater than
the baseline version. The Git-ref mode materializes the committed baseline
without executing code from it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runModuleVersionGuard(cmd.Context(), opts)
		},
	}

	cmd.Flags().StringVar(&opts.modulesDir, "modules-dir", "modules", "Current modules directory")
	cmd.Flags().StringVar(&opts.baselineRef, "baseline-ref", "", "Git commit/ref containing the baseline module tree")
	cmd.Flags().StringVar(&opts.baselineTree, "baseline-tree", "", "Baseline repository tree or modules directory")
	return cmd
}

func init() {
	moduleCmd.AddCommand(newModuleVerifyVersionBumpsCmd())
}

func runModuleVersionGuard(ctx context.Context, opts moduleVersionGuardOptions) error {
	if (strings.TrimSpace(opts.baselineRef) == "") == (strings.TrimSpace(opts.baselineTree) == "") {
		return fmt.Errorf("exactly one of --baseline-ref or --baseline-tree is required")
	}

	currentModulesDir, err := filepath.Abs(opts.modulesDir)
	if err != nil {
		return fmt.Errorf("resolve current modules directory: %w", err)
	}
	if !looksLikeModulesDir(currentModulesDir) {
		return fmt.Errorf("current modules directory %s contains no module.cue contracts", currentModulesDir)
	}

	baselineModulesDir := ""
	cleanup := func() {}
	if opts.baselineRef != "" {
		baselineModulesDir, cleanup, err = materializeModuleBaselineRef(ctx, currentModulesDir, opts.baselineRef)
	} else {
		baselineModulesDir, err = resolveBaselineModulesTree(currentModulesDir, opts.baselineTree)
	}
	if err != nil {
		return err
	}
	defer cleanup()

	current, err := loadModuleContractIdentities(currentModulesDir)
	if err != nil {
		return fmt.Errorf("load current module contracts: %w", err)
	}
	baseline, err := loadModuleContractIdentities(baselineModulesDir)
	if err != nil {
		return fmt.Errorf("load baseline module contracts: %w", err)
	}

	results, verifyErr := verifyModuleVersionBumps(current, baseline)
	counts := map[moduleVersionGuardStatus]int{}
	for _, result := range results {
		counts[result.Status]++
		switch result.Status {
		case moduleVersionAdvanced:
			printSuccess("%s contract changed with version %s -> %s", result.Slug, result.BaselineVersion, result.CurrentVersion)
		case moduleVersionNew:
			printInfo("%s@%s is new relative to the baseline", result.Slug, result.CurrentVersion)
		}
	}
	printInfo(
		"module version guard checked=%d unchanged=%d advanced=%d new=%d",
		len(results),
		counts[moduleVersionUnchanged],
		counts[moduleVersionAdvanced],
		counts[moduleVersionNew],
	)
	return verifyErr
}

func loadModuleContractIdentities(modulesDir string) (map[string]moduleContractIdentity, error) {
	contracts, err := skcue.NewModuleReader().ReadAllModules(modulesDir)
	if err != nil {
		return nil, err
	}

	identities := make(map[string]moduleContractIdentity, len(contracts))
	for _, contract := range contracts {
		slug := strings.TrimSpace(contract.Metadata.Name)
		if slug == "" {
			return nil, fmt.Errorf("module in %s has an empty metadata.name", modulesDir)
		}
		if _, exists := identities[slug]; exists {
			return nil, fmt.Errorf("duplicate module metadata.name %q in %s", slug, modulesDir)
		}
		hash, err := skcue.ContractHash(moduleContractToCanonicalMap(contract))
		if err != nil {
			return nil, fmt.Errorf("compute canonical contract hash for %s: %w", slug, err)
		}
		identities[slug] = moduleContractIdentity{
			Slug:    slug,
			Version: strings.TrimSpace(contract.Metadata.Version),
			Hash:    hash,
		}
	}
	return identities, nil
}

func verifyModuleVersionBumps(current, baseline map[string]moduleContractIdentity) ([]moduleVersionGuardResult, error) {
	slugs := make([]string, 0, len(current))
	for slug := range current {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)

	results := make([]moduleVersionGuardResult, 0, len(slugs))
	violations := make([]string, 0)
	for _, slug := range slugs {
		currentIdentity := current[slug]
		baselineIdentity, exists := baseline[slug]
		currentVersionErr := validateModuleSemver(currentIdentity.Version)
		if !exists {
			results = append(results, moduleVersionGuardResult{
				Slug:           slug,
				Status:         moduleVersionNew,
				CurrentVersion: currentIdentity.Version,
			})
			if currentVersionErr != nil {
				violations = append(violations, fmt.Sprintf("%s: new module version %q is not valid semantic version: %v", slug, currentIdentity.Version, currentVersionErr))
			}
			continue
		}

		result := moduleVersionGuardResult{
			Slug:            slug,
			Status:          moduleVersionUnchanged,
			BaselineVersion: baselineIdentity.Version,
			CurrentVersion:  currentIdentity.Version,
		}
		if currentVersionErr != nil {
			violations = append(violations, fmt.Sprintf("%s: current version %q is not valid semantic version: %v", slug, currentIdentity.Version, currentVersionErr))
			results = append(results, result)
			continue
		}
		if err := validateModuleSemver(baselineIdentity.Version); err != nil {
			violations = append(violations, fmt.Sprintf("%s: baseline version %q is not valid semantic version: %v", slug, baselineIdentity.Version, err))
			results = append(results, result)
			continue
		}
		if currentIdentity.Hash == baselineIdentity.Hash {
			results = append(results, result)
			continue
		}

		comparison, err := compareModuleSemver(currentIdentity.Version, baselineIdentity.Version)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: contract changed but versions cannot be ordered: %v", slug, err))
			results = append(results, result)
			continue
		}
		if comparison <= 0 {
			violations = append(violations, fmt.Sprintf(
				"%s: contract hash changed (%s -> %s) but version did not advance (%s -> %s)",
				slug,
				shortHash(baselineIdentity.Hash),
				shortHash(currentIdentity.Hash),
				baselineIdentity.Version,
				currentIdentity.Version,
			))
			results = append(results, result)
			continue
		}

		result.Status = moduleVersionAdvanced
		results = append(results, result)
	}

	if len(violations) > 0 {
		return results, &moduleVersionGuardError{violations: violations}
	}
	return results, nil
}

func validateModuleSemver(version string) error {
	_, err := resolvedplan.VersionAtLeast(version, version)
	return err
}

func compareModuleSemver(current, baseline string) (int, error) {
	currentAtLeast, err := resolvedplan.VersionAtLeast(current, baseline)
	if err != nil {
		return 0, fmt.Errorf("compare current %q with baseline %q: %w", current, baseline, err)
	}
	baselineAtLeast, err := resolvedplan.VersionAtLeast(baseline, current)
	if err != nil {
		return 0, fmt.Errorf("compare baseline %q with current %q: %w", baseline, current, err)
	}
	switch {
	case currentAtLeast && !baselineAtLeast:
		return 1, nil
	case baselineAtLeast && !currentAtLeast:
		return -1, nil
	default:
		return 0, nil
	}
}

func materializeModuleBaselineRef(ctx context.Context, currentModulesDir, baselineRef string) (string, func(), error) {
	repoRoot, modulesRelativePath, err := gitRepoAndModulesRelativePath(ctx, currentModulesDir)
	if err != nil {
		return "", func() {}, err
	}
	commit, err := gitOutput(ctx, repoRoot, "rev-parse", "--verify", "--end-of-options", strings.TrimSpace(baselineRef)+"^{commit}")
	if err != nil {
		return "", func() {}, fmt.Errorf("resolve baseline ref %q: %w", baselineRef, err)
	}
	commit = strings.TrimSpace(commit)

	tree, err := os.MkdirTemp("", "stackkit-module-baseline-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create baseline tree: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tree) }

	archiveCmd := exec.CommandContext(
		ctx,
		"git",
		"-C",
		repoRoot,
		"archive",
		"--format=tar",
		commit,
		"--",
		filepath.ToSlash(modulesRelativePath),
		"base",
		"cue.mod",
	)
	stdout, err := archiveCmd.StdoutPipe()
	if err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("open git archive for %s: %w", commit, err)
	}
	var stderr strings.Builder
	archiveCmd.Stderr = &stderr
	if err := archiveCmd.Start(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("start git archive for %s: %w", commit, err)
	}
	extractErr := extractTarTree(stdout, tree)
	// tar.Reader stops at the archive end marker, which may precede EOF on the
	// process pipe. Drain the trailer before Wait so git cannot block on a full
	// stdout pipe (observable on Windows runners).
	_, drainErr := io.Copy(io.Discard, stdout)
	waitErr := archiveCmd.Wait()
	if extractErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("extract baseline ref %s: %w", commit, extractErr)
	}
	if waitErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("archive baseline ref %s: %w: %s", commit, waitErr, strings.TrimSpace(stderr.String()))
	}
	if drainErr != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("drain baseline ref %s archive: %w", commit, drainErr)
	}

	modulesDir := filepath.Join(tree, modulesRelativePath)
	if !looksLikeModulesDir(modulesDir) {
		cleanup()
		return "", func() {}, fmt.Errorf("baseline ref %s contains no modules at %s", baselineRef, filepath.ToSlash(modulesRelativePath))
	}
	return modulesDir, cleanup, nil
}

func resolveBaselineModulesTree(currentModulesDir, baselineTree string) (string, error) {
	tree, err := filepath.Abs(strings.TrimSpace(baselineTree))
	if err != nil {
		return "", fmt.Errorf("resolve baseline tree: %w", err)
	}

	candidates := make([]string, 0, 3)
	if _, modulesRelativePath, gitErr := gitRepoAndModulesRelativePath(context.Background(), currentModulesDir); gitErr == nil {
		candidates = append(candidates, filepath.Join(tree, modulesRelativePath))
	}
	candidates = append(candidates, filepath.Join(tree, "modules"), tree)

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		candidate = filepath.Clean(candidate)
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if looksLikeModulesDir(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("baseline tree %s contains no module.cue contracts", tree)
}

func gitRepoAndModulesRelativePath(ctx context.Context, modulesDir string) (string, string, error) {
	repoRoot, err := gitOutput(ctx, modulesDir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("locate Git repository for modules directory %s: %w", modulesDir, err)
	}
	repoRoot = strings.TrimSpace(repoRoot)
	modulesDir, err = filepath.Abs(modulesDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve modules directory: %w", err)
	}
	relative, err := filepath.Rel(repoRoot, modulesDir)
	if err != nil {
		return "", "", fmt.Errorf("locate modules directory relative to repository: %w", err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("modules directory %s must be inside Git repository %s", modulesDir, repoRoot)
	}
	return repoRoot, relative, nil
}

func gitOutput(ctx context.Context, workdir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", workdir}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func extractTarTree(reader io.Reader, destination string) error {
	archive := tar.NewReader(reader)
	var extractedBytes int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		cleanName := path.Clean(header.Name)
		if cleanName == "." {
			continue
		}
		if path.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
			return fmt.Errorf("archive path %q escapes the baseline tree", header.Name)
		}
		target := filepath.Join(destination, filepath.FromSlash(cleanName))
		relative, err := filepath.Rel(destination, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path %q escapes the baseline tree", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			// Metadata emitted by git archive. archive/tar applies PAX values to
			// the following entry; there is no filesystem object to materialize.
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o750); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := extractTarRegularFile(archive, target, header, &extractedBytes); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported archive entry %q (type %d)", header.Name, header.Typeflag)
		}
	}
}

func extractTarRegularFile(reader io.Reader, target string, header *tar.Header, extractedBytes *int64) error {
	if header.Size < 0 || header.Size > maxBaselineEntryBytes {
		return fmt.Errorf(
			"archive entry %q size %d exceeds the %d-byte baseline limit",
			header.Name,
			header.Size,
			maxBaselineEntryBytes,
		)
	}
	if *extractedBytes > maxBaselineTreeBytes-header.Size {
		return fmt.Errorf("archive exceeds the %d-byte baseline tree limit", maxBaselineTreeBytes)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, header.FileInfo().Mode().Perm())
	if err != nil {
		return err
	}
	written, copyErr := io.CopyN(file, reader, header.Size)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if written != header.Size {
		return fmt.Errorf("archive entry %q extracted %d bytes, expected %d", header.Name, written, header.Size)
	}
	if closeErr != nil {
		return closeErr
	}
	*extractedBytes += written
	return nil
}

func looksLikeModulesDir(modulesDir string) bool {
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), "_") {
			continue
		}
		if _, err := os.Stat(filepath.Join(modulesDir, entry.Name(), "module.cue")); err == nil {
			return true
		}
	}
	return false
}
