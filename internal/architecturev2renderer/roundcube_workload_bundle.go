package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"reflect"
)

const (
	roundcubeWorkloadModuleID    = "stackkits-roundcube-runtime"
	roundcubeWorkloadUnitID      = "roundcube"
	roundcubeWorkloadTemplateRef = "builtin://workloads/roundcube/bundle/v1.json"
	roundcubeWorkloadVersion     = "1.0.0"
	roundcubeWorkloadOutputRef   = "workloads/roundcube/bundle.json"

	roundcubeGovernedDir = "/var/roundcube/config/stackkit"
	// RoundcubeMailboxScriptPath is the governed script `stackkit setup mail`
	// runs through the fixed Compose exec contract to store the owner's
	// mailbox endpoints on the persistent mailbox volume.
	RoundcubeMailboxScriptPath = roundcubeGovernedDir + "/mailbox.sh"
	roundcubeInitScriptPath    = roundcubeGovernedDir + "/init.sh"
)

// The governed Roundcube files. None carries a secret: the 24-character key
// for the default DES-EDE3-CBC cipher is derived at runtime from the
// custody-backed ROUNDCUBEMAIL_DES_KEY environment (an absent key makes
// Roundcube refuse to encrypt), and the mailbox endpoints come from the owner
// file the setup action writes. Until that file exists imap_host points to an
// unresolvable placeholder, so the login form never offers a free-form server.
var (
	//go:embed assets/roundcube/config.php
	roundcubeConfig string
	//go:embed assets/roundcube/init.sh
	roundcubeInitScript string
	//go:embed assets/roundcube/mailbox.sh
	roundcubeMailboxScript string
	//go:embed assets/roundcube/devices.php
	roundcubeDevicesPHP string
	//go:embed assets/roundcube/devices.conf
	roundcubeDevicesConf string
)

func roundcubeConfigFiles() []selectedPaaSConfigFile {
	return []selectedPaaSConfigFile{
		{Path: roundcubeGovernedDir + "/config.php", Body: roundcubeConfig},
		{Path: roundcubeInitScriptPath, Body: roundcubeInitScript},
		{Path: RoundcubeMailboxScriptPath, Body: roundcubeMailboxScript},
		{Path: roundcubeGovernedDir + "/devices.php", Body: roundcubeDevicesPHP},
		{Path: roundcubeGovernedDir + "/devices.conf", Body: roundcubeDevicesConf},
	}
}

// roundcubeAssetsDigest binds the renderer contract to the exact governed
// files, so any edit to them changes the CUE-declared contract hash.
func roundcubeAssetsDigest() string {
	digest := sha256.New()
	for _, file := range roundcubeConfigFiles() {
		digest.Write([]byte(file.Path + "\x00" + file.Body + "\x00"))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func roundcubeWorkloadRendererSchema() string {
	return `stackkit.workload-bundle/v2|RoundcubeWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:roundcube|egress:external-mailbox|volumes:database,mailbox,config,temp|governed-files:sha256:` + roundcubeAssetsDigest() + `|health:http-root-80|release:` + roundcubeRelease + `|secret-material:not-included`
}

// RoundcubeWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the standalone application adapter for Mail.
type RoundcubeWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Route       ApplicationDeliveryRouteDescriptor
}

func RoundcubeWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(roundcubeWorkloadRendererSchema()))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: roundcubeWorkloadTemplateRef,
		Version: roundcubeWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type roundcubeWorkloadBundleRenderer struct{ contract RendererContract }

func newRoundcubeWorkloadBundleRenderer() roundcubeWorkloadBundleRenderer {
	return roundcubeWorkloadBundleRenderer{contract: RoundcubeWorkloadBundleRendererContract()}
}

func (r roundcubeWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateRoundcubeWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.roundcube-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: roundcubeWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseRoundcubeWorkloadBundle validates the closed artifact before a runtime
// owner may consume it. The session key stays an opaque reference.
func ParseRoundcubeWorkloadBundle(data []byte) (RoundcubeWorkloadBundleDescriptor, error) {
	path := "roundcubeWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return RoundcubeWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed Roundcube workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "RoundcubeWorkloadBundle" ||
		bundle.Workload.Ref != "mail" || bundle.Workload.AlternativeRef != "roundcube" ||
		bundle.Workload.ModuleRef != roundcubeWorkloadModuleID || bundle.Workload.Release != roundcubeRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != roundcubeWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return RoundcubeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed Roundcube contract")
	}
	if len(bundle.SecretRefs) != 1 || !validSecretReference(bundle.SecretRefs["session-key"]) {
		return RoundcubeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "requires exactly one opaque session-key reference")
	}
	if !reflect.DeepEqual(bundle.ConfigFiles, roundcubeConfigFiles()) {
		return RoundcubeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "Roundcube startup file differs from the governed renderer output")
	}
	if bundle.DeliveryRoute == nil {
		return RoundcubeWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".deliveryRoute", "Roundcube requires its declared HTTPS origin")
	}
	if err := validateParsedApplicationDeliveryRoute(*bundle.DeliveryRoute, roundcubeWorkloadModuleID, "mail", 80, path+".deliveryRoute"); err != nil {
		return RoundcubeWorkloadBundleDescriptor{}, err
	}
	if err := validateRoundcubeRuntimeComponents(bundle.Components, path+".components"); err != nil {
		return RoundcubeWorkloadBundleDescriptor{}, err
	}
	if err := validateRoundcubeServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return RoundcubeWorkloadBundleDescriptor{}, err
	}
	return RoundcubeWorkloadBundleDescriptor{
		WorkloadRef: "mail", ModuleRef: roundcubeWorkloadModuleID, Release: roundcubeRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Route: bundle.DeliveryRoute.descriptor(),
	}, nil
}

