package backuplifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
)

type ConfigureInput struct {
	OwnerRef       string           `json:"ownerRef"`
	AuthorityRef   string           `json:"authorityRef"`
	Lineage        AuthorityLineage `json:"lineage"`
	PolicyArtifact []byte           `json:"-"`
}

type Configuration struct {
	APIVersion           string            `json:"apiVersion"`
	OwnerRef             string            `json:"ownerRef"`
	AuthorityRef         string            `json:"authorityRef"`
	Lineage              AuthorityLineage  `json:"lineage"`
	PolicyArtifactDigest string            `json:"policyArtifactDigest"`
	OperationID          string            `json:"operationId"`
	Policy               Policy            `json:"policy"`
	Repository           RepositoryReceipt `json:"repository"`
}

type StatusInput struct {
	OwnerRef       string           `json:"ownerRef"`
	AuthorityRef   string           `json:"authorityRef"`
	Lineage        AuthorityLineage `json:"lineage"`
	PolicyArtifact []byte           `json:"-"`
}

type configurationIntent struct {
	APIVersion    string                  `json:"apiVersion"`
	Configuration RepositoryConfiguration `json:"configuration"`
}

type normalizedBinding struct {
	OwnerRef             string
	AuthorityRef         string
	Lineage              AuthorityLineage
	PolicyArtifactDigest string
	Policy               Policy
}

func normalizeBinding(ownerRef, authorityRef string, lineage AuthorityLineage, artifact []byte) (normalizedBinding, error) {
	if err := validateLineage(lineage); err != nil {
		return normalizedBinding{}, err
	}
	policy, err := decodePolicyArtifact(artifact)
	if err != nil {
		return normalizedBinding{}, err
	}
	return normalizedBinding{
		OwnerRef: ownerRef, AuthorityRef: authorityRef, Lineage: lineage,
		PolicyArtifactDigest: digestBytes(artifact), Policy: policy,
	}, nil
}

func (s *Service) Configure(ctx context.Context, input ConfigureInput) (Configuration, error) {
	if err := s.ready(ctx); err != nil {
		return Configuration{}, err
	}
	if err := RequireSettledRuntimeControls(s.workspaceRoot, ""); err != nil {
		return Configuration{}, err
	}
	owner, err := s.validateBinding(input.OwnerRef, input.AuthorityRef)
	if err != nil {
		return Configuration{}, err
	}
	binding, err := normalizeBinding(input.OwnerRef, input.AuthorityRef, input.Lineage, input.PolicyArtifact)
	if err != nil {
		return Configuration{}, err
	}
	if _, err := s.loadConfiguration(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Configuration{}, err
	}
	return s.configure(ctx, owner, input, binding, nil)
}

// Rebind moves an existing backup configuration to the current authority
// lineage and policy artifact after a converged mutation (a change set that
// adds a workload selects its data volume and re-signs Plan and Apply). It
// stays with the same Owner, authority and repository: a configuration of a
// different Owner or authority, or a runtime that would answer with another
// repository, fails closed. It reports whether the configuration moved and
// returns os.ErrNotExist when no configuration exists yet.
func (s *Service) Rebind(ctx context.Context, input ConfigureInput) (Configuration, bool, error) {
	if err := s.ready(ctx); err != nil {
		return Configuration{}, false, err
	}
	if err := RequireSettledRuntimeControls(s.workspaceRoot, ""); err != nil {
		return Configuration{}, false, err
	}
	owner, err := s.validateBinding(input.OwnerRef, input.AuthorityRef)
	if err != nil {
		return Configuration{}, false, err
	}
	binding, err := normalizeBinding(input.OwnerRef, input.AuthorityRef, input.Lineage, input.PolicyArtifact)
	if err != nil {
		return Configuration{}, false, err
	}
	previous, err := s.loadConfiguration()
	if err != nil {
		return Configuration{}, false, err
	}
	if previous.OwnerRef != owner.OwnerRef || previous.AuthorityRef != input.AuthorityRef {
		return Configuration{}, false, errors.New("backuplifecycle: backup configuration belongs to a different Owner or authority")
	}
	if configurationMatchesBinding(previous, binding) {
		return previous, false, nil
	}
	configuration, err := s.configure(ctx, owner, input, binding, &previous.Repository)
	return configuration, err == nil, err
}

