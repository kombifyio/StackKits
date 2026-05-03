// Package backupcontroller is the control plane for the kombify-Backup
// SaaS path (Phase 4 of the backup rollout — see
// docs/plans/2026-05-01-backup-rollout.md).
//
// This is a SCAFFOLD. It defines the domain model, the Store/JobQueue
// interfaces, and a working in-memory implementation of each. The real
// Postgres-backed Store and NATS JetStream Queue land in follow-up PRs;
// keeping the abstraction in place from day one means the rest of the
// package — server, scheduler, audit, agent enrollment — does not have
// to be rewritten when those land.
//
// Scope of this package:
//   - Tenant / Fleet / Host / Repo / Job model.
//   - HTTP API (REST over stdlib http.ServeMux) for the kombify-TechStack
//     dashboard to consume.
//   - Cron-style Scheduler that turns due Jobs into Queue messages.
//   - AuditLog of every state-changing operation.
//   - Agent enrollment helpers (token mint, host registration).
//
// What this package explicitly does NOT do:
//   - Talk to Kopia directly. The agents do that on each host.
//   - Run schedules. The scheduler dispatches to the Queue; agents pull.
//   - Hold backup data. Storage is the agent's job, against the
//     repo-server addon (addons/backup-repo-server) or directly against
//     the configured S3/B2/Storagebox endpoint.
package backupcontroller

import (
	"time"
)

// =============================================================================
// DOMAIN MODEL
//
// Mirrors internal/backup-controller/migrations/001_init.sql. When that
// schema lands in Postgres, these structs map row-for-row.
// =============================================================================

// Plan enumerates the SaaS subscription tiers. Tier semantics are
// defined in docs/BACKUP-ARCHITECTURE.md → "Plan tiers".
type Plan string

const (
	PlanFree     Plan = "free"
	PlanPro      Plan = "pro"
	PlanBusiness Plan = "business"
)

// Tenant is one paying (or free-tier) customer of the backup SaaS. A
// Tenant owns Fleets, which group Hosts. Tenants are isolated by
// design: cross-tenant queries are not a feature.
type Tenant struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Plan      Plan      `json:"plan"`
	CreatedAt time.Time `json:"created_at"`
}

// Fleet groups Hosts that should be considered together (e.g. "homelab-de"
// vs. "vps-hetzner"). The grouping is purely operator convenience for
// reporting; it does not change how Jobs are scheduled or how repos
// are isolated.
type Fleet struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Name     string `json:"name"`
	Region   string `json:"region,omitempty"`
}

// HostKind tracks which StackKit a host is running. The controller uses
// it to choose hook profiles without re-discovering them on the host.
type HostKind string

const (
	HostKindBaseKit       HostKind = "base-kit"
	HostKindModernHomelab HostKind = "modern-homelab"
	HostKindHAKit         HostKind = "ha-kit"
)

// Host is one server enrolled in a Fleet. The agent_token is a
// per-host shared secret minted at enrollment time and used by
// stackkit-backup-agent to authenticate its calls to the controller.
type Host struct {
	ID           string    `json:"id"`
	FleetID      string    `json:"fleet_id"`
	Hostname     string    `json:"hostname"`
	AgentToken   string    `json:"-"`
	LastSeen     time.Time `json:"last_seen"`
	StackKitKind HostKind  `json:"stackkit_kind"`
}

// RepoKind enumerates the supported storage backends. Same set as the
// addon (addons/backup/addon.cue → #BackupTargets) — we keep the
// vocabulary aligned so the controller can write addon configs verbatim.
type RepoKind string

const (
	RepoKindB2                RepoKind = "b2"
	RepoKindHetznerStoragebox RepoKind = "hetzner-storagebox"
	RepoKindS3                RepoKind = "s3"
)

// Repo is a Kopia repository the controller manages on behalf of a
// Tenant. CredentialsRef is a pointer (e.g. "secret://kombify/tenants/<id>/repo")
// — the actual credential bytes never live in this DB. They live in
// the platform's secrets store, fetched lazily.
type Repo struct {
	ID             string   `json:"id"`
	TenantID       string   `json:"tenant_id"`
	Kind           RepoKind `json:"kind"`
	Endpoint       string   `json:"endpoint"`
	CredentialsRef string   `json:"credentials_ref"`
}

// JobStatus is the lifecycle of a single backup job execution.
type JobStatus string

const (
	JobStatusPending JobStatus = "pending"
	JobStatusRunning JobStatus = "running"
	JobStatusOK      JobStatus = "ok"
	JobStatusFailed  JobStatus = "failed"
)

// Job is a recurring snapshot specification: schedule (cron), where it
// runs (HostID), where it writes (RepoID). LastRun and Status are
// updated by the agent reporting back through the controller.
type Job struct {
	ID       string     `json:"id"`
	TenantID string     `json:"tenant_id"`
	HostID   string     `json:"host_id"`
	RepoID   string     `json:"repo_id"`
	Schedule string     `json:"schedule"` // cron, host TZ
	LastRun  *time.Time `json:"last_run,omitempty"`
	Status   JobStatus  `json:"status"`
}

// AuditEntry records a single state-changing event. Append-only by
// convention: the audit log is the legal record for SaaS compliance and
// must outlive Tenants and Hosts that are deleted.
type AuditEntry struct {
	ID       string                 `json:"id"`
	TenantID string                 `json:"tenant_id"`
	Actor    string                 `json:"actor"`
	Action   string                 `json:"action"`
	Resource string                 `json:"resource"`
	Time     time.Time              `json:"ts"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
}
