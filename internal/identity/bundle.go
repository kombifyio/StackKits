package identity

// The recovery set (homelab identity target §9, §14.9): the Authority's whole
// identity custody as one encrypted file, the break-glass bundle.
//
// Contents: the Owner custody and key, the step-ca root key, the runtime
// custody (Pocket ID encryption and admin keys, step-ca intermediate key and
// password, TinyAuth inputs) with its signed manifest, the TinyAuth/Pocket ID
// binding, the signed owner runtime binding, the device registry, the device
// credential issuer key, the Owner-signed Pocket ID pin, the recovery state,
// and the Pocket ID and step-ca database volumes.
//
// Format: a gzip-compressed tar stream, encrypted with age to the Owner's
// recovery recipient (ML-KEM-768 + X25519 hybrid). The last entries are
// manifest.json (paths, sizes, SHA-256 digests, the identity origin) and
// manifest.mac, an HMAC-SHA-256 of the manifest under a key derived from the
// recovery key. The recipient is public, so age alone would let anyone who
// knows it forge a set; the MAC binds the set to the Authority that holds the
// derived key and to the Owner who holds the recovery key. The Authority
// never holds the recovery key and cannot decrypt a set. Stock `age -d` with
// the recovery key opens a set without StackKits.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	RecoverySetSchema = "stackkit.identity-recovery-set/v1"

	// The Docker volumes of the identity runtime, by compose volume name.
	RecoveryVolumePocketID = "pocketid-data"
	RecoveryVolumeStepCA   = "step-ca-db"

	// DefaultRecoveryBundleDir holds the Authority's local copy of its latest
	// set, break-glass-<node>.age. It is safe to copy anywhere.
	DefaultRecoveryBundleDir = "/var/lib/stackkit/recovery"

	recoverySetManifestEntry = "manifest.json"
	recoverySetMACEntry      = "manifest.mac"
	recoverySetCustodyPrefix = "custody/"
	recoverySetVolumePrefix  = "volumes/"
	recoverySetMACDomain     = "stackkit.identity-recovery-set/v1 manifest authenticity"
	recoverySetStageDir      = ".stackkit"
	// Bounds for a set read back: decompressed bytes, entries and manifest.
	recoverySetMaxBytes         int64 = 32 << 30
	recoverySetMaxEntries             = 1 << 17
	recoverySetMaxManifestBytes int64 = 32 << 20
	recoverySetChunk                  = 64 << 10
)

var (
	// ErrRecoverySetRefused is the one answer to a set that does not open
	// with the given key, fails its MAC or digests, or is malformed.
	ErrRecoverySetRefused = errors.New("identity: the recovery set does not open with this recovery key or was altered")
	// ErrRecoveryRestoreConflict refuses a restore onto a workspace that
	// already holds another Owner or another identity origin.
	ErrRecoveryRestoreConflict = errors.New("identity: this workspace already holds another Owner or identity origin; restore onto a fresh Authority")

	// recoverySetCustodyFiles are single custody files; required ones must exist.
	recoverySetCustodyFiles = []struct {
		path     string
		required bool
	}{
		{".stackkit/custody/owner.json", true},
		{".stackkit/custody/owner-key.json", true},
		{".stackkit/custody/step-ca/root-key.json", true},
		{recoveryStatePath, true},
		{".stackkit/evidence/owner-runtime-binding.json", false},
		{homeDeviceRegistryPath, false},
		{homeDeviceIssuerPath, false},
		{homeVerifierStatePath, false},
	}
	// recoverySetCustodyTrees are whole custody directories. Exactly one of
	// the two runtime custodies exists on an Authority.
	recoverySetCustodyTrees = []string{
		".stackkit/custody/basement-runtime",
		".stackkit/custody/cloud-runtime",
		".stackkit/custody/tinyauth-pocketid",
	}
)

// RecoverySetManifest describes one set. It holds no secret.
type RecoverySetManifest struct {
	Schema         string              `json:"schema"`
	CreatedAt      time.Time           `json:"createdAt"`
	OwnerRef       string              `json:"ownerRef"`
	SiteRef        string              `json:"siteRef"`
	NodeRef        string              `json:"nodeRef"`
	IdentityOrigin string              `json:"identityOrigin"`
	Recipient      string              `json:"recipient"`
	Files          []RecoverySetEntry  `json:"files"`
	Volumes        []RecoverySetVolume `json:"volumes"`
}

