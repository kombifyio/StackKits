package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/stackspeccompletion"
	"github.com/kombifyio/stackkits/internal/stackspecmigration"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	migrationResultAPIVersion = "stackkit.migration-result/v1"
	migrationResultKind       = "StackSpecMigrationResult"
	maxMigrationInputBytes    = 1 << 20
)

type migrateCLIOptions struct {
	targetKit    string
	completeWith string
	outputPath   string
	format       string
	force        bool
}

type migrationResult struct {
	APIVersion             string                                     `json:"apiVersion" yaml:"apiVersion"`
	Kind                   string                                     `json:"kind" yaml:"kind"`
	Status                 stackspecmigration.MigrationStatus         `json:"status" yaml:"status"`
	Source                 migrationResultSource                      `json:"source" yaml:"source"`
	RequestedTargetKit     stackspecmigration.KitProfile              `json:"requestedTargetKit,omitempty" yaml:"requestedTargetKit,omitempty"`
	Report                 stackspecmigration.Report                  `json:"report" yaml:"report"`
	ArchitectureProjection *stackspecmigration.NormalizedArchitecture `json:"architectureProjection,omitempty" yaml:"architectureProjection,omitempty"`
	Completion             *migrationResultCompletion                 `json:"completion,omitempty" yaml:"completion,omitempty"`
	Safety                 migrationResultSafety                      `json:"safety" yaml:"safety"`
}

type migrationResultSource struct {
	Ref             string                           `json:"ref" yaml:"ref"`
	Classification  stackspecmigration.SourceVersion `json:"classification,omitempty" yaml:"classification,omitempty"`
	SHA256          string                           `json:"sha256" yaml:"sha256"`
	UnknownV1Fields []string                         `json:"unknownV1Fields" yaml:"unknownV1Fields"`
}

type migrationResultSafety struct {
	CUEValidV2        bool   `json:"cueValidV2" yaml:"cueValidV2"`
	GeneratorEligible bool   `json:"generatorEligible" yaml:"generatorEligible"`
	Notice            string `json:"notice" yaml:"notice"`
}

type migrationResultCompletion struct {
	APIVersion               string                             `json:"apiVersion" yaml:"apiVersion"`
	Kind                     string                             `json:"kind" yaml:"kind"`
	Status                   stackspecmigration.MigrationStatus `json:"status" yaml:"status"`
	Source                   migrationCompletionSource          `json:"source" yaml:"source"`
	CanonicalStackSpec       map[string]any                     `json:"canonicalStackSpec" yaml:"canonicalStackSpec"`
	CanonicalStackSpecSHA256 string                             `json:"canonicalStackSpecSHA256" yaml:"canonicalStackSpecSHA256"`
	ResolvedPlanHash         string                             `json:"resolvedPlanHash" yaml:"resolvedPlanHash"`
}

type migrationCompletionSource struct {
	Ref    string `json:"ref" yaml:"ref"`
	SHA256 string `json:"sha256" yaml:"sha256"`
}

// migrationCLIStatusError preserves the adapter cause while making a blocked
// process exit distinguishable from a successful ready-for-shadow-resolution
// report. The complete machine-readable result has already been emitted when
// this error is returned.
type migrationCLIStatusError struct {
	Status stackspecmigration.MigrationStatus
	Cause  error
}

func (e *migrationCLIStatusError) Error() string {
	if e == nil {
		return "StackSpec migration is blocked"
	}
	if e.Cause != nil {
		return fmt.Sprintf("StackSpec migration status %s: %v", e.Status, e.Cause)
	}
	return fmt.Sprintf("StackSpec migration status %s", e.Status)
}

