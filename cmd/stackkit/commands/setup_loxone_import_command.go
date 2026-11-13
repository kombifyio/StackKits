package commands

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/homegraph"
	"github.com/spf13/cobra"
)

const (
	loxoneImportCredentialSchema = "stackkit.loxone-credential/v1"
	loxoneImportResultSchema     = "stackkit.loxone-import-result/v1"

	loxoneUnavailable = "loxone_miniserver_unavailable"
	loxoneInvalid     = "loxone_import_request_invalid"
)

// loxoneCredentialDocument is the on-node Miniserver access. It is read from a
// private workspace file and never leaves this command: it is not part of the
// result and no error carries it. CAPEM optionally pins the Miniserver's
// self-signed certificate.
type loxoneCredentialDocument struct {
	SchemaVersion string `json:"schemaVersion"`
	BaseURL       string `json:"baseUrl"`
	User          string `json:"user"`
	Password      string `json:"password"`
	CAPEM         string `json:"caPem,omitempty"`
}

// loxoneImportResult is the read-only outcome: the merged home graph proposal
// and the counts. kombify never writes to the Miniserver; differences are
// Loxone Config tasks for the person (no supported write path is used).
type loxoneImportResult struct {
	SchemaVersion string          `json:"schemaVersion"`
	Graph         homegraph.Graph `json:"graph"`
	RoomsAdded    int             `json:"roomsAdded"`
	DevicesAdded  int             `json:"devicesAdded"`
	ReadOnly      bool            `json:"readOnly"`
}

type loxoneImportFailure struct {
	reason   string
	message  string
	guidance string
	retry    bool
}

func (e *loxoneImportFailure) Error() string { return e.reason + ": " + e.message }

func (e *loxoneImportFailure) ActionableError() actionableerror.Contract {
	return actionableerror.New("stackkit_command_failed", e.reason, e.message, []string{e.guidance}, e.retry)
}

func (e *loxoneImportFailure) ActionableStatus() string { return "failed" }

func newLoxoneImportCommand() *cobra.Command {
	var credentialFile, graphFile string
	var outputJSON bool
	command := &cobra.Command{Use: "loxone-import", Short: "Read the Loxone Miniserver structure file and merge its rooms and controls into the home graph (read-only)", Args: cobra.NoArgs, Annotations: map[string]string{noDeployObservabilityAnnotation: "true"}, RunE: func(cmd *cobra.Command, _ []string) error {
		if credentialFile == "" || graphFile == "" || !outputJSON {
			return errors.New("loxone-import requires credential-file, graph-file and json")
		}
		workspace := getWorkDir()
		var credential loxoneCredentialDocument
		if err := readNativeSetupCredentialJSON(workspace, credentialFile, &credential); err != nil || credential.SchemaVersion != loxoneImportCredentialSchema || credential.BaseURL == "" || credential.User == "" || credential.Password == "" {
			return machineAwareCommandError(cmd, &loxoneImportFailure{reason: loxoneInvalid, message: "Miniserver credential file is not readable", guidance: "Provide a workspace-relative private " + loxoneImportCredentialSchema + " file with baseUrl, user and password."})
		}
		var graph homegraph.Graph
		if err := readNativeSetupPrivateJSON(workspace, graphFile, &graph, homegraph.MaxSyncDocumentBytes); err != nil {
			return machineAwareCommandError(cmd, &loxoneImportFailure{reason: loxoneInvalid, message: "home graph is not readable as a closed JSON object within 512 KiB", guidance: "Send a " + homegraph.SchemaID + " document."})
		}
		if err := graph.Validate(); err != nil {
			return machineAwareCommandError(cmd, &loxoneImportFailure{reason: loxoneInvalid, message: err.Error(), guidance: "Correct the home graph and send it again."})
		}
		client, err := loxoneHTTPClient(credential.CAPEM)
		if err != nil {
			return machineAwareCommandError(cmd, &loxoneImportFailure{reason: loxoneInvalid, message: "caPem is not a valid certificate", guidance: "Provide the Miniserver certificate as PEM or omit caPem."})
		}
		ctx, cancel := context.WithTimeout(commandContext(cmd), 2*time.Minute)
		defer cancel()
		raw, err := homegraph.FetchLoxoneStructureWith(ctx, client, credential.BaseURL, credential.User, credential.Password)
		if err != nil {
			return machineAwareCommandError(cmd, &loxoneImportFailure{reason: loxoneUnavailable, message: "the Miniserver structure file could not be read", guidance: "Verify the Miniserver address, certificate and user, then retry.", retry: true})
		}
		imp, err := homegraph.ParseLoxoneStructure(raw)
		if err != nil {
			return machineAwareCommandError(cmd, &loxoneImportFailure{reason: loxoneInvalid, message: "the Miniserver answer is not a Loxone structure file", guidance: "Verify that baseUrl points at the Miniserver."})
		}
		merged := homegraph.MergeLoxone(graph, imp)
		if err := merged.Validate(); err != nil {
			return machineAwareCommandError(cmd, &loxoneImportFailure{reason: loxoneInvalid, message: "the merged home graph is not valid: " + err.Error(), guidance: "Reduce the home graph or the Miniserver structure to the schema limits."})
		}
		// The proposal must fit the size this command accepts as graph input, or a saved result could not be imported again.
		if encoded, err := json.Marshal(merged); err != nil || int64(len(encoded)) > int64(homegraph.MaxSyncDocumentBytes) {
			return machineAwareCommandError(cmd, &loxoneImportFailure{reason: loxoneInvalid, message: "the merged home graph exceeds 512 KiB", guidance: "Reduce the home graph or the Miniserver structure to the schema limits."})
		}
		return writeCommandResult(cmd, cmd.CommandPath(), loxoneImportResult{
			SchemaVersion: loxoneImportResultSchema,
			Graph:         merged,
			RoomsAdded:    len(merged.Rooms) - len(graph.Rooms),
			DevicesAdded:  len(merged.Devices) - len(graph.Devices),
			ReadOnly:      true,
		})
	}}
	command.Flags().StringVar(&credentialFile, "credential-file", "", "Workspace-relative private stackkit.loxone-credential/v1 JSON with baseUrl, user, password and optional caPem; stays on the node")
	command.Flags().StringVar(&graphFile, "graph-file", "", "Workspace-relative private kombify.home_graph.v1 JSON (at most 512 KiB)")
	command.Flags().BoolVar(&outputJSON, "json", false, "Emit the stackkit.loxone-import-result/v1 result")
	return command
}

// loxoneHTTPClient returns nil (default client) without a pinned certificate,
// otherwise a client that trusts exactly that certificate.
func loxoneHTTPClient(caPEM string) (*http.Client, error) {
	if caPEM == "" {
		return nil, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(caPEM)) {
		return nil, errors.New("invalid certificate")
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}, nil
}
