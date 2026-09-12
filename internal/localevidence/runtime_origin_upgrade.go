package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const originUpgradePath = ".stackkit/custody/basement-origin-upgrade.json"
const originConfigPath = "step-ca/config/ca.json"

type basementOriginUpgrade struct {
	OldConfig   []byte                    `json:"oldConfig"`
	NewConfig   []byte                    `json:"newConfig"`
	OldManifest []byte                    `json:"oldManifest"`
	NewManifest []byte                    `json:"newManifest"`
	Signature   OwnerPolicyStateSignature `json:"signature"`
}

// UpgradeBasementOriginProvisioner is an Apply-owned custody transition. It
// preserves established secrets and journals both signed file generations
// before replacement. A true result means step-ca must still be reloaded.
func UpgradeBasementOriginProvisioner(workspaceRoot string) (bool, error) {
	return withOriginUpgradeLock(workspaceRoot, func(tx *confinedfs.Transaction) (bool, error) {
		journal, err := loadOriginUpgrade(workspaceRoot, tx)
		if errors.Is(err, os.ErrNotExist) {
			if _, err := LoadBasementRuntimeCustody(workspaceRoot); err != nil {
				return false, err
			}
			if err := requireBasementWorkloadProvisioners(workspaceRoot); err == nil {
				return false, nil
			} else if !errors.Is(err, ErrBasementOriginProvisionerMissing) {
				return false, err
			}
			journal, err = prepareOriginUpgrade(workspaceRoot, tx)
			if err != nil {
				return false, err
			}
			path, err := confinedCustodyPath(workspaceRoot, originUpgradePath)
			if err != nil {
				return false, err
			}
			if err := writePrivateJSON(path, journal); err != nil {
				return false, err
			}
			if _, err := tx.SyncDirectory(".stackkit/custody"); err != nil {
				return false, err
			}
		} else if err != nil {
			return false, err
		}
		if err := applyOriginUpgrade(workspaceRoot, tx, journal); err != nil {
			return false, err
		}
		// A prior binary may have journaled only the server profile. Finish its
		// exact signed generation first, then durably journal the next transition.
		// Both crash boundaries remain replayable without replacing old secrets.
		if err := requireBasementWorkloadProvisioners(workspaceRoot); err != nil {
			if !errors.Is(err, ErrBasementOriginProvisionerMissing) {
				return false, err
			}
			next, err := prepareOriginUpgrade(workspaceRoot, tx)
			if err != nil {
				return false, err
			}
			path, err := confinedCustodyPath(workspaceRoot, originUpgradePath)
			if err != nil {
				return false, err
			}
			if err := writePrivateJSON(path, next); err != nil {
				return false, err
			}
			if _, err := tx.SyncDirectory(".stackkit/custody"); err != nil {
				return false, err
			}
			if err := applyOriginUpgrade(workspaceRoot, tx, next); err != nil {
				return false, err
			}
		}
		return true, nil
	})
}

// CompleteBasementOriginProvisionerUpgrade is called only after Apply has
// reloaded and observed the configured step-ca provisioner. Keeping the journal
// until then ensures a failed or interrupted reload is retried on the next Apply.
func CompleteBasementOriginProvisionerUpgrade(workspaceRoot string) error {
	_, err := withOriginUpgradeLock(workspaceRoot, func(tx *confinedfs.Transaction) (bool, error) {
		journal, err := loadOriginUpgrade(workspaceRoot, tx)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if err := validateOriginUpgradeCurrent(tx, journal); err != nil {
			return false, err
		}
		if _, err := LoadBasementRuntimeCustody(workspaceRoot); err != nil {
			return false, err
		}
		if err := requireBasementWorkloadProvisioners(workspaceRoot); err != nil {
			return false, err
		}
		path, err := confinedCustodyPath(workspaceRoot, originUpgradePath)
		if err != nil {
			return false, err
		}
		if err := os.Remove(path); err != nil {
			return false, err
		}
		_, err = tx.SyncDirectory(".stackkit/custody")
		return false, err
	})
	return err
}

func withOriginUpgradeLock(workspaceRoot string, apply func(*confinedfs.Transaction) (bool, error)) (bool, error) {
	root, err := confinedfs.Open(workspaceRoot)
	if err != nil {
		return false, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return false, err
	}
	defer tx.Close()
	lock, err := tx.TryAcquireOutputLock(basementRuntimeCustodyRelDir)
	if err != nil {
		return false, err
	}
	defer lock.Release()
	return apply(tx)
}

