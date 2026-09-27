package architecturev2renderer

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"path"
	"reflect"
	"slices"
	"strings"
)

// Private AI image and video (docs/use-case-expansion/ai-agents.md): ComfyUI
// on the node's GPU, or on the CPU when no accelerator profile is selected. ComfyUI has no sign-in of its own and custom nodes run
// arbitrary Python, so the governed runtime never loads ComfyUI-Manager or
// any custom node, has no paid API nodes and is reached only
// behind the kit's login on a private route. Model weights are never part of
// the bundle: the owner downloads a preset with `stackkit setup`.
const (
	comfyUIWorkloadModuleID    = "stackkits-comfyui-runtime"
	comfyUIWorkloadUnitID      = "comfyui"
	comfyUIWorkloadRef         = "ai-image-video"
	comfyUIWorkloadTemplateRef = "builtin://workloads/comfyui/bundle/v2.json"
	comfyUIWorkloadVersion     = "2.0.0"
	comfyUIWorkloadOutputRef   = "workloads/comfyui/bundle.json"
	comfyUIPort                = 8188
	comfyUICPUFlag             = "--cpu"

	// ComfyUIModelFetchScriptPath is the governed script `stackkit setup
	// ai-image-video` runs through the fixed Compose exec contract to
	// download and verify one model file of an owner-approved preset.
	ComfyUIModelFetchScriptPath = "/opt/ComfyUI/user/stackkit/fetch_model.py"
	// comfyUIWorkflowDir is the default user's workflow directory that the
	// ComfyUI workflow browser lists.
	comfyUIWorkflowDir = "/opt/ComfyUI/user/default/workflows"
)

var (
	//go:embed assets/comfyui/fetch_model.py
	comfyUIFetchModelScript string
	//go:embed assets/comfyui/workflows/*.json
	comfyUIWorkflowTemplates embed.FS
)

// comfyUIConfigFiles are the governed read-only files: the reviewed workflow
// templates and the model download script. None carries a secret.
func comfyUIConfigFiles() []selectedPaaSConfigFile {
	files := []selectedPaaSConfigFile{{Path: ComfyUIModelFetchScriptPath, Body: comfyUIFetchModelScript}}
	names, err := fs.Glob(comfyUIWorkflowTemplates, "assets/comfyui/workflows/*.json")
	if err != nil {
		panic(err)
	}
	slices.Sort(names)
	for _, name := range names {
		body, err := comfyUIWorkflowTemplates.ReadFile(name)
		if err != nil {
			panic(err)
		}
		files = append(files, selectedPaaSConfigFile{Path: comfyUIWorkflowDir + "/" + path.Base(name), Body: string(body)})
	}
	return files
}

