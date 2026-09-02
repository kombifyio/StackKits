// Package backuplifecycle owns the local, owner-authorized backup lifecycle
// journal while delegating repository mechanics to a narrow runtime boundary.
package backuplifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	configurationAPIVersion  = "stackkit.local-backup-configuration/v1"
	configurationIntentAPI   = "stackkit.local-backup-configuration-intent/v1"
	snapshotAnchorAPIVersion = "stackkit.local-backup-snapshot-anchor/v1"
	snapshotOperationAPI     = "stackkit.local-backup-snapshot-operation/v1"
	repositoryReceiptAPI     = "stackkit.local-backup-repository-receipt/v1"
	repositorySnapshotAPI    = "stackkit.local-backup-repository-snapshot/v1"
	restoreRecoveryAPI       = "stackkit.local-backup-restore-recovery-anchor/v1"
	restoreOperationAPI      = "stackkit.local-backup-restore-operation/v1"
	restoreResultAPI         = "stackkit.local-backup-restore-result/v1"
	repositoryRestoreAPI     = "stackkit.local-backup-repository-restore/v1"
	restoreVerificationAPI   = "stackkit.local-backup-restore-verification/v1"

	configurationPath            = ".stackkit/backups/configuration.json"
	configurationIntentDirectory = ".stackkit/backups/configuration-intents"
	anchorDirectory              = ".stackkit/backups/snapshot-anchors"
	operationDirectory           = ".stackkit/backups/operations"
	restoreRecoveryDirectory     = ".stackkit/backups/restore-recovery-anchors"
	restoreOperationDirectory    = ".stackkit/backups/restore-operations"
	restoreResultDirectory       = ".stackkit/backups/restore-results"
)

type Consistency string

const ConsistencyCrashConsistent Consistency = "crash-consistent"

// AuthorityLineage binds backup side effects to the complete verified
// generation and apply chain, rather than to a plan hash in isolation.
type AuthorityLineage struct {
	Binding               generationartifact.PlanBinding `json:"binding"`
	ManifestHash          string                         `json:"manifestHash"`
	GenerationReceiptHash string                         `json:"generationReceiptHash"`
	ApplyResultHash       string                         `json:"applyResultHash"`
	ApplyReceiptHash      string                         `json:"applyReceiptHash"`
	OwnerBindingDigest    string                         `json:"ownerBindingDigest"`
	PocketIDSubject       string                         `json:"pocketIdSubject"`
}

// Policy is the shared strict renderer-to-lifecycle contract.
type Policy = localbackuppolicy.Policy

type RepositoryConfiguration struct {
	OwnerRef             string           `json:"ownerRef"`
	AuthorityRef         string           `json:"authorityRef"`
	Lineage              AuthorityLineage `json:"lineage"`
	PolicyArtifactDigest string           `json:"policyArtifactDigest"`
	OperationID          string           `json:"operationId"`
	Policy               Policy           `json:"policy"`
}

type RepositoryScope struct {
	OwnerRef     string           `json:"ownerRef"`
	AuthorityRef string           `json:"authorityRef"`
	RepositoryID string           `json:"repositoryId"`
	Lineage      AuthorityLineage `json:"lineage"`
}

type RepositoryReceipt struct {
	APIVersion          string `json:"apiVersion"`
	RepositoryID        string `json:"repositoryId"`
	Backend             string `json:"backend"`
	ConfigurationDigest string `json:"configurationDigest"`
}

type RepositoryStatus struct {
	RepositoryID string          `json:"repositoryId"`
	Ready        bool            `json:"ready"`
	Consistency  Consistency     `json:"consistency"`
	History      *History        `json:"history,omitempty"`
	Coverage     *BackupCoverage `json:"coverage,omitempty"`
}

type RepositorySnapshotRequest struct {
	OwnerRef             string           `json:"ownerRef"`
	AuthorityRef         string           `json:"authorityRef"`
	Lineage              AuthorityLineage `json:"lineage"`
	PolicyArtifactDigest string           `json:"policyArtifactDigest"`
	RepositoryID         string           `json:"repositoryId"`
	OperationID          string           `json:"operationId"`
	Source               string           `json:"source"`
	Excludes             []string         `json:"excludes"`
	Consistency          Consistency      `json:"consistency"`
	ProtectRecovery      bool             `json:"protectRecovery,omitempty"`
}

type RepositorySnapshotReceipt struct {
	APIVersion    string      `json:"apiVersion"`
	RepositoryID  string      `json:"repositoryId"`
	SnapshotID    string      `json:"snapshotId"`
	OperationID   string      `json:"operationId"`
	RequestDigest string      `json:"requestDigest"`
	ContentDigest string      `json:"contentDigest"`
	Consistency   Consistency `json:"consistency"`
	CreatedAt     time.Time   `json:"createdAt"`
}

// SnapshotQuiescence is the journaled pre-snapshot Docker identity set. The
// operation journal persists this value before the first stop mutation so a
// retry can address only the exact containers that were running at capture
// time. CaptureStartedAt, when present, precedes the first writer stop and
// provides a conservative age boundary, not application-consistency evidence.
type SnapshotQuiescence struct {
	Phase            string                      `json:"phase"`
	GraphDigest      string                      `json:"graphDigest,omitempty"`
	CaptureStartedAt *time.Time                  `json:"captureStartedAt,omitempty"`
	Containers       []SnapshotQuiescedContainer `json:"containers"`
}

type SnapshotQuiescedContainer struct {
	ID             string                 `json:"id"`
	Name           string                 `json:"name"`
	WasRunning     bool                   `json:"wasRunning"`
	WorkloadRef    string                 `json:"workloadRef,omitempty"`
	SiteRef        string                 `json:"siteRef,omitempty"`
	NodeRef        string                 `json:"nodeRef,omitempty"`
	ComposeProject string                 `json:"composeProject,omitempty"`
	ComposeService string                 `json:"composeService,omitempty"`
	ComponentRef   string                 `json:"componentRef,omitempty"`
	Image          string                 `json:"image,omitempty"`
	StopOrder      int                    `json:"stopOrder,omitempty"`
	Mounts         []SnapshotQuiesceMount `json:"mounts"`
}

type SnapshotQuiesceMount struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	RW          bool   `json:"rw"`
	Propagation string `json:"propagation"`
}

type RepositoryRestoreRequest struct {
	OwnerRef             string                    `json:"ownerRef"`
	AuthorityRef         string                    `json:"authorityRef"`
	AuthorizationLineage AuthorityLineage          `json:"authorizationLineage"`
	PolicyArtifactDigest string                    `json:"policyArtifactDigest"`
	RepositoryID         string                    `json:"repositoryId"`
	SnapshotAnchorID     string                    `json:"snapshotAnchorId"`
	SnapshotSourceDigest string                    `json:"snapshotSourceDigest"`
	SnapshotRequest      RepositorySnapshotRequest `json:"snapshotRequest"`
	SnapshotReceipt      RepositorySnapshotReceipt `json:"snapshotReceipt"`
	OperationID          string                    `json:"operationId"`
	StagingPath          string                    `json:"stagingPath"`
}

type RepositoryRestoreReceipt struct {
	APIVersion                string    `json:"apiVersion"`
	RepositoryID              string    `json:"repositoryId"`
	SnapshotID                string    `json:"snapshotId"`
	OperationID               string    `json:"operationId"`
	RequestDigest             string    `json:"requestDigest"`
	StagingPath               string    `json:"stagingPath"`
	SnapshotContentDigest     string    `json:"snapshotContentDigest"`
	RepositoryContentVerified bool      `json:"repositoryContentVerified"`
	CompletedAt               time.Time `json:"completedAt"`
}

// RepositoryRuntime owns repository-specific side effects. LookupSnapshot
// must return a receipt only for the exact request digest, allowing a pending
// journal to recover after repository success without duplicating a snapshot.
type RepositoryRuntime interface {
	Configure(context.Context, RepositoryConfiguration) (RepositoryReceipt, error)
	Status(context.Context, RepositoryScope) (RepositoryStatus, error)
	LookupSnapshot(context.Context, RepositorySnapshotRequest) (RepositorySnapshotReceipt, bool, error)
	CreateSnapshot(context.Context, RepositorySnapshotRequest) (RepositorySnapshotReceipt, error)
	RestoreSnapshot(context.Context, RepositoryRestoreRequest) (RepositoryRestoreReceipt, error)
}

// SnapshotQuiesceRuntime is an optional extension for productive runtimes
// that can stop and restore Docker writers around one Kopia snapshot. Keeping
// it optional preserves legacy repository fakes and v1 journals.
type SnapshotQuiesceRuntime interface {
	CreateSnapshotWithQuiescence(
		context.Context,
		RepositorySnapshotRequest,
		SnapshotQuiescence,
		func(SnapshotQuiescence) error,
	) (RepositorySnapshotReceipt, error)
}

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

type RunInput struct {
	OwnerRef        string           `json:"ownerRef"`
	AuthorityRef    string           `json:"authorityRef"`
	Lineage         AuthorityLineage `json:"lineage"`
	PolicyArtifact  []byte           `json:"-"`
	OperationID     string           `json:"operationId"`
	ProtectRecovery bool             `json:"protectRecovery,omitempty"`
}

type RestoreVerificationRequest struct {
	OwnerRef             string           `json:"ownerRef"`
	AuthorizationLineage AuthorityLineage `json:"authorizationLineage"`
	SnapshotAnchorID     string           `json:"snapshotAnchorId"`
	OperationID          string           `json:"operationId"`
	StagingPath          string           `json:"stagingPath"`
}

