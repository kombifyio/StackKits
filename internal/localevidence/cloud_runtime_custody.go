package localevidence

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	CloudRuntimeCustodyAPIVersion = "stackkit.cloud-runtime-custody/v1"
	cloudRuntimeCustodyRelDir     = ".stackkit/custody/cloud-runtime"
)

var (
	ErrCloudRuntimeCustodyMissing = errors.New("localevidence: no Cloud runtime custody")
	cloudRuntimeFilePaths         = []string{"coolify.env", "pocketid.env", "tinyauth.env"}
)

// CloudRuntimeCustody is the owner-signed, secret-free index for the
// provider-neutral services installed on an externally supplied Cloud host.
// Provider credentials and server lifecycle never enter this bundle.
type CloudRuntimeCustody struct {
	APIVersion    string                       `json:"apiVersion"`
	Kind          string                       `json:"kind"`
	OwnerRef      string                       `json:"ownerRef"`
	KeyID         string                       `json:"keyId"`
	Domain        string                       `json:"domain"`
	EstablishedAt time.Time                    `json:"establishedAt"`
	Files         []BasementRuntimeCustodyFile `json:"files"`
	Signature     string                       `json:"signature"`
}

func EstablishCloudRuntimeCustody(workspaceRoot, domain string) (CloudRuntimeCustody, error) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	if !validBasementRuntimeDomain(domain) {
		return CloudRuntimeCustody{}, errors.New("localevidence: Cloud runtime custody requires a canonical domain")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return CloudRuntimeCustody{}, err
	}
	if existing, loadErr := LoadCloudRuntimeCustody(workspaceRoot); loadErr == nil {
		if existing.Domain != domain {
			return CloudRuntimeCustody{}, errors.New("localevidence: Cloud runtime custody domain differs from established custody")
		}
		return existing, nil
	} else if !errors.Is(loadErr, ErrCloudRuntimeCustodyMissing) {
		return CloudRuntimeCustody{}, loadErr
	}

	finalDirectory, err := confinedCustodyPath(workspaceRoot, cloudRuntimeCustodyRelDir)
	if err != nil {
		return CloudRuntimeCustody{}, err
	}
	if _, statErr := os.Lstat(finalDirectory); statErr == nil {
		return CloudRuntimeCustody{}, errors.New("localevidence: Cloud runtime custody exists without a valid signed manifest")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: inspect Cloud runtime custody: %w", statErr)
	}
	parent := filepath.Dir(finalDirectory)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: create Cloud runtime custody parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".cloud-runtime-*")
	if err != nil {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: create Cloud runtime custody transaction: %w", err)
	}
	removeStage := true
	defer func() {
		if removeStage {
			_ = os.RemoveAll(stage)
		}
	}()
	if err := os.Chmod(stage, 0o700); err != nil {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: restrict Cloud runtime custody transaction: %w", err)
	}

	files, err := basementRuntimeEnvironments(owner, domain)
	if err != nil {
		return CloudRuntimeCustody{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return CloudRuntimeCustody{}, err
	}
	manifestFiles := make([]BasementRuntimeCustodyFile, 0, len(cloudRuntimeFilePaths))
	for _, relative := range cloudRuntimeFilePaths {
		content, ok := files[relative]
		if !ok {
			return CloudRuntimeCustody{}, fmt.Errorf("localevidence: internal Cloud runtime file %q is missing", relative)
		}
		if err := writePrivateRuntimeFile(filepath.Join(stage, relative), content); err != nil {
			return CloudRuntimeCustody{}, fmt.Errorf("localevidence: persist Cloud runtime file %q: %w", relative, err)
		}
		manifestFiles = append(manifestFiles, BasementRuntimeCustodyFile{
			Path: relative, MAC: cloudRuntimeFileMAC(key, relative, content),
		})
	}
	record := CloudRuntimeCustody{
		APIVersion: CloudRuntimeCustodyAPIVersion, Kind: "CloudRuntimeCustody",
		OwnerRef: owner.OwnerRef, KeyID: owner.KeyID, Domain: domain,
		EstablishedAt: time.Now().UTC().Truncate(time.Second), Files: manifestFiles,
	}
	signingBytes, err := cloudRuntimeSigningBytes(record)
	if err != nil {
		return CloudRuntimeCustody{}, err
	}
	record.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(key.private, signingBytes))
	manifest, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: encode Cloud runtime custody: %w", err)
	}
	if err := writePrivateRuntimeFile(filepath.Join(stage, basementRuntimeManifestRelPath), manifest); err != nil {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: persist Cloud runtime custody manifest: %w", err)
	}
	if err := os.Rename(stage, finalDirectory); err != nil {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: atomically install Cloud runtime custody: %w", err)
	}
	removeStage = false
	return LoadCloudRuntimeCustody(workspaceRoot)
}

