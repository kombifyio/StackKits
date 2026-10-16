package engine

import (
	"crypto/rand"
	"encoding/base32"
	"strings"
	"time"

	"github.com/mtalavi/coolify-mirror/internal/coolify"
)

// Version of this tool (set at build time with -ldflags).
var Version = "1.9.3"

// Pinned marks the kombify StackKits fork: the binary never replaces itself
// and reports the upstream commit it is based on (see KOMBIFY-FORK.md).
const Pinned = true

// ForkRevision identifies the kombify changes on top of UpstreamCommit.
const (
	UpstreamCommit = "f46fa093644268759fc0802e7d098e9e45dee984"
	ForkRevision   = "kombify.4"
)

// FormatName identifies the backup layout.
const FormatName = "github.com/mtalavi/coolify-mirror/1"

// Backup modes.
const (
	ModeSelective = "selective"
	ModeFull      = "full"
)

// Entry names inside the archive.
const (
	entryManifest      = "manifest.json"
	entryTransfer      = "coolify/server-transfer.json" // Coolify's own Server Transfer bundle
	entryExport        = "db/export.json"
	entryDump          = "db/coolify.dump"
	entryEnv           = "db/source.env"
	entryDockerConfig  = "extra/docker-config.json"
	entryCustomCompose = "extra/docker-compose.custom.yml"
	prefixPaths        = "paths"
	prefixVolumes      = "volumes"
	prefixImages       = "images"
)

// Manifest describes a backup; it is the first entry of the archive.
type Manifest struct {
	Format      string             `json:"format"`
	ToolVersion string             `json:"tool_version"`
	CreatedAt   time.Time          `json:"created_at"`
	Mode        string             `json:"mode"`
	Source      SourceInfo         `json:"source"`
	Resources   []coolify.Resource `json:"resources"`
	Volumes     []VolumeEntry      `json:"volumes"`
	Paths       []PathEntry        `json:"paths"`
	Images      []ImageEntry       `json:"images"`
	// HostRequirements: fingerprinted host files from backups made by an
	// intermediate build; still validated on restore. New backups record
	// host files in HostDeps (with their SHA-256).
	HostRequirements []HostRequirement `json:"host_requirements,omitempty"`
	// Dumps are PostgreSQL/MySQL/MariaDB databases saved with their own dump
	// tool; their data volumes are stored as definitions only.
	Dumps []DumpEntry `json:"dumps,omitempty"`
	// HasTransferBundle: the archive holds Coolify\'s own Server Transfer
	// bundle (coolify/server-transfer.json) right after this manifest.
	HasTransferBundle bool `json:"has_transfer_bundle,omitempty"`
	// HostDeps are files and buildx builders outside the resources' folders
	// that they need to build or run (see hostdeps.go).
	HostDeps []HostDep      `json:"host_deps,omitempty"`
	Builders []BuilderEntry `json:"builders,omitempty"`
	// Runtime lists, per resource uuid, the compose services that were up
	// (or had finished successfully) on the source; the restored resource is
	// only reported as running when the same services are up here.
	Runtime map[string][]string `json:"runtime,omitempty"`

	Options    BackupOptions `json:"options"`
	TotalBytes int64         `json:"total_bytes"`
	// LocalKeyUUID is the private key used by the "localhost" server (full mode).
	LocalKeyUUID string `json:"local_key_uuid,omitempty"`
	LocalUser    string `json:"local_user,omitempty"`
}

// SourceInfo describes the server the backup was taken on.
type SourceInfo struct {
	Hostname       string `json:"hostname"`
	IPv4           string `json:"ipv4,omitempty"`
	IPv6           string `json:"ipv6,omitempty"`
	CoolifyVersion string `json:"coolify_version"`
	Arch           string `json:"arch"`
}

// VolumeEntry is one saved docker volume.
type VolumeEntry struct {
	Name    string            `json:"name"`
	Driver  string            `json:"driver"`
	Labels  map[string]string `json:"labels,omitempty"`
	Options map[string]string `json:"options,omitempty"`
	Size    int64             `json:"size"`
	Owner   string            `json:"owner,omitempty"` // resource uuid
	// External: the data lives outside the volume (NFS/CIFS or bind driver
	// options); only the definition is stored and recreated.
	External bool `json:"external,omitempty"`
	// Dumped: the volume is a database data directory saved as a native dump
	// (see Manifest.Dumps); it is re-created empty and loaded from the dump.
	Dumped bool `json:"dumped,omitempty"`
}

// PathEntry is one saved host directory or file.
type PathEntry struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Owner string `json:"owner,omitempty"`
}

// ImageEntry is one `docker save` group.
type ImageEntry struct {
	Refs  []string `json:"refs"`
	Size  int64    `json:"size"`
	Owner string   `json:"owner,omitempty"`
}

// HostRequirement describes a root-owned host prerequisite used by a resource.
// Selective restore validates it instead of copying it, preserving the boundary
// between application data and server policy.
type HostRequirement struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Reason string `json:"reason,omitempty"`
}

// BackupOptions records how the backup was taken.
type BackupOptions struct {
	Consistency    string `json:"consistency"`
	Images         string `json:"images"`
	IncludeBackups bool   `json:"include_backups"`
}

// NewPassphrase returns a random 130 bit key, easy to copy (a-z2-7).
func NewPassphrase() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b))
}
