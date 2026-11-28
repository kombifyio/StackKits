package coolify

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// AgentProjectName is the single dedicated Coolify project pre-created by
// StackKits bootstrap for agent-managed applications (ADR-0054 §3).
const AgentProjectName = "kombify-agent-apps"

// Coolify 4.7.5+ token abilities relevant to the Agent facade.
const (
	ScopeRead          = "read"
	ScopeWrite         = "write"
	ScopeDeploy        = "deploy"
	ScopeReadSensitive = "read:sensitive"
	ScopeDelete        = "delete"
)

// Governance approval classifications and decision references.
const (
	ApprovalClassOpenDefault   = "open-default"
	ApprovalClassHumanRequired = "human-approval-required"
	ApprovalClassDenied        = "denied"

	GovernanceOpenDefault   = "D-2026-11-26-08/D-2026-11-27-09"
	GovernanceHumanApproval = "D-2026-11-20-06"
	GovernanceADR0054       = "ADR-0054"

	// RedactedObservationRedirect directs callers away from Coolify's
	// read:sensitive GetLogs endpoint to StackKits' redacted host observation
	// snapshot (ADR-0051).
	RedactedObservationRedirect = "use StackKits redacted host observation snapshot (ADR-0051); Coolify 4.7.5 get_logs (GetLogs.php:34,39) requires read:sensitive which is forbidden on Agent tokens"
)

// ApprovalVerificationRequest carries the exact operation context that a
// D-2026-11-20-06 human-approval token must be cryptographically/authoritatively
// bound to (token, tool, project_uuid, resource_uuid, publication_id,
// host_root, new_public_exposure, and verification clock).
type ApprovalVerificationRequest struct {
	Token             string
	Tool              string
	ProjectUUID       string
	ResourceUUID      string
	PublicationID     string
	HostRoot          bool
	NewPublicExposure bool
	Now               time.Time
}

// ApprovalVerifier validates D-2026-11-20-06 human-approval tokens fail-closed.
type ApprovalVerifier interface {
	VerifyApproval(ctx context.Context, req ApprovalVerificationRequest) error
}

// ApprovalVerifierFunc adapts an ordinary function to ApprovalVerifier.
type ApprovalVerifierFunc func(ctx context.Context, req ApprovalVerificationRequest) error

// VerifyApproval calls fn(ctx, req) and fails closed if fn is nil.
func (fn ApprovalVerifierFunc) VerifyApproval(ctx context.Context, req ApprovalVerificationRequest) error {
	if fn == nil {
		return errors.New("coolify agent facade: approval verifier function is nil")
	}
	return fn(ctx, req)
}

// ValidateAgentTokenScopes enforces fail-closed that an Agent Coolify token
// holds ONLY abilities from {read, write, deploy} and NEVER carries
// read:sensitive, delete, root, wildcard, or unknown scopes.
func ValidateAgentTokenScopes(scopes []string) error {
	if len(scopes) == 0 {
		return errors.New("coolify agent facade: token scopes must not be empty")
	}
	validCount := 0
	for _, raw := range scopes {
		scope := strings.ToLower(strings.TrimSpace(raw))
		if scope == "" {
			return errors.New("coolify agent facade: token scope list contains an empty scope")
		}
		switch scope {
		case ScopeRead, ScopeWrite, ScopeDeploy:
			validCount++
		case ScopeReadSensitive:
			return errors.New("coolify agent facade: token scope read:sensitive is forbidden for Agent tokens (ADR-0054)")
		case ScopeDelete:
			return errors.New("coolify agent facade: token scope delete is forbidden for Agent tokens (ADR-0054, D-2026-11-20-06)")
		default:
			return fmt.Errorf("coolify agent facade: unsupported or privileged token scope %q is forbidden for Agent tokens", raw)
		}
	}
	if validCount == 0 {
		return errors.New("coolify agent facade: at least one permitted scope (read, write, deploy) is required")
	}
	return nil
}

