package commands

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/kombifyio/stackkits/internal/federationcontrol"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/spf13/cobra"
)

func readControlInput(path string, out any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 256<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("one bounded control document required")
	}
	return nil
}

func init() {
	control := &cobra.Command{Use: "control", Short: "Execute Home-authorized plan and verify at the selected Cloud node"}
	var bindFile, actionFile, sendFile, endpointFile string
	bind := &cobra.Command{Use: "bind", Args: cobra.NoArgs, Short: "Admit exact public Home authority and local receiver custody", RunE: func(cmd *cobra.Command, _ []string) error {
		var c federationcontrol.ReceiverCustody
		if err := readControlInput(bindFile, &c); err != nil {
			return err
		}
		if err := withLifecycleMutation(getWorkDir(), "federation control bind", func() error { return federationcontrol.BindReceiver(getWorkDir(), c) }); err != nil {
			return err
		}
		return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"bound": true, "mutateCapabilities": "unavailable"})
	}}
	bind.Flags().StringVar(&bindFile, "file", "", "Owner-reviewed receiver custody JSON; no private keys")
	withdraw := &cobra.Command{Use: "withdraw", Args: cobra.NoArgs, Short: "Persistently withdraw receiver authority, including existing TLS sessions", RunE: func(cmd *cobra.Command, _ []string) error {
		// Withdrawal must remain available while a remote action holds the
		// lifecycle lock. The signed custody replacement is atomic.
		if err := federationcontrol.WithdrawReceiver(getWorkDir()); err != nil {
			return err
		}
		return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"active": false})
	}}
	sign := &cobra.Command{Use: "sign", Args: cobra.NoArgs, Short: "Sign one exact short-lived action with current Home custody", RunE: func(cmd *cobra.Command, _ []string) error {
		var a federationcontrol.Action
		if err := readControlInput(actionFile, &a); err != nil {
			return err
		}
		signed, err := federationcontrol.SignAction(getWorkDir(), a)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(signed)
	}}
	sign.Flags().StringVar(&actionFile, "file", "", "Exact action JSON with plan hash, target and bounded timestamps")
	send := &cobra.Command{Use: "send", Args: cobra.NoArgs, Short: "Send the signed action over an exact Home-initiated mTLS connection", RunE: func(cmd *cobra.Command, _ []string) error {
		var a federationcontrol.Action
		var endpoint federationcontrol.Endpoint
		if err := readControlInput(sendFile, &a); err != nil {
			return err
		}
		if err := readControlInput(endpointFile, &endpoint); err != nil {
			return err
		}
		result, err := federationcontrol.SendAction(cmd.Context(), getWorkDir(), endpoint, a)
		if err != nil {
			var denial *federationcontrol.Denial
			if errors.As(err, &denial) {
				_ = json.NewEncoder(cmd.OutOrStdout()).Encode(denial)
			}
			return err
		}
		if err = json.NewEncoder(cmd.OutOrStdout()).Encode(result); err != nil {
			return err
		}
		if result.Status != "succeeded" {
			return errors.New("remote local-owner operation failed; see structured result")
		}
		return nil
	}}
	send.Flags().StringVar(&sendFile, "file", "", "Home-signed action JSON")
	send.Flags().StringVar(&endpointFile, "endpoint-file", "", "Local pinned mTLS endpoint and confined credential paths")
	// The process adapter has a distinct forced-native entry: an old v0.6 plan
	// fallback must never turn a remote read request into OpenTofu initialization.
	var localAction, localPlan, expectedHash string
	local := &cobra.Command{Use: "run-readonly", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) (returnErr error) {
		defer func() {
			if returnErr != nil {
				returnErr = writeMachineCommandFailure(cmd, returnErr, "Correct the local native plan or verification authority before retrying the signed action.")
			}
		}()
		if (localAction != "plan" && localAction != "verify") || localPlan == "" || expectedHash == "" {
			return errors.New("exact native read-only action and plan required")
		}
		// Reuse the installed inventory without observing or persisting new
		// local facts. Read actions cannot change inputs to later local Apply.
		_, inventoryPath, err := locateArchitectureV2Inventory(getWorkDir(), "")
		if err != nil {
			return err
		}
		if inventoryPath == "" {
			return errors.New("native read-only action requires existing local inventory")
		}
		options := architectureV2ExecutionCLIOptions{planPath: localPlan, expectedPlanHash: expectedHash, inventoryPath: inventoryPath, context: cmd.Context()}
		options.verifiedPlanSink = func(p generationartifact.VerifiedPlan) error { return p.RequireExpectedPlanHash(expectedHash) }
		options.inspectionSink = func(p generationartifact.PlanInspection) error { return json.NewEncoder(cmd.OutOrStdout()).Encode(p) }
		options.verifySink = func(p architectureV2VerifyReport) error { return json.NewEncoder(cmd.OutOrStdout()).Encode(p) }
		gate := newArchitectureV2ExecutionGate()
		gate.rejectV1 = true
		if handled, err := gate.preflight(getWorkDir(), specFile, architectureV2ExecutionMode(localAction), options); handled {
			return err
		}
		return errors.New("native Architecture v2 plan required; no legacy execution fallback")
	}}
	local.Flags().StringVar(&localAction, "action", "", "Closed native read operation")
	local.Flags().StringVar(&localPlan, "resolved-plan", "", "Exact local canonical plan")
	local.Flags().StringVar(&expectedHash, "expected-plan-hash", "", "Exact admitted plan hash")
	control.AddCommand(bind, withdraw, sign, send, local)
	federationCmd.AddCommand(control)
}
