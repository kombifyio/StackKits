package terramatestackgraph

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	// APIVersion identifies the stack graph document.
	APIVersion = "stackkit.terramate-stack-graph/v1"
	// ArtifactID is the plan-owned generation artifact that carries the graph.
	ArtifactID = "terramate-stack-graph"
	// ArtifactPath is the graph path relative to generation.outputRoot.
	ArtifactPath = ".stackkit/terramate-stack-graph.json"
	// GenerationTarget is the only target that renders the graph.
	GenerationTarget = "terramate"

	// OpenTofuRootGenerated marks a stack whose `main.tf` is a generation
	// artifact (the four kit cores).
	OpenTofuRootGenerated = "generated"
	// OpenTofuRootExecutor marks a stack whose `main.tf` the executor
	// materializes at apply time (workloads, edge and federation owners).
	OpenTofuRootExecutor = "executor-materialized"
)

// Graph is the complete Terramate stack graph of one resolved plan. Every
// list is sorted, so equal plans yield byte-identical documents.
type Graph struct {
	APIVersion               string  `json:"apiVersion"`
	StackID                  string  `json:"stackId"`
	PlanHash                 string  `json:"planHash"`
	GenerationTarget         string  `json:"generationTarget"`
	TerramateRequiredVersion string  `json:"terramateRequiredVersion"`
	Hosts                    []Host  `json:"hosts"`
	Stacks                   []Stack `json:"stacks"`
}

// Host is one self-contained Terramate project: the executor-managed runtime
// tree of one node. A Techstack-dispatched change set runs per host through
// its execution channel, in RunOrder, after every cross-host `after` stack.
type Host struct {
	SiteRef             string   `json:"siteRef"`
	NodeRef             string   `json:"nodeRef"`
	ExecutionChannelRef string   `json:"executionChannelRef,omitempty"`
	ProjectRoot         string   `json:"projectRoot"`
	RootConfigArtifact  string   `json:"rootConfigArtifact"`
	RunOrder            []string `json:"runOrder"`
}

// Stack is one Terramate stack: one module render instance on one host.
type Stack struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Role         Role           `json:"role"`
	ModuleRef    string         `json:"moduleRef"`
	UnitRef      string         `json:"unitRef"`
	InstanceRef  string         `json:"instanceRef"`
	SiteRef      string         `json:"siteRef"`
	NodeRef      string         `json:"nodeRef"`
	WorkloadRef  string         `json:"workloadRef,omitempty"`
	RuntimeRoot  string         `json:"runtimeRoot"`
	OpenTofuRoot string         `json:"openTofuRoot"`
	After        []string       `json:"after"`
	Tags         []string       `json:"tags"`
	Artifacts    StackArtifacts `json:"artifacts"`
}

// StackArtifacts names the generation artifacts that belong to a stack.
type StackArtifacts struct {
	Stack    string `json:"stack"`
	OpenTofu string `json:"openTofu,omitempty"`
}

type planDocument struct {
	StackID    string `json:"stackId"`
	PlanHash   string `json:"planHash"`
	Generation struct {
		Target    string `json:"target"`
		Artifacts []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Owner struct {
				Kind        string `json:"kind"`
				ModuleRef   string `json:"moduleRef"`
				UnitRef     string `json:"unitRef"`
				InstanceRef string `json:"instanceRef"`
			} `json:"owner"`
		} `json:"artifacts"`
	} `json:"generation"`
	Modules []struct {
		ID          string `json:"id"`
		RenderUnits []struct {
			ID          string `json:"id"`
			Kind        string `json:"kind"`
			TemplateRef string `json:"templateRef"`
			Instances   []struct {
				ID      string `json:"id"`
				SiteRef string `json:"siteRef"`
				NodeRef string `json:"nodeRef"`
				Outputs []struct {
					ArtifactRef string `json:"artifactRef"`
					Ref         string `json:"ref"`
				} `json:"outputs"`
			} `json:"instances"`
		} `json:"renderUnits"`
	} `json:"modules"`
	Workloads []struct {
		ID          string `json:"id"`
		Alternative struct {
			ModuleRef string `json:"moduleRef"`
		} `json:"alternative"`
	} `json:"workloads"`
	Source struct {
		Inventory struct {
			Document struct {
				ExecutionChannels map[string]struct {
					ChannelRef string `json:"channelRef"`
					SiteRef    string `json:"siteRef"`
					NodeRef    string `json:"nodeRef"`
				} `json:"executionChannels"`
			} `json:"document"`
		} `json:"inventory"`
	} `json:"source"`
}

