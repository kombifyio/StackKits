package hostconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	inventorySchemaVersion = "stackkit.inventory/v1"
	bytesPerGiB            = 1024 * 1024 * 1024
)

// NodeInventoryFacts are the attested host capacity fields written onto one
// Inventory node. They are compiler input, not a HostConformanceReceipt.
type NodeInventoryFacts struct {
	Architecture   string
	CPUCores       int
	RamGB          int
	StorageGB      int
	Virtualization string
}

// ObserveNodeInventory reads CPU, RAM, disk, architecture, and virtualization
// from the local host. Missing or sub-1GiB measurements fail closed; they are
// never defaulted into an empty inventory node.
func ObserveNodeInventory(ctx context.Context, probe LocalProbe) (NodeInventoryFacts, error) {
	source := probe.Source
	if source == nil {
		source = osLocalSource{}
	}
	architecture := normalizeArchitecture(probe.Architecture)
	if architecture == "" {
		architecture = normalizeArchitecture(runtime.GOARCH)
	}
	if architecture != "amd64" && architecture != "arm64" {
		return NodeInventoryFacts{}, fmt.Errorf("unsupported host architecture %q", architecture)
	}
	cpuCores, ramGB, storageGB, err := observeCapacity(ctx, source)
	if err != nil {
		return NodeInventoryFacts{}, err
	}
	virtualization := detectVirtualization(ctx, source).Class
	if !allowedValue(virtualization, "bare-metal", "kvm", "openvz", "lxc", "vmware", "hyperv", "xen", "oracle", "microsoft", "none") {
		return NodeInventoryFacts{}, fmt.Errorf("host virtualization class %q is invalid", virtualization)
	}
	return NodeInventoryFacts{
		Architecture:   architecture,
		CPUCores:       cpuCores,
		RamGB:          ramGB,
		StorageGB:      storageGB,
		Virtualization: virtualization,
	}, nil
}

// MergeNodeInventoryFacts attests observed capacity onto exactly one existing
// or new inventory node. Bindings, receipts, and runtime daemons are kept.
func MergeNodeInventoryFacts(inventory resolvedplan.InventoryFacts, nodeRef string, facts NodeInventoryFacts, observedSiteKind string) (resolvedplan.InventoryFacts, error) {
	if !contractIDPattern.MatchString(nodeRef) {
		return nil, errors.New("inventory nodeRef must be a canonical contract ID")
	}
	if err := validateNodeInventoryFacts(facts); err != nil {
		return nil, err
	}
	clone, err := cloneInventoryDocument(inventory)
	if err != nil {
		return nil, err
	}
	if schema, _ := clone["schemaVersion"].(string); strings.TrimSpace(schema) == "" {
		clone["schemaVersion"] = inventorySchemaVersion
	} else if schema != inventorySchemaVersion {
		return nil, fmt.Errorf("inventory schemaVersion %q is not %s", schema, inventorySchemaVersion)
	}
	nodes, _ := clone["nodes"].(map[string]any)
	if nodes == nil {
		nodes = map[string]any{}
		clone["nodes"] = nodes
	}
	node, _ := nodes[nodeRef].(map[string]any)
	if node == nil {
		if _, exists := nodes[nodeRef]; exists {
			return nil, fmt.Errorf("inventory node %q is not an object", nodeRef)
		}
		node = map[string]any{}
		nodes[nodeRef] = node
	}
	node["arch"] = facts.Architecture
	node["cpuCores"] = facts.CPUCores
	node["ramGB"] = facts.RamGB
	node["storageGB"] = facts.StorageGB
	node["virtualization"] = facts.Virtualization
	if strings.TrimSpace(observedSiteKind) != "" {
		node["observedSiteKind"] = observedSiteKind
	}
	return resolvedplan.InventoryFacts(clone), nil
}

func validateNodeInventoryFacts(facts NodeInventoryFacts) error {
	if facts.Architecture != "amd64" && facts.Architecture != "arm64" {
		return fmt.Errorf("host architecture %q is unsupported", facts.Architecture)
	}
	if facts.CPUCores < 1 || facts.RamGB < 1 || facts.StorageGB < 1 {
		return errors.New("observed host capacity is below the 1 core / 1GiB floor")
	}
	if !allowedValue(facts.Virtualization, "bare-metal", "kvm", "openvz", "lxc", "vmware", "hyperv", "xen", "oracle", "microsoft", "none") {
		return fmt.Errorf("host virtualization class %q is invalid", facts.Virtualization)
	}
	return nil
}

