package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/hostconformance"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"github.com/spf13/cobra"
)

type hostConformanceAttachDeps struct {
	workspace func() string
	mutate    func(string, string, func() error) error
	read      func(string, string) ([]byte, error)
	write     func(string, string, []byte) error
	exists    func(string, string) (bool, error)
}

type hostConformanceAttachResult struct {
	SchemaVersion string `json:"schemaVersion"`
	InventoryPath string `json:"inventoryPath"`
	NodeRef       string `json:"nodeRef"`
	BindingRef    string `json:"bindingRef"`
	ReceiptRef    string `json:"receiptRef"`
}

func defaultHostConformanceAttachDeps() hostConformanceAttachDeps {
	return hostConformanceAttachDeps{
		workspace: getWorkDir,
		mutate:    withLifecycleMutation,
		read:      readFederationStableFile,
		write:     writeFederationPrivateAtomic,
		exists:    hostConformanceWorkspaceFileExists,
	}
}

func newHostConformanceAttachCommand(deps hostConformanceAttachDeps) *cobra.Command {
	var inventoryPath, bindingPath, receiptPath string
	command := &cobra.Command{
		Use:   "attach-conformance",
		Short: "Attach exact host evidence to Inventory before plan generation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHostConformanceAttach(cmd, deps, inventoryPath, bindingPath, receiptPath)
		},
	}
	command.Flags().StringVar(&inventoryPath, "inventory", "", "Workspace-confined inventory.json to update atomically")
	command.Flags().StringVar(&bindingPath, "binding", "", "Workspace-confined ExternalHostBinding JSON")
	command.Flags().StringVar(&receiptPath, "receipt", "", "Workspace-confined HostConformanceReceipt JSON")
	_ = command.MarkFlagRequired("inventory")
	_ = command.MarkFlagRequired("binding")
	_ = command.MarkFlagRequired("receipt")
	return command
}

func runHostConformanceAttach(
	command *cobra.Command,
	deps hostConformanceAttachDeps,
	inventoryInput, bindingInput, receiptInput string,
) error {
	if deps.workspace == nil || deps.mutate == nil || deps.read == nil || deps.write == nil || deps.exists == nil {
		return errors.New("host conformance attach command dependencies are incomplete")
	}
	workspace, err := federationWorkspace(deps.workspace())
	if err != nil {
		return err
	}
	inventoryPath, err := federationWorkspacePath(workspace, inventoryInput, "Inventory")
	if err != nil {
		return err
	}
	if path.Base(inventoryPath) != "inventory.json" {
		return errors.New("host conformance attachment requires an inventory.json target")
	}
	bindingPath, err := federationWorkspacePath(workspace, bindingInput, "ExternalHostBinding")
	if err != nil {
		return err
	}
	receiptPath, err := federationWorkspacePath(workspace, receiptInput, "HostConformanceReceipt")
	if err != nil {
		return err
	}
	planPath := federationPlanPathForInventory(inventoryPath)

	var result hostConformanceAttachResult
	err = deps.mutate(workspace, "host conformance attach", func() error {
		generated, err := deps.exists(workspace, planPath)
		if err != nil {
			return fmt.Errorf("inspect canonical ResolvedPlan before host conformance attachment: %w", err)
		}
		if generated {
			return fmt.Errorf("host conformance must be attached before generation; canonical ResolvedPlan already exists at %s", planPath)
		}
		inventoryRaw, err := deps.read(workspace, inventoryPath)
		if err != nil {
			return fmt.Errorf("read Inventory: %w", err)
		}
		bindingRaw, err := deps.read(workspace, bindingPath)
		if err != nil {
			return fmt.Errorf("read ExternalHostBinding: %w", err)
		}
		receiptRaw, err := deps.read(workspace, receiptPath)
		if err != nil {
			return fmt.Errorf("read HostConformanceReceipt: %w", err)
		}
		inventory, err := decodeOneJSON[resolvedplan.InventoryFacts](inventoryRaw, "Inventory")
		if err != nil {
			return err
		}
		binding, err := decodeOneJSON[resolvedplan.ExternalHostBinding](bindingRaw, "ExternalHostBinding")
		if err != nil {
			return err
		}
		receipt, err := decodeOneJSON[resolvedplan.HostConformanceReceipt](receiptRaw, "HostConformanceReceipt")
		if err != nil {
			return err
		}
		nodeRef, _ := receipt["nodeRef"].(string)
		updated, err := hostconformance.Attach(inventory, nodeRef, binding, receipt)
		if err != nil {
			return fmt.Errorf("attach exact host conformance evidence: %w", err)
		}
		canonical, err := resolvedplan.CanonicalJSON(updated)
		if err != nil {
			return fmt.Errorf("canonicalize host-conformance Inventory: %w", err)
		}
		if err := deps.write(workspace, inventoryPath, canonical); err != nil {
			return fmt.Errorf("persist host-conformance Inventory: %w", err)
		}
		result = hostConformanceAttachResult{
			SchemaVersion: "stackkit.host-conformance-attach-result/v1",
			InventoryPath: inventoryPath,
			NodeRef:       nodeRef,
			BindingRef:    stringValue(binding, "bindingRef"),
			ReceiptRef:    stringValue(receipt, "receiptRef"),
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeCommandResult(command, command.CommandPath(), result)
}

func decodeOneJSON[T any](raw []byte, label string) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode %s: %w", label, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != nil && !errors.Is(err, io.EOF) {
		return value, fmt.Errorf("decode trailing %s data: %w", label, err)
	} else if err == nil {
		return value, fmt.Errorf("%s must contain exactly one JSON document", label)
	}
	return value, nil
}

func hostConformanceWorkspaceFileExists(workspace, relative string) (bool, error) {
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	transaction, err := root.BeginTransaction()
	if err != nil {
		return false, err
	}
	defer func() { _ = transaction.Close() }()
	exists, _, err := transaction.Exists(strings.TrimSpace(relative))
	return exists, err
}
