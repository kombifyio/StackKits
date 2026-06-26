// Package backupplan builds the non-secret recovery plan emitted by
// `stackkit generate`.
package backupplan

import (
	"strings"

	"github.com/kombifyio/stackkits/pkg/models"
)

const (
	SchemaVersion       = "stackkit.backup-recovery.v1"
	MaterializerPending = "pending"
)

type RecoveryPlan struct {
	SchemaVersion      string                         `json:"schemaVersion"`
	Source             string                         `json:"source"`
	StackKit           string                         `json:"stackkit,omitempty"`
	InstallMode        string                         `json:"installMode,omitempty"`
	PlacementMode      string                         `json:"placementMode,omitempty"`
	NodeCount          int                            `json:"nodeCount"`
	Enabled            bool                           `json:"enabled"`
	PrimaryEngine      string                         `json:"primaryEngine"`
	DataClasses        []string                       `json:"dataClasses,omitempty"`
	SingleServer       SingleServerPlan               `json:"singleServer"`
	MultiServer        MultiServerPlan                `json:"multiServer"`
	EmergencyExport    EmergencyExportPlan            `json:"emergencyExport"`
	ManagedServerless  ManagedServerlessPlan          `json:"managedServerless"`
	MaterializerStatus string                         `json:"materializerStatus"`
	FollowUps          []string                       `json:"followUps,omitempty"`
	Destinations       []models.BackupDestinationSpec `json:"destinations,omitempty"`
}

type SingleServerPlan struct {
	Enabled                  bool   `json:"enabled"`
	MinimumRecoveryCopies    int    `json:"minimumRecoveryCopies"`
	RequireLocalRepo         bool   `json:"requireLocalRepo"`
	RequireEmergencyExport   bool   `json:"requireEmergencyExport"`
	RequireRestoreDrill      bool   `json:"requireRestoreDrill"`
	RequireOffHostCopy       bool   `json:"requireOffHostCopy"`
	RecommendOffsite         bool   `json:"recommendOffsite"`
	RecommendImmutableCopy   bool   `json:"recommendImmutableCopy"`
	KopiaIndependentFallback string `json:"kopiaIndependentFallback"`
}

type MultiServerPlan struct {
	Enabled                      bool                       `json:"enabled"`
	Topology                     string                     `json:"topology"`
	MinServers                   int                        `json:"minServers"`
	MinManagers                  int                        `json:"minManagers"`
	QuorumSize                   int                        `json:"quorumSize"`
	ToleratedManagerFailures     int                        `json:"toleratedManagerFailures"`
	CapacityHeadroomNodes        int                        `json:"capacityHeadroomNodes"`
	ReleaseReadyHA               bool                       `json:"releaseReadyHa"`
	CoordinationMode             string                     `json:"coordinationMode"`
	RequireOffsiteRepo           bool                       `json:"requireOffsiteRepo"`
	RequireEmergencyExport       bool                       `json:"requireEmergencyExport"`
	RequireRestoreDrill          bool                       `json:"requireRestoreDrill"`
	RequireSharedVolumeInventory bool                       `json:"requireSharedVolumeInventory"`
	RequirePlacementSpread       bool                       `json:"requirePlacementSpread"`
	RequireManagedServerlessPlan bool                       `json:"requireManagedServerlessPlan"`
	Media                        MultiServerMediaPlan       `json:"media"`
	Performance                  MultiServerPerformancePlan `json:"performance"`
}

type MultiServerMediaPlan struct {
	DocumentsMode           string `json:"documentsMode"`
	PhotosMode              string `json:"photosMode"`
	LargeMediaMode          string `json:"largeMediaMode"`
	ExcludeGeneratedCaches  bool   `json:"excludeGeneratedCaches"`
	RequireExternalMediaMap bool   `json:"requireExternalMediaMap"`
}

type MultiServerPerformancePlan struct {
	Profile                    string `json:"profile"`
	AvoidPrimaryOnlySnapshots  bool   `json:"avoidPrimaryOnlySnapshots"`
	StaggerNodeSnapshots       bool   `json:"staggerNodeSnapshots"`
	MaxConcurrentNodeSnapshots int    `json:"maxConcurrentNodeSnapshots"`
	PreferRepoServerFanIn      bool   `json:"preferRepoServerFanIn"`
}