type RestoreVerification struct {
	APIVersion         string    `json:"apiVersion"`
	OwnerRef           string    `json:"ownerRef"`
	OwnerBindingDigest string    `json:"ownerBindingDigest"`
	PocketIDSubject    string    `json:"pocketIdSubject"`
	PlanHash           string    `json:"planHash"`
	ServicesVerified   bool      `json:"servicesVerified"`
	VerifiedAt         time.Time `json:"verifiedAt"`
}

type RestorePostVerifier func(context.Context, RestoreVerificationRequest) (RestoreVerification, error)

type RestoreInput struct {
	OwnerRef         string              `json:"ownerRef"`
	AuthorityRef     string              `json:"authorityRef"`
	Lineage          AuthorityLineage    `json:"lineage"`
	PolicyArtifact   []byte              `json:"-"`
	SnapshotAnchorID string              `json:"snapshotAnchorId"`
	OperationID      string              `json:"operationId"`
	OwnerApproved    bool                `json:"ownerApproved"`
	PostVerify       RestorePostVerifier `json:"-"`
}

type operationInput struct {
	OwnerRef             string           `json:"ownerRef"`
	AuthorityRef         string           `json:"authorityRef"`
	Lineage              AuthorityLineage `json:"lineage"`
	PolicyArtifactDigest string           `json:"policyArtifactDigest"`
	OperationID          string           `json:"operationId"`
	Policy               Policy           `json:"policy"`
	ProtectRecovery      bool             `json:"protectRecovery,omitempty"`
}

type SnapshotAnchor struct {
	APIVersion           string                                     `json:"apiVersion"`
	ID                   string                                     `json:"id"`
	OwnerRef             string                                     `json:"ownerRef"`
	AuthorityRef         string                                     `json:"authorityRef"`
	Lineage              AuthorityLineage                           `json:"lineage"`
	PolicyArtifactDigest string                                     `json:"policyArtifactDigest"`
	OperationID          string                                     `json:"operationId"`
	Policy               Policy                                     `json:"policy"`
	Repository           RepositoryReceipt                          `json:"repository"`
	Snapshot             RepositorySnapshotReceipt                  `json:"snapshot"`
	Quiescence           *SnapshotQuiescence                        `json:"quiescence,omitempty"`
	Signature            localevidence.OwnerSnapshotAnchorSignature `json:"signature"`
	ProtectRecovery      bool                                       `json:"protectRecovery,omitempty"`
}

type RestoreRecoveryAnchor struct {
	APIVersion            string                                      `json:"apiVersion"`
	ID                    string                                      `json:"id"`
	OwnerRef              string                                      `json:"ownerRef"`
	AuthorityRef          string                                      `json:"authorityRef"`
	AuthorizationLineage  AuthorityLineage                            `json:"authorizationLineage"`
	PolicyArtifactDigest  string                                      `json:"policyArtifactDigest"`
	SnapshotAnchorID      string                                      `json:"snapshotAnchorId"`
	SnapshotLineage       AuthorityLineage                            `json:"snapshotLineage"`
	RepositoryID          string                                      `json:"repositoryId"`
	SnapshotID            string                                      `json:"snapshotId"`
	SnapshotContentDigest string                                      `json:"snapshotContentDigest"`
	OperationID           string                                      `json:"operationId"`
	StagingPath           string                                      `json:"stagingPath"`
	Mode                  string                                      `json:"mode"`
	ApprovalMethod        string                                      `json:"approvalMethod"`
	ApprovedAt            time.Time                                   `json:"approvedAt"`
	ExpiresAt             time.Time                                   `json:"expiresAt"`
	Signature             localevidence.OwnerRestoreRecoverySignature `json:"signature"`
}

type RestoreResult struct {
	APIVersion           string                                    `json:"apiVersion"`
	ID                   string                                    `json:"id"`
	OwnerRef             string                                    `json:"ownerRef"`
	AuthorityRef         string                                    `json:"authorityRef"`
	AuthorizationLineage AuthorityLineage                          `json:"authorizationLineage"`
	SnapshotAnchorID     string                                    `json:"snapshotAnchorId"`
	SnapshotLineage      AuthorityLineage                          `json:"snapshotLineage"`
	OperationID          string                                    `json:"operationId"`
	RecoveryAnchor       RestoreRecoveryAnchor                     `json:"recoveryAnchor"`
	Request              RepositoryRestoreRequest                  `json:"request"`
	Receipt              RepositoryRestoreReceipt                  `json:"receipt"`
	Verification         RestoreVerification                       `json:"verification"`
	Signature            localevidence.OwnerRestoreResultSignature `json:"signature"`
}

type configurationIntent struct {
	APIVersion    string                  `json:"apiVersion"`
	Configuration RepositoryConfiguration `json:"configuration"`
}

type snapshotOperation struct {
	APIVersion string                                         `json:"apiVersion"`
	State      string                                         `json:"state"`
	Input      operationInput                                 `json:"input"`
	Quiescence *SnapshotQuiescence                            `json:"quiescence,omitempty"`
	Anchor     *SnapshotAnchor                                `json:"anchor,omitempty"`
	Signature  *localevidence.OwnerLifecycleMutationSignature `json:"signature,omitempty"`
}

type restoreOperation struct {
	APIVersion  string                    `json:"apiVersion"`
	State       string                    `json:"state"`
	Input       restoreOperationInput     `json:"input"`
	Recovery    RestoreRecoveryAnchor     `json:"recoveryAnchor"`
	Receipt     *RepositoryRestoreReceipt `json:"receipt,omitempty"`
	Result      *RestoreResult            `json:"result,omitempty"`
	Abandonment *RestoreAbandonment       `json:"abandonment,omitempty"`
}

type restoreOperationInput struct {
	OwnerRef             string           `json:"ownerRef"`
	AuthorityRef         string           `json:"authorityRef"`
	AuthorizationLineage AuthorityLineage `json:"authorizationLineage"`
	PolicyArtifactDigest string           `json:"policyArtifactDigest"`
	SnapshotAnchorID     string           `json:"snapshotAnchorId"`
	OperationID          string           `json:"operationId"`
}

type Creator struct{}

func NewCreator() Creator { return Creator{} }

func (Creator) Create(workspaceRoot string, runtime RepositoryRuntime) (*Service, error) {
	if runtime == nil {
		return nil, errors.New("backuplifecycle: repository runtime is required")
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: open workspace: %w", err)
	}
	absolute := root.Name()
	if err := root.Close(); err != nil {
		return nil, fmt.Errorf("backuplifecycle: close workspace validation root: %w", err)
	}
	return &Service{workspaceRoot: absolute, runtime: runtime}, nil
}

type Service struct {
	workspaceRoot string
	runtime       RepositoryRuntime
}