type hostKey struct{ site, node string }

// Build derives the stack graph from a canonical ResolvedPlan generated under
// the `terramate` target. It reads only plan facts; it never inspects
// rendered bytes, so the executor can recompute and compare it.
func Build(canonicalPlan []byte) (Graph, error) {
	var plan planDocument
	if err := json.Unmarshal(canonicalPlan, &plan); err != nil {
		return Graph{}, fmt.Errorf("decode resolved plan: %w", err)
	}
	if plan.Generation.Target != GenerationTarget {
		return Graph{}, fmt.Errorf("stack graph requires generation target %q, plan has %q", GenerationTarget, plan.Generation.Target)
	}
	if plan.StackID == "" || plan.PlanHash == "" {
		return Graph{}, errors.New("resolved plan has no stackId or planHash")
	}
	terramateArtifacts := make(map[string]string)
	for _, artifact := range plan.Generation.Artifacts {
		if artifact.Kind == "terramate" && artifact.Owner.Kind == "render-instance" {
			terramateArtifacts[artifact.ID] = artifact.Owner.ModuleRef + "\x00" + artifact.Owner.UnitRef + "\x00" + artifact.Owner.InstanceRef
		}
	}
	workloads := make(map[string][]string)
	for _, workload := range plan.Workloads {
		workloads[workload.Alternative.ModuleRef] = append(workloads[workload.Alternative.ModuleRef], workload.ID)
	}

	roots := make(map[hostKey]string)
	stacks := make([]Stack, 0)
	for _, module := range plan.Modules {
		for _, unit := range module.RenderUnits {
			if unit.Kind != "terramate" {
				continue
			}
			isRoot := unit.TemplateRef == ProjectRootTemplateRef
			role, isStack := RoleForTemplate(unit.TemplateRef)
			if !isRoot && !isStack {
				return Graph{}, fmt.Errorf("module %s unit %s: terramate template %q is neither a stack nor a project root", module.ID, unit.ID, unit.TemplateRef)
			}
			for _, instance := range unit.Instances {
				where := fmt.Sprintf("module %s unit %s instance %s", module.ID, unit.ID, instance.ID)
				if instance.SiteRef == "" || instance.NodeRef == "" {
					return Graph{}, fmt.Errorf("%s: terramate units must be node-local", where)
				}
				outputs := make(map[string]string, len(instance.Outputs))
				for _, output := range instance.Outputs {
					owner, governed := terramateArtifacts[output.ArtifactRef]
					if !governed || owner != module.ID+"\x00"+unit.ID+"\x00"+instance.ID {
						return Graph{}, fmt.Errorf("%s: output %s is not a governed terramate artifact of this instance", where, output.Ref)
					}
					name := path.Base(output.Ref)
					if _, duplicate := outputs[name]; duplicate {
						return Graph{}, fmt.Errorf("%s: duplicate %s output", where, name)
					}
					outputs[name] = output.ArtifactRef
				}
				key := hostKey{instance.SiteRef, instance.NodeRef}
				if isRoot {
					if len(outputs) != 1 || outputs["terramate.tm.hcl"] == "" {
						return Graph{}, fmt.Errorf("%s: project root unit must own exactly terramate.tm.hcl", where)
					}
					if _, duplicate := roots[key]; duplicate {
						return Graph{}, fmt.Errorf("%s: node %s has more than one Terramate project root", where, instance.NodeRef)
					}
					roots[key] = outputs["terramate.tm.hcl"]
					continue
				}
				stack, err := buildStack(role, unit.TemplateRef, module.ID, unit.ID, instance.ID, instance.SiteRef, instance.NodeRef, outputs, workloads[module.ID])
				if err != nil {
					return Graph{}, fmt.Errorf("%s: %w", where, err)
				}
				stacks = append(stacks, stack)
			}
		}
	}

	channels := make(map[hostKey][]string)
	for ref, channel := range plan.Source.Inventory.Document.ExecutionChannels {
		key := hostKey{channel.SiteRef, channel.NodeRef}
		channels[key] = append(channels[key], ref)
	}
	hostSet := make(map[hostKey]struct{})
	for key := range roots {
		hostSet[key] = struct{}{}
	}
	for _, stack := range stacks {
		key := hostKey{stack.SiteRef, stack.NodeRef}
		if roots[key] == "" {
			return Graph{}, fmt.Errorf("stack %s: node %s of site %s has no Terramate project root", stack.ID, stack.NodeRef, stack.SiteRef)
		}
	}
	hosts := make([]Host, 0, len(hostSet))
	for key := range hostSet {
		host := Host{SiteRef: key.site, NodeRef: key.node, ProjectRoot: RuntimeRoot, RootConfigArtifact: roots[key]}
		switch refs := channels[key]; len(refs) {
		case 0:
		case 1:
			host.ExecutionChannelRef = refs[0]
		default:
			sort.Strings(refs)
			return Graph{}, fmt.Errorf("node %s of site %s has more than one execution channel: %s", key.node, key.site, strings.Join(refs, ", "))
		}
		hosts = append(hosts, host)
	}

	graph := Graph{
		APIVersion: APIVersion, StackID: plan.StackID, PlanHash: plan.PlanHash,
		GenerationTarget: GenerationTarget, TerramateRequiredVersion: RequiredVersion,
		Hosts: hosts, Stacks: stacks,
	}
	if err := order(&graph); err != nil {
		return Graph{}, err
	}
	return graph, nil
}

