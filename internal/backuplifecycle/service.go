// Package backuplifecycle owns the local, owner-authorized backup lifecycle
// journal while delegating repository mechanics to a narrow runtime boundary.
package backuplifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
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
