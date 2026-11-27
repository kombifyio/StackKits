package ownapplication

import (
	"context"
	"errors"
	"regexp"
	"time"

	contract "github.com/kombifyio/stackkits/contracts/ownapplication"
)

// Guard C producer: reverse one image update whose canary the owning consumer
// measured as failed. The producer performs no health decision of its own; it
// accepts that authorized outcome as evidence, proves it names the admitted
// update, and restores the previous admitted digest and configuration through
// the existing own-application lifecycle. Every step keeps the existing
// operation ID and review-digest fencing.
//
// This covers one stateless application change set. A volume-backed or
// checkpointed update is refused: restoring arbitrary data is not promised.
const (
	// ReverseVersion is the wire version of one reverse of an unhealthy update.
	ReverseVersion = "stackkit.own-application-reverse/v1"
	// ReverseEvidenceSchema is the exact execution evidence of a reversal.
	ReverseEvidenceSchema = "stackkit.own-application-reverse-evidence/v1"

	// CanaryFailed is the only canary outcome that starts a reversal.
	CanaryFailed = "failed"

	// Reverse outcomes.
	ReverseReviewed = "reviewed"
	ReverseReversed = "reversed"
	ReverseRefused  = "refused"
	ReverseFailed   = "reverse_failed"
)

// Guard C reason codes refused before any effect: one candidate identity and
// one reversal of an unhealthy update.
const (
	ReasonIdentityRefused  = "image_identity_refused"
	ReasonAdmissionInvalid = "image_admission_invalid"
	ReasonCanaryRefused    = "canary_outcome_invalid"
	ReasonStatefulReverse  = "stateful_change_set_unsupported"
	ReasonPreviousAdmitted = "previous_admission_unavailable"
)

// CanaryDecisionMaxAge bounds how old an authorized failed-canary outcome may
// be when it reverses an update. A stale decision names a state the application
// no longer runs.
const CanaryDecisionMaxAge = 24 * time.Hour

var canaryDecisionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

var (
	errStatefulReverse  = errors.New("a reversal covers one stateless one-container image change; this application has a volume or a data checkpoint")
	errPreviousAdmitted = errors.New("the application and its admission record no previous admitted image")
)

// CanaryOutcome is the owning consumer's decision about the update that just
// ran. It is evidence, not authority on its own: the reversal still requires
// OwnerApproved, a distinct operation ID and the review digest of its dry run.
type CanaryOutcome struct {
	// DecisionRef names the consumer's durable decision, never a host path.
	DecisionRef string `json:"decisionRef"`
	// Outcome must be CanaryFailed.
	Outcome string `json:"outcome"`
	// Authorized reports that the owning consumer authorized the reversal. A
	// false value is refused rather than treated as an unverified failure.
	Authorized bool `json:"authorized"`
	// ChangeSet and Digest bind the decision to the admission it measured.
	ChangeSet string `json:"changeSet"`
	Digest    string `json:"digest,omitempty"`
	// ObservedAt is when the consumer measured the failed canary.
	ObservedAt time.Time `json:"observedAt"`
}

func (c CanaryOutcome) validate(now time.Time) error {
	if !canaryDecisionPattern.MatchString(c.DecisionRef) {
		return errors.New("a canary outcome requires a bounded decision reference")
	}
	if c.Outcome != CanaryFailed {
		return errors.New("only a failed canary outcome reverses an update")
	}
	if !c.Authorized {
		return errors.New("the canary outcome is not authorized for reversal")
	}
	if !imageIdentityDigestPattern.MatchString(c.ChangeSet) {
		return errors.New("the canary outcome has no admitted change set")
	}
	if c.Digest != "" && !imageIdentityDigestPattern.MatchString(c.Digest) {
		return errors.New("the canary outcome digest is invalid")
	}
	if c.ObservedAt.IsZero() || c.ObservedAt.After(now.Add(5*time.Minute)) || now.Sub(c.ObservedAt) > CanaryDecisionMaxAge {
		return errors.New("the canary observation time is missing, in the future or too old")
	}
	return nil
}

// ReverseRequest asks to restore the previous admitted image of one update
// after an authorized failed canary. Admission is the receipt that update
// returned; Canary is the owning consumer's failed decision.
type ReverseRequest struct {
	Name          string
	Admission     *ImageAdmission
	Canary        CanaryOutcome
	OperationID   string
	ReviewDigest  string
	OwnerApproved bool
	DryRun        bool
}

