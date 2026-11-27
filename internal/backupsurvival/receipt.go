package backupsurvival

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ReceiptVersion identifies the receipt wire shape.
const ReceiptVersion = "stackkit.guard-backup-survival-receipt/v1"

// Verdict is the whole proof's outcome. There is no partial pass.
type Verdict string

// Verdicts.
const (
	VerdictSurvives Verdict = "survives"
	VerdictFailed   Verdict = "failed"
)

// Step outcomes.
const (
	OutcomeOK        = "ok"
	OutcomeDenied    = "denied"
	OutcomeAbsorbed  = "absorbed"
	OutcomeBreached  = "breached"
	OutcomeFailed    = "failed"
	OutcomeWarn      = "warn"
	OutcomeSkipped   = "skipped"
	StepBinding      = "binding"
	StepCommit       = "commit-canary"
	StepRetention    = "verify-retention"
	StepAttackPrefix = "attack:"
	StepSurvival     = "verify-survival"
	StepRestore      = "clean-room-restore"
	StepPrune        = "maintenance-prune"
	StepCapacity     = "capacity"
)

// RequiredNotClaimed is the closed set of bounds every receipt must state. A
// receipt missing one is invalid, so a verifier can never read silence as a
// stronger claim.
var RequiredNotClaimed = []string{
	"backup-admin-compromise: whoever holds the maintenance or bypass authority can delete retained points; this proof does not protect against that identity",
	"provider-account-compromise: the cloud account owner and provider staff are outside the proof",
	"real-recovery-points: no deletion was attempted against any real retained recovery point",
	"lock-renewal: retention was verified at proof time only; renewal before expiry is not proven",
	"application-recovery: restored bytes are verified, application behaviour on them is not",
	"live-target: the proof ran against the bound backend only, no other target or provider is covered",
}

// Entry is one hash-chained step.
type Entry struct {
	Seq      int       `json:"seq"`
	Step     string    `json:"step"`
	Outcome  string    `json:"outcome"`
	Detail   string    `json:"detail"`
	At       time.Time `json:"at"`
	PrevHash string    `json:"prevHash"`
	Hash     string    `json:"hash"`
}

// Receipt is the proof's sealed result. It holds identity IDs and credential
// fingerprints, never credential values.
type Receipt struct {
	APIVersion    string    `json:"apiVersion"`
	BindingDigest string    `json:"bindingDigest"`
	BackendRef    string    `json:"backendRef"`
	Canary        ObjectRef `json:"canary"`
	Verdict       Verdict   `json:"verdict"`
	Entries       []Entry   `json:"entries"`
	HeadHash      string    `json:"headHash"`
	NotClaimed    []string  `json:"notClaimed"`
	CompletedAt   time.Time `json:"completedAt"`
}

func hashOf(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func genesis(bindingDigest string) string {
	return hashOf([]byte("backupsurvival/genesis/v1:"), []byte(bindingDigest))
}

func entryHash(e Entry) (string, error) {
	e.Hash = ""
	raw, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	return hashOf([]byte(e.PrevHash), raw), nil
}

type chain struct {
	entries []Entry
	prev    string
	now     func() time.Time
	failed  bool
}

func newChain(bindingDigest string, now func() time.Time) *chain {
	return &chain{prev: genesis(bindingDigest), now: now}
}

func (c *chain) add(step, outcome, detail string) {
	if outcome == OutcomeFailed || outcome == OutcomeBreached {
		c.failed = true
	}
	e := Entry{Seq: len(c.entries) + 1, Step: step, Outcome: outcome, Detail: detail, At: c.now().UTC(), PrevHash: c.prev}
	e.Hash, _ = entryHash(e)
	c.entries = append(c.entries, e)
	c.prev = e.Hash
}

// Verify recomputes the chain and every structural rule. It trusts nothing in
// the receipt: a verdict, head hash or missing bound that disagrees fails.
func (r Receipt) Verify() error {
	if r.APIVersion != ReceiptVersion {
		return errors.New("backupsurvival: unknown receipt version")
	}
	if len(r.Entries) == 0 {
		return errors.New("backupsurvival: receipt has no entries")
	}
	prev := genesis(r.BindingDigest)
	failed := false
	for i, e := range r.Entries {
		if e.Seq != i+1 || e.PrevHash != prev {
			return fmt.Errorf("backupsurvival: receipt chain broken at entry %d", i+1)
		}
		want, err := entryHash(e)
		if err != nil || want != e.Hash {
			return fmt.Errorf("backupsurvival: receipt entry %d was altered", i+1)
		}
		if e.Outcome == OutcomeFailed || e.Outcome == OutcomeBreached {
			failed = true
		}
		prev = e.Hash
	}
	if r.HeadHash != prev {
		return errors.New("backupsurvival: receipt head hash does not match the chain")
	}
	if want := VerdictSurvives; !failed && r.Verdict != want {
		return errors.New("backupsurvival: verdict understates the recorded steps")
	}
	if failed && r.Verdict != VerdictFailed {
		return errors.New("backupsurvival: verdict overstates a failed proof")
	}
	if r.Verdict == VerdictSurvives {
		if err := r.requireSteps(); err != nil {
			return err
		}
	}
	have := map[string]bool{}
	for _, n := range r.NotClaimed {
		have[n] = true
	}
	for _, n := range RequiredNotClaimed {
		if !have[n] {
			return errors.New("backupsurvival: receipt omits a required not-claimed bound")
		}
	}
	return nil
}

// requireSteps makes a "survives" verdict depend on every proof leg being
// present, so a truncated chain cannot pass.
func (r Receipt) requireSteps() error {
	seen := map[string]string{}
	for _, e := range r.Entries {
		seen[e.Step] = e.Outcome
	}
	for _, step := range []string{StepBinding, StepCommit, StepRetention, StepSurvival, StepRestore, StepPrune, StepCapacity} {
		if o, ok := seen[step]; !ok || o == OutcomeSkipped {
			return fmt.Errorf("backupsurvival: survives verdict lacks step %q", step)
		}
	}
	for _, a := range attacks {
		if _, ok := seen[StepAttackPrefix+string(a.attack)]; !ok {
			return fmt.Errorf("backupsurvival: survives verdict lacks attack %q", a.attack)
		}
	}
	return nil
}