// RecoverySetEntry is one file or directory of the set.
type RecoverySetEntry struct {
	Path   string `json:"path"`
	Dir    bool   `json:"dir,omitempty"`
	Mode   uint32 `json:"mode"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// RecoverySetVolume is one Docker volume of the identity runtime.
type RecoverySetVolume struct {
	Name         string             `json:"name"`
	DockerVolume string             `json:"dockerVolume"`
	Entries      []RecoverySetEntry `json:"entries"`
}

// RecoveryVolumeSource is one identity runtime volume read from its host
// directory while its writer is paused.
type RecoveryVolumeSource struct {
	Name         string
	DockerVolume string
	Dir          string
}

// RecoveryBundleName is the file name of a node's set.
func RecoveryBundleName(nodeRef string) string {
	return "break-glass-" + nodeRef + ".age"
}

// WriteRecoverySet streams the Authority's identity custody and the given
// runtime volumes as one encrypted set to out. The Pocket ID volume is
// required; the step-ca database exists on kits that run step-ca.
func WriteRecoverySet(root string, volumes []RecoveryVolumeSource, out io.Writer, now time.Time) (RecoverySetManifest, error) {
	state, err := loadRecoveryState(root)
	if errors.Is(err, fs.ErrNotExist) {
		return RecoverySetManifest{}, ErrRecoveryNotConfigured
	}
	if err != nil {
		return RecoverySetManifest{}, err
	}
	recipient, err := age.ParseHybridRecipient(state.Recipient)
	if err != nil || len(state.SetAuthKey) != sha256.Size {
		return RecoverySetManifest{}, errors.New("identity: unreadable recovery state")
	}
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return RecoverySetManifest{}, err
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(root)
	if err != nil {
		return RecoverySetManifest{}, err
	}
	if err := validateRecoveryVolumeSources(volumes); err != nil {
		return RecoverySetManifest{}, err
	}
	manifest := RecoverySetManifest{
		Schema: RecoverySetSchema, CreatedAt: now.UTC().Truncate(time.Second), OwnerRef: owner.OwnerRef,
		SiteRef: owner.Binding.SiteRef, NodeRef: owner.Binding.NodeRef, IdentityOrigin: address.PocketIDOrigin(),
		Recipient: state.Recipient, Files: []RecoverySetEntry{}, Volumes: []RecoverySetVolume{},
	}
	encrypted, err := age.Encrypt(out, recipient)
	if err != nil {
		return RecoverySetManifest{}, err
	}
	compressed, err := gzip.NewWriterLevel(encrypted, gzip.BestSpeed)
	if err != nil {
		return RecoverySetManifest{}, err
	}
	archive := tar.NewWriter(compressed)
	absolute, err := filepath.Abs(root)
	if err != nil {
		return RecoverySetManifest{}, err
	}
	for _, file := range recoverySetCustodyFiles {
		entry, err := addRecoverySetPath(archive, filepath.Join(absolute, filepath.FromSlash(file.path)), recoverySetCustodyPrefix+file.path, file.path, false)
		if errors.Is(err, fs.ErrNotExist) && !file.required {
			continue
		}
		if err != nil {
			return RecoverySetManifest{}, fmt.Errorf("identity: recovery set %s: %w", file.path, err)
		}
		manifest.Files = append(manifest.Files, entry...)
	}
	runtimes := 0
	for _, tree := range recoverySetCustodyTrees {
		entries, err := addRecoverySetPath(archive, filepath.Join(absolute, filepath.FromSlash(tree)), recoverySetCustodyPrefix+tree, tree, true)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return RecoverySetManifest{}, fmt.Errorf("identity: recovery set %s: %w", tree, err)
		}
		if strings.HasSuffix(tree, "-runtime") {
			runtimes++
		}
		manifest.Files = append(manifest.Files, entries...)
	}
	if runtimes != 1 {
		return RecoverySetManifest{}, errors.New("identity: the recovery set needs exactly one identity runtime custody")
	}
	for _, volume := range volumes {
		entries, err := addRecoverySetPath(archive, volume.Dir, recoverySetVolumePrefix+volume.Name, "", true)
		if err != nil {
			return RecoverySetManifest{}, fmt.Errorf("identity: recovery set volume %s: %w", volume.Name, err)
		}
		manifest.Volumes = append(manifest.Volumes, RecoverySetVolume{Name: volume.Name, DockerVolume: volume.DockerVolume, Entries: entries})
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return RecoverySetManifest{}, err
	}
	mac := recoverySetManifestMAC(state.SetAuthKey, raw)
	for _, item := range []struct {
		name string
		body []byte
	}{{recoverySetManifestEntry, raw}, {recoverySetMACEntry, []byte(hex.EncodeToString(mac))}} {
		header := &tar.Header{Name: item.name, Mode: 0o600, Size: int64(len(item.body)), Typeflag: tar.TypeReg, ModTime: manifest.CreatedAt, Format: tar.FormatPAX}
		if err := archive.WriteHeader(header); err != nil {
			return RecoverySetManifest{}, err
		}
		if _, err := archive.Write(item.body); err != nil {
			return RecoverySetManifest{}, err
		}
	}
	if err := archive.Close(); err != nil {
		return RecoverySetManifest{}, err
	}
	if err := compressed.Close(); err != nil {
		return RecoverySetManifest{}, err
	}
	if err := encrypted.Close(); err != nil {
		return RecoverySetManifest{}, err
	}
	// The Owner setup re-evaluates the recovery step against the current
	// identity origin: after a domain move only a set of the new origin counts.
	if err := updateRecoveryState(root, false, func(state *recoveryState, _ bool) error {
		state.LastSet = &recoverySetRecord{WrittenAt: manifest.CreatedAt, IdentityOrigin: manifest.IdentityOrigin}
		return nil
	}); err != nil {
		return RecoverySetManifest{}, fmt.Errorf("identity: record the written recovery set: %w", err)
	}
	return manifest, nil
}

// RecoverySetRestore is an opened, fully verified set staged below the
// workspace. The caller places its volumes into fresh Docker volumes, then
// installs its custody. Close removes the stage.
type RecoverySetRestore struct {
	Manifest RecoverySetManifest
	root     string
	stage    string
}

// OpenRecoverySet opens a set with the Owner's recovery key and verifies it
// completely (age decryption, manifest MAC, every digest, the staged Owner
// custody and identity origin) without changing the workspace. It refuses a
// workspace that holds another Owner or another identity origin.
func OpenRecoverySet(root string, encrypted io.Reader, recoveryKey string) (*RecoverySetRestore, error) {
	identity, err := age.ParseHybridIdentity(strings.TrimSpace(recoveryKey))
	if err != nil {
		return nil, ErrRecoverySetRefused
	}
	authKey, err := recoverySetAuthKey(identity)
	if err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(absolute, recoverySetStageDir), 0o700); err != nil {
		return nil, err
	}
	// A stage left by an interrupted restore holds plaintext custody.
	if leftovers, err := filepath.Glob(filepath.Join(absolute, recoverySetStageDir, ".recovery-restore-*")); err == nil {
		for _, leftover := range leftovers {
			_ = os.RemoveAll(leftover)
		}
	}
	stage, err := os.MkdirTemp(filepath.Join(absolute, recoverySetStageDir), ".recovery-restore-*")
	if err != nil {
		return nil, err
	}
	restore := &RecoverySetRestore{root: absolute, stage: stage}
	opened := false
	defer func() {
		if !opened {
			_ = os.RemoveAll(stage)
		}
	}()
	manifest, err := extractRecoverySet(encrypted, identity, authKey, stage)
	if err != nil {
		return nil, err
	}
	restore.Manifest = manifest
	staged := filepath.Join(stage, "workspace")
	// Custody keeps the modes it had: the runtime custody holds files that
	// service containers read through bind mounts.
	for _, entry := range manifest.Files {
		if err := applyRecoveryOwnership(filepath.Join(staged, filepath.FromSlash(entry.Path)), entry); err != nil {
			return nil, err
		}
	}
	if err := requireRecoverySetCustody(staged, manifest); err != nil {
		return nil, err
	}
	if err := requireRestorableWorkspace(absolute, manifest); err != nil {
		return nil, err
	}
	opened = true
	return restore, nil
}

// InstallCustody installs the set's custody into the workspace, keeping the
// identity origin: the restored runtime custody carries the same Pocket ID
// host, so passkeys registered for it keep working. The restored recovery
// state ends any pending device recovery and marks the code used
// (ResetRecoveryAfterRestore). Installing the same set again is allowed.
func (r *RecoverySetRestore) InstallCustody() error {
	if err := requireRestorableWorkspace(r.root, r.Manifest); err != nil {
		return err
	}
	if err := installRecoverySetCustody(r.root, filepath.Join(r.stage, "workspace"), r.Manifest); err != nil {
		return err
	}
	if err := requireRecoverySetCustody(r.root, r.Manifest); err != nil {
		return err
	}
	invalidateHomeVerifier(r.root)
	return ResetRecoveryAfterRestore(r.root, time.Now())
}

// PlaceVolume copies one staged volume into an empty target directory (the
// mountpoint of a freshly created Docker volume), keeping modes, owners when
// running as root, and sparse regions.
func (r *RecoverySetRestore) PlaceVolume(name, target string) error {
	index := slices.IndexFunc(r.Manifest.Volumes, func(volume RecoverySetVolume) bool { return volume.Name == name })
	if index < 0 {
		return fmt.Errorf("identity: the recovery set has no volume %s", name)
	}
	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("identity: volume %s target is not empty", name)
	}
	source := filepath.Join(r.stage, "volumes", name)
	for _, entry := range r.Manifest.Volumes[index].Entries {
		from, to := filepath.Join(source, filepath.FromSlash(entry.Path)), filepath.Join(target, filepath.FromSlash(entry.Path))
		if entry.Path == "." {
			to = target
		}
		if entry.Dir {
			if err := os.MkdirAll(to, 0o700); err != nil {
				return err
			}
		} else if err := copyRecoveryFile(from, to); err != nil {
			return err
		}
		if err := applyRecoveryOwnership(to, entry); err != nil {
			return err
		}
	}
	return nil
}

// Close removes the staged volumes.
func (r *RecoverySetRestore) Close() error {
	if r == nil || r.stage == "" {
		return nil
	}
	return os.RemoveAll(r.stage)
}

func validateRecoveryVolumeSources(volumes []RecoveryVolumeSource) error {
	seen := map[string]bool{}
	for _, volume := range volumes {
		if volume.Name != RecoveryVolumePocketID && volume.Name != RecoveryVolumeStepCA || seen[volume.Name] || volume.Dir == "" ||
			!validRecoveryDockerVolume(volume.DockerVolume, volume.Name) {
			return fmt.Errorf("identity: invalid recovery volume %q", volume.Name)
		}
		seen[volume.Name] = true
	}
	if !seen[RecoveryVolumePocketID] {
		return errors.New("identity: the recovery set needs the Pocket ID database volume")
	}
	return nil
}

// validRecoveryDockerVolume admits <compose project>_<name> only.
func validRecoveryDockerVolume(docker, name string) bool {
	project, ok := strings.CutSuffix(docker, "_"+name)
	if !ok || project == "" || len(project) > 63 {
		return false
	}
	for _, r := range project {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// addRecoverySetPath writes a regular file, or with tree a whole directory,
// to the archive under name and returns its manifest entries. Entry paths are
// relative to the tree (or the file's custody path). Symlinks, devices and
// sockets fail the set rather than being followed or skipped.
func addRecoverySetPath(archive *tar.Writer, source, name, custodyPath string, tree bool) ([]RecoverySetEntry, error) {
	info, err := os.Lstat(source)
	if err != nil {
		return nil, err
	}
	if !tree {
		if !info.Mode().IsRegular() {
			return nil, errors.New("not a regular file")
		}
		entry, err := addRecoverySetEntry(archive, source, name, custodyPath, info)
		return []RecoverySetEntry{entry}, err
	}
	if !info.IsDir() {
		return nil, errors.New("not a directory")
	}
	var entries []RecoverySetEntry
	err = filepath.WalkDir(source, func(current string, item fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a plain file or directory", current)
		}
		relative, err := filepath.Rel(source, current)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		entryPath := relative
		if custodyPath != "" {
			entryPath = path.Join(custodyPath, relative)
		}
		entry, err := addRecoverySetEntry(archive, current, path.Join(name, relative), entryPath, info)
		entries = append(entries, entry)
		return err
	})
	return entries, err
}

func addRecoverySetEntry(archive *tar.Writer, source, name, entryPath string, info fs.FileInfo) (RecoverySetEntry, error) {
	header, err := tar.FileInfoHeader(info, "")
	if err != nil {
		return RecoverySetEntry{}, err
	}
	header.Name, header.Format, header.Uname, header.Gname = name, tar.FormatPAX, "", ""
	header.AccessTime, header.ChangeTime = time.Time{}, time.Time{}
	entry := RecoverySetEntry{Path: entryPath, Dir: info.IsDir(), Mode: uint32(info.Mode().Perm()), UID: header.Uid, GID: header.Gid}
	if info.IsDir() {
		header.Name += "/"
		return entry, archive.WriteHeader(header)
	}
	file, err := os.Open(source) //nolint:gosec // fixed custody paths and the identity runtime volumes
	if err != nil {
		return RecoverySetEntry{}, err
	}
	defer func() { _ = file.Close() }()
	if err := archive.WriteHeader(header); err != nil {
		return RecoverySetEntry{}, err
	}
	digest := sha256.New()
	written, err := io.Copy(archive, io.TeeReader(io.LimitReader(file, header.Size), digest))
	if err != nil {
		return RecoverySetEntry{}, err
	}
	if written != header.Size {
		return RecoverySetEntry{}, fmt.Errorf("%s changed while it was read", name)
	}
	entry.Size, entry.SHA256 = written, hex.EncodeToString(digest.Sum(nil))
	return entry, nil
}

// extractRecoverySet decrypts and unpacks the set into stage: custody below
// stage/workspace, volumes below stage/volumes/<name>. It verifies the MAC of
// the manifest and that the staged entries are exactly the manifest's.
func extractRecoverySet(encrypted io.Reader, identity *age.HybridIdentity, authKey []byte, stage string) (RecoverySetManifest, error) {
	refuse := func() (RecoverySetManifest, error) { return RecoverySetManifest{}, ErrRecoverySetRefused }
	plain, err := age.Decrypt(encrypted, identity)
	if err != nil {
		return refuse()
	}
	decompressed, err := gzip.NewReader(plain)
	if err != nil {
		return refuse()
	}
	archive := tar.NewReader(&boundedRecoveryReader{reader: decompressed, remaining: recoverySetMaxBytes})
	staged := map[string]RecoverySetEntry{}
	var manifestRaw, macRaw []byte
	var declared int64
	for count := 0; ; count++ {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || count >= recoverySetMaxEntries || macRaw != nil || !plainRecoverySetHeader(header) {
			return refuse()
		}
		// Declared sizes count too: a sparse entry's holes never pass
		// through the bounded reader.
		if declared += header.Size; header.Size < 0 || declared > recoverySetMaxBytes {
			return refuse()
		}
		switch header.Name {
		case recoverySetManifestEntry, recoverySetMACEntry:
			if header.Typeflag != tar.TypeReg || header.Size > recoverySetMaxManifestBytes || header.Name == recoverySetMACEntry && manifestRaw == nil {
				return refuse()
			}
			body, err := io.ReadAll(io.LimitReader(archive, recoverySetMaxManifestBytes))
			if err != nil {
				return refuse()
			}
			if header.Name == recoverySetManifestEntry {
				manifestRaw = body
			} else {
				macRaw = body
			}
			continue
		}
		if manifestRaw != nil {
			return refuse()
		}
		key, target, ok := recoverySetStagePath(stage, header.Name)
		if _, duplicate := staged[key]; !ok || duplicate {
			return refuse()
		}
		entry, err := stageRecoverySetEntry(archive, header, target)
		if err != nil {
			return refuse()
		}
		staged[key] = entry
	}
	// Drain the age stream so a truncated or altered tail fails here.
	if _, err := io.Copy(io.Discard, plain); err != nil || manifestRaw == nil || macRaw == nil {
		return refuse()
	}
	mac, err := hex.DecodeString(string(macRaw))
	if err != nil || !hmac.Equal(mac, recoverySetManifestMAC(authKey, manifestRaw)) {
		return refuse()
	}
	var manifest RecoverySetManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestRaw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.Schema != RecoverySetSchema ||
		manifest.Recipient != identity.Recipient().String() || manifest.OwnerRef == "" || manifest.IdentityOrigin == "" {
		return refuse()
	}
	expected := map[string]RecoverySetEntry{}
	for _, entry := range manifest.Files {
		if !allowedRecoveryCustodyPath(entry.Path) {
			return refuse()
		}
		expected["custody:"+entry.Path] = entry
	}
	for _, volume := range manifest.Volumes {
		if volume.Name != RecoveryVolumePocketID && volume.Name != RecoveryVolumeStepCA || !validRecoveryDockerVolume(volume.DockerVolume, volume.Name) {
			return refuse()
		}
		for _, entry := range volume.Entries {
			expected["volume:"+volume.Name+":"+entry.Path] = entry
		}
	}
	if len(expected) != len(staged) {
		return refuse()
	}
	for key, want := range expected {
		got, ok := staged[key]
		if !ok || got.Dir != want.Dir || got.Size != want.Size || got.SHA256 != want.SHA256 || got.Mode != want.Mode || got.UID != want.UID || got.GID != want.GID {
			return refuse()
		}
	}
	return manifest, nil
}

// recoverySetStagePath maps an archive name to its staging target. Custody
// entries keep their workspace path below stage/workspace; volume entries go
// below stage/volumes/<name>. Anything else, or any path that is not clean
// and relative, is refused.
func recoverySetStagePath(stage, name string) (key, target string, ok bool) {
	name = strings.TrimSuffix(name, "/")
	if name == "" || path.Clean(name) != name || path.IsAbs(name) || strings.Contains(name, "\\") || strings.HasPrefix(name, "../") || name == ".." {
		return "", "", false
	}
	if custody, found := strings.CutPrefix(name, recoverySetCustodyPrefix); found {
		if !allowedRecoveryCustodyPath(custody) {
			return "", "", false
		}
		return "custody:" + custody, filepath.Join(stage, "workspace", filepath.FromSlash(custody)), true
	}
	rest, found := strings.CutPrefix(name, recoverySetVolumePrefix)
	if !found {
		return "", "", false
	}
	volume, relative, _ := strings.Cut(rest, "/")
	if volume != RecoveryVolumePocketID && volume != RecoveryVolumeStepCA {
		return "", "", false
	}
	if relative == "" {
		relative = "."
	}
	return "volume:" + volume + ":" + relative, filepath.Join(stage, "volumes", volume, filepath.FromSlash(relative)), true
}

// plainRecoverySetHeader admits only the entries a set is written with:
// regular files and directories without sparse maps.
func plainRecoverySetHeader(header *tar.Header) bool {
	if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
		return false
	}
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse.") {
			return false
		}
	}
	return true
}

func allowedRecoveryCustodyPath(custody string) bool {
	for _, file := range recoverySetCustodyFiles {
		if custody == file.path {
			return true
		}
	}
	for _, tree := range recoverySetCustodyTrees {
		if custody == tree || strings.HasPrefix(custody, tree+"/") {
			return true
		}
	}
	return false
}

func stageRecoverySetEntry(archive io.Reader, header *tar.Header, target string) (RecoverySetEntry, error) {
	entry := RecoverySetEntry{Mode: uint32(header.Mode) & 0o777, UID: header.Uid, GID: header.Gid} // #nosec G115 -- permission bits only
	switch header.Typeflag {
	case tar.TypeDir:
		entry.Dir = true
		return entry, os.MkdirAll(target, 0o700)
	case tar.TypeReg:
	default:
		return entry, errors.New("unsupported entry")
	}
	if header.Size < 0 {
		return entry, errors.New("invalid size")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return entry, err
	}
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // confined stage path
	if err != nil {
		return entry, err
	}
	digest := sha256.New()
	written, writeErr := writeSparse(file, io.TeeReader(io.LimitReader(archive, header.Size), digest), header.Size)
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil || written != header.Size {
		return entry, errors.New("short entry")
	}
	entry.Size, entry.SHA256 = written, hex.EncodeToString(digest.Sum(nil))
	return entry, nil
}

// requireRecoverySetCustody proves that root holds the set's Owner and
// identity origin: the owner custody verifies (signature, step-ca chain,
// evidence key) and the runtime custody names the same Pocket ID origin.
func requireRecoverySetCustody(root string, manifest RecoverySetManifest) error {
	owner, err := localevidence.LoadOwnerCustody(root)
	if err != nil || owner.OwnerRef != manifest.OwnerRef || owner.Binding.SiteRef != manifest.SiteRef || owner.Binding.NodeRef != manifest.NodeRef {
		return ErrRecoverySetRefused
	}
	address, err := localevidence.LocalIdentityRuntimeAddress(root)
	if err != nil || address.PocketIDOrigin() != manifest.IdentityOrigin {
		return ErrRecoverySetRefused
	}
	return nil
}

// requireRestorableWorkspace admits a fresh workspace or one that already
// holds this very Owner under this very origin (a repeated restore).
func requireRestorableWorkspace(root string, manifest RecoverySetManifest) error {
	owner, err := localevidence.LoadOwnerCustody(root)
	if errors.Is(err, localevidence.ErrOwnerCustodyMissing) {
		// Without an Owner nothing else of the set may already exist.
		for _, file := range recoverySetCustodyFiles {
			if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(file.path))); !errors.Is(statErr, fs.ErrNotExist) {
				return ErrRecoveryRestoreConflict
			}
		}
		for _, tree := range recoverySetCustodyTrees {
			if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(tree))); !errors.Is(statErr, fs.ErrNotExist) {
				return ErrRecoveryRestoreConflict
			}
		}
		return nil
	}
	if err != nil || owner.OwnerRef != manifest.OwnerRef {
		return ErrRecoveryRestoreConflict
	}
	if address, err := localevidence.LocalIdentityRuntimeAddress(root); err == nil && address.PocketIDOrigin() != manifest.IdentityOrigin {
		return ErrRecoveryRestoreConflict
	}
	return nil
}

// installRecoverySetCustody moves the staged custody into root: whole trees
// replace their directory, single files replace their file.
func installRecoverySetCustody(root, staged string, manifest RecoverySetManifest) error {
	for _, tree := range recoverySetCustodyTrees {
		from := filepath.Join(staged, filepath.FromSlash(tree))
		if _, err := os.Lstat(from); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		to := filepath.Join(root, filepath.FromSlash(tree))
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		if err := os.RemoveAll(to); err != nil {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return err
		}
	}
	for _, entry := range manifest.Files {
		if entry.Dir || slices.ContainsFunc(recoverySetCustodyTrees, func(tree string) bool { return strings.HasPrefix(entry.Path, tree+"/") }) {
			continue
		}
		to := filepath.Join(root, filepath.FromSlash(entry.Path))
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		if err := os.Rename(filepath.Join(staged, filepath.FromSlash(entry.Path)), to); err != nil {
			return err
		}
	}
	return nil
}

func copyRecoveryFile(from, to string) error {
	source, err := os.Open(from) //nolint:gosec // staged volume entry
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return err
	}
	target, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // empty Docker volume mountpoint
	if err != nil {
		return err
	}
	written, err := writeSparse(target, source, info.Size())
	if closeErr := target.Close(); err == nil {
		err = closeErr
	}
	if err == nil && written != info.Size() {
		err = errors.New("identity: short volume copy")
	}
	return err
}

func applyRecoveryOwnership(target string, entry RecoverySetEntry) error {
	if os.Geteuid() == 0 {
		if err := os.Lchown(target, entry.UID, entry.GID); err != nil {
			return err
		}
	}
	return os.Chmod(target, fs.FileMode(entry.Mode&0o777))
}

// writeSparse copies size bytes and leaves all-zero chunks as holes, so the
// step-ca value log, which is mostly preallocated zeros, stays sparse.
func writeSparse(file *os.File, source io.Reader, size int64) (int64, error) {
	buffer := make([]byte, recoverySetChunk)
	var written int64
	for written < size {
		n, err := io.ReadFull(source, buffer[:min(int64(len(buffer)), size-written)])
		if n > 0 {
			chunk := buffer[:n]
			if isZeroChunk(chunk) {
				if _, seekErr := file.Seek(int64(n), io.SeekCurrent); seekErr != nil {
					return written, seekErr
				}
			} else if _, writeErr := file.Write(chunk); writeErr != nil {
				return written, writeErr
			}
			written += int64(n)
		}
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
				break
			}
			return written, err
		}
	}
	return written, file.Truncate(written)
}

func isZeroChunk(chunk []byte) bool {
	for _, b := range chunk {
		if b != 0 {
			return false
		}
	}
	return true
}

// recoverySetAuthKey derives the manifest MAC key from the recovery key. The
// derivation is one-way, so the Authority, which stores this key to write
// sets, learns nothing that decrypts a set.
func recoverySetAuthKey(identity *age.HybridIdentity) ([]byte, error) {
	return hkdf.Key(sha256.New, []byte(identity.String()), []byte(recoverySetMACDomain), identity.Recipient().String(), sha256.Size)
}

func recoverySetManifestMAC(key, manifest []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(recoverySetMACDomain + "\x00"))
	_, _ = mac.Write(manifest)
	return mac.Sum(nil)
}

type boundedRecoveryReader struct {
	reader    io.Reader
	remaining int64
}

func (r *boundedRecoveryReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errors.New("identity: recovery set exceeds its size bound")
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}
