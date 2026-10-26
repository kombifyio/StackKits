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
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/runtimeprofile"
)

const OfflineModelMount = architecturev2renderer.OfflineModelMount

// VerifyOfflineModelRef is the runtime read-only mount admission, not a new
// installer. Init has bound the whole authenticated bundle to the canonical
// ref; runtime additionally re-derives the model's immutable content pins from
// this binary before each use. It never trusts a manifest's own model hashes.
func VerifyOfflineModelRef(ctx context.Context, ref runtimeprofile.OfflineBundleRef, component string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := ref.Validate(); err != nil {
		return "", err
	}
	manifestName := filepath.Join(ref.Path, BundleManifestName)
	var manifest BundleManifest
	if err := readBundleJSON(manifestName, &manifest); err != nil {
		return "", err
	}
	digest, err := FileDigest(manifestName)
	if err != nil || digest != ref.ManifestSHA256 {
		return "", errors.New("offline bundle reference changed since native init")
	}
	pin, err := architecturev2renderer.OfflinePrivateAIModel(component, "small")
	if err != nil {
		return "", err
	}
	matched := false
	for _, model := range manifest.Models {
		matched = matched || model == pin
	}
	if !matched || manifest.SchemaVersion != "stackkit.offline-bundle/v1" || manifest.Neutral.Profile.DataProfile != "full-local" {
		return "", errors.New("bundle does not contain the reviewed local model closure")
	}
	if _, err := verifyOfflineModel(ref.Path, pin); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return ModelDirectory(ref.Path, pin), nil
}

type modelBlob struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

type modelManifest struct {
	SchemaVersion int         `json:"schemaVersion"`
	MediaType     string      `json:"mediaType"`
	Config        modelBlob   `json:"config"`
	Layers        []modelBlob `json:"layers"`
}

func offlineModelDirectory(model architecturev2renderer.OfflineModelPin) string {
	return "models/" + model.Component + "-" + model.Size
}

func ModelDirectory(root string, model architecturev2renderer.OfflineModelPin) string {
	return filepath.Join(root, filepath.FromSlash(offlineModelDirectory(model)))
}

func pinnedModelManifest(raw []byte, pin architecturev2renderer.OfflineModelPin) (modelManifest, error) {
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != pin.ManifestSHA256 {
		return modelManifest{}, errors.New("offline model manifest differs from the reviewed pin")
	}
	var document modelManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return modelManifest{}, err
	}
	if decoder.Decode(new(any)) != io.EOF || document.SchemaVersion != 2 || len(document.Layers) == 0 || len(document.Layers) > 16 {
		return modelManifest{}, errors.New("offline model manifest has an invalid shape")
	}
	for _, blob := range append([]modelBlob{document.Config}, document.Layers...) {
		if !strings.HasPrefix(blob.Digest, "sha256:") || !isSHA256(strings.TrimPrefix(blob.Digest, "sha256:")) || blob.Size <= 0 || blob.Size > 2<<30 {
			return modelManifest{}, errors.New("offline model blob requires a bounded SHA256 descriptor")
		}
	}
	return document, nil
}

// StageOfflineModel copies only the reviewed model closure into a new neutral
// bundle. A pre-existing Ollama cache is a transport optimization, never trust.
func StageOfflineModel(ctx context.Context, root string, pin architecturev2renderer.OfflineModelPin, modelCache string) error {
	authoritative, err := architecturev2renderer.OfflinePrivateAIModel(pin.Component, pin.Size)
	if err != nil || pin != authoritative {
		return errors.New("model staging requires the embedded reviewed pin")
	}
	repository, tag, ok := strings.Cut(pin.Tag, ":")
	if !ok || strings.ContainsAny(repository+tag, "/\\") {
		return errors.New("offline model must be one reviewed library tag")
	}
	directory := ModelDirectory(root, pin)
	if err := os.MkdirAll(filepath.Join(directory, "blobs"), 0755); err != nil {
		return err
	}
	manifestName := filepath.Join(directory, "manifest.json")
	var cache *os.Root
	if modelCache != "" {
		if err := SafePath(modelCache); err != nil {
			return err
		}
		var err error
		cache, err = os.OpenRoot(modelCache)
		if err != nil {
			return err
		}
		defer cache.Close()
	}
	copyInput := func(relative, remote, target, digest string, size int64) error {
		var input io.ReadCloser
		if cache != nil {
			info, err := cache.Lstat(relative)
			if err != nil || !info.Mode().IsRegular() || (size > 0 && info.Size() != size) || info.Size() > 2<<30 {
				return errors.New("local Ollama cache is missing a regular pinned input")
			}
			file, err := cache.Open(relative)
			if err != nil {
				return err
			}
			input = file
		} else {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, remote, nil)
			if err != nil {
				return err
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return err
			}
			if response.StatusCode != http.StatusOK {
				response.Body.Close()
				return fmt.Errorf("reviewed model artifact returned HTTP %d", response.StatusCode)
			}
			input = response.Body
		}
		defer input.Close()
		limit := size
		if limit <= 0 {
			limit = 1 << 20
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input, limit+1))
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if count == 0 || count > limit || (size > 0 && count != size) || hex.EncodeToString(hash.Sum(nil)) != digest {
			return errors.New("model artifact failed its reviewed content digest or size")
		}
		return nil
	}
	base := "https://registry.ollama.ai/v2/library/" + repository
	if err := copyInput(path.Join("manifests/registry.ollama.ai/library", repository, tag), base+"/manifests/"+tag, manifestName, pin.ManifestSHA256, 0); err != nil {
		return err
	}
	raw, err := os.ReadFile(manifestName)
	if err != nil {
		return err
	}
	document, err := pinnedModelManifest(raw, pin)
	if err != nil {
		return err
	}
	for _, blob := range append([]modelBlob{document.Config}, document.Layers...) {
		name := "sha256-" + strings.TrimPrefix(blob.Digest, "sha256:")
		if err := copyInput("blobs/"+name, base+"/blobs/"+blob.Digest, filepath.Join(directory, "blobs", name), strings.TrimPrefix(blob.Digest, "sha256:"), blob.Size); err != nil {
			return err
		}
	}
	modelfile, err := modelFile(directory, document)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "Modelfile"), modelfile, 0644)
}

