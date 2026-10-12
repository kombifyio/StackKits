package backupexec

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
	"math"
	"path"
	"reflect"
	"strings"
)

// SnapshotMember binds an independent Kopia source to the same StackKits
// operation. The aggregate is content-addressed; partial sets never complete.
type SnapshotMember struct {
	Source   localbackuppolicy.SnapshotSource `json:"source"`
	Snapshot Snapshot                         `json:"snapshot"`
}

func memberSnapshotRequest(parent SnapshotRequest, source localbackuppolicy.SnapshotSource) SnapshotRequest {
	sum := sha256.Sum256([]byte(parent.OperationID + "\x00" + source.VolumeName + "\x00" + source.DataClass))
	return SnapshotRequest{Source: source.Path, Description: parent.Description + ":" + source.VolumeName, OperationID: "stackkit-member." + hex.EncodeToString(sum[:]), ProtectRecovery: parent.ProtectRecovery}
}

func (e V2Engine) validateSnapshotSelection(request SnapshotRequest, create bool) error {
	root := request
	if e.selection != nil {
		if request.Source != e.selection.Path || len(request.Sources) != 0 {
			return fmt.Errorf("snapshot differs from the selected governed volume")
		}
		root.Source = DefaultVolumeSource
	} else if len(request.Sources) > 0 {
		if e.policy == nil {
			return fmt.Errorf("class snapshots require the held governed policy")
		}
		if create {
			if !reflect.DeepEqual(request.Sources, e.sources) {
				return fmt.Errorf("class snapshot sources differ from the current coverage policy")
			}
		} else if err := e.policy.ValidateSnapshotSourceSelection(request.Sources); err != nil {
			return err
		}
	} else if create && len(e.sources) > 0 {
		return fmt.Errorf("per-class policy cannot create a whole-root snapshot")
	}
	return validateSnapshotRequest(root)
}

func (e V2Engine) createClassSnapshots(ctx context.Context, request SnapshotRequest, password []byte) (Snapshot, error) {
	// A completed operation is read back exactly. A partial or expired set
	// cannot be mixed with a later writer state under the same operation ID.
	var existing []SnapshotMember
	for _, source := range request.Sources {
		engine, err := e.withSnapshotSource(source)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot, found, err := engine.FindSnapshot(ctx, memberSnapshotRequest(request, source), password)
		if err != nil {
			return Snapshot{}, err
		}
		if found {
			existing = append(existing, SnapshotMember{Source: source, Snapshot: snapshot})
		}
	}
	if len(existing) == len(request.Sources) {
		return aggregateSnapshot(request, existing, e.hostname)
	}
	if len(existing) > 0 {
		return Snapshot{}, fmt.Errorf("class snapshot operation has a partial or expired set; start a new governed operation after writer settlement")
	}
	members := make([]SnapshotMember, 0, len(request.Sources))
	for _, source := range request.Sources {
		engine, err := e.withSnapshotSource(source)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot, err := engine.CreateSnapshot(ctx, memberSnapshotRequest(request, source), password)
		if err != nil {
			return Snapshot{}, fmt.Errorf("capture class %s volume %s: %w", source.DataClass, source.VolumeName, err)
		}
		members = append(members, SnapshotMember{Source: source, Snapshot: snapshot})
	}
	return aggregateSnapshot(request, members, e.hostname)
}

func (e V2Engine) findClassSnapshots(ctx context.Context, request SnapshotRequest, password []byte) (Snapshot, bool, error) {
	members := make([]SnapshotMember, 0, len(request.Sources))
	for _, source := range request.Sources {
		engine, err := e.withSnapshotSource(source)
		if err != nil {
			return Snapshot{}, false, err
		}
		snapshot, found, err := engine.FindSnapshot(ctx, memberSnapshotRequest(request, source), password)
		if err != nil {
			return Snapshot{}, false, fmt.Errorf("lookup class %s volume %s: %w", source.DataClass, source.VolumeName, err)
		}
		if !found {
			return Snapshot{}, false, nil
		}
		members = append(members, SnapshotMember{Source: source, Snapshot: snapshot})
	}
	aggregate, err := aggregateSnapshot(request, members, e.hostname)
	return aggregate, err == nil, err
}