type EmergencyExportPlan struct {
	Enabled        bool                          `json:"enabled"`
	Mode           string                        `json:"mode"`
	Format         string                        `json:"format"`
	Schedule       string                        `json:"schedule,omitempty"`
	IncludeClasses []string                      `json:"includeClasses,omitempty"`
	LargeMediaMode string                        `json:"largeMediaMode"`
	Target         *models.BackupDestinationSpec `json:"target,omitempty"`
	Manifest       EmergencyExportManifestPlan   `json:"manifest"`
}

type EmergencyExportManifestPlan struct {
	Enabled               bool `json:"enabled"`
	IncludeRestoreRunbook bool `json:"includeRestoreRunbook"`
	IncludeChecksums      bool `json:"includeChecksums"`
}

type ManagedServerlessPlan struct {
	Enabled                    bool     `json:"enabled"`
	NoServerDependency         bool     `json:"noServerDependency"`
	Authority                  string   `json:"authority"`
	ProtectedClasses           []string `json:"protectedClasses,omitempty"`
	ControlPlaneSnapshot       bool     `json:"controlPlaneSnapshot"`
	ProviderNativeBackups      bool     `json:"providerNativeBackups"`
	RequireProviderDataHandles bool     `json:"requireProviderDataHandles"`
	RequireRebuildIntent       bool     `json:"requireRebuildIntent"`
	PortableManifest           bool     `json:"portableManifest"`
	PreChangeSnapshot          bool     `json:"preChangeSnapshot"`
	Schedule                   string   `json:"schedule,omitempty"`
}

func Build(spec *models.StackSpec) RecoveryPlan {
	nodeCount := effectiveNodeCount(spec)
	backup := backupSpec(spec)
	enabled := boolDefault(backup.Enabled, true)
	dataClasses := stringSliceDefault(backup.DataClasses, defaultDataClasses())

	plan := RecoveryPlan{
		SchemaVersion:      SchemaVersion,
		Source:             "stack-spec",
		NodeCount:          nodeCount,
		Enabled:            enabled,
		PrimaryEngine:      normalizeEngine(backup.Engine, backup.Backend),
		DataClasses:        dataClasses,
		SingleServer:       buildSingleServerPlan(backup.Resilience, nodeCount),
		MultiServer:        buildMultiServerPlan(backup.Resilience, nodeCount, countMainManagers(spec)),
		EmergencyExport:    buildEmergencyExportPlan(backup.Resilience, dataClasses),
		ManagedServerless:  buildManagedServerlessPlan(spec, backup.Resilience, dataClasses),
		MaterializerStatus: MaterializerPending,
		Destinations:       append([]models.BackupDestinationSpec(nil), backup.Destinations...),
	}
	if spec != nil {
		plan.StackKit = spec.StackKit
		plan.InstallMode = spec.EffectiveInstallMode()
		plan.PlacementMode = spec.EffectivePlacementMode()
	}
	plan.FollowUps = planFollowUps(plan)
	return plan
}

func backupSpec(spec *models.StackSpec) models.BackupSpec {
	if spec == nil || spec.Backup == nil {
		return models.BackupSpec{}
	}
	return *spec.Backup
}

func buildSingleServerPlan(resilience *models.BackupResilienceSpec, nodeCount int) SingleServerPlan {
	cfg := (*models.SingleServerBackupSafetySpec)(nil)
	if resilience != nil {
		cfg = resilience.SingleServer
	}
	plan := SingleServerPlan{
		Enabled:                  nodeCount <= 1,
		MinimumRecoveryCopies:    3,
		RequireLocalRepo:         true,
		RequireEmergencyExport:   true,
		RequireRestoreDrill:      true,
		RequireOffHostCopy:       true,
		RecommendOffsite:         true,
		RecommendImmutableCopy:   true,
		KopiaIndependentFallback: "portable-archive",
	}
	if cfg == nil {
		return plan
	}
	plan.Enabled = boolDefault(cfg.Enabled, plan.Enabled)
	if cfg.MinimumRecoveryCopies > 0 {
		plan.MinimumRecoveryCopies = cfg.MinimumRecoveryCopies
	}
	plan.RequireLocalRepo = boolDefault(cfg.RequireLocalRepo, plan.RequireLocalRepo)
	plan.RequireEmergencyExport = boolDefault(cfg.RequireEmergencyExport, plan.RequireEmergencyExport)
	plan.RequireRestoreDrill = boolDefault(cfg.RequireRestoreDrill, plan.RequireRestoreDrill)
	plan.RequireOffHostCopy = boolDefault(cfg.RequireOffHostCopy, plan.RequireOffHostCopy)
	plan.RecommendOffsite = boolDefault(cfg.RecommendOffsite, plan.RecommendOffsite)
	plan.RecommendImmutableCopy = boolDefault(cfg.RecommendImmutableCopy, plan.RecommendImmutableCopy)
	if cfg.KopiaIndependentFallback != "" {
		plan.KopiaIndependentFallback = cfg.KopiaIndependentFallback
	}
	return plan
}

