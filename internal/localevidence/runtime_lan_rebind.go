package localevidence

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const (
	basementLANRebindJournalPath = ".stackkit/custody/basement-lan-rebind.json"
	basementLANRebindBackupDir   = ".stackkit/custody/backups"
)

type lanRebindJournal struct {
	From      string                         `json:"from"`
	To        string                         `json:"to"`
	Files     map[string]domainMigrationFile `json:"files"`
	Signature OwnerPolicyStateSignature      `json:"signature"`
}

// RebindBasementLANAddress re-issues the LAN resolver record of an established
// Basement runtime custody for a new site address, for a server that was moved
// to another network. Only the A record, its file MAC and the signed manifest
// change: users, secrets, certificate authorities and volumes stay as issued.
//
// The previous record and manifest are kept below .stackkit/custody/backups,
// and a signed private journal makes a partial write replayable, exactly as
// MigrateBasementRuntimeDomain does for the domain. The caller holds the
// lifecycle mutation session. changed is false when the record already holds
// address.
func RebindBasementLANAddress(workspaceRoot string, address netip.Addr) (previous netip.Addr, changed bool, err error) {
	address = address.Unmap()
	_, err = withDomainMigrationLock(workspaceRoot, func(tx *confinedfs.Transaction, view confinedfs.View) (bool, error) {
		journal, loadErr := loadBasementLANRebind(workspaceRoot, tx)
		if errors.Is(loadErr, os.ErrNotExist) {
			var prepared bool
			journal, previous, prepared, loadErr = prepareBasementLANRebind(workspaceRoot, tx, view, address)
			if loadErr != nil {
				return false, loadErr
			}
			if !prepared {
				return false, nil
			}
			raw, marshalErr := json.Marshal(journal)
			if marshalErr != nil {
				return false, marshalErr
			}
			if _, writeErr := view.WriteAtomic0600(basementLANRebindJournalPath, raw); writeErr != nil {
				return false, writeErr
			}
			if _, syncErr := tx.SyncDirectory(".stackkit/custody"); syncErr != nil {
				return false, syncErr
			}
		} else if loadErr != nil {
			return false, loadErr
		}
		changed = true
		if applyErr := applyBasementLANRebind(workspaceRoot, tx, view, journal); applyErr != nil {
			return false, applyErr
		}
		info, statErr := tx.Lstat(basementLANRebindJournalPath)
		if statErr != nil {
			return false, statErr
		}
		if removeErr := tx.RemoveRegularFile(basementLANRebindJournalPath, info); removeErr != nil {
			return false, removeErr
		}
		_, syncErr := tx.SyncDirectory(".stackkit/custody")
		return false, syncErr
	})
	return previous, changed, err
}

func prepareBasementLANRebind(root string, tx *confinedfs.Transaction, view confinedfs.View, address netip.Addr) (lanRebindJournal, netip.Addr, bool, error) {
	journal := lanRebindJournal{Files: map[string]domainMigrationFile{}}
	custody, err := LoadBasementRuntimeCustody(root)
	if err != nil {
		return journal, netip.Addr{}, false, err
	}
	key, err := LoadOwnerKey(root)
	if err != nil {
		return journal, netip.Addr{}, false, err
	}
	recordPath := basementRuntimeCustodyRelDir + "/" + lanDNSRecordsPath
	before, _, err := tx.ReadStableBounded(recordPath, 1<<20)
	if err != nil {
		return journal, netip.Addr{}, false, err
	}
	previous, err := parseLANDNSRecords(custody.Domain, before)
	if err != nil {
		return journal, netip.Addr{}, false, err
	}
	if previous == address {
		return journal, previous, false, nil
	}
	after, err := buildLANDNSRecords(custody.Domain, address)
	if err != nil {
		return journal, previous, false, err
	}
	macFound := false
	for i, file := range custody.Files {
		if file.Path != lanDNSRecordsPath {
			continue
		}
		if basementRuntimeFileMAC(key, file.Path, before) != file.MAC {
			return journal, previous, false, errors.New("localevidence: LAN resolver input changed while preparing the rebind")
		}
		custody.Files[i].MAC = basementRuntimeFileMAC(key, file.Path, after)
		macFound = true
	}
	if !macFound {
		return journal, previous, false, errors.New("localevidence: LAN resolver input is absent from verified custody")
	}
	manifestPath := basementRuntimeCustodyRelDir + "/" + basementRuntimeManifestRelPath
	manifestBefore, _, err := tx.ReadStableBounded(manifestPath, 1<<20)
	if err != nil {
		return journal, previous, false, err
	}
	custody.Signature = ""
	signing, err := basementRuntimeSigningBytes(custody)
	if err != nil {
		return journal, previous, false, err
	}
	custody.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signing))
	manifestAfter, err := json.MarshalIndent(custody, "", "  ")
	if err != nil {
		return journal, previous, false, err
	}
	journal.From, journal.To = previous.String(), address.String()
	journal.Files[recordPath] = domainMigrationFile{Before: before, After: after}
	journal.Files[manifestPath] = domainMigrationFile{Before: manifestBefore, After: manifestAfter}

	// Keep the previous generation for rollback before anything is replaced.
	backup := basementLANRebindBackupDir + "/lan-rebind-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	if err := tx.MkdirAll(backup, 0o700); err != nil {
		return journal, previous, false, err
	}
	if _, err := view.WriteAtomic0600(backup+"/a-records.conf", before); err != nil {
		return journal, previous, false, err
	}
	if _, err := view.WriteAtomic0600(backup+"/manifest.json", manifestBefore); err != nil {
		return journal, previous, false, err
	}

	signingJournal, err := json.Marshal(journal)
	if err != nil {
		return journal, previous, false, err
	}
	journal.Signature, err = SignOwnerPolicyState(root, signingJournal)
	return journal, previous, err == nil, err
}

func loadBasementLANRebind(root string, tx *confinedfs.Transaction) (lanRebindJournal, error) {
	var journal lanRebindJournal
	raw, _, err := tx.ReadStableBounded(basementLANRebindJournalPath, 8<<20)
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
	return journal, nil
}

func applyBasementLANRebind(root string, tx *confinedfs.Transaction, view confinedfs.View, journal lanRebindJournal) error {
	paths := make([]string, 0, len(journal.Files))
	for path, file := range journal.Files {
		current, _, err := tx.ReadStableBounded(path, 1<<20)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, file.Before) && !bytes.Equal(current, file.After) {
			return errors.New("localevidence: custody changed outside the LAN address rebind")
		}
		paths = append(paths, path)
	}
	// Record first, manifest last: a crash between them replays from the journal.
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
		return fmt.Errorf("localevidence: custody does not verify after the LAN address rebind: %w", err)
	}
	return nil
}
