package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/homegraph"
	"github.com/spf13/cobra"
)

const (
	homeGraphBackupDir = homeGraphMappingDir + "/backups"

	homeGraphAdoptionStale = "home_graph_adoption_stale"
	homeGraphBackupFailed  = "home_graph_backup_failed"
)

// newHomeGraphAdoptCommand adopts an existing Home Assistant into the home
// graph, and changes the leading system later, with the same plan
// (HOME-GRAPH-TARGET.md sections 6 and 8). The inventory writes nothing to
// Home Assistant; the apply takes a registry backup before any write.
func newHomeGraphAdoptCommand(dependencies *homeAssistantConnectorDependencies) *cobra.Command {
	var requestFile, operationID, workload, adoptionFile string
	var outputJSON bool
	command := &cobra.Command{Use: "home-graph-adopt", Short: "Inventory or apply the adoption of the exact local Home Assistant into the household home graph", Args: cobra.NoArgs, Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		if workload != "smart-home" || requestFile == "" || operationID == "" || adoptionFile == "" || !outputJSON {
			return errors.New("home-graph-adopt requires workload smart-home, request-file, operation-id, adoption-file and json")
		}
		workspace := getWorkDir()
		var document homeAssistantConnectorDocument
		if err := readNativeSetupCredentialJSON(workspace, requestFile, &document); err != nil {
			return machineAwareCommandError(cmd, &homeGraphFailure{reason: homeGraphInvalid, message: "connector request is not readable", guidance: "Provide the workspace-relative private connector request used for the connector setup."})
		}
		if document.SchemaVersion != homeAssistantConnectorRequestSchema || document.Connector.Action != "ensure" {
			return machineAwareCommandError(cmd, &homeGraphFailure{reason: homeGraphInvalid, message: "connector request must be an ensure request", guidance: "Provide the connector ensure request of the active Home Assistant connector."})
		}
		var request homegraph.AdoptionRequest
		if err := readNativeSetupPrivateJSON(workspace, adoptionFile, &request, homegraph.MaxSyncDocumentBytes); err != nil {
			return machineAwareCommandError(cmd, &homeGraphFailure{reason: homeGraphInvalid, message: "adoption request is not readable as a closed JSON object within 512 KiB", guidance: "Send a " + homegraph.AdoptionRequestSchema + " document."})
		}
		if err := request.Validate(); err != nil {
			return machineAwareCommandError(cmd, &homeGraphFailure{reason: homeGraphInvalid, message: err.Error(), guidance: "Correct the adoption request and send it again."})
		}
		raw, err := json.Marshal(document)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		intent := "sha256:" + hex.EncodeToString(digest[:])
		ctx, cancel := context.WithTimeout(commandContext(cmd), 3*time.Minute)
		defer cancel()
		var result homegraph.AdoptionResult
		_, err = runVerifiedHomeAssistantConnector(ctx, workspace, workload, operationID, intent, document.Connector, dependencies, func(v verifiedHomeAssistantConnector) error {
			if !v.active {
				return &homeGraphFailure{reason: homeGraphUnavailable, message: "the Home Assistant connector is not active", guidance: "Set up the Home Assistant connector first.", retry: true}
			}
			var adoptErr error
			err := v.withHTTP(ctx, workspace, v.deployment, func(client *http.Client, origin string) error {
				return appsetup.WithHomeAssistantConnectorCredential(ctx, client, v.request, origin, v.deployment.Release, func(token string) error {
					result, adoptErr = adoptHomeGraph(ctx, workspace, client, origin, token, request)
					return adoptErr
				})
			})
			if adoptErr != nil {
				return adoptErr
			}
			return err
		})
		if err != nil {
			var failure *homeGraphFailure
			if !errors.As(err, &failure) {
				err = &homeGraphFailure{reason: homeGraphUnavailable, message: "the verified Home Assistant connector could not be used", guidance: "Verify the Home Assistant connector (application.connector.verify) and retry.", retry: true}
			}
			return machineAwareCommandError(cmd, err)
		}
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}}
	command.Flags().StringVar(&workload, "workload", "", "Exact local workload; only smart-home is admitted")
	command.Flags().StringVar(&requestFile, "request-file", "", "Workspace-relative private metadata-only connector ensure request JSON; never contains credentials")
	command.Flags().StringVar(&operationID, "operation-id", "", "Exact stable connector setup operation identity")
	command.Flags().StringVar(&adoptionFile, "adoption-file", "", "Workspace-relative private stackkit.home-graph-adoption-request/v1 JSON (at most 512 KiB)")
	command.Flags().BoolVar(&outputJSON, "json", false, "Emit the stackkit.home-graph-adoption-result/v1 result")
	return command
}

