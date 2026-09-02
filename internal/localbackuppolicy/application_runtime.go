package localbackuppolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ApplicationRuntime is the compiler-owned component graph for one exact
// Standalone-Compose workload placement. The graph is retained in the source
// policy so lifecycle consumers cannot infer stop order from Docker names or
// mutable container labels.
type ApplicationRuntime struct {
	WorkloadRef    string                        `json:"workloadRef"`
	SiteRef        string                        `json:"siteRef"`
	NodeRef        string                        `json:"nodeRef"`
	ComposeProject string                        `json:"composeProject"`
	Components     []ApplicationRuntimeComponent `json:"components"`
}

// ApplicationRuntimeComponent is one closed runtime graph node. ImageRef and
// ImageDigest are copied from the selected CUE runtime component; DependsOn
// is the only source used to derive quiescence order.
type ApplicationRuntimeComponent struct {
	ComponentRef string   `json:"componentRef"`
	Role         string   `json:"role"`
	Lifecycle    string   `json:"lifecycle"`
	ImageRef     string   `json:"imageRef"`
	ImageDigest  string   `json:"imageDigest"`
	DependsOn    []string `json:"dependsOn"`
}

func cloneApplicationRuntimes(runtimes []ApplicationRuntime) []ApplicationRuntime {
	if len(runtimes) == 0 {
		return nil
	}
	cloned := make([]ApplicationRuntime, len(runtimes))
	for index, runtime := range runtimes {
		cloned[index] = runtime
		cloned[index].Components = make([]ApplicationRuntimeComponent, len(runtime.Components))
		for componentIndex, component := range runtime.Components {
			cloned[index].Components[componentIndex] = component
			cloned[index].Components[componentIndex].DependsOn = append([]string{}, component.DependsOn...)
		}
	}
	return cloned
}

func applicationRuntimeKey(runtime ApplicationRuntime) string {
	return strings.Join([]string{runtime.WorkloadRef, runtime.SiteRef, runtime.NodeRef, runtime.ComposeProject}, "\x00")
}

func canonicalApplicationRuntimes(runtimes []ApplicationRuntime) ([]ApplicationRuntime, error) {
	canonical := cloneApplicationRuntimes(runtimes)
	if len(canonical) == 0 {
		return nil, nil
	}
	seenRuntimes := make(map[string]struct{}, len(canonical))
	for index := range canonical {
		runtime := &canonical[index]
		if err := validateApplicationRuntime(*runtime); err != nil {
			return nil, fmt.Errorf("application runtime %d: %w", index, err)
		}
		key := applicationRuntimeKey(*runtime)
		if _, exists := seenRuntimes[key]; exists {
			return nil, fmt.Errorf("application runtime %q is selected more than once", runtime.ComposeProject)
		}
		seenRuntimes[key] = struct{}{}
		seenComponents := make(map[string]struct{}, len(runtime.Components))
		for componentIndex := range runtime.Components {
			component := &runtime.Components[componentIndex]
			slices.Sort(component.DependsOn)
			for dependencyIndex := 1; dependencyIndex < len(component.DependsOn); dependencyIndex++ {
				if component.DependsOn[dependencyIndex] == component.DependsOn[dependencyIndex-1] {
					return nil, fmt.Errorf("application runtime %q repeats dependency %q", runtime.ComposeProject, component.DependsOn[dependencyIndex])
				}
			}
			if _, exists := seenComponents[component.ComponentRef]; exists {
				return nil, fmt.Errorf("application runtime %q repeats component %q", runtime.ComposeProject, component.ComponentRef)
			}
			seenComponents[component.ComponentRef] = struct{}{}
		}
		slices.SortFunc(runtime.Components, func(left, right ApplicationRuntimeComponent) int {
			return strings.Compare(left.ComponentRef, right.ComponentRef)
		})
	}
	slices.SortFunc(canonical, func(left, right ApplicationRuntime) int {
		return strings.Compare(applicationRuntimeKey(left), applicationRuntimeKey(right))
	})
	return canonical, nil
}

