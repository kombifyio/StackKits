package hostconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"

	"github.com/kombifyio/stackkits/internal/resolvedplan"
)

const (
	inventorySchemaVersion = "stackkit.inventory/v1"
	bytesPerGiB            = 1024 * 1024 * 1024
)

// NodeInventoryFacts are the attested host facts written onto one Inventory
// node. They are compiler input, not a HostConformanceReceipt.
type NodeInventoryFacts struct {
	Architecture                string
	AMD64MicroarchitectureLevel int
	CPUCores                    int
	RamGB                       int
	StorageGB                   int
	Virtualization              string
	StorageCapacity             *StorageCapacityFacts
}

// StorageCapacityFacts is an observation of free space on the exact storage
// target used by the resolved plan. It is intentionally separate from the
// legacy total storage fact, which may describe the host root filesystem.
type StorageCapacityFacts struct {
	SourceRef         string
	Path              string
	FreeGiB           float64
	FilesystemType    string
	FilesystemClass   StorageFilesystemClass
	SupportsOwnership bool
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
	storageSourceRef, storagePath, err := validateStorageProbe(probe)
	if err != nil {
		return NodeInventoryFacts{}, err
	}
	// Keep the legacy total storage fact rooted at the host filesystem. The
	// exact target is probed separately below and is the only fact usable for
	// DataBinding capacity admission.
	cpuCores, ramGB, storageGB, err := observeCapacity(ctx, source, "")
	if err != nil {
		return NodeInventoryFacts{}, err
	}
	var storageCapacity *StorageCapacityFacts
	if storagePath != "" {
		freeGiB, freeErr := storageFreeGiBFromSource(ctx, source, storagePath)
		if freeErr != nil {
			// A fresh host may not have the resolved data root yet (for example
			// before Docker preparation). Preserve legacy facts and leave the
			// exact capacity fact absent so Apply remains unverified.
			if _, isHost := source.(osLocalSource); !isHost {
				return NodeInventoryFacts{}, freeErr
			}
		} else {
			storageCapacity = &StorageCapacityFacts{SourceRef: storageSourceRef, Path: storagePath, FreeGiB: freeGiB}
			filesystemFacts, filesystemErr := ObserveStorageFilesystem(ctx, source, storagePath)
			if filesystemErr == nil {
				storageCapacity.FilesystemType = filesystemFacts.FilesystemType
				storageCapacity.FilesystemClass = filesystemFacts.FilesystemClass
				storageCapacity.SupportsOwnership = filesystemFacts.SupportsOwnership
			}
		}
	}
	virtualization := detectVirtualization(ctx, source).Class
	if !allowedValue(virtualization, "bare-metal", "kvm", "openvz", "lxc", "vmware", "hyperv", "xen", "oracle", "microsoft", "none") {
		return NodeInventoryFacts{}, fmt.Errorf("host virtualization class %q is invalid", virtualization)
	}
	return NodeInventoryFacts{
		Architecture:                architecture,
		AMD64MicroarchitectureLevel: observeAMD64MicroarchitectureLevel(source, architecture),
		CPUCores:                    cpuCores,
		RamGB:                       ramGB,
		StorageGB:                   storageGB,
		Virtualization:              virtualization,
		StorageCapacity:             storageCapacity,
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
	delete(node, "amd64MicroarchitectureLevel")
	if facts.AMD64MicroarchitectureLevel > 0 {
		node["amd64MicroarchitectureLevel"] = facts.AMD64MicroarchitectureLevel
	}
	node["cpuCores"] = facts.CPUCores
	node["ramGB"] = facts.RamGB
	node["storageGB"] = facts.StorageGB
	node["virtualization"] = facts.Virtualization
	if facts.StorageCapacity != nil {
		capacity := map[string]any{
			"sourceRef": facts.StorageCapacity.SourceRef,
			"path":      facts.StorageCapacity.Path,
			"freeGiB":   facts.StorageCapacity.FreeGiB,
		}
		if facts.StorageCapacity.FilesystemClass != "" {
			capacity["filesystemClass"] = string(facts.StorageCapacity.FilesystemClass)
			if facts.StorageCapacity.FilesystemType != "" {
				capacity["filesystemType"] = facts.StorageCapacity.FilesystemType
			}
			capacity["supportsOwnership"] = facts.StorageCapacity.SupportsOwnership
		}
		node["storageCapacity"] = capacity
	} else {
		// A current failed/missing observation cannot refresh a stale free-space
		// value from an earlier inventory file into fresh admission evidence.
		delete(node, "storageCapacity")
	}
	if strings.TrimSpace(observedSiteKind) != "" {
		node["observedSiteKind"] = observedSiteKind
	}
	return resolvedplan.InventoryFacts(clone), nil
}

func validateNodeInventoryFacts(facts NodeInventoryFacts) error {
	if facts.Architecture != "amd64" && facts.Architecture != "arm64" {
		return fmt.Errorf("host architecture %q is unsupported", facts.Architecture)
	}
	if facts.AMD64MicroarchitectureLevel < 0 || facts.AMD64MicroarchitectureLevel > 4 || (facts.Architecture == "arm64" && facts.AMD64MicroarchitectureLevel != 0) {
		return errors.New("host amd64 microarchitecture level is invalid for the observed architecture")
	}
	if facts.CPUCores < 1 || facts.RamGB < 1 || facts.StorageGB < 1 {
		return errors.New("observed host capacity is below the 1 core / 1GiB floor")
	}
	if !allowedValue(facts.Virtualization, "bare-metal", "kvm", "openvz", "lxc", "vmware", "hyperv", "xen", "oracle", "microsoft", "none") {
		return fmt.Errorf("host virtualization class %q is invalid", facts.Virtualization)
	}
	if facts.StorageCapacity != nil {
		if _, _, err := validateStorageProbe(LocalProbe{
			StorageSourceRef: facts.StorageCapacity.SourceRef,
			StoragePath:      facts.StorageCapacity.Path,
		}); err != nil {
			return err
		}
		if math.IsNaN(facts.StorageCapacity.FreeGiB) || math.IsInf(facts.StorageCapacity.FreeGiB, 0) || facts.StorageCapacity.FreeGiB < 0 {
			return errors.New("observed storage free capacity must be finite and non-negative")
		}
		if err := validateStorageFilesystemFacts(*facts.StorageCapacity); err != nil {
			return err
		}
	}
	return nil
}

func validateStorageFilesystemFacts(facts StorageCapacityFacts) error {
	if facts.FilesystemClass == "" {
		if facts.FilesystemType != "" || facts.SupportsOwnership {
			return errors.New("observed storage filesystem fields are incomplete")
		}
		return nil
	}
	if !knownStorageFilesystemClass(string(facts.FilesystemClass)) {
		return fmt.Errorf("observed storage filesystem class %q is unsupported", facts.FilesystemClass)
	}
	if facts.FilesystemClass != StorageFilesystemUnknown && facts.FilesystemType == "" {
		return errors.New("observed storage filesystem type is missing")
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

func observeCapacity(ctx context.Context, source LocalSource, storagePath string) (cpuCores, ramGB, storageGB int, err error) {
	cpuCores, cpuErr := cpuCoresFromSource(ctx, source)
	ramGB, ramErr := ramGBFromSource(source)
	storageGB, storageErr := storageGBFromSource(ctx, source, storagePath)
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
		storageBytes, storageErr = platformStorageBytesAt(storagePath)
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

func storageGBFromSource(ctx context.Context, source LocalSource, storagePath string) (int, error) {
	if storagePath == "" {
		storagePath = "/"
	}
	if _, err := source.LookPath("df"); err != nil {
		return 0, errors.New("host storage is unobserved")
	}
	output, err := source.Run(ctx, "df", "-B1", "-P", storagePath)
	if err != nil {
		return 0, fmt.Errorf("probe storage target size: %w", err)
	}
	bytes, err := parsePOSIXDFSize(output)
	if err != nil {
		return 0, err
	}
	return bytesToGiB(bytes)
}

func validateStorageProbe(probe LocalProbe) (sourceRef, storagePath string, err error) {
	sourceRef = strings.TrimSpace(probe.StorageSourceRef)
	storagePath = strings.TrimSpace(probe.StoragePath)
	if sourceRef == "" && storagePath == "" {
		return "", "", nil
	}
	if sourceRef == "" || storagePath == "" {
		return "", "", errors.New("storage source reference and path must be supplied together")
	}
	if sourceRef != "storage.dataRoot" && sourceRef != "system.container.dataRoot" {
		return "", "", fmt.Errorf("unsupported storage source reference %q", sourceRef)
	}
	if !strings.HasPrefix(storagePath, "/") || strings.ContainsAny(storagePath, "\x00\r\n") {
		return "", "", fmt.Errorf("storage target path %q is not an absolute contract path", storagePath)
	}
	return sourceRef, storagePath, nil
}

func storageFreeGiBFromSource(ctx context.Context, source LocalSource, storagePath string) (float64, error) {
	if _, err := source.LookPath("df"); err == nil {
		output, runErr := source.Run(ctx, "df", "-B1", "-P", storagePath)
		if runErr == nil {
			bytes, parseErr := parsePOSIXDFFree(output)
			if parseErr == nil {
				return float64(bytes) / bytesPerGiB, nil
			}
			if _, isHost := source.(osLocalSource); !isHost {
				return 0, parseErr
			}
		} else if _, isHost := source.(osLocalSource); !isHost {
			return 0, fmt.Errorf("probe storage target free space: %w", runErr)
		}
	}
	if _, isHost := source.(osLocalSource); !isHost {
		return 0, errors.New("storage target free space is unobserved")
	}
	freeBytes, err := platformStorageFreeBytes(storagePath)
	if err != nil {
		return 0, err
	}
	return float64(freeBytes) / bytesPerGiB, nil
}

func parsePOSIXDFFree(output []byte) (uint64, error) {
	lines := bytes.Split(bytes.ReplaceAll(output, []byte("\r\n"), []byte("\n")), []byte("\n"))
	for i, line := range lines {
		if i == 0 || len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		fields := strings.Fields(string(line))
		if len(fields) < 4 {
			continue
		}
		available, err := strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			return 0, errors.New("host storage probe returned no canonical free capacity")
		}
		return available, nil
	}
	return 0, errors.New("host storage free capacity is unobserved")
}

func platformStorageBytesAt(path string) (uint64, error) {
	if path == "" {
		return platformStorageBytes()
	}
	return platformStorageBytesForPath(path)
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
