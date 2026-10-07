package commands

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/kombifyio/stackkits/internal/applicationlifecycle"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
	"github.com/spf13/cobra"
)

const homeAssistantConnectorRequestSchema = "stackkit.home-assistant-connector-request/v1"
const homeAssistantConnectorResultSchema = "stackkit.home-assistant-connector-result/v1"

type homeAssistantConnectorDocument struct {
	SchemaVersion string                                 `json:"schemaVersion"`
	Connector     appsetup.HomeAssistantConnectorRequest `json:"connector"`
}
type homeAssistantConnectorExport struct {
	SchemaVersion          string                                                `json:"schemaVersion"`
	SignatureVerified      bool                                                  `json:"signatureVerified"`
	ReceiptBase64          string                                                `json:"receiptBase64"`
	ReceiptDigest          string                                                `json:"receiptDigest"`
	CurrentLocalDeployment applicationlifecycle.HomeAssistantConnectorDeployment `json:"currentLocalDeployment"`
}

// These private dependencies isolate daemon observation in the public CLI
// fixture. Production always uses the registered local adapter and custody
// observer; no command input can select a transport or observer.
type homeAssistantConnectorDependencies struct {
	observe  func(context.Context, string, nativehost.SelectedPaaSWorkloadDeployment) (map[string]string, error)
	withHTTP func(context.Context, string, nativehost.SelectedPaaSWorkloadDeployment, func(*http.Client, string) error) error
}

func newHomeAssistantConnectorCommand(dependencies *homeAssistantConnectorDependencies) *cobra.Command {
	var requestFile, operationID, workload string
	var approved, verifyOnly, outputJSON bool
	command := &cobra.Command{Use: "connector", Short: "Issue, revoke or verify the exact local Home Assistant connector", Args: cobra.NoArgs, Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		if workload != "smart-home" || requestFile == "" || operationID == "" || !outputJSON || (verifyOnly && approved) || (!verifyOnly && !approved) {
			return errors.New("connector requires workload smart-home, request-file, operation-id and json; select owner-approve or read-only verify-only")
		}
		var document homeAssistantConnectorDocument
		workspace := getWorkDir()
		if err := readNativeSetupCredentialJSON(workspace, requestFile, &document); err != nil {
			return err
		}
		if document.SchemaVersion != homeAssistantConnectorRequestSchema {
			return errors.New("unsupported Home Assistant connector request")
		}
		raw, err := json.Marshal(document)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		intent := "sha256:" + hex.EncodeToString(digest[:])
		ctx, cancel := context.WithTimeout(commandContext(cmd), 3*time.Minute)
		defer cancel()
		if !verifyOnly {
			_, err = executeNativeSetup(ctx, workspace, workload, nativeSetupOptions{ownerApproved: true, operationID: operationID, connectorRequest: &document.Connector, connectorIntent: intent, connectorDependencies: dependencies})
			if err != nil {
				return machineAwareCommandError(cmd, err)
			}
		}
		exported, err := verifyHomeAssistantConnectorSetup(ctx, workspace, workload, operationID, intent, document.Connector, dependencies)
		if err != nil {
			return machineAwareCommandError(cmd, err)
		}
		return writeCommandResult(cmd, cmd.CommandPath(), exported)
	}}
	command.Flags().StringVar(&workload, "workload", "", "Exact local workload; only smart-home is admitted")
	command.Flags().StringVar(&requestFile, "request-file", "", "Workspace-relative private metadata-only connector request JSON; never contains credentials")
	command.Flags().StringVar(&operationID, "operation-id", "", "Exact stable connector setup operation identity")
	command.Flags().BoolVar(&approved, "owner-approve", false, "Approve this exact connector ensure or revoke request")
	command.Flags().BoolVar(&verifyOnly, "verify-only", false, "Read and verify a retained receipt against current local binding without issuing or revoking")
	command.Flags().BoolVar(&outputJSON, "json", false, "Emit the authenticated exact signed receipt bytes and current local facts")
	return command
}

