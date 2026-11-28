package offlinerecovery

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/contracts/canonicaljson"
	"github.com/kombifyio/stackkits/internal/confinedfs"
)

type receiptBinding struct {
	APIVersion         string     `json:"apiVersion"`
	OperationID        string     `json:"operationId"`
	Mode               string     `json:"mode"`
	PlanVersion        string     `json:"planVersion"`
	PlanDigest         string     `json:"planDigest"`
	AuthorityVersion   string     `json:"authorityVersion"`
	AuthorityDigest    string     `json:"authorityDigest"`
	AuthorityIssuedAt  time.Time  `json:"authorityIssuedAt"`
	AuthorityExpiresAt time.Time  `json:"authorityExpiresAt"`
	NodeCustodyDigest  string     `json:"nodeCustodyDigest"`
	ArtifactSetDigest  string     `json:"artifactSetDigest"`
	Artifacts          []Artifact `json:"artifacts"`
	RequiredSteps      []string   `json:"requiredSteps"`
}

func bindingFor(receipt Receipt) receiptBinding {
	artifacts := make([]Artifact, 0, len(receipt.Artifacts))
	for _, evidence := range receipt.Artifacts {
		artifacts = append(artifacts, Artifact{
			ID: evidence.ID, RelativePath: evidence.RelativePath, Version: evidence.Version,
			SHA256: evidence.SHA256, SizeBytes: evidence.SizeBytes,
		})
	}
	return receiptBinding{
		APIVersion: receipt.APIVersion, OperationID: receipt.OperationID, Mode: receipt.Mode,
		PlanVersion: receipt.PlanVersion, PlanDigest: receipt.PlanDigest,
		AuthorityVersion: receipt.AuthorityVersion, AuthorityDigest: receipt.AuthorityDigest,
		AuthorityIssuedAt: receipt.AuthorityIssuedAt, AuthorityExpiresAt: receipt.AuthorityExpiresAt,
		NodeCustodyDigest: receipt.NodeCustodyDigest, ArtifactSetDigest: receipt.ArtifactSetDigest,
		Artifacts: artifacts, RequiredSteps: append([]string(nil), receipt.RequiredSteps...),
	}
}

func receiptBindingDigest(receipt Receipt) (string, error) {
	raw, err := canonicalJSON(bindingFor(receipt))
	if err != nil {
		return "", err
	}
	return canonicaljson.SHA256Hex(raw), nil
}

func receiptGenesis(bindingDigest string) string {
	return canonicaljson.SHA256Hex([]byte(ReceiptSchemaVersion), []byte{0}, []byte(bindingDigest))
}

func entryHash(entry Entry) (string, error) {
	entry.Hash = ""
	raw, err := canonicalJSON(entry)
	if err != nil {
		return "", err
	}
	return canonicaljson.SHA256Hex([]byte(entry.PrevHash), []byte{0}, raw), nil
}

type receiptBuilder struct {
	receipt       Receipt
	bindingDigest string
}

func newReceiptBuilder(
	plan Plan,
	authority Authority,
	authorityDigest, artifactSetDigest, custodyDigest string,
	started ClockReading,
) (*receiptBuilder, error) {
	artifacts := make([]ArtifactEvidence, 0, len(plan.Artifacts))
	for _, artifact := range plan.Artifacts {
		artifacts = append(artifacts, ArtifactEvidence{
			ID: artifact.ID, Version: artifact.Version, RelativePath: artifact.RelativePath,
			SHA256: artifact.SHA256, SizeBytes: artifact.SizeBytes,
		})
	}
	steps := make([]string, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		steps = append(steps, step.ID)
	}
	sort.Strings(steps)
	unknowns := sortedCopy(RequiredUnknowns)
	notClaimed := sortedCopy(RequiredNotClaimed)
	authorityAge := 0.0
	if !authority.IssuedAt.IsZero() {
		authorityAge = started.Wall.UTC().Sub(authority.IssuedAt).Seconds()
		if authorityAge < 0 {
			authorityAge = 0
		}
	}
	receipt := Receipt{
		APIVersion: ReceiptSchemaVersion, OperationID: plan.OperationID, Mode: plan.Mode,
		PlanVersion: plan.Version, PlanDigest: "", AuthorityVersion: authority.Version,
		AuthorityDigest: authorityDigest, AuthorityIssuedAt: authority.IssuedAt,
		AuthorityExpiresAt: authority.ExpiresAt, NodeCustodyDigest: custodyDigest,
		ArtifactSetDigest: artifactSetDigest, Artifacts: artifacts, RequiredSteps: steps,
		Verdict: VerdictPending, Timing: Timing{
			StartedAt: started.Wall.UTC(), AuthorityAgeSeconds: authorityAge,
		},
		Unknowns: unknowns, NotClaimed: notClaimed, ReconciliationRequired: true,
	}
	builder := &receiptBuilder{receipt: receipt}
	bindingDigest, err := receiptBindingDigest(receipt)
	if err != nil {
		return nil, err
	}
	builder.bindingDigest = bindingDigest
	builder.receipt.BindingDigest = bindingDigest
	builder.receipt.Genesis = receiptGenesis(bindingDigest)
	return builder, nil
}

