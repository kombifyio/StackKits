package architecturev2renderer

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/internal/accessgatetable"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// OwnApplicationDescriptor adapts the closed user contract to the same graph
// the catalog supplies. Safety and naming admission live in the wire contract.
func OwnApplicationDescriptor(raw []byte) (ApplicationDeliveryBundleDescriptor, error) {
	var b ownapplication.Bundle
	if err := ownapplication.Decode(raw, &b); err != nil {
		return ApplicationDeliveryBundleDescriptor{}, err
	}
	if err := b.Validate(); err != nil {
		return ApplicationDeliveryBundleDescriptor{}, err
	}
	if b.Application.IsProject() {
		return ownProjectDescriptor(b)
	}
	a, i := b.Application, b.Installation
	image := a.Image
	if b.ResolvedImage != "" {
		image = b.ResolvedImage
	}
	ref, d, _ := ownapplication.SplitImage(image)
	limits := a.Limits()
	c := ApplicationDeliveryComponentDescriptor{ID: "app", Role: "application", Lifecycle: "daemon", HealthFailure: "blocking", ImageID: b.ImageID, ImageRef: ref, ImageDigest: d, Egress: false, Environment: a.Environment, SecretEnvironment: map[string]string{}, HealthKind: "http", HealthPath: "/", HealthPort: a.HTTPPort, Resources: &ApplicationDeliveryResourcesDescriptor{MemoryLimit: fmt.Sprintf("%dm", limits.MemoryMiB), CPUs: limits.CPUs}}
	for key := range a.SecretRefs {
		c.SecretEnvironment[key] = key
	}
	if a.Volume != nil {
		c.Volumes = []ApplicationDeliveryVolumeDescriptor{{ID: "data", Target: a.Volume.MountPath, Class: "persistent", Backup: true}}
	}
	auth := "none"
	if a.Protected() {
		auth = "forward-auth"
	}
	var ports []ApplicationDeliveryOwnPublishedPort
	for _, p := range a.PublishedPorts {
		ports = append(ports, ApplicationDeliveryOwnPublishedPort{HostIP: p.HostIP, HostPort: p.HostPort, ContainerPort: p.ContainerPort, Protocol: p.Protocol})
	}
	return ApplicationDeliveryBundleDescriptor{OwnApplication: true, OwnApplicationFirewalled: a.Firewalled(), OwnApplicationOutbound: a.OutboundEnabled(), OwnApplicationPorts: ports, WorkloadRef: a.WorkloadRef(), ModuleRef: ownapplication.ModuleRef, Release: image, EntryComponent: "app", SiteRef: i.SiteRef, NodeRef: i.NodeRef, InstanceRef: a.WorkloadRef() + "-" + i.NodeRef, SecretRefs: a.SecretRefs, Components: []ApplicationDeliveryComponentDescriptor{c}, Route: ApplicationDeliveryRouteDescriptor{CoreModuleRef: i.CoreModuleRef, ID: a.WorkloadRef(), ServiceRef: a.WorkloadRef(), ModuleRef: ownapplication.ModuleRef, Exposure: i.Exposure, Protocol: "https", UpstreamProtocol: "http", HealthGateRef: a.WorkloadRef(), BackendPoolRef: a.WorkloadRef(), Host: a.Hostname, Path: "/", Port: 443, TargetPort: a.HTTPPort, TLSRequired: true, TLSMode: "terminate", TLSMinVersion: "1.2", TLSProfileRef: i.TLSProfileRef, TLSIssuerRef: i.TLSIssuerRef, IngressAuth: auth}}, nil
}