// comfyUIAssetsDigest binds the renderer contract to the exact governed
// files, so any edit to them changes the CUE-declared contract hash.
func comfyUIAssetsDigest() string {
	digest := sha256.New()
	for _, file := range comfyUIConfigFiles() {
		digest.Write([]byte(file.Path + "\x00" + file.Body + "\x00"))
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func comfyUIWorkloadRendererSchema() string {
	return `stackkit.workload-bundle/v2|ComfyUIWorkloadBundle|application-adapter|route:authority-bound-module-route-v1|provider-lifecycle:not-owned|components:comfyui|accelerator:required|egress:owner-model-download|volumes:user,input,output,models|custom-nodes:disabled|governed-files:sha256:` +
		comfyUIAssetsDigest() + `|release:` + comfyuiRelease + `|secret-material:not-included|peer:ai/private-ai-internal`
}

// ComfyUIWorkloadBundleDescriptor is the closed, credential-free runtime
// artifact accepted by the standalone application adapter.
type ComfyUIWorkloadBundleDescriptor struct {
	WorkloadRef string
	ModuleRef   string
	Release     string
	SiteRef     string
	NodeRef     string
	InstanceRef string
	Components  []SelectedPaaSWorkloadComponentDescriptor
	Route       ApplicationDeliveryRouteDescriptor
}

func ComfyUIWorkloadBundleRendererContract() RendererContract {
	sum := sha256.Sum256([]byte(comfyUIWorkloadRendererSchema()))
	return RendererContract{
		Kind: "native-config", RendererRef: "stackkit", TemplateRef: comfyUIWorkloadTemplateRef,
		Version: comfyUIWorkloadVersion, ContractHash: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

type comfyUIWorkloadBundleRenderer struct{ contract RendererContract }

func newComfyUIWorkloadBundleRenderer() comfyUIWorkloadBundleRenderer {
	return comfyUIWorkloadBundleRenderer{contract: ComfyUIWorkloadBundleRendererContract()}
}

func (r comfyUIWorkloadBundleRenderer) RenderUnit(ctx context.Context, unit RenderUnit) ([]UnitOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	bundle, err := validateComfyUIWorkloadUnit(unit, r.contract)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(bundle)
	if err != nil {
		return nil, wrap(ErrRendererFailure, "renderer.comfyui-workload", "marshal governed workload bundle", err)
	}
	return []UnitOutput{{Ref: comfyUIWorkloadOutputRef, Bytes: append(data, '\n')}}, nil
}

// ParseComfyUIWorkloadBundle validates the closed artifact before a runtime
// owner may consume it.
func ParseComfyUIWorkloadBundle(data []byte) (ComfyUIWorkloadBundleDescriptor, error) {
	path := "comfyUIWorkloadBundle"
	var bundle selectedPaaSWorkloadBundle
	if err := decodeStrict(data, &bundle); err != nil {
		return ComfyUIWorkloadBundleDescriptor{}, wrap(ErrInvalidPlan, path, "decode closed ComfyUI workload bundle", err)
	}
	if bundle.APIVersion != "stackkit.workload-bundle/v2" || bundle.Kind != "ComfyUIWorkloadBundle" ||
		bundle.Workload.Ref != comfyUIWorkloadRef || bundle.Workload.AlternativeRef != "comfyui" ||
		bundle.Workload.ModuleRef != comfyUIWorkloadModuleID || bundle.Workload.Release != comfyuiRelease ||
		bundle.Workload.Delivery != "application-adapter" || bundle.Workload.EntryComponent != comfyUIWorkloadUnitID ||
		bundle.Ownership.ExecutionAdapter != "selected-application-adapter" ||
		bundle.Ownership.ProviderLifecycle != "not-owned" || bundle.Ownership.Credentials != "opaque-references-only" {
		return ComfyUIWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path, "workload or ownership identity differs from the closed ComfyUI "+comfyuiRelease+" contract")
	}
	if len(bundle.SecretRefs) != 0 {
		return ComfyUIWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".secretRefs", "ComfyUI receives no secret")
	}
	if !reflect.DeepEqual(bundle.ConfigFiles, comfyUIConfigFiles()) {
		return ComfyUIWorkloadBundleDescriptor{}, fail(ErrInvalidPlan, path+".configFiles", "ComfyUI templates and download script differ from the governed renderer output")
	}
	if err := validateComfyUIRuntimeComponents(bundle.Components, path+".components"); err != nil {
		return ComfyUIWorkloadBundleDescriptor{}, err
	}
	if err := validateComfyUIServiceEndpoint(bundle.Route, path+".route"); err != nil {
		return ComfyUIWorkloadBundleDescriptor{}, err
	}
	descriptor := ComfyUIWorkloadBundleDescriptor{
		WorkloadRef: comfyUIWorkloadRef, ModuleRef: comfyUIWorkloadModuleID, Release: comfyuiRelease,
		SiteRef: bundle.Target.SiteRef, NodeRef: bundle.Target.NodeRef, InstanceRef: bundle.Target.InstanceRef,
		Components: make([]SelectedPaaSWorkloadComponentDescriptor, len(bundle.Components)),
	}
	if bundle.DeliveryRoute != nil {
		if err := validateComfyUIRoute(*bundle.DeliveryRoute, path+".deliveryRoute"); err != nil {
			return ComfyUIWorkloadBundleDescriptor{}, err
		}
		descriptor.Route = bundle.DeliveryRoute.descriptor()
	}
	for index, component := range bundle.Components {
		descriptor.Components[index] = SelectedPaaSWorkloadComponentDescriptor{
			ID: component.ID, Lifecycle: component.Lifecycle,
			ImageRef: component.Image.Ref, ImageDigest: component.Image.Digest,
		}
	}
	return descriptor, nil
}

//nolint:gocyclo // One closed-contract check per render-unit field.
func validateComfyUIWorkloadUnit(unit RenderUnit, contract RendererContract) (selectedPaaSWorkloadBundle, error) {
	path := "resolvedPlan.modules." + comfyUIWorkloadModuleID + ".renderUnits." + comfyUIWorkloadUnitID
	if unit.ModuleID() != comfyUIWorkloadModuleID || unit.ID() != comfyUIWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path, "renderer accepts only %s/%s", comfyUIWorkloadModuleID, comfyUIWorkloadUnitID)
	}
	if unit.Kind() != contract.Kind || unit.RendererRef() != contract.RendererRef ||
		unit.TemplateRef() != contract.TemplateRef || unit.Version() != contract.Version ||
		unit.ContractHash() != contract.ContractHash {
		return selectedPaaSWorkloadBundle{}, fail(ErrOutputChanged, path, "render-unit implementation identity differs from the registered ComfyUI workload contract")
	}
	engine, hasEngine := unit.RuntimeEngine()
	imageRef, hasImage := unit.ContainerImageRef()
	imageDigest, hasDigest := unit.ContainerImageDigest()
	entry, hasEntry := unit.RuntimeEntryComponentRef()
	if unit.RuntimeKind() != "container" || unit.RuntimeDelivery() != "application-adapter" ||
		!hasEngine || engine != "docker" || !hasImage || imageRef != comfyuiImageRef ||
		!hasDigest || imageDigest != comfyuiImageDigest || !hasEntry || entry != comfyUIWorkloadUnitID {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".runtime", "runtime identity must match the exact ComfyUI "+comfyuiRelease+" contract")
	}
	siteRef, hasSite := unit.SiteRef()
	nodeRef, hasNode := unit.NodeRef()
	if unit.InstanceScope() != "node-local" || !hasSite || !hasNode ||
		!exactStringList(unit.LogicalSiteRefs(), []string{siteRef}) ||
		!exactStringList(unit.LogicalNodeRefs(), []string{nodeRef}) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "ComfyUI requires one exact node-local target")
	}
	_, hasDaemonRef := unit.DaemonRef()
	_, hasDaemonInstance := unit.DaemonInstanceRef()
	_, hasDaemonEngine := unit.DaemonEngine()
	_, hasDaemonSocket := unit.DaemonSocketPath()
	if hasDaemonRef || hasDaemonInstance || hasDaemonEngine || hasDaemonSocket {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".instances", "selected-PaaS workload receives no daemon or socket authority")
	}
	deliveryRoute, err := validateApplicationDeliveryRouteInput(unit, comfyUIWorkloadModuleID, comfyUIWorkloadRef, comfyUIPort, path+".inputs")
	if err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	if deliveryRoute != nil && deliveryRoute.Exposure == "public" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "ComfyUI has no sign-in of its own and is never published")
	}
	if !emptyJSONObject(unit.SecretRefsJSON()) || len(unit.SecretInputRefs()) != 0 ||
		!emptyJSONArray(unit.ProvidedInterfacesJSON()) || !emptyJSONArray(unit.RequiredInterfacesJSON()) ||
		!emptyJSONArray(unit.PrivilegedInterfaceApprovalsJSON()) || !emptyJSONArray(unit.RuntimeNetworkBindingsJSON()) {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".inputs", "ComfyUI bundle accepts no secret, free input or host authority")
	}
	var placement struct {
		Scope       string `json:"scope"`
		Cardinality string `json:"cardinality"`
	}
	if err := decodeStrict(unit.PlacementJSON(), &placement); err != nil ||
		placement.Scope != "node-local" || placement.Cardinality != "one-per-node" {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".placement", "requires exact node-local/one-per-node placement")
	}
	if outputs := unit.DeclaredOutputs(); len(outputs) != 1 || outputs[0] != comfyUIWorkloadOutputRef {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".outputs", "requires exactly output %q", comfyUIWorkloadOutputRef)
	}
	var components []selectedPaaSRuntimeComponent
	if err := decodeStrict(unit.RuntimeComponentsJSON(), &components); err != nil {
		return selectedPaaSWorkloadBundle{}, wrap(ErrInvalidPlan, path+".runtime.components", "decode closed component graph", err)
	}
	// Without a selected accelerator profile ComfyUI runs on the CPU (owner
	// decision 2026-09-27: the core AI components run on every supported
	// host). Image work is slow there; the model presets gate what fits.
	accelerator, selected := unit.ModuleAccelerator()
	if selected {
		components, err = applyModuleAccelerator(comfyUIWorkloadModuleID, components, accelerator, path+".acceleratorProfile")
		if err != nil {
			return selectedPaaSWorkloadBundle{}, err
		}
	}
	if !selected && len(components) == 1 {
		components[0].Command = append(slices.Clone(components[0].Command), comfyUICPUFlag)
	}
	if err := validateComfyUIRuntimeComponents(components, path+".runtime.components"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	var endpoints []selectedPaaSServiceEndpoint
	if err := decodeStrict(unit.ServiceEndpointsJSON(), &endpoints); err != nil || len(endpoints) != 1 {
		return selectedPaaSWorkloadBundle{}, fail(ErrInvalidPlan, path+".serviceEndpoints", "requires one exact ComfyUI endpoint")
	}
	if err := validateComfyUIServiceEndpoint(endpoints[0], path+".serviceEndpoints"); err != nil {
		return selectedPaaSWorkloadBundle{}, err
	}
	bundle := selectedPaaSWorkloadBundle{
		APIVersion: "stackkit.workload-bundle/v2", Kind: "ComfyUIWorkloadBundle",
		SecretRefs: map[string]string{}, Components: components, Route: endpoints[0], DeliveryRoute: deliveryRoute,
		ConfigFiles: comfyUIConfigFiles(),
	}
	bundle.Workload.Ref, bundle.Workload.AlternativeRef = comfyUIWorkloadRef, "comfyui"
	bundle.Workload.ModuleRef, bundle.Workload.Release = comfyUIWorkloadModuleID, comfyuiRelease
	bundle.Workload.Delivery, bundle.Workload.EntryComponent = "application-adapter", entry
	bundle.Target.SiteRef, bundle.Target.NodeRef, bundle.Target.InstanceRef = siteRef, nodeRef, unit.InstanceID()
	bundle.Ownership.ExecutionAdapter = "selected-application-adapter"
	bundle.Ownership.ProviderLifecycle = "not-owned"
	bundle.Ownership.Credentials = "opaque-references-only"
	return bundle, nil
}

