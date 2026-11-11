package architecturev2renderer

import (
	"encoding/json"
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
	return ApplicationDeliveryBundleDescriptor{OwnApplication: true, WorkloadRef: a.WorkloadRef(), ModuleRef: ownapplication.ModuleRef, Release: image, EntryComponent: "app", SiteRef: i.SiteRef, NodeRef: i.NodeRef, InstanceRef: a.WorkloadRef() + "-" + i.NodeRef, SecretRefs: a.SecretRefs, Components: []ApplicationDeliveryComponentDescriptor{c}, Route: ApplicationDeliveryRouteDescriptor{CoreModuleRef: i.CoreModuleRef, ID: a.WorkloadRef(), ServiceRef: a.WorkloadRef(), ModuleRef: ownapplication.ModuleRef, Exposure: i.Exposure, Protocol: "https", UpstreamProtocol: "http", HealthGateRef: a.WorkloadRef(), BackendPoolRef: a.WorkloadRef(), Host: a.Hostname, Path: "/", Port: 443, TargetPort: a.HTTPPort, TLSRequired: true, TLSMode: "terminate", TLSMinVersion: "1.2", TLSProfileRef: i.TLSProfileRef, TLSIssuerRef: i.TLSIssuerRef, IngressAuth: auth}}, nil
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
	r := localbackuppolicy.ApplicationRuntime{RuntimeAdapterRef: "standalone-compose", WorkloadRef: a.WorkloadRef(), SiteRef: i.SiteRef, NodeRef: i.NodeRef, ComposeProject: p, Components: []localbackuppolicy.ApplicationRuntimeComponent{{ComponentRef: "app", Role: "application", Lifecycle: "daemon", ImageID: c.ImageID, ImageRef: c.ImageRef, ImageDigest: c.ImageDigest, DependsOn: []string{}}}}
	return []localbackuppolicy.ApplicationVolume{v}, []localbackuppolicy.ApplicationRuntime{r}, nil
}
