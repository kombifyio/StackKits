package restoreactivation

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/resolvedplan"
	"slices"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/generationartifact"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// ApplicationTransferProjection selects the complete native graph from verified
// generation bundles, independently of persistent backup volume membership.
func ApplicationTransferProjection(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, selected []string, policy ...localbackuppolicy.Policy) ([]localbackuppolicy.ApplicationRuntime, error) {
	bindings, bundles, err := applicationTransferBundles(workspace, plan, manifest, selected)
	if err != nil {
		return nil, err
	}
	if err = requireApplicationTransferPeerClosure(workspace, plan, manifest, bindings, selected); err != nil {
		return nil, err
	}
	if len(policy) > 1 {
		return nil, errors.New("ambiguous transfer persistent policy")
	}
	if len(policy) == 1 {
		if _, err := localbackuppolicy.ArtifactBytes(policy[0]); err != nil {
			return nil, err
		}
		for ref, bundle := range bundles {
			for _, c := range bundle.Components {
				for _, v := range c.Volumes {
					if v.HostPath != "" {
						return nil, errors.New("transfer does not import host device or bind-mounted application data")
					}
					if v.Class != "persistent" || v.SharedFromComponent != "" {
						continue
					}
					if !slices.ContainsFunc(policy[0].Source.ApplicationVolumes, func(p localbackuppolicy.ApplicationVolume) bool {
						return p.WorkloadRef == ref && p.ComponentRef == c.ID && p.VolumeRef == v.ID && p.Target == v.Target
					}) {
						return nil, errors.New("selected runtime declares persistent data outside governed transfer sources")
					}
				}
			}
		}
	}
	graphs := make([]localbackuppolicy.ApplicationRuntime, 0, len(selected))
	for _, binding := range bindings {
		bundle := bundles[binding.WorkloadRef]
		graph := localbackuppolicy.ApplicationRuntime{WorkloadRef: binding.WorkloadRef, SiteRef: binding.SiteRef, NodeRef: binding.NodeRef, ComposeProject: localbackuppolicy.StandaloneComposeProjectName(binding.WorkloadRef, binding.NodeRef)}
		for _, c := range bundle.Components {
			health := c.HealthFailure
			if health == "blocking" {
				health = ""
			}
			graph.Components = append(graph.Components, localbackuppolicy.ApplicationRuntimeComponent{ComponentRef: c.ID, Role: c.Role, Lifecycle: c.Lifecycle, HealthFailure: health, ImageRef: c.ImageRef, ImageDigest: c.ImageDigest, DependsOn: slices.Clone(c.DependsOn)})
		}
		graphs = append(graphs, graph)
	}
	return localbackuppolicy.CanonicalApplicationTransferRuntimes(graphs)
}

