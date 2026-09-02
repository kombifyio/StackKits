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
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/backupcustody"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

const (
	EmergencyExportAPI        = "stackkit.backup-emergency-export/v2"
	EmergencyArchive          = "stackkit-emergency.tar.gz.age"
	EmergencyManifestName     = "stackkit-emergency-export-manifest.json"
	maxEmergencyEntries       = 100000
	maxEmergencyManifestBytes = 32 << 20
)

// EmergencySource is an explicit owner-selected local source. Media omitted
// by policy remains in the manifest so missing bytes cannot look protected.
type EmergencySource struct {
	Path        string `json:"path"`
	Class       string `json:"class"`
	ArchivePath string `json:"archivePath"`
	Coverage    string `json:"coverage"`
}

type EmergencyEntry struct {
	Path      string `json:"path"`
	Directory bool   `json:"directory,omitempty"`
	Bytes     int64  `json:"bytes"`
	SHA256    string `json:"sha256,omitempty"`
	Mode      uint32 `json:"mode"`
}

// EmergencyManifest describes bytes actually archived, not a requested backup
// policy. Copying live files is never an application consistency/restore proof.
type EmergencyManifest struct {
	APIVersion           string            `json:"apiVersion"`
	CreatedAt            time.Time         `json:"createdAt"`
	Consistency          string            `json:"consistency"`
	LargeMediaMode       string            `json:"largeMediaMode"`
	Sources              []EmergencySource `json:"sources"`
	Entries              []EmergencyEntry  `json:"entries"`
	ApplicationsVerified bool              `json:"applicationsVerified"`
}

type EmergencyExportInput struct {
	Target         string
	Sources        []EmergencySource
	Recipients     []age.Recipient
	LargeMediaMode string
}

type EmergencyExportResult struct {
	APIVersion    string            `json:"apiVersion"`
	Archive       string            `json:"archive"`
	ArchiveSHA256 string            `json:"archiveSHA256"`
	Manifest      EmergencyManifest `json:"manifest"`
}

