package localorigin

import (
	"errors"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

// RemoveObsolete disables omitted publications in signed custody. Both new TLS
// handshakes and the next request on an existing connection consult that state.
// Keys remain in custody so interruption cannot accidentally resurrect a route.
func RemoveObsolete(root string, serverNames []string) error {
	keep := make(map[string]bool, len(serverNames))
	for _, name := range serverNames {
		keep[name] = true
	}
	fs, err := confinedfs.Open(root)
	if err != nil {
		return err
	}
	defer fs.Close()
	tx, err := fs.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	entries, err := tx.Walk(stateDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Path, stateDirectory+"/publication-") || len(entry.Path) != len(stateDirectory+"/publication-")+64+len(".json") || !strings.HasSuffix(entry.Path, ".json") {
			continue
		}
		var p publication
		if err := readState(root, entry.Path, "publication", &p); err != nil {
			return err
		}
		if stateRef("publication", p.Policy.ServerName) != entry.Path {
			return errors.New("localorigin: publication path mismatch")
		}
		if keep[p.Policy.ServerName] || p.Disabled {
			continue
		}
		p.Disabled = true
		if err := writeState(root, entry.Path, "publication", p); err != nil {
			return err
		}
	}
	return nil
}
