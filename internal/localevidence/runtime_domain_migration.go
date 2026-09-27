package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const basementDomainMigrationPath = ".stackkit/custody/basement-domain-migration.json"

// This transition is deliberately restricted to the retired Basement default.
// Arbitrary identity-domain changes need their own explicit migration contract.
const LegacyBasementDomain = "home"
const MigratedBasementDomain = "lab.home"

type domainMigrationFile struct {
	Before []byte `json:"before"`
	After  []byte `json:"after"`
}

type basementDomainMigration struct {
	From      string                         `json:"from"`
	To        string                         `json:"to"`
	Files     map[string]domainMigrationFile `json:"files"`
	Signature OwnerPolicyStateSignature      `json:"signature"`
}

// MigrateBasementRuntimeDomain moves the legacy home runtime inputs to lab.home.
// The caller must hold the lifecycle mutation session and obtain explicit Owner
// approval. It does not alter users, passkeys, secrets, certificate authorities,
// volumes or application data. A signed private journal makes partial writes
// replayable and retains the previous configuration for recovery.
//
// The caller still owns StackSpec migration, PocketID callback convergence and
// rollout. Success here describes only the authenticated local configuration.
func MigrateBasementRuntimeDomain(workspaceRoot string) error {
	_, err := withDomainMigrationLock(workspaceRoot, func(tx *confinedfs.Transaction, view confinedfs.View) (bool, error) {
		journal, err := loadBasementDomainMigration(workspaceRoot, tx)
		if errors.Is(err, os.ErrNotExist) {
			custody, loadErr := LoadBasementRuntimeCustody(workspaceRoot)
			if loadErr != nil {
				return false, loadErr
			}
			if custody.Domain == MigratedBasementDomain {
				return false, nil
			}
			journal, err = prepareBasementDomainMigration(workspaceRoot, tx)
			if err != nil {
				return false, err
			}
			raw, err := json.Marshal(journal)
			if err != nil {
				return false, err
			}
			if _, err := view.WriteAtomic0600(basementDomainMigrationPath, raw); err != nil {
				return false, err
			}
			if _, err := tx.SyncDirectory(".stackkit/custody"); err != nil {
				return false, err
			}
		} else if err != nil {
			return false, err
		}
		if err := applyBasementDomainMigration(workspaceRoot, tx, view, journal); err != nil {
			return false, err
		}
		// The signed long-lived reenrollment state is separate from this journal.
		// Retire exact-byte recovery constraints after the local bundle verifies.
		info, err := tx.Lstat(basementDomainMigrationPath)
		if err != nil {
			return false, err
		}
		if err := tx.RemoveRegularFile(basementDomainMigrationPath, info); err != nil {
			return false, err
		}
		_, err = tx.SyncDirectory(".stackkit/custody")
		return false, err
	})
	return err
}

