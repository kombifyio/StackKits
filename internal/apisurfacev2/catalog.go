package apisurface

import (
	"sort"
	"strings"
)

// CatalogFragmentKind identifies the Gateway MCP catalog fragment artifact.
const CatalogFragmentKind = "kombify.mcp-catalog-fragment/v1"

// Gateway action classes, ordered read < reversible_write < external_write <
// destructive / cost_bearing (kombify-Gateway MCP_ACTION_CLASSES).
const (
	ActionRead            = "read"
	ActionReversibleWrite = "reversible_write"
	ActionExternalWrite   = "external_write"
	ActionDestructive     = "destructive"
	ActionCostBearing     = "cost_bearing"
)

var actionClasses = []string{ActionRead, ActionReversibleWrite, ActionExternalWrite, ActionDestructive, ActionCostBearing}

// Catalog is x-kombify-surface.mcp.catalog: how the product's tools enter the
// Gateway's public MCP catalog.
type Catalog struct {
	ServerKey             string
	ConnectorFamily       string
	Upstream              CatalogUpstream
	PortalVisibilityGroup string
	RequiredScopes        []string
	FeaturePrefix         string
	QuotaPrefix           string
	AuditPrefix           string
}

// CatalogUpstream names the backend the Gateway forwards tool calls to.
type CatalogUpstream struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
}

// CatalogFragment is the product's contribution to the Gateway MCP catalog.
type CatalogFragment struct {
	Kind      string        `json:"kind"`
	ServerKey string        `json:"serverKey"`
	Source    CatalogSource `json:"source"`
	Tools     []CatalogTool `json:"tools"`
}

