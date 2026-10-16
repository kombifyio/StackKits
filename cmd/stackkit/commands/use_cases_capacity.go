package commands

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/capacityfit"
	"github.com/kombifyio/stackkits/internal/hostconformance"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func pickNativeUseCases(cmd *cobra.Command) ([]string, error) {
	// Reject an unknown kit before probing the machine or reading an Inventory.
	nodeRef := useCasesNode
	if nodeRef == "" {
		nodeRef = "local-host"
	}
	catalog, err := architecturev2.EmbeddedUseCasePickerCatalog(useCasesKit, nodeRef)
	if err != nil {
		return nil, err
	}
	capacity, source, err := pickerNodeCapacity(cmd, nodeRef)
	if err != nil {
		return nil, err
	}
	choices := make([]choice, 0, len(catalog.Optional))
	for _, entry := range catalog.Optional {
		choices = append(choices, choice{Key: entry.ID, Display: entry.Title, IsDefault: entry.Default,
			Description: "std min " + pickerBudget(capacityfit.Max(entry.Module.Compute.HostFloor, entry.Module.Compute.Reservation))})
	}
	summary := func(selected []string) (string, error) {
		modules := make([]capacityfit.Module, 0, len(catalog.Required)+len(selected))
		var required []string
		for _, entry := range catalog.Required {
			modules = append(modules, entry.Module)
			required = append(required, entry.ID+"/"+entry.Alternative)
		}
		for _, entry := range catalog.Optional {
			for _, id := range selected {
				if id == entry.ID {
					modules = append(modules, entry.Module)
				}
			}
		}
		assessment, err := capacityfit.Assess(capacityfit.Input{Modules: modules, Capacities: []capacityfit.NodeCapacity{capacity}})
		if err != nil {
			return "", err
		}
		node := assessment.Nodes[0]
		var text strings.Builder
		fmt.Fprintf(&text, "\n  Required: %s (standard)\n", strings.Join(required, ", "))
		fmt.Fprintf(&text, "  Declared reserved: %s\n  Floor: %s\n", pickerBudget(node.Demand.Reservation), pickerBudget(node.Demand.HostFloor))
		fmt.Fprintf(&text, "  Recommended: %s\n  Host: %s\n  Source: %s\n", pickerBudget(node.Demand.Recommended), pickerBudget(capacity.Values), source)
		fmt.Fprintf(&text, "  Fit: %s (numeric preview; ? = unknown)\n", assessment.Overall)
		if len(node.ReasonCodes) > 0 {
			fmt.Fprintf(&text, "  Reasons: %s\n", strings.Join(node.ReasonCodes, ", "))
		}
		for _, advice := range node.Advice {
			fmt.Fprintf(&text, "  %s\n", advice)
		}
		return text.String(), nil
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "Standard default alternatives; CPU / RAM GiB / disk GiB. Numeric preview only.")
	fmt.Fprintln(cmd.ErrOrStderr(), "Data, add-ons, other profiles and resident policy need a plan. No Apply admission.")
	return selectManyWithSummary(os.Stderr, "Select use cases for "+useCasesKit, choices, summary)
}

func pickerBudget(value capacityfit.Vector) string {
	return fmt.Sprintf("%s CPU / %s GiB / %s GiB", capacityNumber(value.CPUCores), capacityNumber(value.RAMGB), capacityNumber(value.StorageGB))
}

func pickerNodeCapacity(cmd *cobra.Command, nodeRef string) (capacityfit.NodeCapacity, string, error) {
	capacity := capacityfit.NodeCapacity{NodeRef: nodeRef, Origin: capacityfit.Inventory}
	if useCasesInventory == "" {
		if useCasesNode != "" {
			return capacity, "", fmt.Errorf("--node requires --inventory")
		}
		facts, err := hostconformance.ObserveNodeInventory(cmd.Context(), hostconformance.LocalProbe{})
		if err != nil {
			return capacity, "", fmt.Errorf("observe local capacity: %w; use --inventory and --node for another target", err)
		}
		capacity.Values.Set(capacityfit.CPU, float64(facts.CPUCores))
		capacity.Values.Set(capacityfit.RAM, float64(facts.RamGB))
		capacity.Values.Set(capacityfit.Storage, float64(facts.StorageGB))
		return capacity, "local host observed; total disk", nil
	}
	if useCasesNode == "" {
		return capacity, "", fmt.Errorf("--inventory requires an explicit --node")
	}
	data, err := os.ReadFile(resolvePathFromWorkDir(getWorkDir(), useCasesInventory))
	if err != nil {
		return capacity, "", err
	}
	var document struct {
		SchemaVersion string `yaml:"schemaVersion"`
		Nodes         map[string]struct {
			CPU     *float64 `yaml:"cpuCores"`
			RAM     *float64 `yaml:"ramGB"`
			Storage *float64 `yaml:"storageGB"`
		} `yaml:"nodes"`
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&document); err != nil {
		return capacity, "", fmt.Errorf("decode picker Inventory: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return capacity, "", fmt.Errorf("decode trailing picker Inventory content: %w", err)
		}
		return capacity, "", fmt.Errorf("picker Inventory requires exactly one YAML document")
	}
	if document.SchemaVersion != "stackkit.inventory/v1" {
		return capacity, "", fmt.Errorf("picker Inventory requires schemaVersion stackkit.inventory/v1")
	}
	node, found := document.Nodes[nodeRef]
	if !found {
		return capacity, "", fmt.Errorf("picker Inventory has no node %q", nodeRef)
	}
	for _, value := range []struct {
		axis capacityfit.Axis
		fact *float64
	}{{capacityfit.CPU, node.CPU}, {capacityfit.RAM, node.RAM}, {capacityfit.Storage, node.Storage}} {
		if value.fact == nil {
			continue
		}
		if math.IsNaN(*value.fact) || math.IsInf(*value.fact, 0) || *value.fact < 1 || math.Trunc(*value.fact) != *value.fact {
			return capacity, "", fmt.Errorf("picker Inventory %s must be a positive whole-unit fact", value.axis)
		}
		capacity.Values.Set(value.axis, *value.fact)
	}
	return capacity, "Inventory " + nodeRef + "; supplied facts, no attestation", nil
}
