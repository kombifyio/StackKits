package backupsurvival

import (
	"context"
	"errors"
	"time"
)

// ErrDenied is what a Backend returns when its policy refused the identity.
// Any other error is a transport or backend fault: it proves nothing, so the
// proof treats it as inconclusive and fails.
var ErrDenied = errors.New("backupsurvival: operation denied by backend policy")

// Attack is one destructive operation a compromised source node could try.
type Attack string

// The attacks driven with the writer credential.
const (
	AttackDeleteObject        Attack = "delete-object"
	AttackOverwriteObject     Attack = "overwrite-object"
	AttackDeleteVersion       Attack = "delete-version"
	AttackDeleteVersionBypass Attack = "delete-version-with-bypass"
	AttackShortenRetention    Attack = "shorten-retention"
	AttackDisableVersioning   Attack = "disable-versioning"
	AttackDisableObjectLock   Attack = "disable-object-lock"
	AttackPrune               Attack = "prune"
)

// attacks is the fixed, ordered set. absorb marks an attack a versioned,
// locked bucket may accept as a harmless write (a delete marker or a new
// version): the proof then relies on the survival check, not on denial.
var attacks = []struct {
	attack Attack
	absorb bool
}{
	{AttackDeleteObject, true},
	{AttackOverwriteObject, true},
	{AttackDeleteVersion, false},
	{AttackDeleteVersionBypass, false},
	{AttackShortenRetention, false},
	{AttackDisableVersioning, false},
	{AttackDisableObjectLock, false},
	{AttackPrune, false},
}

// ObjectRef names one committed object version. The proof only ever creates
// refs under the binding's canary prefix.
type ObjectRef struct {
	Key       string `json:"key"`
	VersionID string `json:"versionId"`
}

// Usage is the backend's capacity reading for the canary prefix's bucket.
type Usage struct {
	UsedBytes  int64
	QuotaBytes int64
}

// Backend is the port to one existing backup backend. Every method acts as
// exactly the Identity it is given, so the backend's own policy decides; the
// proof never simulates a denial.
type Backend interface {
	// Commit writes a new locked version and returns its ref.
	Commit(ctx context.Context, as Identity, key string, data []byte) (ObjectRef, error)
	// Attempt tries one attack against a committed object. nil means the
	// backend accepted it; ErrDenied means policy refused it.
	Attempt(ctx context.Context, as Identity, attack Attack, ref ObjectRef) error
	// RetainUntil reports the lock expiry of one version.
	RetainUntil(ctx context.Context, as Identity, ref ObjectRef) (time.Time, error)
	// Read returns the bytes of exactly one version.
	Read(ctx context.Context, as Identity, ref ObjectRef) ([]byte, error)
	// Prune removes one disposable version as the maintenance authority.
	Prune(ctx context.Context, as Identity, ref ObjectRef) error
	// Present reports whether the version is still listed as a live version.
	Present(ctx context.Context, as Identity, ref ObjectRef) (bool, error)
	// Usage reads the capacity of the backend holding the canary prefix.
	Usage(ctx context.Context, as Identity, prefix string) (Usage, error)
}