func buildMultiServerPlan(resilience *models.BackupResilienceSpec, nodeCount, mainManagers int) MultiServerPlan {
	cfg := (*models.MultiServerBackupSafetySpec)(nil)
	if resilience != nil {
		cfg = resilience.MultiServer
	}
	plan := MultiServerPlan{
		Enabled:                      nodeCount > 1,
		Topology:                     topologyForNodeCount(nodeCount),
		MinServers:                   maxInt(nodeCount, 1),
		MinManagers:                  maxInt(mainManagers, 1),
		QuorumSize:                   quorumForManagers(mainManagers),
		ToleratedManagerFailures:     toleratedFailuresForManagers(mainManagers),
		CapacityHeadroomNodes:        1,
		ReleaseReadyHA:               nodeCount >= 3 && mainManagers >= 3,
		CoordinationMode:             "node-orchestrated",
		RequireOffsiteRepo:           true,
		RequireEmergencyExport:       true,
		RequireRestoreDrill:          true,
		RequireSharedVolumeInventory: true,
		RequirePlacementSpread:       nodeCount >= 3,
		RequireManagedServerlessPlan: true,
		Media: MultiServerMediaPlan{
			DocumentsMode:           "shared-volume-inventory",
			PhotosMode:              "shared-volume-inventory",
			LargeMediaMode:          "manifest-plus-tiered-sync",
			ExcludeGeneratedCaches:  true,
			RequireExternalMediaMap: true,
		},
		Performance: MultiServerPerformancePlan{
			Profile:                    "staggered",
			AvoidPrimaryOnlySnapshots:  true,
			StaggerNodeSnapshots:       true,
			MaxConcurrentNodeSnapshots: 1,
			PreferRepoServerFanIn:      true,
		},
	}
	if cfg == nil {
		return plan
	}
	plan.Enabled = boolDefault(cfg.Enabled, plan.Enabled)
	if cfg.Topology != "" {
		plan.Topology = cfg.Topology
	}
	if cfg.MinServers > 0 {
		plan.MinServers = cfg.MinServers
	}
	if cfg.MinManagers > 0 {
		plan.MinManagers = cfg.MinManagers
	}
	if cfg.QuorumSize > 0 {
		plan.QuorumSize = cfg.QuorumSize
	}
	if cfg.ToleratedManagerFailures > 0 {
		plan.ToleratedManagerFailures = cfg.ToleratedManagerFailures
	}
	if cfg.CapacityHeadroomNodes > 0 {
		plan.CapacityHeadroomNodes = cfg.CapacityHeadroomNodes
	}
	plan.ReleaseReadyHA = boolDefault(cfg.ReleaseReadyHA, plan.ReleaseReadyHA)
	if cfg.CoordinationMode != "" {
		plan.CoordinationMode = cfg.CoordinationMode
	}
	plan.RequireOffsiteRepo = boolDefault(cfg.RequireOffsiteRepo, plan.RequireOffsiteRepo)
	plan.RequireEmergencyExport = boolDefault(cfg.RequireEmergencyExport, plan.RequireEmergencyExport)
	plan.RequireRestoreDrill = boolDefault(cfg.RequireRestoreDrill, plan.RequireRestoreDrill)
	plan.RequireSharedVolumeInventory = boolDefault(cfg.RequireSharedVolumeInventory, plan.RequireSharedVolumeInventory)
	plan.RequirePlacementSpread = boolDefault(cfg.RequirePlacementSpread, plan.RequirePlacementSpread)
	plan.RequireManagedServerlessPlan = boolDefault(cfg.RequireManagedServerlessPlan, plan.RequireManagedServerlessPlan)
	if cfg.Media != nil {
		overlayMedia(&plan.Media, cfg.Media)
	}
	if cfg.Performance != nil {
		overlayPerformance(&plan.Performance, cfg.Performance)
	}
	return plan
}

