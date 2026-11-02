package applicationrelease

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

// Directory holds one owner-signed record per scoped release operation.
const Directory = ".stackkit/applications/releases"

const maxRecordBytes = 4 << 20

// Persist signs and atomically replaces the record of its operation. A new
// record must start at StateCheckpointStarted; an existing one may move only
// along the declared transitions.
func Persist(workspace string, record Record) (Record, error) {
	now := time.Now().UTC()
	record.APIVersion = APIVersion
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	previous, found, err := read(workspace, record.OperationID)
	if err != nil {
		return Record{}, err
	}
	if !found && record.State != StateCheckpointStarted {
		return Record{}, errors.New("applicationrelease: a release operation starts at its checkpoint")
	}
	if found && (!CanAdvance(previous.State, record.State) || !sameIdentity(previous, record)) {
		return Record{}, fmt.Errorf("applicationrelease: operation %s cannot move from %s to %s", record.OperationID, previous.State, record.State)
	}
	if err := record.validate(); err != nil {
		return Record{}, err
	}
	record.Signature = localevidence.OwnerApplicationReleaseSignature{}
	canonical, err := json.Marshal(record)
	if err != nil {
		return Record{}, err
	}
	if record.Signature, err = localevidence.SignOwnerApplicationRelease(workspace, canonical); err != nil {
		return Record{}, err
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return Record{}, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Record{}, err
	}
	defer func() { _ = transaction.Close() }()
	if err := transaction.MkdirAll(Directory, 0o700); err != nil {
		return Record{}, err
	}
	final := path.Join(Directory, record.OperationID+".json")
	staged := path.Join(Directory, "."+record.OperationID+"."+fmt.Sprint(now.UnixNano())+".tmp")
	if err := transaction.WriteFileExclusive(staged, append(data, '\n'), 0o600); err != nil {
		return Record{}, err
	}
	if _, err := transaction.ReplaceRegularFile(staged, final); err != nil {
		return Record{}, err
	}
	return record, nil
}

// Read returns the verified record of one operation.
func Read(workspace, operation string) (Record, error) {
	record, found, err := read(workspace, operation)
	if err == nil && !found {
		err = fmt.Errorf("applicationrelease: no release operation %s", operation)
	}
	return record, err
}

// List returns every verified record, oldest first.
func List(workspace string) ([]Record, error) {
	entries, err := os.ReadDir(filepath.Join(workspace, filepath.FromSlash(Directory)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var records []Record
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		record, err := Read(workspace, strings.TrimSuffix(name, ".json"))
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.Before(records[j].CreatedAt) })
	return records, nil
}

// OpenFor returns the one record that still governs a workload.
func OpenFor(workspace, workload string) (Record, bool, error) {
	records, err := List(workspace)
	if err != nil {
		return Record{}, false, err
	}
	var open []Record
	for _, record := range records {
		if record.WorkloadRef == workload && record.Open() {
			open = append(open, record)
		}
	}
	if len(open) > 1 {
		return Record{}, false, fmt.Errorf("applicationrelease: workload %s has %d open release operations", workload, len(open))
	}
	if len(open) == 0 {
		return Record{}, false, nil
	}
	return open[0], true, nil
}

func read(workspace, operation string) (Record, bool, error) {
	if !ValidOperationID(operation) {
		return Record{}, false, errors.New("applicationrelease: invalid release operation identity")
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return Record{}, false, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return Record{}, false, err
	}
	defer func() { _ = transaction.Close() }()
	relative := path.Join(Directory, operation+".json")
	exists, _, err := transaction.Exists(relative)
	if err != nil || !exists {
		return Record{}, false, err
	}
	data, info, err := transaction.ReadStableBounded(relative, maxRecordBytes)
	if err != nil || info == nil || !info.Mode().IsRegular() {
		return Record{}, false, errors.New("applicationrelease: release record custody is unavailable")
	}
	var record Record
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return Record{}, false, fmt.Errorf("applicationrelease: decode release record: %w", err)
	}
	if record.OperationID != operation {
		return Record{}, false, errors.New("applicationrelease: release record names another operation")
	}
	if err := record.validate(); err != nil {
		return Record{}, false, err
	}
	signature := record.Signature
	record.Signature = localevidence.OwnerApplicationReleaseSignature{}
	canonical, err := json.Marshal(record)
	if err != nil {
		return Record{}, false, err
	}
	if err := localevidence.VerifyOwnerApplicationRelease(workspace, canonical, signature); err != nil {
		return Record{}, false, err
	}
	record.Signature = signature
	return record, true, nil
}

// sameIdentity keeps every authority field fixed across transitions; only the
// state and the volume readbacks advance.
func sameIdentity(previous, next Record) bool {
	if previous.OperationID != next.OperationID || previous.UseCaseRef != next.UseCaseRef || previous.WorkloadRef != next.WorkloadRef || previous.ModuleRef != next.ModuleRef ||
		previous.NodeRef != next.NodeRef || previous.ComposeProject != next.ComposeProject || previous.EntryComponent != next.EntryComponent ||
		previous.PriorRelease != next.PriorRelease || previous.PriorImage != next.PriorImage ||
		previous.TargetRelease != next.TargetRelease || previous.TargetImage != next.TargetImage ||
		previous.PriorSpecHash != next.PriorSpecHash || previous.TargetSpecHash != next.TargetSpecHash ||
		!bytes.Equal(previous.PriorSpec, next.PriorSpec) || !bytes.Equal(previous.TargetSpec, next.TargetSpec) ||
		previous.PlanHash != next.PlanHash || previous.KopiaHelperImage != next.KopiaHelperImage ||
		!previous.CreatedAt.Equal(next.CreatedAt) || len(previous.Volumes) != len(next.Volumes) {
		return false
	}
	for index, volume := range previous.Volumes {
		other := next.Volumes[index]
		if volume.LiveName != other.LiveName || volume.RollbackName != other.RollbackName || volume.ComponentRef != other.ComponentRef ||
			volume.LogicalName != other.LogicalName || volume.ComposeProject != other.ComposeProject {
			return false
		}
		// A completed checkpoint digest never changes afterwards.
		if volume.Checkpoint != nil && (other.Checkpoint == nil || *volume.Checkpoint != *other.Checkpoint) {
			return false
		}
	}
	return true
}