// comfyUIComponent is the one governed ComfyUI component without its GPU
// grant. Its volumes follow the catalog's storage allocation: workflows,
// uploads and outputs are backed up; models are re-downloadable.
func comfyUIComponent() (selectedPaaSRuntimeComponent, error) {
	var command []string
	if err := json.Unmarshal([]byte(comfyuiCommandJSON), &command); err != nil {
		return selectedPaaSRuntimeComponent{}, err
	}
	return selectedPaaSRuntimeComponent{
		ID: "comfyui", Role: "application", Lifecycle: "daemon", Egress: true,
		Image:     selectedPaaSRuntimeImage{Ref: comfyuiImageRef, Digest: comfyuiImageDigest},
		DependsOn: []string{}, NetworkRefs: []string{"comfyui-internal"},
		Command: command,
		Volumes: []selectedPaaSRuntimeVolume{
			{ID: "user", Target: "/opt/ComfyUI/user", Class: "persistent", Backup: true},
			{ID: "input", Target: "/opt/ComfyUI/input", Class: "persistent", Backup: true},
			{ID: "output", Target: "/opt/ComfyUI/output", Class: "persistent", Backup: true},
			{ID: "models", Target: "/opt/ComfyUI/models", Class: "persistent"},
		},
		Health:       selectedPaaSRuntimeHealth{Kind: "http", Path: "/system_stats", Port: comfyUIPort},
		Resources:    &selectedPaaSRuntimeLimits{MemoryLimit: "28g", MemoryReservation: "2g"},
		PeerNetworks: []selectedPaaSPeerNetwork{{WorkloadRef: "ai", NetworkRef: "private-ai-internal"}},
	}, nil
}

