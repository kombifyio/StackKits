// Package offlinerecovery admits and executes one bounded, cached local recovery
// recipe without a kombify account, WAN access, a control plane, or an Advanced
// executor. It reuses local Owner custody and policy-state signatures, verifies
// every cached artifact's exact SHA-256 digest and size, and records each effect
// in a durable hash-chained receipt before any later effect can start.
//
// The package deliberately exposes no shell, command, URL, provider, or generic
// execution port. Workload operations cross a small local-only Executor seam;
// artifact materialization stays inside the package beneath the workspace root.
package offlinerecovery

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/kombifyio/stackkits/contracts/canonicaljson"
	"github.com/kombifyio/stackkits/internal/localevidence"
)

const (
	// ReceiptSchemaVersion identifies the offline recovery receipt wire shape.
	ReceiptSchemaVersion = "stackkit.offline-recovery-receipt/v1"
	// PlanSchemaVersion identifies the bounded recovery recipe contract.
	PlanSchemaVersion = "stackkit.offline-recovery-plan/v1"
	// AuthoritySchemaVersion identifies the Owner-signed offline authority.
	AuthoritySchemaVersion = "stackkit.offline-recovery-authority/v1"
	// RevocationSchemaVersion identifies the local revocation projection.
	RevocationSchemaVersion = "stackkit.offline-recovery-revocations/v1"

	ModeStandard = "standard"

	MaxAuthorityLifetime = 5 * time.Minute
	MaxClockSkew         = 30 * time.Second
	MaxAuthorityBytes    = 64 << 10
	MaxReceiptBytes      = 1 << 20
	MaxRevocationBytes   = 1 << 20
	MaxArtifacts         = 64
	MaxArtifactBytes     = 16 << 20
	MaxTotalArtifactSize = 128 << 20
	MaxSteps             = 32
	MaxWorkloads         = 32
)

