package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localorigin"
	"github.com/spf13/cobra"
)

func init() {
	group := &cobra.Command{Use: "workload-peer", Short: "Exchange owner-approved Cloud workload certificates with Home"}
	descriptions := map[string]string{
		"request": "Create or reuse a Cloud-held key and export its public CSR",
		"enroll":  "Issue and admit a peer for one installed Home publication",
		"install": "Install a Home-issued certificate using an independently verified root",
		"revoke":  "Withdraw a peer's current Home admission",
		"probe":   "Request the origin through an existing federation loopback socket",
	}
	for _, action := range []string{"request", "enroll", "install", "revoke", "probe"} {
		command := &cobra.Command{Use: action, Short: descriptions[action], Args: cobra.NoArgs, RunE: runIdentityWorkloadPeer}
		command.Flags().String("peer-ref", "", "Explicit workload peer identity")
		command.Flags().String("file", "", "Public request or credential JSON file")
		command.Flags().String("server-name", "", "Installed Home origin server name")
		command.Flags().String("home-root-sha256", "", "Home root sha256 fingerprint verified through a separate owner-approved channel")
		command.Flags().String("address", "", "Existing federation loopback socket for the Cloud probe")
		command.Flags().Bool("owner-approve", false, "Explicitly approve this local workload identity operation")
		if action == "request" {
			command.Flags().Bool("rotate-key", false, "Generate a new Cloud key while preserving the active credential until install")
		}
		if action == "enroll" {
			command.Flags().Bool("replace-key", false, "Explicitly replace the selected peer key; revoked keys remain denied")
		}
		group.AddCommand(command)
	}
	identityCmd.AddCommand(group)
}

func runIdentityWorkloadPeer(cmd *cobra.Command, _ []string) error {
	root := getWorkDir()
	peer, _ := cmd.Flags().GetString("peer-ref")
	if cmd.Name() == "probe" {
		address, _ := cmd.Flags().GetString("address")
		status, err := localorigin.ProbePeer(cmd.Context(), root, peer, address)
		if err != nil {
			return err
		}
		if status < 200 || status >= 400 {
			return fmt.Errorf("workload origin returned HTTP %d", status)
		}
		return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"httpStatus": status})
	}
	approved, _ := cmd.Flags().GetBool("owner-approve")
	if !approved {
		return errors.New("workload identity mutation requires explicit --owner-approve")
	}
	var result any
	err := withLifecycleMutation(root, "identity workload-peer "+cmd.Name(), func() error {
		switch cmd.Name() {
		case "request":
			rotate, _ := cmd.Flags().GetBool("rotate-key")
			value, err := localorigin.RequestPeerWithRotation(root, peer, rotate)
			result = value
			return err
		case "enroll":
			var request localorigin.PeerRequest
			if err := readWorkloadPeerInput(cmd, &request); err != nil {
				return err
			}
			serverName, _ := cmd.Flags().GetString("server-name")
			replace, _ := cmd.Flags().GetBool("replace-key")
			value, err := localorigin.EnrollPeer(cmd.Context(), root, serverName, peer, request, replace)
			result = value
			return err
		case "install":
			var credential localorigin.PeerCredential
			if err := readWorkloadPeerInput(cmd, &credential); err != nil {
				return err
			}
			fingerprint, _ := cmd.Flags().GetString("home-root-sha256")
			result = map[string]any{"peerRef": credential.PeerRef, "installed": true}
			return localorigin.InstallPeer(root, credential, fingerprint)
		case "revoke":
			now := time.Now().UTC()
			operation, err := localevidence.SignWorkloadPeerOperation(root, localevidence.WorkloadPeerOperation{Operation: "revoke", PeerRef: peer, IssuedAt: now, ExpiresAt: now.Add(time.Minute)})
			if err != nil {
				return err
			}
			result = map[string]any{"peerRef": peer, "revoked": true}
			return localevidence.ApplyWorkloadPeerOperation(root, operation)
		}
		return errors.New("unsupported workload peer operation")
	})
	if err != nil {
		return err
	}
	// Exchange commands emit only public CSR/certificate documents, consumable
	// verbatim on the other node. Private custody is never serialized here.
	if cmd.Name() == "request" || cmd.Name() == "enroll" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

func readWorkloadPeerInput(cmd *cobra.Command, value any) error {
	path, _ := cmd.Flags().GetString("file")
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, (128<<10)+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("one public workload identity document required")
	}
	return nil
}