func LoadCloudRuntimeCustody(workspaceRoot string) (CloudRuntimeCustody, error) {
	directory, err := confinedCustodyPath(workspaceRoot, cloudRuntimeCustodyRelDir)
	if err != nil {
		return CloudRuntimeCustody{}, err
	}
	manifestPath := filepath.Join(directory, basementRuntimeManifestRelPath)
	raw, err := os.ReadFile(manifestPath) //nolint:gosec // fixed path below explicit workspace
	if errors.Is(err, os.ErrNotExist) {
		return CloudRuntimeCustody{}, ErrCloudRuntimeCustodyMissing
	}
	if err != nil {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: read Cloud runtime custody manifest: %w", err)
	}
	if err := requirePrivateRuntimeFile(manifestPath); err != nil {
		return CloudRuntimeCustody{}, err
	}
	var record CloudRuntimeCustody
	if err := json.Unmarshal(raw, &record); err != nil {
		return CloudRuntimeCustody{}, fmt.Errorf("localevidence: decode Cloud runtime custody manifest: %w", err)
	}
	if record.APIVersion != CloudRuntimeCustodyAPIVersion || record.Kind != "CloudRuntimeCustody" ||
		record.OwnerRef == "" || record.KeyID == "" || !validBasementRuntimeDomain(record.Domain) || record.EstablishedAt.IsZero() {
		return CloudRuntimeCustody{}, errors.New("localevidence: Cloud runtime custody is not a recognised record")
	}
	owner, err := LoadOwnerCustody(workspaceRoot)
	if err != nil {
		return CloudRuntimeCustody{}, err
	}
	key, err := LoadOwnerKey(workspaceRoot)
	if err != nil {
		return CloudRuntimeCustody{}, err
	}
	if record.OwnerRef != owner.OwnerRef || record.KeyID != owner.KeyID || key.OwnerRef != record.OwnerRef || key.KeyID != record.KeyID {
		return CloudRuntimeCustody{}, errors.New("localevidence: Cloud runtime custody is not bound to the established owner")
	}
	signature, err := base64.RawStdEncoding.Strict().DecodeString(record.Signature)
	signingBytes, signingErr := cloudRuntimeSigningBytes(record)
	if err != nil || signingErr != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key.Public(), signingBytes, signature) {
		return CloudRuntimeCustody{}, errors.New("localevidence: Cloud runtime custody signature does not verify")
	}
	if err := validateCloudRuntimeInventory(directory, record.Files, key); err != nil {
		return CloudRuntimeCustody{}, err
	}
	return record, nil
}

func cloudRuntimeSigningBytes(record CloudRuntimeCustody) ([]byte, error) {
	record.Signature = ""
	return json.Marshal(record)
}

func validateCloudRuntimeInventory(directory string, files []BasementRuntimeCustodyFile, key OwnerKey) error {
	if len(files) != len(cloudRuntimeFilePaths) {
		return errors.New("localevidence: Cloud runtime custody file inventory is not complete")
	}
	expected := append([]string(nil), cloudRuntimeFilePaths...)
	sort.Strings(expected)
	actual := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if _, duplicate := seen[file.Path]; duplicate {
			return errors.New("localevidence: Cloud runtime custody file inventory contains a duplicate")
		}
		seen[file.Path] = struct{}{}
		actual = append(actual, file.Path)
		path := filepath.Join(directory, filepath.FromSlash(file.Path))
		if err := requirePrivateRuntimeFile(path); err != nil {
			return err
		}
		raw, err := os.ReadFile(path) //nolint:gosec // closed signed inventory
		if err != nil || file.MAC != cloudRuntimeFileMAC(key, file.Path, raw) {
			return fmt.Errorf("localevidence: Cloud runtime custody file %q does not match its signed manifest", file.Path)
		}
	}
	sort.Strings(actual)
	if !equalStrings(actual, expected) {
		return errors.New("localevidence: Cloud runtime custody file inventory is not the closed expected set")
	}
	allowed := map[string]struct{}{basementRuntimeManifestRelPath: {}}
	for _, path := range cloudRuntimeFilePaths {
		allowed[path] = struct{}{}
	}
	return filepath.WalkDir(directory, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("localevidence: Cloud runtime custody contains a symbolic link")
		}
		if entry.IsDir() {
			if relative != "." {
				return fmt.Errorf("localevidence: Cloud runtime custody contains unexpected directory %q", relative)
			}
			return nil
		}
		if _, ok := allowed[relative]; !ok {
			return fmt.Errorf("localevidence: Cloud runtime custody contains unexpected file %q", relative)
		}
		return nil
	})
}

func cloudRuntimeFileMAC(key OwnerKey, path string, content []byte) string {
	return basementRuntimeFileMAC(key, "cloud-runtime/"+path, content)
}