var (
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	keyIDPattern    = regexp.MustCompile(`^ed25519://sha256/[0-9a-f]{64}$`)
	refPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	ownerRefPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*(?:/[A-Za-z0-9][A-Za-z0-9._:-]*)*$`)
)

// RequiredUnknowns are facts every receipt must leave explicitly unknown.
var RequiredUnknowns = []string{
	"artifact cache modification time is an observation, not trusted provenance time",
	"remote revocation state after the authority receipt is unknown; unobserved revocation is bounded by authority expiry",
	"workload behavior outside the plan's local readiness checks is unknown",
}

// RequiredNotClaimed are the closed bounds every receipt must state. Silence
// never strengthens a receipt into a claim about accounts, WAN reachability,
// Advanced execution, or effects outside this operation.
var RequiredNotClaimed = []string{
	"account entitlement and hosted service availability are not claimed",
	"durable effect is not claimed when the outcome is unknown",
	"no standalone Advanced executor is provided",
	"reconnection reconciliation has not run",
	"recovery outside the signed plan and verified artifact set is not claimed",
	"WAN and control-plane reachability are not claimed",
}

// Denial is a structured, recoverable refusal. Code is stable for callers;
// Repair names the next local action without asking for an authentication bypass.
type Denial struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Repair  string `json:"repair"`
}

func (e *Denial) Error() string {
	if e == nil {
		return "offlinerecovery: denial"
	}
	return fmt.Sprintf("offlinerecovery: %s: %s", e.Code, e.Message)
}

func deny(code, message, repair string) error {
	return &Denial{Code: code, Message: message, Repair: repair}
}

// Artifact is one exact cached input in the signed plan. RelativePath is
// interpreted only beneath Request.CacheRoot and never used as a command.
type Artifact struct {
	ID           string `json:"id"`
	RelativePath string `json:"relativePath"`
	Version      string `json:"version"`
	SHA256       string `json:"sha256"`
	SizeBytes    int64  `json:"sizeBytes"`
}

// StepKind is the closed local recipe vocabulary.
type StepKind string

const (
	StepMaterialize StepKind = "materialize"
	StepRestart     StepKind = "restart"
	StepVerify      StepKind = "verify"
)

// Step is one typed, bounded recipe operation. Materialize copies verified
// cached bytes to a plan-owned workspace path. Restart and Verify name only a
// workload selected by the same plan; neither accepts a command or endpoint.
type Step struct {
	ID          string   `json:"id"`
	Kind        StepKind `json:"kind"`
	ArtifactID  string   `json:"artifactId,omitempty"`
	Destination string   `json:"destination,omitempty"`
	WorkloadRef string   `json:"workloadRef,omitempty"`
}

// Plan is the already admitted local recovery recipe. Its digest is signed by
// Authority; a plan without that exact signature is not execution authority.
type Plan struct {
	SchemaVersion string     `json:"schemaVersion"`
	Version       string     `json:"version"`
	OperationID   string     `json:"operationId"`
	Mode          string     `json:"mode"`
	OwnerRef      string     `json:"ownerRef"`
	SiteRef       string     `json:"siteRef"`
	NodeRef       string     `json:"nodeRef"`
	ChannelRef    string     `json:"channelRef"`
	StackID       string     `json:"stackId"`
	WorkloadRefs  []string   `json:"workloadRefs"`
	Artifacts     []Artifact `json:"artifacts"`
	Steps         []Step     `json:"steps"`
}

// Authority is a short-lived local Owner policy-state signature over the exact
// plan digest, artifact set digest, node custody, and operation identity.
type Authority struct {
	SchemaVersion     string                                  `json:"schemaVersion"`
	Version           string                                  `json:"version"`
	Mode              string                                  `json:"mode"`
	PlanDigest        string                                  `json:"planDigest"`
	ArtifactSetDigest string                                  `json:"artifactSetDigest"`
	OwnerRef          string                                  `json:"ownerRef"`
	OwnerKeyID        string                                  `json:"ownerKeyId"`
	SiteRef           string                                  `json:"siteRef"`
	NodeRef           string                                  `json:"nodeRef"`
	ChannelRef        string                                  `json:"channelRef"`
	OperationID       string                                  `json:"operationId"`
	IssuedAt          time.Time                               `json:"issuedAt"`
	ExpiresAt         time.Time                               `json:"expiresAt"`
	Signature         localevidence.OwnerPolicyStateSignature `json:"signature"`
}

// SigningBytes returns the canonical authority payload signed through the
// existing local Owner policy-state custody. It contains no private key.
func (a Authority) SigningBytes() ([]byte, error) {
	a.Signature = localevidence.OwnerPolicyStateSignature{}
	return canonicalJSON(a)
}

// Digest is the content identity of the signed authority.
func (a Authority) Digest() (string, error) {
	raw, err := a.SigningBytes()
	if err != nil {
		return "", err
	}
	return canonicaljson.SHA256Hex(raw), nil
}

// ClockReading pairs wall time with a monotonic reading from the same process.
// A zero monotonic reading is not a trusted clock anchor.
type ClockReading struct {
	Wall      time.Time
	Monotonic time.Duration
}

// ExecutorIdentity describes the local-only workload seam. NetworkAccess and
// ShellAccess must remain false; the package has no Advanced execution path.
type ExecutorIdentity struct {
	ID            string
	Version       string
	Digest        string
	NodeRef       string
	Mode          string
	NetworkAccess bool
	ShellAccess   bool
}

// Effect reports whether a step certainly applied, certainly did not apply, or
// has an unknown outcome. An error never turns an unknown effect into a retry.
type Effect string

const (
	EffectNotApplied Effect = "not_applied"
	EffectApplied    Effect = "applied"
	EffectUnknown    Effect = "unknown"
)

// StepResult is the complete untrusted adapter return. ObservationDigest binds
// the local readback without carrying logs, command output, or secret values.
type StepResult struct {
	Effect            Effect
	ObservationDigest string
}

// Executor executes only plan-selected local restart and verify steps. It has
// no command, shell, URL, provider, artifact, or network operation in its interface.
type Executor interface {
	Identity() ExecutorIdentity
	Execute(context.Context, Step) (StepResult, error)
}

// Request is the complete input to Run. WorkspaceRoot holds existing local
// Owner custody and the durable operation journal. CacheRoot holds only the
// plan's bounded cached artifacts. Clock must return wall and monotonic readings
// from one continuing process; SystemClock is the production adapter.
type Request struct {
	WorkspaceRoot string
	CacheRoot     string
	Plan          Plan
	AuthorityRaw  []byte
	Clock         func() ClockReading
	Executor      Executor
}

// ArtifactEvidence records the exact bytes observed and their cache-file age.
type ArtifactEvidence struct {
	ID                 string    `json:"id"`
	Version            string    `json:"version"`
	RelativePath       string    `json:"relativePath"`
	SHA256             string    `json:"sha256"`
	SizeBytes          int64     `json:"sizeBytes"`
	CacheObservedAt    time.Time `json:"cacheObservedAt"`
	EvidenceAgeSeconds float64   `json:"evidenceAgeSeconds"`
}

// Entry is one hash-chained operation event. Refused and uncertain attempts are
// recorded rather than hidden.
type Entry struct {
	Seq      int       `json:"seq"`
	Step     string    `json:"step"`
	Outcome  string    `json:"outcome"`
	Code     string    `json:"code,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	At       time.Time `json:"at"`
	PrevHash string    `json:"prevHash"`
	Hash     string    `json:"hash"`
}