// ReverseEvidence is the exact, secret-free execution evidence of one reversal.
type ReverseEvidence struct {
	SchemaVersion       string    `json:"schemaVersion"`
	Name                string    `json:"name"`
	ChangeSet           string    `json:"changeSet"`
	UpdateOperationID   string    `json:"updateOperationId"`
	ReverseOperationID  string    `json:"reverseOperationId"`
	UpdateReviewDigest  string    `json:"updateReviewDigest"`
	ReverseReviewDigest string    `json:"reverseReviewDigest"`
	FromImage           string    `json:"fromImage"`
	FromResolved        string    `json:"fromResolved"`
	FromDigest          string    `json:"fromDigest"`
	FromImageID         string    `json:"fromImageID,omitempty"`
	FromConfig          string    `json:"fromConfig,omitempty"`
	ToImage             string    `json:"toImage"`
	ToResolved          string    `json:"toResolved"`
	ToDigest            string    `json:"toDigest"`
	ToImageID           string    `json:"toImageID,omitempty"`
	ToConfig            string    `json:"toConfig,omitempty"`
	PreviousImageID     string    `json:"previousImageID,omitempty"`
	PreviousConfig      string    `json:"previousConfig,omitempty"`
	ConfigMatch         bool      `json:"configMatch"`
	ConfigDelta         []string  `json:"configDelta,omitempty"`
	Signer              string    `json:"signer"`
	Issuer              string    `json:"issuer"`
	TrustDigest         string    `json:"trustDigest"`
	CanaryDecision      string    `json:"canaryDecision"`
	CanaryOutcome       string    `json:"canaryOutcome"`
	CanaryDigest        string    `json:"canaryDigest,omitempty"`
	Verified            bool      `json:"verified"`
	At                  time.Time `json:"at"`
}

// ReverseResult is the outcome of a reversal: reviewed (dry run), reversed,
// refused before effects, or reverse_failed after effects started.
type ReverseResult struct {
	APIVersion   string           `json:"apiVersion"`
	Name         string           `json:"name"`
	Outcome      string           `json:"outcome"`
	Reason       string           `json:"reasonCode,omitempty"`
	Message      string           `json:"message,omitempty"`
	ReviewDigest string           `json:"reviewDigest,omitempty"`
	OperationID  string           `json:"operationId,omitempty"`
	Admission    *ImageAdmission  `json:"admission,omitempty"`
	Rollback     *Result          `json:"rollback,omitempty"`
	Evidence     *ReverseEvidence `json:"evidence,omitempty"`
}