func (s *Service) configure(
	ctx context.Context,
	owner localevidence.OwnerCustody,
	input ConfigureInput,
	binding normalizedBinding,
	expectedRepository *RepositoryReceipt,
) (Configuration, error) {
	repositoryConfiguration := RepositoryConfiguration{
		OwnerRef: owner.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: binding.Lineage, PolicyArtifactDigest: binding.PolicyArtifactDigest,
		Policy: clonePolicy(binding.Policy),
	}
	repositoryConfiguration.OperationID = configurationOperationID(repositoryConfiguration)
	if err := s.ensureConfigurationIntent(repositoryConfiguration); err != nil {
		return Configuration{}, err
	}
	repository, err := s.runtime.Configure(ctx, repositoryConfiguration)
	if err != nil {
		return Configuration{}, fmt.Errorf("backuplifecycle: configure repository: %w", err)
	}
	if err := validateRepositoryReceipt(repository, repositoryConfiguration); err != nil {
		return Configuration{}, err
	}
	if expectedRepository != nil && (repository.RepositoryID != expectedRepository.RepositoryID ||
		repository.Backend != expectedRepository.Backend) {
		return Configuration{}, errors.New("backuplifecycle: rebinding would move the backup to a different repository")
	}
	configuration := Configuration{
		APIVersion: configurationAPIVersion,
		OwnerRef:   owner.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: binding.Lineage, PolicyArtifactDigest: binding.PolicyArtifactDigest,
		OperationID: repositoryConfiguration.OperationID,
		Policy:      clonePolicy(binding.Policy), Repository: repository,
	}
	if err := s.writeJSON(configurationPath, configuration); err != nil {
		return Configuration{}, err
	}
	return configuration, nil
}

func (s *Service) Status(ctx context.Context, input StatusInput) (RepositoryStatus, error) {
	if err := s.ready(ctx); err != nil {
		return RepositoryStatus{}, err
	}
	configuration, err := s.boundConfiguration(input)
	if errors.Is(err, os.ErrNotExist) {
		return RepositoryStatus{}, &NotConfiguredError{}
	}
	if err != nil {
		return RepositoryStatus{}, err
	}
	status, err := s.repositoryStatus(ctx, configuration)
	if err != nil {
		return RepositoryStatus{}, err
	}
	history, err := s.history(ctx, configuration, time.Now().UTC(), status.Ready)
	if err != nil {
		if ctx.Err() != nil {
			return RepositoryStatus{}, ctx.Err()
		}
		history = History{
			ObservedAt: time.Now().UTC(), Scope: "current-source-policy-receipts", Issue: "history-could-not-be-authenticated",
			Snapshot: EvidenceAge{State: "unverified"}, StagedRestore: EvidenceAge{State: "unverified"},
		}
	}
	status.History = &history
	status.Coverage, err = sourceCoverage(configuration)
	if err != nil {
		return RepositoryStatus{}, err
	}
	return status, nil
}

// Snapshot admission reads current runtime readiness without scanning historical
// diagnostic receipts. An unrelated old receipt cannot disable a new backup.
func (s *Service) repositoryStatus(ctx context.Context, configuration Configuration) (RepositoryStatus, error) {
	status, err := s.runtime.Status(ctx, RepositoryScope{
		OwnerRef: configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		RepositoryID: configuration.Repository.RepositoryID, Lineage: configuration.Lineage,
	})
	if err != nil {
		return RepositoryStatus{}, fmt.Errorf("backuplifecycle: read repository status: %w", err)
	}
	if status.RepositoryID != configuration.Repository.RepositoryID {
		return RepositoryStatus{}, errors.New("backuplifecycle: repository status identifies a different repository")
	}
	if status.Ready && status.Consistency != ConsistencyCrashConsistent {
		return RepositoryStatus{}, errors.New("backuplifecycle: repository cannot truthfully report crash-consistent readiness")
	}
	return status, nil
}

