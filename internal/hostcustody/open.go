package hostcustody

import (
	"errors"
	"slices"
	"time"

	stackcrypto "github.com/kombifyio/stackkits/internal/crypto"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/pkg/hostdelivery"
)

const (
	// ledgerRetention keeps a consumed delivery recorded beyond its expiry.
	ledgerRetention = time.Hour
	// maxDelivered bounds the registry of delivered secrets.
	maxDelivered = 256
)

// Expectation is what the opening node requires of a delivery beyond its own
// identity: the tenant the owner operates for, and the operations this caller
// can carry out.
type Expectation struct {
	TenantRef  string
	Operations []string
}

// Delivery is one opened delivery. The caller owns Plaintext and clears it.
type Delivery struct {
	Context   hostdelivery.Context
	Plaintext []byte
}

type deliveryLedger struct {
	APIVersion string                                  `json:"apiVersion"`
	Generation uint64                                  `json:"generation"`
	Consumed   []consumedDelivery                      `json:"consumed"`
	Signature  localevidence.OwnerPolicyStateSignature `json:"signature"`
}

type consumedDelivery struct {
	OperationID string `json:"operationId"`
	NotAfter    string `json:"notAfter"`
}

// OpenDelivery opens one sealed delivery for this node and consumes it. It
// refuses a delivery for another tenant, Site, node or operation, a detached
// node, a retired or unknown recipient generation, an operation that needs an
// authority signature before one is pinned, any delivery whose signature is
// missing or not by the pinned key once one is pinned, an expired or
// oversized one, and a replay. The delivery is recorded as consumed (and a
// secret one in the delivered registry) before the plaintext is returned, so
// a replay, including one after a crash, is denied and the delivering
// authority must seal a fresh one.
//
//nolint:gocyclo // One linear sequence of independent refusals before decryption.
func OpenDelivery(store Store, raw []byte, expect Expectation, now time.Time) (Delivery, error) {
	if expect.TenantRef == "" || len(expect.Operations) == 0 {
		return Delivery{}, errors.New("hostcustody: opening a delivery requires the expected tenant and operations")
	}
	envelope, err := hostdelivery.ParseEnvelope(raw)
	if err != nil {
		return Delivery{}, err
	}
	spec, known := hostdelivery.LookupOperation(envelope.Context.Operation)
	if !known || !slices.Contains(expect.Operations, envelope.Context.Operation) {
		return Delivery{}, errors.New("hostcustody: sealed delivery is for an operation this command does not carry out")
	}
	owner, err := localevidence.LoadOwnerCustody(store.Workspace())
	if err != nil {
		return Delivery{}, err
	}
	ownerKey, err := localevidence.LoadOwnerKey(store.Workspace())
	if err != nil {
		return Delivery{}, err
	}
	current, err := readRecipient(store)
	if err != nil {
		return Delivery{}, err
	}
	if err := pruneRetired(store, current.Generation, now); err != nil {
		return Delivery{}, err
	}
	state, stateExisting, err := readState(store, current.Generation)
	if err != nil {
		return Delivery{}, err
	}
	if state.DetachedAt != "" {
		return Delivery{}, ErrDetached
	}
	ciphertext, authority, err := recipientFor(store, current, state, envelope.Context, now)
	if err != nil {
		return Delivery{}, err
	}
	switch {
	case spec.AuthoritySigned && authority == nil:
		return Delivery{}, errors.New("hostcustody: this operation needs a pinned delivering authority key; pin it with the recipient statement first")
	case authority != nil:
		// Once a delivering authority is pinned, every operation carries its
		// signature: an unsigned delivery would let anyone who knows the public
		// recipient key redirect what the node trusts, backup targets included.
		public, keyErr := authority.public()
		if keyErr != nil {
			return Delivery{}, keyErr
		}
		if err := envelope.VerifyAuthority(public, authority.KeyID); err != nil {
			return Delivery{}, err
		}
	}
	wrapping, err := store.WrappingKey()
	if err != nil {
		return Delivery{}, err
	}
	seed, err := stackcrypto.DecryptWithPassphrase(ciphertext, string(wrapping))
	clearBytes(wrapping)
	if err != nil {
		return Delivery{}, errors.New("hostcustody: cannot decrypt recipient key")
	}
	context, plaintext, err := hostdelivery.Open(seed, raw, now)
	clearBytes(seed)
	if err != nil {
		return Delivery{}, err
	}
	// The node reference is the one this node's statements report; a delivery
	// sealed to its plain StackSpec reference (statements before the identity
	// reference) still names this node, whose recipient key alone opens it.
	if context.Operation != envelope.Context.Operation || context.TenantRef != expect.TenantRef ||
		context.SiteRef != owner.Binding.SiteRef ||
		(context.NodeRef != owner.Binding.NodeRef && context.NodeRef != hostdelivery.NodeIdentityRef(owner.Binding.NodeRef, ownerKey.Public())) {
		clearBytes(plaintext)
		return Delivery{}, errors.New("hostcustody: sealed delivery is not addressed to this node, tenant and operation")
	}
	if spec.Secret {
		if state.Delivered, err = recordDelivered(state.Delivered, context, now); err != nil {
			clearBytes(plaintext)
			return Delivery{}, err
		}
	}
	if err := consumeDelivery(store, current.Generation, context, now); err != nil {
		clearBytes(plaintext)
		return Delivery{}, err
	}
	if spec.Secret {
		if err := writeState(store, state, stateExisting); err != nil {
			clearBytes(plaintext)
			return Delivery{}, err
		}
	}
	return Delivery{Context: context, Plaintext: plaintext}, nil
}

