package nativehost

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

var errWikiPullGrantExpired = errors.New("Wiki pull grant expired; renew registry-auth through Wallet host delivery")

// The account-delivered kombify OCI pull grant is host material. It never reaches
// the Wiki container, its backup volume or the generated plan.
func (o *osStandaloneComposeWorkloadOperations) wikiRegistryFiles(bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) (map[string][]byte, error) {
	ref, ok := bundle.SecretRefs["registry-auth"]
	if !ok || ref != architecturev2renderer.WikiRegistrySecretRef {
		return nil, errors.New("Wiki install needs a node-custodied kombify OCI pull grant in registry-auth")
	}
	material, err := o.workloadSecretMaterial(ref)
	if err != nil {
		return nil, errors.New("Wiki registry-auth custody is unavailable; deliver the server's pull grant through Wallet host delivery")
	}
	defer clear(material)
	if o.preview {
		return map[string][]byte{"files/config.json": []byte(`{"auths":{"api.kombify.io":{"registrytoken":"<custody-reference>"}}}`), "files/wiki-registry-expiry": []byte("2099-01-01T00:00:00Z")}, nil
	}
	var grant struct {
		Repository    string    `json:"repository"`
		RegistryToken string    `json:"registryToken"`
		ExpiresAt     time.Time `json:"expiresAt"`
	}
	decoder := json.NewDecoder(bytes.NewReader(material))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&grant) != nil || decoder.Decode(new(any)) != io.EOF || grant.Repository != "kombiverselabs/kombify-knowledge-node" || len(grant.RegistryToken) < 32 || strings.ContainsAny(grant.RegistryToken, "\x00\r\n ") || grant.ExpiresAt.IsZero() {
		return nil, errors.New("Wiki registry-auth requires a package-scoped kombify OCI registry bearer with an expiry")
	}
	// Docker's native registrytoken goes directly to the registry. A broad
	// account password, login token or credential helper is never accepted.
	config, err := json.Marshal(map[string]any{"auths": map[string]any{"api.kombify.io": map[string]string{
		"registrytoken": grant.RegistryToken,
	}}})
	if err != nil {
		return nil, err
	}
	return map[string][]byte{"files/config.json": config, "files/wiki-registry-expiry": []byte(grant.ExpiresAt.Format(time.RFC3339Nano))}, nil
}

// Only this governed Compose project gets its own Docker pull authority.
// Other applications retain the closed runner's empty user environment.
func wikiRegistryDirectory(args []string, directory string) (string, error) {
	if len(args) < 3 || args[0] != "compose" || !strings.HasPrefix(args[2], "stackkit-wiki-") {
		return "", nil
	}
	if len(args) < 8 {
		return "", errors.New("Wiki Compose command is incomplete")
	}
	// Only up can pull. Readback, stopped-service backup and restart use no
	// registry authority, even after the original grant expires.
	if args[7] != "up" {
		return "", nil
	}
	if filepath.Base(directory) != args[2] {
		return "", errors.New("Wiki registry project differs from its runtime directory")
	}
	root, err := confinedfs.Open(directory)
	if err != nil {
		return "", errors.New("Wiki registry custody is unavailable")
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return "", err
	}
	defer tx.Close()
	data, info, err := tx.ReadStableBounded("files/config.json", 64<<10)
	if err != nil || info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return "", errors.New("Wiki registry custody must be a private regular file")
	}
	defer clear(data)
	var config struct {
		Auths map[string]struct {
			RegistryToken string `json:"registrytoken"`
		} `json:"auths"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || len(config.Auths) != 1 || config.Auths["api.kombify.io"].RegistryToken == "" {
		return "", errors.New("Wiki registry custody admits only the kombify OCI pull grant")
	}
	expiry, expiryInfo, err := tx.ReadStableBounded("files/wiki-registry-expiry", 128)
	if err != nil || expiryInfo == nil || !expiryInfo.Mode().IsRegular() || expiryInfo.Mode().Perm() != 0o600 {
		return "", errors.New("Wiki registry expiry custody is unavailable")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, string(expiry))
	if err != nil {
		return "", errors.New("Wiki registry expiry is invalid")
	}
	if !expiresAt.After(time.Now()) {
		return "", errWikiPullGrantExpired
	}
	return filepath.Join(directory, "files"), nil
}

// Expiry prevents registry access, while Docker can still use its exact pinned
// local image. A missing local image fails without making a registry request.
func wikiRegistryCommand(args []string, directory string) ([]string, string, error) {
	registry, err := wikiRegistryDirectory(args, directory)
	if errors.Is(err, errWikiPullGrantExpired) {
		if len(args) < 10 || args[8] != "--pull" || args[9] != "never" {
			args = slices.Insert(slices.Clone(args), 8, "--pull", "never")
		}
		return args, "", nil
	}
	return args, registry, err
}