func cloneInventoryDocument(inventory resolvedplan.InventoryFacts) (map[string]any, error) {
	if inventory == nil {
		return map[string]any{
			"schemaVersion": inventorySchemaVersion,
			"nodes":         map[string]any{},
		}, nil
	}
	raw, err := json.Marshal(inventory)
	if err != nil {
		return nil, fmt.Errorf("clone inventory: %w", err)
	}
	var clone map[string]any
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil, fmt.Errorf("decode cloned inventory: %w", err)
	}
	if clone == nil {
		clone = map[string]any{}
	}
	return clone, nil
}

func observeCapacity(ctx context.Context, source LocalSource) (cpuCores, ramGB, storageGB int, err error) {
	cpuCores, cpuErr := cpuCoresFromSource(ctx, source)
	ramGB, ramErr := ramGBFromSource(source)
	storageGB, storageErr := storageGBFromSource(ctx, source)
	if cpuErr == nil && ramErr == nil && storageErr == nil {
		return cpuCores, ramGB, storageGB, nil
	}
	if _, isHost := source.(osLocalSource); !isHost {
		return 0, 0, 0, errors.Join(cpuErr, ramErr, storageErr)
	}
	if cpuErr != nil {
		cpuCores, cpuErr = platformCPUCores()
	}
	if ramErr != nil {
		var ramBytes uint64
		ramBytes, ramErr = platformMemoryBytes()
		if ramErr == nil {
			ramGB, ramErr = bytesToGiB(ramBytes)
		}
	}
	if storageErr != nil {
		var storageBytes uint64
		storageBytes, storageErr = platformStorageBytes()
		if storageErr == nil {
			storageGB, storageErr = bytesToGiB(storageBytes)
		}
	}
	if cpuErr != nil || ramErr != nil || storageErr != nil {
		return 0, 0, 0, errors.Join(cpuErr, ramErr, storageErr)
	}
	return cpuCores, ramGB, storageGB, nil
}

func cpuCoresFromSource(ctx context.Context, source LocalSource) (int, error) {
	if data, err := source.ReadFile("/proc/cpuinfo"); err == nil {
		count := 0
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "processor") {
				count++
			}
		}
		if count >= 1 {
			return count, nil
		}
	}
	if _, err := source.LookPath("nproc"); err == nil {
		output, runErr := source.Run(ctx, "nproc")
		if runErr == nil {
			parsed, parseErr := strconv.Atoi(strings.TrimSpace(string(output)))
			if parseErr == nil && parsed >= 1 {
				return parsed, nil
			}
		}
	}
	return 0, errors.New("host CPU core count is unobserved")
}

func ramGBFromSource(source LocalSource) (int, error) {
	data, err := source.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("read /proc/meminfo: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kib, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil || kib == 0 {
			break
		}
		return bytesToGiB(kib * 1024)
	}
	return 0, errors.New("host RAM is unobserved")
}

func storageGBFromSource(ctx context.Context, source LocalSource) (int, error) {
	if _, err := source.LookPath("df"); err != nil {
		return 0, errors.New("host storage is unobserved")
	}
	output, err := source.Run(ctx, "df", "-B1", "-P", "/")
	if err != nil {
		return 0, fmt.Errorf("probe root filesystem size: %w", err)
	}
	bytes, err := parsePOSIXDFSize(output)
	if err != nil {
		return 0, err
	}
	return bytesToGiB(bytes)
}

func parsePOSIXDFSize(output []byte) (uint64, error) {
	lines := bytes.Split(bytes.ReplaceAll(output, []byte("\r\n"), []byte("\n")), []byte("\n"))
	for i, line := range lines {
		if i == 0 || len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		fields := strings.Fields(string(line))
		if len(fields) < 2 {
			continue
		}
		size, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || size == 0 {
			return 0, errors.New("host storage probe returned no canonical size")
		}
		return size, nil
	}
	return 0, errors.New("host storage is unobserved")
}

func bytesToGiB(bytes uint64) (int, error) {
	gb := bytes / bytesPerGiB
	if gb < 1 {
		return 0, errors.New("observed host capacity is below 1GiB")
	}
	if gb > uint64(^uint(0)>>1) {
		return 0, errors.New("observed host capacity overflows the inventory integer")
	}
	return int(gb), nil
}
