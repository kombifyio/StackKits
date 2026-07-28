package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/executionchannelbundle"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/runtimeexecutorv2"
	"github.com/spf13/cobra"
)

const runtimeExecuteEventSchema = "stackkit.runtime-execute-event/v1"

type runtimeExecuteError struct {
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type runtimeExecuteEvent struct {
	SchemaVersion       string                           `json:"schemaVersion"`
	Sequence            int                              `json:"sequence"`
	Type                string                           `json:"type"`
	BundleDigest        string                           `json:"bundleDigest"`
	RequestDigest       string                           `json:"requestDigest,omitempty"`
	ExecutionChannelRef string                           `json:"executionChannelRef,omitempty"`
	SiteRef             string                           `json:"siteRef,omitempty"`
	NodeRef             string                           `json:"nodeRef,omitempty"`
	Result              *runtimeexecutor.ExecutionResult `json:"result,omitempty"`
	Error               *runtimeExecuteError             `json:"error,omitempty"`
}

type runtimeExecuteAdmission struct {
	Verified    executionchannelbundle.Verified
	TrustDigest string
	OwnerRef    string
	OwnerKeyID  string
	Binding     localevidence.LocalBinding
}

type runtimeExecuteAdmissionError struct {
	Code  string
	Field string
	Err   error
}

func (e *runtimeExecuteAdmissionError) Error() string { return e.Err.Error() }
func (e *runtimeExecuteAdmissionError) Unwrap() error { return e.Err }

type runtimeExecuteDependencies struct {
	now     func() time.Time
	admit   func(string, []byte, time.Time) (runtimeExecuteAdmission, error)
	mutate  func(string, string, func() error) error
	execute func(context.Context, string, runtimeExecuteAdmission) (runtimeexecutor.ExecutionResult, error)
}

var runtimeExecuteDeps = runtimeExecuteDependencies{
	now:     time.Now,
	admit:   admitRuntimeExecute,
	mutate:  withLifecycleMutation,
	execute: executeRuntimeAdmission,
}

var runtimeCmd = &cobra.Command{
	Use:   "runtime",
	Short: "Execute authenticated node-side runtime operations",
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
}

var runtimeExecuteCmd = &cobra.Command{
	Use:   "execute",
	Short: "Execute one signed execution-channel-bundle/v1 from stdin",
	Args:  cobra.NoArgs,
	RunE:  runRuntimeExecute,
}

func init() {
	runtimeCmd.AddCommand(runtimeExecuteCmd)
	rootCmd.AddCommand(runtimeCmd)
}

func runRuntimeExecute(cmd *cobra.Command, _ []string) error {
	workspaceRoot, err := filepath.Abs(getWorkDir())
	if err != nil {
		return fmt.Errorf("resolve runtime workspace: %w", err)
	}
	bundleRaw, err := io.ReadAll(io.LimitReader(
		cmd.InOrStdin(),
		executionchannelbundle.MaxBundleBytes+1,
	))
	if err != nil {
		return fmt.Errorf("read execution-channel bundle from stdin: %w", err)
	}
	rawDigest := sha256.Sum256(bundleRaw)
	bundleDigest := "sha256:" + hex.EncodeToString(rawDigest[:])
	failureBase := runtimeExecuteEvent{
		SchemaVersion: runtimeExecuteEventSchema,
		Sequence:      1,
		Type:          "failed",
		BundleDigest:  bundleDigest,
	}
	if len(bundleRaw) == 0 || len(bundleRaw) > executionchannelbundle.MaxBundleBytes {
		return writeRuntimeExecuteFailure(cmd, failureBase, "bundle_rejected", "", "execution-channel bundle size is outside the allowed bound")
	}
	now := runtimeExecuteDeps.now().UTC().Truncate(time.Second)
	admission, err := runtimeExecuteDeps.admit(workspaceRoot, bundleRaw, now)
	if err != nil {
		code, field := runtimeAdmissionReason(err)
		return writeRuntimeExecuteFailure(cmd, failureBase, code, field, err.Error())
	}
	base := runtimeEventForAdmission(admission)
	eventWritten := false
	err = runtimeExecuteDeps.mutate(workspaceRoot, "runtime execute", func() error {
		revalidated, revalidateErr := runtimeExecuteDeps.admit(workspaceRoot, bundleRaw, now)
		if revalidateErr != nil {
			code, field := runtimeAdmissionReason(revalidateErr)
			eventWritten = true
			return writeRuntimeExecuteFailure(cmd, failureBase, code, field, revalidateErr.Error())
		}
		if !equalRuntimeExecuteAdmission(admission, revalidated) {
			eventWritten = true
			return writeRuntimeExecuteFailure(cmd, failureBase, "admission_changed", "", "execution-channel trust or local binding changed after admission")
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetEscapeHTML(false)
		accepted := base
		accepted.Sequence = 1
		accepted.Type = "accepted"
		if encodeErr := encoder.Encode(accepted); encodeErr != nil {
			return fmt.Errorf("write runtime accepted event before execution: %w", encodeErr)
		}
		eventWritten = true
		result, executeErr := runtimeExecuteDeps.execute(cmd.Context(), workspaceRoot, revalidated)
		if executeErr != nil {
			code, field := "execution_failed", ""
			var typed *runtimeexecutor.Error
			if errors.As(executeErr, &typed) {
				code, field = string(typed.Code), typed.Field
			}
			failed := base
			failed.Sequence = 2
			failed.Type = "failed"
			failed.Error = &runtimeExecuteError{Code: code, Field: field, Message: executeErr.Error()}
			if encodeErr := encoder.Encode(failed); encodeErr != nil {
				return errors.Join(executeErr, fmt.Errorf("write runtime failed event: %w", encodeErr))
			}
			return executeErr
		}
		completed := base
		completed.Sequence = 2
		completed.Type = "completed"
		completed.Result = &result
		return encoder.Encode(completed)
	})
	if err != nil && !eventWritten {
		return writeRuntimeExecuteFailure(cmd, failureBase, "mutation_unavailable", "", err.Error())
	}
	return err
}

func admitRuntimeExecute(workspace string, bundleRaw []byte, now time.Time) (runtimeExecuteAdmission, error) {
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return runtimeExecuteAdmission{}, runtimeAdmissionError("trust_unavailable", "ownerRef", fmt.Errorf("load local Owner custody: %w", err))
	}
	trust, err := advancedtrust.Load(workspace)
	if err != nil {
		return runtimeExecuteAdmission{}, runtimeAdmissionError("trust_unavailable", "trustBundle", fmt.Errorf("load Owner-approved Advanced trust: %w", err))
	}
	if trust.OwnerRef != owner.OwnerRef || trust.OwnerKeyID != owner.KeyID {
		return runtimeExecuteAdmission{}, runtimeAdmissionError("trust_unavailable", "trustBundle", errors.New("Advanced trust is not bound to current local Owner custody"))
	}
	bundle := trust.TrustBundle()
	verified, err := executionchannelbundle.DecodeAndVerify(bundleRaw, &bundle, now)
	if err != nil {
		return runtimeExecuteAdmission{}, runtimeAdmissionError("bundle_rejected", "", err)
	}
	expected := executionchannelbundle.ChannelBinding{
		ChannelRef: owner.Binding.ChannelRef,
		SiteRef:    owner.Binding.SiteRef,
		NodeRef:    owner.Binding.NodeRef,
	}
	if verified.Channel != expected {
		return runtimeExecuteAdmission{}, runtimeAdmissionError(
			"channel_rejected", "channel",
			errors.New("execution-channel bundle does not match the persisted local Owner Site/node/channel binding"),
		)
	}
	return runtimeExecuteAdmission{
		Verified:    verified,
		TrustDigest: trust.BundleSHA256,
		OwnerRef:    owner.OwnerRef,
		OwnerKeyID:  owner.KeyID,
		Binding:     owner.Binding,
	}, nil
}

func executeRuntimeAdmission(
	ctx context.Context,
	workspaceRoot string,
	admission runtimeExecuteAdmission,
) (runtimeexecutor.ExecutionResult, error) {
	runtimeVersion := architectureV2ComponentVersion(version)
	identity, err := architecturev2.NewProductRuntimeRootIdentity(runtimeVersion)
	if err != nil {
		return runtimeexecutor.ExecutionResult{}, err
	}
	if admission.Verified.Request.Executor != identity {
		return runtimeexecutor.ExecutionResult{}, errors.New("bundle targets a different StackKits runtime identity")
	}
	registrations, err := architectureV2LocalRuntimeOwnerRegistrations(workspaceRoot, runtimeVersion)
	if err != nil {
		return runtimeexecutor.ExecutionResult{}, err
	}
	channels, err := architecturev2.NewProductLocalExecutionChannelFactory(
		architecturev2.ProductLocalExecutionChannelBinding{
			ChannelRef: admission.Binding.ChannelRef,
			SiteRef:    admission.Binding.SiteRef,
			NodeRef:    admission.Binding.NodeRef,
		},
	)
	if err != nil {
		return runtimeexecutor.ExecutionResult{}, err
	}
	journal, err := architecturev2.NewProductApplyFileJournal(workspaceRoot)
	if err != nil {
		return runtimeexecutor.ExecutionResult{}, err
	}
	defer func() { _ = journal.Close() }()
	registry, err := architecturev2.NewProductRuntimeOwnerRegistryWithRecovery(
		identity, registrations, channels, journal, journal,
	)
	if err != nil {
		return runtimeexecutor.ExecutionResult{}, err
	}
	return runtimeexecutor.Invoke(ctx, registry, admission.Verified.Request)
}

func runtimeAdmissionError(code, field string, err error) error {
	return &runtimeExecuteAdmissionError{Code: code, Field: field, Err: err}
}

func runtimeAdmissionReason(err error) (string, string) {
	var typed *runtimeExecuteAdmissionError
	if errors.As(err, &typed) {
		return typed.Code, typed.Field
	}
	return "bundle_rejected", ""
}

func runtimeEventForAdmission(admission runtimeExecuteAdmission) runtimeExecuteEvent {
	return runtimeExecuteEvent{
		SchemaVersion:       runtimeExecuteEventSchema,
		BundleDigest:        admission.Verified.BundleDigest,
		RequestDigest:       admission.Verified.Request.RequestDigest,
		ExecutionChannelRef: admission.Binding.ChannelRef,
		SiteRef:             admission.Binding.SiteRef,
		NodeRef:             admission.Binding.NodeRef,
	}
}

func equalRuntimeExecuteAdmission(left, right runtimeExecuteAdmission) bool {
	return left.TrustDigest == right.TrustDigest &&
		left.OwnerRef == right.OwnerRef &&
		left.OwnerKeyID == right.OwnerKeyID &&
		left.Binding == right.Binding &&
		left.Verified.BundleDigest == right.Verified.BundleDigest &&
		left.Verified.Request.RequestDigest == right.Verified.Request.RequestDigest &&
		left.Verified.Channel == right.Verified.Channel &&
		left.Verified.IssuerID == right.Verified.IssuerID &&
		left.Verified.KeyID == right.Verified.KeyID &&
		left.Verified.IssuedAt.Equal(right.Verified.IssuedAt) &&
		left.Verified.ExpiresAt.Equal(right.Verified.ExpiresAt)
}

func writeRuntimeExecuteFailure(cmd *cobra.Command, event runtimeExecuteEvent, code, field, message string) error {
	event.Type = "failed"
	event.Error = &runtimeExecuteError{Code: code, Field: field, Message: message}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(event); err != nil {
		return fmt.Errorf("write runtime failure event: %w", err)
	}
	return errors.New(message)
}
