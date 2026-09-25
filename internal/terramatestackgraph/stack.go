// Package terramatestackgraph owns the Terramate stack identity, tagging and
// ordering rules for Architecture v2 plans generated under the `terramate`
// target (ADR-0045 section 2 and section 5), and the machine-readable
// `stackkit.terramate-stack-graph/v1` artifact that the Advanced executor and
// Techstack consume. Generation renders one `stack.tm.hcl` per stack-bearing
// render instance and one project root per host; this package is the single
// authority both the renderer and the graph builder use, so the files and the
// graph cannot disagree.
package terramatestackgraph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Role orders stacks inside a plan. Host and security baseline owners are not
// stacks; they stay native executor operations.
type Role string

const (
	// RoleCore is the site core of one host (Basement core, Basement core
	// Lite, Cloud core, Cloud standalone core). Its OpenTofu root is generated.
	RoleCore Role = "core"
	// RoleEdge is the Cloud public edge of one host. In Modern it anchors the
	// Cloud site, which has no Compose core.
	RoleEdge Role = "edge"
	// RoleWorkload is one selected application bundle on one host.
	RoleWorkload Role = "workload"
	// RoleFederation is one Modern federation or bridge owner on one host.
	RoleFederation Role = "federation"
)

const (
	// RequiredVersion is the Terramate constraint of every generated project.
	// The release pins Terramate 0.17.1 (scripts/release/fetch-terramate.sh).
	RequiredVersion = "~> 0.17"
	// RuntimeRoot is the executor-managed Terramate project root on each host.
	RuntimeRoot = ".stackkit/runtime"

	// ProjectRootTemplateRef identifies the per-host project root unit.
	ProjectRootTemplateRef = "builtin://terramate/project-root/v1"
	// WorkloadStackTemplateRef identifies the generic workload stack unit.
	WorkloadStackTemplateRef = "builtin://terramate/stack/workload/v1"
	// EdgeStackTemplateRef identifies the generic Cloud public edge stack unit.
	EdgeStackTemplateRef = "builtin://terramate/stack/edge/v1"
	// FederationStackTemplateRef identifies the generic federation stack unit.
	FederationStackTemplateRef = "builtin://terramate/stack/federation/v1"

	stackIDNamespace = "stackkit.terramate-stack/v1"
)

// ProjectRootConfig is the exact `terramate.tm.hcl` placed at RuntimeRoot on
// every host. The runtime tree is not a Git repository, so the project relies
// on explicit stack ordering, not Git change detection.
const ProjectRootConfig = `terramate {
  required_version = "` + RequiredVersion + `"

  config {
    run {
      env {
        TF_IN_AUTOMATION = "1"
        TF_INPUT         = "0"
      }
    }
  }
}
`

// coreStack binds each core Terramate unit template to the native runtime
// directory whose OpenTofu root it generates (docs/ARCHITECTURE.md "Stage 1
// OpenTofu wrapper roots").
var coreStacks = map[string]string{
	"builtin://basement/core/terramate/v1":         "basement-core",
	"builtin://basement/core-lite/terramate/v1":    "basement-core",
	"builtin://cloud/core/terramate/v1":            "cloud-core",
	"builtin://cloud/core-standalone/terramate/v1": "cloud-core-standalone",
}

var genericStacks = map[string]Role{
	WorkloadStackTemplateRef:   RoleWorkload,
	EdgeStackTemplateRef:       RoleEdge,
	FederationStackTemplateRef: RoleFederation,
}

// RoleForTemplate returns the stack role a Terramate render-unit template
// produces. Unknown templates are not stacks and must fail closed.
func RoleForTemplate(templateRef string) (Role, bool) {
	if _, core := coreStacks[templateRef]; core {
		return RoleCore, true
	}
	role, generic := genericStacks[templateRef]
	return role, generic
}

var (
	refPattern     = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,126}[a-z0-9])?$`)
	stackIDPattern = regexp.MustCompile(`^stackkit-[0-9a-f]{20}$`)
)

// Definition is the complete Terramate identity of one stack. The renderer
// writes it into `stack.tm.hcl`; the graph records the same values.
type Definition struct {
	ID           string
	Name         string
	Description  string
	Tags         []string
	AfterQueries []string
}

// Define derives the stack identity for one module render instance on one
// host. The ID is stable for the same module, Site and node across plan
// revisions, so Terramate history and per-stack drift survive regeneration.
func Define(role Role, moduleRef, siteRef, nodeRef string) (Definition, error) {
	for field, value := range map[string]string{"moduleRef": moduleRef, "siteRef": siteRef, "nodeRef": nodeRef} {
		if !refPattern.MatchString(value) {
			return Definition{}, fmt.Errorf("terramate stack %s %q is not a lowercase contract ID", field, value)
		}
	}
	after, err := afterQueries(role)
	if err != nil {
		return Definition{}, err
	}
	return Definition{
		ID:          StackID(moduleRef, siteRef, nodeRef),
		Name:        moduleRef + " @ " + siteRef + "/" + nodeRef,
		Description: "StackKits " + string(role) + " stack for " + moduleRef + " on node " + nodeRef + " of site " + siteRef,
		Tags: []string{
			"stackkit",
			"role/" + string(role),
			"site/" + siteRef,
			"node/" + nodeRef,
			"module/" + moduleRef,
		},
		AfterQueries: after,
	}, nil
}

// StackID is `stackkit-` plus the first 20 hex characters of
// sha256("stackkit.terramate-stack/v1\n<moduleRef>\n<siteRef>\n<nodeRef>").
func StackID(moduleRef, siteRef, nodeRef string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{stackIDNamespace, moduleRef, siteRef, nodeRef}, "\n")))
	return "stackkit-" + hex.EncodeToString(sum[:])[:20]
}

// afterQueries are the in-project Terramate ordering rules. A host project
// holds only the stacks of that host, so the tag queries resolve to the local
// site core and edge. Cross-host order lives only in the graph.
func afterQueries(role Role) ([]string, error) {
	switch role {
	case RoleCore:
		return nil, nil
	case RoleEdge, RoleWorkload:
		return []string{"tag:role/" + string(RoleCore)}, nil
	case RoleFederation:
		return []string{"tag:role/" + string(RoleCore), "tag:role/" + string(RoleEdge)}, nil
	default:
		return nil, fmt.Errorf("unknown terramate stack role %q", role)
	}
}

// runtimeStackRoot is the directory, relative to the host workspace, where the
// executor places the stack file and runs `tofu`.
func runtimeStackRoot(templateRef string, role Role, moduleRef, workloadRef, nodeRef string) string {
	switch role {
	case RoleCore:
		return RuntimeRoot + "/" + coreStacks[templateRef] + "/opentofu"
	case RoleWorkload:
		return RuntimeRoot + "/applications/stackkit-" + workloadRef + "-" + nodeRef + "/opentofu"
	default:
		return RuntimeRoot + "/modules/" + moduleRef + "/opentofu"
	}
}

func sortedUnique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
