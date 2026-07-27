package commands

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/localbackupruntime"
)

const nativeV2BackupMaximumOperationTimeout = 15 * time.Minute

type nativeV2BackupService interface {
	Configure(context.Context, backuplifecycle.ConfigureInput) (backuplifecycle.Configuration, error)
	Status(context.Context, backuplifecycle.StatusInput) (backuplifecycle.RepositoryStatus, error)
	Run(context.Context, backuplifecycle.RunInput) (backuplifecycle.SnapshotAnchor, error)
	Restore(context.Context, backuplifecycle.RestoreInput) (backuplifecycle.RestoreResult, error)
}

var newNativeV2BackupService = func(workspaceRoot string) (nativeV2BackupService, error) {
	runtime, err := localbackupruntime.New(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("initialize native v2 backup runtime: %w", err)
	}
	service, err := backuplifecycle.NewCreator().Create(workspaceRoot, runtime)
	if err != nil {
		return nil, fmt.Errorf("initialize native v2 backup lifecycle: %w", err)
	}
	return service, nil
}

func continueNativeV2BackupProduction(
	ctx context.Context,
	operation nativeV2BackupOperation,
	authority nativeV2BackupAuthority,
	request nativeV2BackupRequest,
) (any, error) {
	service, err := newNativeV2BackupService(authority.WorkspaceRoot)
	if err != nil {
		return nil, err
	}

	switch operation {
	case nativeV2BackupConfigure:
		operationContext, cancel := nativeV2BackupOperationContext(ctx, backupLongOperationTimeout)
		defer cancel()
		return service.Configure(operationContext, backuplifecycle.ConfigureInput{
			OwnerRef:       authority.OwnerRef,
			AuthorityRef:   authority.AuthorityRef,
			Lineage:        authority.Lineage,
			PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
		})
	case nativeV2BackupStatus:
		operationContext, cancel := nativeV2BackupOperationContext(ctx, backupQuickOperationTimeout)
		defer cancel()
		return service.Status(operationContext, backuplifecycle.StatusInput{
			OwnerRef:       authority.OwnerRef,
			AuthorityRef:   authority.AuthorityRef,
			Lineage:        authority.Lineage,
			PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
		})
	case nativeV2BackupRun:
		operationContext, cancel := nativeV2BackupOperationContext(ctx, backupLongOperationTimeout)
		defer cancel()
		return service.Run(operationContext, backuplifecycle.RunInput{
			OwnerRef:       authority.OwnerRef,
			AuthorityRef:   authority.AuthorityRef,
			Lineage:        authority.Lineage,
			PolicyArtifact: append([]byte(nil), authority.PolicyArtifact...),
			OperationID:    request.OperationID,
		})
	case nativeV2BackupRestore:
		operationContext, cancel := nativeV2BackupOperationContext(ctx, backupLongOperationTimeout)
		defer cancel()
		return service.Restore(operationContext, backuplifecycle.RestoreInput{
			OwnerRef:         authority.OwnerRef,
			AuthorityRef:     authority.AuthorityRef,
			Lineage:          authority.Lineage,
			PolicyArtifact:   append([]byte(nil), authority.PolicyArtifact...),
			SnapshotAnchorID: request.SnapshotAnchorID,
			OperationID:      request.OperationID,
			OwnerApproved:    request.OwnerApproved,
			PostVerify: func(
				verifyContext context.Context,
				verifyRequest backuplifecycle.RestoreVerificationRequest,
			) (backuplifecycle.RestoreVerification, error) {
				return verifyNativeV2BackupRestore(verifyContext, authority, verifyRequest)
			},
		})
	default:
		return nil, errors.New("native v2 backup operation is unsupported")
	}
}

func nativeV2BackupOperationContext(parent context.Context, maximum time.Duration) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if maximum <= 0 || maximum > nativeV2BackupMaximumOperationTimeout {
		maximum = nativeV2BackupMaximumOperationTimeout
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= maximum {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, maximum)
}