//nolint:gocyclo // One boundary validates the complete render-unit authority.
func validateRoundcubeWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + roundcubeWorkloadModuleID + ".renderUnits." + roundcubeWorkloadUnitID
	if unit.ModuleID() != roundcubeWorkloadModuleID || unit.ID() != roundcubeWorkloadUnitID ||
		unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef || unit.TemplateRef() != contract.TemplateRef ||
		unit.Version() != contract.Version || unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit identity differs from the registered Roundcube contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" || !hasEngine || engine != "docker" ||
		!hasImage || imageRef != roundcubeImageRef || !hasDigest || imageDigest != roundcubeImageDigest || !hasEntry || entry != roundcubeWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity differs from the pinned Roundcube image")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode || !exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) || !exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "Roundcube requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "application adapter receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, roundcubeWorkloadModuleID, "mail", 80, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute == nil {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Roundcube requires a declared delivery route")
	}
	if !exactStringList(unit.SecretInputRefs(), []string{"session-key"}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "Roundcube requires only its session-key slot")
	}
	secretRefs := map[string]string{}
	if err := decodeStrict(unit.SecretRefsJSON(), &secretRefs); err != nil || len(secretRefs) != 1 || !validSecretReference(secretRefs["session-key"]) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".secretRefs", "requires one opaque session-key reference and no secret material")
	}
	if !emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".interfaces", "Roundcube receives no host, socket or runtime-network authority")
	}
	var placement struct{ Scope, Cardinality string }
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil || placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != roundcubeWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", roundcubeWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode component graph", err)
	}
	if err := validateRoundcubeRuntimeComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact mail endpoint")
	}
	if err := validateRoundcubeServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "RoundcubeWorkloadBundle", SecretRefs: secretRefs,
		Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute, ConfigFiles: roundcubeConfigFiles(),
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = "mail", "roundcube"
	bundle.Workload.ModuleRef, bundle.Workload.Release = roundcubeWorkloadModuleID, roundcubeRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter, bundle.Ownership.ProviderLifecycle, bundle.Ownership.Credentials = "selected-application-adapter", "not-owned", "opaque-references-only"
	return bundle, nil
}

var roundcubeEnvironment = map[string]string{
	"ROUNDCUBEMAIL_DB_TYPE":      "sqlite",
	"ROUNDCUBEMAIL_DB_DIR":       "/var/roundcube/db",
	"ROUNDCUBEMAIL_SKIN":         "elastic",
	"ROUNDCUBEMAIL_PLUGINS":      "archive,zipdownload",
	"ROUNDCUBEMAIL_TEMP_DIR":     "/tmp/roundcube-temp",
	"ROUNDCUBEMAIL_REQUEST_PATH": "/",
}

var roundcubeVolumes = map[string]selectedPaaSRuntimeVolume{
	"database": {ID: "database", Target: "/var/roundcube/db", Class: "persistent", Backup: true},
	"mailbox":  {ID: "mailbox", Target: "/var/roundcube/mailbox", Class: "persistent", Backup: true},
	"config":   {ID: "config", Target: "/var/roundcube/config", Class: "cache"},
	"temp":     {ID: "temp", Target: "/tmp/roundcube-temp", Class: "cache"},
}

func validateRoundcubeRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	if len(components) != 1 {
		return fail(ErrInvalidPlan, path, "requires exactly the Roundcube component")
	}
	c := components[0]
	if c.ID != roundcubeWorkloadUnitID || c.Role != "application" || c.Lifecycle != "daemon" || c.HealthFailure != "" ||
		c.Image.Ref != roundcubeImageRef || c.Image.Digest != roundcubeImageDigest || len(c.DependsOn) != 0 ||
		!exactStringList(c.NetworkRefs, []string{"mail-internal"}) || !c.Egress || !exactStringList(c.Entrypoint, []string{"/bin/sh", roundcubeInitScriptPath}) ||
		!exactStringList(c.Command, []string{"apache2-foreground"}) ||
		c.RouteHostLoopback || c.DockerLifecycleOwner != nil || len(c.OwnerEnvironment) != 0 ||
		!reflect.DeepEqual(c.Environment, roundcubeEnvironment) ||
		!reflect.DeepEqual(c.SecretEnvironment, map[string]string{"ROUNDCUBEMAIL_DES_KEY": "session-key"}) ||
		c.Health.Kind != "http" || c.Health.Path != "/" || c.Health.Port != 80 || len(c.Health.Command) != 0 {
		return fail(ErrInvalidPlan, path, "Roundcube image, egress, environment, key binding or health differs from the closed contract")
	}
	if len(c.Volumes) != len(roundcubeVolumes) {
		return fail(ErrInvalidPlan, path+".volumes", "Roundcube requires its database, mailbox, config and temp volumes")
	}
	for _, volume := range c.Volumes {
		if want, ok := roundcubeVolumes[volume.ID]; !ok || !reflect.DeepEqual(volume, want) {
			return fail(ErrInvalidPlan, path+".volumes", "Roundcube volume %q differs from the closed contract", volume.ID)
		}
	}
	return nil
}

func validateRoundcubeServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != "mail" || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != 80 || endpoint.RequiredPrivilege != "user" ||
		endpoint.OriginSelector != "control-authority-site" || endpoint.HealthRef != "roundcube-http" || !validBundleApplicationIngressAuth(endpoint.IngressAuth) ||
		endpoint.Data.BindingRef != "mail" || endpoint.Data.Locality != "primary-site" || !exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) || !sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private", "public"}) {
		return fail(ErrInvalidPlan, path, "mail route authority differs from the governed Roundcube endpoint")
	}
	return nil
}
