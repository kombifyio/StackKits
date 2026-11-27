package attackpath

import (
	"fmt"
	"time"
)

const (
	// ReportSchemaVersion identifies the serialized Report.
	ReportSchemaVersion = "stackkit.attack-path-report/v1"

	// MaxFreshness bounds how long any section stays admissible. A producer
	// cannot widen it (ADR 0043 "Freshness and absence").
	MaxFreshness = 24 * time.Hour
	// MaxClockSkew bounds how far an observation may claim to lie ahead of the
	// evaluation clock before it is rejected.
	MaxClockSkew = 5 * time.Minute

	// MaxContainers, MaxMounts, MaxRoutes, MaxPaths and MaxDepth bound the
	// facts read and the paths reported. Overflow is unknown, not closed.
	MaxContainers = 256
	MaxMounts     = 64
	MaxRoutes     = 512
	MaxPaths      = 32
	MaxDepth      = 6
)

// Basis says how an edge is known.
type Basis string

const (
	// BasisObserved is read from the running host or a runtime inspection.
	BasisObserved Basis = "observed"
	// BasisConfigured is read from declared configuration (the route table,
	// the backup custody record) and says nothing about the live host.
	BasisConfigured Basis = "configured"
	// BasisInferred follows from a fixed operating-system rule, such as root
	// reading any local file. It is the weakest basis.
	BasisInferred Basis = "inferred"
)

// EdgeState is what the evidence says about one step of a path.
type EdgeState string

const (
	// EdgeOpen means fresh evidence shows the step is possible.
	EdgeOpen EdgeState = "open"
	// EdgeClosed means fresh evidence shows the step is not possible.
	EdgeClosed EdgeState = "closed"
	// EdgeUnknown means the evidence is missing, stale or unreadable.
	EdgeUnknown EdgeState = "unknown"
)

// Node identities. They are stable and carry no secret.
const (
	NodeNetwork  = "network"
	NodeHostUser = "host:user"
	NodeHostRoot = "host:root"
)

// Mechanisms are the fixed rules the graph is built from. None is a heuristic
// and none is discovered by probing.
const (
	MechanismPublicRoute     = "public_route_without_login"
	MechanismRouteBackend    = "route_backend"
	MechanismPublishedPort   = "published_port"
	MechanismDockerSocket    = "docker_socket_mount"
	MechanismPrivileged      = "privileged_container"
	MechanismCapability      = "dangerous_capability"
	MechanismHostRootMount   = "host_root_mount"
	MechanismCredentialMount = "backup_credential_mount"
	MechanismSSHPassword     = "ssh_password_login"
	MechanismSSHRootLogin    = "ssh_root_login"
	MechanismPrivilegeGain   = "local_privilege_gain"
	MechanismRootReadsBackup = "root_reads_backup_credential"
	mechanismSeparator       = ">"
	backupNodePrefix         = "backup:"
	containerNodePrefix      = "container:"
	routeNodePrefix          = "route:"
)

