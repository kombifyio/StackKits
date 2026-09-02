package backuplifecycle

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

type EmergencyRestoreInput struct {
	Archive    string
	Identities []age.Identity
	Target     string
	MaxBytes   int64
}

type EmergencyRestoreResult struct {
	APIVersion           string            `json:"apiVersion"`
	Target               string            `json:"target"`
	ContentVerified      bool              `json:"contentVerified"`
	ApplicationsVerified bool              `json:"applicationsVerified"`
	Manifest             EmergencyManifest `json:"manifest"`
}

// RestoreEmergency only stages data into a new directory. It requires the
// private age identity, not old owner custody or access to the failed server.
// Authentication or checksum failures discard the task-owned staging tree.
func RestoreEmergency(ctx context.Context, input EmergencyRestoreInput) (EmergencyRestoreResult, error) {
	if len(input.Identities) == 0 || input.MaxBytes <= 0 || strings.TrimSpace(input.Target) == "" {
		return EmergencyRestoreResult{}, errors.New("emergency restore requires identities, a new target and a positive byte limit")
	}
	archivePath, err := filepath.Abs(input.Archive)
	if err != nil {
		return EmergencyRestoreResult{}, err
	}
	root, err := confinedfs.Open(filepath.Dir(archivePath))
	if err != nil {
		return EmergencyRestoreResult{}, err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return EmergencyRestoreResult{}, err
	}
	file, err := view.Open(filepath.Base(archivePath))
	if err != nil {
		return EmergencyRestoreResult{}, err
	}
	defer file.Close()
	decrypted, err := age.Decrypt(&emergencyContextReader{ctx: ctx, reader: file}, input.Identities...)
	if err != nil {
		return EmergencyRestoreResult{}, fmt.Errorf("decrypt emergency archive: %w", err)
	}
	compressed, err := gzip.NewReader(decrypted)
	if err != nil {
		return EmergencyRestoreResult{}, err
	}
	defer compressed.Close()
	target, err := filepath.Abs(input.Target)
	if err != nil {
		return EmergencyRestoreResult{}, err
	}
	var manifest EmergencyManifest
	err = withEmergencyTarget(target, func(tx *confinedfs.Transaction, staging string) error {
		// Bound all decompressed bytes, including headers and padding, not just
		// files claimed in the manifest. This also bounds forged gzip payloads.
		limit := &io.LimitedReader{R: &emergencyContextReader{ctx: ctx, reader: compressed}, N: input.MaxBytes}
		reader := tar.NewReader(limit)
		actual := map[string]EmergencyEntry{}
		seen := map[string]bool{}
		var manifestRaw []byte
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			name := strings.TrimSuffix(header.Name, "/")
			if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\:") {
				return errors.New("unsafe emergency archive path")
			}
			key := strings.ToLower(name)
			if seen[key] || len(seen) >= maxEmergencyEntries+2 {
				return errors.New("duplicate or excessive emergency archive entries")
			}
			seen[key] = true
			if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
				return errors.New("emergency archive contains a link or special entry")
			}
			metadata := name == EmergencyManifestName || name == "RESTORE.md"
			if !metadata && !strings.HasPrefix(name, "sources/") {
				return errors.New("unknown emergency archive entry")
			}
			if metadata && (header.Typeflag == tar.TypeDir || header.Size > maxEmergencyManifestBytes) {
				return errors.New("invalid emergency archive metadata")
			}
			item := EmergencyEntry{Path: name, Directory: header.Typeflag == tar.TypeDir, Bytes: header.Size, Mode: uint32(header.Mode) & 0o777}
			if item.Directory && item.Bytes != 0 {
				return errors.New("emergency directory contains file data")
			}
			if item.Directory {
				if err := tx.MkdirAll(path.Join(staging, name), 0o700); err != nil {
					return err
				}
			} else {
				if err := tx.MkdirAll(path.Join(staging, path.Dir(name)), 0o700); err != nil {
					return err
				}
				digest := sha256.New()
				var data []byte
				if metadata {
					data, err = io.ReadAll(reader)
					if err != nil {
						return err
					}
					if name == EmergencyManifestName {
						manifestRaw = data
					}
					if err := tx.WriteFileExclusive(path.Join(staging, name), data, 0o600); err != nil {
						return err
					}
				} else {
					err := tx.WriteFileExclusiveStream(path.Join(staging, name), 0o600, func(writer io.Writer) error {
						written, err := io.Copy(io.MultiWriter(writer, digest), reader)
						if err != nil {
							return err
						}
						if written != header.Size {
							return errors.New("truncated emergency archive entry")
						}
						return nil
					})
					if err != nil {
						return err
					}
					item.SHA256 = hex.EncodeToString(digest.Sum(nil))
				}
			}
			if !metadata {
				actual[name] = item
			}
		}
		// tar EOF is not authenticated age EOF. Drain the whole stream before
		// accepting it, so a truncated final encryption tag cannot pass.
		if _, err := io.Copy(emergencyTarPadding{}, limit); err != nil {
			return err
		}
		if limit.N == 0 {
			// Reaching the limit exactly is valid. Probe the underlying stream
			// for an actual excess byte without MaxBytes+1 integer overflow.
			var extra [1]byte
			if n, err := compressed.Read(extra[:]); n != 0 {
				return errors.New("emergency restore exceeds the configured uncompressed byte limit")
			} else if err != io.EOF {
				if err != nil {
					return err
				}
				return errors.New("emergency archive did not reach its authenticated end")
			}
		}
		if _, err := io.Copy(io.Discard, decrypted); err != nil {
			return err
		}
		if !seen["restore.md"] || len(manifestRaw) == 0 {
			return errors.New("emergency archive is missing recovery metadata")
		}
		if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
			return err
		}
		if err := verifyEmergencyManifest(manifest, actual); err != nil {
			return err
		}
		return root.VerifyPathIdentity()
	})
	if err != nil {
		return EmergencyRestoreResult{}, fmt.Errorf("emergency restore: %w", err)
	}
	return EmergencyRestoreResult{APIVersion: EmergencyExportAPI, Target: target, ContentVerified: true, Manifest: manifest}, nil
}

