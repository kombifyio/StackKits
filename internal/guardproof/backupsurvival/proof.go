package backupsurvival

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/contracts/canonicaljson"
)

// ErrNotProven is returned with a failed Receipt. The receipt is still sealed
// and verifiable: a failure is evidence too.
var ErrNotProven = errors.New("backupsurvival: backup survival not proven")

const retentionSkew = 5 * time.Minute

var nonceShape = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,63}$`)

// Input is everything one proof run needs.
type Input struct {
	Binding Binding
	Backend Backend
	Target  RestoreTarget
	// NodeID and WorkloadID optionally bind the receipt to one exact node and
	// workload identity inside the genesis and canonical receipt digest.
	NodeID     string
	WorkloadID string
	// SigningKey optionally Ed25519-signs the sealed receipt.
	SigningKey ed25519.PrivateKey
	// Nonce makes the canary keys and bytes unique and reproducible per run.
	Nonce string
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// canaryBytes expands the nonce into a deterministic payload of the requested
// size, so a run is reproducible and a restored byte cannot match by accident.
func canaryBytes(nonce string, kib int) []byte {
	out := make([]byte, 0, kib*1024)
	seed := sha256.Sum256([]byte("backupsurvival/canary/v1:" + nonce))
	block := seed[:]
	for len(out) < kib*1024 {
		next := sha256.Sum256(block)
		block = next[:]
		out = append(out, block...)
	}
	return out[:kib*1024]
}

func (in Input) canaryRef(ref ObjectRef, wantKey string) error {
	if ref.Key != wantKey || ref.VersionID == "" || !strings.HasPrefix(ref.Key, in.Binding.CanaryPrefix) {
		return errors.New("backend returned an object outside the disposable canary scope")
	}
	return nil
}

// Run executes the proof. The error is nil only for a "survives" receipt; any
// invalid input, backend fault, breach or missing evidence returns a sealed
// "failed" receipt (when a chain exists) and ErrNotProven.
func Run(ctx context.Context, in Input) (Receipt, error) {
	if in.Backend == nil || in.Target == nil {
		return Receipt{}, errors.New("backupsurvival: backend and restore target are required")
	}
	if !nonceShape.MatchString(in.Nonce) {
		return Receipt{}, errors.New("backupsurvival: nonce must be 8..64 lowercase alphanumerics or dashes")
	}
	if !validIdentifier(in.Binding.BackendRef) {
		return Receipt{}, fmt.Errorf("%w: backend reference must be a trimmed non-empty identifier", ErrInvalidBinding)
	}
	if (in.NodeID != "" || in.WorkloadID != "") && (!validIdentifier(in.NodeID) || !validIdentifier(in.WorkloadID)) {
		return Receipt{}, errors.New("backupsurvival: nodeId and workloadId must both be valid identifiers when bound")
	}
	if len(in.SigningKey) != 0 && len(in.SigningKey) != ed25519.PrivateKeySize {
		return Receipt{}, errors.New("backupsurvival: signing key must be an Ed25519 private key")
	}
	now := in.Now
	if now == nil {
		now = time.Now
	}
	digest, err := in.Binding.Digest()
	if err != nil {
		return Receipt{}, fmt.Errorf("backupsurvival: digest binding: %w", err)
	}
	genesisDigest, err := genesisFor(Receipt{
		APIVersion:    ReceiptVersion,
		BindingDigest: digest,
		BackendRef:    in.Binding.BackendRef,
		NodeID:        in.NodeID,
		WorkloadID:    in.WorkloadID,
	})
	if err != nil {
		return Receipt{}, fmt.Errorf("backupsurvival: compute genesis: %w", err)
	}
	c := newChain(genesisDigest, now)
	var canary ObjectRef
	seal := func() (Receipt, error) {
		r := Receipt{
			APIVersion:    ReceiptVersion,
			BindingDigest: digest,
			BackendRef:    in.Binding.BackendRef,
			NodeID:        in.NodeID,
			WorkloadID:    in.WorkloadID,
			Canary:        canary,
			Genesis:       c.genesis,
			Entries:       c.entries,
			HeadHash:      c.prev,
			NotClaimed:    append([]string(nil), RequiredNotClaimed...),
			CompletedAt:   now().UTC(),
		}
		r.Verdict = VerdictSurvives
		if c.failed {
			r.Verdict = VerdictFailed
		}
		invalidate := func() {
			r.Verdict = VerdictFailed
			r.ReceiptDigest = ""
			r.Signature = nil
		}
		receiptDigest, err := r.ComputeDigest()
		if err != nil {
			invalidate()
			return r, fmt.Errorf("%w: digest receipt: %v", ErrNotProven, err)
		}
		r.ReceiptDigest = receiptDigest
		if len(in.SigningKey) == ed25519.PrivateKeySize {
			if err := r.Sign(in.SigningKey); err != nil {
				invalidate()
				return r, fmt.Errorf("%w: sign receipt: %v", ErrNotProven, err)
			}
			pub, _ := in.SigningKey.Public().(ed25519.PublicKey)
			if err := r.VerifyWithPublicKey(pub); err != nil {
				invalidate()
				return r, fmt.Errorf("%w: self-check signature: %v", ErrNotProven, err)
			}
		} else if err := r.Verify(); err != nil {
			invalidate()
			return r, fmt.Errorf("%w: self-check: %v", ErrNotProven, err)
		}
		if r.Verdict != VerdictSurvives {
			return r, ErrNotProven
		}
		return r, nil
	}
	fail := func(step, format string, args ...any) (Receipt, error) {
		c.add(step, OutcomeFailed, fmt.Sprintf(format, args...))
		return seal()
	}

	if err := in.Binding.Validate(); err != nil {
		return fail(StepBinding, "%v", err)
	}
	c.add(StepBinding, OutcomeOK, "three separated identities, immutable retention and canary scope validated")
	b := in.Binding

	usage, err := in.Backend.Usage(ctx, b.Maintenance, b.CanaryPrefix)
	if err != nil {
		return fail(StepCapacity, "capacity unreadable: %v", err)
	}
	if usage.QuotaBytes <= 0 || usage.UsedBytes < 0 {
		return fail(StepCapacity, "no readable quota: an unbounded backend cannot raise a capacity alert")
	}
	ratio := float64(usage.UsedBytes) / float64(usage.QuotaBytes)
	switch {
	case ratio >= b.Capacity.FailRatio:
		return fail(StepCapacity, "usage %.3f reached the fail ratio %.3f", ratio, b.Capacity.FailRatio)
	case ratio >= b.Capacity.AlertRatio:
		c.add(StepCapacity, OutcomeWarn, fmt.Sprintf("usage %.3f reached the alert ratio %.3f", ratio, b.Capacity.AlertRatio))
	default:
		c.add(StepCapacity, OutcomeOK, fmt.Sprintf("usage %.3f below the alert ratio %.3f", ratio, b.Capacity.AlertRatio))
	}

	data := canaryBytes(in.Nonce, b.MaxSnapshotKB)
	want := canonicaljson.SHA256Hex(data)
	key := b.CanaryPrefix + "snapshot-" + in.Nonce
	canary, err = in.Backend.Commit(ctx, b.Writer, key, data)
	if err == nil {
		err = in.canaryRef(canary, key)
	}
	if err != nil {
		canary = ObjectRef{}
		return fail(StepCommit, "writer could not commit the canary snapshot: %v", err)
	}
	c.add(StepCommit, OutcomeOK, fmt.Sprintf("writer %q committed %s as %s", b.Writer.ID, canary.Key, want))

	committedUntil, err := in.Backend.RetainUntil(ctx, b.Recovery, canary)
	if err != nil {
		return fail(StepRetention, "retention unreadable by the recovery identity: %v", err)
	}
	if floor := now().Add(b.Retention.minRetain() - retentionSkew); committedUntil.Before(floor) {
		return fail(StepRetention, "retained until %s, shorter than the bound %d days", committedUntil.UTC().Format(time.RFC3339), b.Retention.MinRetainDays)
	}
	c.add(StepRetention, OutcomeOK, "retained until "+committedUntil.UTC().Format(time.RFC3339))

	for _, a := range attacks {
		step := StepAttackPrefix + string(a.attack)
		switch err := in.Backend.Attempt(ctx, b.Writer, a.attack, canary); {
		case errors.Is(err, ErrDenied):
			c.add(step, OutcomeDenied, "writer credential refused")
		case err == nil && a.absorb:
			c.add(step, OutcomeAbsorbed, "accepted as a non-destructive write; survival verified below")
		case err == nil:
			c.add(step, OutcomeBreached, "writer credential performed a destructive operation")
		default:
			c.add(step, OutcomeFailed, "inconclusive, not a policy denial: "+err.Error())
		}
	}

	if err := in.verifySurvival(ctx, canary, want, committedUntil); err != nil {
		c.add(StepSurvival, OutcomeFailed, err.Error())
	} else {
		c.add(StepSurvival, OutcomeOK, "version present, bytes identical and retention not shortened after every attack")
	}

	if err := in.restore(ctx, canary, want); err != nil {
		c.add(StepRestore, OutcomeFailed, err.Error())
	} else {
		c.add(StepRestore, OutcomeOK, "recovery identity restored identical bytes into isolated "+in.Target.Path())
	}

	if err := in.pruneProbe(ctx, canary); err != nil {
		c.add(StepPrune, OutcomeFailed, err.Error())
	} else {
		c.add(StepPrune, OutcomeOK, "maintenance authority pruned a disposable canary; the retained canary stayed")
	}
	return seal()
}

func (in Input) verifySurvival(ctx context.Context, ref ObjectRef, want string, committedUntil time.Time) error {
	b := in.Binding
	present, err := in.Backend.Present(ctx, b.Recovery, ref)
	if err != nil {
		return fmt.Errorf("presence unreadable: %w", err)
	}
	if !present {
		return errors.New("committed version is gone")
	}
	until, err := in.Backend.RetainUntil(ctx, b.Recovery, ref)
	if err != nil {
		return fmt.Errorf("retention unreadable: %w", err)
	}
	if until.Before(committedUntil) {
		return fmt.Errorf("retention shortened from %s to %s", committedUntil.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))
	}
	got, err := in.Backend.Read(ctx, b.Recovery, ref)
	if err != nil {
		return fmt.Errorf("committed version unreadable: %w", err)
	}
	if canonicaljson.SHA256Hex(got) != want {
		return errors.New("committed version bytes changed")
	}
	return nil
}

func (in Input) restore(ctx context.Context, ref ObjectRef, want string) error {
	got, err := in.Backend.Read(ctx, in.Binding.Recovery, ref)
	if err != nil {
		return fmt.Errorf("recovery identity could not read: %w", err)
	}
	if canonicaljson.SHA256Hex(got) != want {
		return errors.New("recovered bytes differ from the committed snapshot")
	}
	written, err := in.Target.Write("restored-snapshot", got)
	if err != nil {
		return fmt.Errorf("isolated target write failed: %w", err)
	}
	if written != want {
		return errors.New("isolated target holds different bytes than the committed snapshot")
	}
	return nil
}

func (in Input) pruneProbe(ctx context.Context, retained ObjectRef) error {
	b := in.Binding
	key := b.CanaryPrefix + "prune-" + in.Nonce
	data := canaryBytes(in.Nonce+"-prune", 1)
	disposable, err := in.Backend.Commit(ctx, b.Writer, key, data)
	if err == nil {
		err = in.canaryRef(disposable, key)
	}
	if err != nil {
		return fmt.Errorf("could not commit the disposable prune canary: %w", err)
	}
	if err := in.Backend.Prune(ctx, b.Maintenance, disposable); err != nil {
		return fmt.Errorf("maintenance authority could not prune: %w", err)
	}
	gone, err := in.Backend.Present(ctx, b.Recovery, disposable)
	if err != nil || gone {
		return errors.New("pruned canary is still present or unreadable")
	}
	still, err := in.Backend.Present(ctx, b.Recovery, retained)
	if err != nil || !still {
		return errors.New("pruning removed or hid the retained canary")
	}
	return nil
}