func (s *Service) boundConfiguration(input StatusInput) (Configuration, error) {
	if _, err := s.validateBinding(input.OwnerRef, input.AuthorityRef); err != nil {
		return Configuration{}, err
	}
	binding, err := normalizeBinding(input.OwnerRef, input.AuthorityRef, input.Lineage, input.PolicyArtifact)
	if err != nil {
		return Configuration{}, err
	}
	configuration, err := s.loadConfiguration()
	if err != nil {
		return Configuration{}, err
	}
	if issue := configurationBindingIssue(configuration, binding); issue != nil {
		return Configuration{}, issue
	}
	return configuration, nil
}

// InspectConfigurationBinding reports, without touching the repository
// runtime, whether the stored backup configuration binds the authority the
// caller holds now. It returns os.ErrNotExist when no configuration exists
// and a *ConfigurationBindingError when one exists for an earlier Apply or
// policy artifact, so `stackkit status` can surface the same condition the
// backup commands fail on.
func InspectConfigurationBinding(workspaceRoot string, input StatusInput) (Configuration, error) {
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return Configuration{}, err
	}
	service := &Service{workspaceRoot: absolute}
	if _, err := service.validateBinding(input.OwnerRef, input.AuthorityRef); err != nil {
		return Configuration{}, err
	}
	binding, err := normalizeBinding(input.OwnerRef, input.AuthorityRef, input.Lineage, input.PolicyArtifact)
	if err != nil {
		return Configuration{}, err
	}
	configuration, err := service.loadConfiguration()
	if err != nil {
		return Configuration{}, err
	}
	if issue := configurationBindingIssue(configuration, binding); issue != nil {
		return configuration, issue
	}
	return configuration, nil
}

// ConfigurationBindingError reports a stored backup configuration that was
// made for an earlier authority than the one now in force. Every re-apply
// (a CLI upgrade included) re-signs Plan and Apply, so the lineage moves even
// when the owner changed nothing. The configuration, the repository and its
// snapshots stay intact; `stackkit backup configure` carries them forward to
// the current Apply with the same Owner custody and no new secret.
type ConfigurationBindingError struct {
	OwnerDiffers     bool
	AuthorityDiffers bool
	LineageDiffers   bool
	PolicyDiffers    bool
}

func configurationBindingIssue(configuration Configuration, binding normalizedBinding) *ConfigurationBindingError {
	issue := &ConfigurationBindingError{
		OwnerDiffers:     configuration.OwnerRef != binding.OwnerRef,
		AuthorityDiffers: configuration.AuthorityRef != binding.AuthorityRef,
		LineageDiffers:   !lineagesEqual(configuration.Lineage, binding.Lineage),
		PolicyDiffers: configuration.PolicyArtifactDigest != binding.PolicyArtifactDigest ||
			!policiesEqual(configuration.Policy, binding.Policy),
	}
	if !issue.OwnerDiffers && !issue.AuthorityDiffers && !issue.LineageDiffers && !issue.PolicyDiffers {
		return nil
	}
	return issue
}

func configurationMatchesBinding(configuration Configuration, binding normalizedBinding) bool {
	return configurationBindingIssue(configuration, binding) == nil
}

// CarryForwardable reports whether `stackkit backup configure` can move the
// configuration to the current authority: the Owner and human authority are
// unchanged and only the Apply lineage or the generated policy moved.
func (e *ConfigurationBindingError) CarryForwardable() bool {
	return e != nil && !e.OwnerDiffers && !e.AuthorityDiffers
}

func (e *ConfigurationBindingError) Error() string {
	if e == nil {
		return "backuplifecycle: backup configuration binding issue"
	}
	var changed []string
	if e.OwnerDiffers {
		changed = append(changed, "Owner")
	}
	if e.AuthorityDiffers {
		changed = append(changed, "human authority")
	}
	if e.LineageDiffers {
		changed = append(changed, "Plan/Apply lineage")
	}
	if e.PolicyDiffers {
		changed = append(changed, "backup policy artifact")
	}
	message := "backuplifecycle: backup configuration was made for an earlier " + strings.Join(changed, ", ") +
		" than the current Apply"
	if e.CarryForwardable() {
		return message + "; run `stackkit backup configure` to carry it forward (the repository, its snapshots and Owner custody stay the same)"
	}
	return message + "; the configuration cannot be carried forward automatically because it belongs to a different Owner or authority"
}