func verifyEmergencyManifest(manifest EmergencyManifest, actual map[string]EmergencyEntry) error {
	if manifest.APIVersion != EmergencyExportAPI || manifest.Consistency != "file-copy-unverified" || manifest.ApplicationsVerified || len(manifest.Sources) == 0 || len(manifest.Entries) == 0 {
		return errors.New("invalid emergency export manifest")
	}
	included := map[string]bool{}
	for index, source := range manifest.Sources {
		if !emergencyDataClass(source.Class) || source.ArchivePath != fmt.Sprintf("sources/%04d", index) {
			return errors.New("invalid emergency source mapping")
		}
		switch source.Coverage {
		case "included":
			included[source.ArchivePath] = true
		case "manifest-only", "exclude":
			if source.Class != "large-media" {
				return errors.New("undeclared emergency source exclusion")
			}
		default:
			return errors.New("invalid emergency source coverage")
		}
	}
	for _, expected := range manifest.Entries {
		item, ok := actual[expected.Path]
		if !ok || item != expected {
			return fmt.Errorf("emergency content checksum or metadata differs: %s", expected.Path)
		}
		parts := strings.Split(expected.Path, "/")
		if len(parts) < 2 || !included[strings.Join(parts[:2], "/")] {
			return errors.New("emergency entry has no included source")
		}
		delete(actual, expected.Path)
	}
	if len(actual) != 0 {
		return errors.New("emergency archive contains unlisted data")
	}
	return nil
}

// ReadEmergencyIdentities keeps recovery secrets out of argv and JSON output.
func ReadEmergencyIdentities(filename string) ([]age.Identity, error) {
	identityPath, err := filepath.Abs(filename)
	if err != nil {
		return nil, err
	}
	if err := backupcustody.RequirePrivatePath(identityPath, false); err != nil {
		return nil, err
	}
	root, err := confinedfs.Open(filepath.Dir(identityPath))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	view, err := root.View(".")
	if err != nil {
		return nil, err
	}
	file, err := view.Open(filepath.Base(identityPath))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	identities, err := age.ParseIdentities(io.LimitReader(file, 1<<20))
	if err != nil {
		return nil, errors.New("invalid age recovery identity file")
	}
	if err := root.VerifyPathIdentity(); err != nil {
		return nil, err
	}
	return identities, nil
}

// Tar permits zero padding after its end marker. Authenticated nonzero data
// there would be unlisted content and must not be silently discarded.
type emergencyTarPadding struct{}

func (emergencyTarPadding) Write(data []byte) (int, error) {
	for _, value := range data {
		if value != 0 {
			return 0, errors.New("unlisted content follows the emergency tar end marker")
		}
	}
	return len(data), nil
}