// ownProjectDescriptor adapts a Compose project to the same delivery graph:
// one component per declared service, one routed service on the project
// hostname, and every other service on the project's private network.
func ownProjectDescriptor(b ownapplication.Bundle) (ApplicationDeliveryBundleDescriptor, error) {
	a, i := b.Application, b.Installation
	components := make([]ApplicationDeliveryComponentDescriptor, 0, len(a.Services))
	secretRefs := map[string]string{}
	entry := ""
	for _, s := range a.Services {
		image := s.Image
		if pinned := b.ResolvedImages[s.Name]; pinned != "" {
			image = pinned
		}
		ref, d, _ := ownapplication.SplitImage(image)
		limits := s.Limits()
		c := ApplicationDeliveryComponentDescriptor{ID: s.Name, Role: "application", Lifecycle: "daemon", HealthFailure: "blocking", ImageID: b.ImageIDs[s.Name], ImageRef: ref, ImageDigest: d, Egress: false, Environment: s.Environment, SecretEnvironment: map[string]string{}, HealthKind: "http", HealthPath: "/", HealthPort: s.HTTPPort, Resources: &ApplicationDeliveryResourcesDescriptor{MemoryLimit: fmt.Sprintf("%dm", limits.MemoryMiB), CPUs: limits.CPUs}, DependsOn: s.DependsOn}
		for key, secret := range s.SecretRefs {
			slot := ownapplication.SecretSlotKey(s.Name, key)
			c.SecretEnvironment[key] = slot
			secretRefs[slot] = secret
		}
		if s.Volume != nil {
			c.Volumes = []ApplicationDeliveryVolumeDescriptor{{ID: "data", Target: s.Volume.MountPath, Class: "persistent", Backup: true}}
		}
		if s.HTTPPort != 0 {
			entry = s.Name
		}
		components = append(components, c)
	}
	routed, ok := a.RoutedService()
	if !ok || entry == "" {
		return ApplicationDeliveryBundleDescriptor{}, errors.New("an own application project declares exactly one routed service")
	}
	auth := "none"
	if a.Protected() {
		auth = "forward-auth"
	}
	return ApplicationDeliveryBundleDescriptor{OwnApplication: true, OwnApplicationOutbound: false, WorkloadRef: a.WorkloadRef(), ModuleRef: ownapplication.ModuleRef, Release: b.ProjectRelease(), EntryComponent: entry, SiteRef: i.SiteRef, NodeRef: i.NodeRef, InstanceRef: a.WorkloadRef() + "-" + i.NodeRef, SecretRefs: secretRefs, Components: components, Route: ApplicationDeliveryRouteDescriptor{CoreModuleRef: i.CoreModuleRef, ID: a.WorkloadRef(), ServiceRef: a.WorkloadRef(), ModuleRef: ownapplication.ModuleRef, Exposure: i.Exposure, Protocol: "https", UpstreamProtocol: "http", HealthGateRef: a.WorkloadRef(), BackendPoolRef: a.WorkloadRef(), Host: a.Hostname, Path: "/", Port: 443, TargetPort: routed.HTTPPort, TLSRequired: true, TLSMode: "terminate", TLSMinVersion: "1.2", TLSProfileRef: i.TLSProfileRef, TLSIssuerRef: i.TLSIssuerRef, IngressAuth: auth}}, nil
}

// OwnApplicationAccessRoute uses the existing Hub's device-bound browser
// session on both listeners; no arbitrary image is trusted to authenticate.
func OwnApplicationAccessRoute(b ownapplication.Bundle) accessgatetable.Route {
	a := b.Application
	r := accessgatetable.Route{ID: a.WorkloadRef(), Host: a.Hostname, Path: "/", Exposure: b.Installation.Exposure, Privilege: accessgatetable.PrivilegeUser, Authentication: accessgatetable.AuthenticationNone, IngressAuth: accessgatetable.IngressNone}
	if a.Protected() {
		r.Authentication = accessgatetable.AuthenticationHumanDevice
		r.IngressAuth = accessgatetable.IngressNative
		r.Groups = []string{"owners", "admins", "household"}
	} else if r.Exposure == accessgatetable.ExposureRemotePrivate {
		r.Exposure = accessgatetable.ExposurePublic
	}
	if !a.Protected() && r.Exposure == accessgatetable.ExposurePublic {
		r.AnonymousRateLimit = &accessgatetable.RateLimit{Requests: 60, WindowSeconds: 60}
	}
	return r
}
func OwnApplicationBackup(b ownapplication.Bundle) ([]localbackuppolicy.ApplicationVolume, []localbackuppolicy.ApplicationRuntime, error) {
	if err := b.Validate(); err != nil {
		return nil, nil, err
	}
	if b.Application.IsProject() {
		return ownProjectBackup(b)
	}
	if b.Application.Volume == nil {
		return nil, nil, nil
	}
	if b.ResolvedImage == "" {
		_, d, _ := ownapplication.SplitImage(b.Application.Image)
		if d == "" {
			return nil, nil, fmt.Errorf("backup membership requires node-resolved image custody")
		}
	}
	raw, _ := json.Marshal(b)
	d, err := OwnApplicationDescriptor(raw)
	if err != nil {
		return nil, nil, err
	}
	a, i := b.Application, b.Installation
	p := localbackuppolicy.StandaloneComposeProjectName(a.WorkloadRef(), i.NodeRef)
	v := localbackuppolicy.ApplicationVolume{WorkloadRef: a.WorkloadRef(), SiteRef: i.SiteRef, NodeRef: i.NodeRef, ComposeProject: p, ComponentRef: "app", VolumeRef: "data", LogicalName: "app-data", VolumeName: p + "_app-data", Target: a.Volume.MountPath, Class: "persistent", Backup: true, BackupDataClass: "user-content", DataClasses: []string{"sensitive"}, DataBindingRef: a.WorkloadRef() + "-data"}
	c := d.Components[0]
	r := localbackuppolicy.ApplicationRuntime{Firewalled: a.Firewalled(), OutboundInternet: a.OutboundEnabled(), RuntimeAdapterRef: "standalone-compose", WorkloadRef: a.WorkloadRef(), SiteRef: i.SiteRef, NodeRef: i.NodeRef, ComposeProject: p, Components: []localbackuppolicy.ApplicationRuntimeComponent{{ComponentRef: "app", Role: "application", Lifecycle: "daemon", ImageID: c.ImageID, ImageRef: c.ImageRef, ImageDigest: c.ImageDigest, DependsOn: []string{}}}}
	return []localbackuppolicy.ApplicationVolume{v}, []localbackuppolicy.ApplicationRuntime{r}, nil
}

