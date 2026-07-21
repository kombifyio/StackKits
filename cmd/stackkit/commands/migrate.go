package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/confinedfs"
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
	specOutput   string
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
	CandidateIntentSHA256    string                             `json:"candidateIntentSHA256" yaml:"candidateIntentSHA256"`
	MigrationDecisionRecord  map[string]any                     `json:"migrationDecisionRecord" yaml:"migrationDecisionRecord"`
	MigrationReportSHA256    string                             `json:"migrationReportSHA256" yaml:"migrationReportSHA256"`
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

--spec-output writes the exact completed canonical StackSpec v2 as deterministic
JSON. It is valid only with --complete-with, never defaults to an in-place rewrite,
and is governed by the same fail-if-exists/--force policy as --output.

A ready-for-shadow-resolution result exits successfully. A blocked result is
still emitted as machine-readable output, then the command exits unsuccessfully.
Context maps legacy locality and Pi hardware only; it never selects a Kit.

Examples:
  stackkit migrate stack-spec.yaml
  stackkit migrate legacy.yaml --target-kit cloud-kit
  stackkit migrate legacy.yaml --target-kit basement-kit --complete-with explicit-v2.yaml --spec-output stack-spec.v2.json
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
	cmd.Flags().StringVar(&options.specOutput, "spec-output", "", "Write the completed canonical StackSpec v2 as deterministic JSON beneath the working directory (requires --complete-with)")
	cmd.Flags().StringVar(&options.format, "format", "json", "Machine-readable output format: json or yaml")
	cmd.Flags().BoolVar(&options.force, "force", false, "Atomically replace existing migration result and canonical StackSpec output files")
	return cmd
}

func runMigrate(cmd *cobra.Command, args []string, options *migrateCLIOptions, wd string) (returnErr error) {
	if options == nil {
		return fmt.Errorf("migrate options are not initialized")
	}
	if strings.TrimSpace(options.specOutput) != "" && strings.TrimSpace(options.completeWith) == "" {
		return fmt.Errorf("--spec-output requires --complete-with so only a governed completed StackSpec v2 can be persisted")
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
	var outputSession *migrationOutputSession
	var reportTarget, specTarget string
	if migrationNeedsOutputSession(options.outputPath, options.specOutput) {
		outputSession, err = openMigrationOutputSession(wd)
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, outputSession.Close()) }()
		reportTarget, specTarget, err = outputSession.prepareTargets(inputPath, options.outputPath, options.specOutput, options.force)
		if err != nil {
			return err
		}
	}

	document, readErr := stackspecmigration.Read(raw)
	result := newMigrationResult(portableMigrationSourceRef(wd, inputPath), raw, options.targetKit)
	var migrationErr error
	var canonicalSpecBytes []byte
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
			service, serviceErr := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
			if serviceErr != nil {
				return fmt.Errorf("initialize embedded Architecture v2 authority for migration completion: %w", serviceErr)
			}
			completed, completeErr := stackspeccompletion.Complete(service, stackspeccompletion.Input{
				Legacy:           document,
				LegacySourceRef:  result.Source.Ref,
				Candidate:        candidate,
				TargetKitProfile: stackspecmigration.KitProfile(strings.TrimSpace(options.targetKit)),
			})
			result.Report = completed.Report
			result.Status = completed.Report.Status
			result.ArchitectureProjection = completed.ArchitectureProjection
			migrationErr = completeErr
			if completeErr == nil {
				var migrationDecisionRecord map[string]any
				if err := json.Unmarshal(completed.MigrationDecisionRecord, &migrationDecisionRecord); err != nil {
					return fmt.Errorf("decode canonical migration decision record: %w", err)
				}
				canonicalSpecBytes = append([]byte(nil), completed.CanonicalStackSpecBytes...)
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
					CandidateIntentSHA256:    completed.CandidateIntentSHA256,
					MigrationDecisionRecord:  migrationDecisionRecord,
					MigrationReportSHA256:    completed.MigrationReportSHA256,
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
	// Commit the self-contained audit result first. A later StackSpec install
	// failure can therefore leave only an honest report, never a replaced
	// in-place source whose audit record was not published.
	if err := outputSession.write(cmd.OutOrStdout(), reportTarget, encoded, options.force, "migration result"); err != nil {
		return err
	}
	if result.Completion != nil && specTarget != "" {
		if err := outputSession.write(nil, specTarget, canonicalSpecBytes, options.force, "canonical StackSpec v2"); err != nil {
			return err
		}
	}

	if migrationErr != nil || (result.Status != stackspecmigration.MigrationStatusReady && result.Status != stackspecmigration.MigrationStatusCompleted) {
		return &migrationCLIStatusError{Status: result.Status, Cause: migrationErr}
	}
	return nil
}

