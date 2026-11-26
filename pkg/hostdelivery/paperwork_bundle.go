package hostdelivery

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
	"net/http"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
	"time"
)

const maxPaperworkBundleBytes = 2 << 20

var paperworkSourceSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
var paperworkDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var paperworkVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
var paperworkRegistryName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

var errPaperworkPin = errors.New("Paperwork requires an admitted source, exact registry image, and exact bundle digest and size")

type PaperworkBundlePin struct {
	SourceSHA    string
	Image        string
	BundleSHA256 string
	BundleSize   int64
}

type PaperworkBundle struct {
	Compose []byte
	Version string
}

// RegistryHost returns the registry named by the admitted image reference. The
// public CLI carries no kombify registry endpoint of its own: the private
// caller admits the exact image, and this package uses only the host in it.
func (pin PaperworkBundlePin) RegistryHost() (string, error) {
	name, digest, pinned := strings.Cut(pin.Image, "@")
	if !pinned || !paperworkDigest.MatchString(digest) {
		return "", errPaperworkPin
	}
	host, repository, named := strings.Cut(name, "/")
	if !named || repository != PaperworkRegistryRepository || !paperworkRegistryName.MatchString(host) {
		return "", errPaperworkPin
	}
	return host, nil
}

func (pin PaperworkBundlePin) validate() (string, error) {
	registryHost, err := pin.RegistryHost()
	if err != nil || !paperworkDigest.MatchString(pin.BundleSHA256) || !paperworkSourceSHA.MatchString(pin.SourceSHA) ||
		pin.BundleSize <= 0 || pin.BundleSize > maxPaperworkBundleBytes {
		return "", errPaperworkPin
	}
	return registryHost, nil
}

func FetchPaperworkBundle(ctx context.Context, pin PaperworkBundlePin, registryGrant []byte) (PaperworkBundle, error) {
	registryHost, err := pin.validate()
	if err != nil {
		return PaperworkBundle{}, err
	}
	grant, err := ParsePaperworkRegistryGrant(registryGrant, time.Now(), 30*time.Second)
	if err != nil {
		return PaperworkBundle{}, err
	}
	if !grant.AuthorizesRegistry(registryHost) {
		return PaperworkBundle{}, errors.New("Paperwork registry grant does not authorize the admitted image registry")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://"+registryHost+"/v2/"+PaperworkRegistryRepository+"/blobs/"+pin.BundleSHA256, nil)
	if err != nil {
		return PaperworkBundle{}, errors.New("construct Paperwork registry request")
	}
	request.Header.Set("Authorization", "Bearer "+grant.RegistryToken)
	client := &http.Client{
		Transport: &http.Transport{DisableCompression: true},
		Timeout:   20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return PaperworkBundle{}, errors.New("Paperwork registry download failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return PaperworkBundle{}, fmt.Errorf("Paperwork registry refused bundle download (HTTP %d)", response.StatusCode)
	}
	if (response.ContentLength >= 0 && response.ContentLength != pin.BundleSize) || response.Header.Get("Docker-Content-Digest") != pin.BundleSHA256 ||
		response.Header.Get("Content-Type") != "application/octet-stream" || response.Header.Get("Content-Encoding") != "" {
		return PaperworkBundle{}, errors.New("Paperwork registry response differs from the admitted bundle metadata")
	}
	archive, err := io.ReadAll(io.LimitReader(response.Body, pin.BundleSize+1))
	if err != nil {
		return PaperworkBundle{}, errors.New("read Paperwork registry bundle")
	}
	return verifyPaperworkBundle(pin, archive)
}

func verifyPaperworkBundle(pin PaperworkBundlePin, archive []byte) (PaperworkBundle, error) {
	if _, err := pin.validate(); err != nil {
		return PaperworkBundle{}, err
	}
	if int64(len(archive)) != pin.BundleSize || "sha256:"+paperworkSHA256(archive) != pin.BundleSHA256 {
		return PaperworkBundle{}, errors.New("Paperwork install archive digest or size mismatch")
	}
	compressed := bytes.NewReader(archive)
	stream, err := gzip.NewReader(compressed)
	if err != nil {
		return PaperworkBundle{}, errors.New("Paperwork install archive is not gzip")
	}
	defer stream.Close()
	stream.Multistream(false)
	expanded, err := io.ReadAll(io.LimitReader(stream, maxPaperworkBundleBytes+1))
	if err != nil || len(expanded) > maxPaperworkBundleBytes || compressed.Len() != 0 {
		return PaperworkBundle{}, errors.New("Paperwork install archive expansion is invalid")
	}
	files := map[string][]byte{"compose.yaml": nil, "env.example": nil, "install-receipt.json": nil}
	contents := bytes.NewReader(expanded)
	entries := tar.NewReader(contents)
	for {
		header, err := entries.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return PaperworkBundle{}, errors.New("Paperwork install archive is not a valid tar")
		}
		previous, allowed := files[header.Name]
		if !allowed || previous != nil || header.Format != tar.FormatUSTAR || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > maxPaperworkBundleBytes {
			return PaperworkBundle{}, errors.New("Paperwork install archive has unexpected entries")
		}
		files[header.Name], err = io.ReadAll(entries)
		if err != nil {
			return PaperworkBundle{}, errors.New("Paperwork install archive entry is incomplete")
		}
	}
	if len(bytes.Trim(expanded[len(expanded)-contents.Len():], "\x00")) != 0 {
		return PaperworkBundle{}, errors.New("Paperwork install archive has trailing data")
	}
	var receipt struct {
		Schema        string `json:"schema"`
		SourceSHA     string `json:"source_sha"`
		Version       string `json:"version"`
		Image         string `json:"image"`
		ComposeSHA256 string `json:"compose_sha256"`
		EnvSHA256     string `json:"env_sha256"`
	}
	reader := json.NewDecoder(bytes.NewReader(files["install-receipt.json"]))
	reader.DisallowUnknownFields()
	if reader.Decode(&receipt) != nil || reader.Decode(new(any)) != io.EOF ||
		receipt.Schema != "kombify.paperwork-selfhost-install.v1" || receipt.SourceSHA != pin.SourceSHA || receipt.Image != pin.Image ||
		!paperworkVersion.MatchString(receipt.Version) || len(files["compose.yaml"]) == 0 || len(files["env.example"]) == 0 ||
		receipt.ComposeSHA256 != paperworkSHA256(files["compose.yaml"]) || receipt.EnvSHA256 != paperworkSHA256(files["env.example"]) {
		return PaperworkBundle{}, errors.New("Paperwork install archive receipt differs from the admitted release")
	}
	if !paperworkComposeUsesPin(files["compose.yaml"], pin.Image) {
		return PaperworkBundle{}, errors.New("Paperwork Compose services do not use the admitted image")
	}
	return PaperworkBundle{Compose: files["compose.yaml"], Version: receipt.Version}, nil
}

func paperworkComposeUsesPin(compose []byte, image string) bool {
	var definition struct {
		Services map[string]struct {
			Image string `yaml:"image"`
			Build any    `yaml:"build"`
		} `yaml:"services"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(compose))
	if decoder.Decode(&definition) != nil || decoder.Decode(new(any)) != io.EOF {
		return false
	}
	for _, name := range []string{"generate", "init", "context-issuer", "paperwork"} {
		service := definition.Services[name]
		if service.Image != image || service.Build != nil {
			return false
		}
	}
	return true
}

func paperworkSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