func (b *receiptBuilder) setPlanDigest(digest string) {
	b.receipt.PlanDigest = digest
	bindingDigest, err := receiptBindingDigest(b.receipt)
	if err == nil {
		b.bindingDigest = bindingDigest
		b.receipt.BindingDigest = bindingDigest
		b.receipt.Genesis = receiptGenesis(bindingDigest)
	}
}

func (b *receiptBuilder) setArtifacts(evidence []ArtifactEvidence) {
	b.receipt.Artifacts = append([]ArtifactEvidence(nil), evidence...)
}

func (b *receiptBuilder) add(step, outcome, code, detail string, at time.Time) error {
	at = at.UTC()
	if len(b.receipt.Entries) > 0 && at.Before(b.receipt.Entries[len(b.receipt.Entries)-1].At) {
		return deny("clock_untrusted", "the local clock moved backwards while recording the receipt", "stop and inspect the durable operation journal; do not rewrite receipt time")
	}
	entry := Entry{
		Seq: len(b.receipt.Entries) + 1, Step: step, Outcome: outcome,
		Code: code, Detail: detail, At: at, PrevHash: b.receipt.HeadHash,
	}
	if entry.PrevHash == "" {
		entry.PrevHash = b.receipt.Genesis
	}
	hash, err := entryHash(entry)
	if err != nil {
		return err
	}
	entry.Hash = hash
	b.receipt.Entries = append(b.receipt.Entries, entry)
	b.receipt.HeadHash = hash
	return nil
}

func (b *receiptBuilder) finish(verdict string, finished ClockReading) error {
	if verdict == VerdictPending {
		b.receipt.Verdict = verdict
		return nil
	}
	finishedAt := finished.Wall.UTC()
	if finishedAt.Before(b.receipt.Timing.StartedAt) {
		return deny("clock_untrusted", "the recovery receipt finished before it started", "stop and inspect the operation journal on a trusted clock")
	}
	b.receipt.Timing.FinishedAt = finishedAt
	b.receipt.Timing.DurationSeconds = finishedAt.Sub(b.receipt.Timing.StartedAt).Seconds()
	b.receipt.Verdict = verdict
	return nil
}

func sortedCopy(values []string) []string {
	copy := append([]string(nil), values...)
	sort.Strings(copy)
	return copy
}

func deriveVerdict(receipt Receipt, pending bool) string {
	seenSteps := make(map[string]string, len(receipt.Entries))
	admitOK, admitDenied := false, false
	journalOK := false
	completeOK := false
	for _, entry := range receipt.Entries {
		if entry.Outcome == OutcomeUnknown {
			return VerdictUnknown
		}
		switch entry.Step {
		case "admit":
			admitOK = entry.Outcome == OutcomeOK
			admitDenied = entry.Outcome == OutcomeDenied
		case "journal":
			journalOK = entry.Outcome == OutcomeOK
		case "complete":
			completeOK = entry.Outcome == OutcomeOK
		default:
			if strings.HasPrefix(entry.Step, "step:") {
				if _, exists := seenSteps[entry.Step]; exists {
					return VerdictFailed
				}
				seenSteps[entry.Step] = entry.Outcome
			}
		}
	}
	if pending {
		return VerdictPending
	}
	if admitDenied && len(seenSteps) == 0 {
		return VerdictDenied
	}
	for _, step := range receipt.RequiredSteps {
		if seenSteps["step:"+step] != OutcomeOK {
			return VerdictFailed
		}
	}
	if admitOK && journalOK && completeOK && len(seenSteps) == len(receipt.RequiredSteps) {
		return VerdictRecovered
	}
	return VerdictFailed
}

