package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/lifecyclemutation"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localorigin"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

// The current complete group is retained here; previous groups remain in their
// owning signed snapshot/activation histories and are linked by exact digest.
// This is an extension of native volume custody, not a backup/control journal.
type runtipiVolumeCustodyLineage struct {
	Version            int                                `json:"version"`
	EntryComponent     string                             `json:"entryComponent"`
	Initial            backuplifecycle.AppGroupGeneration `json:"initial"`
	Head               *backuplifecycle.AppGroupControl   `json:"head,omitempty"`
	PreviousGroup      *runtipiPriorGroup                 `json:"previousGroup,omitempty"`
	PriorCustodyDigest string                             `json:"priorCustodyDigest,omitempty"`
}
type runtipiPriorGroup struct {
	OperationID string `json:"operationId"`
	GroupID     string `json:"groupId"`
	Digest      string `json:"digest"`
}

func runtipiBytesDigest(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
func normalizedRuntipiGroup(g backuplifecycle.AppGroupControl) backuplifecycle.AppGroupControl {
	g = backuplifecycle.CloneAppGroups([]backuplifecycle.AppGroupControl{g})[0]
	for i := range g.Effects {
		if g.Effects[i].State == backuplifecycle.ControlObserved {
			g.Effects[i].State = backuplifecycle.ControlAdopted
		}
		if g.Effects[i].State == backuplifecycle.ControlFailed {
			g.Effects[i].State = backuplifecycle.ControlNegativeAdopted
		}
	}
	return g
}
func currentRuntipiCustody(c standaloneComposeVolumeCustody) backuplifecycle.AppGroupGeneration {
	if c.AppGroupLineage.Head == nil {
		return c.AppGroupLineage.Initial
	}
	return c.AppGroupLineage.Head.Current()
}
func runtipiCustodyEntry(c standaloneComposeVolumeCustody, lastPresent bool) string {
	if c.AppGroupLineage == nil {
		return c.EntryContainerID
	}
	l := c.AppGroupLineage
	generation := currentRuntipiCustody(c)
	if generation.Absent && lastPresent && l.Head != nil {
		generation = l.Head.Original
		for _, e := range l.Head.Effects {
			if e.Generation != nil && !e.Generation.Absent {
				generation = *e.Generation
			}
		}
	}
	for _, v := range generation.Containers {
		if v.ComponentRef == l.EntryComponent {
			return v.ID
		}
	}
	return ""
}
func validateRuntipiVolumeCustody(c standaloneComposeVolumeCustody) error {
	l := c.AppGroupLineage
	if l == nil {
		return nil
	}
	if l.Version != 2 || l.EntryComponent == "" || l.Initial.Absent || len(l.Initial.Containers) == 0 {
		return errors.New("native v2 custody root invalid")
	}
	original := ""
	for _, v := range l.Initial.Containers {
		if v.ComponentRef == l.EntryComponent {
			original = v.ID
		}
	}
	if original == "" || original != c.EntryContainerID {
		return errors.New("immutable native custody G0 entry changed")
	}
	if l.Head != nil {
		if err := backuplifecycle.ValidateAppGroups([]backuplifecycle.AppGroupControl{*l.Head}); err != nil {
			return err
		}
		if l.Head.Binding.LogicalProject != c.Project || l.Head.Binding.AppliedRequestDigest != c.AppliedRequestDigest {
			return errors.New("native group lineage changes applied project")
		}
	}
	return nil
}
func readRuntipiVolumeCustody(workspace string, tx *confinedfs.Transaction, path string) (standaloneComposeVolumeCustody, []byte, error) {
	var record standaloneComposeVolumeCustodyRecord
	raw, info, err := tx.ReadStableBounded(path, 1<<20)
	if err != nil {
		return record.Custody, nil, err
	}
	if !info.Mode().IsRegular() {
		return record.Custody, nil, errors.New("native custody is not regular")
	}
	if json.Unmarshal(raw, &record) != nil {
		return record.Custody, nil, errors.New("native custody payload invalid")
	}
	canonical, err := resolvedplan.CanonicalJSON(record.Custody)
	if err != nil {
		return record.Custody, nil, err
	}
	if err = localevidence.VerifyOwnerLifecycleMutation(workspace, canonical, record.Signature); err != nil {
		return record.Custody, nil, err
	}
	if record.Custody.APIVersion != volumeCustodyAPIVersion {
		return record.Custody, nil, errors.New("native volume custody version invalid")
	}
	if err = validateRuntipiVolumeCustody(record.Custody); err != nil {
		return record.Custody, nil, err
	}
	return record.Custody, raw, nil
}
func writeRuntipiVolumeCustody(workspace string, tx *confinedfs.Transaction, view confinedfs.View, path string, c standaloneComposeVolumeCustody, previous []byte) error {
	canonical, err := resolvedplan.CanonicalJSON(c)
	if err != nil {
		return err
	}
	signature, err := localevidence.SignOwnerLifecycleMutation(workspace, canonical)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(standaloneComposeVolumeCustodyRecord{Custody: c, Signature: signature})
	if err != nil {
		return err
	}
	if len(encoded) > 1<<20 {
		return errors.New("native group custody exceeds bound")
	}
	if err = tx.MkdirAll(filepath.ToSlash(filepath.Dir(path)), 0700); err != nil {
		return err
	}
	if previous != nil {
		current, _, err := tx.ReadStableBounded(path, 1<<20)
		if err != nil || runtipiBytesDigest(current) != runtipiBytesDigest(previous) {
			return errors.New("native generation CAS preimage changed")
		}
	}
	var result confinedfs.AtomicWriteResult
	if previous == nil {
		result, err = view.WriteAtomic0600NoReplace(path, encoded)
	} else {
		result, err = view.WriteAtomic0600(path, encoded)
	}
	if err != nil {
		return err
	}
	if !result.Installed || !result.FileSynced {
		return errors.New("native custody installation was not durable")
	}
	if _, err = tx.SyncDirectory(filepath.ToSlash(filepath.Dir(path))); err != nil {
		return err
	}
	actual, _, err := tx.ReadStableBounded(path, 1<<20)
	if err != nil {
		return err
	}
	if runtipiBytesDigest(actual) != runtipiBytesDigest(encoded) {
		return errors.New("native generation CAS readback differs")
	}
	return nil
}
func ensureRuntipiInitialCustody(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, project standaloneComposeProject, g *backuplifecycle.AppGroupControl) error {
	return lifecyclemutation.WithAppGroupCustodyTransaction(ctx, workspace, g.Binding.JournalOperationID, func(tx *confinedfs.Transaction, view confinedfs.View) error {
		path := volumeCustodyPath(g.Binding.AppliedRequestDigest, deployment.WorkloadRef)
		c, raw, err := readRuntipiVolumeCustody(workspace, tx, path)
		if errors.Is(err, os.ErrNotExist) {
			c = standaloneComposeVolumeCustody{APIVersion: volumeCustodyAPIVersion, AppliedRequestDigest: g.Binding.AppliedRequestDigest, WorkloadRef: deployment.WorkloadRef, InstanceRef: deployment.InstanceRef, Project: g.Binding.LogicalProject, WorkingDir: project.directory, AppGroupLineage: &runtipiVolumeCustodyLineage{Version: 2, EntryComponent: project.bundle.EntryComponent, Initial: g.Original}}
			for _, container := range g.Original.Containers {
				if container.ComponentRef == project.bundle.EntryComponent {
					c.EntryContainerID = container.ID
				}
			}
			for _, volume := range g.Original.Volumes {
				actual, e := docker.NewLocalClient().InspectVolumeIdentity(ctx, volume.Name)
				if e != nil {
					return e
				}
				target := ""
				for _, container := range g.Original.Containers {
					for _, mount := range container.Mounts {
						if mount.Name == volume.Name {
							target = mount.Destination
						}
					}
				}
				key := strings.TrimPrefix(volume.Name, g.Binding.LogicalProject+"_")
				c.Volumes = append(c.Volumes, standaloneComposeVolumeCustodyEntry{Key: key, Name: volume.Name, Driver: actual.Driver, Target: target, CreatedAt: actual.CreatedAt, Mountpoint: actual.Mountpoint, Scope: actual.Scope, Options: normalizedVolumeOptions(actual.Options)})
			}
			if err = writeRuntipiVolumeCustody(workspace, tx, view, path, c, nil); err != nil {
				return err
			}
			c, raw, err = readRuntipiVolumeCustody(workspace, tx, path)
		}
		if err != nil {
			return err
		}
		if c.WorkloadRef != deployment.WorkloadRef || c.InstanceRef != deployment.InstanceRef || c.Project != g.Binding.LogicalProject || c.AppliedRequestDigest != g.Binding.AppliedRequestDigest || filepath.Clean(c.WorkingDir) != filepath.Clean(project.directory) {
			return errors.New("G0 custody differs from held applied native workload")
		}
		if c.AppGroupLineage != nil {
			if backuplifecycle.AppGroupDigest(currentRuntipiCustody(c)) != backuplifecycle.AppGroupDigest(g.Original) {
				return errors.New("G0 is not previously authorized native successor")
			}
		} else {
			entry := ""
			for _, v := range g.Original.Containers {
				if v.ComponentRef == project.bundle.EntryComponent {
					entry = v.ID
				}
			}
			if entry != c.EntryContainerID {
				return errors.New("G0 entry differs from existing exact native custody")
			}
		}
		if len(c.Volumes) != len(g.Original.Volumes) {
			return errors.New("G0 volume custody set differs")
		}
		for _, held := range c.Volumes {
			actual, e := docker.NewLocalClient().InspectVolumeIdentity(ctx, held.Name)
			if e != nil {
				return e
			}
			if actual.CreatedAt != held.CreatedAt || actual.Mountpoint != held.Mountpoint || actual.Driver != held.Driver || actual.Scope != held.Scope || !reflect.DeepEqual(normalizedVolumeOptions(actual.Options), normalizedVolumeOptions(held.Options)) {
				return errors.New("G0 volume custody identity changed")
			}
		}
		g.Binding.CustodyDigest = runtipiBytesDigest(raw)
		g.Binding.BindingDigest = runtipiBindingDigest(g.Binding, deployment)
		return nil
	})
}
func newRuntipiGroupCustody(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, lineage backuplifecycle.AuthorityLineage, session *lifecyclemutation.Session) (RuntipiGroupCustody, error) {
	scoped := func(c context.Context, g backuplifecycle.AppGroupControl) (context.Context, error) {
		if session != nil {
			return session.WithAppGroupContext(c)
		}
		if err := lifecyclemutation.RequireAppGroupLease(c, workspace, g.Binding.JournalOperationID); err != nil {
			return nil, err
		}
		return c, nil
	}
	verify := func(c context.Context, g backuplifecycle.AppGroupControl, completed bool) error {
		bound, err := scoped(c, g)
		if err != nil {
			return err
		}
		if err = lifecyclemutation.RequireAppGroupLease(bound, workspace, g.Binding.JournalOperationID); err != nil {
			return err
		}
		if err = verifyRuntipiBinding(g.Binding, deployment, lineage); err != nil {
			return err
		}
		if session != nil {
			return session.VerifyRestoreAppGroup(g, completed)
		}
		return backuplifecycle.VerifySnapshotAppGroupAuthority(workspace, g, lineage, completed)
	}
	custody := RuntipiGroupCustody{Observe: ObserveRuntipiAppGroupGeneration, VerifyJournal: func(c context.Context, g backuplifecycle.AppGroupControl) error { return verify(c, g, false) }, VerifyCompletion: func(c context.Context, g backuplifecycle.AppGroupControl) error { return verify(c, g, true) }}
	custody.PredecessorDigest = func(c context.Context, g backuplifecycle.AppGroupControl) (string, error) {
		bound, err := scoped(c, g)
		if err != nil {
			return "", err
		}
		var digest string
		err = lifecyclemutation.WithAppGroupCustodyTransaction(bound, workspace, g.Binding.JournalOperationID, func(tx *confinedfs.Transaction, _ confinedfs.View) error {
			_, raw, e := readRuntipiVolumeCustody(workspace, tx, volumeCustodyPath(g.Binding.AppliedRequestDigest, deployment.WorkloadRef))
			if e == nil {
				digest = runtipiBytesDigest(raw)
			}
			return e
		})
		return digest, err
	}
	custody.Adopt = func(c context.Context, g backuplifecycle.AppGroupControl) error {
		if err := verify(c, g, false); err != nil {
			return err
		}
		bound, err := scoped(c, g)
		if err != nil {
			return err
		}
		if len(g.Effects) == 0 {
			if err = verify(c, g, true); err != nil {
				return err
			}
		}
		return lifecyclemutation.WithAppGroupCustodyTransaction(bound, workspace, g.Binding.JournalOperationID, func(tx *confinedfs.Transaction, view confinedfs.View) error {
			return adoptRuntipiGeneration(bound, workspace, deployment, g, tx, view)
		})
	}
	return custody, nil
}
func NewRuntipiSnapshotGroupCustody(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, lineage backuplifecycle.AuthorityLineage) (RuntipiGroupCustody, error) {
	return newRuntipiGroupCustody(ctx, workspace, deployment, lineage, nil)
}
func NewRuntipiRestoreGroupCustody(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, lineage backuplifecycle.AuthorityLineage, session *lifecyclemutation.Session) (RuntipiGroupCustody, error) {
	if session == nil {
		return RuntipiGroupCustody{}, errors.New("actual restore Session required")
	}
	return newRuntipiGroupCustody(ctx, workspace, deployment, lineage, session)
}
func adoptRuntipiGeneration(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, g backuplifecycle.AppGroupControl, tx *confinedfs.Transaction, view confinedfs.View) error {
	if len(g.Effects) == 0 {
		return adoptRuntipiUnchanged(ctx, workspace, deployment, g, tx, view)
	}
	target := normalizedRuntipiGroup(g)
	last := g.Effects[len(g.Effects)-1]
	if last.Generation == nil || last.State != backuplifecycle.ControlAdopted && last.State != backuplifecycle.ControlNegativeAdopted {
		return errors.New("native adoption requires signed terminal outcome/generation")
	}
	previous := ""
	observation := last.Action
	if last.Receipt != nil {
		previous = last.Receipt.PreviousCustodyDigest
	} else if last.Negative != nil {
		previous = last.Negative.PreviousCustodyDigest
		observation = "settle-negative"
	} else {
		return errors.New("native terminal proof missing")
	}
	if last.Negative != nil && last.Negative.NoNativeActuation {
		observation = "verify"
	}
	path := volumeCustodyPath(g.Binding.AppliedRequestDigest, deployment.WorkloadRef)
	c, raw, err := readRuntipiVolumeCustody(workspace, tx, path)
	if err != nil {
		return err
	}
	if c.Project != g.Binding.LogicalProject || c.WorkloadRef != deployment.WorkloadRef || c.InstanceRef != deployment.InstanceRef {
		return errors.New("native generation custody placement changed")
	}
	if c.AppGroupLineage == nil {
		c.AppGroupLineage = &runtipiVolumeCustodyLineage{Version: 2, Initial: g.Original, EntryComponent: findRuntipiEntry(g, c.EntryContainerID)}
	}
	l := c.AppGroupLineage
	replay := l.Head != nil && backuplifecycle.AppGroupDigest(normalizedRuntipiGroup(*l.Head)) == backuplifecycle.AppGroupDigest(target)
	// Revalidate on exact replay too: installed custody is not proof that an
	// unfenced external actor has left the signed graph untouched since CAS.
	actual, e := ObserveRuntipiAppGroupGeneration(ctx, g, observation)
	if e != nil {
		return e
	}
	if backuplifecycle.AppGroupDigest(actual) != backuplifecycle.AppGroupDigest(*last.Generation) {
		return errors.New("native terminal graph changed before adoption/replay")
	}
	if !replay {
		if runtipiBytesDigest(raw) != previous {
			return errors.New("native generation CAS exact signed predecessor changed")
		}
		if len(g.Effects) == 1 {
			if previous != g.Binding.CustodyDigest || backuplifecycle.AppGroupDigest(currentRuntipiCustody(c)) != backuplifecycle.AppGroupDigest(g.Original) {
				return errors.New("first native effect does not follow captured custody G0")
			}
			if l.Head != nil {
				l.PreviousGroup = &runtipiPriorGroup{l.Head.Binding.JournalOperationID, l.Head.Binding.GroupID, backuplifecycle.AppGroupDigest(*l.Head)}
			}
		} else {
			before := normalizedRuntipiGroup(g)
			before.Effects = before.Effects[:len(before.Effects)-1]
			if l.Head == nil || backuplifecycle.AppGroupDigest(normalizedRuntipiGroup(*l.Head)) != backuplifecycle.AppGroupDigest(before) {
				return errors.New("native generation CAS ordered group predecessor changed")
			}
		}
		actual, e := ObserveRuntipiAppGroupGeneration(ctx, g, observation)
		if e != nil {
			return e
		}
		if backuplifecycle.AppGroupDigest(actual) != backuplifecycle.AppGroupDigest(*last.Generation) {
			return errors.New("native successor changed before adoption")
		}
		l.PriorCustodyDigest = runtipiBytesDigest(raw)
		installed := backuplifecycle.CloneAppGroups([]backuplifecycle.AppGroupControl{g})[0]
		l.Head = &installed
		if err = writeRuntipiVolumeCustody(workspace, tx, view, path, c, raw); err != nil {
			return err
		}
	} else if l.PriorCustodyDigest != previous {
		return errors.New("native replay predecessor receipt changed")
	}
	return refreshRuntipiGroupRoute(ctx, workspace, deployment, g)
}
func findRuntipiEntry(g backuplifecycle.AppGroupControl, id string) string {
	for _, v := range g.Original.Containers {
		if v.ID == id {
			return v.ComponentRef
		}
	}
	return ""
}
func refreshRuntipiGroupRoute(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, g backuplifecycle.AppGroupControl) error {
	operations, err := NewOSRuntipiWorkloadOperations(workspace)
	if err != nil {
		return err
	}
	o := operations.(*osRuntipiWorkloadOperations)
	project, err := o.prepared.prepareWithIdentityMutation(ctx, deployment, false)
	if err != nil {
		return err
	}
	if err = o.prepared.verifyPersisted(project); err != nil {
		return err
	}
	project.runtimeProject = g.Binding.PhysicalProject
	if project.bundle.Route.ServiceRef == "" {
		return nil
	}
	current := g.Current()
	entry := project.bundle.EntryComponent
	prior := g.Original
	for _, e := range g.Effects[:len(g.Effects)-1] {
		if e.Generation != nil && !e.Generation.Absent {
			prior = *e.Generation
		}
	}
	oldID := ""
	for _, v := range prior.Containers {
		if v.ComponentRef == entry {
			oldID = v.ID
		}
	}
	expected := localorigin.Backend{ModuleRef: project.bundle.ModuleRef, UnitRef: project.unitRef, InstanceRef: project.bundle.InstanceRef, NodeRef: project.bundle.NodeRef, ServiceRef: project.bundle.Route.ServiceRef, ContainerID: oldID}
	last := g.Effects[len(g.Effects)-1]
	if last.Negative != nil {
		// A failed child never registers a potentially partial application route.
		// Preparation/non-admission changes no route; retain exact previous identity.
		actual, exists, e := readRuntipiRoute(workspace, expected.InstanceRef)
		if e != nil {
			return e
		}
		if exists && (!sameRuntipiRouteIdentity(actual, expected) || actual.ContainerID != oldID) {
			return errors.New("negative settlement route identity changed")
		}
		if last.Negative.NoNativeActuation {
			return nil
		}
		if !exists {
			return nil
		}
		return withdrawRuntipiRoute(workspace, expected)
	}
	if current.Absent {
		return withdrawRuntipiRoute(workspace, expected)
	}
	id := ""
	for _, v := range current.Containers {
		if v.ComponentRef == entry {
			id = v.ID
		}
	}
	raw, err := o.prepared.runner.Run(ctx, standaloneComposeArgs(project, "port"), project.directory)
	if err != nil {
		return err
	}
	address, err := standaloneComposeLoopbackAddress(raw)
	if err != nil {
		return err
	}
	status, err := o.prepared.prober.Probe(ctx, "http://"+address+project.entry.HealthPath)
	if err != nil || status != 200 && status != 302 {
		return errors.New("successor application route not ready")
	}
	actual, exists, err := readRuntipiRoute(workspace, expected.InstanceRef)
	if err != nil {
		return err
	}
	if exists {
		if !sameRuntipiRouteIdentity(actual, expected) || actual.ContainerID != oldID && actual.ContainerID != id {
			return errors.New("origin route belongs to foreign generation")
		}
		if actual.ContainerID == id && actual.Address == address && actual.TargetPort == project.entry.HealthPort {
			return nil
		}
	}
	expected.ContainerID = id
	expected.Address = address
	expected.TargetPort = project.entry.HealthPort
	expected.ObservedAt = time.Now().UTC()
	if err = localorigin.RecordBackend(workspace, expected); err != nil {
		return err
	}
	actual, exists, err = readRuntipiRoute(workspace, expected.InstanceRef)
	if err != nil {
		return err
	}
	if !exists || !reflect.DeepEqual(actual, expected) {
		return errors.New("successor route custody readback differs")
	}
	return nil
}
func sameRuntipiRouteIdentity(a, b localorigin.Backend) bool {
	return a.ModuleRef == b.ModuleRef && a.UnitRef == b.UnitRef && a.InstanceRef == b.InstanceRef && a.NodeRef == b.NodeRef && a.ServiceRef == b.ServiceRef
}
func withdrawRuntipiRoute(workspace string, expected localorigin.Backend) error {
	if err := localorigin.WithdrawBackend(workspace, expected); err != nil {
		return err
	}
	_, exists, err := readRuntipiRoute(workspace, expected.InstanceRef)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("original route was not withdrawn")
	}
	return nil
}
func readRuntipiRoute(workspace, instance string) (localorigin.Backend, bool, error) {
	var backend localorigin.Backend
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return backend, false, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return backend, false, err
	}
	defer tx.Close()
	s := sha256.Sum256([]byte(instance))
	path := ".stackkit/custody/origin-runtime/backend-" + hex.EncodeToString(s[:]) + ".json"
	raw, _, err := tx.ReadStableBounded(path, 256<<10)
	if errors.Is(err, os.ErrNotExist) {
		return backend, false, nil
	}
	if err != nil {
		return backend, false, err
	}
	var record struct {
		Kind      string                                  `json:"kind"`
		Payload   json.RawMessage                         `json:"payload"`
		Signature localevidence.OwnerPolicyStateSignature `json:"signature"`
	}
	if json.Unmarshal(raw, &record) != nil || record.Kind != "backend" {
		return backend, false, errors.New("origin backend payload invalid")
	}
	sig := record.Signature
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	canonical, err := json.Marshal(record)
	if err != nil {
		return backend, false, err
	}
	if err = localevidence.VerifyOwnerPolicyState(workspace, canonical, sig); err != nil {
		return backend, false, err
	}
	if err = json.Unmarshal(record.Payload, &backend); err != nil {
		return backend, false, err
	}
	return backend, true, nil
}

