package localevidence

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

func CloudSessionRoutingArchivePath(state CloudSessionRouting) string {
	return ".stackkit/custody/cloud-session-routing-history/" + strings.TrimPrefix(SessionRoutingValueDigest(state), "sha256:") + ".json"
}

// ArchiveAbortedCloudSessionRouting retains the exact signed terminal receipt
// before retiring its active pointer. A closed diagnostic must not become a
// competing authority over future normal Apply, restore or owner realization.
// The caller holds the existing lifecycle mutation lock.
func ArchiveAbortedCloudSessionRouting(workspace string) error {
	state, err := LoadCloudSessionRouting(workspace)
	if err != nil {
		return err
	}
	if state.Phase != "aborted" {
		return errors.New("cloud_session_transition_pending: only a verified aborted transition can be archived")
	}
	canonical, err := json.Marshal(state)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	raw, info, err := tx.ReadStableBounded(CloudSessionRoutingPath, 8<<20)
	if err != nil || !bytes.Equal(raw, canonical) {
		return errors.New("cloud_session_transition_drift: terminal journal changed before archival")
	}
	if err := tx.MkdirAll(".stackkit/custody/cloud-session-routing-history", 0o700); err != nil {
		return err
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	archive := CloudSessionRoutingArchivePath(state)
	result, err := view.WriteAtomic0600NoReplace(archive, raw)
	if err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, _, readErr := tx.ReadStableBounded(archive, 8<<20)
		if readErr != nil || !bytes.Equal(existing, raw) {
			return errors.New("cloud_session_transition_drift: archived receipt differs from the terminal journal")
		}
	} else if !result.Installed || !result.FileSynced {
		return errors.New("cloud_session_transition_write_failed: terminal receipt was not durably archived")
	}
	// Persist both the archive entry and its newly created parent before
	// retiring the active pointer; a crash must not lose both references.
	for _, directory := range []string{".stackkit/custody/cloud-session-routing-history", ".stackkit/custody"} {
		if _, err := tx.SyncDirectory(directory); err != nil {
			return err
		}
	}
	if err := tx.RemoveRegularFile(CloudSessionRoutingPath, info); err != nil {
		return err
	}
	_, err = tx.SyncDirectory(".stackkit/custody")
	return err
}