// ReverseUnhealthyUpdate restores the previous admitted digest and
// configuration of one stateless image update. The dry run returns the review
// digest of the exact reversal; the execution names it with its own operation
// ID. Any mismatch refuses before the application stops.
func ReverseUnhealthyUpdate(ctx context.Context, workspace string, installation contract.Installation, req ReverseRequest) (ReverseResult, error) {
	out := ReverseResult{APIVersion: ReverseVersion, Name: req.Name, OperationID: req.OperationID}
	refuse := func(reason, message string) (ReverseResult, error) {
		out.Outcome, out.Reason, out.Message = ReverseRefused, reason, message
		return out, errors.New(message)
	}
	if req.Admission == nil {
		return refuse(ReasonAdmissionInvalid, "a reversal requires the image admission receipt of the update it reverses")
	}
	if err := req.Admission.Validate(); err != nil {
		return refuse(ReasonAdmissionInvalid, err.Error())
	}
	if err := req.Canary.validate(time.Now().UTC()); err != nil {
		return refuse(ReasonCanaryRefused, err.Error())
	}
	s, err := loadOwnApplications(workspace)
	if err != nil {
		return refuse(ReasonPending, err.Error())
	}
	current, exists := s.Applications[req.Name]
	if !exists || current.State == "removed" {
		return refuse(ReasonNotFound, "own application is absent")
	}
	// A retried reversal with the same operation ID is the completed operation,
	// not a new decision. Execute still verifies the exact request and review
	// digest; a different request under the same ID is refused.
	if !req.DryRun && current.Operation.ID == req.OperationID && current.Operation.Action == "rollback" {
		if !req.OwnerApproved || req.ReviewDigest == "" {
			return refuse(ReasonApprovalMissing, "retrying a reversal requires its original --owner-approve and --review-digest")
		}
		request := Request{Action: "rollback", Name: req.Name, Admission: req.Admission, Canary: &req.Canary, OperationID: req.OperationID, ReviewDigest: req.ReviewDigest, OwnerApproved: true}
		rolled, e := executeOwnApplication(ctx, workspace, installation, request)
		out.Rollback = &rolled
		out.Admission = req.Admission
		if e != nil {
			out.Outcome, out.Reason, out.Message = ReverseFailed, ReasonImageRollback, e.Error()
			return out, e
		}
		if rolled.Record.Bundle.ImageID == "" {
			out.Outcome, out.Reason, out.Message = ReverseFailed, ReasonRollbackVerify, "the restored image has no observed runtime configuration identity"
			return out, errors.New(out.Message)
		}
		if e = verifyRunning(ctx, workspace, installation, req.Name); e != nil {
			out.Outcome, out.Reason, out.Message = ReverseFailed, ReasonRollbackVerify, e.Error()
			out.Evidence = reverseEvidence(*req.Admission, req.Canary, current, rolled.Record, req.OperationID, req.ReviewDigest)
			out.Evidence.Verified = false
			return out, e
		}
		out.Outcome = ReverseReversed
		out.Evidence = reverseEvidence(*req.Admission, req.Canary, current, rolled.Record, req.OperationID, req.ReviewDigest)
		return out, nil
	}
	if err = validateReverseState(current, *req.Admission, req.Canary, req.Name, req.OperationID); err != nil {
		reason := ReasonAdmissionInvalid
		switch {
		case errors.Is(err, errStatefulReverse):
			reason = ReasonStatefulReverse
		case errors.Is(err, errPreviousAdmitted):
			reason = ReasonPreviousAdmitted
		}
		return refuse(reason, err.Error())
	}
	out.Admission = req.Admission
	request := Request{Action: "rollback", Name: req.Name, Admission: req.Admission, Canary: &req.Canary}
	if req.DryRun {
		request.DryRun = true
		reviewed, e := executeOwnApplication(ctx, workspace, installation, request)
		if e != nil {
			return refuse(ReasonAdmissionInvalid, e.Error())
		}
		out.Outcome, out.ReviewDigest = ReverseReviewed, reviewed.ReviewDigest
		return out, nil
	}
	if !req.OwnerApproved || req.OperationID == "" || req.ReviewDigest == "" {
		return refuse(ReasonApprovalMissing, "a reversal runs with --owner-approve, --operation-id and the --review-digest of its dry run")
	}
	request.OperationID, request.ReviewDigest, request.OwnerApproved = req.OperationID, req.ReviewDigest, true
	rolled, err := executeOwnApplication(ctx, workspace, installation, request)
	out.Rollback = &rolled
	if err != nil {
		out.Outcome, out.Reason, out.Message = ReverseFailed, ReasonImageRollback, err.Error()
		return out, err
	}
	if rolled.Record.Bundle.ImageID == "" {
		out.Outcome, out.Reason, out.Message = ReverseFailed, ReasonRollbackVerify, "the restored image has no observed runtime configuration identity"
		return out, errors.New(out.Message)
	}
	if err = verifyRunning(ctx, workspace, installation, req.Name); err != nil {
		out.Outcome, out.Reason, out.Message = ReverseFailed, ReasonRollbackVerify, err.Error()
		out.Evidence = reverseEvidence(*req.Admission, req.Canary, current, rolled.Record, req.OperationID, req.ReviewDigest)
		out.Evidence.Verified = false
		return out, err
	}
	out.Outcome = ReverseReversed
	out.Evidence = reverseEvidence(*req.Admission, req.Canary, current, rolled.Record, req.OperationID, req.ReviewDigest)
	return out, nil
}