func buildStack(role Role, templateRef, moduleRef, unitRef, instanceRef, siteRef, nodeRef string, outputs map[string]string, workloadRefs []string) (Stack, error) {
	definition, err := Define(role, moduleRef, siteRef, nodeRef)
	if err != nil {
		return Stack{}, err
	}
	stack := Stack{
		ID: definition.ID, Name: definition.Name, Role: role,
		ModuleRef: moduleRef, UnitRef: unitRef, InstanceRef: instanceRef,
		SiteRef: siteRef, NodeRef: nodeRef, Tags: definition.Tags,
		Artifacts: StackArtifacts{Stack: outputs["stack.tm.hcl"], OpenTofu: outputs["main.tf"]},
	}
	if stack.Artifacts.Stack == "" {
		return Stack{}, errors.New("stack unit owns no stack.tm.hcl")
	}
	wantOutputs := 1
	stack.OpenTofuRoot = OpenTofuRootExecutor
	if role == RoleCore {
		if stack.Artifacts.OpenTofu == "" {
			return Stack{}, errors.New("core stack unit owns no generated main.tf")
		}
		wantOutputs = 2
		stack.OpenTofuRoot = OpenTofuRootGenerated
	}
	if len(outputs) != wantOutputs {
		return Stack{}, fmt.Errorf("%s stack unit owns %d outputs, want %d", role, len(outputs), wantOutputs)
	}
	if role == RoleWorkload {
		if len(workloadRefs) != 1 {
			return Stack{}, fmt.Errorf("workload stack module is selected by %d workloads, want exactly one", len(workloadRefs))
		}
		stack.WorkloadRef = workloadRefs[0]
		if !refPattern.MatchString(stack.WorkloadRef) {
			return Stack{}, fmt.Errorf("workload ref %q is not a lowercase contract ID", stack.WorkloadRef)
		}
	}
	stack.RuntimeRoot = runtimeStackRoot(templateRef, role, moduleRef, stack.WorkloadRef, nodeRef)
	return stack, nil
}

// expectedAfter applies the ordering rules: the site core first; the edge and
// workloads of a host after that host's cores; federation and bridge owners
// after every core and edge of every site, so a link starts only when both
// ends are up.
func expectedAfter(stacks []Stack) map[string][]string {
	result := make(map[string][]string, len(stacks))
	for _, stack := range stacks {
		after := make([]string, 0)
		for _, other := range stacks {
			if other.ID == stack.ID {
				continue
			}
			sameHost := other.SiteRef == stack.SiteRef && other.NodeRef == stack.NodeRef
			switch stack.Role {
			case RoleEdge, RoleWorkload:
				if sameHost && other.Role == RoleCore {
					after = append(after, other.ID)
				}
			case RoleFederation:
				if other.Role == RoleCore || other.Role == RoleEdge {
					after = append(after, other.ID)
				}
			}
		}
		result[stack.ID] = sortedUnique(after)
	}
	return result
}

