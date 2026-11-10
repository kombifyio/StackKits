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

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/appsetup"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/homegraph"
	"github.com/spf13/cobra"
)

const (
	homeGraphMappingDir  = ".stackkit/home-graph"
	homeGraphMappingFile = homeGraphMappingDir + "/home-graph-mapping.json"

	homeGraphUnavailable = "home_graph_connector_unavailable"
	homeGraphInvalid     = "home_graph_request_invalid"
	homeGraphApplyFailed = "home_graph_apply_failed"
)

// homeGraphFailure carries a stable reason code through the machine-aware
// command error path.
type homeGraphFailure struct {
	reason   string
	message  string
	guidance string
	retry    bool
}

func (e *homeGraphFailure) Error() string { return e.reason + ": " + e.message }

func (e *homeGraphFailure) ActionableError() actionableerror.Contract {
	return actionableerror.New("stackkit_command_failed", e.reason, e.message, []string{e.guidance}, e.retry)
}

func (e *homeGraphFailure) ActionableStatus() string { return "failed" }

func newHomeGraphCommand(dependencies *homeAssistantConnectorDependencies) *cobra.Command {
	var requestFile, operationID, workload, graphFile string
	var outputJSON bool
	command := &cobra.Command{Use: "home-graph", Short: "Project the household home graph onto the exact local Home Assistant", Args: cobra.NoArgs, Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		if workload != "smart-home" || requestFile == "" || operationID == "" || graphFile == "" || !outputJSON {
			return errors.New("home-graph requires workload smart-home, request-file, operation-id, graph-file and json")
		}
		workspace := getWorkDir()
		var document homeAssistantConnectorDocument
		if err := readNativeSetupCredentialJSON(workspace, requestFile, &document); err != nil {
			return machineAwareCommandError(cmd, &homeGraphFailure{reason: homeGraphInvalid, message: "connector request is not readable", guidance: "Provide the workspace-relative private connector request used for the connector setup."})
		}
		if document.SchemaVersion != homeAssistantConnectorRequestSchema || document.Connector.Action != "ensure" {
			return machineAwareCommandError(cmd, &homeGraphFailure{reason: homeGraphInvalid, message: "connector request must be an ensure request", guidance: "Provide the connector ensure request of the active Home Assistant connector."})
		}
		var request homegraph.SyncRequest
		if err := readNativeSetupPrivateJSON(workspace, graphFile, &request, homegraph.MaxSyncDocumentBytes); err != nil {
			return machineAwareCommandError(cmd, &homeGraphFailure{reason: homeGraphInvalid, message: "home graph request is not readable as a closed JSON object within 512 KiB", guidance: "Send a stackkit.home-graph-sync-request/v1 document."})
		}
		if err := request.Validate(); err != nil {
			return machineAwareCommandError(cmd, &homeGraphFailure{reason: homeGraphInvalid, message: err.Error(), guidance: "Correct the home graph request and send it again."})
		}
		raw, err := json.Marshal(document)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(raw)
		intent := "sha256:" + hex.EncodeToString(digest[:])
		ctx, cancel := context.WithTimeout(commandContext(cmd), 3*time.Minute)
		defer cancel()
		var result homegraph.SyncResult
		_, err = runVerifiedHomeAssistantConnector(ctx, workspace, workload, operationID, intent, document.Connector, dependencies, func(v verifiedHomeAssistantConnector) error {
			if !v.active {
				return &homeGraphFailure{reason: homeGraphUnavailable, message: "the Home Assistant connector is not active", guidance: "Set up the Home Assistant connector first.", retry: true}
			}
			var syncErr error
			err := v.withHTTP(ctx, workspace, v.deployment, func(client *http.Client, origin string) error {
				return appsetup.WithHomeAssistantConnectorCredential(ctx, client, v.request, origin, v.deployment.Release, func(token string) error {
					result, syncErr = syncHomeGraph(ctx, workspace, client, origin, token, request)
					return syncErr
				})
			})
			if syncErr != nil {
				return syncErr
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
	command.Flags().StringVar(&graphFile, "graph-file", "", "Workspace-relative private stackkit.home-graph-sync-request/v1 JSON (at most 512 KiB)")
	command.Flags().BoolVar(&outputJSON, "json", false, "Emit the stackkit.home-graph-sync-result/v1 result")
	return command
}

// syncHomeGraph connects to Home Assistant with the opened credential and
// runs one sync; the credential never leaves this call.
func syncHomeGraph(ctx context.Context, workspace string, client *http.Client, origin, token string, request homegraph.SyncRequest) (homegraph.SyncResult, error) {
	mapping, err := loadHomeGraphMapping(workspace)
	if err != nil {
		return homegraph.SyncResult{}, &homeGraphFailure{reason: homeGraphApplyFailed, message: "the stored mapping is unreadable", guidance: "Inspect the node's home graph mapping state."}
	}
	ha, err := homegraph.NewClient(ctx, client, origin, token)
	if err != nil {
		return homegraph.SyncResult{}, &homeGraphFailure{reason: homeGraphUnavailable, message: "Home Assistant did not accept the connector session", guidance: "Verify the Home Assistant connector and retry.", retry: true}
	}
	defer func() { _ = ha.Close() }()
	result, next, syncErr := homegraph.Sync(ctx, ha, request, mapping, time.Now())
	// A failed apply returns the links made so far; keep them so the next
	// run resumes. A read-only run returns the mapping unchanged.
	if request.Graph.LeadingSystem == homegraph.LeadingKombify {
		if saveErr := saveHomeGraphMapping(workspace, next); saveErr != nil && syncErr == nil {
			syncErr = saveErr
		}
	}
	if syncErr != nil {
		return homegraph.SyncResult{}, &homeGraphFailure{reason: homeGraphApplyFailed, message: "the home graph could not be applied to Home Assistant", guidance: "Retry the sync; links made so far are kept.", retry: true}
	}
	return result, nil
}

func loadHomeGraphMapping(workspace string) (homegraph.Mapping, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return homegraph.Mapping{}, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return homegraph.Mapping{}, err
	}
	defer tx.Close()
	exists, _, err := tx.Exists(homeGraphMappingFile)
	if err != nil {
		return homegraph.Mapping{}, err
	}
	if !exists {
		return homegraph.ParseMapping([]byte("{}"))
	}
	raw, _, err := tx.ReadStableBounded(homeGraphMappingFile, 4<<20)
	if err != nil {
		return homegraph.Mapping{}, err
	}
	return homegraph.ParseMapping(raw)
}

// saveHomeGraphMapping writes the mapping atomically with mode 0600 inside the
// confined workspace: staged beside the target, then renamed over it.
func saveHomeGraphMapping(workspace string, mapping homegraph.Mapping) error {
	data, err := mapping.Marshal()
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	if err := tx.MkdirAll(homeGraphMappingDir, 0o700); err != nil {
		return err
	}
	staged := fmt.Sprintf("%s/.mapping-%d.tmp", homeGraphMappingDir, os.Getpid())
	if exists, info, _ := tx.Exists(staged); exists {
		_ = tx.RemoveRegularFile(staged, info)
	}
	if err := tx.WriteFileExclusive(staged, data, 0o600); err != nil {
		return err
	}
	if _, err := tx.ReplaceRegularFile(staged, homeGraphMappingFile); err != nil {
		if exists, info, _ := tx.Exists(staged); exists {
			_ = tx.RemoveRegularFile(staged, info)
		}
		return err
	}
	return nil
}