// validateReverseState proves the admission names the update the application
// currently runs and that the failed canary measured that same change set. It
// refuses a stateful change set before any effect.
func validateReverseState(current Record, admission ImageAdmission, canary CanaryOutcome, name, reverseOperationID string) error {
	if err := canary.validate(time.Now().UTC()); err != nil {
		return err
	}
	if admission.Name != name {
		return errors.New("the image admission belongs to a different own application")
	}
	if current.Bundle.Application.IsProject() || len(applicationVolumes(current.Bundle)) != 0 || current.Checkpoint != nil {
		return errStatefulReverse
	}
	if current.Operation.Status != "succeeded" || current.Operation.Action != "update" || current.TransitionFrom != nil {
		return errors.New("the application does not hold one completed image update to reverse")
	}
	if current.Admission == nil || *current.Admission != admission {
		return errors.New("the image admission does not match the update this application runs")
	}
	if admission.Resolved != current.Bundle.ResolvedImage {
		return errors.New("the image admission digest differs from the running image")
	}
	if _, digest, err := contract.SplitImage(current.Bundle.ResolvedImage); err != nil || digest != admission.Digest {
		return errors.New("the image admission digest differs from the running image")
	}
	if admission.ReviewDigest == "" || admission.ReviewDigest != current.Operation.ReviewDigest {
		return errors.New("the image admission does not name the update's reviewed digest")
	}
	if admission.ImageID == "" || admission.ImageID != current.Bundle.ImageID {
		return errors.New("the image admission does not name the running image configuration")
	}
	if admission.BundleRevision == "" || admission.BundleRevision != current.Revision {
		return errors.New("the image admission does not name the running bundle revision")
	}
	if current.PreviousImage == "" || current.PreviousResolvedImage == "" {
		return errPreviousAdmitted
	}
	if admission.Previous.Image != current.PreviousImage || admission.Previous.Resolved != current.PreviousResolvedImage {
		return errors.New("the image admission does not retain the previous admitted image this rollback restores")
	}
	if canary.ChangeSet != admission.ChangeSet {
		return errors.New("the failed canary measured a different admitted change set")
	}
	if canary.Digest != "" && canary.Digest != admission.Digest {
		return errors.New("the failed canary measured a different image digest")
	}
	if reverseOperationID != "" && reverseOperationID == current.Operation.ID {
		return errors.New("a reversal needs its own operation ID; it never reuses the update's")
	}
	if admission.OperationID == "" || admission.OperationID != current.Operation.ID {
		return errors.New("the image admission does not name the update operation this application ran")
	}
	return nil
}

// reverseEvidence records the exact before and after custody plus the decision
// that started it. ToImage and ToDigest are read from the executed record, not
// assumed from the admission.
func reverseEvidence(admission ImageAdmission, canary CanaryOutcome, before, after Record, reverseOperationID, reverseReview string) *ReverseEvidence {
	toDigest := ""
	if _, digest, err := contract.SplitImage(after.Bundle.ResolvedImage); err == nil {
		toDigest = digest
	}
	deltas := []string{}
	if admission.Previous.ImageID == "" {
		deltas = append(deltas, "previous_image_configuration_unrecorded")
	} else if after.Bundle.ImageID != "" && admission.Previous.ImageID != after.Bundle.ImageID {
		deltas = append(deltas, "previous_image_configuration_changed")
	}
	if admission.Previous.BundleRevision != "" && after.Revision != "" && admission.Previous.BundleRevision != after.Revision {
		deltas = append(deltas, "previous_bundle_revision_changed")
	}
	if after.Bundle.ImageID == "" {
		deltas = append(deltas, "restored_image_configuration_unobserved")
	}
	return &ReverseEvidence{
		SchemaVersion:       ReverseEvidenceSchema,
		Name:                admission.Name,
		ChangeSet:           admission.ChangeSet,
		UpdateOperationID:   admission.OperationID,
		ReverseOperationID:  reverseOperationID,
		UpdateReviewDigest:  admission.ReviewDigest,
		ReverseReviewDigest: reverseReview,
		FromImage:           admission.Image,
		FromResolved:        admission.Resolved,
		FromDigest:          admission.Digest,
		FromImageID:         admission.ImageID,
		FromConfig:          admission.BundleRevision,
		ToImage:             after.Bundle.Application.Image,
		ToResolved:          after.Bundle.ResolvedImage,
		ToDigest:            toDigest,
		ToImageID:           after.Bundle.ImageID,
		ToConfig:            after.Revision,
		PreviousImageID:     admission.Previous.ImageID,
		PreviousConfig:      admission.Previous.BundleRevision,
		ConfigMatch:         len(deltas) == 0,
		ConfigDelta:         deltas,
		Signer:              admission.Signer,
		Issuer:              admission.Issuer,
		TrustDigest:         admission.TrustDigest,
		CanaryDecision:      canary.DecisionRef,
		CanaryOutcome:       canary.Outcome,
		CanaryDigest:        canary.Digest,
		Verified:            true,
		At:                  time.Now().UTC(),
	}
}
