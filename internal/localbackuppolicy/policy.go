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

	// CoreModuleRef and CoreLiteModuleRef are the only runtime profiles that
	// may own the local Kopia source. An empty Source.CoreModuleRef is retained
	// solely for decoding pre-profile Full-Core policy artifacts.
	CoreModuleRef     = "stackkits-basement-core-runtime"
	CoreLiteModuleRef = "stackkits-basement-core-lite-runtime"

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
	DockerDaemonRef          = "docker-default"
	DockerDaemonEngine       = "docker"
	DockerDaemonSocketPath   = "/var/run/docker.sock"
	HostScope                = "exclusive-stack-per-daemon"
	canonicalNewline         = byte('\n')
)

var (
	contractIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]*$`)
	imageDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

var managedVolumeNames = []string{
	"stackkit-basement-core_coolify-applications",
	"stackkit-basement-core_coolify-backups",
	"stackkit-basement-core_coolify-data",
	"stackkit-basement-core_coolify-databases",
	"stackkit-basement-core_coolify-postgres-data",
	"stackkit-basement-core_coolify-redis-data",
	"stackkit-basement-core_coolify-services",
	"stackkit-basement-core_coolify-ssh",
	"stackkit-basement-core_pocketid-data",
	"stackkit-basement-core_step-ca-db",
	"stackkit-basement-core_tinyauth-data",
}

var managedLiteVolumeNames = []string{
	"stackkit-basement-core_pocketid-data",
	"stackkit-basement-core_step-ca-db",
	"stackkit-basement-core_tinyauth-data",
}

type coreSourceProfile struct {
	managedVolumeNames []string
}

func coreProfile(coreModuleRef string) (coreSourceProfile, error) {
	switch coreModuleRef {
	case "", CoreModuleRef:
		return coreSourceProfile{managedVolumeNames: managedVolumeNames}, nil
	case CoreLiteModuleRef:
		return coreSourceProfile{managedVolumeNames: managedLiteVolumeNames}, nil
	default:
		return coreSourceProfile{}, fmt.Errorf("local Kopia source coreModuleRef %q is not a supported Core profile", coreModuleRef)
	}
}

// Document is the versioned on-disk local Kopia runtime policy artifact.
type Document struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Policy     Policy `json:"policy"`
}

// Policy binds the governed local Kopia runtime to one resolved Stack target.
type Policy struct {
	StackID            string              `json:"stackId"`
	Target             Target              `json:"target"`
	Runtime            Runtime             `json:"runtime"`
	Source             Source              `json:"source"`
	Retention          *Retention          `json:"retention,omitempty"`
	Schedule           *Schedule           `json:"schedule,omitempty"`
	RecoveryObjectives []RecoveryObjective `json:"recoveryObjectives,omitempty"`
}

// Schedule is the resolved foundation.#BackupScheduleV1 UTC intent. It grants
// no execution authority; local scheduling requires separate Owner approval.
// Nil preserves historical policies that have no executable schedule intent.
type Schedule struct {
	Cadence       string `json:"cadence"`
	MinuteUTC     int    `json:"minuteUTC"`
	HourUTC       *int   `json:"hourUTC,omitempty"`
	WeekdayUTC    string `json:"weekdayUTC,omitempty"`
	JitterSeconds int    `json:"jitterSeconds"`
}

func (schedule Schedule) Validate() error {
	if schedule.MinuteUTC < 0 || schedule.MinuteUTC > 59 || schedule.JitterSeconds < 0 || schedule.JitterSeconds > 1800 {
		return errors.New("backup schedule is outside the governed UTC ranges")
	}
	if schedule.Cadence == "hourly" {
		if schedule.HourUTC != nil || schedule.WeekdayUTC != "" {
			return errors.New("hourly backup schedule cannot declare an hour or weekday")
		}
		return nil
	}
	if schedule.HourUTC == nil || *schedule.HourUTC < 0 || *schedule.HourUTC > 23 {
		return errors.New("backup schedule requires a governed UTC hour")
	}
	switch schedule.Cadence {
	case "daily":
		if schedule.WeekdayUTC != "" {
			return errors.New("daily backup schedule cannot declare a weekday")
		}
	case "weekly":
		switch schedule.WeekdayUTC {
		case "sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday":
		default:
			return errors.New("weekly backup schedule requires a governed UTC weekday")
		}
	default:
		return errors.New("backup schedule requires daily, hourly or weekly cadence")
	}
	return nil
}

// MaximumTriggerIntervalSeconds bounds adjacent scheduled UTC trigger slots,
// including jitter. It does not include backup duration or prove an RPO.
func (schedule Schedule) MaximumTriggerIntervalSeconds() (int, error) {
	if err := schedule.Validate(); err != nil {
		return 0, err
	}
	interval := 60 * 60
	switch schedule.Cadence {
	case "daily":
		interval *= 24
	case "weekly":
		interval *= 24 * 7
	}
	return interval + schedule.JitterSeconds, nil
}

// Retention carries the resolved foundation.#BackupRetentionV1 contract.
// Defaults are supplied by CUE. Nil preserves the historical manual policy;
// it must never be interpreted as a newly selected retention schedule.
type Retention struct {
	KeepDaily   int `json:"keepDaily"`
	KeepWeekly  int `json:"keepWeekly"`
	KeepMonthly int `json:"keepMonthly"`
	KeepYearly  int `json:"keepYearly"`
}

// Validate bounds the runtime lowering of an already CUE-resolved policy.
func (retention Retention) Validate() error {
	if retention.KeepDaily < 1 || retention.KeepDaily > 365 ||
		retention.KeepWeekly < 0 || retention.KeepWeekly > 104 ||
		retention.KeepMonthly < 0 || retention.KeepMonthly > 60 ||
		retention.KeepYearly < 0 || retention.KeepYearly > 10 {
		return errors.New("backup retention is outside the governed CUE ranges")
	}
	return nil
}

type Target struct {
	SiteRef          string `json:"siteRef"`
	NodeRef          string `json:"nodeRef"`
	DaemonRef        string `json:"daemonRef,omitempty"`
	DaemonEngine     string `json:"daemonEngine,omitempty"`
	DaemonSocketPath string `json:"daemonSocketPath,omitempty"`
	HostScope        string `json:"hostScope,omitempty"`
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
	Kind                string               `json:"kind"`
	HostPath            string               `json:"hostPath"`
	ContainerPath       string               `json:"containerPath"`
	ReadOnly            bool                 `json:"readOnly"`
	CoreModuleRef       string               `json:"coreModuleRef,omitempty"`
	ManagedVolumeNames  []string             `json:"managedVolumeNames"`
	ApplicationVolumes  []ApplicationVolume  `json:"applicationVolumes,omitempty"`
	ApplicationRuntimes []ApplicationRuntime `json:"applicationRuntimes,omitempty"`
	ExcludePaths        []string             `json:"excludePaths"`
	RepositoryPath      string               `json:"repositoryPath"`
	ConfigPath          string               `json:"configPath"`
	CachePath           string               `json:"cachePath"`
	Custody             string               `json:"custody"`
	RuntimeMaterial     string               `json:"runtimeMaterial"`
}

// NewWithApplicationVolumes returns the governed local Kopia policy with the
// selected Standalone-Compose application volumes attached to the target.
// Application records remain in the signed policy artifact so the runtime and
// restore authorities retain the CUE-derived ownership binding.
func NewWithApplicationVolumes(stackID, siteRef, nodeRef string, applicationVolumes []ApplicationVolume) (Policy, error) {
	return NewWithApplicationVolumesAndRuntimes(stackID, siteRef, nodeRef, applicationVolumes, nil)
}

// NewWithApplicationVolumesAndRuntimes returns the governed policy with the
// exact selected application volumes and their compiler-owned runtime graphs.
func NewWithApplicationVolumesAndRuntimes(stackID, siteRef, nodeRef string, applicationVolumes []ApplicationVolume, applicationRuntimes []ApplicationRuntime) (Policy, error) {
	source, err := GovernedSourceWithApplicationVolumesAndRuntimes(applicationVolumes, applicationRuntimes)
	if err != nil {
		return Policy{}, err
	}
	policy := Policy{
		StackID: stackID,
		Target:  governedTarget(siteRef, nodeRef),
		Runtime: GovernedRuntime(),
		Source:  source,
	}
	if err := policy.validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// NewWithApplicationVolumesAndRuntimesForCoreModule returns a policy bound to
// one of the finite local Core runtime profiles. The profile controls the
// governed Core volume set; application volumes remain compiler-selected.
// Full-Core intentionally retains the fieldless legacy wire encoding so a
// profile-aware regeneration does not change an otherwise identical artifact;
// CoreLite remains explicitly identified in the source.
func NewWithApplicationVolumesAndRuntimesForCoreModule(coreModuleRef, stackID, siteRef, nodeRef string, applicationVolumes []ApplicationVolume, applicationRuntimes []ApplicationRuntime) (Policy, error) {
	source, err := governedSourceWithApplicationVolumesAndRuntimes(coreModuleRef, coreModuleRef == CoreLiteModuleRef, applicationVolumes, applicationRuntimes)
	if err != nil {
		return Policy{}, err
	}
	policy := Policy{
		StackID: stackID,
		Target:  governedTarget(siteRef, nodeRef),
		Runtime: GovernedRuntime(),
		Source:  source,
	}
	if err := policy.validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// New returns the exact governed policy for one resolved Basement target.
func New(stackID, siteRef, nodeRef string) (Policy, error) {
	policy := Policy{
		StackID: stackID,
		Target:  governedTarget(siteRef, nodeRef),
		Runtime: GovernedRuntime(),
		Source:  GovernedSource(),
	}
	if err := policy.validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

// NewForCoreModule returns a policy bound to one of the finite local Core
// runtime profiles. Full-Core uses the pre-profile fieldless encoding for
// compatibility; CoreLite carries its explicit module binding.
func NewForCoreModule(coreModuleRef, stackID, siteRef, nodeRef string) (Policy, error) {
	return NewWithApplicationVolumesAndRuntimesForCoreModule(coreModuleRef, stackID, siteRef, nodeRef, nil, nil)
}

func governedTarget(siteRef, nodeRef string) Target {
	return Target{
		SiteRef: siteRef, NodeRef: nodeRef,
		DaemonRef: DockerDaemonRef, DaemonEngine: DockerDaemonEngine,
		DaemonSocketPath: DockerDaemonSocketPath, HostScope: HostScope,
	}
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
	return governedSource(CoreModuleRef, false)
}

// GovernedSourceForCoreModule returns the exact source for a supported Core
// runtime profile. New artifacts carry the selected module explicitly so the
// source allowlist cannot be widened by a consumer.
func GovernedSourceForCoreModule(coreModuleRef string) (Source, error) {
	if _, err := coreProfile(coreModuleRef); err != nil {
		return Source{}, err
	}
	return governedSource(coreModuleRef, true), nil
}

func governedSource(coreModuleRef string, explicit bool) Source {
	profile, _ := coreProfile(coreModuleRef)
	source := Source{
		Kind:               "docker-volume-root",
		HostPath:           "/var/lib/docker/volumes",
		ContainerPath:      SourcePath,
		ReadOnly:           true,
		ManagedVolumeNames: append([]string(nil), profile.managedVolumeNames...),
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
	if explicit {
		source.CoreModuleRef = coreModuleRef
	}
	return source
}

func governedSourceForRef(coreModuleRef string) (Source, error) {
	if coreModuleRef == "" {
		return GovernedSource(), nil
	}
	return GovernedSourceForCoreModule(coreModuleRef)
}

// GovernedSourceWithApplicationVolumes extends the CUE-owned Core source with
// exactly the selected application volumes. The static Core list and
// exclusion topology remain unchanged.
func GovernedSourceWithApplicationVolumes(applicationVolumes []ApplicationVolume) (Source, error) {
	return GovernedSourceWithApplicationVolumesAndRuntimes(applicationVolumes, nil)
}

// GovernedSourceWithApplicationVolumesAndRuntimes extends the CUE-owned Core
// source with selected application volumes and their closed runtime graphs.
func GovernedSourceWithApplicationVolumesAndRuntimes(applicationVolumes []ApplicationVolume, applicationRuntimes []ApplicationRuntime) (Source, error) {
	return governedSourceWithApplicationVolumesAndRuntimes(CoreModuleRef, false, applicationVolumes, applicationRuntimes)
}

// GovernedSourceWithApplicationVolumesAndRuntimesForCoreModule extends the
// selected finite Core profile with compiler-owned application volumes and
// runtime graphs.
func GovernedSourceWithApplicationVolumesAndRuntimesForCoreModule(coreModuleRef string, applicationVolumes []ApplicationVolume, applicationRuntimes []ApplicationRuntime) (Source, error) {
	return governedSourceWithApplicationVolumesAndRuntimes(coreModuleRef, true, applicationVolumes, applicationRuntimes)
}

func governedSourceWithApplicationVolumesAndRuntimes(coreModuleRef string, explicit bool, applicationVolumes []ApplicationVolume, applicationRuntimes []ApplicationRuntime) (Source, error) {
	applications, err := canonicalApplicationVolumes(applicationVolumes)
	if err != nil {
		return Source{}, err
	}
	runtimes, err := canonicalApplicationRuntimes(applicationRuntimes)
	if err != nil {
		return Source{}, err
	}
	if err := validateApplicationProjection(applications, runtimes); err != nil {
		return Source{}, err
	}
	if _, err := coreProfile(coreModuleRef); err != nil {
		return Source{}, err
	}
	source := governedSource(coreModuleRef, explicit)
	if len(applications) == 0 && len(runtimes) == 0 {
		return source, nil
	}
	source.ApplicationVolumes = applications
	source.ApplicationRuntimes = runtimes
	source.ManagedVolumeNames = append(source.ManagedVolumeNames, applicationVolumeNames(applications)...)
	return source, nil
}

// ManagedVolumeNames returns the exact Compose-qualified persistent volume
// allowlist that the local Kopia source may observe. Repository, cache, and
// restore-staging volumes are intentionally absent.
func ManagedVolumeNames() []string {
	return append([]string(nil), managedVolumeNames...)
}

// ManagedVolumeNamesForCoreModule returns the exact profile-owned Core names.
func ManagedVolumeNamesForCoreModule(coreModuleRef string) ([]string, error) {
	profile, err := coreProfile(coreModuleRef)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), profile.managedVolumeNames...), nil
}

// ManagedVolumeNamesWithApplicationVolumes returns the canonical Core names
// followed by selected application volume names in stable order.
func ManagedVolumeNamesWithApplicationVolumes(applicationVolumes []ApplicationVolume) ([]string, error) {
	source, err := GovernedSourceWithApplicationVolumes(applicationVolumes)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), source.ManagedVolumeNames...), nil
}

// ManagedVolumeNamesWithApplicationVolumesForCoreModule returns the exact
// selected Core profile names followed by compiler-owned application names.
func ManagedVolumeNamesWithApplicationVolumesForCoreModule(coreModuleRef string, applicationVolumes []ApplicationVolume) ([]string, error) {
	source, err := GovernedSourceWithApplicationVolumesAndRuntimesForCoreModule(coreModuleRef, applicationVolumes, nil)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), source.ManagedVolumeNames...), nil
}

// SourceDigest binds restore execution to the exact historical/current volume
// selection without coupling it to unrelated runtime-policy revisions.
func SourceDigest(source Source) (string, error) {
	if expected, ok := recognizedSnapshotSource(source.CoreModuleRef, source.ContainerPath, source.ExcludePaths); ok {
		source.ExcludePaths = append([]string(nil), expected.ExcludePaths...)
	}
	if err := validateSourceProjection(source); err != nil {
		return "", err
	}
	// Full-Core's new explicit profile marker is metadata for selecting the
	// same governed source, not a new physical source identity. Keep it out of
	// the digest while preserving CoreLite's distinct marker and volume set.
	if source.CoreModuleRef == CoreModuleRef {
		source.CoreModuleRef = ""
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return "", fmt.Errorf("marshal local Kopia source digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// IsRecognizedSnapshotSelection accepts the current governed backup selection
// and the immediately preceding v1 selection. The latter did not mention the
// restore-staging volume because that volume did not exist yet. Keeping this
// narrow compatibility rule allows pre-restore anchors to remain usable while
// every new snapshot excludes staged restore bytes.
func IsRecognizedSnapshotSelection(containerPath string, excludes []string) bool {
	return IsRecognizedSnapshotSelectionForCoreModule("", containerPath, excludes)
}

// IsRecognizedSnapshotSelectionForCoreModule accepts the current or
// pre-staging selection for one supported Core profile. Empty moduleRef is the
// legacy Full-Core encoding and is intentionally never inferred as Lite.
func IsRecognizedSnapshotSelectionForCoreModule(coreModuleRef, containerPath string, excludes []string) bool {
	_, ok := recognizedSnapshotSource(coreModuleRef, containerPath, excludes)
	return ok
}

func recognizedSnapshotSource(coreModuleRef, containerPath string, excludes []string) (Source, bool) {
	current, err := governedSourceForRef(coreModuleRef)
	if err != nil || containerPath != current.ContainerPath {
		return Source{}, false
	}
	if reflect.DeepEqual(excludes, current.ExcludePaths) {
		return current, true
	}
	legacy := current.ExcludePaths[:len(current.ExcludePaths)-1]
	if reflect.DeepEqual(excludes, legacy) {
		return current, true
	}
	return Source{}, false
}

// ValidateSnapshotPolicy verifies a policy embedded in owner-signed snapshot
// evidence. New policy artifacts remain exact-current through Decode; this
// verifier additionally recognizes the pre-staging v1 snapshot selection so
// upgrades do not invalidate existing anchors.
func ValidateSnapshotPolicy(policy Policy) error {
	if policy.Schedule != nil {
		if err := policy.Schedule.Validate(); err != nil {
			return err
		}
	}
	if policy.Retention != nil {
		if err := policy.Retention.Validate(); err != nil {
			return err
		}
	}
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
	if !legacySnapshotTarget(policy.Target) && policy.Target != governedTarget(policy.Target.SiteRef, policy.Target.NodeRef) {
		return errors.New("local Kopia snapshot policy daemon target is not recognized")
	}
	source := policy.Source
	current, recognized := recognizedSnapshotSource(source.CoreModuleRef, source.ContainerPath, source.ExcludePaths)
	if !recognized {
		return errors.New("local Kopia snapshot policy selection is not recognized")
	}
	source.ExcludePaths = append([]string(nil), current.ExcludePaths...)
	if err := validateSource(source, policy.Target); err != nil {
		return fmt.Errorf("local Kopia snapshot policy data topology is not recognized: %w", err)
	}
	if err := policy.validateRecoveryObjectives(); err != nil {
		return err
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

func legacySnapshotTarget(target Target) bool {
	return target.DaemonRef == "" && target.DaemonEngine == "" && target.DaemonSocketPath == "" && target.HostScope == ""
}

// SourceProjection returns a detached source projection suitable for lifecycle
// translation without allowing callers to mutate the decoded authority.
func (policy Policy) SourceProjection() Source {
	source := policy.Source
	source.ManagedVolumeNames = append([]string(nil), policy.Source.ManagedVolumeNames...)
	source.ApplicationVolumes = cloneApplicationVolumes(policy.Source.ApplicationVolumes)
	source.ApplicationRuntimes = cloneApplicationRuntimes(policy.Source.ApplicationRuntimes)
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
	if policy.Schedule != nil {
		if err := policy.Schedule.Validate(); err != nil {
			return err
		}
	}
	if policy.Retention != nil {
		if err := policy.Retention.Validate(); err != nil {
			return err
		}
	}
	if err := policy.validateRecoveryObjectives(); err != nil {
		return err
	}
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
	if policy.Target != governedTarget(policy.Target.SiteRef, policy.Target.NodeRef) {
		return errors.New("local Kopia runtime policy target differs from the governed exclusive rootful Docker daemon")
	}
	if !reflect.DeepEqual(policy.Runtime, GovernedRuntime()) {
		return errors.New("local Kopia runtime policy runtime differs from the governed local runtime")
	}
	if err := validateSource(policy.Source, policy.Target); err != nil {
		return fmt.Errorf("local Kopia runtime policy source differs from the governed read-only source: %w", err)
	}
	return nil
}