func validateApplicationRuntime(runtime ApplicationRuntime) error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "workloadRef", value: runtime.WorkloadRef},
		{name: "siteRef", value: runtime.SiteRef},
		{name: "nodeRef", value: runtime.NodeRef},
	} {
		if !applicationRefPattern.MatchString(field.value) {
			return fmt.Errorf("%s must be a non-empty portable contract ID", field.name)
		}
	}
	if !applicationVolumeNameRegex.MatchString(runtime.ComposeProject) {
		return fmt.Errorf("composeProject must use the portable Docker name subset")
	}
	if runtime.ComposeProject != StandaloneComposeProjectName(runtime.WorkloadRef, runtime.NodeRef) {
		return fmt.Errorf("composeProject does not match the Standalone-Compose workload and node")
	}
	if len(runtime.Components) == 0 {
		return fmt.Errorf("components must not be empty")
	}
	seen := make(map[string]struct{}, len(runtime.Components))
	for index, component := range runtime.Components {
		if !contractIDPattern.MatchString(component.ComponentRef) ||
			!validApplicationRuntimeRole(component.Role) ||
			!validApplicationRuntimeLifecycle(component.Lifecycle) {
			return fmt.Errorf("component %d has an invalid identity, role, or lifecycle", index)
		}
		if !contractIDPattern.MatchString(component.ImageRef) || strings.Contains(component.ImageRef, "@") ||
			!imageDigestPattern.MatchString(component.ImageDigest) {
			return fmt.Errorf("component %q has an unpinned image", component.ComponentRef)
		}
		seen[component.ComponentRef] = struct{}{}
	}
	for _, component := range runtime.Components {
		for _, dependency := range component.DependsOn {
			if _, exists := seen[dependency]; !exists {
				return fmt.Errorf("component %q depends on unknown component %q", component.ComponentRef, dependency)
			}
		}
	}
	if _, err := applicationRuntimeStartOrder(runtime); err != nil {
		return err
	}
	return nil
}

func validApplicationRuntimeRole(role string) bool {
	switch role {
	case "application", "machine-learning", "database", "cache", "database-init":
		return true
	default:
		return false
	}
}

func validApplicationRuntimeLifecycle(lifecycle string) bool {
	switch lifecycle {
	case "daemon", "one-shot":
		return true
	default:
		return false
	}
}

// ApplicationRuntimesForTarget narrows the compiler aggregate to one exact
// node-local placement while preserving the canonical graph encoding.
func ApplicationRuntimesForTarget(runtimes []ApplicationRuntime, siteRef, nodeRef string) ([]ApplicationRuntime, error) {
	canonical, err := canonicalApplicationRuntimes(runtimes)
	if err != nil {
		return nil, err
	}
	selected := make([]ApplicationRuntime, 0, len(canonical))
	for _, runtime := range canonical {
		if runtime.SiteRef == siteRef && runtime.NodeRef == nodeRef {
			selected = append(selected, runtime)
		}
	}
	return selected, nil
}

func applicationRuntimeStartOrder(runtime ApplicationRuntime) ([]string, error) {
	components := make(map[string]ApplicationRuntimeComponent, len(runtime.Components))
	indegree := make(map[string]int, len(runtime.Components))
	dependents := make(map[string][]string, len(runtime.Components))
	for _, component := range runtime.Components {
		components[component.ComponentRef] = component
		indegree[component.ComponentRef] = len(component.DependsOn)
		for _, dependency := range component.DependsOn {
			dependents[dependency] = append(dependents[dependency], component.ComponentRef)
		}
	}
	ready := make([]string, 0, len(runtime.Components))
	for componentRef, degree := range indegree {
		if degree == 0 {
			ready = append(ready, componentRef)
		}
	}
	slices.Sort(ready)
	order := make([]string, 0, len(runtime.Components))
	for len(ready) > 0 {
		componentRef := ready[0]
		ready = ready[1:]
		order = append(order, componentRef)
		for _, dependent := range dependents[componentRef] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
		slices.Sort(ready)
	}
	if len(order) != len(components) {
		return nil, fmt.Errorf("application runtime %q contains a dependency cycle", runtime.ComposeProject)
	}
	return order, nil
}

// ApplicationRuntimeStartOrder returns the dependency-first order derived
// from the selected graph. Callers must not replace it with label or ID order.
func ApplicationRuntimeStartOrder(runtime ApplicationRuntime) ([]string, error) {
	canonical, err := canonicalApplicationRuntimes([]ApplicationRuntime{runtime})
	if err != nil {
		return nil, err
	}
	return applicationRuntimeStartOrder(canonical[0])
}

// ApplicationRuntimeStopOrder returns the reverse dependency order used to
// stop application writers before their databases and infrastructure.
func ApplicationRuntimeStopOrder(runtime ApplicationRuntime) ([]string, error) {
	order, err := ApplicationRuntimeStartOrder(runtime)
	if err != nil {
		return nil, err
	}
	for left, right := 0, len(order)-1; left < right; left, right = left+1, right-1 {
		order[left], order[right] = order[right], order[left]
	}
	return order, nil
}

// ApplicationGraphDigest binds a persisted quiescence journal to the exact
// selected application graphs. Core-only policies intentionally return an
// empty digest and retain their legacy crash-consistent behavior.
func ApplicationGraphDigest(source Source) (string, error) {
	if err := validateSourceProjection(source); err != nil {
		return "", err
	}
	runtimes, err := canonicalApplicationRuntimes(source.ApplicationRuntimes)
	if err != nil {
		return "", err
	}
	if len(runtimes) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(runtimes)
	if err != nil {
		return "", fmt.Errorf("marshal application runtime graph digest: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
