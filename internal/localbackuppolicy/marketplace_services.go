package localbackuppolicy

import (
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

// MarketplaceService selects upstream-owned data without impersonating a
// compiler workload or adopting a HostDeployment. It is sealed in the Plan's
// source policy; discovery cannot add sources at snapshot time.
type MarketplaceService struct {
	SiteRef            string                       `json:"siteRef"`
	NodeRef            string                       `json:"nodeRef"`
	InstallationDigest string                       `json:"installationDigest"`
	ServerUUID         string                       `json:"serverUUID"`
	ServiceUUID        string                       `json:"serviceUUID"`
	ComponentUUID      string                       `json:"componentUUID"`
	ComponentName      string                       `json:"componentName"`
	VolumeName         string                       `json:"volumeName"`
	ContainerTarget    string                       `json:"containerTarget"`
	BackupDataClass    string                       `json:"backupDataClass"`
	ExpectedGeneration MarketplaceServiceGeneration `json:"expectedGeneration"`
}

// Docker has no immutable volume UUID. The retained container reference,
// daemon, exact mount and volume metadata must all survive this first slice.
type MarketplaceServiceGeneration struct {
	DaemonID             string `json:"daemonID"`
	ContainerID          string `json:"containerID"`
	ImageID              string `json:"imageID"`
	VolumeCreatedAt      string `json:"volumeCreatedAt"`
	VolumeMountpoint     string `json:"volumeMountpoint"`
	VolumeMetadataDigest string `json:"volumeMetadataDigest"`
}

var marketplaceUUID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var dockerContainerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func canonicalMarketplaceServices(input []MarketplaceService) ([]MarketplaceService, error) {
	if len(input) == 0 {
		return nil, nil
	}
	// One independently writable filesystem component is the qualified first
	// implementation; no unqualified multiwriter/dependency group is inferred.
	if len(input) != 1 {
		return nil, fmt.Errorf("Marketplace store backup currently requires one explicitly selected filesystem service/volume")
	}
	result := slices.Clone(input)
	for _, s := range result {
		for _, id := range []string{s.SiteRef, s.NodeRef, s.ServerUUID, s.ServiceUUID, s.ComponentUUID, s.ComponentName} {
			if !marketplaceUUID.MatchString(id) {
				return nil, fmt.Errorf("Marketplace store source has an invalid node or service identity")
			}
		}
		if !imageDigestPattern.MatchString(s.InstallationDigest) || !imageDigestPattern.MatchString(s.ExpectedGeneration.ImageID) || !imageDigestPattern.MatchString(s.ExpectedGeneration.VolumeMetadataDigest) || !dockerContainerID.MatchString(s.ExpectedGeneration.ContainerID) || !marketplaceUUID.MatchString(s.ExpectedGeneration.DaemonID) {
			return nil, fmt.Errorf("Marketplace store source requires exact installation/daemon/container/image/volume generation")
		}
		if !applicationVolumeNameRegex.MatchString(s.VolumeName) || strings.Contains(s.VolumeName, "/") {
			return nil, fmt.Errorf("Marketplace store source has an invalid local volume name")
		}
		if s.ContainerTarget == "/" || !path.IsAbs(s.ContainerTarget) || path.Clean(s.ContainerTarget) != s.ContainerTarget || strings.ContainsRune(s.ContainerTarget, 0) {
			return nil, fmt.Errorf("Marketplace store mount target must be a confined canonical absolute path")
		}
		mount := s.ExpectedGeneration.VolumeMountpoint
		if !path.IsAbs(mount) || path.Clean(mount) != mount || !strings.HasSuffix(mount, "/volumes/"+s.VolumeName+"/_data") {
			return nil, fmt.Errorf("Marketplace store volume requires its exact local Docker mountpoint")
		}
		if _, err := time.Parse(time.RFC3339Nano, s.ExpectedGeneration.VolumeCreatedAt); err != nil {
			return nil, fmt.Errorf("Marketplace store volume requires native creation metadata")
		}
		if s.BackupDataClass != "config" && s.BackupDataClass != "user-content" {
			return nil, fmt.Errorf("Marketplace filesystem source must select config or user-content retention")
		}
	}
	return result, nil
}

func (source Source) WithMarketplaceServices(input []MarketplaceService) (Source, error) {
	if len(source.MarketplaceServices) != 0 {
		return Source{}, fmt.Errorf("Marketplace source already selected")
	}
	services, err := canonicalMarketplaceServices(input)
	if err != nil {
		return Source{}, err
	}
	for _, service := range services {
		if slices.Contains(source.ManagedVolumeNames, service.VolumeName) {
			return Source{}, fmt.Errorf("Marketplace store source collides with a Core or curated volume")
		}
		source.ManagedVolumeNames = append(slices.Clone(source.ManagedVolumeNames), service.VolumeName)
	}
	source.MarketplaceServices = services
	return source, nil
}

func MarketplaceServicesForTarget(input []MarketplaceService, site, node string) ([]MarketplaceService, error) {
	services, err := canonicalMarketplaceServices(input)
	if err != nil {
		return nil, err
	}
	var selected []MarketplaceService
	for _, s := range services {
		if s.SiteRef == site && s.NodeRef == node {
			selected = append(selected, s)
		}
	}
	return selected, nil
}

func validateMarketplaceServices(source Source, target *Target) error {
	services, err := canonicalMarketplaceServices(source.MarketplaceServices)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(services, source.MarketplaceServices) {
		return fmt.Errorf("Marketplace service source is not canonical")
	}
	if target != nil {
		for _, s := range services {
			if s.SiteRef != target.SiteRef || s.NodeRef != target.NodeRef {
				return fmt.Errorf("Marketplace store source belongs to another node")
			}
		}
	}
	return nil
}

// RequireMarketplaceServiceGeneration is the write/copy admission boundary.
// A same-name replacement is a new generation, not an automatic restore target.
func RequireMarketplaceServiceGeneration(expected, observed MarketplaceService) error {
	if _, err := canonicalMarketplaceServices([]MarketplaceService{expected}); err != nil {
		return err
	}
	if _, err := canonicalMarketplaceServices([]MarketplaceService{observed}); err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, observed) {
		return fmt.Errorf("Marketplace store service/container/volume generation differs from the Owner-selected source")
	}
	return nil
}