// AgentFacadeConfig configures an enforceable Coolify 4.7.5 Agent facade.
type AgentFacadeConfig struct {
	// ProjectName defaults to AgentProjectName ("kombify-agent-apps") and must
	// match AgentProjectName.
	ProjectName string

	// ProjectUUID is the Coolify UUID of the kombify-agent-apps project.
	ProjectUUID string

	// TokenScopes are the Coolify token abilities held by the Agent token.
	TokenScopes []string

	// OwnedResources maps Coolify resource_uuid -> project_uuid for resources
	// admitted inside kombify-agent-apps.
	OwnedResources map[string]string

	// AdmittedPublications enumerates publication IDs already admitted for
	// reversible domain-bind operations without new public exposure.
	AdmittedPublications map[string]bool

	// ApprovalVerifier verifies D-2026-11-20-06 human-approval tokens bound to
	// the exact tool, project_uuid, resource_uuid, publication_id, host_root,
	// new_public_exposure, and timestamp. When nil, every human-approval-gated
	// operation fails closed.
	ApprovalVerifier ApprovalVerifier

	// Now provides the clock timestamp passed to ApprovalVerifier. Defaults to
	// time.Now().UTC() when nil.
	Now func() time.Time
}

// AgentFacade enforces tool allowlisting, token-scope boundaries, project and
// resource UUID scoping, and D-2026-11-20-06 human-approval gating for Coolify
// 4.7.5+ agent tool calls.
type AgentFacade struct {
	projectName          string
	projectUUID          string
	scopes               map[string]bool
	ownedResources       map[string]string
	admittedPublications map[string]bool
	approvalVerifier     ApprovalVerifier
	now                  func() time.Time
}

// ToolCallRequest describes one candidate Coolify facade tool invocation.
type ToolCallRequest struct {
	Tool               string
	ProjectUUID        string
	ResourceUUID       string
	PublicationID      string
	NewPublicExposure  bool
	HostRoot           bool
	HumanApprovalToken string
}

// AuthorizationDecision records the enforceable authorization outcome.
type AuthorizationDecision struct {
	Allowed       bool
	ApprovalClass string
	GovernanceRef string
	Reason        string
	RedirectHint  string
}

type toolPolicy struct {
	requiredScopes  []string
	requireResource bool
	alwaysHumanGate bool
	openReason      string
	gateReason      string
}

var allowlistedTools = map[string]toolPolicy{
	"status": {
		requiredScopes:  []string{ScopeRead},
		requireResource: false,
		openReason:      "reversible status observation inside ",
	},
	"deploy": {
		requiredScopes:  []string{ScopeDeploy},
		requireResource: true,
		openReason:      "reversible deploy inside ",
	},
	"restart": {
		requiredScopes:  []string{ScopeDeploy, ScopeWrite},
		requireResource: true,
		openReason:      "reversible restart inside ",
	},
	"vault-ref-env": {
		requiredScopes:  []string{ScopeWrite},
		requireResource: true,
		openReason:      "reversible vault-ref environment binding inside ",
	},
	"domain-bind": {
		requiredScopes:  []string{ScopeWrite},
		requireResource: true,
		openReason:      "reversible domain-bind inside already-admitted publication ",
	},
	"expose_public": {
		requiredScopes:  []string{ScopeWrite},
		requireResource: true,
		alwaysHumanGate: true,
		gateReason:      "new public exposure requires explicit human approval",
	},
	"public_exposure": {
		requiredScopes:  []string{ScopeWrite},
		requireResource: true,
		alwaysHumanGate: true,
		gateReason:      "new public exposure requires explicit human approval",
	},
}