func (s *Service) Configure(ctx context.Context, input ConfigureInput) (Configuration, error) {
	if err := s.ready(ctx); err != nil {
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
	status.Coverage = sourceCoverage(configuration)
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

func (s *Service) Run(ctx context.Context, input RunInput) (SnapshotAnchor, error) {
	if err := s.ready(ctx); err != nil {
		return SnapshotAnchor{}, err
	}
	if !validOperationID(input.OperationID) {
		return SnapshotAnchor{}, errors.New("backuplifecycle: operation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}")
	}
	configuration, err := s.boundConfiguration(StatusInput{
		OwnerRef: input.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: input.Lineage, PolicyArtifact: input.PolicyArtifact,
	})
	if err != nil {
		return SnapshotAnchor{}, err
	}
	boundInput := operationInput{
		OwnerRef: configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		Lineage: configuration.Lineage, PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		OperationID: input.OperationID, Policy: clonePolicy(configuration.Policy), ProtectRecovery: input.ProtectRecovery,
	}
	operationPath := operationDirectory + "/" + operationKey(input.OperationID) + ".json"
	pending := false
	var pendingOperation snapshotOperation
	if operation, loadErr := s.loadOperation(operationPath); loadErr == nil {
		if !operationInputsEqual(operation.Input, boundInput) {
			return SnapshotAnchor{}, errors.New("backuplifecycle: operation intent differs from requested authority lineage, policy, or operation")
		}
		switch {
		case operation.State == "completed" && operation.Anchor != nil:
			if err := VerifySnapshotAnchor(s.workspaceRoot, *operation.Anchor); err != nil {
				return SnapshotAnchor{}, err
			}
			if !anchorMatchesOperation(*operation.Anchor, operation) {
				return SnapshotAnchor{}, errors.New("backuplifecycle: completed operation anchor differs from its exact input")
			}
			stored, err := s.loadStoredSnapshotAnchor(operation.Anchor.ID)
			if err != nil {
				return SnapshotAnchor{}, err
			}
			if !reflect.DeepEqual(stored, *operation.Anchor) {
				return SnapshotAnchor{}, errors.New("backuplifecycle: completed operation journal differs from its content-addressed snapshot anchor")
			}
			return stored, nil
		case operation.State == "pending" && operation.Anchor == nil:
			pending, pendingOperation = true, operation
		default:
			return SnapshotAnchor{}, errors.New("backuplifecycle: snapshot operation journal is malformed")
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return SnapshotAnchor{}, loadErr
	}
	if err := s.admitSnapshotRetention(ctx, configuration); err != nil {
		return SnapshotAnchor{}, err
	}

	request := snapshotRequest(configuration, input.OperationID)
	request.ProtectRecovery = input.ProtectRecovery
	quiescedRuntime, supportsQuiescence := s.runtime.(SnapshotQuiesceRuntime)
	persistQuiescence := func(quiescence SnapshotQuiescence) error {
		return s.persistSnapshotQuiescence(operationPath, boundInput, quiescence)
	}
	if pending && pendingOperation.Quiescence != nil && !supportsQuiescence {
		return SnapshotAnchor{}, errors.New("backuplifecycle: journaled snapshot quiescence requires a quiescence-capable runtime")
	}
	if pending && supportsQuiescence {
		if pendingOperation.Quiescence == nil {
			status, statusErr := s.repositoryStatus(ctx, configuration)
			if statusErr != nil {
				return SnapshotAnchor{}, statusErr
			}
			if !status.Ready || status.Consistency != ConsistencyCrashConsistent {
				return SnapshotAnchor{}, errors.New("backuplifecycle: repository is not ready for a crash-consistent snapshot")
			}
		}
		quiescence := SnapshotQuiescence{}
		if pendingOperation.Quiescence != nil {
			quiescence = cloneSnapshotQuiescence(*pendingOperation.Quiescence)
		}
		snapshot, quiesceErr := quiescedRuntime.CreateSnapshotWithQuiescence(ctx, request, quiescence, persistQuiescence)
		if quiesceErr != nil {
			return SnapshotAnchor{}, fmt.Errorf("backuplifecycle: create repository snapshot: %w", quiesceErr)
		}
		if err := validateSnapshotReceipt(snapshot, request); err != nil {
			return SnapshotAnchor{}, err
		}
		return s.completeSnapshot(operationPath, boundInput, configuration.Repository, snapshot, true)
	}
	if pending {
		snapshot, found, lookupErr := s.runtime.LookupSnapshot(ctx, request)
		if lookupErr != nil {
			return SnapshotAnchor{}, fmt.Errorf("backuplifecycle: lookup pending repository snapshot: %w", lookupErr)
		}
		if found {
			if err := validateSnapshotReceipt(snapshot, request); err != nil {
				return SnapshotAnchor{}, err
			}
			return s.completeSnapshot(operationPath, boundInput, configuration.Repository, snapshot, false)
		}
	}

	status, err := s.repositoryStatus(ctx, configuration)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	if !status.Ready || status.Consistency != ConsistencyCrashConsistent {
		return SnapshotAnchor{}, errors.New("backuplifecycle: repository is not ready for a crash-consistent snapshot")
	}
	if !pending {
		if err := s.writeSnapshotOperation(operationPath, snapshotOperation{
			APIVersion: snapshotOperationAPI, State: "pending", Input: cloneOperationInput(boundInput),
		}); err != nil {
			return SnapshotAnchor{}, err
		}
	}
	var snapshot RepositorySnapshotReceipt
	if supportsQuiescence {
		snapshot, err = quiescedRuntime.CreateSnapshotWithQuiescence(ctx, request, SnapshotQuiescence{}, persistQuiescence)
	} else {
		snapshot, err = s.runtime.CreateSnapshot(ctx, request)
	}
	if err != nil {
		return SnapshotAnchor{}, fmt.Errorf("backuplifecycle: create repository snapshot: %w", err)
	}
	if err := validateSnapshotReceipt(snapshot, request); err != nil {
		return SnapshotAnchor{}, err
	}
	return s.completeSnapshot(operationPath, boundInput, configuration.Repository, snapshot, supportsQuiescence)
}

// Restore verifies one owner-signed snapshot anchor, stages its complete
// contents below the governed isolated restore volume, and signs evidence only
// after the caller re-verifies the current local Plan/Apply/Owner/service
// closure. It deliberately does not mutate live Docker volumes.
func (s *Service) Restore(ctx context.Context, input RestoreInput) (RestoreResult, error) {
	if err := s.ready(ctx); err != nil {
		return RestoreResult{}, err
	}
	if !input.OwnerApproved {
		return RestoreResult{}, errors.New("backuplifecycle: restore requires explicit local Owner approval")
	}
	if !validOperationID(input.OperationID) {
		return RestoreResult{}, errors.New("backuplifecycle: restore operation ID must match [A-Za-z0-9][A-Za-z0-9._-]{0,127}")
	}
	if !validDigest(input.SnapshotAnchorID) {
		return RestoreResult{}, errors.New("backuplifecycle: restore requires a content-addressed snapshot anchor ID")
	}
	if input.PostVerify == nil {
		return RestoreResult{}, errors.New("backuplifecycle: restore requires a current local post-verifier")
	}
	configuration, err := s.boundConfiguration(StatusInput{
		OwnerRef: input.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: input.Lineage, PolicyArtifact: input.PolicyArtifact,
	})
	if err != nil {
		return RestoreResult{}, err
	}
	anchor, err := s.loadStoredSnapshotAnchor(input.SnapshotAnchorID)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := validateRestoreSource(configuration, anchor); err != nil {
		return RestoreResult{}, err
	}
	status, err := s.Status(ctx, StatusInput{
		OwnerRef: input.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: input.Lineage, PolicyArtifact: input.PolicyArtifact,
	})
	if err != nil {
		return RestoreResult{}, err
	}
	if !status.Ready || status.Consistency != ConsistencyCrashConsistent {
		return RestoreResult{}, errors.New("backuplifecycle: repository is not ready for a verified staged restore")
	}

	operationInput := restoreOperationInput{
		OwnerRef: configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		AuthorizationLineage: configuration.Lineage,
		PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		SnapshotAnchorID:     anchor.ID, OperationID: input.OperationID,
	}
	operationPath := restoreOperationDirectory + "/" + operationKey(input.OperationID) + ".json"
	recovery, stagedReceipt, existingResult, err := s.prepareRestoreOperation(operationPath, operationInput, configuration, anchor)
	if err != nil {
		return RestoreResult{}, err
	}
	if existingResult != nil {
		return *existingResult, nil
	}

	request, err := repositoryRestoreRequest(configuration, anchor, input.OperationID)
	if err != nil {
		return RestoreResult{}, err
	}
	var receipt RepositoryRestoreReceipt
	if stagedReceipt != nil {
		receipt = *stagedReceipt
	} else {
		if !restoreApprovalCurrent(recovery, time.Now().UTC()) {
			return RestoreResult{}, errors.New("backuplifecycle: restore Owner approval expired before staging; use a new operation ID")
		}
		receipt, err = s.runtime.RestoreSnapshot(ctx, request)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("backuplifecycle: stage repository snapshot: %w", err)
		}
		if err := validateRestoreReceipt(receipt, request); err != nil {
			return RestoreResult{}, err
		}
		if !restoreApprovalCurrent(recovery, receipt.CompletedAt) || receipt.CompletedAt.After(time.Now().UTC()) {
			return RestoreResult{}, errors.New("backuplifecycle: staged restore receipt is outside its Owner approval")
		}
		if err := s.persistStagedRestore(operationPath, operationInput, recovery, receipt); err != nil {
			return RestoreResult{}, err
		}
	}
	verificationStartedAt := time.Now().UTC()
	if !restoreApprovalCurrent(recovery, verificationStartedAt) {
		return RestoreResult{}, errors.New("backuplifecycle: restore Owner approval is not current before verification; authorize a new operation")
	}
	verification, err := input.PostVerify(ctx, RestoreVerificationRequest{
		OwnerRef:             configuration.OwnerRef,
		AuthorizationLineage: configuration.Lineage,
		SnapshotAnchorID:     anchor.ID,
		OperationID:          input.OperationID,
		StagingPath:          request.StagingPath,
	})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("backuplifecycle: verify local platform after staged restore: %w", err)
	}
	if err := validateRestoreVerification(verification, configuration, receipt); err != nil {
		return RestoreResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RestoreResult{}, err
	}
	if err := validateFreshRestoreVerification(verification, recovery, verificationStartedAt, time.Now().UTC()); err != nil {
		return RestoreResult{}, err
	}
	return s.completeRestore(operationPath, recovery, anchor, receipt, verification)
}

func (s *Service) prepareRestoreOperation(
	operationPath string,
	input restoreOperationInput,
	configuration Configuration,
	anchor SnapshotAnchor,
) (RestoreRecoveryAnchor, *RepositoryRestoreReceipt, *RestoreResult, error) {
	if existing, err := s.loadRestoreOperation(operationPath); err == nil {
		if !reflect.DeepEqual(existing.Input, input) {
			return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: restore operation differs from its persisted owner-authorized input")
		}
		if err := VerifyRestoreRecoveryAnchor(s.workspaceRoot, existing.Recovery); err != nil {
			return RestoreRecoveryAnchor{}, nil, nil, err
		}
		if err := validateRestoreRecoveryBinding(existing.Recovery, input, configuration, anchor); err != nil {
			return RestoreRecoveryAnchor{}, nil, nil, err
		}
		switch existing.State {
		case "pending":
			if existing.Receipt != nil || existing.Result != nil || existing.Abandonment != nil {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: pending restore operation unexpectedly contains a receipt or result")
			}
			return existing.Recovery, nil, nil, nil
		case "staged":
			if existing.Receipt == nil || existing.Result != nil || existing.Abandonment != nil {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: staged restore operation must contain only its repository receipt")
			}
			request, requestErr := repositoryRestoreRequest(configuration, anchor, input.OperationID)
			if requestErr != nil {
				return RestoreRecoveryAnchor{}, nil, nil, requestErr
			}
			if err := validateRestoreReceipt(*existing.Receipt, request); err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			receipt := *existing.Receipt
			return existing.Recovery, &receipt, nil, nil
		case "completed":
			if existing.Result == nil {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: completed restore operation has no result")
			}
			if err := VerifyRestoreResult(s.workspaceRoot, *existing.Result); err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			stored, err := s.loadStoredRestoreResult(existing.Result.ID)
			if err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			if !reflect.DeepEqual(stored, *existing.Result) {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: restore journal differs from its content-addressed result")
			}
			return existing.Recovery, nil, &stored, nil
		case "abandoned":
			if existing.Abandonment == nil || existing.Result != nil {
				return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: abandoned restore operation lacks its terminal evidence")
			}
			if err := VerifyRestoreAbandonment(s.workspaceRoot, *existing.Abandonment); err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			if err := validateRestoreAbandonmentBinding(existing, anchor, *existing.Abandonment); err != nil {
				return RestoreRecoveryAnchor{}, nil, nil, err
			}
			if existing.Receipt != nil {
				request, requestErr := repositoryRestoreRequest(configuration, anchor, input.OperationID)
				if requestErr != nil {
					return RestoreRecoveryAnchor{}, nil, nil, requestErr
				}
				if err := validateRestoreReceipt(*existing.Receipt, request); err != nil {
					return RestoreRecoveryAnchor{}, nil, nil, err
				}
			}
			return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: restore operation was explicitly abandoned; use a new operation ID")
		default:
			return RestoreRecoveryAnchor{}, nil, nil, errors.New("backuplifecycle: restore operation state is malformed")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}

	now := time.Now().UTC()
	recovery := RestoreRecoveryAnchor{
		APIVersion: restoreRecoveryAPI,
		OwnerRef:   configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		AuthorizationLineage: configuration.Lineage,
		PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		SnapshotAnchorID:     anchor.ID, SnapshotLineage: anchor.Lineage,
		RepositoryID:          anchor.Repository.RepositoryID,
		SnapshotID:            anchor.Snapshot.SnapshotID,
		SnapshotContentDigest: anchor.Snapshot.ContentDigest,
		OperationID:           input.OperationID,
		StagingPath:           RestoreStagingPath(input.OperationID),
		Mode:                  "verified-staging-only",
		ApprovalMethod:        "explicit-cli-owner-custody",
		ApprovedAt:            now, ExpiresAt: now.Add(24 * time.Hour),
	}
	unsigned, err := canonicalRestoreRecovery(recovery)
	if err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	recovery.ID = digestBytes(unsigned)
	signingBytes, err := canonicalRestoreRecovery(recovery)
	if err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	recovery.Signature, err = localevidence.SignOwnerRestoreRecovery(s.workspaceRoot, signingBytes)
	if err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	if err := VerifyRestoreRecoveryAnchor(s.workspaceRoot, recovery); err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	if err := validateRestoreRecoveryBinding(recovery, input, configuration, anchor); err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	recoveryPath := restoreRecoveryDirectory + "/" + operationKey(input.OperationID) + ".json"
	if err := s.writeJSON(recoveryPath, recovery); err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	if err := s.writeJSON(operationPath, restoreOperation{
		APIVersion: restoreOperationAPI,
		State:      "pending", Input: input, Recovery: recovery,
	}); err != nil {
		return RestoreRecoveryAnchor{}, nil, nil, err
	}
	return recovery, nil, nil, nil
}

func (s *Service) persistStagedRestore(
	operationPath string,
	input restoreOperationInput,
	recovery RestoreRecoveryAnchor,
	receipt RepositoryRestoreReceipt,
) error {
	return s.writeJSON(operationPath, restoreOperation{
		APIVersion: restoreOperationAPI,
		State:      "staged",
		Input:      input,
		Recovery:   recovery,
		Receipt:    &receipt,
	})
}

func (s *Service) completeRestore(
	operationPath string,
	recovery RestoreRecoveryAnchor,
	anchor SnapshotAnchor,
	receipt RepositoryRestoreReceipt,
	verification RestoreVerification,
) (RestoreResult, error) {
	request, err := repositoryRestoreRequestFromRecovery(recovery, anchor)
	if err != nil {
		return RestoreResult{}, err
	}
	result := RestoreResult{
		APIVersion: restoreResultAPI,
		OwnerRef:   recovery.OwnerRef, AuthorityRef: recovery.AuthorityRef,
		AuthorizationLineage: recovery.AuthorizationLineage,
		SnapshotAnchorID:     anchor.ID, SnapshotLineage: anchor.Lineage,
		OperationID:    recovery.OperationID,
		RecoveryAnchor: recovery,
		Request:        request,
		Receipt:        receipt, Verification: verification,
	}
	unsigned, err := canonicalRestoreResult(result)
	if err != nil {
		return RestoreResult{}, err
	}
	result.ID = digestBytes(unsigned)
	signingBytes, err := canonicalRestoreResult(result)
	if err != nil {
		return RestoreResult{}, err
	}
	result.Signature, err = localevidence.SignOwnerRestoreResult(s.workspaceRoot, signingBytes)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := VerifyRestoreResult(s.workspaceRoot, result); err != nil {
		return RestoreResult{}, err
	}
	resultPath := restoreResultDirectory + "/" + strings.TrimPrefix(result.ID, "sha256:") + ".json"
	if stored, err := s.loadStoredRestoreResult(result.ID); err == nil {
		if !reflect.DeepEqual(stored, result) {
			return RestoreResult{}, errors.New("backuplifecycle: content-addressed restore result differs from completion")
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := s.writeJSON(resultPath, result); err != nil {
			return RestoreResult{}, err
		}
	} else {
		return RestoreResult{}, err
	}
	if err := s.writeJSON(operationPath, restoreOperation{
		APIVersion: restoreOperationAPI,
		State:      "completed",
		Input: restoreOperationInput{
			OwnerRef: recovery.OwnerRef, AuthorityRef: recovery.AuthorityRef,
			AuthorizationLineage: recovery.AuthorizationLineage,
			PolicyArtifactDigest: recovery.PolicyArtifactDigest,
			SnapshotAnchorID:     recovery.SnapshotAnchorID,
			OperationID:          recovery.OperationID,
		},
		Recovery: recovery, Receipt: &receipt, Result: &result,
	}); err != nil {
		return RestoreResult{}, err
	}
	return result, nil
}

func (s *Service) completeSnapshot(
	operationPath string,
	input operationInput,
	repository RepositoryReceipt,
	snapshot RepositorySnapshotReceipt,
	requireRestoredQuiescence bool,
) (SnapshotAnchor, error) {
	operation, err := s.loadOperation(operationPath)
	if err != nil {
		return SnapshotAnchor{}, fmt.Errorf("backuplifecycle: authenticate snapshot operation before completion: %w", err)
	}
	if operation.State != "pending" || operation.Anchor != nil || !operationInputsEqual(operation.Input, input) {
		return SnapshotAnchor{}, errors.New("backuplifecycle: snapshot operation changed before completion")
	}
	var quiescence *SnapshotQuiescence
	if operation.Quiescence != nil {
		if err := validateRestoredSnapshotQuiescence(*operation.Quiescence); err != nil {
			return SnapshotAnchor{}, err
		}
		cloned := cloneSnapshotQuiescence(*operation.Quiescence)
		quiescence = &cloned
	} else if requireRestoredQuiescence {
		return SnapshotAnchor{}, errors.New("backuplifecycle: quiescence-capable snapshot lacks authenticated restored evidence")
	}
	anchor := SnapshotAnchor{
		APIVersion: snapshotAnchorAPIVersion,
		OwnerRef:   input.OwnerRef, AuthorityRef: input.AuthorityRef,
		Lineage: input.Lineage, PolicyArtifactDigest: input.PolicyArtifactDigest,
		OperationID: input.OperationID, Policy: clonePolicy(input.Policy), ProtectRecovery: input.ProtectRecovery,
		Quiescence: quiescence,
		Repository: repository, Snapshot: snapshot,
	}
	unsigned, err := canonicalAnchor(anchor)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	anchor.ID = digestBytes(unsigned)
	signingBytes, err := canonicalAnchor(anchor)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	anchor.Signature, err = localevidence.SignOwnerSnapshotAnchor(s.workspaceRoot, signingBytes)
	if err != nil {
		return SnapshotAnchor{}, err
	}
	if err := VerifySnapshotAnchor(s.workspaceRoot, anchor); err != nil {
		return SnapshotAnchor{}, err
	}
	anchorPath := anchorDirectory + "/" + strings.TrimPrefix(anchor.ID, "sha256:") + ".json"
	if stored, loadErr := s.loadStoredSnapshotAnchor(anchor.ID); loadErr == nil {
		if !reflect.DeepEqual(stored, anchor) {
			return SnapshotAnchor{}, errors.New("backuplifecycle: content-addressed snapshot anchor differs from completed snapshot")
		}
	} else if errors.Is(loadErr, os.ErrNotExist) {
		if err := s.writeJSON(anchorPath, anchor); err != nil {
			return SnapshotAnchor{}, err
		}
		stored, err := s.loadStoredSnapshotAnchor(anchor.ID)
		if err != nil {
			return SnapshotAnchor{}, err
		}
		if !reflect.DeepEqual(stored, anchor) {
			return SnapshotAnchor{}, errors.New("backuplifecycle: persisted snapshot anchor differs from completed snapshot")
		}
	} else {
		return SnapshotAnchor{}, loadErr
	}
	if err := s.writeSnapshotOperation(operationPath, snapshotOperation{
		APIVersion: snapshotOperationAPI, State: "completed",
		Input: cloneOperationInput(input), Quiescence: quiescence, Anchor: &anchor,
	}); err != nil {
		return SnapshotAnchor{}, err
	}
	return anchor, nil
}

func (s *Service) loadStoredSnapshotAnchor(anchorID string) (SnapshotAnchor, error) {
	if !validDigest(anchorID) {
		return SnapshotAnchor{}, errors.New("backuplifecycle: snapshot anchor identity is invalid")
	}
	anchorPath := anchorDirectory + "/" + strings.TrimPrefix(anchorID, "sha256:") + ".json"
	var anchor SnapshotAnchor
	if err := s.readJSON(anchorPath, &anchor); err != nil {
		return SnapshotAnchor{}, err
	}
	if anchor.ID != anchorID {
		return SnapshotAnchor{}, errors.New("backuplifecycle: stored snapshot anchor identity differs from its content address")
	}
	if err := VerifySnapshotAnchor(s.workspaceRoot, anchor); err != nil {
		return SnapshotAnchor{}, err
	}
	return anchor, nil
}

func VerifySnapshotAnchor(workspaceRoot string, anchor SnapshotAnchor) error {
	if anchor.APIVersion != snapshotAnchorAPIVersion ||
		anchor.ID == "" || anchor.OwnerRef == "" || anchor.AuthorityRef == "" ||
		anchor.OperationID == "" || !validDigest(anchor.PolicyArtifactDigest) {
		return errors.New("backuplifecycle: snapshot anchor is incomplete")
	}
	if err := validateLineage(anchor.Lineage); err != nil {
		return err
	}
	if err := validatePolicy(anchor.Policy); err != nil {
		return err
	}
	if anchor.Quiescence != nil {
		if err := validateRestoredSnapshotQuiescence(*anchor.Quiescence); err != nil {
			return err
		}
	}
	configuration := RepositoryConfiguration{
		OwnerRef: anchor.OwnerRef, AuthorityRef: anchor.AuthorityRef,
		Lineage: anchor.Lineage, PolicyArtifactDigest: anchor.PolicyArtifactDigest,
		Policy: clonePolicy(anchor.Policy),
	}
	configuration.OperationID = configurationOperationID(configuration)
	if err := validateRepositoryReceipt(anchor.Repository, configuration); err != nil {
		return err
	}
	request := RepositorySnapshotRequest{
		OwnerRef: anchor.OwnerRef, AuthorityRef: anchor.AuthorityRef,
		Lineage: anchor.Lineage, PolicyArtifactDigest: anchor.PolicyArtifactDigest,
		RepositoryID: anchor.Repository.RepositoryID, OperationID: anchor.OperationID, ProtectRecovery: anchor.ProtectRecovery,
		Source:      anchor.Policy.Source.ContainerPath,
		Excludes:    append([]string(nil), anchor.Policy.Source.ExcludePaths...),
		Consistency: ConsistencyCrashConsistent,
	}
	if err := validateSnapshotReceipt(anchor.Snapshot, request); err != nil {
		return err
	}
	if anchor.Quiescence != nil && anchor.Quiescence.CaptureStartedAt != nil &&
		anchor.Quiescence.CaptureStartedAt.After(anchor.Snapshot.CreatedAt) {
		return errors.New("backuplifecycle: snapshot capture starts after its completion")
	}
	unsigned := anchor
	unsigned.ID = ""
	unsigned.Signature = localevidence.OwnerSnapshotAnchorSignature{}
	canonicalUnsigned, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode snapshot anchor identity: %w", err)
	}
	if anchor.ID != digestBytes(canonicalUnsigned) {
		return errors.New("backuplifecycle: snapshot anchor content identity does not verify")
	}
	signingBytes, err := canonicalAnchor(anchor)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerSnapshotAnchor(workspaceRoot, signingBytes, anchor.Signature)
}

// RestoreStagingPath returns the fixed isolated target for one restore
// operation. Callers cannot supply an alternate host or container path.
func RestoreStagingPath(operationID string) string {
	return localbackuppolicy.RestorePathForOperation(operationID)
}

func VerifyRestoreRecoveryAnchor(workspaceRoot string, recovery RestoreRecoveryAnchor) error {
	if recovery.APIVersion != restoreRecoveryAPI ||
		!validDigest(recovery.ID) ||
		recovery.OwnerRef == "" || recovery.AuthorityRef == "" ||
		!validDigest(recovery.PolicyArtifactDigest) ||
		!validDigest(recovery.SnapshotAnchorID) ||
		recovery.RepositoryID == "" || !validPortableValue(recovery.SnapshotID) ||
		!validDigest(recovery.SnapshotContentDigest) ||
		!validOperationID(recovery.OperationID) ||
		recovery.StagingPath != RestoreStagingPath(recovery.OperationID) ||
		recovery.Mode != "verified-staging-only" ||
		recovery.ApprovalMethod != "explicit-cli-owner-custody" ||
		recovery.ApprovedAt.IsZero() || recovery.ExpiresAt.IsZero() ||
		!recovery.ExpiresAt.After(recovery.ApprovedAt) ||
		recovery.ExpiresAt.Sub(recovery.ApprovedAt) > 24*time.Hour {
		return errors.New("backuplifecycle: restore recovery anchor is incomplete or widens staging authority")
	}
	if err := validateLineage(recovery.AuthorizationLineage); err != nil {
		return err
	}
	if err := validateLineage(recovery.SnapshotLineage); err != nil {
		return err
	}
	unsigned := recovery
	unsigned.ID = ""
	unsigned.Signature = localevidence.OwnerRestoreRecoverySignature{}
	canonicalUnsigned, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode restore recovery identity: %w", err)
	}
	if recovery.ID != digestBytes(canonicalUnsigned) {
		return errors.New("backuplifecycle: restore recovery anchor content identity does not verify")
	}
	signingBytes, err := canonicalRestoreRecovery(recovery)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerRestoreRecovery(workspaceRoot, signingBytes, recovery.Signature)
}

func VerifyRestoreResult(workspaceRoot string, result RestoreResult) error {
	if result.APIVersion != restoreResultAPI ||
		!validDigest(result.ID) ||
		result.OwnerRef == "" || result.AuthorityRef == "" ||
		!validDigest(result.SnapshotAnchorID) ||
		!validOperationID(result.OperationID) {
		return errors.New("backuplifecycle: restore result is incomplete")
	}
	if err := VerifyRestoreRecoveryAnchor(workspaceRoot, result.RecoveryAnchor); err != nil {
		return err
	}
	if result.OwnerRef != result.RecoveryAnchor.OwnerRef ||
		result.AuthorityRef != result.RecoveryAnchor.AuthorityRef ||
		!lineagesEqual(result.AuthorizationLineage, result.RecoveryAnchor.AuthorizationLineage) ||
		result.SnapshotAnchorID != result.RecoveryAnchor.SnapshotAnchorID ||
		!lineagesEqual(result.SnapshotLineage, result.RecoveryAnchor.SnapshotLineage) ||
		result.OperationID != result.RecoveryAnchor.OperationID {
		return errors.New("backuplifecycle: restore result differs from its Owner-approved recovery anchor")
	}
	if result.Request.OwnerRef != result.OwnerRef ||
		result.Request.AuthorityRef != result.AuthorityRef ||
		!lineagesEqual(result.Request.AuthorizationLineage, result.AuthorizationLineage) ||
		result.Request.PolicyArtifactDigest != result.RecoveryAnchor.PolicyArtifactDigest ||
		!validDigest(result.Request.SnapshotSourceDigest) ||
		result.Request.RepositoryID != result.RecoveryAnchor.RepositoryID ||
		result.Request.SnapshotAnchorID != result.SnapshotAnchorID ||
		result.Request.SnapshotReceipt.SnapshotID != result.RecoveryAnchor.SnapshotID ||
		result.Request.SnapshotReceipt.ContentDigest != result.RecoveryAnchor.SnapshotContentDigest ||
		result.Request.OperationID != result.OperationID ||
		result.Request.StagingPath != result.RecoveryAnchor.StagingPath {
		return errors.New("backuplifecycle: restore request differs from its signed recovery anchor")
	}
	if err := validateSnapshotReceipt(result.Request.SnapshotReceipt, result.Request.SnapshotRequest); err != nil {
		return err
	}
	if err := validateRestoreReceipt(result.Receipt, result.Request); err != nil {
		return err
	}
	if result.Verification.APIVersion != restoreVerificationAPI ||
		result.Verification.OwnerRef != result.OwnerRef ||
		result.Verification.OwnerBindingDigest != result.AuthorizationLineage.OwnerBindingDigest ||
		result.Verification.PocketIDSubject != result.AuthorizationLineage.PocketIDSubject ||
		result.Verification.PlanHash != result.AuthorizationLineage.Binding.PlanHash ||
		!result.Verification.ServicesVerified ||
		!restoreApprovalCurrent(result.RecoveryAnchor, result.Receipt.CompletedAt) ||
		!restoreApprovalCurrent(result.RecoveryAnchor, result.Verification.VerifiedAt) ||
		result.Verification.VerifiedAt.Before(result.Receipt.CompletedAt) {
		return errors.New("backuplifecycle: restore result lacks exact current local platform verification")
	}
	unsigned := result
	unsigned.ID = ""
	unsigned.Signature = localevidence.OwnerRestoreResultSignature{}
	canonicalUnsigned, err := json.Marshal(unsigned)
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode restore result identity: %w", err)
	}
	if result.ID != digestBytes(canonicalUnsigned) {
		return errors.New("backuplifecycle: restore result content identity does not verify")
	}
	signingBytes, err := canonicalRestoreResult(result)
	if err != nil {
		return err
	}
	return localevidence.VerifyOwnerRestoreResult(workspaceRoot, signingBytes, result.Signature)
}