// ExportEmergency writes encrypted tar/gzip bytes through the existing local
// backup owner. No Kopia repository, Docker daemon or hosted account is needed.
// Only a complete export is installed at Target; existing targets are preserved.
func ExportEmergency(ctx context.Context, input EmergencyExportInput) (EmergencyExportResult, error) {
	if len(input.Recipients) == 0 || len(input.Sources) == 0 {
		return EmergencyExportResult{}, errors.New("emergency export requires an age recipient and explicit sources")
	}
	if input.LargeMediaMode == "" {
		input.LargeMediaMode = "manifest-only"
	}
	if input.LargeMediaMode != "manifest-only" && input.LargeMediaMode != "include" && input.LargeMediaMode != "exclude" {
		return EmergencyExportResult{}, errors.New("invalid emergency export media policy")
	}
	target, err := filepath.Abs(input.Target)
	if err != nil || strings.TrimSpace(input.Target) == "" {
		return EmergencyExportResult{}, errors.New("emergency export target is required")
	}
	manifest := EmergencyManifest{APIVersion: EmergencyExportAPI, CreatedAt: time.Now().UTC(),
		Consistency: "file-copy-unverified", LargeMediaMode: input.LargeMediaMode,
		Sources: append([]EmergencySource(nil), input.Sources...), Entries: []EmergencyEntry{}}
	for i := range manifest.Sources {
		source := &manifest.Sources[i]
		if !emergencyDataClass(source.Class) || strings.TrimSpace(source.Path) == "" {
			return EmergencyExportResult{}, errors.New("emergency source requires a supported data class and path")
		}
		source.Path, err = filepath.Abs(source.Path)
		if err != nil {
			return EmergencyExportResult{}, err
		}
		if pathWithin(source.Path, target) {
			return EmergencyExportResult{}, errors.New("emergency export target must be outside every source")
		}
		for j := 0; j < i; j++ {
			if pathWithin(source.Path, manifest.Sources[j].Path) || pathWithin(manifest.Sources[j].Path, source.Path) {
				return EmergencyExportResult{}, errors.New("emergency export sources overlap")
			}
		}
		source.ArchivePath = fmt.Sprintf("sources/%04d", i)
		source.Coverage = "included"
		if source.Class == "large-media" && input.LargeMediaMode != "include" {
			source.Coverage = input.LargeMediaMode
		}
	}
	var result EmergencyExportResult
	err = withEmergencyTarget(target, func(tx *confinedfs.Transaction, staging string) error {
		digest := sha256.New()
		err := tx.WriteFileExclusiveStream(path.Join(staging, EmergencyArchive), 0o600, func(output io.Writer) error {
			encrypted, err := age.Encrypt(io.MultiWriter(output, digest), input.Recipients...)
			if err != nil {
				return err
			}
			compressed := gzip.NewWriter(encrypted)
			archive := tar.NewWriter(compressed)
			for _, source := range manifest.Sources {
				if source.Coverage != "included" {
					continue
				}
				if err := archiveEmergencySource(ctx, archive, source, &manifest); err != nil {
					return err
				}
			}
			if len(manifest.Entries) == 0 {
				return errors.New("emergency export has no included sources")
			}
			raw, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return err
			}
			if len(raw) > maxEmergencyManifestBytes {
				return errors.New("emergency manifest is too large; split the source set")
			}
			if err := writeEmergencyTarBytes(archive, EmergencyManifestName, raw); err != nil {
				return err
			}
			if err := writeEmergencyTarBytes(archive, "RESTORE.md", []byte(EmergencyRestoreRunbook)); err != nil {
				return err
			}
			if err := archive.Close(); err != nil {
				return err
			}
			if err := compressed.Close(); err != nil {
				return err
			}
			return encrypted.Close()
		})
		if err != nil {
			return err
		}
		result = EmergencyExportResult{APIVersion: EmergencyExportAPI, Archive: filepath.Join(target, EmergencyArchive),
			ArchiveSHA256: hex.EncodeToString(digest.Sum(nil)), Manifest: manifest}
		receipt, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		if err := tx.WriteFileExclusive(path.Join(staging, EmergencyManifestName), receipt, 0o600); err != nil {
			return err
		}
		return tx.WriteFileExclusive(path.Join(staging, "RESTORE.md"), []byte(EmergencyRestoreRunbook), 0o600)
	})
	if err != nil {
		return EmergencyExportResult{}, fmt.Errorf("emergency export: %w", err)
	}
	return result, nil
}

func emergencyDataClass(class string) bool {
	switch class {
	case "config", "secrets", "platform-state", "database", "user-content", "documents", "photos", "large-media", "telemetry-timeseries", "serverless-config", "cache-generated":
		return true
	}
	return false
}

func pathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}

func withEmergencyTarget(target string, write func(*confinedfs.Transaction, string) error) (returnErr error) {
	parent, err := confinedfs.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer parent.Close()
	tx, err := parent.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	name := filepath.Base(target)
	if exists, _, err := tx.Exists(name); err != nil || exists {
		if err != nil {
			return err
		}
		return errors.New("target already exists; choose a new export or restore directory")
	}
	staging, err := tx.CreatePrivateDirectory(".stackkit-emergency-")
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			returnErr = errors.Join(returnErr, tx.RemoveTree(staging))
		}
	}()
	if err := backupcustody.ProtectPrivatePath(filepath.Join(tx.Name(), staging), true); err != nil {
		return err
	}
	if err := tx.VerifyPathIdentity(); err != nil {
		return err
	}
	if err := write(tx, staging); err != nil {
		return err
	}
	installed, err = tx.Rename(staging, name)
	return err
}