// order sets `after`, sorts every list and derives each host's run order. It
// rejects duplicate stack IDs and cycles.
func order(graph *Graph) error {
	seen := make(map[string]struct{}, len(graph.Stacks))
	for _, stack := range graph.Stacks {
		if _, duplicate := seen[stack.ID]; duplicate {
			return fmt.Errorf("duplicate stack id %s", stack.ID)
		}
		seen[stack.ID] = struct{}{}
	}
	after := expectedAfter(graph.Stacks)
	for index := range graph.Stacks {
		graph.Stacks[index].After = after[graph.Stacks[index].ID]
	}
	sort.Slice(graph.Stacks, func(i, j int) bool { return graph.Stacks[i].ID < graph.Stacks[j].ID })
	sort.Slice(graph.Hosts, func(i, j int) bool {
		if graph.Hosts[i].SiteRef != graph.Hosts[j].SiteRef {
			return graph.Hosts[i].SiteRef < graph.Hosts[j].SiteRef
		}
		return graph.Hosts[i].NodeRef < graph.Hosts[j].NodeRef
	})
	global, err := topologicalOrder(graph.Stacks)
	if err != nil {
		return err
	}
	for index := range graph.Hosts {
		host := &graph.Hosts[index]
		host.RunOrder = make([]string, 0)
		for _, stack := range global {
			if stack.SiteRef == host.SiteRef && stack.NodeRef == host.NodeRef {
				host.RunOrder = append(host.RunOrder, stack.ID)
			}
		}
	}
	return nil
}

// RunOrder returns every stack ID of the graph in the global run order: the
// same topological order and tie-breaking that derive each host's RunOrder,
// so cross-host `after` edges (federation after every core and edge) hold.
func RunOrder(graph Graph) ([]string, error) {
	ordered, err := topologicalOrder(graph.Stacks)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(ordered))
	for _, stack := range ordered {
		ids = append(ids, stack.ID)
	}
	return ids, nil
}