func runtipiVolumeMatchesCustody(actual dockerVolumeInspect, entry standaloneComposeVolumeCustodyEntry, c standaloneComposeVolumeCustody) bool {
	if actual.Name != entry.Name || actual.CreatedAt != entry.CreatedAt || actual.Driver != entry.Driver || actual.Mountpoint != entry.Mountpoint || actual.Scope != entry.Scope || !reflect.DeepEqual(normalizedVolumeOptions(actual.Options), normalizedVolumeOptions(entry.Options)) {
		return false
	}
	encoded := docker.VolumeIdentity{Name: actual.Name, Driver: actual.Driver, Mountpoint: actual.Mountpoint, CreatedAt: actual.CreatedAt, Scope: actual.Scope, Labels: actual.Labels, Options: actual.Options}
	for _, v := range c.AppGroupLineage.Initial.Volumes {
		if v.Name == actual.Name {
			return v.IdentityDigest == backuplifecycle.AppGroupDigest(encoded)
		}
	}
	return false
}

// A zero-effect group retained its initial power. Completion may record its
// unchanged exact generation in native custody, without any route/power effect.
func adoptRuntipiUnchanged(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, g backuplifecycle.AppGroupControl, tx *confinedfs.Transaction, view confinedfs.View) error {
	if len(g.Effects) != 0 {
		return errors.New("unchanged adoption requires zero-effect group")
	}
	path := volumeCustodyPath(g.Binding.AppliedRequestDigest, deployment.WorkloadRef)
	c, raw, err := readRuntipiVolumeCustody(workspace, tx, path)
	if err != nil {
		return err
	}
	if c.AppGroupLineage == nil {
		return errors.New("unchanged group lacks native G0 custody")
	}
	actual, err := ObserveRuntipiAppGroupGeneration(ctx, g, "verify")
	if err != nil {
		return err
	}
	if backuplifecycle.AppGroupDigest(actual) != backuplifecycle.AppGroupDigest(g.Original) || backuplifecycle.AppGroupDigest(currentRuntipiCustody(c)) != backuplifecycle.AppGroupDigest(g.Original) {
		return errors.New("unchanged generation differs from native custody")
	}
	if c.AppGroupLineage.Head != nil && backuplifecycle.AppGroupDigest(*c.AppGroupLineage.Head) == backuplifecycle.AppGroupDigest(g) {
		return nil
	}
	if runtipiBytesDigest(raw) != g.Binding.CustodyDigest {
		return errors.New("unchanged native generation CAS preimage changed")
	}
	if c.AppGroupLineage.Head != nil {
		old := c.AppGroupLineage.Head
		c.AppGroupLineage.PreviousGroup = &runtipiPriorGroup{old.Binding.JournalOperationID, old.Binding.GroupID, backuplifecycle.AppGroupDigest(*old)}
	}
	c.AppGroupLineage.PriorCustodyDigest = runtipiBytesDigest(raw)
	installed := backuplifecycle.CloneAppGroups([]backuplifecycle.AppGroupControl{g})[0]
	c.AppGroupLineage.Head = &installed
	return writeRuntipiVolumeCustody(workspace, tx, view, path, c, raw)
}
