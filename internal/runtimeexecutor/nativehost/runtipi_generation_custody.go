package nativehost

import (
	"errors"
	"reflect"

	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/docker"
)

// The current complete group is retained here; previous groups remain in their
// owning signed snapshot/activation histories and are linked by exact digest.
// This is an extension of native volume custody, not a backup/control journal.
// Runtipi removal still reads it as retained custody, so the record form and its
// validation stay although nothing installs a lineage any more.
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

// runtipiVolumeMatchesCustody proves a retained lineage's immutable G0 volume
// identity still matches the actual daemon volume.
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