func prepareBasementDomainMigration(root string, tx *confinedfs.Transaction) (basementDomainMigration, error) {
	journal := basementDomainMigration{From: LegacyBasementDomain, To: MigratedBasementDomain, Files: map[string]domainMigrationFile{}}
	custody, err := LoadBasementRuntimeCustody(root)
	if err != nil {
		return journal, err
	}
	if custody.Domain != LegacyBasementDomain {
		return journal, errors.New("localevidence: domain migration requires the established legacy home domain")
	}
	if _, _, err := tx.ReadStableBounded(originUpgradePath, 4<<20); !errors.Is(err, os.ErrNotExist) {
		return journal, errors.New("localevidence: finish the pending origin provisioner transition before domain migration")
	}
	key, err := LoadOwnerKey(root)
	if err != nil {
		return journal, err
	}
	add := func(path string, after []byte) error {
		before, _, err := tx.ReadStableBounded(path, 1<<20)
		if err != nil {
			return err
		}
		journal.Files[path] = domainMigrationFile{Before: before, After: after}
		return nil
	}
	for i, file := range custody.Files {
		path := basementRuntimeCustodyRelDir + "/" + file.Path
		before, _, err := tx.ReadStableBounded(path, 1<<20)
		if err != nil || basementRuntimeFileMAC(key, file.Path, before) != file.MAC {
			return journal, errors.New("localevidence: runtime input changed while preparing domain migration")
		}
		after := append([]byte(nil), before...)
		switch file.Path {
		case "pocketid.env":
			after, err = migrateDomainEnvironment(before, map[string]string{"APP_URL": "https://id.home"})
		case "tinyauth.env":
			after, err = migrateDomainEnvironment(before, map[string]string{
				"TINYAUTH_APPURL": "https://auth.home",
				"TINYAUTH_OAUTH_PROVIDERS_POCKETID_AUTHURL":     "https://id.home/authorize",
				"TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL": "https://auth.home/api/oauth/callback/pocketid",
			})
		case lanDNSRecordsPath:
			address, parseErr := parseLANDNSRecords(LegacyBasementDomain, before)
			if parseErr != nil {
				return journal, parseErr
			}
			after, err = buildLANDNSRecords(MigratedBasementDomain, address)
		case originConfigPath:
			var config map[string]json.RawMessage
			if err = json.Unmarshal(before, &config); err == nil {
				var names []string
				err = json.Unmarshal(config["dnsNames"], &names)
				found := false
				for n, name := range names {
					if name == "ca.home" {
						names[n], found = "ca.lab.home", true
					}
				}
				if err == nil && !found {
					err = errors.New("localevidence: legacy step-ca domain is absent")
				}
				if err == nil {
					config["dnsNames"], err = json.Marshal(names)
				}
				if err == nil {
					after, err = json.MarshalIndent(config, "", "  ")
				}
			}
		}
		if err != nil {
			return journal, err
		}
		// Include unchanged authenticated files so recovery cannot conceal a
		// concurrent change to a service secret or CA key.
		journal.Files[path] = domainMigrationFile{Before: before, After: after}
		custody.Files[i].MAC = basementRuntimeFileMAC(key, file.Path, after)
	}
	custody.Domain, custody.Signature = MigratedBasementDomain, ""
	signing, err := basementRuntimeSigningBytes(custody)
	if err != nil {
		return journal, err
	}
	custody.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signing))
	manifest, err := json.MarshalIndent(custody, "", "  ")
	if err != nil {
		return journal, err
	}
	if err := add(basementRuntimeCustodyRelDir+"/"+basementRuntimeManifestRelPath, manifest); err != nil {
		return journal, err
	}

	// TinyAuth's issued client secret belongs to a second signed bundle. Keep
	// that exact secret and group binding while changing only its origins.
	binding, err := LoadBasementTinyAuthPocketIDBinding(root)
	if err == nil {
		envPath := tinyAuthPocketIDBindingRelDir + "/" + tinyAuthPocketIDEnvRelPath
		env, _, err := tx.ReadStableBounded(envPath, 1<<20)
		if err != nil {
			return journal, err
		}
		env, err = migrateDomainEnvironment(env, map[string]string{
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_AUTHURL":     "https://id.home/authorize",
			"TINYAUTH_OAUTH_PROVIDERS_POCKETID_REDIRECTURL": "https://auth.home/api/oauth/callback/pocketid",
		})
		if err != nil {
			return journal, err
		}
		binding.CallbackURL = TinyAuthPocketIDCallbackURL(IdentityRuntimeAddress{Domain: MigratedBasementDomain})
		binding.EnvMAC = basementRuntimeFileMAC(key, tinyAuthPocketIDEnvRelPath, env)
		binding.Signature = ""
		signing, err := tinyAuthPocketIDSigningBytes(binding)
		if err != nil {
			return journal, err
		}
		binding.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signing))
		raw, err := json.MarshalIndent(binding, "", "  ")
		if err != nil {
			return journal, err
		}
		if err = add(envPath, env); err != nil {
			return journal, err
		}
		if err = add(tinyAuthPocketIDBindingRelDir+"/"+tinyAuthPocketIDRecordRelPath, raw); err != nil {
			return journal, err
		}
	} else if !errors.Is(err, ErrTinyAuthPocketIDBindingMissing) {
		return journal, err
	}
	signing, err = json.Marshal(journal)
	if err != nil {
		return journal, err
	}
	journal.Signature, err = SignOwnerPolicyState(root, signing)
	return journal, err
}

func migrateDomainEnvironment(raw []byte, expected map[string]string) ([]byte, error) {
	lines := strings.Split(string(raw), "\n")
	seen := map[string]bool{}
	for i, line := range lines {
		name, value, ok := strings.Cut(line, "=")
		want, selected := expected[name]
		if !ok || !selected {
			continue
		}
		if seen[name] || value != want {
			return nil, fmt.Errorf("localevidence: unexpected legacy domain setting %s", name)
		}
		seen[name] = true
		lines[i] = name + "=" + strings.Replace(value, ".home", ".lab.home", 1)
	}
	for name := range expected {
		if !seen[name] {
			return nil, fmt.Errorf("localevidence: legacy domain setting %s is missing", name)
		}
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func loadBasementDomainMigration(root string, tx *confinedfs.Transaction) (basementDomainMigration, error) {
	var journal basementDomainMigration
	raw, _, err := tx.ReadStableBounded(basementDomainMigrationPath, 8<<20)
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
	if err := VerifyOwnerPolicyState(root, signing, signature); err != nil {
		return journal, err
	}
	if journal.From != LegacyBasementDomain || journal.To != MigratedBasementDomain {
		return journal, errors.New("localevidence: unsupported domain migration")
	}
	return journal, nil
}

func applyBasementDomainMigration(root string, tx *confinedfs.Transaction, view confinedfs.View, journal basementDomainMigration) error {
	paths := make([]string, 0, len(journal.Files))
	for path, file := range journal.Files {
		// Every byte must still be one of the signed generations before any
		// replacement. An unrelated mutation is never repaired by this replay.
		current, _, err := tx.ReadStableBounded(path, 1<<20)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, file.Before) && !bytes.Equal(current, file.After) {
			return errors.New("localevidence: custody changed outside the domain migration")
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, relative := range paths {
		file := journal.Files[relative]
		if bytes.Equal(file.Before, file.After) {
			continue
		}
		if err := tx.VerifyPathIdentity(); err != nil {
			return err
		}
		if _, err := view.WriteAtomic0600(relative, file.After); err != nil {
			return err
		}
	}
	if _, err := LoadBasementRuntimeCustody(root); err != nil {
		return err
	}
	if _, exists := journal.Files[tinyAuthPocketIDBindingRelDir+"/"+tinyAuthPocketIDRecordRelPath]; exists {
		_, err := LoadBasementTinyAuthPocketIDBinding(root)
		return err
	}
	return nil
}

func withDomainMigrationLock(rootPath string, apply func(*confinedfs.Transaction, confinedfs.View) (bool, error)) (bool, error) {
	root, err := confinedfs.Open(rootPath)
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
	view, err := root.View(".")
	if err != nil {
		return false, err
	}
	return apply(tx, view)
}