// NewAgentFacade validates cfg fail-closed and constructs an AgentFacade.
func NewAgentFacade(cfg AgentFacadeConfig) (*AgentFacade, error) {
	projectName := strings.TrimSpace(cfg.ProjectName)
	if projectName == "" {
		projectName = AgentProjectName
	}
	if projectName != AgentProjectName {
		return nil, fmt.Errorf("coolify agent facade: project name %q must be %q", projectName, AgentProjectName)
	}
	projectUUID := strings.TrimSpace(cfg.ProjectUUID)
	if projectUUID == "" {
		return nil, errors.New("coolify agent facade: project_uuid for kombify-agent-apps is required")
	}
	if err := ValidateAgentTokenScopes(cfg.TokenScopes); err != nil {
		return nil, err
	}
	scopeSet := make(map[string]bool, len(cfg.TokenScopes))
	for _, s := range cfg.TokenScopes {
		scopeSet[strings.ToLower(strings.TrimSpace(s))] = true
	}
	owned := make(map[string]string, len(cfg.OwnedResources))
	for resUUID, projUUID := range cfg.OwnedResources {
		r := strings.TrimSpace(resUUID)
		p := strings.TrimSpace(projUUID)
		if r != "" && p != "" {
			owned[r] = p
		}
	}
	pubs := make(map[string]bool, len(cfg.AdmittedPublications))
	for pubID, ok := range cfg.AdmittedPublications {
		if id := strings.TrimSpace(pubID); id != "" && ok {
			pubs[id] = true
		}
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = func() time.Time { return time.Now().UTC() }
	}
	return &AgentFacade{
		projectName:          projectName,
		projectUUID:          projectUUID,
		scopes:               scopeSet,
		ownedResources:       owned,
		admittedPublications: pubs,
		approvalVerifier:     cfg.ApprovalVerifier,
		now:                  nowFn,
	}, nil
}

// Authorize evaluates req with a background context.
func (f *AgentFacade) Authorize(req ToolCallRequest) (AuthorizationDecision, error) {
	return f.AuthorizeContext(context.Background(), req)
}