func archiveEmergencySource(ctx context.Context, archive *tar.Writer, source EmergencySource, manifest *EmergencyManifest) error {
	// Hold the source's parent so a file and a directory use the same strict
	// no-symlink traversal; links, sockets and devices fail rather than vanish.
	root, err := confinedfs.Open(filepath.Dir(source.Path))
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	name := filepath.Base(source.Path)
	entries, err := tx.Walk(name)
	if err != nil {
		return err
	}
	if len(entries) > maxEmergencyEntries-len(manifest.Entries) {
		return errors.New("too many emergency export entries; split the source set")
	}
	view, err := root.View(".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		suffix := strings.TrimPrefix(strings.TrimPrefix(entry.Path, name), "/")
		archived := path.Join(source.ArchivePath, suffix)
		item := EmergencyEntry{Path: archived, Directory: entry.Info.IsDir(), Mode: uint32(entry.Info.Mode().Perm())}
		header := &tar.Header{Name: archived, Mode: int64(item.Mode), ModTime: entry.Info.ModTime(), Typeflag: tar.TypeDir}
		if !item.Directory {
			header.Typeflag = tar.TypeReg
			header.Size = entry.Info.Size()
			item.Bytes = header.Size
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if !item.Directory {
			file, err := view.Open(entry.Path)
			if err != nil {
				return err
			}
			digest := sha256.New()
			_, copyErr := io.Copy(io.MultiWriter(archive, digest), &emergencyContextReader{ctx: ctx, reader: file})
			after, statErr := file.Stat()
			closeErr := file.Close()
			if err := errors.Join(copyErr, statErr, closeErr); err != nil {
				return err
			}
			current, err := view.Lstat(entry.Path)
			if err != nil {
				return err
			}
			if !os.SameFile(entry.Info, after) || !os.SameFile(after, current) || entry.Info.Size() != after.Size() || !entry.Info.ModTime().Equal(after.ModTime()) {
				return fmt.Errorf("source changed during export: %s", entry.Path)
			}
			item.SHA256 = hex.EncodeToString(digest.Sum(nil))
		}
		manifest.Entries = append(manifest.Entries, item)
	}
	return root.VerifyPathIdentity()
}

type emergencyContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *emergencyContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func writeEmergencyTarBytes(archive *tar.Writer, name string, data []byte) error {
	if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := archive.Write(data)
	return err
}

const EmergencyRestoreRunbook = `# Emergency restore

Keep the age identity separately from this archive. Anyone with that identity
can decrypt the included secrets and data. No Kopia repository, original host,
Kombify account or hosted control plane is required to decrypt these bytes.

1. On a replacement machine, keep the archive and recovery identity locally.
2. Run: stackkit backup emergency-restore --archive stackkit-emergency.tar.gz.age --identity-file recovery-key.txt --target NEW-STAGING-DIRECTORY
3. The command authenticates the complete encrypted stream and verifies every
   file checksum before publishing the staging directory. It never writes to
   the original paths or overwrites an existing directory.
4. Read the manifest's sources mapping. Omitted media has no bytes in this
   archive and must be recovered from its separate copy. Do not interpret a
   source class as evidence that all application data was selected.
5. Recreate the recorded application versions and restore config and secrets
   first. Import consistent database dumps with their database-native tools;
   raw database files from a running server have unverified consistency.
6. Restore user files and permissions for the replacement service identities.
   The safe staging directory uses private permissions; it does not activate
   services, original user IDs, device nodes, symlinks or executable privileges.
7. Check native login, file/photo content, application health and client access.
   Successful decryption and checksums prove bytes, not a usable application.

Commodity fallback: age --decrypt -i recovery-key.txt stackkit-emergency.tar.gz.age > emergency.tar.gz
Then inspect with tar -tzf emergency.tar.gz and extract only into a new isolated
directory. Delete the decrypted temporary archive after recovery. The embedded
manifest records SHA-256 checksums and the original-to-staged source mapping.
`
