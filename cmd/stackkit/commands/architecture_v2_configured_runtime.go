package commands

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"gopkg.in/yaml.v3"
)

type architectureV2ConfiguredStandardRuntime struct {
	bindings []architecturev2.ProductProcessExecutionChannelBinding
}

type architectureV2ConfiguredInventory struct {
	SchemaVersion     string `yaml:"schemaVersion"`
	ExecutionChannels map[string]struct {
		APIVersion        string `yaml:"apiVersion"`
		Kind              string `yaml:"kind"`
		ChannelRef        string `yaml:"channelRef"`
		SiteRef           string `yaml:"siteRef"`
		NodeRef           string `yaml:"nodeRef"`
		OperationClass    string `yaml:"operationClass"`
		OperationsProcess struct {
			Executable       string `yaml:"executable"`
			ExecutableSHA256 string `yaml:"executableSha256"`
		} `yaml:"operationsProcess"`
	} `yaml:"executionChannels"`
}

// architectureV2ConfiguredStandardRuntimeFromInventory constructs only the
// external execution-channel adapter custody. Workload topology and channel
// selection come exclusively from the verified ResolvedPlan consumed later by
// Product Apply; raw StackSpec bytes never enter this runtime boundary.
func architectureV2ConfiguredStandardRuntimeFromInventory(options architectureV2ExecutionCLIOptions) (*architectureV2ConfiguredStandardRuntime, bool, error) {
	if len(options.inventoryData) == 0 {
		return nil, false, nil
	}
	var inventory architectureV2ConfiguredInventory
	if err := yaml.Unmarshal(options.inventoryData, &inventory); err != nil {
		return nil, false, fmt.Errorf("decode Inventory executionChannels: %w", err)
	}
	if inventory.SchemaVersion != "stackkit.inventory/v1" {
		return nil, false, errors.New("configured execution channels require stackkit.inventory/v1")
	}
	if len(inventory.ExecutionChannels) == 0 {
		return nil, false, nil
	}

	keys := make([]string, 0, len(inventory.ExecutionChannels))
	for key := range inventory.ExecutionChannels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	configured := &architectureV2ConfiguredStandardRuntime{
		bindings: make([]architecturev2.ProductProcessExecutionChannelBinding, 0, len(keys)),
	}
	for _, key := range keys {
		channel := inventory.ExecutionChannels[key]
		if channel.APIVersion != "stackkit.standard-execution-channel/v1" ||
			channel.Kind != "StandardExecutionChannel" || channel.OperationClass != "standard" ||
			key != channel.ChannelRef {
			return nil, false, fmt.Errorf("execution channel %q has an unsupported or mismatched contract", key)
		}
		if strings.TrimSpace(channel.SiteRef) == "" || strings.TrimSpace(channel.NodeRef) == "" {
			return nil, false, fmt.Errorf("execution channel %q has an incomplete Site/node identity", key)
		}
		executable := strings.TrimSpace(channel.OperationsProcess.Executable)
		if !filepath.IsAbs(executable) {
			return nil, false, fmt.Errorf("execution channel %q operationsProcess.executable must be absolute", key)
		}
		configured.bindings = append(configured.bindings, architecturev2.ProductProcessExecutionChannelBinding{
			ChannelRef: key, SiteRef: channel.SiteRef, NodeRef: channel.NodeRef,
			Executable:       filepath.Clean(executable),
			ExecutableSHA256: channel.OperationsProcess.ExecutableSHA256,
		})
	}
	if options.localChannelRef != "" {
		matched := false
		for _, binding := range configured.bindings {
			if binding.ChannelRef == options.localChannelRef && binding.SiteRef == options.localSiteRef && binding.NodeRef == options.localNodeRef {
				matched = true
				break
			}
		}
		if !matched {
			return nil, false, errors.New("persisted owner Site/node/channel is not present exactly in Inventory executionChannels")
		}
	}
	return configured, true, nil
}