// AuthorizeContext evaluates req strictly in this fail-closed order so human
// approval can NEVER substitute for authorization:
//
//  1. (a) Forbidden tools (get_logs, get_env_values, docker_cleanup,
//     SSH/server/key, destructive delete) -> ALWAYS DENIED even with approval.
//  2. (b) Tool allowlist -> unknown tools ALWAYS DENIED.
//  3. (c) Token scope check (read/write/deploy; delete & read:sensitive
//     forbidden) -> scope violations ALWAYS DENIED.
//  4. (d) project_uuid & resource_uuid ownership check against
//     kombify-agent-apps -> cross-project/unowned resources ALWAYS DENIED even
//     with approval.
//  5. (e) ONLY AFTER (a)-(d) pass, evaluate the D-2026-11-20-06 human-approval
//     gate for guarded operations (new public exposure, unadmitted domain-bind,
//     host-root) using the configured ApprovalVerifier.
func (f *AgentFacade) AuthorizeContext(ctx context.Context, req ToolCallRequest) (AuthorizationDecision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	tool := strings.ToLower(strings.TrimSpace(req.Tool))
	if tool == "" {
		return AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        "tool name is required",
		}, errors.New("coolify agent facade: tool name is required")
	}

	// (a) Forbidden tools: ALWAYS DENIED (even with human approval).
	switch tool {
	case "get_logs", "logs", "application_logs", "service_logs":
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        "get_logs is excluded from the Coolify agent facade because Coolify 4.7.5 GetLogs.php:34,39 requires read:sensitive",
			RedirectHint:  RedactedObservationRedirect,
		}
		return dec, fmt.Errorf("coolify agent facade: %s (%s)", dec.Reason, dec.RedirectHint)

	case "get_env_values", "list_envs_sensitive", "read_secrets", "env_values":
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        fmt.Sprintf("tool %q is refused to prevent secret exfiltration", tool),
		}
		return dec, fmt.Errorf("coolify agent facade: %s", dec.Reason)

	case "docker_cleanup", "docker_prune", "system_prune":
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        fmt.Sprintf("tool %q is refused on the Coolify agent facade", tool),
		}
		return dec, fmt.Errorf("coolify agent facade: %s", dec.Reason)
	}

	if isSSHServerOrKeyTool(tool) {
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        fmt.Sprintf("SSH/server/key tool %q is refused on the Coolify agent facade", tool),
		}
		return dec, fmt.Errorf("coolify agent facade: %s", dec.Reason)
	}

	if isDeleteTool(tool) {
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        fmt.Sprintf("destructive tool %q is denied on the Coolify agent facade because Agent tokens forbid the delete scope (%s)", tool, GovernanceHumanApproval),
		}
		return dec, fmt.Errorf("coolify agent facade: %s", dec.Reason)
	}

	// (b) Tool allowlist: unknown tools ALWAYS DENIED.
	policy, allowedTool := allowlistedTools[tool]
	if !allowedTool {
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        fmt.Sprintf("tool %q is not in the Coolify agent facade allowlist", tool),
		}
		return dec, fmt.Errorf("coolify agent facade: %s", dec.Reason)
	}

	// (c) Token scope check: forbidden scopes or missing required scope ALWAYS DENIED.
	if f.scopes[ScopeDelete] || f.scopes[ScopeReadSensitive] {
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        "agent token carries forbidden scope (delete or read:sensitive)",
		}
		return dec, fmt.Errorf("coolify agent facade: %s", dec.Reason)
	}
	if !f.hasAnyScope(policy.requiredScopes) {
		return denyMissingScope(tool, strings.Join(policy.requiredScopes, " or "))
	}

	// (d) project_uuid & resource_uuid ownership check against kombify-agent-apps:
	// cross-project or unowned resources ALWAYS DENIED (even with approval).
	if err := f.verifyProjectAndResource(req, policy.requireResource); err != nil {
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassDenied,
			GovernanceRef: GovernanceADR0054,
			Reason:        err.Error(),
		}
		return dec, err
	}

	// (e) ONLY AFTER (a)-(d) pass, evaluate D-2026-11-20-06 human-approval gate
	// for guarded operations using ApprovalVerifier.
	var gateReasons []string
	if req.HostRoot {
		gateReasons = append(gateReasons, "host-root operation requires explicit human approval")
	}
	pubID := strings.TrimSpace(req.PublicationID)
	unadmittedDomainBind := tool == "domain-bind" && (pubID == "" || !f.admittedPublications[pubID])
	newPublicExposure := policy.alwaysHumanGate || req.NewPublicExposure || unadmittedDomainBind
	if policy.alwaysHumanGate || req.NewPublicExposure {
		gateReasons = append(gateReasons, "new public exposure requires explicit human approval")
	}
	if unadmittedDomainBind {
		gateReasons = append(gateReasons, fmt.Sprintf("domain-bind for unadmitted publication %q creates new public exposure and requires human approval", pubID))
	}

	if len(gateReasons) > 0 {
		return f.gateHumanApproval(
			ctx,
			ApprovalVerificationRequest{
				Token:             strings.TrimSpace(req.HumanApprovalToken),
				Tool:              tool,
				ProjectUUID:       strings.TrimSpace(req.ProjectUUID),
				ResourceUUID:      strings.TrimSpace(req.ResourceUUID),
				PublicationID:     pubID,
				HostRoot:          req.HostRoot,
				NewPublicExposure: newPublicExposure,
			},
			strings.Join(gateReasons, "; "),
		)
	}

	if tool == "domain-bind" {
		return openDefaultAllowed(policy.openReason + pubID), nil
	}
	return openDefaultAllowed(policy.openReason + f.projectName), nil
}

func AgentToolNames() []string { return slices.Sorted(maps.Keys(allowlistedTools)) }

func (f *AgentFacade) hasAnyScope(required []string) bool {
	for _, scope := range required {
		if f.scopes[scope] {
			return true
		}
	}
	return false
}