func (e *migrationCLIStatusError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

var migrateCmd = newMigrateCommand()

func newMigrateCommand() *cobra.Command {
	options := &migrateCLIOptions{}
	cmd := &cobra.Command{
		Use:           "migrate [v1-spec-file]",
		Short:         "Classify a StackSpec v1 and emit its migration report",
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `Losslessly classify one StackSpec and run the bounded v1 to v2 migration
adapter. The result always includes the complete migration report and, only
when deterministic, an architecture-only migration projection.

IMPORTANT: without --complete-with, the projection is NOT a CUE-valid StackSpec v2, is NOT a ResolvedPlan,
and does NOT authorize generation or deployment. Complete the reported manual
actions and pass a full StackSpec v2 through CUE resolution first.

--complete-with accepts one full explicit StackSpec v2, never a partial overlay.
It reconciles deterministic legacy Site, node and hardware bindings, then uses
the embedded governed Architecture v2 authority. A completed result contains the
explicit canonical candidate and its ResolvedPlan hash. Generator eligibility is
reported independently from CUE validity and follows ResolvedPlan readiness.

A ready-for-shadow-resolution result exits successfully. A blocked result is
still emitted as machine-readable output, then the command exits unsuccessfully.
Context maps legacy locality and Pi hardware only; it never selects a Kit.

Examples:
  stackkit migrate stack-spec.yaml
  stackkit migrate legacy.yaml --target-kit cloud-kit
  stackkit migrate legacy.yaml --target-kit basement-kit --complete-with explicit-v2.yaml
  stackkit migrate legacy.yaml --format yaml
  stackkit migrate legacy.yaml --output .stackkit/migration-result.json`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMigrate(cmd, args, options, getWorkDir())
		},
	}
	cmd.Flags().StringVar(&options.targetKit, "target-kit", "", "Explicit target KitProfile supported by the authority (never inferred from context)")
	cmd.Flags().StringVar(&options.completeWith, "complete-with", "", "Full explicit StackSpec v2 candidate to reconcile and resolve with the embedded authority (never a partial overlay)")
	cmd.Flags().StringVarP(&options.outputPath, "output", "o", "", "Write the migration result beneath the working directory instead of stdout")
	cmd.Flags().StringVar(&options.format, "format", "json", "Machine-readable output format: json or yaml")
	cmd.Flags().BoolVar(&options.force, "force", false, "Atomically replace an existing output file")
	return cmd
}

func runMigrate(cmd *cobra.Command, args []string, options *migrateCLIOptions, wd string) error {
	if options == nil {
		return fmt.Errorf("migrate options are not initialized")
	}

	format, err := normalizeMigrationFormat(options.format)
	if err != nil {
		return err
	}
	inputPath := specFile
	if len(args) == 1 {
		inputPath = args[0]
	}
	inputPath = resolvePathFromWorkDir(wd, inputPath)
	raw, err := readMigrationInput(inputPath)
	if err != nil {
		return err
	}

	document, readErr := stackspecmigration.Read(raw)
	result := newMigrationResult(portableMigrationSourceRef(wd, inputPath), raw, options.targetKit)
	var migrationErr error
	if readErr != nil {
		result.Report = migrationReportForReadError(readErr)
		result.Status = stackspecmigration.MigrationStatusBlocked
		migrationErr = readErr
	} else {
		result.Source.Classification = document.Version
		result.Source.UnknownV1Fields = append([]string(nil), document.UnknownV1Fields...)
		if strings.TrimSpace(options.completeWith) == "" {
			projection, report, migrateErr := stackspecmigration.MigrateDocument(document, stackspecmigration.Options{
				TargetKitProfile: stackspecmigration.KitProfile(strings.TrimSpace(options.targetKit)),
			})
			result.Report = report
			result.Status = report.Status
			migrationErr = migrateErr
			if migrateErr == nil && report.Status == stackspecmigration.MigrationStatusReady {
				result.ArchitectureProjection = &projection
			}
		} else {
			completionPath := resolvePathFromWorkDir(wd, options.completeWith)
			candidate, candidateErr := readMigrationInput(completionPath)
			if candidateErr != nil {
				return candidateErr
			}
			service, serviceErr := architecturev2.NewEmbeddedService(architecturev2.StackKitsV06Contract(version))
			if serviceErr != nil {
				return fmt.Errorf("initialize embedded Architecture v2 authority for migration completion: %w", serviceErr)
			}
			completed, completeErr := stackspeccompletion.Complete(service, stackspeccompletion.Input{
				Legacy:           document,
				Candidate:        candidate,
				TargetKitProfile: stackspecmigration.KitProfile(strings.TrimSpace(options.targetKit)),
			})
			result.Report = completed.Report
			result.Status = completed.Report.Status
			result.ArchitectureProjection = completed.ArchitectureProjection
			migrationErr = completeErr
			if completeErr == nil {
				result.Completion = &migrationResultCompletion{
					APIVersion: "stackkit.migration-completion/v1",
					Kind:       "StackSpecV2Completion",
					Status:     completed.Status,
					Source: migrationCompletionSource{
						Ref:    portableMigrationSourceRef(wd, completionPath),
						SHA256: completed.CandidateSourceSHA256,
					},
					CanonicalStackSpec:       completed.CanonicalStackSpec,
					CanonicalStackSpecSHA256: completed.CanonicalStackSpecSHA256,
					ResolvedPlanHash:         completed.PlanHash,
				}
				result.Safety.CUEValidV2 = true
				result.Safety.GeneratorEligible = completed.GeneratorEligible
				if completed.GeneratorEligible {
					result.Safety.Notice = "Explicit StackSpec v2 passed the embedded governed authority and its ResolvedPlan reports generation readiness ready."
				} else {
					result.Safety.Notice = "Explicit StackSpec v2 passed the embedded governed authority, but its ResolvedPlan does not report generation readiness ready; generation remains blocked."
				}
			}
		}
	}

	encoded, err := encodeMigrationResult(result, format)
	if err != nil {
		return fmt.Errorf("encode migration result as %s: %w", format, err)
	}
	if err := writeMigrationResult(cmd.OutOrStdout(), wd, options.outputPath, encoded, options.force); err != nil {
		return err
	}

	if migrationErr != nil || (result.Status != stackspecmigration.MigrationStatusReady && result.Status != stackspecmigration.MigrationStatusCompleted) {
		return &migrationCLIStatusError{Status: result.Status, Cause: migrationErr}
	}
	return nil
}

