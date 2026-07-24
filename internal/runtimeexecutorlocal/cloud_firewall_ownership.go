package runtimeexecutorlocal

import "strings"

const (
	cloudHostFirewallRulesetPrefix = "stackkits-cloud-host-security"
	cloudPublicEdgeChainPrefix     = "stackkits-cloud-public-edge"
)

// These references are provider-neutral logical ownership boundaries. The
// host-security owner controls the parent ruleset and delegates only the child
// chain to the public-edge owner; neither owner may mutate the other's scope.
func cloudHostFirewallRulesetRef(siteRef, nodeRef string) string {
	return cloudHostFirewallRulesetPrefix + "/" + strings.TrimSpace(siteRef) + "/" + strings.TrimSpace(nodeRef)
}

func cloudPublicEdgeChainRef(siteRef, nodeRef string) string {
	return cloudPublicEdgeChainPrefix + "/" + strings.TrimSpace(siteRef) + "/" + strings.TrimSpace(nodeRef)
}
