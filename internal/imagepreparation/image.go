// Package imagepreparation prepares neutral caches, never installed stacks.
package imagepreparation

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/internal/tofu"
)

const ManifestName = "neutral-image.json"

var ErrAdmissionMismatch = errors.New("neutral image admission mismatch")
var ErrNotNeutral = errors.New("image roots contain state that cannot be cloned")

type Image struct {
	Component string `json:"component"`
	Ref       string `json:"ref"`
	Digest    string `json:"digest"`
}
type Profile struct {
	ID              string   `json:"id"`
	Kit             string   `json:"kit"`
	Module          string   `json:"module"`
	DefaultOS       string   `json:"defaultOS"`
	CompatibleOS    []string `json:"compatibleOS"`
	Architectures   []string `json:"architectures"`
	Runtime         string   `json:"runtime"`
	ManagedTarget   string   `json:"managedTarget"`
	CacheScope      string   `json:"cacheScope"`
	NetworkRequired bool     `json:"networkRequired"`
	Images          []Image  `json:"images"`
}
type Platform struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	Distribution string `json:"distribution"`
	Version      string `json:"version"`
}
type Release struct {
	Kit           string `json:"kit"`
	Version       string `json:"version"`
	ArchiveSHA256 string `json:"archiveSha256"`
	IndexSHA256   string `json:"indexSha256"`
}
type Manifest struct {
	SchemaVersion string            `json:"schemaVersion"`
	Profile       Profile           `json:"profile"`
	Platform      Platform          `json:"platform"`
	Release       Release           `json:"release"`
	Binaries      map[string]string `json:"binaries"`
	ProviderFiles map[string]string `json:"providerFiles"`
}

func Plan(id string) (Profile, error) {
	raw, err := architecturev2.NeutralImageProfile(id)
	if err != nil {
		return Profile{}, err
	}
	var profile Profile
	err = json.Unmarshal(raw, &profile)
	if err == nil && len(profile.Images) == 0 {
		err = errors.New("neutral profile has no authoritative container images")
	}
	return profile, err
}

// LocalDocker always addresses the local Unix socket with an empty config.
// Contexts, DOCKER_HOST and user registry credentials cannot redirect a bake.
type LocalDocker struct{ Config string }

func (docker LocalDocker) Run(ctx context.Context, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock", "--config", docker.Config}, args...)...)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "DOCKER_") {
			command.Env = append(command.Env, item)
		}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("local Docker %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

type ContainerCache interface {
	Run(context.Context, ...string) ([]byte, error)
}

type Options struct {
	CacheRoot  string
	Target     string
	Executable string
	Version    string
	Installer  releaseindex.Installer
	Docker     ContainerCache
	// Host observation is injectable only for package tests. The CLI always
	// supplies ObserveHost and LocalDocker; manifests never supply these facts.
	ObserveHost func() (Platform, error)
}

func ObserveHost() (Platform, error) {
	if runtime.GOOS != "linux" {
		return Platform{}, errors.New("neutral image preparation requires a Linux Ubuntu host")
	}
	raw, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return Platform{}, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[key] = strings.Trim(value, "\"'")
		}
	}
	if values["ID"] != "ubuntu" || (values["VERSION_ID"] != "26.04" && values["VERSION_ID"] != "24.04") || (runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") {
		return Platform{}, errors.New("neutral image profile requires Ubuntu 26.04 or 24.04 on amd64 or arm64")
	}
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH, Distribution: values["ID"], Version: values["VERSION_ID"]}, nil
}

// CleanTarget refuses every existing entry, including foreign custody nested
// beneath arbitrary directories. Preparation never scrubs an initialized host.
func CleanTarget(target string) error {
	if err := SafePath(target); err != nil {
		return err
	}
	entries, err := os.ReadDir(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("%w: deployment target must be empty", ErrNotNeutral)
	}
	return nil
}