// validateComfyUIRuntimeComponents admits exactly the governed component,
// either without a GPU grant (CPU runtime) or with its NVIDIA grant; nothing
// else reaches the container.
func validateComfyUIRuntimeComponents(components []selectedPaaSRuntimeComponent, path string) error {
	expected, err := comfyUIComponent()
	if err != nil {
		return wrap(ErrInvalidPlan, path, "decode governed ComfyUI command", err)
	}
	if len(components) != 1 {
		return fail(ErrInvalidPlan, path, "ComfyUI runtime graph differs from the closed "+comfyuiRelease+" contract")
	}
	actual := components[0]
	grant := actual.Accelerator
	if grant != nil && (grant.Vendor != "nvidia" || grant.Access != "cdi" || grant.Profile == "") {
		return fail(ErrInvalidPlan, path+".accelerator", "ComfyUI admits only its NVIDIA accelerator grant or none (CPU)")
	}
	if err := validatePeerNetworks(comfyUIWorkloadModuleID, actual, path); err != nil {
		return err
	}
	if grant == nil {
		// The CPU runtime is the governed command plus exactly --cpu.
		expected.Command = append(expected.Command, comfyUICPUFlag)
	}
	actual.Accelerator = nil
	// The plan orders volumes by its own rule; the set is what is governed.
	byID := func(a, b selectedPaaSRuntimeVolume) int { return strings.Compare(a.ID, b.ID) }
	actual.Volumes = slices.SortedFunc(slices.Values(actual.Volumes), byID)
	expected.Volumes = slices.SortedFunc(slices.Values(expected.Volumes), byID)
	if !reflect.DeepEqual(actual, expected) {
		return fail(ErrInvalidPlan, path, "ComfyUI runtime graph differs from the closed "+comfyuiRelease+" contract")
	}
	return nil
}

