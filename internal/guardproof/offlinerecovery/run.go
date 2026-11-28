package offlinerecovery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

// Run admits and executes one exact offline recovery operation. It performs no
// network operation. A persisted pending, failed, unknown, or recovered receipt
// is returned instead of reexecuting the operation. A denial before admission
// (malformed or unsigned authority, binding mismatch, expiry, revocation,
// artifact or executor refusal) is returned to the caller and never journaled:
// the operation ID slot belongs to the first admitted authority, so an
// unauthenticated call can never claim it.
func Run(ctx context.Context, request Request) (Receipt, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if request.Clock == nil {
		return Receipt{}, deny("clock_untrusted", "offline recovery requires a wall and monotonic clock source", "use SystemClock or an equivalent continuing process clock")
	}
	if !filepath.IsAbs(request.WorkspaceRoot) || !filepath.IsAbs(request.CacheRoot) {
		return Receipt{}, deny("local_root_invalid", "offline recovery requires absolute local workspace and cache roots", "select the existing node-local workspace and cache directories")
	}
	if err := validatePlan(request.Plan); err != nil {
		return Receipt{}, err
	}
	started := request.Clock()
	if err := validateClock(started); err != nil {
		return Receipt{}, err
	}
	planDigest, err := request.Plan.Digest()
	if err != nil {
		return Receipt{}, err
	}
	artifactSetDigest, err := request.Plan.ArtifactSetDigest()
	if err != nil {
		return Receipt{}, err
	}
	custody, err := loadCustody(request.WorkspaceRoot)
	if err != nil {
		return Receipt{}, err
	}

	authority, authorityErr := decodeAuthority(request.AuthorityRaw)
	authorityDigest := ""
	if authorityErr == nil {
		authorityDigest, authorityErr = authority.Digest()
	}
	builder, err := newReceiptBuilder(
		request.Plan, authority, authorityDigest, artifactSetDigest, custody.Digest, started,
	)
	if err != nil {
		return Receipt{}, err
	}
	builder.setPlanDigest(planDigest)
	if authorityErr != nil {
		return builder.deny(authorityErr, request.Clock())
	}
	if err := verifyAuthorityBinding(
		request.WorkspaceRoot, authority, request.Plan, planDigest, artifactSetDigest, custody,
	); err != nil {
		return builder.deny(err, request.Clock())
	}

	if stored, exists, err := loadReceipt(request.WorkspaceRoot, request.Plan.OperationID); err != nil {
		return builder.deny(deny("operation_journal_invalid", "the durable offline recovery journal cannot be verified", "inspect and repair the local operation journal; never delete uncertain effect evidence to force a retry"), request.Clock())
	} else if exists {
		if !receiptMatchesBinding(stored, builder.bindingDigest) {
			return builder.deny(deny("operation_journal_conflict", "the operation ID is already bound to a different plan, authority, artifact set, or node", "use a new operation ID only after reconciling the existing receipt; never overwrite another operation"), request.Clock())
		}
		return stored, stored.Error()
	}

	if err := admitAuthorityClock(authority, started); err != nil {
		return builder.deny(err, request.Clock())
	}
	if err := checkRevocation(request.WorkspaceRoot, authority, authorityDigest, planDigest); err != nil {
		return builder.deny(err, request.Clock())
	}
	evidence, content, err := verifyArtifacts(request.CacheRoot, request.Plan, started.Wall)
	if err != nil {
		return builder.deny(err, request.Clock())
	}
	builder.setArtifacts(evidence)
	if needsExecutor(request.Plan) {
		if request.Executor == nil {
			return builder.deny(deny("executor_missing", "the local workload executor is missing", "use the existing node-local Standard Mode workload adapter"), request.Clock())
		}
		if err := validateExecutorIdentity(request.Executor.Identity(), request.Plan.NodeRef); err != nil {
			return builder.deny(err, request.Clock())
		}
	}

	now := request.Clock()
	if err := builder.add("admit", OutcomeOK, "", "authority, node custody, plan, and exact cached artifacts admitted", now.Wall); err != nil {
		return builder.deny(err, now)
	}
	if err := builder.add("journal", OutcomeOK, "", "operation journaled before its first effect", now.Wall); err != nil {
		return builder.deny(err, now)
	}
	if err := builder.finish(VerdictPending, now); err != nil {
		return builder.deny(err, now)
	}
	if err := saveReceipt(request.WorkspaceRoot, builder.receipt, false); err != nil {
		stored, exists, loadErr := loadReceipt(request.WorkspaceRoot, request.Plan.OperationID)
		if loadErr == nil && exists {
			if !receiptMatchesBinding(stored, builder.bindingDigest) {
				return builder.deny(deny("operation_journal_conflict", "another operation owns this journal identity", "reconcile the existing receipt before choosing a new operation ID"), request.Clock())
			}
			return stored, stored.Error()
		}
		return builder.failBeforeJournal(request, deny("operation_journal_persist_failed", "the operation could not be journaled before its first effect", "repair durable local storage; no effect was started"), request.Clock())
	}

	remaining := authority.ExpiresAt.Sub(started.Wall.UTC())
	if remaining <= 0 {
		return builder.failAfterJournal(request, deny("authority_expired", "the authority expired before execution", "obtain a new authority before any effect"), request.Clock())
	}
	executionContext, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()

	for _, step := range request.Plan.Steps {
		if err := executionContext.Err(); err != nil {
			return builder.failAfterJournal(request, deny("execution_cancelled", "the bounded local execution context ended before the next step", "inspect the journal and reconcile completed effects before any new operation"), request.Clock())
		}
		now = request.Clock()
		if err := checkAuthorityWindow(authority, started, now); err != nil {
			return builder.failStepAndReturn(request, step, OutcomeDenied, err, now)
		}
		if err := checkRevocation(request.WorkspaceRoot, authority, authorityDigest, planDigest); err != nil {
			return builder.failStepAndReturn(request, step, OutcomeDenied, err, now)
		}
		currentCustody, err := loadCustody(request.WorkspaceRoot)
		if err != nil || currentCustody.Digest != custody.Digest {
			if err == nil {
				err = deny("node_custody_mismatch", "node custody changed before the next local effect", "stop and reconcile the node's established custody before any new authority")
			}
			return builder.failStepAndReturn(request, step, OutcomeDenied, err, now)
		}
		if step.Kind != StepMaterialize {
			if request.Executor == nil {
				return builder.failStepAndReturn(request, step, OutcomeDenied, deny("executor_missing", "the local workload executor disappeared before the step", "restore the local Standard Mode adapter and inspect the journal"), now)
			}
			if err := validateExecutorIdentity(request.Executor.Identity(), request.Plan.NodeRef); err != nil {
				return builder.failStepAndReturn(request, step, OutcomeDenied, err, now)
			}
		}

		result, effectErr := executeStep(executionContext, request, step, content)
		outcome, code, detail := classifyStep(step, result, effectErr)
		now = request.Clock()
		if err := builder.add("step:"+step.ID, outcome, code, detail, now.Wall); err != nil {
			return builder.failAfterJournal(request, err, now)
		}
		if outcome != OutcomeOK {
			return builder.finishAfterEffect(request, outcome, code, detail, now)
		}

		if err := executionContext.Err(); err != nil {
			return builder.finishAfterEffect(request, OutcomeUnknown, "execution_context_ended", "the execution context ended after a step effect", request.Clock())
		}
		now = request.Clock()
		if err := checkAuthorityWindow(authority, started, now); err != nil {
			return builder.finishAfterEffect(request, OutcomeUnknown, "authority_expired_after_step", "authority was not current after the recorded step effect", now)
		}
		if err := checkRevocation(request.WorkspaceRoot, authority, authorityDigest, planDigest); err != nil {
			return builder.finishAfterEffect(request, OutcomeUnknown, "authority_revoked_after_step", "revocation was observed after the recorded step effect", now)
		}
		currentCustody, err = loadCustody(request.WorkspaceRoot)
		if err != nil || currentCustody.Digest != custody.Digest {
			return builder.finishAfterEffect(request, OutcomeUnknown, "node_custody_changed_after_step", "node custody changed across the recorded step effect", request.Clock())
		}
		if err := saveReceipt(request.WorkspaceRoot, builder.receipt, true); err != nil {
			return builder.journalWriteUnknown(request, now)
		}
	}

	now = request.Clock()
	if err := executionContext.Err(); err != nil {
		return builder.finishAfterEffect(request, OutcomeUnknown, "execution_context_ended", "the execution context ended before completion", now)
	}
	if err := checkAuthorityWindow(authority, started, now); err != nil {
		return builder.finishAfterEffect(request, OutcomeUnknown, "authority_expired_before_completion", "authority was not current at completion", now)
	}
	if err := checkRevocation(request.WorkspaceRoot, authority, authorityDigest, planDigest); err != nil {
		return builder.finishAfterEffect(request, OutcomeUnknown, "authority_revoked_before_completion", "revocation was observed before completion", now)
	}
	finalCustody, err := loadCustody(request.WorkspaceRoot)
	if err != nil || finalCustody.Digest != custody.Digest {
		return builder.finishAfterEffect(request, OutcomeUnknown, "node_custody_changed_before_completion", "node custody changed before completion", request.Clock())
	}
	if err := builder.add("complete", OutcomeOK, "", "all bounded local recipe steps completed and revalidated", now.Wall); err != nil {
		return builder.failAfterJournal(request, err, now)
	}
	if err := builder.finish(VerdictRecovered, now); err != nil {
		return builder.failAfterJournal(request, err, now)
	}
	if err := saveReceipt(request.WorkspaceRoot, builder.receipt, true); err != nil {
		return builder.journalWriteUnknown(request, now)
	}
	return builder.receipt, nil
}