func applicationTransferBundles(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, selected []string) ([]standaloneComposeApplicationBinding, map[string]architecturev2renderer.ApplicationDeliveryBundleDescriptor, error) {
	if manifest.Binding != plan.Binding() {
		return nil, nil, errors.New("transfer bundle generation differs from Plan")
	}
	var doc map[string]any
	if err := json.Unmarshal(plan.Canonical(), &doc); err != nil {
		return nil, nil, err
	}
	workloads, err := array(doc, "workloads")
	if err != nil {
		return nil, nil, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Close()
	bindings := []standaloneComposeApplicationBinding{}
	bundles := map[string]architecturev2renderer.ApplicationDeliveryBundleDescriptor{}
	for _, raw := range workloads {
		w, ok := raw.(map[string]any)
		if !ok {
			return nil, nil, errors.New("invalid workload")
		}
		ref := text(w, "id")
		if !slices.Contains(selected, ref) {
			continue
		}
		alt, e := object(w, "alternative")
		if e != nil {
			return nil, nil, e
		}
		runtime, e := object(alt, "runtime")
		if e != nil || text(runtime, "delivery") != "application-adapter" {
			return nil, nil, errors.New("transfer requires native application delivery")
		}
		adapter, e := object(runtime, "adapter")
		if e != nil || text(adapter, "id") != "standalone-compose" {
			return nil, nil, errors.New("transfer requires native standalone-compose adapter")
		}
		sites, e := stringArray(w, "siteRefs")
		if e != nil || len(sites) != 1 {
			return nil, nil, errors.New("transfer requires exact site")
		}
		nodes, e := stringArray(w, "nodeRefs")
		if e != nil || len(nodes) != 1 {
			return nil, nil, errors.New("transfer requires exact node")
		}
		b := standaloneComposeApplicationBinding{WorkloadRef: ref, BundleWorkloadRef: text(alt, "id"), ModuleRef: text(alt, "moduleRef"), SiteRef: sites[0], NodeRef: nodes[0]}
		artifact, e := standaloneComposeBundleArtifact(doc, plan.OutputRoot(), manifest, b)
		if e != nil {
			return nil, nil, e
		}
		data, _, e := tx.ReadStable(artifact.Path)
		if e != nil {
			return nil, nil, e
		}
		if runtimeFileDigest(data) != artifact.SHA256 {
			return nil, nil, errors.New("transfer bundle differs from generation manifest")
		}
		bundle, e := architecturev2renderer.ParseApplicationDeliveryWorkloadBundle(data)
		if e != nil {
			return nil, nil, e
		}
		if bundle.WorkloadRef != ref || bundle.SiteRef != b.SiteRef || bundle.NodeRef != b.NodeRef || bundle.ModuleRef != b.ModuleRef {
			return nil, nil, errors.New("transfer bundle placement differs from Plan")
		}
		if _, exists := bundles[ref]; exists {
			return nil, nil, errors.New("duplicate transfer workload")
		}
		bindings = append(bindings, b)
		bundles[ref] = bundle
	}
	if len(bindings) != len(selected) {
		return nil, nil, errors.New("transfer selected workload closure is incomplete")
	}
	return bindings, bundles, nil
}

// ApplicationTransferRuntimeCustody binds even stateless companions to their
// exact persisted Compose/config/environment custody using the existing binder.
func ApplicationTransferRuntimeCustody(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, selected []string) ([]StandaloneComposeRuntimeCustody, error) {
	graphs, err := ApplicationTransferProjection(workspace, plan, manifest, selected)
	if err != nil {
		return nil, err
	}
	bindings, bundles, err := applicationTransferBundles(workspace, plan, manifest, selected)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = json.Unmarshal(plan.Canonical(), &doc); err != nil {
		return nil, err
	}
	result := []StandaloneComposeRuntimeCustody{}
	for _, b := range bindings {
		index := slices.IndexFunc(graphs, func(g localbackuppolicy.ApplicationRuntime) bool { return g.WorkloadRef == b.WorkloadRef })
		g := graphs[index]
		logical := []string{}
		for _, c := range bundles[b.WorkloadRef].Components {
			for _, v := range c.Volumes {
				if v.HostPath != "" || v.Class != "persistent" || v.SharedFromComponent != "" {
					continue
				}
				logical = append(logical, localbackuppolicy.StandaloneComposeLogicalVolumeName(c.ID, v.ID))
			}
		}
		custody, e := bindStandaloneComposeCustody(workspace, g.ComposeProject, logical, g)
		if e != nil {
			return nil, e
		}
		b.Runtime = custody.Runtime
		b.Custody = custody
		bundle, files, e := bindStandaloneComposeConfigCustody(workspace, doc, plan.OutputRoot(), manifest, b)
		if e != nil {
			return nil, e
		}
		custody.Bundle = bundle
		custody.ConfigFiles = files
		result = append(result, custody)
	}
	return result, nil
}

// ApplicationTransferPersistentWorkloads never invents an empty data backup.
func ApplicationTransferPersistentWorkloads(policy localbackuppolicy.Policy, selected []string) ([]string, error) {
	refs := []string{}
	for _, v := range policy.Source.ApplicationVolumes {
		if slices.Contains(selected, v.WorkloadRef) && !slices.Contains(refs, v.WorkloadRef) {
			refs = append(refs, v.WorkloadRef)
		}
	}
	slices.Sort(refs)
	if len(refs) == 0 {
		return nil, errors.New("all-stateless transfer has no persistent snapshot source; runtime-only retirement is not yet supported")
	}
	return refs, nil
}

// ApplicationTransferConfiguration projects existing compiler-owned public
// workload settings and effective module profile bindings. Placement, routes,
// Foundation identities and compiler-bound render values remain destination-local.
// Secret material is never a public setting and travels by exact sealed slots.
func ApplicationTransferConfiguration(plan generationartifact.VerifiedPlan, selected []string) (map[string]backuplifecycle.ApplicationTransferWorkloadConfiguration, error) {
	var doc map[string]any
	if err := json.Unmarshal(plan.Canonical(), &doc); err != nil {
		return nil, err
	}
	workloads, err := array(doc, "workloads")
	if err != nil {
		return nil, err
	}
	modules, err := array(doc, "modules")
	if err != nil {
		return nil, err
	}
	result := map[string]backuplifecycle.ApplicationTransferWorkloadConfiguration{}
	for _, raw := range workloads {
		w, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("invalid configuration workload")
		}
		ref := text(w, "id")
		if !slices.Contains(selected, ref) {
			continue
		}
		a, e := object(w, "alternative")
		if e != nil {
			return nil, e
		}
		settings, e := object(w, "settings")
		if e != nil {
			return nil, e
		}
		config := backuplifecycle.ApplicationTransferWorkloadConfiguration{AlternativeRef: text(a, "id"), ModuleRef: text(a, "moduleRef"), Settings: settings, Profiles: map[string]any{}}
		found := false
		for _, rm := range modules {
			m, ok := rm.(map[string]any)
			if !ok || text(m, "id") != config.ModuleRef {
				continue
			}
			if found {
				return nil, errors.New("ambiguous selected module configuration")
			}
			found = true
			for _, field := range []string{"computeProfile", "computeProfileHash", "computeProfileBinding", "storageProfile", "storageProfileHash", "storageProfileBinding", "acceleratorProfile", "acceleratorProfileHash", "acceleratorProfileBinding"} {
				if value, ok := m[field]; ok {
					config.Profiles[field] = value
				}
			}
		}
		if !found {
			return nil, errors.New("selected module configuration absent")
		}
		result[ref] = config
	}
	if len(result) != len(selected) {
		return nil, errors.New("selected configuration closure incomplete")
	}
	return result, nil
}
func RequireApplicationTransferConfiguration(t backuplifecycle.ApplicationTransfer, plan generationartifact.VerifiedPlan, selected []string) error {
	actual, err := ApplicationTransferConfiguration(plan, selected)
	if err != nil {
		return err
	}
	if len(actual) != len(t.PortableConfiguration) {
		return errors.New("portable application configuration closure differs; configure receiving selected apps to the source public settings/profiles before transfer")
	}
	for ref, config := range actual {
		want, ok := t.PortableConfiguration[ref]
		if !ok {
			return fmt.Errorf("portable configuration of %s missing", ref)
		}
		gotDigest, e := resolvedplan.CanonicalSHA256(config)
		if e != nil {
			return e
		}
		wantDigest, e := resolvedplan.CanonicalSHA256(want)
		if e != nil {
			return e
		}
		if gotDigest != wantDigest {
			return fmt.Errorf("receiving workload %s differs from source configured settings/profiles; use use-cases configure with the signed portableConfiguration before issuing a new transfer admission", ref)
		}
	}
	return nil
}