// Timing distinguishes authority age, execution duration, and absent completion.
type Timing struct {
	StartedAt           time.Time `json:"startedAt"`
	FinishedAt          time.Time `json:"finishedAt,omitempty"`
	DurationSeconds     float64   `json:"durationSeconds"`
	AuthorityAgeSeconds float64   `json:"authorityAgeSeconds"`
}

// Receipt is the deterministic, verifiable result of one operation. Reexecuted
// is always false: a persisted pending or terminal journal is returned instead
// of running an operation again.
type Receipt struct {
	APIVersion             string             `json:"apiVersion"`
	OperationID            string             `json:"operationId"`
	Mode                   string             `json:"mode"`
	PlanVersion            string             `json:"planVersion"`
	PlanDigest             string             `json:"planDigest"`
	AuthorityVersion       string             `json:"authorityVersion"`
	AuthorityDigest        string             `json:"authorityDigest"`
	AuthorityIssuedAt      time.Time          `json:"authorityIssuedAt"`
	AuthorityExpiresAt     time.Time          `json:"authorityExpiresAt"`
	NodeCustodyDigest      string             `json:"nodeCustodyDigest"`
	ArtifactSetDigest      string             `json:"artifactSetDigest"`
	BindingDigest          string             `json:"bindingDigest"`
	Artifacts              []ArtifactEvidence `json:"artifacts"`
	RequiredSteps          []string           `json:"requiredSteps"`
	Genesis                string             `json:"genesis"`
	Entries                []Entry            `json:"entries"`
	Verdict                string             `json:"verdict"`
	Timing                 Timing             `json:"timing"`
	Unknowns               []string           `json:"unknowns"`
	NotClaimed             []string           `json:"not_claimed"`
	ReconciliationRequired bool               `json:"reconciliationRequired"`
	Reexecuted             bool               `json:"reexecuted"`
	HeadHash               string             `json:"headHash"`
}

const (
	OutcomeOK      = "ok"
	OutcomeDenied  = "denied"
	OutcomeFailed  = "failed"
	OutcomeUnknown = "unknown"

	VerdictRecovered = "recovered"
	VerdictDenied    = "denied"
	VerdictFailed    = "failed"
	VerdictUnknown   = "unknown"
	VerdictPending   = "pending"
)

// ErrOutcomeUnknown marks a persisted operation whose effect cannot safely be
// repeated. Callers reconcile its receipt instead of starting a new execution.
var ErrOutcomeUnknown = errors.New("offlinerecovery: effect outcome unknown")