// SafePath checks every existing ancestor, not just the final directory.
func SafePath(name string) error {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	for current := absolute; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("image path cannot traverse a symlink: %s", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}

func validateOptions(options Options) (Platform, error) {
	if err := CleanTarget(options.Target); err != nil {
		return Platform{}, err
	}
	return validateCacheOptions(options)
}

func validateCacheOptions(options Options) (Platform, error) {
	if options.ObserveHost == nil || options.Docker == nil || options.Installer.Attestations == nil {
		return Platform{}, errors.New("trusted host observation, local container cache and release verifier are required")
	}
	if options.Version == "" || options.Version == "dev" || strings.ContainsAny(options.Version, "/\\") {
		return Platform{}, errors.New("an exact published running release is required")
	}
	if exact, err := releaseindex.ExactTagForBuildVersion(options.Version); err != nil || exact != options.Version {
		return Platform{}, errors.New("neutral image requires an exact published release tag")
	}
	if err := SafePath(options.CacheRoot); err != nil {
		return Platform{}, err
	}
	if err := SafePath(options.Target); err != nil {
		return Platform{}, err
	}
	cacheRoot, err := filepath.Abs(options.CacheRoot)
	if err != nil {
		return Platform{}, err
	}
	target, err := filepath.Abs(options.Target)
	if err != nil {
		return Platform{}, err
	}
	// The deployment cannot contain the neutral release cache or vice versa.
	for _, pair := range [][2]string{{cacheRoot, target}, {target, cacheRoot}} {
		relative, err := filepath.Rel(pair[0], pair[1])
		if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return Platform{}, errors.New("image cache and deployment target must be separate directories")
		}
	}
	return options.ObserveHost()
}

func releaseDirectory(options Options, profile Profile, platform Platform) string {
	return filepath.Join(options.CacheRoot, ".stackkit", "releases", profile.Kit, options.Version, platform.OS+"-"+platform.Arch)
}

func trustedManifest(ctx context.Context, options Options, profile Profile, platform Platform) (Manifest, error) {
	manifest := Manifest{SchemaVersion: "stackkit.neutral-image/v1", Profile: profile, Platform: platform}
	installDir := releaseDirectory(options, profile, platform)
	if err := SafePath(installDir); err != nil {
		return Manifest{}, err
	}
	if err := neutralCacheRoot(options.CacheRoot, installDir); err != nil {
		return Manifest{}, err
	}
	err := options.Installer.InspectInstalled(ctx, installDir, func(installation releaseindex.VerifiedInstallation) error {
		return installation.Inspect(func(receipt releaseindex.Receipt, asset releaseindex.Asset, archive io.Reader) error {
			if receipt.Kit != profile.Kit || receipt.Version != options.Version || receipt.Platform.OS != platform.OS || receipt.Platform.Arch != platform.Arch {
				return errors.New("verified release does not match running CLI and host")
			}
			if err := releaseCacheFiles(installDir, asset); err != nil {
				return err
			}
			digests, providers, err := archiveBinaryDigests(archive)
			if err != nil {
				return err
			}
			for binary, digest := range digests {
				file := filepath.Join(filepath.Dir(options.Executable), binary)
				if binary == "stackkit" {
					file = options.Executable
				}
				actual, err := FileDigest(file)
				if err != nil {
					return err
				}
				if actual != digest {
					return fmt.Errorf("%w: packaged %s bytes do not match verified release archive", ErrAdmissionMismatch, binary)
				}
			}
			manifest.Binaries = digests
			providerRoot, err := installedProviderRoot(options.Executable)
			if err != nil {
				return err
			}
			if _, err := tofu.LoadProviderClosure(providerRoot); err != nil {
				return err
			}
			actualProviders, err := digestTree(providerRoot)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(providers, actualProviders) {
				return errors.New("packaged provider mirror bytes differ from verified release archive")
			}
			manifest.ProviderFiles = providers
			manifest.Release = Release{Kit: receipt.Kit, Version: receipt.Version, ArchiveSHA256: receipt.ArchiveSHA256, IndexSHA256: receipt.IndexSHA256}
			return nil
		})
	})
	return manifest, err
}