func verifyOfflineModel(root string, pin architecturev2renderer.OfflineModelPin) ([]string, error) {
	directory := ModelDirectory(root, pin)
	manifestName := filepath.Join(directory, "manifest.json")
	if err := SafePath(manifestName); err != nil {
		return nil, err
	}
	info, err := os.Lstat(manifestName)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, errors.New("offline model manifest is missing or unbounded")
	}
	raw, err := os.ReadFile(manifestName)
	if err != nil {
		return nil, err
	}
	document, err := pinnedModelManifest(raw, pin)
	if err != nil {
		return nil, err
	}
	relative := offlineModelDirectory(pin)
	files := []string{relative + "/manifest.json", relative + "/Modelfile"}
	for _, blob := range append([]modelBlob{document.Config}, document.Layers...) {
		name := "blobs/sha256-" + strings.TrimPrefix(blob.Digest, "sha256:")
		file, err := digestArtifact(filepath.Join(directory, filepath.FromSlash(name)), blob.Size)
		if err != nil {
			return nil, err
		}
		if file.SHA256 != strings.TrimPrefix(blob.Digest, "sha256:") || file.Size != blob.Size {
			return nil, errors.New("offline model blob differs from its reviewed manifest")
		}
		files = append(files, relative+"/"+name)
	}
	want, err := modelFile(directory, document)
	if err != nil {
		return nil, err
	}
	actual, err := os.ReadFile(filepath.Join(directory, "Modelfile"))
	if err != nil || !bytes.Equal(actual, want) {
		return nil, errors.New("offline Modelfile differs from the reviewed model closure")
	}
	return files, nil
}

// modelFile contains data only, consumed by Ollama's native create command.
// It never embeds a tag that could trigger a registry pull.
func modelFile(directory string, document modelManifest) ([]byte, error) {
	var result strings.Builder
	modelCount := 0
	for _, blob := range document.Layers {
		name := "sha256-" + strings.TrimPrefix(blob.Digest, "sha256:")
		if blob.MediaType == "application/vnd.ollama.image.model" {
			modelCount++
			fmt.Fprintf(&result, "FROM %s/blobs/%s\n", OfflineModelMount, name)
			continue
		}
		if blob.Size > 1<<20 {
			return nil, errors.New("offline model text metadata is too large")
		}
		raw, err := os.ReadFile(filepath.Join(directory, "blobs", name))
		if err != nil {
			return nil, err
		}
		if bytes.Contains(raw, []byte("\"\"\"")) || bytes.ContainsRune(raw, 0) {
			return nil, errors.New("offline model metadata cannot escape its Modelfile directive")
		}
		switch blob.MediaType {
		case "application/vnd.ollama.image.system", "application/vnd.ollama.image.template", "application/vnd.ollama.image.license":
			directive := strings.ToUpper(strings.TrimPrefix(blob.MediaType, "application/vnd.ollama.image."))
			fmt.Fprintf(&result, "%s \"\"\"%s\"\"\"\n", directive, raw)
		case "application/vnd.ollama.image.params":
			var parameters map[string]json.RawMessage
			if err := json.Unmarshal(raw, &parameters); err != nil {
				return nil, err
			}
			keys := make([]string, 0, len(parameters))
			for key := range parameters {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if strings.ContainsAny(key, " \t\r\n\"\\") || key == "" {
					return nil, errors.New("offline model parameter name is invalid")
				}
				values := []json.RawMessage{parameters[key]}
				if bytes.HasPrefix(bytes.TrimSpace(parameters[key]), []byte("[")) {
					if err := json.Unmarshal(parameters[key], &values); err != nil {
						return nil, err
					}
				}
				for _, value := range values {
					var text string
					if json.Unmarshal(value, &text) == nil {
						fmt.Fprintf(&result, "PARAMETER %s %s\n", key, strconv.Quote(text))
					} else {
						var number json.Number
						if json.Unmarshal(value, &number) != nil {
							return nil, errors.New("offline model parameter must be a scalar")
						}
						fmt.Fprintf(&result, "PARAMETER %s %s\n", key, number)
					}
				}
			}
		default:
			return nil, fmt.Errorf("offline model layer %q is unsupported", blob.MediaType)
		}
	}
	if modelCount != 1 {
		return nil, errors.New("offline model requires exactly one reviewed GGUF weights blob")
	}
	return []byte(result.String()), nil
}

func digestArtifact(name string, limit int64) (BundleFile, error) {
	if err := SafePath(name); err != nil {
		return BundleFile{}, err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return BundleFile{}, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return BundleFile{}, errors.New("offline artifact is not a bounded regular file")
	}
	file, err := os.Open(name)
	if err != nil {
		return BundleFile{}, err
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, file)
	if err != nil {
		return BundleFile{}, err
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(info, after) || count != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return BundleFile{}, errors.New("offline artifact changed during verification")
	}
	return BundleFile{SHA256: hex.EncodeToString(hash.Sum(nil)), Size: count}, nil
}