// topologicalOrder is Kahn's algorithm. Ties break by runtime root, then stack
// ID, which matches the order `terramate list --run-order` prints for one host
// project (Terramate orders unconstrained stacks by path).
func topologicalOrder(stacks []Stack) ([]Stack, error) {
	byID := make(map[string]Stack, len(stacks))
	pending := make(map[string]int, len(stacks))
	dependents := make(map[string][]string, len(stacks))
	for _, stack := range stacks {
		byID[stack.ID] = stack
	}
	for _, stack := range stacks {
		for _, dependency := range stack.After {
			if _, exists := byID[dependency]; !exists {
				return nil, fmt.Errorf("stack %s runs after unknown stack %s", stack.ID, dependency)
			}
			if dependency == stack.ID {
				return nil, fmt.Errorf("stack %s runs after itself", stack.ID)
			}
			dependents[dependency] = append(dependents[dependency], stack.ID)
		}
		pending[stack.ID] = len(stack.After)
	}
	less := func(left, right string) bool {
		if byID[left].RuntimeRoot != byID[right].RuntimeRoot {
			return byID[left].RuntimeRoot < byID[right].RuntimeRoot
		}
		return left < right
	}
	ready := make([]string, 0)
	for id, count := range pending {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	result := make([]Stack, 0, len(stacks))
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return less(ready[i], ready[j]) })
		next := ready[0]
		ready = ready[1:]
		result = append(result, byID[next])
		for _, dependent := range dependents[next] {
			pending[dependent]--
			if pending[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(result) != len(stacks) {
		return nil, errors.New("stack graph contains a cycle")
	}
	return result, nil
}

// Marshal emits the canonical graph document: two-space indented JSON with a
// trailing newline.
func Marshal(graph Graph) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(graph); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Render builds and marshals the graph of a canonical ResolvedPlan.
func Render(canonicalPlan []byte) ([]byte, error) {
	graph, err := Build(canonicalPlan)
	if err != nil {
		return nil, err
	}
	return Marshal(graph)
}

// Parse strictly decodes a graph document and proves it is canonical, closed
// and internally consistent: unknown fields, unsorted lists, identities or
// orderings that differ from the rules, dangling references and cycles are
// rejected.
func Parse(raw []byte) (Graph, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var graph Graph
	if err := decoder.Decode(&graph); err != nil {
		return Graph{}, fmt.Errorf("decode stack graph: %w", err)
	}
	if decoder.More() {
		return Graph{}, errors.New("stack graph has trailing data")
	}
	if graph.APIVersion != APIVersion || graph.GenerationTarget != GenerationTarget || graph.TerramateRequiredVersion != RequiredVersion {
		return Graph{}, errors.New("stack graph apiVersion, generationTarget or terramateRequiredVersion is not supported")
	}
	if graph.StackID == "" || graph.PlanHash == "" || graph.Hosts == nil || graph.Stacks == nil {
		return Graph{}, errors.New("stack graph requires stackId, planHash, hosts and stacks")
	}
	hosts := make(map[hostKey]struct{}, len(graph.Hosts))
	for _, host := range graph.Hosts {
		if host.ProjectRoot != RuntimeRoot || host.RootConfigArtifact == "" || host.RunOrder == nil {
			return Graph{}, fmt.Errorf("host %s/%s has no governed project root", host.SiteRef, host.NodeRef)
		}
		hosts[hostKey{host.SiteRef, host.NodeRef}] = struct{}{}
	}
	for _, stack := range graph.Stacks {
		definition, err := Define(stack.Role, stack.ModuleRef, stack.SiteRef, stack.NodeRef)
		if err != nil {
			return Graph{}, fmt.Errorf("stack %s: %w", stack.ID, err)
		}
		if stack.ID != definition.ID || stack.Name != definition.Name || !equalStrings(stack.Tags, definition.Tags) {
			return Graph{}, fmt.Errorf("stack %s identity differs from its module, site and node", stack.ID)
		}
		if _, exists := hosts[hostKey{stack.SiteRef, stack.NodeRef}]; !exists {
			return Graph{}, fmt.Errorf("stack %s has no host", stack.ID)
		}
		if stack.UnitRef == "" || stack.InstanceRef == "" || stack.Artifacts.Stack == "" || stack.After == nil {
			return Graph{}, fmt.Errorf("stack %s is incomplete", stack.ID)
		}
		generated := stack.Role == RoleCore
		if generated != (stack.OpenTofuRoot == OpenTofuRootGenerated) || generated == (stack.Artifacts.OpenTofu == "") ||
			(!generated && stack.OpenTofuRoot != OpenTofuRootExecutor) {
			return Graph{}, fmt.Errorf("stack %s OpenTofu root provenance does not match its role", stack.ID)
		}
		if (stack.Role == RoleWorkload) != (stack.WorkloadRef != "") {
			return Graph{}, fmt.Errorf("stack %s workloadRef does not match its role", stack.ID)
		}
		clean := path.Clean(stack.RuntimeRoot)
		if clean != stack.RuntimeRoot || !strings.HasPrefix(clean, RuntimeRoot+"/") || path.Base(clean) != "opentofu" {
			return Graph{}, fmt.Errorf("stack %s runtime root %q is outside the runtime tree", stack.ID, stack.RuntimeRoot)
		}
	}
	rebuilt := Graph{
		APIVersion: graph.APIVersion, StackID: graph.StackID, PlanHash: graph.PlanHash,
		GenerationTarget: graph.GenerationTarget, TerramateRequiredVersion: graph.TerramateRequiredVersion,
		Hosts: append([]Host(nil), graph.Hosts...), Stacks: append([]Stack(nil), graph.Stacks...),
	}
	if err := order(&rebuilt); err != nil {
		return Graph{}, err
	}
	canonical, err := Marshal(rebuilt)
	if err != nil {
		return Graph{}, err
	}
	if !bytes.Equal(canonical, raw) {
		return Graph{}, errors.New("stack graph is not in canonical form or its ordering differs from the rules")
	}
	return graph, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