func needsExecutor(plan Plan) bool {
	for _, step := range plan.Steps {
		if step.Kind == StepRestart || step.Kind == StepVerify {
			return true
		}
	}
	return false
}

func executeStep(ctx context.Context, request Request, step Step, content map[string][]byte) (StepResult, error) {
	if step.Kind == StepMaterialize {
		for _, artifact := range request.Plan.Artifacts {
			if artifact.ID == step.ArtifactID {
				return materializeArtifact(ctx, request.WorkspaceRoot, step, artifact, content[artifact.ID])
			}
		}
		return StepResult{Effect: EffectNotApplied}, errors.New("plan artifact disappeared during execution")
	}
	return request.Executor.Execute(ctx, step)
}

func classifyStep(step Step, result StepResult, effectErr error) (outcome, code, detail string) {
	if effectErr != nil {
		if result.Effect == EffectNotApplied {
			return OutcomeFailed, "step_not_applied", "the local step failed without an applied effect"
		}
		return OutcomeUnknown, "step_outcome_unknown", "the local step returned an error without proving a not-applied effect"
	}
	if !digestPattern.MatchString(result.ObservationDigest) {
		return OutcomeUnknown, "step_observation_missing", "the local step returned no digest-bound observation"
	}
	switch step.Kind {
	case StepVerify:
		if result.Effect != EffectNotApplied {
			return OutcomeUnknown, "verify_step_mutated", "a read-only verify step did not prove a not-applied effect"
		}
	case StepMaterialize, StepRestart:
		if result.Effect != EffectApplied {
			if result.Effect == EffectUnknown {
				return OutcomeUnknown, "step_outcome_unknown", "the local step could not prove whether its effect applied"
			}
			return OutcomeFailed, "step_not_applied", "the local step did not apply its authorized effect"
		}
	default:
		return OutcomeUnknown, "step_kind_unsupported", "the local step kind is unsupported"
	}
	return OutcomeOK, "", "the local step effect and observation were admitted"
}

