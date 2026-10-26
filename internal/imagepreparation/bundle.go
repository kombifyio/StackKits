package imagepreparation

import (
	"bytes"
	"context"
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
	"reflect"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
)

const BundleManifestName = "manifest.json"

type BundleFile struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type BundleImage struct {
	Image Image  `json:"image"`
	Path  string `json:"path"`
}

// BundleManifest is unsigned transport metadata, never Apply or release
// authority. The running attested release, embedded catalog and reviewed model
// table independently authenticate every executable, image and model input.
type BundleManifest struct {
	SchemaVersion string                                   `json:"schemaVersion"`
	Neutral       Manifest                                 `json:"neutral"`
	Images        []BundleImage                            `json:"images"`
	Models        []architecturev2renderer.OfflineModelPin `json:"models"`
	Files         map[string]BundleFile                    `json:"files"`
}

func bundleModels(profile Profile) ([]architecturev2renderer.OfflineModelPin, error) {
	if profile.DataProfile != "full-local" || profile.NetworkRequired || profile.CacheScope != "full-local-artifacts" {
		return nil, errors.New("offline bundles require a catalog-owned Full Local profile")
	}
	result := make([]architecturev2renderer.OfflineModelPin, 0, len(profile.ModelPacks))
	for _, pack := range profile.ModelPacks {
		model, err := architecturev2renderer.OfflinePrivateAIModel(pack.Component, pack.Size)
		if err != nil {
			return nil, err
		}
		result = append(result, model)
	}
	if len(result) == 0 {
		return nil, errors.New("Full Local bundle has no reviewed local model")
	}
	return result, nil
}

// PrepareOfflineBundle is the online preparation phase. It delegates release,
// tool, provider and image admission to the existing neutral-cache owner.
// It never initializes an Owner, starts a workload or copies a workspace.
func PrepareOfflineBundle(ctx context.Context, options Options, root, profileID, modelCache string) (BundleManifest, error) {
	if runtimeprofile.FullLocal() {
		return BundleManifest{}, errors.New("bundle creation is the online installation-preparation phase; an active Full Local process cannot download inputs")
	}
	if err := separateDirectories(root, options.Target); err != nil {
		return BundleManifest{}, err
	}
	if err := CleanTarget(root); err != nil {
		return BundleManifest{}, err
	}
	profile, err := Plan(profileID, options.Authority)
	if err != nil {
		return BundleManifest{}, err
	}
	models, err := bundleModels(profile)
	if err != nil {
		return BundleManifest{}, err
	}
	options.CacheRoot = filepath.Join(root, "cache")
	platform, err := validateOptions(options)
	if err != nil {
		return BundleManifest{}, err
	}
	if options.candidateSource() {
		if err := retainCandidate(ctx, options, profile, platform); err != nil {
			return BundleManifest{}, err
		}
		// Complete model admission precedes even Docker observation. The source
		// and every packaged executable/provider have already been authenticated.
		for _, model := range models {
			if err := StageOfflineModel(ctx, root, model, modelCache); err != nil {
				return BundleManifest{}, err
			}
			if _, err := verifyOfflineModel(root, model); err != nil {
				return BundleManifest{}, err
			}
		}
	}
	if err := requireContainerdStore(ctx, options.Docker); err != nil {
		return BundleManifest{}, err
	}
	neutral, err := Prepare(ctx, options, profileID)
	if err != nil {
		return BundleManifest{}, err
	}
	manifest := BundleManifest{SchemaVersion: "stackkit.offline-bundle/v1", Neutral: neutral, Models: models}
	for _, image := range profile.Images {
		relative := "images/" + strings.TrimPrefix(image.Digest, "sha256:") + ".tar"
		name := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			return BundleManifest{}, err
		}
		// Saving the original root preserves the catalog's manifest digest.
		// The content graph check refuses a legacy or incomplete export.
		if _, err := options.Docker.Run(ctx, "image", "save", "--output", name, image.Ref+"@"+image.Digest); err != nil {
			return BundleManifest{}, err
		}
		if err := InspectOCIArchive(name, image, neutral.Platform); err != nil {
			return BundleManifest{}, err
		}
		manifest.Images = append(manifest.Images, BundleImage{Image: image, Path: relative})
	}
	for _, model := range models {
		if !options.candidateSource() {
			if err := StageOfflineModel(ctx, root, model, modelCache); err != nil {
				return BundleManifest{}, err
			}
		}
	}
	manifest.Files, err = bundleFiles(root)
	if err != nil {
		return BundleManifest{}, err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return BundleManifest{}, err
	}
	file, err := os.OpenFile(filepath.Join(root, BundleManifestName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return BundleManifest{}, err
	}
	_, writeErr := file.Write(append(raw, '\n'))
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return BundleManifest{}, err
	}
	return VerifyOfflineBundle(ctx, options, root)
}