func validateComfyUIServiceEndpoint(endpoint selectedPaaSServiceEndpoint, path string) error {
	if endpoint.ServiceRef != comfyUIWorkloadRef || endpoint.UpstreamProtocol != "http" || endpoint.TargetPort != comfyUIPort ||
		endpoint.RequiredPrivilege != "user" || endpoint.OriginSelector != "control-authority-site" ||
		endpoint.HealthRef != "comfyui-http" || endpoint.IngressAuth != "forward-auth" ||
		endpoint.Data.BindingRef != comfyUIWorkloadRef || endpoint.Data.Locality != "primary-site" ||
		!exactStringList(endpoint.Data.RequiredClasses, []string{"personal"}) ||
		!exactStringList(endpoint.AllowedIngressProtocols, []string{"https"}) ||
		!sameStringSet(endpoint.AllowedExposures, []string{"local", "remote-private"}) {
		return fail(ErrInvalidPlan, path, "route authority differs from the governed private ComfyUI endpoint")
	}
	return nil
}

func validateComfyUIRoute(route applicationDeliveryRoute, path string) error {
	if err := validateParsedApplicationDeliveryRoute(route, comfyUIWorkloadModuleID, comfyUIWorkloadRef, comfyUIPort, path); err != nil {
		return err
	}
	if route.Exposure == "public" {
		return fail(ErrInvalidPlan, path, "ComfyUI has no sign-in of its own and is never published")
	}
	return nil
}