// deny returns the no-effect denial receipt without persisting it. Every caller
// is before the pending journal write, so no effect was started and nothing
// needs reconciliation; journaling it would bind the operation ID to evidence
// an unauthenticated caller controls.
func (b *receiptBuilder) deny(cause error, now ClockReading) (Receipt, error) {
	code, message, _ := denialFields(cause)
	b.receipt.ReconciliationRequired = false
	_ = b.add("admit", OutcomeDenied, code, message, now.Wall)
	_ = b.add("complete", OutcomeDenied, code, message, now.Wall)
	_ = b.finish(VerdictDenied, now)
	return b.receipt, cause
}

func (b *receiptBuilder) failStepAndReturn(request Request, step Step, outcome string, cause error, now ClockReading) (Receipt, error) {
	code, message, _ := denialFields(cause)
	_ = b.add("step:"+step.ID, outcome, code, message, now.Wall)
	_ = b.add("complete", OutcomeFailed, code, message, now.Wall)
	_ = b.finish(VerdictFailed, now)
	return b.persistFailure(request, cause)
}

func (b *receiptBuilder) failBeforeJournal(request Request, cause error, now ClockReading) (Receipt, error) {
	code, message, _ := denialFields(cause)
	b.receipt.ReconciliationRequired = false
	_ = b.add("complete", OutcomeFailed, code, message, now.Wall)
	_ = b.finish(VerdictFailed, now)
	receipt := b.receipt
	if err := saveReceipt(request.WorkspaceRoot, receipt, false); err != nil {
		stored, exists, loadErr := loadReceipt(request.WorkspaceRoot, request.Plan.OperationID)
		if loadErr == nil && exists && receiptMatchesBinding(stored, b.bindingDigest) {
			return stored, stored.Error()
		}
		return receipt, errors.Join(cause, err)
	}
	return receipt, cause
}