func buildEmergencyExportPlan(resilience *models.BackupResilienceSpec, dataClasses []string) EmergencyExportPlan {
	cfg := (*models.BackupEmergencyExportSpec)(nil)
	if resilience != nil {
		cfg = resilience.EmergencyExport
	}
	plan := EmergencyExportPlan{
		Enabled:        true,
		Mode:           "portable-archive",
		Format:         "tar.zst.age",
		IncludeClasses: append([]string(nil), dataClasses...),
		LargeMediaMode: "manifest-only",
		Manifest: EmergencyExportManifestPlan{
			Enabled:               true,
			IncludeRestoreRunbook: true,
			IncludeChecksums:      true,
		},
	}
	if cfg == nil {
		return plan
	}
	plan.Enabled = boolDefault(cfg.Enabled, plan.Enabled)
	if cfg.Mode != "" {
		plan.Mode = cfg.Mode
	}
	if cfg.Format != "" {
		plan.Format = cfg.Format
	}
	if cfg.Schedule != "" {
		plan.Schedule = cfg.Schedule
	}
	if len(cfg.IncludeClasses) > 0 {
		plan.IncludeClasses = append([]string(nil), cfg.IncludeClasses...)
	}
	if cfg.LargeMediaMode != "" {
		plan.LargeMediaMode = cfg.LargeMediaMode
	}
	if cfg.Target != nil {
		target := *cfg.Target
		plan.Target = &target
	}
	if cfg.Manifest != nil {
		plan.Manifest.Enabled = boolDefault(cfg.Manifest.Enabled, plan.Manifest.Enabled)
		plan.Manifest.IncludeRestoreRunbook = boolDefault(cfg.Manifest.IncludeRestoreRunbook, plan.Manifest.IncludeRestoreRunbook)
		plan.Manifest.IncludeChecksums = boolDefault(cfg.Manifest.IncludeChecksums, plan.Manifest.IncludeChecksums)
	}
	return plan
}

func buildManagedServerlessPlan(spec *models.StackSpec, resilience *models.BackupResilienceSpec, dataClasses []string) ManagedServerlessPlan {
	cfg := (*models.ManagedServerlessRecoverySpec)(nil)
	if resilience != nil {
		cfg = resilience.ManagedServerless
	}
	defaultEnabled := spec != nil && spec.EffectivePlacementMode() == models.PlacementManaged
	plan := ManagedServerlessPlan{
		Enabled:                    defaultEnabled,
		NoServerDependency:         true,
		Authority:                  "control-plane",
		ProtectedClasses:           append([]string(nil), dataClasses...),
		ControlPlaneSnapshot:       true,
		ProviderNativeBackups:      true,
		RequireProviderDataHandles: true,
		RequireRebuildIntent:       true,
		PortableManifest:           true,
		PreChangeSnapshot:          true,
		Schedule:                   "before-change-and-daily",
	}
	if cfg == nil {
		return plan
	}
	plan.Enabled = boolDefault(cfg.Enabled, plan.Enabled)
	plan.NoServerDependency = boolDefault(cfg.NoServerDependency, plan.NoServerDependency)
	if cfg.Authority != "" {
		plan.Authority = cfg.Authority
	}
	if len(cfg.ProtectedClasses) > 0 {
		plan.ProtectedClasses = append([]string(nil), cfg.ProtectedClasses...)
	}
	plan.ControlPlaneSnapshot = boolDefault(cfg.ControlPlaneSnapshot, plan.ControlPlaneSnapshot)
	plan.ProviderNativeBackups = boolDefault(cfg.ProviderNativeBackups, plan.ProviderNativeBackups)
	plan.RequireProviderDataHandles = boolDefault(cfg.RequireProviderDataHandles, plan.RequireProviderDataHandles)
	plan.RequireRebuildIntent = boolDefault(cfg.RequireRebuildIntent, plan.RequireRebuildIntent)
	plan.PortableManifest = boolDefault(cfg.PortableManifest, plan.PortableManifest)
	plan.PreChangeSnapshot = boolDefault(cfg.PreChangeSnapshot, plan.PreChangeSnapshot)
	if cfg.Schedule != "" {
		plan.Schedule = cfg.Schedule
	}
	return plan
}