// VerifyOfflineBundle performs no Docker mutation, registry request or Apply.
// It also works after installation: runtime owners must recheck this exact
// immutable closure before mounting a local model, not re-run bake neutrality.
func VerifyOfflineBundle(ctx context.Context, options Options, root string) (BundleManifest, error) {
	if err := separateDirectories(root, options.Target); err != nil {
		return BundleManifest{}, err
	}
	options.CacheRoot = filepath.Join(root, "cache")
	platform, err := validateCacheOptions(options)
	if err != nil {
		return BundleManifest{}, err
	}
	var manifest BundleManifest
	if err := readBundleJSON(filepath.Join(root, BundleManifestName), &manifest); err != nil {
		return BundleManifest{}, err
	}
	if manifest.SchemaVersion != "stackkit.offline-bundle/v1" || manifest.Neutral.Platform != platform {
		return BundleManifest{}, errors.New("offline bundle schema or observed platform differs")
	}
	profile, err := Plan(manifest.Neutral.Profile.ID, options.Authority)
	if err != nil {
		return BundleManifest{}, err
	}
	models, err := bundleModels(profile)
	if err != nil {
		return BundleManifest{}, err
	}
	if !reflect.DeepEqual(profile, manifest.Neutral.Profile) || !reflect.DeepEqual(models, manifest.Models) {
		return BundleManifest{}, errors.New("offline bundle profile or model pins differ from this running release")
	}
	trusted, err := trustedManifest(ctx, options, profile, platform)
	if err != nil {
		return BundleManifest{}, err
	}
	neutral, err := ReadManifest(filepath.Join(options.CacheRoot, ManifestName), options.Authority)
	if err != nil || !reflect.DeepEqual(trusted, manifest.Neutral) || !reflect.DeepEqual(trusted, neutral) {
		return BundleManifest{}, errors.New("offline bundle release metadata differs from the attested cache")
	}
	files, err := bundleFiles(root)
	if err != nil {
		return BundleManifest{}, err
	}
	if !reflect.DeepEqual(manifest.Files, files) {
		return BundleManifest{}, errors.New("offline bundle file closure changed or is incomplete")
	}
	allowed := map[string]bool{}
	for name := range files {
		if strings.HasPrefix(name, "cache/") {
			// trustedManifest has already checked the closed neutral cache.
			allowed[name] = true
		}
	}
	if len(manifest.Images) != len(profile.Images) {
		return BundleManifest{}, errors.New("offline bundle image closure differs")
	}
	for i, image := range profile.Images {
		relative := "images/" + strings.TrimPrefix(image.Digest, "sha256:") + ".tar"
		if manifest.Images[i].Image != image || manifest.Images[i].Path != relative {
			return BundleManifest{}, errors.New("offline image selection differs from the catalog")
		}
		if err := InspectOCIArchive(filepath.Join(root, filepath.FromSlash(relative)), image, platform); err != nil {
			return BundleManifest{}, err
		}
		allowed[relative] = true
	}
	for _, model := range models {
		modelFiles, err := verifyOfflineModel(root, model)
		if err != nil {
			return BundleManifest{}, err
		}
		for _, name := range modelFiles {
			allowed[name] = true
		}
	}
	if len(allowed) != len(files) {
		return BundleManifest{}, errors.New("offline bundle contains unrecognized inputs")
	}
	for name := range files {
		if !allowed[name] {
			return BundleManifest{}, errors.New("offline bundle contains an input outside the release, catalog or reviewed models")
		}
	}
	return manifest, nil
}