// Verify recomputes the binding, genesis, every chain link, verdict, timing, and
// mandatory explicit bounds. It trusts no stored hash or verdict.
func (r Receipt) Verify() error {
	if r.APIVersion != ReceiptSchemaVersion || r.Mode != ModeStandard || !refPattern.MatchString(r.OperationID) {
		return errors.New("offlinerecovery: unsupported receipt contract or identity")
	}
	if !digestPattern.MatchString(r.PlanDigest) || !digestPattern.MatchString(r.ArtifactSetDigest) ||
		!digestPattern.MatchString(r.NodeCustodyDigest) || r.Reexecuted ||
		!refPattern.MatchString(r.PlanVersion) {
		return errors.New("offlinerecovery: receipt binding is incomplete or claims reexecution")
	}
	if r.AuthorityDigest != "" && (!digestPattern.MatchString(r.AuthorityDigest) ||
		!refPattern.MatchString(r.AuthorityVersion) || !canonicalUTC(r.AuthorityIssuedAt) ||
		!canonicalUTC(r.AuthorityExpiresAt) || !r.AuthorityExpiresAt.After(r.AuthorityIssuedAt) ||
		r.AuthorityExpiresAt.Sub(r.AuthorityIssuedAt) > MaxAuthorityLifetime) {
		return errors.New("offlinerecovery: receipt authority digest or lifetime is invalid")
	}
	// Only a denial may lack an authority digest, and a denial is returned to its
	// caller, never journaled (saveReceipt refuses it), so a receipt without an
	// admitted authority can never bind an operation ID.
	if r.Verdict != VerdictDenied && r.AuthorityDigest == "" {
		return errors.New("offlinerecovery: non-denial receipt omits its authority")
	}
	if len(r.Artifacts) == 0 || len(r.Artifacts) > MaxArtifacts || len(r.RequiredSteps) == 0 || len(r.RequiredSteps) > MaxSteps {
		return errors.New("offlinerecovery: receipt artifact or step set is outside its bound")
	}
	previousID := ""
	var totalArtifactSize int64
	for _, evidence := range r.Artifacts {
		_, pathErr := confinedfs.ValidatePortablePath(evidence.RelativePath)
		if evidence.ID <= previousID || !refPattern.MatchString(evidence.ID) ||
			!refPattern.MatchString(evidence.Version) || pathErr != nil ||
			!digestPattern.MatchString(evidence.SHA256) || evidence.SizeBytes <= 0 ||
			evidence.SizeBytes > MaxArtifactBytes || evidence.EvidenceAgeSeconds < 0 {
			return errors.New("offlinerecovery: receipt artifact evidence is invalid")
		}
		previousID = evidence.ID
		totalArtifactSize += evidence.SizeBytes
		if totalArtifactSize > MaxTotalArtifactSize {
			return errors.New("offlinerecovery: receipt artifact evidence exceeds the total bound")
		}
		if r.Verdict == VerdictRecovered {
			if evidence.CacheObservedAt.IsZero() || evidence.CacheObservedAt.Location() != time.UTC ||
				evidence.CacheObservedAt.After(r.Timing.StartedAt.Add(MaxClockSkew)) {
				return errors.New("offlinerecovery: recovered receipt omits or misstates cache observation time")
			}
			expectedAge := r.Timing.StartedAt.Sub(evidence.CacheObservedAt).Seconds()
			if expectedAge < 0 {
				expectedAge = 0
			}
			if math.Abs(evidence.EvidenceAgeSeconds-expectedAge) > 1e-6 {
				return errors.New("offlinerecovery: recovered receipt misstates cache evidence age")
			}
		}
	}
	if !sortedUniqueStrings(r.RequiredSteps) {
		return errors.New("offlinerecovery: receipt required steps are duplicated or unsorted")
	}
	for _, step := range r.RequiredSteps {
		if !refPattern.MatchString(step) {
			return errors.New("offlinerecovery: receipt contains an invalid required step identity")
		}
	}
	if !containsAll(r.Unknowns, RequiredUnknowns) || !containsAll(r.NotClaimed, RequiredNotClaimed) ||
		!sortedUniqueStrings(r.Unknowns) || !sortedUniqueStrings(r.NotClaimed) {
		return errors.New("offlinerecovery: receipt omits or duplicates a mandatory explicit bound")
	}
	bindingDigest, err := receiptBindingDigest(r)
	if err != nil {
		return err
	}
	if r.BindingDigest != bindingDigest {
		return errors.New("offlinerecovery: receipt binding digest does not match its header")
	}
	genesis := receiptGenesis(bindingDigest)
	if r.Genesis != genesis || len(r.Entries) == 0 {
		return errors.New("offlinerecovery: receipt genesis or entries are invalid")
	}
	previous := genesis
	last := r.Timing.StartedAt
	for index, entry := range r.Entries {
		if entry.Seq != index+1 || entry.PrevHash != previous || entry.At.Before(last) || entry.At.Location() != time.UTC {
			return fmt.Errorf("offlinerecovery: receipt chain broken at entry %d", index+1)
		}
		want, err := entryHash(entry)
		if err != nil || want != entry.Hash {
			return fmt.Errorf("offlinerecovery: receipt entry %d was altered", index+1)
		}
		switch entry.Outcome {
		case OutcomeOK, OutcomeDenied, OutcomeFailed, OutcomeUnknown:
		default:
			return fmt.Errorf("offlinerecovery: receipt entry %d has an invalid outcome", index+1)
		}
		if len(entry.Detail) > 512 || len(entry.Code) > 128 || len(entry.Step) > 160 {
			return fmt.Errorf("offlinerecovery: receipt entry %d exceeds its text bound", index+1)
		}
		previous, last = entry.Hash, entry.At
	}
	if r.HeadHash != previous {
		return errors.New("offlinerecovery: receipt head hash does not match the chain")
	}
	pending := r.Verdict == VerdictPending
	if verdict := deriveVerdict(r, pending); verdict != r.Verdict {
		return errors.New("offlinerecovery: receipt verdict does not replay from its entries")
	}
	if r.Timing.StartedAt.IsZero() || r.Timing.StartedAt.Location() != time.UTC || r.Timing.DurationSeconds < 0 {
		return errors.New("offlinerecovery: receipt timing is invalid")
	}
	expectedAuthorityAge := 0.0
	if !r.AuthorityIssuedAt.IsZero() {
		expectedAuthorityAge = r.Timing.StartedAt.Sub(r.AuthorityIssuedAt).Seconds()
		if expectedAuthorityAge < 0 {
			expectedAuthorityAge = 0
		}
	}
	if math.Abs(r.Timing.AuthorityAgeSeconds-expectedAuthorityAge) > 1e-6 {
		return errors.New("offlinerecovery: receipt authority age does not match its timing")
	}
	if pending {
		if !r.Timing.FinishedAt.IsZero() || r.Timing.DurationSeconds != 0 || !r.ReconciliationRequired {
			return errors.New("offlinerecovery: pending receipt has terminal timing or no reconciliation requirement")
		}
	} else {
		if r.Timing.FinishedAt.IsZero() || r.Timing.FinishedAt.Location() != time.UTC ||
			r.Timing.FinishedAt.Before(last) ||
			math.Abs(r.Timing.DurationSeconds-r.Timing.FinishedAt.Sub(r.Timing.StartedAt).Seconds()) > 1e-6 {
			return errors.New("offlinerecovery: terminal receipt timing does not match its chain")
		}
		if r.Verdict == VerdictRecovered && !r.ReconciliationRequired {
			return errors.New("offlinerecovery: recovered receipt does not require reconnect reconciliation")
		}
		if r.Verdict == VerdictDenied && r.ReconciliationRequired {
			return errors.New("offlinerecovery: no-effect denial unexpectedly requires reconciliation")
		}
	}
	return nil
}