var requiredBinaries = []string{"stackkit", "stackkit-server", "stackkit-mcp", "tofu", "terramate"}

func archiveBinaryDigests(reader io.Reader) (map[string]string, map[string]string, error) {
	zipped, err := gzip.NewReader(reader)
	if err != nil {
		return nil, nil, err
	}
	defer zipped.Close()
	archive := tar.NewReader(zipped)
	digests := map[string]string{}
	providers := map[string]string{}
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		clean := path.Clean(header.Name)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "\\") {
			return nil, nil, errors.New("verified archive contains escaping path")
		}
		if strings.HasPrefix(clean, "providers/") && header.Typeflag != tar.TypeDir {
			if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 256<<20 {
				return nil, nil, errors.New("archive provider mirror must contain bounded regular files")
			}
			name := strings.TrimPrefix(clean, "providers/")
			if providers[name] != "" {
				return nil, nil, errors.New("archive provider file is repeated")
			}
			hash := sha256.New()
			if _, err := io.Copy(hash, archive); err != nil {
				return nil, nil, err
			}
			providers[name] = hex.EncodeToString(hash.Sum(nil))
			continue
		}
		for _, binary := range requiredBinaries {
			if path.Base(clean) != binary {
				continue
			}
			if header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > 256<<20 || header.Mode&0111 == 0 {
				return nil, nil, fmt.Errorf("archive %s must be a bounded regular executable", binary)
			}
			if _, exists := digests[binary]; exists {
				return nil, nil, fmt.Errorf("archive repeats %s", binary)
			}
			hash := sha256.New()
			if _, err := io.Copy(hash, archive); err != nil {
				return nil, nil, err
			}
			digests[binary] = hex.EncodeToString(hash.Sum(nil))
		}
	}
	for _, binary := range requiredBinaries {
		if digests[binary] == "" {
			return nil, nil, fmt.Errorf("verified release does not package %s", binary)
		}
	}
	if providers[tofu.ProviderManifestFile] == "" || providers[tofu.ProviderLockFile] == "" {
		return nil, nil, errors.New("verified release does not package its provider closure")
	}
	return digests, providers, nil
}