// CatalogSource binds the fragment to the spec it was generated from.
type CatalogSource struct {
	Product string `json:"product"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

// CatalogTool is one tool entry in the Gateway's live catalog format.
type CatalogTool struct {
	ToolID                string           `json:"toolId"`
	ConnectorFamily       string           `json:"connectorFamily"`
	ServerKey             string           `json:"serverKey"`
	ToolName              string           `json:"toolName"`
	Alias                 string           `json:"alias"`
	Title                 string           `json:"title,omitempty"`
	Description           string           `json:"description,omitempty"`
	InputSchema           map[string]any   `json:"inputSchema"`
	Annotations           Annotations      `json:"annotations"`
	FeatureKey            string           `json:"featureKey"`
	EntitlementKey        string           `json:"entitlementKey"`
	FGAObject             string           `json:"fgaObject"`
	AuditEvent            string           `json:"auditEvent"`
	OperationIDs          []string         `json:"operationIds"`
	Risk                  string           `json:"risk"`
	ApprovalPolicy        string           `json:"approvalPolicy"`
	ActionClass           string           `json:"actionClass"`
	QuotaKey              string           `json:"quotaKey"`
	Upstream              CatalogUpstream  `json:"upstream"`
	PortalVisibilityGroup string           `json:"portalVisibilityGroup"`
	RequiredScopes        []string         `json:"requiredScopes"`
	CapabilityBundle      string           `json:"capabilityBundle"`
	RequiredCapabilities  []string         `json:"requiredCapabilities"`
	ResourceBinding       *ResourceBinding `json:"resourceBinding,omitempty"`
	CostBearing           bool             `json:"costBearing"`
	UpstreamKind          string           `json:"upstreamKind"`
	LiveEnabled           bool             `json:"liveEnabled"`
}

// ActionClassFor returns the tool's Gateway action class: the explicit
// x-kombify-mcp.actionClass, else read for read-only tools, cost_bearing,
// destructive, external_write for open-world writes and reversible_write.
func ActionClassFor(m *MCP) string {
	switch {
	case m.ActionClass != "":
		return m.ActionClass
	case m.Annotations.ReadOnlyHint:
		return ActionRead
	case m.CostBearing:
		return ActionCostBearing
	case m.Annotations.DestructiveHint:
		return ActionDestructive
	case m.Annotations.OpenWorldHint:
		return ActionExternalWrite
	default:
		return ActionReversibleWrite
	}
}

// actionClassRules are the Gateway catalog loader's per-class conditions.
var actionClassRules = map[string]func(m *MCP) bool{
	ActionRead:            func(m *MCP) bool { return m.Annotations.ReadOnlyHint && !m.Annotations.DestructiveHint },
	ActionReversibleWrite: plainWrite,
	ActionExternalWrite:   func(m *MCP) bool { return plainWrite(m) && m.Annotations.OpenWorldHint },
	ActionDestructive:     func(m *MCP) bool { return m.Annotations.DestructiveHint },
	ActionCostBearing:     func(m *MCP) bool { return m.CostBearing },
}

// plainWrite is a non-destructive, non-cost-bearing write.
func plainWrite(m *MCP) bool {
	return !m.Annotations.ReadOnlyHint && !m.Annotations.DestructiveHint && !m.CostBearing
}

// actionClassProblem applies the Gateway catalog loader's class rules so a
// generated fragment is never rejected there.
func actionClassProblem(m *MCP) string {
	class := ActionClassFor(m)
	rule, known := actionClassRules[class]
	switch {
	case !known:
		return "actionClass " + class + " must be one of " + strings.Join(actionClasses, ", ")
	case m.CostBearing && !m.Annotations.ReadOnlyHint && class != ActionCostBearing:
		return "a cost-bearing write must use actionClass cost_bearing"
	case !rule(m):
		return "actionClass " + class + " contradicts the tool's annotations or costBearing"
	}
	return ""
}

// CatalogFragment projects every MCP tool into the Gateway catalog format,
// tools sorted by toolName. Title, description and input schema are those of
// the tool manifest. Derived tools start with liveEnabled false so the
// Gateway curates them before they go live.
func (s *Surface) CatalogFragment(c *Catalog) *CatalogFragment {
	f := &CatalogFragment{
		Kind:      CatalogFragmentKind,
		ServerKey: c.ServerKey,
		Source:    CatalogSource{Product: s.Product, Path: s.Source.Path, SHA256: s.Source.SHA256},
		Tools:     []CatalogTool{},
	}
	ops := make(map[string]*Operation, len(s.Operations))
	for i := range s.Operations {
		ops[s.Operations[i].OperationID] = &s.Operations[i]
	}
	for _, t := range s.ToolManifest().Tools {
		op := ops[t.OperationID]
		class := ActionClassFor(op.MCP)
		capability := t.RequiredCapability
		approval := "none"
		if op.Confirmation != "" || class == ActionDestructive || class == ActionCostBearing {
			approval = "human"
		}
		id := c.ServerKey + "." + t.Name
		f.Tools = append(f.Tools, CatalogTool{
			ToolID:                id,
			ConnectorFamily:       c.ConnectorFamily,
			ServerKey:             c.ServerKey,
			ToolName:              t.Name,
			Alias:                 id,
			Title:                 t.Title,
			Description:           t.Description,
			InputSchema:           t.InputSchema,
			Annotations:           t.Annotations,
			FeatureKey:            c.FeaturePrefix + "." + capability,
			EntitlementKey:        capability,
			FGAObject:             "tool:" + c.ServerKey + "/" + t.Name,
			AuditEvent:            joinKey(c.AuditPrefix, capabilityFamily(capability), t.Name),
			OperationIDs:          []string{c.ServerKey + "." + t.OperationID},
			Risk:                  riskFor(class),
			ApprovalPolicy:        approval,
			ActionClass:           class,
			QuotaKey:              c.QuotaPrefix + "." + capability,
			Upstream:              c.Upstream,
			PortalVisibilityGroup: c.PortalVisibilityGroup,
			RequiredScopes:        c.RequiredScopes,
			CapabilityBundle:      capability,
			RequiredCapabilities:  []string{capability},
			ResourceBinding:       op.MCP.ResourceBinding,
			CostBearing:           op.MCP.CostBearing,
			UpstreamKind:          c.Upstream.Kind,
			LiveEnabled:           !op.MCP.Derived,
		})
	}
	sort.Slice(f.Tools, func(i, j int) bool { return f.Tools[i].ToolName < f.Tools[j].ToolName })
	return f
}

// Marshal encodes the fragment deterministically: 2-space indent, trailing
// newline, no HTML escaping.
func (f *CatalogFragment) Marshal() ([]byte, error) { return encode(f) }

func riskFor(class string) string {
	switch class {
	case ActionRead:
		return "low"
	case ActionReversibleWrite:
		return "medium"
	default:
		return "high"
	}
}

// capabilityFamily drops the capability's last dot segment (its verb):
// techstack.inventory.read becomes techstack.inventory.
func capabilityFamily(capability string) string {
	if i := strings.LastIndex(capability, "."); i >= 0 {
		return capability[:i]
	}
	return ""
}

func joinKey(parts ...string) string {
	out := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ".")
}