// Separate workloads sharing an application runtime network form one transfer
// selection; neither a foreign writer nor a missing dependency is silently left.
func requireApplicationTransferPeerClosure(workspace string, plan generationartifact.VerifiedPlan, manifest generationartifact.ArtifactManifest, bindings []standaloneComposeApplicationBinding, selected []string) error {
	var doc map[string]any
	if err := json.Unmarshal(plan.Canonical(), &doc); err != nil {
		return err
	}
	workloads, err := array(doc, "workloads")
	if err != nil {
		return err
	}
	local := []string{}
	for _, raw := range workloads {
		w, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		a, e := object(w, "alternative")
		if e != nil {
			continue
		}
		runtime, e := object(a, "runtime")
		if e != nil {
			continue
		}
		adapter, e := object(runtime, "adapter")
		if e != nil || text(adapter, "id") != "standalone-compose" {
			continue
		}
		sites, e := stringArray(w, "siteRefs")
		if e != nil || len(sites) != 1 {
			continue
		}
		nodes, e := stringArray(w, "nodeRefs")
		if e != nil || len(nodes) != 1 {
			continue
		}
		if slices.ContainsFunc(bindings, func(b standaloneComposeApplicationBinding) bool {
			return b.SiteRef == sites[0] && b.NodeRef == nodes[0]
		}) {
			local = append(local, text(w, "id"))
		}
	}
	_, bundles, err := applicationTransferBundles(workspace, plan, manifest, local)
	if err != nil {
		return err
	}
	for ref, bundle := range bundles {
		for _, c := range bundle.Components {
			for _, peer := range c.PeerNetworks {
				if slices.Contains(selected, ref) != slices.Contains(selected, peer.WorkloadRef) {
					return fmt.Errorf("transfer selection is not closed: workloads %s and %s share an application network; select their complete use-case/workload closure", ref, peer.WorkloadRef)
				}
			}
		}
	}
	return nil
}