func aggregateSnapshot(request SnapshotRequest, members []SnapshotMember, hostname string) (Snapshot, error) {
	if len(members) == 0 || len(members) != len(request.Sources) {
		return Snapshot{}, fmt.Errorf("class snapshot set is incomplete")
	}
	result := Snapshot{SourcePath: request.Source, SourceHost: hostname, Description: request.Description, OperationID: request.OperationID, Members: members}
	if request.ProtectRecovery {
		result.Pins = []string{snapshotRecoveryPin(request.OperationID)}
	}
	for i, member := range members {
		wanted := memberSnapshotRequest(request, request.Sources[i])
		snapshot := member.Snapshot
		if !reflect.DeepEqual(member.Source, request.Sources[i]) || len(snapshot.Members) != 0 || !validPortableID(snapshot.ID) || snapshot.SourcePath != wanted.Source || snapshot.SourceHost != hostname || snapshot.Description != wanted.Description || snapshot.OperationID != wanted.OperationID || !snapshotProtectionMatches(snapshot, wanted) || snapshot.StartTime.IsZero() || snapshot.EndTime.IsZero() || snapshot.EndTime.Before(snapshot.StartTime) || snapshot.TotalSize < 0 {
			return Snapshot{}, fmt.Errorf("class snapshot member differs from its exact source and operation")
		}
		if result.StartTime.IsZero() || snapshot.StartTime.Before(result.StartTime) {
			result.StartTime = snapshot.StartTime
		}
		if snapshot.EndTime.After(result.EndTime) {
			result.EndTime = snapshot.EndTime
		}
		if snapshot.TotalSize > math.MaxInt64-result.TotalSize {
			return Snapshot{}, fmt.Errorf("class snapshot byte total overflows")
		}
		result.TotalSize += snapshot.TotalSize
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return Snapshot{}, err
	}
	sum := sha256.Sum256(encoded)
	result.ID = "stackkit-set." + hex.EncodeToString(sum[:])
	return result, nil
}

func (e V2Engine) restoreClassSnapshots(ctx context.Context, request RestoreRequest, password []byte) (RestoreResult, error) {
	snapshot := request.Snapshot
	if snapshot == nil || snapshot.ID != request.SnapshotID || len(snapshot.Members) == 0 {
		return RestoreResult{}, fmt.Errorf("restore requires the exact observed class snapshot set")
	}
	if e.policy == nil {
		return RestoreResult{}, fmt.Errorf("restore class set requires the held governed source")
	}
	sources := make([]localbackuppolicy.SnapshotSource, 0, len(snapshot.Members))
	for _, member := range snapshot.Members {
		sources = append(sources, member.Source)
	}
	if err := e.policy.ValidateSnapshotSourceSelection(sources); err != nil {
		return RestoreResult{}, err
	}
	parent := SnapshotRequest{Source: DefaultVolumeSource, Description: snapshot.Description, OperationID: snapshot.OperationID, ProtectRecovery: len(snapshot.Pins) > 0, Sources: sources}
	rebuilt, err := aggregateSnapshot(parent, snapshot.Members, e.hostname)
	if err != nil || !reflect.DeepEqual(rebuilt, *snapshot) {
		return RestoreResult{}, fmt.Errorf("restore class set differs from the governed content-addressed snapshot")
	}
	// Verify every retained member before writing any staging data. An expired
	// media member makes a full restore unavailable, never silently partial.
	for _, member := range snapshot.Members {
		if err := e.verifySnapshotContent(ctx, member.Snapshot.ID, password); err != nil {
			return RestoreResult{}, fmt.Errorf("verify class %s before restore: %w", member.Source.DataClass, err)
		}
	}
	for _, member := range snapshot.Members {
		relative := strings.TrimPrefix(member.Source.Path, DefaultVolumeSource+"/")
		if relative == member.Source.Path || path.Clean(relative) != relative || strings.HasPrefix(relative, "../") {
			return RestoreResult{}, fmt.Errorf("class restore member escapes the governed source root")
		}
		target := path.Join(request.StagingPath, relative)
		input, err := repositoryPasswordInput(password, 1)
		if err != nil {
			return RestoreResult{}, err
		}
		_, restoreErr := e.invoke(ctx, []string{"snapshot", "restore", member.Snapshot.ID, target, "--no-overwrite-symlinks", "--write-files-atomically", "--no-ignore-permission-errors", "--no-ignore-errors", "--no-skip-owners"}, input)
		clear(input)
		if restoreErr != nil {
			return RestoreResult{}, fmt.Errorf("restore class %s to isolated staging: %w", member.Source.DataClass, restoreErr)
		}
	}
	for _, member := range snapshot.Members {
		if err := e.verifySnapshotContent(ctx, member.Snapshot.ID, password); err != nil {
			return RestoreResult{}, fmt.Errorf("verify class %s after restore: %w", member.Source.DataClass, err)
		}
	}
	return RestoreResult{SnapshotID: request.SnapshotID, OperationID: request.OperationID, StagingPath: request.StagingPath, RepositoryContentVerified: true}, nil
}