func containsAll(values, required []string) bool {
	set := stringSet(values)
	for _, value := range required {
		if _, exists := set[value]; !exists {
			return false
		}
	}
	return true
}

// Error returns the stable failure for a non-recovered receipt. A recovered
// receipt returns nil; pending and unknown receipts return ErrOutcomeUnknown.
func (r Receipt) Error() error {
	switch r.Verdict {
	case "":
		return errors.New("offlinerecovery: receipt has no verdict")
	case VerdictRecovered:
		return nil
	case VerdictPending, VerdictUnknown:
		code := "outcome_unknown"
		message := "the durable operation may have applied an effect and must not be reexecuted"
		return errors.Join(ErrOutcomeUnknown, &Denial{Code: code, Message: message, Repair: "inspect the local receipt and reconcile the observed effect before any new recovery operation"})
	}
	code := r.Verdict
	message := "the offline recovery operation was not completed"
	for index := len(r.Entries) - 1; index >= 0; index-- {
		entry := r.Entries[index]
		if entry.Outcome != OutcomeOK {
			if entry.Code != "" {
				code = entry.Code
			}
			if entry.Detail != "" {
				message = entry.Detail
			}
			break
		}
	}
	return &Denial{Code: code, Message: message, Repair: "follow the receipt's denial repair, inspect local custody, and obtain new authority before any new effect"}
}
