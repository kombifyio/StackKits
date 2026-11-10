package homegraph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Wire schema ids of the node sync (StackKits operation
// application.home-graph.sync).
const (
	SyncRequestSchema = "stackkit.home-graph-sync-request/v1"
	SyncResultSchema  = "stackkit.home-graph-sync-result/v1"

	// MaxSyncDocumentBytes bounds the request and the result document.
	MaxSyncDocumentBytes = 512 << 10
	maxRestore           = 256
	maxInventoryFloors   = 32
	maxInventoryAreas    = 256
	maxDrift             = 256
	maxText              = 255
)

// clip bounds a Home Assistant string to the result schema (255 runes).
func clip(s string) string {
	r := []rune(s)
	if len(r) <= maxText {
		return s
	}
	return string(r[:maxText])
}

// Sync statuses.
const (
	StatusApplied  = "applied"
	StatusObserved = "observed"
)

// SyncRequest is the document Cloud hands to a node through Techstack.
type SyncRequest struct {
	SchemaVersion string    `json:"schemaVersion"`
	Graph         Graph     `json:"graph"`
	Restore       []Restore `json:"restore"`
}

// Validate checks the schema id, the graph and the restore list.
func (r SyncRequest) Validate() error {
	if r.SchemaVersion != SyncRequestSchema {
		return fmt.Errorf("schemaVersion must be %q", SyncRequestSchema)
	}
	if err := r.Graph.Validate(); err != nil {
		return err
	}
	if len(r.Restore) > maxRestore {
		return fmt.Errorf("restore: at most %d items", maxRestore)
	}
	fields := map[string]map[string]bool{
		"floor":  setOf("name", "level"),
		"area":   setOf("name", "floor"),
		"device": setOf("area"),
	}
	seen := map[string]bool{}
	for _, item := range r.Restore {
		allowed, ok := fields[item.Kind]
		if !ok || !allowed[item.Field] {
			return fmt.Errorf("restore: %s field %q is not restorable", item.Kind, item.Field)
		}
		valid := map[string]bool{"floor": reFloor.MatchString(item.KombifyID), "area": reRoom.MatchString(item.KombifyID), "device": reDevice.MatchString(item.KombifyID)}[item.Kind]
		if !valid {
			return errors.New("restore: kombifyId is invalid")
		}
		if seen[item.key()] {
			return errors.New("restore: duplicate item")
		}
		seen[item.key()] = true
	}
	return nil
}

// SyncDrift is one reported difference.
type SyncDrift struct {
	Kind      string `json:"kind"`
	Field     string `json:"field"`
	KombifyID string `json:"kombifyId"`
	HAID      string `json:"haId"`
	Graph     string `json:"graph"`
	HA        string `json:"ha"`
}

// SyncLinks maps kombify ids to Home Assistant ids.
type SyncLinks struct {
	Floors  map[string]string `json:"floors"`
	Areas   map[string]string `json:"areas"`
	Devices map[string]string `json:"devices"`
}

// InventoryFloor and InventoryArea are the bounded Home Assistant inventory.
type InventoryFloor struct {
	HAID  string `json:"haId"`
	Name  string `json:"name"`
	Level *int   `json:"level"`
}

// InventoryArea is one Home Assistant area.
type InventoryArea struct {
	HAID      string `json:"haId"`
	Name      string `json:"name"`
	FloorHAID string `json:"floorHaId,omitempty"`
}

// SyncInventory lists floors and areas; devices are only counted.
type SyncInventory struct {
	Floors          []InventoryFloor `json:"floors"`
	Areas           []InventoryArea  `json:"areas"`
	UnmappedDevices int              `json:"unmappedDevices"`
}

// SyncResult is the document the node returns.
type SyncResult struct {
	SchemaVersion string        `json:"schemaVersion"`
	Revision      int64         `json:"revision"`
	LeadingSystem string        `json:"leadingSystem"`
	Status        string        `json:"status"`
	Operations    int           `json:"operations"`
	Links         SyncLinks     `json:"links"`
	Drift         []SyncDrift   `json:"drift"`
	Inventory     SyncInventory `json:"inventory"`
	SyncedAt      string        `json:"syncedAt"`
}

// Sync runs one synchronization. Leading kombify: plan (with the restore
// items), apply and return the updated mapping. Leading Home Assistant:
// read the registries only and return the mapping unchanged. Nothing is ever
// deleted in Home Assistant. On an apply error the returned mapping holds the
// progress made so the caller can persist it; the result is then zero.
func Sync(ctx context.Context, reg Registry, req SyncRequest, mapping Mapping, now time.Time) (SyncResult, Mapping, error) {
	snap, err := reg.Snapshot(ctx)
	if err != nil {
		return SyncResult{}, mapping, err
	}
	result := SyncResult{SchemaVersion: SyncResultSchema, Revision: req.Graph.Revision, LeadingSystem: req.Graph.LeadingSystem, Status: StatusObserved, Drift: []SyncDrift{}}
	next := mapping
	if req.Graph.LeadingSystem == LeadingKombify {
		plan := PlanProjectionRestore(req.Graph, snap, mapping, req.Restore)
		applied, skipped, applyErr := ApplyPlan(ctx, reg, plan)
		if applyErr != nil {
			return SyncResult{}, applied, applyErr
		}
		next = applied
		result.Status = StatusApplied
		result.Operations = len(plan.Ops)
		for _, d := range append(append([]Drift{}, plan.Drift...), skipped...) {
			if len(result.Drift) == maxDrift {
				break
			}
			result.Drift = append(result.Drift, SyncDrift{Kind: d.Kind, Field: d.Field, KombifyID: d.KombifyID, HAID: clip(d.HAID), Graph: clip(d.Graph), HA: clip(d.HA)})
		}
		// The sync observed Home Assistant before writing; read it again so
		// the inventory shows the state after the writes. The writes already
		// succeeded, so a failed re-read keeps the earlier inventory.
		if result.Operations > 0 {
			if after, err := reg.Snapshot(ctx); err == nil {
				snap = after
			}
		}
	}
	next.normalize()
	result.Links = SyncLinks{Floors: cloneMap(next.Floors), Areas: cloneMap(next.Areas), Devices: cloneMap(next.Devices)}
	result.Inventory = inventoryOf(snap, next)
	result.SyncedAt = now.UTC().Format(time.RFC3339)
	return result, next, nil
}

func inventoryOf(snap HASnapshot, m Mapping) SyncInventory {
	inv := SyncInventory{Floors: []InventoryFloor{}, Areas: []InventoryArea{}}
	floors := append([]HAFloor{}, snap.Floors...)
	sort.Slice(floors, func(i, j int) bool { return floors[i].FloorID < floors[j].FloorID })
	for i, f := range floors {
		if i == maxInventoryFloors {
			break
		}
		inv.Floors = append(inv.Floors, InventoryFloor{HAID: clip(f.FloorID), Name: clip(f.Name), Level: f.Level})
	}
	areas := append([]HAArea{}, snap.Areas...)
	sort.Slice(areas, func(i, j int) bool { return areas[i].AreaID < areas[j].AreaID })
	for i, a := range areas {
		if i == maxInventoryAreas {
			break
		}
		area := InventoryArea{HAID: clip(a.AreaID), Name: clip(a.Name)}
		if a.FloorID != nil {
			area.FloorHAID = clip(*a.FloorID)
		}
		inv.Areas = append(inv.Areas, area)
	}
	mapped := map[string]bool{}
	for _, id := range m.Devices {
		mapped[id] = true
	}
	for _, d := range snap.Devices {
		if !mapped[d.ID] {
			inv.UnmappedDevices++
		}
	}
	return inv
}