// ImportOfflineBundle only imports admitted neutral artifacts. Native init,
// generation and Apply retain their existing separate ownership and custody.
func ImportOfflineBundle(ctx context.Context, options Options, root string) (result BundleManifest, err error) {
	admission, err := AdmitFreshOfflineBundle(ctx, options, root)
	if err != nil {
		return BundleManifest{}, err
	}
	docker, ok := options.Docker.(containerInputCache)
	if !ok {
		return BundleManifest{}, errors.New("offline image import requires local Docker with open-file stdin support")
	}
	custody, err := stageBundleImages(ctx, admission)
	if err != nil {
		return BundleManifest{}, err
	}
	defer func() {
		if closeErr := custody.close(); closeErr != nil {
			result = BundleManifest{}
			err = errors.Join(err, closeErr)
		}
	}()
	manifest := admission.manifest
	if err := requireContainerdStore(ctx, options.Docker); err != nil {
		return BundleManifest{}, err
	}
	if err := emptyRuntime(ctx, options.Docker, manifest.Neutral.Platform); err != nil {
		return BundleManifest{}, err
	}
	if err := neutralContainerCache(ctx, options.Docker, manifest.Neutral.Profile, manifest.Neutral.Platform); err != nil {
		return BundleManifest{}, err
	}
	if err := admission.recheck(); err != nil {
		return BundleManifest{}, err
	}
	for _, staged := range custody.images {
		if _, err := docker.RunInput(ctx, staged.file, "image", "load", "--platform", manifest.Neutral.Platform.OS+"/"+manifest.Neutral.Platform.Arch); err != nil {
			return BundleManifest{}, err
		}
		// containerd retains the authenticated root but initially has no
		// repository name for digest-only exports. Associate only the exact
		// root authenticated above, then keep inspectImages as final admission.
		if _, err := options.Docker.Run(ctx, "image", "tag", staged.image.Image.Digest, staged.image.Image.Ref); err != nil {
			return BundleManifest{}, err
		}
	}
	options.CacheRoot = filepath.Join(root, "cache")
	if _, err := Verify(ctx, options, filepath.Join(options.CacheRoot, ManifestName)); err != nil {
		return BundleManifest{}, err
	}
	if err := admission.recheck(); err != nil {
		return BundleManifest{}, err
	}
	return manifest, nil
}

func requireContainerdStore(ctx context.Context, docker ContainerCache) error {
	raw, err := docker.Run(ctx, "info", "--format", "{{json .DriverStatus}}")
	if err != nil {
		return err
	}
	var status [][]string
	if err := json.Unmarshal(raw, &status); err != nil {
		return err
	}
	for _, pair := range status {
		if len(pair) == 2 && pair[0] == "driver-type" && pair[1] == "io.containerd.snapshotter.v1" {
			return nil
		}
	}
	return errors.New("offline image import requires Docker's containerd image store to retain authoritative repository digests")
}

func readBundleJSON(name string, value any) error {
	if err := SafePath(name); err != nil {
		return err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 4<<20 {
		return errors.New("offline metadata must be a bounded regular file")
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("offline metadata has trailing data")
	}
	return nil
}

func bundleFiles(root string) (map[string]BundleFile, error) {
	if err := SafePath(root); err != nil {
		return nil, err
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	files := map[string]BundleFile{}
	var directories []string
	err = fs.WalkDir(opened.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == "." || name == BundleManifestName {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || strings.Contains(name, "\\") || name != path.Clean(name) {
			return errors.New("offline bundle paths must be regular, confined and unique")
		}
		if entry.IsDir() {
			directories = append(directories, name)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 16<<30 || len(files) >= 20000 {
			return errors.New("offline bundle requires a bounded regular-file closure")
		}
		file, err := opened.Open(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		count, hashErr := io.Copy(hash, file)
		after, statErr := file.Stat()
		closeErr := file.Close()
		if err := errors.Join(hashErr, statErr, closeErr); err != nil {
			return err
		}
		if !os.SameFile(info, after) || count != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			return errors.New("offline bundle file changed during verification")
		}
		files[name] = BundleFile{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: count}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, directory := range directories {
		found := false
		for name := range files {
			if strings.HasPrefix(name, directory+"/") {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("offline bundle contains empty or foreign directory %s", directory)
		}
	}
	return files, nil
}