func migrationNeedsOutputSession(reportPath, specPath string) bool {
	report := strings.TrimSpace(reportPath)
	return (report != "" && report != "-") || strings.TrimSpace(specPath) != ""
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

type migrationOutputSession struct {
	root        *confinedfs.Root
	transaction *confinedfs.Transaction
	view        confinedfs.View
	lock        *confinedfs.OutputLock
}

func openMigrationOutputSession(wd string) (*migrationOutputSession, error) {
	root, err := confinedfs.Open(wd)
	if err != nil {
		return nil, fmt.Errorf("open held migration output root: %w", err)
	}
	transaction, err := root.BeginTransaction()
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("begin held migration output transaction: %w", err)
	}
	lock, err := transaction.TryAcquireOutputLock(".stackkit/migration-output")
	if err != nil {
		_ = transaction.Close()
		_ = root.Close()
		return nil, fmt.Errorf("acquire migration output lock: %w", err)
	}
	view, err := root.View(".")
	if err != nil {
		_ = lock.Close()
		_ = transaction.Close()
		_ = root.Close()
		return nil, fmt.Errorf("open migration output view: %w", err)
	}
	return &migrationOutputSession{root: root, transaction: transaction, view: view, lock: lock}, nil
}

func (s *migrationOutputSession) Close() error {
	if s == nil {
		return nil
	}
	var closeErrors []error
	if s.lock != nil {
		closeErrors = append(closeErrors, s.lock.Close())
		s.lock = nil
	}
	if s.transaction != nil {
		closeErrors = append(closeErrors, s.transaction.Close())
		s.transaction = nil
	}
	if s.root != nil {
		closeErrors = append(closeErrors, s.root.Close())
		s.root = nil
	}
	return errors.Join(closeErrors...)
}

func (s *migrationOutputSession) prepareTargets(inputPath, reportPath, specPath string, force bool) (string, string, error) {
	reportTarget, err := s.prepareTarget(reportPath, true, force, "migration result")
	if err != nil {
		return "", "", err
	}
	specTarget, err := s.prepareTarget(specPath, false, force, "canonical StackSpec v2")
	if err != nil {
		return "", "", err
	}
	if reportTarget != "" && specTarget != "" && sameMigrationOutputPath(reportTarget, specTarget) {
		return "", "", fmt.Errorf("--output and --spec-output must name different files")
	}
	inputTarget := s.portableTarget(inputPath)
	if reportTarget != "" && inputTarget != "" && sameMigrationOutputPath(reportTarget, inputTarget) {
		return "", "", fmt.Errorf("--output cannot replace the legacy input; use --spec-output with --force for an explicit in-place v2 migration")
	}
	if specTarget != "" && inputTarget != "" && sameMigrationOutputPath(specTarget, inputTarget) && !force {
		return "", "", fmt.Errorf("canonical StackSpec v2 output %s is the legacy input; use --force for an explicit in-place migration", specTarget)
	}
	return reportTarget, specTarget, nil
}

func (s *migrationOutputSession) prepareTarget(outputPath string, stdoutAllowed, force bool, label string) (string, error) {
	trimmed := strings.TrimSpace(outputPath)
	if trimmed == "" || (stdoutAllowed && trimmed == "-") {
		return "", nil
	}
	if trimmed == "-" {
		return "", fmt.Errorf("%s output must be a file beneath the working directory", label)
	}
	absolute, err := containedMigrationOutputPath(s.root.Name(), outputPath)
	if err != nil {
		return "", err
	}
	relative := s.portableTarget(absolute)
	if relative == "" {
		return "", fmt.Errorf("%s output %s is outside the held working directory", label, absolute)
	}
	parent := path.Dir(relative)
	if err := s.transaction.MkdirAll(parent, 0o750); err != nil {
		return "", fmt.Errorf("create held %s output directory: %w", label, err)
	}
	exists, _, err := s.transaction.Exists(relative)
	if err != nil {
		return "", fmt.Errorf("inspect held %s output %s: %w", label, absolute, err)
	}
	if exists && !force {
		return "", fmt.Errorf("%s output %s already exists; use --force to replace it", label, absolute)
	}
	return relative, nil
}

func (s *migrationOutputSession) portableTarget(target string) string {
	if s == nil || s.root == nil {
		return ""
	}
	absolute, err := filepath.Abs(target)
	if err != nil || !pathWithin(s.root.Name(), absolute) {
		return ""
	}
	relative, err := filepath.Rel(s.root.Name(), absolute)
	if err != nil || relative == "." {
		return ""
	}
	return filepath.ToSlash(relative)
}

func (s *migrationOutputSession) write(stdout io.Writer, target string, data []byte, force bool, label string) error {
	if target == "" {
		if stdout == nil {
			return fmt.Errorf("%s output target is not configured", label)
		}
		_, err := stdout.Write(data)
		return err
	}
	if s == nil {
		return fmt.Errorf("%s output session is not configured", label)
	}
	var (
		result confinedfs.AtomicWriteResult
		err    error
	)
	if force {
		result, err = s.view.WriteAtomic0600(target, data)
	} else {
		result, err = s.view.WriteAtomic0600NoReplace(target, data)
	}
	if err != nil {
		return fmt.Errorf("atomically install held %s %s: %w", label, target, err)
	}
	if !result.Installed || !result.FileSynced {
		return fmt.Errorf("atomically install held %s %s returned incomplete evidence: %#v", label, target, result)
	}
	return nil
}

func sameMigrationOutputPath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
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

func pathWithin(base, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(base), filepath.Clean(candidate))
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