// Provenance names where a section came from and how long it stays valid.
type Provenance struct {
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// usable reports whether the section is admissible at now: it has a source,
// an observation time that is not in the future beyond the skew bound, and an
// expiry (clamped to MaxFreshness) that has not passed.
func (p Provenance) usable(now time.Time) bool {
	if p.Source == "" || p.ObservedAt.IsZero() || p.ExpiresAt.IsZero() {
		return false
	}
	if p.ObservedAt.After(now.Add(MaxClockSkew)) {
		return false
	}
	return now.Before(p.effectiveExpiry())
}

func (p Provenance) effectiveExpiry() time.Time {
	limit := p.ObservedAt.Add(MaxFreshness)
	if p.ExpiresAt.After(limit) {
		return limit
	}
	return p.ExpiresAt
}

// Edge is one step an attacker could take, with the evidence behind it.
type Edge struct {
	ID         string    `json:"id"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	Mechanism  string    `json:"mechanism"`
	State      EdgeState `json:"state"`
	Basis      Basis     `json:"basis,omitempty"`
	Source     string    `json:"source"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	Detail     string    `json:"detail"`
}

func newEdge(from, to, mechanism string) Edge {
	return Edge{ID: from + mechanismSeparator + mechanism + mechanismSeparator + to, From: from, To: to, Mechanism: mechanism, State: EdgeUnknown}
}

// PathStatus says how far the evidence carries a path.
type PathStatus string

const (
	// PathConfirmed means every edge is open on fresh evidence.
	PathConfirmed PathStatus = "confirmed"
	// PathUnresolved means exactly one edge is unknown and all others are
	// open. It is a question for the owner, not a finding.
	PathUnresolved PathStatus = "unresolved"
)

// Path is one chain from the network to a consequence.
type Path struct {
	ID          string     `json:"id"`
	Status      PathStatus `json:"status"`
	Target      string     `json:"target"`
	Basis       Basis      `json:"basis"`
	EdgeIDs     []string   `json:"edge_ids"`
	UnknownEdge string     `json:"unknown_edge,omitempty"`
	Explanation []string   `json:"explanation"`
}

// SectionReport states the evidence one section contributed.
type SectionReport struct {
	Name       string    `json:"name"`
	Source     string    `json:"source,omitempty"`
	Version    string    `json:"version,omitempty"`
	ObservedAt time.Time `json:"observed_at,omitzero"`
	AgeSeconds int64     `json:"age_seconds,omitempty"`
	Usable     bool      `json:"usable"`
	Note       string    `json:"note,omitempty"`
}

// ServiceState is the intended-access evidence one route contributes.
type ServiceState struct {
	Route     string `json:"route"`
	Container string `json:"container,omitempty"`
	// State is open when the route is defined and its backend is running,
	// closed when fresh evidence shows either missing, unknown otherwise.
	State  EdgeState `json:"state"`
	Detail string    `json:"detail"`
}

// Report is the deterministic result of Evaluate.
type Report struct {
	SchemaVersion string          `json:"schema_version"`
	Host          string          `json:"host,omitempty"`
	EvaluatedAt   time.Time       `json:"evaluated_at"`
	Sections      []SectionReport `json:"sections"`
	Edges         []Edge          `json:"edges"`
	Services      []ServiceState  `json:"services,omitempty"`
	Paths         []Path          `json:"paths"`
	// Unknowns lists every unknown edge, so missing or stale permissions stay
	// visible even when they complete no path.
	Unknowns []string `json:"unknowns,omitempty"`
	// Truncated is true when a bound cut containers, routes or paths; a cut
	// is never a pass for what it no longer names.
	Truncated bool `json:"truncated,omitempty"`
	// Limits states what this report does not claim.
	Limits []string `json:"limits"`
}

var reportLimits = []string{
	"Only the listed sections were read; paths outside them are not evaluated.",
	"No probing, scanning or exploitation took place.",
	"An unknown edge is not a pass. No claim of absence of other attack paths, of malware or of endpoint protection is made.",
}

func (r Report) edge(id string) (Edge, bool) {
	for _, edge := range r.Edges {
		if edge.ID == id {
			return edge, true
		}
	}
	return Edge{}, false
}

func (r Report) path(id string) (Path, bool) {
	for _, path := range r.Paths {
		if path.ID == id {
			return path, true
		}
	}
	return Path{}, false
}

func (e Edge) explain() string {
	state := string(e.State)
	if e.Basis != "" {
		state += ", " + string(e.Basis)
	}
	text := fmt.Sprintf("%s -> %s via %s (%s; %s)", e.From, e.To, e.Mechanism, state, e.Source)
	if !e.ObservedAt.IsZero() {
		text += " at " + e.ObservedAt.UTC().Format(time.RFC3339)
	}
	if e.Detail != "" {
		text += ": " + e.Detail
	}
	return text
}