func prepareOriginUpgrade(workspaceRoot string, tx *confinedfs.Transaction) (basementOriginUpgrade, error) {
	var journal basementOriginUpgrade
	var err error
	journal.OldConfig, _, err = tx.ReadStableBounded(basementRuntimeCustodyRelDir+"/"+originConfigPath, 1<<20)
	if err != nil {
		return journal, err
	}
	journal.OldManifest, _, err = tx.ReadStableBounded(basementRuntimeCustodyRelDir+"/"+basementRuntimeManifestRelPath, 1<<20)
	if err != nil {
		return journal, err
	}
	// Retain any future config fields; the transition changes only the one
	// owner provisioner and never reconstructs the rest of step-ca configuration.
	var config map[string]json.RawMessage
	if err := json.Unmarshal(journal.OldConfig, &config); err != nil {
		return journal, err
	}
	var authority map[string]json.RawMessage
	if err := json.Unmarshal(config["authority"], &authority); err != nil {
		return journal, err
	}
	var provisioners []json.RawMessage
	if err := json.Unmarshal(authority["provisioners"], &provisioners); err != nil {
		return journal, err
	}
	expected, err := basementWorkloadProvisioners(workspaceRoot)
	if err != nil {
		return journal, err
	}
	for _, wanted := range expected {
		found := false
		for _, raw := range provisioners {
			var value basementStepCAProvisioner
			if err := json.Unmarshal(raw, &value); err != nil {
				return journal, err
			}
			if value.Name != wanted.Name {
				continue
			}
			if found || !reflect.DeepEqual(value, wanted) {
				return journal, errors.New("localevidence: conflicting workload provisioner must not be replaced")
			}
			found = true
		}
		if !found {
			raw, err := json.Marshal(wanted)
			if err != nil {
				return journal, err
			}
			provisioners = append(provisioners, raw)
		}
	}
	authority["provisioners"], err = json.Marshal(provisioners)
	if err != nil {
		return journal, err
	}
	config["authority"], err = json.Marshal(authority)
	if err != nil {
		return journal, err
	}
	journal.NewConfig, err = json.MarshalIndent(config, "", "  ")
	if err != nil {
		return journal, err
	}
	var manifest BasementRuntimeCustody
	if err := json.Unmarshal(journal.OldManifest, &manifest); err != nil {
		return journal, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return journal, err
	}
	for i := range manifest.Files {
		if manifest.Files[i].Path == originConfigPath {
			manifest.Files[i].MAC = basementRuntimeFileMAC(key, originConfigPath, journal.NewConfig)
		}
	}
	manifest.Signature = ""
	signing, err := basementRuntimeSigningBytes(manifest)
	if err != nil {
		return journal, err
	}
	manifest.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signing))
	journal.NewManifest, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return journal, err
	}
	signing, err = json.Marshal(journal)
	if err != nil {
		return journal, err
	}
	journal.Signature, err = SignOwnerPolicyState(workspaceRoot, signing)
	return journal, err
}

func loadOriginUpgrade(workspaceRoot string, tx *confinedfs.Transaction) (basementOriginUpgrade, error) {
	var journal basementOriginUpgrade
	raw, _, err := tx.ReadStableBounded(originUpgradePath, 4<<20)
	if err != nil {
		return journal, err
	}
	if err := json.Unmarshal(raw, &journal); err != nil {
		return journal, err
	}
	signature := journal.Signature
	journal.Signature = OwnerPolicyStateSignature{}
	signing, err := json.Marshal(journal)
	journal.Signature = signature
	if err != nil {
		return journal, err
	}
	if err := VerifyOwnerPolicyState(workspaceRoot, signing, signature); err != nil {
		return journal, err
	}
	return journal, nil
}

func validateOriginUpgradeCurrent(tx *confinedfs.Transaction, journal basementOriginUpgrade) error {
	for _, target := range []struct {
		path      string
		old, next []byte
	}{
		{originConfigPath, journal.OldConfig, journal.NewConfig},
		{basementRuntimeManifestRelPath, journal.OldManifest, journal.NewManifest},
	} {
		current, _, err := tx.ReadStableBounded(basementRuntimeCustodyRelDir+"/"+target.path, 1<<20)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, target.old) && !bytes.Equal(current, target.next) {
			return fmt.Errorf("localevidence: custody changed outside origin upgrade: %s", target.path)
		}
	}
	return nil
}

func applyOriginUpgrade(workspaceRoot string, tx *confinedfs.Transaction, journal basementOriginUpgrade) error {
	if err := validateOriginUpgradeCurrent(tx, journal); err != nil {
		return err
	}
	// A reload retry must not conceal unrelated secret changes during a prior
	// interrupted config/manifest replacement.
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return err
	}
	var oldManifest BasementRuntimeCustody
	if err := json.Unmarshal(journal.OldManifest, &oldManifest); err != nil {
		return err
	}
	for _, file := range oldManifest.Files {
		if file.Path == originConfigPath {
			continue
		}
		content, _, err := tx.ReadStableBounded(basementRuntimeCustodyRelDir+"/"+file.Path, 1<<20)
		if err != nil {
			return err
		}
		if basementRuntimeFileMAC(key, file.Path, content) != file.MAC {
			return errors.New("localevidence: unrelated runtime custody changed during origin upgrade")
		}
	}
	for _, target := range []struct {
		path    string
		content json.RawMessage
	}{{originConfigPath, journal.NewConfig}, {basementRuntimeManifestRelPath, journal.NewManifest}} {
		path, err := confinedCustodyPath(workspaceRoot, filepath.ToSlash(filepath.Join(basementRuntimeCustodyRelDir, target.path)))
		if err != nil {
			return err
		}
		if err := writePrivateJSON(path, target.content); err != nil {
			return err
		}
		if _, err := tx.SyncDirectory(filepath.ToSlash(filepath.Dir(filepath.Join(basementRuntimeCustodyRelDir, target.path)))); err != nil {
			return err
		}
	}
	_, err = LoadBasementRuntimeCustody(workspaceRoot)
	return err
}
