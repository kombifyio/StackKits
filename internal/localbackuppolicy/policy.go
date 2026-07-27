// Package localbackuppolicy owns the secret-free, generated contract between
// the Basement renderer and the native local backup lifecycle.
package localbackuppolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"
)

const (
	APIVersion = "stackkit.local-kopia-runtime-policy/v1"
	Kind       = "LocalKopiaRuntimePolicy"

	ServiceRef          = "kopia-agent"
	Hostname            = "stackkit-basement-backup"
	ImageRef            = "docker.io/kopia/kopia:0.18.2"
	ImageDigest         = "sha256:b6cb1f09a5fa832a320ee06d7803e82cdd7f69ac6f61d76a0d55fbbf1495c043"
	Mode                = "idle-until-owner-command"
	NetworkMode         = "internal-no-peer"
	NetworkRef          = "basement-backup"
	RequiredEvidenceRef = "basement-core-runtime-evidence"

	SourcePath               = "/source/docker-volumes"
	RepositoryPath           = "/app/repository"
	ConfigPath               = "/app/config"
	CachePath                = "/app/cache"
	RestoreStagingPath       = "/restore-staging"
	RestoreStagingMode       = "isolated-named-volume"
	RestoreStagingSourcePath = "/source/docker-volumes/stackkit-basement-core_kopia-restore-staging/_data"
	Custody                  = "owner-local"
	RuntimeMaterial          = "owner-command"
	canonicalNewline         = byte('\n')
)

var (
	contractIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)
	imageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

// Document is the versioned on-disk local Kopia runtime policy artifact.
type Document struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Policy     Policy `json:"policy"`
}

// Policy binds the governed local Kopia runtime to one resolved Stack target.
type Policy struct {
	StackID string  `json:"stackId"`
	Target  Target  `json:"target"`
	Runtime Runtime `json:"runtime"`
	Source  Source  `json:"source"`
}

type Target struct {
	SiteRef string `json:"siteRef"`
	NodeRef string `json:"nodeRef"`
}

type Runtime struct {
	ServiceRef          string   `json:"serviceRef"`
	Hostname            string   `json:"hostname"`
	Image               string   `json:"image"`
	Mode                string   `json:"mode"`
	NetworkMode         string   `json:"networkMode"`
	NetworkRef          string   `json:"networkRef"`
	HealthCommand       []string `json:"healthCommand"`
	RequiredEvidenceRef string   `json:"requiredEvidenceRef"`
}

type Source struct {
	Kind            string   `json:"kind"`
	HostPath        string   `json:"hostPath"`
	ContainerPath   string   `json:"containerPath"`
	ReadOnly        bool     `json:"readOnly"`
	ExcludePaths    []string `json:"excludePaths"`
	RepositoryPath  string   `json:"repositoryPath"`
	ConfigPath      string   `json:"configPath"`
	CachePath       string   `json:"cachePath"`
	Custody         string   `json:"custody"`
	RuntimeMaterial string   `json:"runtimeMaterial"`
}