func (f *AgentFacade) verifyProjectAndResource(req ToolCallRequest, requireResource bool) error {
	projectUUID := strings.TrimSpace(req.ProjectUUID)
	if projectUUID == "" {
		return fmt.Errorf("coolify agent facade: project_uuid is required and must match %s (%s)", f.projectName, f.projectUUID)
	}
	if projectUUID != f.projectUUID {
		return fmt.Errorf("coolify agent facade: project_uuid %q is outside allowed project %s (%s)", projectUUID, f.projectName, f.projectUUID)
	}
	resourceUUID := strings.TrimSpace(req.ResourceUUID)
	if resourceUUID == "" {
		if requireResource {
			return fmt.Errorf("coolify agent facade: resource_uuid is required for %q inside %s", req.Tool, f.projectName)
		}
		return nil
	}
	ownerProject, exists := f.ownedResources[resourceUUID]
	if !exists || ownerProject != f.projectUUID {
		return fmt.Errorf("coolify agent facade: resource_uuid %q does not belong to %s (%s)", resourceUUID, f.projectName, f.projectUUID)
	}
	return nil
}

func openDefaultAllowed(reason string) AuthorizationDecision {
	return AuthorizationDecision{
		Allowed:       true,
		ApprovalClass: ApprovalClassOpenDefault,
		GovernanceRef: GovernanceOpenDefault,
		Reason:        reason,
	}
}

func denyMissingScope(tool, required string) (AuthorizationDecision, error) {
	dec := AuthorizationDecision{
		Allowed:       false,
		ApprovalClass: ApprovalClassDenied,
		GovernanceRef: GovernanceADR0054,
		Reason:        fmt.Sprintf("tool %q requires Coolify token scope %s", tool, required),
	}
	return dec, fmt.Errorf("coolify agent facade: %s", dec.Reason)
}

func (f *AgentFacade) gateHumanApproval(ctx context.Context, verifyReq ApprovalVerificationRequest, reason string) (AuthorizationDecision, error) {
	verifyReq.Token = strings.TrimSpace(verifyReq.Token)
	if verifyReq.Token == "" {
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassHumanRequired,
			GovernanceRef: GovernanceHumanApproval,
			Reason:        reason,
		}
		return dec, fmt.Errorf("coolify agent facade: %s (%s)", reason, GovernanceHumanApproval)
	}
	if f.approvalVerifier == nil {
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassHumanRequired,
			GovernanceRef: GovernanceHumanApproval,
			Reason:        reason + " (human approval verifier is not configured; failing closed)",
		}
		return dec, fmt.Errorf("coolify agent facade: %s (%s)", dec.Reason, GovernanceHumanApproval)
	}
	now := time.Now().UTC()
	if f.now != nil {
		now = f.now().UTC()
	}
	verifyReq.Now = now
	if verifyErr := f.approvalVerifier.VerifyApproval(ctx, verifyReq); verifyErr != nil {
		dec := AuthorizationDecision{
			Allowed:       false,
			ApprovalClass: ApprovalClassHumanRequired,
			GovernanceRef: GovernanceHumanApproval,
			Reason:        reason + " (human approval verification failed)",
		}
		return dec, fmt.Errorf("coolify agent facade: %s (%s)", dec.Reason, GovernanceHumanApproval)
	}
	return AuthorizationDecision{
		Allowed:       true,
		ApprovalClass: ApprovalClassHumanRequired,
		GovernanceRef: GovernanceHumanApproval,
		Reason:        reason + " (human approval verified)",
	}, nil
}

func isDeleteTool(tool string) bool {
	return tool == "delete" || strings.HasPrefix(tool, "delete_") || strings.HasPrefix(tool, "delete-") || strings.HasPrefix(tool, "remove_")
}

func isSSHServerOrKeyTool(tool string) bool {
	prefixes := []string{"ssh_", "ssh-", "server_", "server-", "private_key", "key_", "key-", "host_exec", "terminal_"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(tool, prefix) {
			return true
		}
	}
	switch tool {
	case "ssh", "server", "servers", "private_keys", "keys", "terminal":
		return true
	default:
		return false
	}
}