func newMigrationResult(sourceRef string, raw []byte, requestedTarget string) migrationResult {
	digest := sha256.Sum256(raw)
	return migrationResult{
		APIVersion:         migrationResultAPIVersion,
		Kind:               migrationResultKind,
		Status:             stackspecmigration.MigrationStatusBlocked,
		RequestedTargetKit: stackspecmigration.KitProfile(strings.TrimSpace(requestedTarget)),
		Source: migrationResultSource{
			Ref:             sourceRef,
			SHA256:          "sha256:" + hex.EncodeToString(digest[:]),
			UnknownV1Fields: []string{},
		},
		Safety: migrationResultSafety{
			CUEValidV2:        false,
			GeneratorEligible: false,
			Notice:            "Architecture projection only; complete explicit v2 intent and CUE resolution before generation or deployment.",
		},
	}
}

// portableMigrationSourceRef makes reports reproducible across workstations
// and prevents absolute checkout/home paths from entering durable artifacts.
// Files under the working directory use a slash-normalized relative ref;
// explicitly external inputs retain only their basename and content hash.
func portableMigrationSourceRef(wd, resolvedInputPath string) string {
	base, baseErr := filepath.Abs(wd)
	input, inputErr := filepath.Abs(resolvedInputPath)
	if baseErr == nil && inputErr == nil && pathWithin(base, input) {
		if relative, err := filepath.Rel(base, input); err == nil && relative != "." {
			return filepath.ToSlash(relative)
		}
	}
	name := filepath.Base(filepath.Clean(resolvedInputPath))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "stack-spec"
	}
	return "external/" + filepath.ToSlash(name)
}

func migrationReportForReadError(err error) stackspecmigration.Report {
	code := "document.read-failed"
	message := err.Error()
	var typed *stackspecmigration.ReadError
	if errors.As(err, &typed) {
		code = typed.Code
		message = typed.Message
	}
	return stackspecmigration.Report{
		SourceVersion:              "unknown",
		TargetVersion:              stackspecmigration.APIVersionV2Alpha1,
		Status:                     stackspecmigration.MigrationStatusBlocked,
		RequiresExplicitAcceptance: true,
		Decisions:                  []stackspecmigration.Decision{},
		Warnings:                   []stackspecmigration.Warning{},
		ManualActions:              []stackspecmigration.ManualAction{},
		Blockers: []stackspecmigration.Blocker{{
			Code:           code,
			Field:          "document",
			Message:        message,
			RequiredInputs: []string{"losslessly readable StackSpec v1"},
		}},
	}
}