func (b *receiptBuilder) failAfterJournal(request Request, cause error, now ClockReading) (Receipt, error) {
	code, message, _ := denialFields(cause)
	_ = b.add("complete", OutcomeFailed, code, message, now.Wall)
	_ = b.finish(VerdictFailed, now)
	return b.persistFailure(request, cause)
}

func (b *receiptBuilder) finishAfterEffect(request Request, outcome, code, detail string, now ClockReading) (Receipt, error) {
	_ = b.add("post-step", outcome, code, detail, now.Wall)
	verdict := VerdictFailed
	if outcome == OutcomeUnknown {
		verdict = VerdictUnknown
	}
	_ = b.finish(verdict, now)
	cause := &Denial{Code: code, Message: detail, Repair: "inspect the durable operation receipt and reconcile observed effects before any new operation"}
	return b.persistFailure(request, cause)
}

func (b *receiptBuilder) journalWriteUnknown(request Request, now ClockReading) (Receipt, error) {
	_ = b.add("journal-persist", OutcomeUnknown, "operation_journal_persist_failed", "the next durable receipt write failed after a recorded effect", now.Wall)
	_ = b.finish(VerdictUnknown, now)
	receipt := b.receipt
	if err := saveReceipt(request.WorkspaceRoot, receipt, true); err != nil {
		return receipt, errors.Join(receipt.Error(), err)
	}
	return receipt, receipt.Error()
}

func (b *receiptBuilder) persistFailure(request Request, cause error) (Receipt, error) {
	receipt := b.receipt
	if err := saveReceipt(request.WorkspaceRoot, receipt, true); err != nil {
		if receipt.Verdict != VerdictUnknown {
			now := request.Clock()
			_ = b.add("journal-persist", OutcomeUnknown, "operation_journal_persist_failed", "the terminal receipt write failed after journaled effects", now.Wall)
			_ = b.finish(VerdictUnknown, now)
			receipt = b.receipt
			if retryErr := saveReceipt(request.WorkspaceRoot, receipt, true); retryErr != nil {
				return receipt, errors.Join(receipt.Error(), err, retryErr)
			}
		}
		return receipt, receipt.Error()
	}
	return receipt, cause
}

func denialFields(cause error) (code, message, repair string) {
	var denial *Denial
	if errors.As(cause, &denial) {
		return denial.Code, denial.Message, denial.Repair
	}
	return "local_operation_failed", "the local operation failed closed", "inspect local custody and the durable journal before any new effect"
}

// LoadReceipt reads and verifies one durable operation receipt without requiring
// the original authority or cached artifacts. Reconnection reconciliation uses
// this path and never reexecutes the operation.
func LoadReceipt(workspaceRoot, operationID string) (Receipt, error) {
	if !refPattern.MatchString(operationID) {
		return Receipt{}, deny("operation_identity_invalid", "the operation identity is invalid", "use the exact operation ID from the recovery plan")
	}
	receipt, exists, err := loadReceipt(workspaceRoot, operationID)
	if err != nil {
		return Receipt{}, fmt.Errorf("load offline recovery receipt: %w", err)
	}
	if !exists {
		return Receipt{}, deny("operation_receipt_missing", "no durable receipt exists for this operation", "verify the operation ID and local workspace before reconciliation")
	}
	return receipt, nil
}
