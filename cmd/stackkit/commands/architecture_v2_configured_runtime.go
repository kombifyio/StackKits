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

type architectureV2ConfiguredStackSpec struct {
	Sites []struct {
		ID string `yaml:"id"`
	} `yaml:"sites"`
	Nodes []struct {
		ID      string `yaml:"id"`
		SiteRef string `yaml:"siteRef"`
		Enabled *bool  `yaml:"enabled"`
	} `yaml:"nodes"`
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

func architectureV2ConfiguredStandardRuntimeFrom(options architectureV2ExecutionCLIOptions) (*architectureV2ConfiguredStandardRuntime, bool, error) {
	if len(options.stackSpecData) == 0 {
		return nil, false, nil
	}
	var spec architectureV2ConfiguredStackSpec
	if err := yaml.Unmarshal(options.stackSpecData, &spec); err != nil {
		return nil, false, fmt.Errorf("decode StackSpec execution-channel topology: %w", err)
	}
	if len(spec.Sites) < 2 {
		return nil, false, nil
	}
	if len(options.inventoryData) == 0 {
		return nil, false, errors.New("multi-Site Apply requires explicit Inventory executionChannels; no channel authority was configured")
	}
	var inventory architectureV2ConfiguredInventory
	if err := yaml.Unmarshal(options.inventoryData, &inventory); err != nil {
		return nil, false, fmt.Errorf("decode Inventory executionChannels: %w", err)
	}
	if inventory.SchemaVersion != "stackkit.inventory/v1" || len(inventory.ExecutionChannels) == 0 {
		return nil, false, errors.New("multi-Site Apply requires stackkit.inventory/v1 executionChannels")
	}

	enabledNodes := make(map[string]string)
	for _, node := range spec.Nodes {
		if node.Enabled != nil && !*node.Enabled {
			continue
		}
		if strings.TrimSpace(node.ID) == "" || strings.TrimSpace(node.SiteRef) == "" {
			return nil, false, errors.New("multi-Site StackSpec contains an incomplete enabled node")
		}
		enabledNodes[node.ID] = node.SiteRef
	}
	if len(enabledNodes) != len(inventory.ExecutionChannels) {
		return nil, false, fmt.Errorf("multi-Site executionChannels must cover every enabled node exactly once: got %d channels for %d nodes", len(inventory.ExecutionChannels), len(enabledNodes))
	}

	keys := make([]string, 0, len(inventory.ExecutionChannels))
	for key := range inventory.ExecutionChannels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	configured := &architectureV2ConfiguredStandardRuntime{
		bindings: make([]architecturev2.ProductProcessExecutionChannelBinding, 0, len(keys)),
	}
	sites := make(map[string]struct{}, 2)
	for _, key := range keys {
		channel := inventory.ExecutionChannels[key]
		if channel.APIVersion != "stackkit.standard-execution-channel/v1" ||
			channel.Kind != "StandardExecutionChannel" || channel.OperationClass != "standard" ||
			key != channel.ChannelRef {
			return nil, false, fmt.Errorf("execution channel %q has an unsupported or mismatched contract", key)
		}
		wantSite, found := enabledNodes[channel.NodeRef]
		if !found || wantSite != channel.SiteRef {
			return nil, false, fmt.Errorf("execution channel %q does not match an enabled StackSpec Site/node", key)
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
		sites[channel.SiteRef] = struct{}{}
	}
	if len(sites) != 2 {
		return nil, false, fmt.Errorf("v0.9 standard execution-channel contract requires exactly two configured Sites, got %d", len(sites))
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
