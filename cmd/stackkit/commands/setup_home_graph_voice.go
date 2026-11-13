package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/homegraph"
)

// saveHomeGraphVoice publishes Cloud's approved graph to a local read-only
// SpeechKit consumer. It contains references and roles, never HA credentials.
func saveHomeGraphVoice(workspace string, graph homegraph.Graph) error {
	data, err := json.Marshal(graph)
	if err != nil || len(data) > homegraph.MaxSyncDocumentBytes {
		return errors.New("home graph voice document exceeds the sync limit")
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
	if err := tx.MkdirAll(homeGraphMappingDir, 0o700); err != nil {
		return err
	}
	target := homeGraphMappingDir + "/voice-" + graph.SpaceID + ".json"
	exists, _, err := tx.Exists(target)
	if err != nil {
		return err
	}
	if exists {
		previous, _, err := tx.ReadStableBounded(target, homegraph.MaxSyncDocumentBytes)
		if err != nil {
			return err
		}
		var prior homegraph.Graph
		if json.Unmarshal(previous, &prior) != nil || prior.SpaceID != graph.SpaceID || graph.Revision < prior.Revision || (graph.Revision == prior.Revision && !bytes.Equal(data, previous)) {
			return errors.New("home graph voice revision rollback or conflict")
		}
	}
	staged := fmt.Sprintf("%s/.voice-%d.tmp", homeGraphMappingDir, os.Getpid())
	if exists, info, _ := tx.Exists(staged); exists {
		_ = tx.RemoveRegularFile(staged, info)
	}
	if err := tx.WriteFileExclusive(staged, data, 0o600); err != nil {
		return err
	}
	defer func() {
		if exists, info, _ := tx.Exists(staged); exists {
			_ = tx.RemoveRegularFile(staged, info)
		}
	}()
	_, err = tx.ReplaceRegularFile(staged, target)
	return err
}