func FileDigest(name string) (string, error) {
	if err := SafePath(name); err != nil {
		return "", err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 256<<20 {
		return "", errors.New("packaged binary must be a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("packaged binary changed while opening")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", errors.New("packaged binary changed while hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func emptyRuntime(ctx context.Context, docker ContainerCache, platform Platform) error {
	raw, err := docker.Run(ctx, "info", "--format", "{{json .}}")
	if err != nil {
		return err
	}
	var info struct {
		OSType       string
		Architecture string
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return err
	}
	arch := info.Architecture
	if arch == "x86_64" {
		arch = "amd64"
	}
	if arch == "aarch64" {
		arch = "arm64"
	}
	if info.OSType != platform.OS || arch != platform.Arch {
		return errors.New("local Docker daemon platform does not match observed host")
	}
	for _, args := range [][]string{{"container", "ls", "--all", "--quiet"}, {"volume", "ls", "--quiet"}} {
		raw, err := docker.Run(ctx, args...)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(raw)) != 0 {
			return errors.New("image bake requires no Docker containers (including stopped) or volumes")
		}
	}
	return nil
}

func inspectImages(ctx context.Context, docker ContainerCache, manifest Manifest) error {
	for _, image := range manifest.Profile.Images {
		ref := image.Ref + "@" + image.Digest
		raw, err := docker.Run(ctx, "image", "inspect", ref)
		if err != nil {
			return err
		}
		var inspections []struct {
			OS          string `json:"Os"`
			Arch        string `json:"Architecture"`
			RepoDigests []string
		}
		if err := json.Unmarshal(raw, &inspections); err != nil {
			return err
		}
		if len(inspections) != 1 || inspections[0].OS != manifest.Platform.OS || inspections[0].Arch != manifest.Platform.Arch {
			return fmt.Errorf("cached %s does not match observed platform", image.Component)
		}
		found := false
		for _, digest := range inspections[0].RepoDigests {
			if strings.HasSuffix(digest, "@"+image.Digest) {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("cached %s has no authoritative repository digest", image.Component)
		}
	}
	return nil
}

// A bake may contain a partial cache of the selected core profile, but cannot
// capture unrelated image layers or builder caches left by previous workloads.
func neutralContainerCache(ctx context.Context, docker ContainerCache, profile Profile, platform Platform) error {
	allowed := map[string]bool{}
	for _, image := range profile.Images {
		raw, err := docker.Run(ctx, "image", "inspect", image.Ref+"@"+image.Digest)
		if err != nil {
			continue
		} // Not cached yet; preparation will pull this pin.
		var images []struct {
			ID          string `json:"Id"`
			OS          string `json:"Os"`
			Arch        string `json:"Architecture"`
			RepoDigests []string
		}
		if err := json.Unmarshal(raw, &images); err != nil {
			return err
		}
		if len(images) != 1 || images[0].OS != platform.OS || images[0].Arch != platform.Arch {
			return fmt.Errorf("%w: cached profile image has another platform", ErrNotNeutral)
		}
		for _, digest := range images[0].RepoDigests {
			if strings.HasSuffix(digest, "@"+image.Digest) {
				allowed[images[0].ID] = true
			}
		}
	}
	raw, err := docker.Run(ctx, "image", "ls", "--all", "--no-trunc", "--quiet")
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(string(raw)) {
		if !allowed[id] {
			return fmt.Errorf("%w: local Docker contains an image outside the selected core cache", ErrNotNeutral)
		}
	}
	raw, err = docker.Run(ctx, "system", "df", "--format", "{{json .}}")
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	found := false
	for {
		var entry struct {
			Type       string
			TotalCount string
		}
		if err := decoder.Decode(&entry); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if entry.Type == "Build Cache" {
			found = true
			if entry.TotalCount != "0" {
				return fmt.Errorf("%w: local Docker contains build cache", ErrNotNeutral)
			}
		}
	}
	if !found {
		return errors.New("local Docker did not report build-cache neutrality")
	}
	return nil
}

// Prepare downloads the current exact attested release into the neutral cache,
// verifies the actual packaged tools, and only pulls immutable container images.
func Prepare(ctx context.Context, options Options, id string) (Manifest, error) {
	profile, err := Plan(id)
	if err != nil {
		return Manifest{}, err
	}
	platform, err := validateOptions(options)
	if err != nil {
		return Manifest{}, err
	}
	manifestPath := filepath.Join(options.CacheRoot, ManifestName)
	if _, err := os.Lstat(manifestPath); err == nil {
		manifest, err := Verify(ctx, options, manifestPath)
		if err == nil && manifest.Profile.ID != id {
			return Manifest{}, errors.New("existing cache manifest belongs to another profile")
		}
		return manifest, err
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	if err := neutralCacheRoot(options.CacheRoot, releaseDirectory(options, profile, platform)); err != nil {
		return Manifest{}, err
	}
	if err := emptyRuntime(ctx, options.Docker, platform); err != nil {
		return Manifest{}, err
	}
	if err := neutralContainerCache(ctx, options.Docker, profile, platform); err != nil {
		return Manifest{}, err
	}
	if err := os.MkdirAll(options.CacheRoot, 0755); err != nil {
		return Manifest{}, err
	}
	if options.Installer.Source == nil {
		return Manifest{}, errors.New("release source is required for neutral installation")
	}
	resolution, err := (releaseindex.Resolver{Source: options.Installer.Source, Attestations: options.Installer.Attestations}).Resolve(ctx, releaseindex.ResolveRequest{Kit: profile.Kit, Target: options.Version, OS: platform.OS, Arch: platform.Arch})
	if err != nil {
		return Manifest{}, err
	}
	if _, err = options.Installer.Install(ctx, resolution, options.CacheRoot); err != nil {
		return Manifest{}, err
	}
	manifest, err := trustedManifest(ctx, options, profile, platform)
	if err != nil {
		return Manifest{}, err
	}
	for _, image := range profile.Images {
		if _, err := options.Docker.Run(ctx, "image", "pull", "--platform", platform.OS+"/"+platform.Arch, image.Ref+"@"+image.Digest); err != nil {
			return Manifest{}, err
		}
	}
	if err := inspectImages(ctx, options.Docker, manifest); err != nil {
		return Manifest{}, err
	}
	if err := emptyRuntime(ctx, options.Docker, platform); err != nil {
		return Manifest{}, err
	}
	if err := neutralContainerCache(ctx, options.Docker, profile, platform); err != nil {
		return Manifest{}, err
	}
	if err := CleanTarget(options.Target); err != nil {
		return Manifest{}, err
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	if err := architecturev2.ValidateNeutralImageManifest(raw); err != nil {
		return Manifest{}, err
	}
	file, err := os.OpenFile(manifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return Manifest{}, err
	}
	_, writeErr := file.Write(append(raw, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return Manifest{}, writeErr
	}
	if closeErr != nil {
		return Manifest{}, closeErr
	}
	return Verify(ctx, options, manifestPath)
}

func ReadManifest(name string) (Manifest, error) {
	if err := SafePath(name); err != nil {
		return Manifest{}, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return Manifest{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return Manifest{}, errors.New("neutral manifest must be a bounded regular file")
	}
	raw, err := os.ReadFile(name)
	if err != nil {
		return Manifest{}, err
	}
	if err := architecturev2.ValidateNeutralImageManifest(raw); err != nil {
		return Manifest{}, fmt.Errorf("neutral manifest schema: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return Manifest{}, errors.New("neutral manifest has trailing data")
	}
	return manifest, nil
}

// Verify re-observes the clone, release bytes and cache. An unsigned manifest's
// asserted digests or its writer's previous host evidence authorize nothing.
func Verify(ctx context.Context, options Options, manifestPath string) (Manifest, error) {
	if err := CleanTarget(options.Target); err != nil {
		return Manifest{}, err
	}
	manifest, err := VerifyCache(ctx, options, manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	if err := CleanTarget(options.Target); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// VerifyCache re-observes the host, attested release, packaged tools and exact
// neutral container cache. It does not admit deployment contents: init must
// separately require an empty target or an already-applied canonical intent.
func VerifyCache(ctx context.Context, options Options, manifestPath string) (Manifest, error) {
	platform, err := validateCacheOptions(options)
	if err != nil {
		return Manifest{}, err
	}
	wantPath, err := filepath.Abs(filepath.Join(options.CacheRoot, ManifestName))
	if err != nil {
		return Manifest{}, err
	}
	actualPath, err := filepath.Abs(manifestPath)
	if err != nil || actualPath != wantPath {
		return Manifest{}, errors.New("manifest must be the neutral cache root's neutral-image.json")
	}
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		return Manifest{}, err
	}
	profile, err := Plan(manifest.Profile.ID)
	if err != nil {
		return Manifest{}, err
	}
	if !reflect.DeepEqual(manifest.Profile, profile) || manifest.Platform != platform || manifest.Release.Version != options.Version || manifest.Release.Kit != profile.Kit {
		return Manifest{}, fmt.Errorf("%w: profile, observed host or running release differs", ErrAdmissionMismatch)
	}
	expected, err := trustedManifest(ctx, options, profile, platform)
	if err != nil {
		return Manifest{}, err
	}
	if !reflect.DeepEqual(manifest, expected) {
		return Manifest{}, fmt.Errorf("%w: metadata differs from current trusted release or packaged binaries", ErrAdmissionMismatch)
	}
	if err := emptyRuntime(ctx, options.Docker, platform); err != nil {
		return Manifest{}, err
	}
	if err := inspectImages(ctx, options.Docker, expected); err != nil {
		return Manifest{}, err
	}
	if err := neutralContainerCache(ctx, options.Docker, profile, platform); err != nil {
		return Manifest{}, err
	}
	return expected, nil
}