// ownProjectBackup projects the declared volumes of every service of a project
// into the shared backup membership. The runtime records every service, so a
// restore knows the whole project it belongs to.
func ownProjectBackup(b ownapplication.Bundle) ([]localbackuppolicy.ApplicationVolume, []localbackuppolicy.ApplicationRuntime, error) {
	a, i := b.Application, b.Installation
	if !b.ImagesResolved() {
		return nil, nil, fmt.Errorf("backup membership requires node-resolved image custody")
	}
	raw, _ := json.Marshal(b)
	d, err := OwnApplicationDescriptor(raw)
	if err != nil {
		return nil, nil, err
	}
	p := localbackuppolicy.StandaloneComposeProjectName(a.WorkloadRef(), i.NodeRef)
	volumes := []localbackuppolicy.ApplicationVolume{}
	for _, s := range a.Services {
		if s.Volume == nil {
			continue
		}
		volumes = append(volumes, localbackuppolicy.ApplicationVolume{WorkloadRef: a.WorkloadRef(), SiteRef: i.SiteRef, NodeRef: i.NodeRef, ComposeProject: p, ComponentRef: s.Name, VolumeRef: "data", LogicalName: localbackuppolicy.StandaloneComposeLogicalVolumeName(s.Name, "data"), VolumeName: localbackuppolicy.StandaloneComposeVolumeName(a.WorkloadRef(), i.NodeRef, s.Name, "data"), Target: s.Volume.MountPath, Class: "persistent", Backup: true, BackupDataClass: "user-content", DataClasses: []string{"sensitive"}, DataBindingRef: a.WorkloadRef() + "-" + s.Name + "-data"})
	}
	components := make([]localbackuppolicy.ApplicationRuntimeComponent, 0, len(d.Components))
	for _, c := range d.Components {
		components = append(components, localbackuppolicy.ApplicationRuntimeComponent{ComponentRef: c.ID, Role: c.Role, Lifecycle: c.Lifecycle, ImageID: c.ImageID, ImageRef: c.ImageRef, ImageDigest: c.ImageDigest, DependsOn: c.DependsOn})
	}
	r := localbackuppolicy.ApplicationRuntime{OutboundInternet: false, RuntimeAdapterRef: "standalone-compose", WorkloadRef: a.WorkloadRef(), SiteRef: i.SiteRef, NodeRef: i.NodeRef, ComposeProject: p, Components: components}
	if len(volumes) == 0 {
		return nil, []localbackuppolicy.ApplicationRuntime{r}, nil
	}
	return volumes, []localbackuppolicy.ApplicationRuntime{r}, nil
}