// recipientFor selects the key a delivery was sealed to: the current
// generation, or a retired one for a delivery issued before its rotation and
// while its window is open.
func recipientFor(store Store, current recipientRecord, state deliveryState, context hostdelivery.Context, now time.Time) ([]byte, *AuthorityKey, error) {
	if context.BindingGeneration == current.Generation {
		return current.Ciphertext, state.Authority, nil
	}
	retired, _, err := readRetired(store, current.Generation)
	if err != nil {
		return nil, nil, err
	}
	issued, issuedErr := time.Parse(time.RFC3339Nano, context.IssuedAt)
	for _, entry := range retired.Retired {
		if entry.Generation != context.BindingGeneration {
			continue
		}
		rotated, rotatedErr := time.Parse(time.RFC3339Nano, entry.RetiredAt)
		if issuedErr != nil || rotatedErr != nil || !issued.Before(rotated) || !retiredOpen(entry, now) {
			break
		}
		// A retired generation that was never pinned stays bound by the current
		// pin: otherwise rotating to a pinned authority would leave an unsigned,
		// back-dated delivery to the old generation open for its whole window.
		authority := entry.Authority
		if authority == nil {
			authority = state.Authority
		}
		return entry.Ciphertext, authority, nil
	}
	return nil, nil, errors.New("hostcustody: sealed delivery is for a retired or unknown recipient generation")
}

func consumeDelivery(store Store, generation uint64, context hostdelivery.Context, now time.Time) error {
	ledger := deliveryLedger{APIVersion: ledgerAPIVersion, Generation: generation}
	existing := false
	if err := store.Read(ledgerName, maxRecordBytes, &ledger, &ledger.Signature); err == nil {
		existing = true
	} else if !errors.Is(err, ErrMissing) {
		return err
	}
	// Operation IDs stay consumed across generations: a delivery to a retired
	// generation can never be replayed after a rotation.
	if ledger.APIVersion != ledgerAPIVersion || ledger.Generation == 0 || ledger.Generation > generation {
		return errors.New("hostcustody: delivery ledger does not match the recipient generation")
	}
	kept := ledger.Consumed[:0]
	for _, entry := range ledger.Consumed {
		if entry.OperationID == context.OperationID {
			return errors.New("hostcustody: sealed delivery was already consumed")
		}
		if until, err := time.Parse(time.RFC3339Nano, entry.NotAfter); err == nil && now.Sub(until) < ledgerRetention {
			kept = append(kept, entry)
		}
	}
	ledger.Generation = generation
	ledger.Consumed = append(kept, consumedDelivery{OperationID: context.OperationID, NotAfter: context.NotAfter})
	return store.Write(ledgerName, &ledger, &ledger.Signature, !existing)
}

// recordDelivered registers a delivered secret by reference. A newer version
// of a reference replaces the older entry and clears its rotation mark: the
// value was rotated upstream and re-delivered.
func recordDelivered(delivered []DeliveredItem, context hostdelivery.Context, now time.Time) ([]DeliveredItem, error) {
	entry := DeliveredItem{TenantRef: context.TenantRef, DeliveryRef: context.DeliveryRef, ItemVersion: context.ItemVersion, Operation: context.Operation, DeliveredAt: now.UTC().Format(time.RFC3339Nano)}
	delivered = slices.Clone(delivered)
	for i, existing := range delivered {
		if existing.TenantRef != entry.TenantRef || existing.DeliveryRef != entry.DeliveryRef {
			continue
		}
		if entry.ItemVersion > existing.ItemVersion {
			delivered[i] = entry
		}
		return delivered, nil
	}
	if len(delivered) < maxDelivered {
		return append(delivered, entry), nil
	}
	// Every reference that still needs rotation is kept; the oldest settled
	// one makes room.
	for i, existing := range delivered {
		if !existing.NeedsRotation {
			return append(slices.Delete(delivered, i, i+1), entry), nil
		}
	}
	return nil, errors.New("hostcustody: every registered delivered secret still awaits rotation; rotate them before delivering new ones")
}