// Guidance lists the exact recovery commands for the actionable-error contract.
func (e *ConfigurationBindingError) Guidance() []string {
	if e.CarryForwardable() {
		return []string{
			"Run `stackkit backup configure` to carry the existing backup configuration forward to the current Apply; the repository, its snapshots and Owner custody stay the same and no new secret is created.",
			"If a scheduled backup timer is enabled, run `stackkit backup schedule enable --owner-approve` afterwards so the timer follows the current Apply and CLI; `stackkit status` shows both conditions.",
		}
	}
	return []string{
		"The stored backup configuration belongs to a different Owner or human authority than the current Apply; verify local Owner custody with `stackkit verify --json` before any backup command.",
	}
}

// NotConfiguredError reports a server on which no backup was ever configured:
// there is no repository, schedule or receipt the status could read. It is a
// local condition with exact remedies, reported instead of the bare missing
// file so a dashboard, an agent and a person see what is missing and what to
// do. It unwraps to os.ErrNotExist like the missing configuration it stands for.
type NotConfiguredError struct{}

func (e *NotConfiguredError) Error() string {
	return "backuplifecycle: no backup is configured on this server, so there is nothing to check; set one up first (see the guidance)"
}

func (e *NotConfiguredError) Unwrap() error { return os.ErrNotExist }

// Guidance lists what is missing and how to set a backup up, per kit.
func (e *NotConfiguredError) Guidance() []string {
	return []string{
		"No backup is configured on this server yet: it has no backup repository, so no snapshot or restore receipt exists to check.",
		"Basement Kit and the home server of a Modern Homelab: the owner sets it up once on the server with `stackkit backup configure`, takes a first snapshot with `stackkit backup run`, and keeps it current with `stackkit backup schedule enable --owner-approve`.",
		"Cloud Kit managed by kombify: the stack's managed backup schedule configures the repository and takes the first snapshot; enable the backup for the stack in Techstack, then check again after its first run.",
	}
}

func (s *Service) loadConfiguration() (Configuration, error) {
	var configuration Configuration
	if err := s.readJSON(configurationPath, &configuration); err != nil {
		return Configuration{}, err
	}
	if configuration.APIVersion != configurationAPIVersion ||
		!validDigest(configuration.PolicyArtifactDigest) ||
		!validDigest(configuration.OperationID) {
		return Configuration{}, errors.New("backuplifecycle: backup configuration identity is incomplete")
	}
	if err := validateLineage(configuration.Lineage); err != nil {
		return Configuration{}, err
	}
	if err := validatePolicy(configuration.Policy); err != nil {
		return Configuration{}, err
	}
	repositoryConfiguration := RepositoryConfiguration{
		OwnerRef: configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		Lineage: configuration.Lineage, PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		OperationID: configuration.OperationID, Policy: clonePolicy(configuration.Policy),
	}
	if configuration.OperationID != configurationOperationID(repositoryConfiguration) {
		return Configuration{}, errors.New("backuplifecycle: backup configuration operation digest does not verify")
	}
	if err := validateRepositoryReceipt(configuration.Repository, repositoryConfiguration); err != nil {
		return Configuration{}, err
	}
	return configuration, nil
}

func (s *Service) ensureConfigurationIntent(configuration RepositoryConfiguration) error {
	var existing configurationIntent
	intentPath := configurationIntentPath(configuration.OperationID)
	if err := s.readJSON(intentPath, &existing); err == nil {
		if existing.APIVersion != configurationIntentAPI ||
			!repositoryConfigurationsEqual(existing.Configuration, configuration) {
			return errors.New("backuplifecycle: content-addressed configuration intent does not verify")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return s.writeJSON(intentPath, configurationIntent{
		APIVersion: configurationIntentAPI, Configuration: configuration,
	})
}

func configurationIntentPath(operationID string) string {
	return configurationIntentDirectory + "/" + strings.TrimPrefix(operationID, "sha256:") + ".json"
}

func configurationOperationID(configuration RepositoryConfiguration) string {
	configuration.OperationID = ""
	return digestJSON(configuration)
}