func planFollowUps(plan RecoveryPlan) []string {
	var followUps []string
	if !plan.EmergencyExport.Enabled {
		followUps = append(followUps, "enable emergencyExport or document an equivalent Kopia-independent export")
	}
	if plan.NodeCount == 2 && !plan.MultiServer.ReleaseReadyHA {
		followUps = append(followUps, "two-server topology improves restore capacity but is not quorum HA; add a third manager for release-ready HA")
	}
	if plan.ManagedServerless.Enabled && plan.ManagedServerless.Authority != "control-plane" {
		followUps = append(followUps, "managed serverless recovery authority should be control-plane")
	}
	if plan.SingleServer.Enabled && !plan.SingleServer.RequireOffHostCopy {
		followUps = append(followUps, "single-server recovery needs an off-host copy")
	}
	return followUps
}

func overlayMedia(plan *MultiServerMediaPlan, cfg *models.MultiServerBackupMediaSpec) {
	if cfg.DocumentsMode != "" {
		plan.DocumentsMode = cfg.DocumentsMode
	}
	if cfg.PhotosMode != "" {
		plan.PhotosMode = cfg.PhotosMode
	}
	if cfg.LargeMediaMode != "" {
		plan.LargeMediaMode = cfg.LargeMediaMode
	}
	plan.ExcludeGeneratedCaches = boolDefault(cfg.ExcludeGeneratedCaches, plan.ExcludeGeneratedCaches)
	plan.RequireExternalMediaMap = boolDefault(cfg.RequireExternalMediaMap, plan.RequireExternalMediaMap)
}

func overlayPerformance(plan *MultiServerPerformancePlan, cfg *models.MultiServerBackupPerformanceSpec) {
	if cfg.Profile != "" {
		plan.Profile = cfg.Profile
	}
	plan.AvoidPrimaryOnlySnapshots = boolDefault(cfg.AvoidPrimaryOnlySnapshots, plan.AvoidPrimaryOnlySnapshots)
	plan.StaggerNodeSnapshots = boolDefault(cfg.StaggerNodeSnapshots, plan.StaggerNodeSnapshots)
	if cfg.MaxConcurrentNodeSnapshots > 0 {
		plan.MaxConcurrentNodeSnapshots = cfg.MaxConcurrentNodeSnapshots
	}
	plan.PreferRepoServerFanIn = boolDefault(cfg.PreferRepoServerFanIn, plan.PreferRepoServerFanIn)
}

func normalizeEngine(engine, backend string) string {
	value := strings.ToLower(strings.TrimSpace(engine))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(backend))
	}
	switch value {
	case "", "kopia":
		return "kopia"
	case "restic", "restic-import":
		return "restic-import"
	default:
		return value
	}
}

func effectiveNodeCount(spec *models.StackSpec) int {
	if spec == nil || len(spec.Nodes) == 0 {
		return 1
	}
	return len(spec.Nodes)
}

func countMainManagers(spec *models.StackSpec) int {
	if spec == nil || len(spec.Nodes) == 0 {
		return 1
	}
	count := 0
	for _, node := range spec.Nodes {
		if models.IsMainNodeRole(node.Role) {
			count++
		}
	}
	if count == 0 && len(spec.Nodes) > 0 {
		return 1
	}
	return count
}

func topologyForNodeCount(nodeCount int) string {
	switch {
	case nodeCount >= 3:
		return "three-server-ha"
	case nodeCount == 2:
		return "two-server"
	default:
		return "single-server"
	}
}

func quorumForManagers(managers int) int {
	if managers <= 1 {
		return 1
	}
	return managers/2 + 1
}

func toleratedFailuresForManagers(managers int) int {
	if managers < 3 {
		return 0
	}
	return (managers - 1) / 2
}

func defaultDataClasses() []string {
	return []string{"config", "secrets", "platform-state", "database", "documents", "serverless-config"}
}

func stringSliceDefault(value, fallback []string) []string {
	if len(value) > 0 {
		return append([]string(nil), value...)
	}
	return append([]string(nil), fallback...)
}

func boolDefault(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
