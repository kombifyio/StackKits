package nativehost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kombifyio/stackkits/contracts/ownapplication"
	"github.com/kombifyio/stackkits/contracts/runtimeexecutor"
	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/ownappfirewall"
	"github.com/kombifyio/stackkits/pkg/workloadremoval"
)

func composeComponentImage(c architecturev2renderer.ApplicationDeliveryComponentDescriptor) string {
	if c.ImageID != "" {
		return c.ImageID
	}
	if c.ImageDigest == "" {
		return c.ImageRef
	}
	return c.ImageRef + "@" + c.ImageDigest
}
func (o *osStandaloneComposeWorkloadOperations) workloadSecretMaterial(ref string) ([]byte, error) {
	if o.preview {
		return []byte("<custody-reference>"), nil
	}
	return localevidence.ResolveLocalSecretMaterial(o.workspaceRoot, ref)
}

// RenderOwnApplication uses the catalog Compose renderer without reading
// custody, creating files, resolving a tag or contacting Docker.
func RenderOwnApplication(b ownapplication.Bundle) ([]byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	descriptor, err := architecturev2renderer.OwnApplicationDescriptor(raw)
	if err != nil {
		return nil, err
	}
	o := osStandaloneComposeWorkloadOperations{preview: true}
	compose, _, _, err := o.render(descriptor)
	return compose, err
}

// OwnApplicationRuntime supplies a bounded catalog-shaped input to the
// existing native Compose operations. It owns no alternative executor.
type OwnApplicationRuntime struct {
	ops *osStandaloneComposeWorkloadOperations
}

func NewOwnApplicationRuntime(workspace string) (*OwnApplicationRuntime, error) {
	ops, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return nil, err
	}
	return &OwnApplicationRuntime{ops: ops.(*osStandaloneComposeWorkloadOperations)}, nil
}
func validOwnApplicationImageQuery(args []string) bool {
	return len(args) == 5 && args[0] == "image" && args[1] == "inspect" && args[2] == "--format" && args[3] == "{{json .Config}}" && ownLocalImageID(args[4])
}
func ownLocalImageID(s string) bool {
	return len(s) == 71 && strings.HasPrefix(s, "sha256:") && strings.Trim(s[7:], "0123456789abcdef") == ""
}
func OwnApplicationDeployment(b ownapplication.Bundle) (SelectedPaaSWorkloadDeployment, error) {
	if !b.ImagesResolved() {
		if b.Application.IsProject() {
			return SelectedPaaSWorkloadDeployment{}, errors.New("execution requires an observed pinned image for every service")
		}
		return SelectedPaaSWorkloadDeployment{}, errors.New("execution requires an observed pinned image")
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return SelectedPaaSWorkloadDeployment{}, err
	}
	d, err := architecturev2renderer.OwnApplicationDescriptor(raw)
	if err != nil {
		return SelectedPaaSWorkloadDeployment{}, err
	}
	sum := sha256.Sum256(raw)
	return SelectedPaaSWorkloadDeployment{WorkloadRef: d.WorkloadRef, ModuleRef: d.ModuleRef, UnitRef: d.WorkloadRef, Release: d.Release, SiteRef: d.SiteRef, NodeRef: d.NodeRef, InstanceRef: d.InstanceRef, ArtifactRef: "own-applications/" + d.WorkloadRef + "/bundle.json", ArtifactDigest: "sha256:" + hex.EncodeToString(sum[:]), Bundle: raw, Route: d.Route, RuntimeAdapter: runtimeexecutor.RuntimeAdapterBinding{ID: standaloneComposeAdapterRef, ModuleRef: standaloneComposeModuleRef}}, nil
}

// ownApplicationImagesAdmitted requires the securely imported local identity of
// every container of the own application.
func ownApplicationImagesAdmitted(b ownapplication.Bundle) error {
	if b.Application.IsProject() {
		for _, s := range b.Application.Services {
			if !ownLocalImageID(b.ImageIDs[s.Name]) {
				return errors.New("execution requires securely imported local image identity for every service")
			}
		}
		return nil
	}
	if !ownLocalImageID(b.ImageID) {
		return errors.New("execution requires securely imported local image identity")
	}
	return nil
}