func normalizeMigrationFormat(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "json":
		return "json", nil
	case "yaml", "yml":
		return "yaml", nil
	default:
		return "", fmt.Errorf("unsupported migration output format %q: use json or yaml", raw)
	}
}

func readMigrationInput(path string) ([]byte, error) {
	file, err := os.Open(filepath.Clean(path)) // #nosec G304 -- explicit operator input, read-only and size-bounded below.
	if err != nil {
		return nil, fmt.Errorf("read StackSpec %s: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxMigrationInputBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read StackSpec %s: %w", path, err)
	}
	if len(data) > maxMigrationInputBytes {
		return nil, fmt.Errorf("StackSpec %s exceeds the %d byte migration limit", path, maxMigrationInputBytes)
	}
	return data, nil
}

func encodeMigrationResult(result migrationResult, format string) ([]byte, error) {
	var (
		data []byte
		err  error
	)
	if format == "yaml" {
		data, err = yaml.Marshal(result)
	} else {
		data, err = json.MarshalIndent(result, "", "  ")
	}
	if err != nil {
		return nil, err
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return data, nil
}

func writeMigrationResult(stdout io.Writer, wd, outputPath string, data []byte, force bool) error {
	if strings.TrimSpace(outputPath) == "" || strings.TrimSpace(outputPath) == "-" {
		_, err := stdout.Write(data)
		return err
	}

	target, err := containedMigrationOutputPath(wd, outputPath)
	if err != nil {
		return err
	}
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return fmt.Errorf("create migration output directory: %w", err)
	}
	if err := verifyMigrationOutputParent(wd, directory); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(directory, "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary migration result beside %s: %w", target, err)
	}
	temporaryPath := temporary.Name()
	installed := false
	defer func() {
		if installed {
			return
		}
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary migration result permissions: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary migration result: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary migration result: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary migration result: %w", err)
	}

	if force {
		if err := atomicReplaceMigrationFile(temporaryPath, target); err != nil {
			return fmt.Errorf("atomically install migration result %s: %w", target, err)
		}
	} else {
		// A same-directory hard link publishes the completely written inode in
		// one operation and fails if the destination appeared concurrently.
		if err := os.Link(temporaryPath, target); err != nil {
			if errors.Is(err, os.ErrExist) {
				return fmt.Errorf("migration output %s already exists; use --force to replace it", target)
			}
			return fmt.Errorf("atomically install migration result %s without overwrite: %w", target, err)
		}
		if err := os.Remove(temporaryPath); err != nil {
			_ = os.Remove(target)
			return fmt.Errorf("remove temporary migration result link: %w", err)
		}
	}
	installed = true
	if err := os.Chmod(target, 0o600); err != nil {
		return fmt.Errorf("enforce migration result permissions: %w", err)
	}
	return nil
}

func containedMigrationOutputPath(wd, outputPath string) (string, error) {
	base, err := filepath.Abs(wd)
	if err != nil {
		return "", fmt.Errorf("resolve migration working directory: %w", err)
	}
	target := filepath.Clean(outputPath)
	if !filepath.IsAbs(target) {
		target = filepath.Join(base, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return "", fmt.Errorf("resolve migration output path: %w", err)
	}
	if !pathWithin(base, target) {
		return "", fmt.Errorf("migration output %s escapes working directory %s", target, base)
	}
	if filepath.Clean(target) == filepath.Clean(base) {
		return "", fmt.Errorf("migration output must be a file beneath working directory %s", base)
	}
	return target, nil
}

func verifyMigrationOutputParent(wd, parent string) error {
	base, err := filepath.Abs(wd)
	if err != nil {
		return fmt.Errorf("resolve migration working directory: %w", err)
	}
	baseReal, err := filepath.EvalSymlinks(base)
	if err != nil {
		return fmt.Errorf("resolve migration working directory links: %w", err)
	}
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolve migration output directory links: %w", err)
	}
	if !pathWithin(baseReal, parentReal) {
		return fmt.Errorf("migration output directory %s resolves outside working directory %s", parentReal, baseReal)
	}
	return nil
}

func pathWithin(base, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(base), filepath.Clean(candidate))
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