// LoadRestoreResult loads one content-addressed staged restore result and
// verifies its complete owner-signed recovery chain before returning it.
// Callers cannot select a path: resultID alone derives the confined location.
func LoadRestoreResult(workspaceRoot, resultID string) (RestoreResult, error) {
	if !validDigest(resultID) {
		return RestoreResult{}, errors.New("backuplifecycle: restore result identity is invalid")
	}
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return RestoreResult{}, err
	}
	resultPath := restoreResultDirectory + "/" + strings.TrimPrefix(resultID, "sha256:") + ".json"
	file, err := view.Open(resultPath)
	if err != nil {
		return RestoreResult{}, err
	}
	defer func() { _ = file.Close() }()
	var result RestoreResult
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return RestoreResult{}, fmt.Errorf("backuplifecycle: decode %s: %w", resultPath, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return RestoreResult{}, fmt.Errorf("backuplifecycle: decode %s: trailing content: %w", resultPath, err)
	}
	if result.ID != resultID {
		return RestoreResult{}, errors.New("backuplifecycle: stored restore result identity differs from its content address")
	}
	if err := VerifyRestoreResult(root.Name(), result); err != nil {
		return RestoreResult{}, err
	}
	return result, nil
}

func (s *Service) ready(ctx context.Context) error {
	if ctx == nil {
		return errors.New("backuplifecycle: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.workspaceRoot == "" || s.runtime == nil {
		return errors.New("backuplifecycle: service is not initialized")
	}
	return nil
}

func (s *Service) validateBinding(ownerRef, authorityRef string) (localevidence.OwnerCustody, error) {
	owner, err := localevidence.LoadOwnerCustody(s.workspaceRoot)
	if err != nil {
		return localevidence.OwnerCustody{}, err
	}
	if ownerRef != owner.OwnerRef {
		return localevidence.OwnerCustody{}, errors.New("backuplifecycle: owner reference differs from local custody")
	}
	if authorityRef == "" || authorityRef != owner.Trust.HumanAuthorityRef {
		return localevidence.OwnerCustody{}, errors.New("backuplifecycle: authority reference differs from local owner trust")
	}
	return owner, nil
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
	if !configurationMatchesBinding(configuration, binding) {
		return Configuration{}, errors.New("backuplifecycle: backup configuration differs from current authority lineage or policy artifact")
	}
	return configuration, nil
}

func configurationMatchesBinding(configuration Configuration, binding normalizedBinding) bool {
	return configuration.OwnerRef == binding.OwnerRef &&
		configuration.AuthorityRef == binding.AuthorityRef &&
		lineagesEqual(configuration.Lineage, binding.Lineage) &&
		configuration.PolicyArtifactDigest == binding.PolicyArtifactDigest &&
		policiesEqual(configuration.Policy, binding.Policy)
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

func (s *Service) loadOperation(relative string) (snapshotOperation, error) {
	var operation snapshotOperation
	if err := s.readJSON(relative, &operation); err != nil {
		return snapshotOperation{}, err
	}
	if operation.APIVersion != snapshotOperationAPI {
		return snapshotOperation{}, errors.New("backuplifecycle: snapshot operation has an unsupported API version")
	}
	if operation.Quiescence != nil {
		if err := validateSnapshotQuiescence(*operation.Quiescence); err != nil {
			return snapshotOperation{}, err
		}
	}
	if err := verifySnapshotOperation(s.workspaceRoot, operation); err != nil {
		return snapshotOperation{}, err
	}
	return operation, nil
}

func (s *Service) loadRestoreOperation(relative string) (restoreOperation, error) {
	var operation restoreOperation
	if err := s.readJSON(relative, &operation); err != nil {
		return restoreOperation{}, err
	}
	if operation.APIVersion != restoreOperationAPI {
		return restoreOperation{}, errors.New("backuplifecycle: restore operation has an unsupported API version")
	}
	return operation, nil
}

func (s *Service) loadStoredRestoreResult(resultID string) (RestoreResult, error) {
	return LoadRestoreResult(s.workspaceRoot, resultID)
}

func (s *Service) readJSON(relative string, target any) error {
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	view, err := root.View(".")
	if err != nil {
		return err
	}
	file, err := view.Open(relative)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return err
	}
	defer func() { _ = file.Close() }()
	absolute := filepath.Join(root.Name(), filepath.FromSlash(relative))
	if err := backupcustody.RequirePrivatePath(filepath.Dir(absolute), true); err != nil {
		return fmt.Errorf("backuplifecycle: private custody check for %s: %w", filepath.Dir(relative), err)
	}
	if err := backupcustody.RequirePrivatePath(absolute, false); err != nil {
		return fmt.Errorf("backuplifecycle: private custody check for %s: %w", relative, err)
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("backuplifecycle: decode %s: %w", relative, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("backuplifecycle: decode %s: trailing content: %w", relative, err)
	}
	return nil
}

func (s *Service) writeJSON(relative string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("backuplifecycle: encode %s: %w", relative, err)
	}
	root, err := confinedfs.Open(s.workspaceRoot)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	parent := filepath.ToSlash(filepath.Dir(relative))
	if err := transaction.MkdirAll(parent, 0o700); err != nil {
		_ = transaction.Close()
		return err
	}
	if err := transaction.Close(); err != nil {
		return err
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	result, err := view.WriteAtomic0600(relative, encoded)
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("backuplifecycle: lifecycle record was not atomically installed and synced")
	}
	absoluteParent := filepath.Join(root.Name(), filepath.FromSlash(parent))
	if err := backupcustody.ProtectPrivatePath(absoluteParent, true); err != nil {
		return fmt.Errorf("backuplifecycle: protect %s: %w", parent, err)
	}
	if err := backupcustody.ProtectPrivatePath(filepath.Join(root.Name(), filepath.FromSlash(relative)), false); err != nil {
		return fmt.Errorf("backuplifecycle: protect %s: %w", relative, err)
	}
	return nil
}

func (s *Service) writeSnapshotOperation(relative string, operation snapshotOperation) error {
	signed, err := signSnapshotOperation(s.workspaceRoot, operation)
	if err != nil {
		return err
	}
	return s.writeJSON(relative, signed)
}

func (s *Service) persistSnapshotQuiescence(
	operationPath string,
	input operationInput,
	quiescence SnapshotQuiescence,
) error {
	if err := validateSnapshotQuiescence(quiescence); err != nil {
		return err
	}
	cloned := cloneSnapshotQuiescence(quiescence)
	return s.writeSnapshotOperation(operationPath, snapshotOperation{
		APIVersion: snapshotOperationAPI,
		State:      "pending",
		Input:      cloneOperationInput(input),
		Quiescence: &cloned,
	})
}

func signSnapshotOperation(workspaceRoot string, operation snapshotOperation) (snapshotOperation, error) {
	operation.Signature = nil
	canonical, err := canonicalSnapshotOperation(operation)
	if err != nil {
		return snapshotOperation{}, err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(workspaceRoot, canonical)
	if err != nil {
		return snapshotOperation{}, fmt.Errorf("backuplifecycle: sign snapshot operation: %w", err)
	}
	operation.Signature = &signature
	return operation, nil
}

func verifySnapshotOperation(workspaceRoot string, operation snapshotOperation) error {
	if operation.Signature == nil {
		if operation.Quiescence != nil {
			return errors.New("backuplifecycle: snapshot operation with quiescence is not owner-authenticated")
		}
		return nil
	}
	if operation.Signature.OwnerRef == "" || operation.Signature.OwnerRef != operation.Input.OwnerRef {
		return errors.New("backuplifecycle: snapshot operation Owner signature differs from its input owner")
	}
	canonical, err := canonicalSnapshotOperation(operation)
	if err != nil {
		return err
	}
	if err := localevidence.VerifyOwnerLifecycleMutation(workspaceRoot, canonical, *operation.Signature); err != nil {
		return fmt.Errorf("backuplifecycle: verify snapshot operation Owner signature: %w", err)
	}
	return nil
}

func canonicalSnapshotOperation(operation snapshotOperation) ([]byte, error) {
	operation.Signature = nil
	encoded, err := resolvedplan.CanonicalJSON(operation)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical snapshot operation: %w", err)
	}
	return encoded, nil
}

func validateSnapshotQuiescence(quiescence SnapshotQuiescence) error {
	switch quiescence.Phase {
	case "prepared", "stopped", "snapshot-created", "restored":
	default:
		return errors.New("backuplifecycle: snapshot quiescence phase is invalid")
	}
	if quiescence.GraphDigest != "" && !validDigest(quiescence.GraphDigest) {
		return errors.New("backuplifecycle: snapshot quiescence graph digest is invalid")
	}
	if quiescence.CaptureStartedAt != nil && quiescence.CaptureStartedAt.IsZero() {
		return errors.New("backuplifecycle: snapshot capture start is invalid")
	}
	seen := make(map[string]struct{}, len(quiescence.Containers))
	for index, container := range quiescence.Containers {
		if strings.TrimSpace(container.ID) == "" || strings.TrimSpace(container.Name) == "" || !container.WasRunning {
			return fmt.Errorf("backuplifecycle: snapshot quiescence container %d identity or running state is incomplete", index)
		}
		if _, exists := seen[container.ID]; exists {
			return fmt.Errorf("backuplifecycle: snapshot quiescence repeats container %q", container.ID)
		}
		seen[container.ID] = struct{}{}
		if container.ComposeProject != "" {
			if quiescence.GraphDigest == "" || container.WorkloadRef == "" || container.SiteRef == "" ||
				container.NodeRef == "" || container.ComposeService == "" || container.ComponentRef == "" ||
				container.ComposeService != container.ComponentRef || container.Image == "" || container.StopOrder < 0 {
				return fmt.Errorf("backuplifecycle: snapshot quiescence container %q has incomplete runtime graph identity", container.ID)
			}
		} else if container.WorkloadRef != "" || container.SiteRef != "" || container.NodeRef != "" ||
			container.ComposeService != "" || container.ComponentRef != "" || container.Image != "" || container.StopOrder != 0 {
			return fmt.Errorf("backuplifecycle: snapshot quiescence container %q has unbound runtime graph identity", container.ID)
		}
		if len(container.Mounts) == 0 && container.ComposeProject == "" {
			return fmt.Errorf("backuplifecycle: snapshot quiescence container %q has no mount identity", container.ID)
		}
	}
	return nil
}

func validateRestoredSnapshotQuiescence(quiescence SnapshotQuiescence) error {
	if err := validateSnapshotQuiescence(quiescence); err != nil {
		return err
	}
	if quiescence.Phase != "restored" {
		return errors.New("backuplifecycle: snapshot anchor requires restored quiescence evidence")
	}
	return nil
}

func cloneSnapshotQuiescence(quiescence SnapshotQuiescence) SnapshotQuiescence {
	cloned := SnapshotQuiescence{
		Phase:       quiescence.Phase,
		GraphDigest: quiescence.GraphDigest,
		Containers:  make([]SnapshotQuiescedContainer, len(quiescence.Containers)),
	}
	if quiescence.CaptureStartedAt != nil {
		startedAt := *quiescence.CaptureStartedAt
		cloned.CaptureStartedAt = &startedAt
	}
	for index, container := range quiescence.Containers {
		cloned.Containers[index] = SnapshotQuiescedContainer{
			ID: container.ID, Name: container.Name, WasRunning: container.WasRunning,
			WorkloadRef: container.WorkloadRef, SiteRef: container.SiteRef, NodeRef: container.NodeRef,
			ComposeProject: container.ComposeProject, ComposeService: container.ComposeService,
			ComponentRef: container.ComponentRef, Image: container.Image, StopOrder: container.StopOrder,
			Mounts: append([]SnapshotQuiesceMount(nil), container.Mounts...),
		}
	}
	return cloned
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

func validOperationID(value string) bool {
	if len(value) == 0 || len(value) > 128 || !isOperationAlphaNumeric(value[0]) {
		return false
	}
	for index := 1; index < len(value); index++ {
		if !isOperationAlphaNumeric(value[index]) &&
			value[index] != '.' && value[index] != '_' && value[index] != '-' {
			return false
		}
	}
	return true
}

func validPortableValue(value string) bool {
	return validOperationID(value)
}

func isOperationAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func decodePolicyArtifact(artifact []byte) (Policy, error) {
	if len(artifact) == 0 {
		return Policy{}, errors.New("backuplifecycle: local backup policy artifact is required")
	}
	policy, err := localbackuppolicy.Decode(artifact)
	if err != nil {
		return Policy{}, fmt.Errorf("backuplifecycle: decode local backup policy artifact: %w", err)
	}
	return policy, nil
}

func validatePolicy(policy Policy) error {
	if err := localbackuppolicy.ValidateSnapshotPolicy(policy); err != nil {
		return fmt.Errorf("backuplifecycle: local backup policy differs from the governed contract: %w", err)
	}
	return nil
}

func validateLineage(lineage AuthorityLineage) error {
	binding := lineage.Binding
	if !validDigest(binding.PlanHash) || !validDigest(binding.SpecHash) ||
		!validDigest(binding.InventoryHash) || !validDigest(binding.DefinitionHash) ||
		binding.CompilerVersion == "" || binding.Renderer.ID == "" || binding.Renderer.Version == "" ||
		binding.Authority.Class == "" || binding.Authority.Document == "" ||
		binding.Authority.Issuer == "" || !validDigest(binding.Authority.AuthorityFingerprint) ||
		!validDigest(binding.Authority.CatalogHash) ||
		!validDigest(lineage.ManifestHash) || !validDigest(lineage.GenerationReceiptHash) ||
		!validDigest(lineage.ApplyResultHash) || !validDigest(lineage.ApplyReceiptHash) ||
		!validDigest(lineage.OwnerBindingDigest) || strings.TrimSpace(lineage.PocketIDSubject) == "" {
		return errors.New("backuplifecycle: complete plan/generation/apply authority lineage is required")
	}
	return nil
}

func validateRepositoryReceipt(receipt RepositoryReceipt, configuration RepositoryConfiguration) error {
	if receipt.APIVersion != repositoryReceiptAPI ||
		receipt.RepositoryID == "" || receipt.Backend == "" ||
		receipt.ConfigurationDigest != RepositoryConfigurationDigest(configuration) {
		return errors.New("backuplifecycle: repository receipt is incomplete or differs from the exact configuration request")
	}
	return nil
}

func validateSnapshotReceipt(receipt RepositorySnapshotReceipt, request RepositorySnapshotRequest) error {
	if receipt.APIVersion != repositorySnapshotAPI ||
		receipt.RepositoryID != request.RepositoryID ||
		receipt.OperationID != request.OperationID ||
		receipt.RequestDigest != RepositorySnapshotRequestDigest(request) ||
		receipt.SnapshotID == "" || !validDigest(receipt.ContentDigest) ||
		receipt.Consistency != ConsistencyCrashConsistent || receipt.CreatedAt.IsZero() {
		return errors.New("backuplifecycle: repository snapshot receipt is incomplete or differs from the exact request")
	}
	return nil
}

func validateRestoreSource(configuration Configuration, anchor SnapshotAnchor) error {
	if anchor.OwnerRef != configuration.OwnerRef ||
		anchor.Repository.RepositoryID != configuration.Repository.RepositoryID ||
		anchor.Policy.StackID != configuration.Policy.StackID ||
		!restoreTargetsCompatible(anchor.Policy.Target, configuration.Policy.Target) ||
		!restoreDataTopologyCompatible(anchor.Policy.Source, configuration.Policy.Source) {
		return errors.New("backuplifecycle: snapshot anchor is not compatible with the current Owner, Stack target, repository, or governed data topology")
	}
	return nil
}

func restoreDataTopologyCompatible(historical, current localbackuppolicy.Source) bool {
	historicalDigest, err := localbackuppolicy.SourceDigest(historical)
	if err != nil {
		return false
	}
	currentDigest, err := localbackuppolicy.SourceDigest(current)
	return err == nil && historicalDigest == currentDigest
}

func restoreTargetsCompatible(historical, current localbackuppolicy.Target) bool {
	if historical.SiteRef != current.SiteRef || historical.NodeRef != current.NodeRef {
		return false
	}
	legacy := historical.DaemonRef == "" && historical.DaemonEngine == "" &&
		historical.DaemonSocketPath == "" && historical.HostScope == ""
	return legacy || historical == current
}

func validateRestoreRecoveryBinding(
	recovery RestoreRecoveryAnchor,
	input restoreOperationInput,
	configuration Configuration,
	anchor SnapshotAnchor,
) error {
	if recovery.OwnerRef != input.OwnerRef ||
		recovery.AuthorityRef != input.AuthorityRef ||
		!lineagesEqual(recovery.AuthorizationLineage, input.AuthorizationLineage) ||
		recovery.PolicyArtifactDigest != input.PolicyArtifactDigest ||
		recovery.SnapshotAnchorID != input.SnapshotAnchorID ||
		recovery.OperationID != input.OperationID ||
		recovery.OwnerRef != configuration.OwnerRef ||
		recovery.AuthorityRef != configuration.AuthorityRef ||
		!lineagesEqual(recovery.AuthorizationLineage, configuration.Lineage) ||
		recovery.PolicyArtifactDigest != configuration.PolicyArtifactDigest ||
		recovery.SnapshotAnchorID != anchor.ID ||
		!lineagesEqual(recovery.SnapshotLineage, anchor.Lineage) ||
		recovery.RepositoryID != anchor.Repository.RepositoryID ||
		recovery.SnapshotID != anchor.Snapshot.SnapshotID ||
		recovery.SnapshotContentDigest != anchor.Snapshot.ContentDigest ||
		recovery.StagingPath != RestoreStagingPath(input.OperationID) {
		return errors.New("backuplifecycle: restore recovery anchor differs from its journal, current authorization, or selected snapshot anchor")
	}
	return nil
}

func repositoryRestoreRequest(
	configuration Configuration,
	anchor SnapshotAnchor,
	operationID string,
) (RepositoryRestoreRequest, error) {
	sourceDigest, err := localbackuppolicy.SourceDigest(anchor.Policy.Source)
	if err != nil {
		return RepositoryRestoreRequest{}, fmt.Errorf("backuplifecycle: digest snapshot source policy: %w", err)
	}
	return RepositoryRestoreRequest{
		OwnerRef:             configuration.OwnerRef,
		AuthorityRef:         configuration.AuthorityRef,
		AuthorizationLineage: configuration.Lineage,
		PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		RepositoryID:         configuration.Repository.RepositoryID,
		SnapshotAnchorID:     anchor.ID,
		SnapshotSourceDigest: sourceDigest,
		SnapshotRequest:      snapshotRequestFromAnchor(anchor),
		SnapshotReceipt:      anchor.Snapshot,
		OperationID:          operationID,
		StagingPath:          RestoreStagingPath(operationID),
	}, nil
}

func repositoryRestoreRequestFromRecovery(
	recovery RestoreRecoveryAnchor,
	anchor SnapshotAnchor,
) (RepositoryRestoreRequest, error) {
	sourceDigest, err := localbackuppolicy.SourceDigest(anchor.Policy.Source)
	if err != nil {
		return RepositoryRestoreRequest{}, fmt.Errorf("backuplifecycle: digest recovered snapshot source policy: %w", err)
	}
	return RepositoryRestoreRequest{
		OwnerRef:             recovery.OwnerRef,
		AuthorityRef:         recovery.AuthorityRef,
		AuthorizationLineage: recovery.AuthorizationLineage,
		PolicyArtifactDigest: recovery.PolicyArtifactDigest,
		RepositoryID:         recovery.RepositoryID,
		SnapshotAnchorID:     recovery.SnapshotAnchorID,
		SnapshotSourceDigest: sourceDigest,
		SnapshotRequest:      snapshotRequestFromAnchor(anchor),
		SnapshotReceipt:      anchor.Snapshot,
		OperationID:          recovery.OperationID,
		StagingPath:          recovery.StagingPath,
	}, nil
}

func snapshotRequestFromAnchor(anchor SnapshotAnchor) RepositorySnapshotRequest {
	return RepositorySnapshotRequest{
		OwnerRef: anchor.OwnerRef, AuthorityRef: anchor.AuthorityRef,
		Lineage: anchor.Lineage, PolicyArtifactDigest: anchor.PolicyArtifactDigest,
		RepositoryID:    anchor.Repository.RepositoryID,
		OperationID:     anchor.OperationID,
		ProtectRecovery: anchor.ProtectRecovery,
		Source:          anchor.Policy.Source.ContainerPath,
		Excludes:        append([]string(nil), anchor.Policy.Source.ExcludePaths...),
		Consistency:     ConsistencyCrashConsistent,
	}
}

func validateRestoreReceipt(receipt RepositoryRestoreReceipt, request RepositoryRestoreRequest) error {
	if receipt.APIVersion != repositoryRestoreAPI ||
		receipt.RepositoryID != request.RepositoryID ||
		receipt.SnapshotID != request.SnapshotReceipt.SnapshotID ||
		receipt.OperationID != request.OperationID ||
		receipt.RequestDigest != RepositoryRestoreRequestDigest(request) ||
		receipt.StagingPath != request.StagingPath ||
		receipt.SnapshotContentDigest != request.SnapshotReceipt.ContentDigest ||
		!receipt.RepositoryContentVerified ||
		receipt.CompletedAt.IsZero() {
		return errors.New("backuplifecycle: repository restore receipt is incomplete or differs from the exact staged restore request")
	}
	return nil
}

func validateRestoreVerification(
	verification RestoreVerification,
	configuration Configuration,
	receipt RepositoryRestoreReceipt,
) error {
	if verification.APIVersion != restoreVerificationAPI ||
		verification.OwnerRef != configuration.OwnerRef ||
		verification.OwnerBindingDigest != configuration.Lineage.OwnerBindingDigest ||
		verification.PocketIDSubject != configuration.Lineage.PocketIDSubject ||
		verification.PlanHash != configuration.Lineage.Binding.PlanHash ||
		!verification.ServicesVerified ||
		verification.VerifiedAt.IsZero() ||
		verification.VerifiedAt.Before(receipt.CompletedAt) {
		return errors.New("backuplifecycle: staged restore post-verification differs from the current Owner, Plan, or service closure")
	}
	return nil
}

// RepositoryConfigurationDigest is the canonical digest a runtime must echo
// in its configuration receipt.
func RepositoryConfigurationDigest(configuration RepositoryConfiguration) string {
	return digestJSON(configuration)
}

// RepositorySnapshotRequestDigest is the canonical digest a runtime must echo
// in both lookup and create receipts.
func RepositorySnapshotRequestDigest(request RepositorySnapshotRequest) string {
	return digestJSON(request)
}

// RepositoryRestoreRequestDigest is the canonical digest a runtime must echo
// after full verification and staging of the exact selected snapshot anchor.
func RepositoryRestoreRequestDigest(request RepositoryRestoreRequest) string {
	return digestJSON(request)
}

func validDigest(value string) bool {
	raw := strings.TrimPrefix(value, "sha256:")
	if len(raw) != sha256.Size*2 || raw == value {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil
}

func clonePolicy(policy Policy) Policy {
	policy.Source = policy.SourceProjection()
	policy.Runtime = policy.RuntimeProjection()
	if policy.Schedule != nil {
		schedule := *policy.Schedule
		if schedule.HourUTC != nil {
			hour := *schedule.HourUTC
			schedule.HourUTC = &hour
		}
		policy.Schedule = &schedule
	}
	if policy.Retention != nil {
		retention := *policy.Retention
		policy.Retention = &retention
	}
	policy.RecoveryObjectives = append([]localbackuppolicy.RecoveryObjective(nil), policy.RecoveryObjectives...)
	for index := range policy.RecoveryObjectives {
		policy.RecoveryObjectives[index].WorkloadRefs = append([]string(nil), policy.RecoveryObjectives[index].WorkloadRefs...)
	}
	return policy
}

func policiesEqual(left, right Policy) bool {
	return reflect.DeepEqual(left, right)
}

func lineagesEqual(left, right AuthorityLineage) bool {
	return digestJSON(left) == digestJSON(right)
}

func repositoryConfigurationsEqual(left, right RepositoryConfiguration) bool {
	return left.OwnerRef == right.OwnerRef &&
		left.AuthorityRef == right.AuthorityRef &&
		lineagesEqual(left.Lineage, right.Lineage) &&
		left.PolicyArtifactDigest == right.PolicyArtifactDigest &&
		left.OperationID == right.OperationID &&
		policiesEqual(left.Policy, right.Policy)
}

func operationInputsEqual(left, right operationInput) bool {
	return left.OwnerRef == right.OwnerRef &&
		left.AuthorityRef == right.AuthorityRef &&
		lineagesEqual(left.Lineage, right.Lineage) &&
		left.PolicyArtifactDigest == right.PolicyArtifactDigest &&
		left.OperationID == right.OperationID && left.ProtectRecovery == right.ProtectRecovery &&
		policiesEqual(left.Policy, right.Policy)
}

func cloneOperationInput(input operationInput) operationInput {
	input.Policy = clonePolicy(input.Policy)
	return input
}

func anchorMatchesOperation(anchor SnapshotAnchor, operation snapshotOperation) bool {
	input := operation.Input
	return anchor.OwnerRef == input.OwnerRef &&
		anchor.AuthorityRef == input.AuthorityRef &&
		lineagesEqual(anchor.Lineage, input.Lineage) &&
		anchor.PolicyArtifactDigest == input.PolicyArtifactDigest &&
		anchor.OperationID == input.OperationID && anchor.ProtectRecovery == input.ProtectRecovery &&
		policiesEqual(anchor.Policy, input.Policy) &&
		snapshotQuiescencePointersEqual(anchor.Quiescence, operation.Quiescence)
}

func snapshotQuiescencePointersEqual(left, right *SnapshotQuiescence) bool {
	if left == nil || right == nil {
		return left == right
	}
	return reflect.DeepEqual(*left, *right)
}

func configurationOperationID(configuration RepositoryConfiguration) string {
	configuration.OperationID = ""
	return digestJSON(configuration)
}

func snapshotRequest(configuration Configuration, operationID string) RepositorySnapshotRequest {
	return RepositorySnapshotRequest{
		OwnerRef: configuration.OwnerRef, AuthorityRef: configuration.AuthorityRef,
		Lineage: configuration.Lineage, PolicyArtifactDigest: configuration.PolicyArtifactDigest,
		RepositoryID: configuration.Repository.RepositoryID, OperationID: operationID,
		Source:      configuration.Policy.Source.ContainerPath,
		Excludes:    append([]string(nil), configuration.Policy.Source.ExcludePaths...),
		Consistency: ConsistencyCrashConsistent,
	}
}

func canonicalAnchor(anchor SnapshotAnchor) ([]byte, error) {
	anchor.Signature = localevidence.OwnerSnapshotAnchorSignature{}
	encoded, err := json.Marshal(anchor)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical snapshot anchor: %w", err)
	}
	return encoded, nil
}

func canonicalRestoreRecovery(recovery RestoreRecoveryAnchor) ([]byte, error) {
	recovery.Signature = localevidence.OwnerRestoreRecoverySignature{}
	encoded, err := json.Marshal(recovery)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical restore recovery anchor: %w", err)
	}
	return encoded, nil
}

func canonicalRestoreResult(result RestoreResult) ([]byte, error) {
	result.Signature = localevidence.OwnerRestoreResultSignature{}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("backuplifecycle: encode canonical restore result: %w", err)
	}
	return encoded, nil
}

func digestJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("backuplifecycle: canonical JSON digest: %v", err))
	}
	return digestBytes(encoded)
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func operationKey(operationID string) string {
	sum := sha256.Sum256([]byte(operationID))
	return hex.EncodeToString(sum[:])
}