// ownApplicationServiceNames lists the Compose services of the own
// application: the single container of v1 and v2, every service of a project.
func ownApplicationServiceNames(bundle architecturev2renderer.ApplicationDeliveryBundleDescriptor) []string {
	names := make([]string, 0, len(bundle.Components))
	for _, component := range bundle.Components {
		names = append(names, component.ID)
	}
	if len(names) == 0 {
		return []string{"app"}
	}
	return names
}
func (r *OwnApplicationRuntime) Apply(ctx context.Context, b ownapplication.Bundle) error {
	if err := ownApplicationImagesAdmitted(b); err != nil {
		return err
	}
	d, err := OwnApplicationDeployment(b)
	if err != nil {
		return err
	}
	if err = r.admitImageConfiguration(ctx, b); err != nil {
		return err
	}
	project, err := r.ops.prepareWithIdentityMutation(ctx, d, false)
	if err != nil {
		return err
	}

	remover := standaloneComposeRemover{ops: r.ops}
	owned, _ := standaloneComposeVolumeDeclarations(project.bundle)
	for _, volume := range owned {
		name := project.name + "_" + volume.Key
		present, e := remover.volumeNamePresent(ctx, name)
		if e != nil {
			return e
		}
		if present {
			observed, e := remover.inspectVolume(ctx, name)
			if e != nil {
				return e
			}
			if e = verifyAdmittedLocalVolume(project, volume.Key, observed); e != nil {
				return e
			}
		}
	}
	_, err = r.ops.ApplyWorkload(ctx, d)
	return err
}
func (r *OwnApplicationRuntime) Read(ctx context.Context, b ownapplication.Bundle, logs bool) (json.RawMessage, error) {
	d, err := OwnApplicationDeployment(b)
	if err != nil {
		return nil, err
	}
	p, err := r.ops.prepareWithIdentityMutation(ctx, d, false)
	if err != nil {
		return nil, err
	}
	if err = r.ops.verifyPersisted(p); err != nil {
		return nil, err
	}
	args := standaloneComposeArgs(p, "ps")
	services := ownApplicationServiceNames(p.bundle)
	if logs {
		args = append(standaloneComposeArgs(p, "ps")[:7], append([]string{"logs", "--no-color", "--timestamps", "--tail", "100"}, services...)...)
	}
	raw, err := r.ops.runner.Run(ctx, args, p.directory)
	if err != nil {
		return nil, errors.New("own application runtime observation failed")
	}
	if logs {
		text := string(raw)
		for _, ref := range b.Application.SecretRefList() {
			material, e := localevidence.ResolveLocalSecretMaterial(r.ops.workspaceRoot, ref)
			if e != nil {
				return nil, e
			}
			if len(material) > 0 {
				text = strings.ReplaceAll(text, string(material), "[REDACTED]")
			}
			clear(material)
		}
		return json.Marshal(map[string]any{"text": logging.RedactText(text), "limit": 100 * len(services)})
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil {
		return nil, err
	}
	if _, err = standaloneComposeContainerIdentities(p.bundle.Components, statuses, true); err != nil {
		return nil, err
	}
	if err = validateStandaloneComposeRouteReadback(statuses[p.bundle.EntryComponent], p.bundle.Route, r.ops.originServes(p.bundle.Route)); err != nil {
		return nil, err
	}
	return json.Marshal(statuses)
}
func (r *OwnApplicationRuntime) Control(ctx context.Context, b ownapplication.Bundle, action string, deleteData bool, generation string) error {
	d, err := OwnApplicationDeployment(b)
	if err != nil {
		return err
	}
	r.ops.preview = true // Prepare the exact scrubbed recipe without resolving secrets.
	defer func() { r.ops.preview = false }()
	p, err := r.ops.prepareWithIdentityMutation(ctx, d, false)
	if err != nil {
		return err
	}

	// Accept either the exact custody-backed recipe or the exact scrubbed recipe
	// left by an interrupted remove; never invoke Compose with edited disk bytes.
	if err = r.ops.verifyPersisted(p); err != nil {
		r.ops.preview = false
		live, prepareErr := r.ops.prepareWithIdentityMutation(ctx, d, false)
		r.ops.preview = true
		if prepareErr != nil {
			return prepareErr
		}
		if err = r.ops.verifyPersisted(live); err != nil {
			return err
		}
	}

	remover := standaloneComposeRemover{ops: r.ops}
	statuses, err := remover.readComposeStatuses(ctx, p)
	if err != nil {
		return err
	}
	present, err := remover.inspectPresentContainers(ctx, p, statuses)
	if err != nil {
		return err
	}
	var volumes map[string]dockerVolumeInspect
	if action == "remove" && b.Application.HasVolume() {
		owned, hostPaths := standaloneComposeVolumeDeclarations(p.bundle)
		if len(hostPaths) != 0 {
			return errors.New("own application may not own host paths")
		}
		disposition := workloadremoval.DataDispositionRetain
		if deleteData {
			disposition = workloadremoval.DataDispositionDelete
		}
		// Reuse the remover's durable signed pre-removal custody. This survives
		// container removal and retries; each create generation has its own key.
		request := workloadremoval.Request{WorkloadRef: b.Application.WorkloadRef(), AppliedRequestDigest: generation, DataDisposition: disposition, Applied: runtimeexecutor.ExecutionRequest{RuntimeTargets: []runtimeexecutor.RuntimeTarget{{InstanceRef: d.InstanceRef}}}}
		volumes, err = remover.admitOwnedVolumes(ctx, request, p, owned, present)
		if err != nil {
			return err
		}
	}

	if b.Application.Firewalled() {
		if err = ownappfirewall.StopWatcher(ctx, ownApplicationNetworkName(p.bundle.WorkloadRef, p.bundle.NodeRef), action == "remove"); err != nil {
			return err
		}
	}
	// Close router peering before teardown; a failed operation remains pending
	// in signed custody and is safely repeatable on a new reviewed request.
	if err = r.ops.detachOwnRouters(ctx, ownApplicationNetworkName(p.bundle.WorkloadRef, p.bundle.NodeRef), false); err != nil {
		return err
	}
	switch action {
	case "stop":
		if _, err = r.ops.runner.Run(ctx, standaloneComposeRemovalArgs(p, append([]string{"stop", "--timeout", "30"}, ownApplicationServiceNames(p.bundle)...)...), p.directory); err != nil {
			return err
		}
		statuses, err = remover.readComposeStatuses(ctx, p)
		if err != nil {
			return err
		}
		if _, err = remover.inspectPresentContainers(ctx, p, statuses); err != nil {
			return err
		}
		for _, status := range statuses {
			if status.State != "exited" && status.State != "dead" && status.State != "created" {
				return errors.New("own application stop has no stopped observation")
			}
		}
	case "remove":
		// The existing remover targets admitted container IDs and checks each
		// workspace/project/image binding. It never performs orphan cleanup.
		if err = remover.stopAndRemoveContainers(ctx, present); err != nil {
			return err
		}
		if err = remover.verifyAdmittedContainersAbsent(ctx, p, present); err != nil {
			return err
		}
		if deleteData {
			if err = remover.deleteOwnedVolumes(ctx, p, volumes); err != nil {
				return err
			}
		}
		if err = r.ops.detachOwnRouters(ctx, ownApplicationNetworkName(p.bundle.WorkloadRef, p.bundle.NodeRef), true); err != nil {
			return err
		}
		if b.Application.Firewalled() {
			if err = ownappfirewall.Remove(ctx, ownApplicationNetworkName(p.bundle.WorkloadRef, p.bundle.NodeRef)); err != nil {
				return err
			}
		}
		// Erase material from the owner-only runtime recipe as well as custody.
		return r.ops.persist(p)
	default:
		return fmt.Errorf("unknown own application action %s", action)
	}
	return nil
}

func ownApplicationRouteLabels(route architecturev2renderer.ApplicationDeliveryRouteDescriptor, originServed bool) map[string]string {
	labels := map[string]string{"traefik.http.routers.stackkit-" + route.ServiceRef + ".middlewares": "stackkit-access-gate-lan@docker"}
	if route.IngressAuth != "forward-auth" {
		return labels
	}
	id := "stackkit-" + route.ServiceRef + "-gate"
	rule := "Host(`" + route.Host + "`) && PathPrefix(`/.well-known/stackkit/`)"
	labels["traefik.http.routers."+id+".rule"] = rule
	labels["traefik.http.routers."+id+".priority"] = "2000"
	labels["traefik.http.routers."+id+".entrypoints"] = "websecure"
	labels["traefik.http.routers."+id+".tls"] = "true"
	if !originServed {
		labels["traefik.http.routers."+id+".tls.certresolver"] = "stackkits"
	}
	labels["traefik.http.routers."+id+".middlewares"] = "stackkit-access-gate-lan@docker"
	labels["traefik.http.routers."+id+".service"] = "stackkit-gate@docker"
	if route.Exposure == "public" || route.Exposure == "remote-private" {
		labels["traefik.http.routers."+id+"-remote.rule"] = rule
		labels["traefik.http.routers."+id+"-remote.priority"] = "2000"
		labels["traefik.http.routers."+id+"-remote.entrypoints"] = "remote"
		labels["traefik.http.routers."+id+"-remote.service"] = "stackkit-gate@docker"
	}
	return labels
}

func (r *OwnApplicationRuntime) admitImageConfiguration(ctx context.Context, b ownapplication.Bundle) error {
	if b.Application.IsProject() {
		for _, s := range b.Application.Services {
			metadata, err := r.ops.runner.Run(ctx, []string{"image", "inspect", "--format", "{{json .Config}}", b.ImageIDs[s.Name]}, r.ops.workspaceRoot)
			if err != nil {
				return errors.New("own application image configuration observation failed")
			}
			if err = s.AdmitImageConfiguration(metadata); err != nil {
				return err
			}
		}
		return nil
	}
	metadata, err := r.ops.runner.Run(ctx, []string{"image", "inspect", "--format", "{{json .Config}}", b.ImageID}, r.ops.workspaceRoot)
	if err != nil {
		return errors.New("own application image configuration observation failed")
	}
	return b.Application.AdmitImageConfiguration(metadata)
}

// ImageVersionLabel reads the org.opencontainers.image.version label of the
// admitted local image of a one-container own application. A digest-pinned
// adopted image has no tag, so a tag update compares against this label; an
// image without it returns "".
func (r *OwnApplicationRuntime) ImageVersionLabel(ctx context.Context, b ownapplication.Bundle) (string, error) {
	if b.Application.IsProject() || !ownLocalImageID(b.ImageID) {
		return "", errors.New("image version label requires the admitted local image of a one-container application")
	}
	metadata, err := r.ops.runner.Run(ctx, []string{"image", "inspect", "--format", "{{json .Config}}", b.ImageID}, r.ops.workspaceRoot)
	if err != nil {
		return "", errors.New("own application image configuration observation failed")
	}
	var config struct {
		Labels map[string]string `json:"Labels"`
	}
	if err = json.Unmarshal(metadata, &config); err != nil {
		return "", errors.New("own application image configuration is malformed")
	}
	return strings.TrimSpace(config.Labels["org.opencontainers.image.version"]), nil
}

func (r *OwnApplicationRuntime) FirewallReceipt() *ownappfirewall.Receipt {
	return r.ops.ownFirewallReceipt
}

func (o *osStandaloneComposeWorkloadOperations) removeFailedOwnStart(ctx context.Context, p standaloneComposeProject) error {
	ctx, cancel := context.WithTimeout(ctx, standaloneComposeApplyBudget)
	defer cancel()
	if err := ownappfirewall.StopWatcher(ctx, ownApplicationNetworkName(p.bundle.WorkloadRef, p.bundle.NodeRef), false); err != nil {
		return err
	}
	remover := standaloneComposeRemover{ops: o}
	statuses, err := remover.readComposeStatuses(ctx, p)
	if err != nil {
		return err
	}
	present, err := remover.inspectPresentContainers(ctx, p, statuses)
	if err != nil {
		return err
	}
	if err = remover.stopAndRemoveContainers(ctx, present); err != nil {
		return err
	}
	return remover.verifyAdmittedContainersAbsent(ctx, p, present)
}

// ReconcilePendingTeardown is used only for a newly reviewed reconciliation of
// signed pending custody. It reconstructs its exact scrubbed recipe after an
// interrupted prepare, before the remover observes and verifies container IDs.
// Normal stop/remove never repairs a mismatched on-disk recipe this way.
func (r *OwnApplicationRuntime) ReconcilePendingTeardown(ctx context.Context, b ownapplication.Bundle) error {
	d, err := OwnApplicationDeployment(b)
	if err != nil {
		return err
	}
	r.ops.preview = true
	defer func() { r.ops.preview = false }()
	p, err := r.ops.prepareWithIdentityMutation(ctx, d, false)
	if err != nil {
		return err
	}
	return r.ops.persist(p)
}

// ExpectFirewallRuleset binds native apply to the concrete host facts shown in
// review, including connected prefixes, gateways, next hops and uplink source.
func (r *OwnApplicationRuntime) ExpectFirewallRuleset(digest string) {
	r.ops.ownFirewallRuleset = digest
}
func (o *osStandaloneComposeWorkloadOperations) startWorkload(ctx context.Context, p standaloneComposeProject) error {
	if p.firewall == nil {
		_, err := o.runner.Run(ctx, standaloneComposeArgs(p, "up"), p.directory)
		return err
	}
	raw, err := o.runner.Run(ctx, standaloneComposeArgs(p, "ps"), p.directory)
	if err != nil {
		return err
	}
	statuses, err := parseStandaloneComposeStatuses(raw)
	if err != nil {
		return err
	}
	status, ok := statuses[p.bundle.EntryComponent]
	if !ok {
		return errors.New("own firewall: exact stopped app container unavailable")
	}
	return p.firewall.StartWatcher(ctx, status.ID)
}