// New returns the exact governed policy for one resolved Basement target.
func New(stackID, siteRef, nodeRef string) (Policy, error) {
	policy := Policy{
		StackID: stackID,
		Target: Target{
			SiteRef: siteRef,
			NodeRef: nodeRef,
		},
		Runtime: GovernedRuntime(),
		Source:  GovernedSource(),
	}
	if err := policy.validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// RestorePathForOperation returns the non-caller-controlled staging directory
// for one portable lifecycle operation ID.
func RestorePathForOperation(operationID string) string {
	sum := sha256.Sum256([]byte(operationID))
	return RestoreStagingPath + "/" + hex.EncodeToString(sum[:])
}

// GovernedRuntime returns a detached copy of the exact local Kopia runtime.
func GovernedRuntime() Runtime {
	return Runtime{
		ServiceRef:          ServiceRef,
		Hostname:            Hostname,
		Image:               ImageRef + "@" + ImageDigest,
		Mode:                Mode,
		NetworkMode:         NetworkMode,
		NetworkRef:          NetworkRef,
		HealthCommand:       []string{"kopia", "--version"},
		RequiredEvidenceRef: RequiredEvidenceRef,
	}
}

// GovernedSource returns a detached copy of the exact read-only backup source.
// Exclusion order is a canonical contract: repository, config, then cache.
func GovernedSource() Source {
	return Source{
		Kind:          "docker-volume-root",
		HostPath:      "/var/lib/docker/volumes",
		ContainerPath: SourcePath,
		ReadOnly:      true,
		ExcludePaths: []string{
			"/source/docker-volumes/stackkit-basement-core_kopia-repository/_data",
			"/source/docker-volumes/stackkit-basement-core_kopia-config/_data",
			"/source/docker-volumes/stackkit-basement-core_kopia-cache/_data",
			RestoreStagingSourcePath,
		},
		RepositoryPath:  RepositoryPath,
		ConfigPath:      ConfigPath,
		CachePath:       CachePath,
		Custody:         Custody,
		RuntimeMaterial: RuntimeMaterial,
	}
}

// IsRecognizedSnapshotSelection accepts the current governed backup selection
// and the immediately preceding v1 selection. The latter did not mention the
// restore-staging volume because that volume did not exist yet. Keeping this
// narrow compatibility rule allows pre-restore anchors to remain usable while
// every new snapshot excludes staged restore bytes.
func IsRecognizedSnapshotSelection(containerPath string, excludes []string) bool {
	current := GovernedSource()
	if containerPath != current.ContainerPath {
		return false
	}
	if reflect.DeepEqual(excludes, current.ExcludePaths) {
		return true
	}
	legacy := current.ExcludePaths[:len(current.ExcludePaths)-1]
	return reflect.DeepEqual(excludes, legacy)
}

// ValidateSnapshotPolicy verifies a policy embedded in owner-signed snapshot
// evidence. New policy artifacts remain exact-current through Decode; this
// verifier additionally recognizes the pre-staging v1 snapshot selection so
// upgrades do not invalidate existing anchors.
func ValidateSnapshotPolicy(policy Policy) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "stackId", value: policy.StackID},
		{name: "target.siteRef", value: policy.Target.SiteRef},
		{name: "target.nodeRef", value: policy.Target.NodeRef},
	} {
		if !contractIDPattern.MatchString(field.value) {
			return fmt.Errorf("local Kopia runtime policy %s must be a non-empty portable contract ID", field.name)
		}
	}
	current := GovernedSource()
	source := policy.Source
	if !IsRecognizedSnapshotSelection(source.ContainerPath, source.ExcludePaths) {
		return errors.New("local Kopia snapshot policy selection is not recognized")
	}
	source.ExcludePaths = append([]string(nil), current.ExcludePaths...)
	if !reflect.DeepEqual(source, current) {
		return errors.New("local Kopia snapshot policy data topology is not recognized")
	}
	runtime := policy.Runtime
	imageName, imageDigest, imagePinned := strings.Cut(runtime.Image, "@")
	if runtime.ServiceRef != ServiceRef ||
		runtime.Hostname != Hostname ||
		!imagePinned ||
		!strings.HasPrefix(imageName, "docker.io/kopia/kopia:") ||
		!imageDigestPattern.MatchString(imageDigest) ||
		runtime.Mode != Mode ||
		runtime.NetworkMode != NetworkMode ||
		runtime.NetworkRef != NetworkRef ||
		!reflect.DeepEqual(runtime.HealthCommand, []string{"kopia", "--version"}) ||
		runtime.RequiredEvidenceRef != RequiredEvidenceRef {
		return errors.New("local Kopia snapshot policy runtime is not recognized")
	}
	return nil
}

// SourceProjection returns a detached source projection suitable for lifecycle
// translation without allowing callers to mutate the decoded authority.
func (policy Policy) SourceProjection() Source {
	source := policy.Source
	source.ExcludePaths = append([]string(nil), policy.Source.ExcludePaths...)
	return source
}

// RuntimeProjection returns a detached runtime projection suitable for
// execution adapters without allowing callers to mutate the decoded authority.
func (policy Policy) RuntimeProjection() Runtime {
	runtime := policy.Runtime
	runtime.HealthCommand = append([]string(nil), policy.Runtime.HealthCommand...)
	return runtime
}

// ArtifactBytes returns the single canonical artifact representation. The
// renderer contract includes exactly one final LF; Decode rejects its absence,
// CRLF, blank lines, and all other whitespace variations.
func ArtifactBytes(policy Policy) ([]byte, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(Document{APIVersion: APIVersion, Kind: Kind, Policy: policy})
	if err != nil {
		return nil, fmt.Errorf("marshal local Kopia runtime policy: %w", err)
	}
	return append(raw, canonicalNewline), nil
}

// Decode verifies strict JSON shape, governed values, and byte-for-byte
// canonical encoding before returning a policy.
func Decode(raw []byte) (Policy, error) {
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Policy{}, fmt.Errorf("decode local Kopia runtime policy: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Policy{}, errors.New("decode local Kopia runtime policy: multiple JSON values")
		}
		return Policy{}, fmt.Errorf("decode local Kopia runtime policy trailing data: %w", err)
	}
	if document.APIVersion != APIVersion || document.Kind != Kind {
		return Policy{}, fmt.Errorf("local Kopia runtime policy must use %s and %s", APIVersion, Kind)
	}
	canonical, err := ArtifactBytes(document.Policy)
	if err != nil {
		return Policy{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return Policy{}, errors.New("local Kopia runtime policy is not the canonical artifact encoding with one final LF")
	}
	return document.Policy, nil
}

// Digest verifies the artifact before returning its content-addressed SHA-256.
func Digest(raw []byte) (string, error) {
	if _, err := Decode(raw); err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (policy Policy) validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "stackId", value: policy.StackID},
		{name: "target.siteRef", value: policy.Target.SiteRef},
		{name: "target.nodeRef", value: policy.Target.NodeRef},
	} {
		if !contractIDPattern.MatchString(field.value) {
			return fmt.Errorf("local Kopia runtime policy %s must be a non-empty portable contract ID", field.name)
		}
	}
	if !reflect.DeepEqual(policy.Runtime, GovernedRuntime()) {
		return errors.New("local Kopia runtime policy runtime differs from the governed local runtime")
	}
	if !reflect.DeepEqual(policy.Source, GovernedSource()) {
		return errors.New("local Kopia runtime policy source differs from the governed read-only source")
	}
	return nil
}