// adoptHomeGraph connects to Home Assistant with the opened credential and
// runs one adoption step; the credential never leaves this call.
func adoptHomeGraph(ctx context.Context, workspace string, client *http.Client, origin, token string, request homegraph.AdoptionRequest) (homegraph.AdoptionResult, error) {
	mapping, err := loadHomeGraphMapping(workspace)
	if err != nil {
		return homegraph.AdoptionResult{}, &homeGraphFailure{reason: homeGraphApplyFailed, message: "the stored mapping is unreadable", guidance: "Inspect the node's home graph mapping state."}
	}
	ha, err := homegraph.NewClient(ctx, client, origin, token)
	if err != nil {
		return homegraph.AdoptionResult{}, &homeGraphFailure{reason: homeGraphUnavailable, message: "Home Assistant did not accept the connector session", guidance: "Verify the Home Assistant connector and retry.", retry: true}
	}
	defer func() { _ = ha.Close() }()
	now := time.Now()
	var backupErr error
	result, next, runErr := homegraph.RunAdoption(ctx, ha, request, mapping, now, func(snap homegraph.HASnapshot) (homegraph.AdoptionBackup, error) {
		ref, err := saveHomeGraphRegistryBackup(workspace, snap, now)
		backupErr = err
		return ref, err
	})
	switch {
	case backupErr != nil:
		return homegraph.AdoptionResult{}, &homeGraphFailure{reason: homeGraphBackupFailed, message: "the Home Assistant registry backup could not be written; nothing was changed", guidance: "Free space in the node workspace and apply the plan again.", retry: true}
	case errors.Is(runErr, homegraph.ErrAdoptionStale):
		return homegraph.AdoptionResult{}, &homeGraphFailure{reason: homeGraphAdoptionStale, message: "Home Assistant or the home graph changed since the inventory; nothing was changed", guidance: "Run the inventory again and confirm the new plan."}
	case errors.Is(runErr, homegraph.ErrAdoptionUndecided):
		return homegraph.AdoptionResult{}, &homeGraphFailure{reason: homeGraphInvalid, message: "every conflict needs a decision, or one side for all", guidance: "Decide each conflict or choose one side for all and apply again."}
	}
	// An inventory records the pending adoption; an apply records the links
	// made so far even when it failed part-way, so the next run resumes.
	if runErr == nil || request.Mode == homegraph.AdoptionApply {
		if saveErr := saveHomeGraphMapping(workspace, next); saveErr != nil && runErr == nil {
			runErr = saveErr
		}
	}
	if runErr != nil {
		if request.Mode == homegraph.AdoptionInventory {
			return homegraph.AdoptionResult{}, &homeGraphFailure{reason: homeGraphUnavailable, message: "the Home Assistant registries could not be read", guidance: "Verify the Home Assistant connector and retry.", retry: true}
		}
		return homegraph.AdoptionResult{}, &homeGraphFailure{reason: homeGraphApplyFailed, message: "the adoption could not be applied to Home Assistant", guidance: "Run the inventory again; links made so far are kept and the registry backup is on the node.", retry: true}
	}
	return result, nil
}

// saveHomeGraphRegistryBackup writes the Home Assistant registry snapshot
// (mode 0600) inside the confined workspace before an adoption writes.
func saveHomeGraphRegistryBackup(workspace string, snap homegraph.HASnapshot, now time.Time) (homegraph.AdoptionBackup, error) {
	data, err := json.MarshalIndent(homegraph.NewRegistryBackup(snap, now), "", "  ")
	if err != nil {
		return homegraph.AdoptionBackup{}, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return homegraph.AdoptionBackup{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return homegraph.AdoptionBackup{}, err
	}
	defer tx.Close()
	if err := tx.MkdirAll(homeGraphBackupDir, 0o700); err != nil {
		return homegraph.AdoptionBackup{}, err
	}
	file := fmt.Sprintf("%s/ha-registry-%s-%d.json", homeGraphBackupDir, now.UTC().Format("20060102T150405Z"), os.Getpid())
	if err := tx.WriteFileExclusive(file, data, 0o600); err != nil {
		return homegraph.AdoptionBackup{}, err
	}
	return homegraph.AdoptionBackup{File: file, TakenAt: now.UTC().Format(time.RFC3339), Floors: len(snap.Floors), Areas: len(snap.Areas), Labels: len(snap.Labels), Devices: len(snap.Devices)}, nil
}