func verifyHomeAssistantConnectorSetup(ctx context.Context, workspace, workload, operationID, intent string, request appsetup.HomeAssistantConnectorRequest, dependencies *homeAssistantConnectorDependencies) (homeAssistantConnectorExport, error) {
	initial, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
	if err != nil {
		return homeAssistantConnectorExport{}, err
	}
	var exported homeAssistantConnectorExport
	err = withArchitectureV2OutputLock(workspace, initial.OutputRoot, func(_ *confinedfs.Transaction, _ *confinedfs.OutputLock) error {
		current, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
		if err != nil {
			return err
		}
		if initial.Plan.Binding() != current.Plan.Binding() || initial.Lineage != current.Lineage {
			return errors.New("connector verification authority changed")
		}
		plan, err := resolvedplan.DecodeCanonicalPlan(current.Plan.Canonical())
		if err != nil {
			return err
		}
		contract, err := applicationlifecycle.ContractFromResolvedPlan(plan, workload)
		if err != nil {
			return err
		}
		store := applicationlifecycle.Store{Workspace: workspace}
		state, err := store.Load(contract)
		if err != nil {
			return err
		}
		matched := false
		for _, operation := range state.Operations {
			if operation.ID == operationID {
				matched = operation.IntentDigest == intent
			}
		}
		if !matched {
			return errors.New("connector receipt intent differs from request")
		}
		result, raw, err := store.ExportSetupResult(contract, operationID, current.Lineage.ApplyResultHash)
		if err != nil {
			return err
		}
		deployment, err := nativeAppliedWorkloadDeployment(current, workload)
		if err != nil {
			return err
		}
		if result.HomeAssistantConnector == nil || result.ActionRef != "home-assistant-owner-bootstrap" || result.ArtifactDigest != deployment.ArtifactDigest || result.InstanceRef != deployment.InstanceRef || result.ApplicationVersion != deployment.Release || result.HomeAssistantConnector.Issuer.RequestedBinding != request.Binding || (request.Action == "ensure" && result.HomeAssistantConnector.Issuer.Status != "active") || (request.Action == "revoke" && result.HomeAssistantConnector.Issuer.Status != "revoked") {
			return errors.New("connector receipt differs from current deployment or requested action")
		}
		observe := nativehost.ObserveStandaloneComposeContainerCustody
		_, err = nativeApplicationSetupAdapter(deployment)
		if err != nil {
			return err
		}
		// The selected adapter is admitted above; verification uses retained
		// identity rather than the setup transport's identity ensure step.
		withHTTP := nativehost.WithStandaloneComposeReadOnlyHTTP
		if dependencies != nil {
			observe, withHTTP = dependencies.observe, dependencies.withHTTP
		}
		request.Workspace = workspace
		// This reuses exactly the same independently observed node/source/container
		// checks as issuance, but the callback performs no setup or mutation.
		_, verified, err := executeVerifiedHomeAssistantConnector(ctx, workspace, deployment, &request, observe, func(*appsetup.HomeAssistantConnectorRequest) (appsetup.HomeAssistantOwnerResult, error) {
			err := withHTTP(ctx, workspace, deployment, func(client *http.Client, origin string) error {
				issuer := result.HomeAssistantConnector.Issuer
				if err := appsetup.VerifyHomeAssistantConnectorCustody(request, origin, deployment.Release, issuer); err != nil {
					return err
				}
				if issuer.Status == "active" {
					return appsetup.WithHomeAssistantConnectorCredential(ctx, client, request, origin, deployment.Release, func(string) error { return nil })
				}
				return nil
			})
			return appsetup.HomeAssistantOwnerResult{UserID: result.AccountRef, Version: result.ApplicationVersion, Connector: &result.HomeAssistantConnector.Issuer}, err
		})
		if err != nil {
			return err
		}
		if verified.VerifiedLocalDeployment != result.HomeAssistantConnector.VerifiedLocalDeployment {
			return errors.New("connector receipt local deployment changed")
		}
		latest, err := inspectNativeV2AppliedAuthority(ctx, workspace, specFile)
		if err != nil {
			return err
		}
		if latest.Plan.Binding() != current.Plan.Binding() || latest.Lineage != current.Lineage {
			return errors.New("connector authority changed during verification")
		}
		if len(raw) > 64<<10 {
			return errors.New("connector signed receipt exceeds 64 KiB")
		}
		digest := sha256.Sum256(raw)
		exported = homeAssistantConnectorExport{SchemaVersion: homeAssistantConnectorResultSchema, SignatureVerified: true, ReceiptBase64: base64.StdEncoding.EncodeToString(raw), ReceiptDigest: "sha256:" + hex.EncodeToString(digest[:]), CurrentLocalDeployment: verified.VerifiedLocalDeployment}
		return nil
	})
	return exported, err
}
