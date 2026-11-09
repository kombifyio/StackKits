package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/kombifyio/stackkits/internal/identity"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/localowner"
	"github.com/kombifyio/stackkits/internal/pocketid"
	"github.com/spf13/cobra"
)

// The agent link of this node (homelab identity target §7, §10): Techstack
// hands the Gateway's agent envelope verification key, the server and tenant
// it was issued for, the owner's kombify subject and the D10 references to the
// node; the Guard dispatches these commands with fixed argv (StackKit
// operations AGENT_LINK_INSTALL and AGENT_LINK_REMOVE, capability
// stackkit.agent-link.v1). Only the Authority's host, which holds the Owner
// custody, pins a key.
func init() {
	group := &cobra.Command{
		Use:   "agent-link",
		Short: "Pin, show or remove the Gateway key that kombify agent envelopes are verified against",
		Long: "A kombify agent reaches this node's /mcp with a Gateway-signed envelope that the Access Gate verifies against " +
			"the key pinned here at linking and maps through the D10 reference to a Homelab account. Techstack delivers the " +
			"link over the Guard when the server is registered as a Connect node and whenever the key or the linked members " +
			"change. install pins the key document for this node's own Guard enrollment and maps the owner's kombify subject " +
			"to the Owner of this node's custody and each linked member to their account. Pinning a key, re-keying and changing " +
			"the owner need the Owner's passkey step-up in the Hub (the request waits up to --wait and stays open for five " +
			"minutes; a later dispatch picks up the approval); a change of the linked members under the pinned key does not. " +
			"A higher binding generation or a new Gateway key id replaces the pin; an unchanged request changes nothing. " +
			"remove deletes the pin when the node is unlinked, status shows what is pinned.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	install := &cobra.Command{
		Use:   "install",
		Short: "Pin the Gateway agent envelope key and the D10 references of this node",
		Args:  cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: runIdentityAgentLinkInstall,
	}
	install.Flags().String("file", "", "Workspace-relative stackkit.home-agent-link-request/v1 JSON (no secret: the public key, the owner's kombify subject and the linked members' kombify subjects and Pocket ID usernames)")
	install.Flags().String("expect-sha256", "", "Required: the sha256:<hex> digest of the file's exact bytes (the request Techstack dispatched)")
	install.Flags().Duration("wait", 5*time.Minute, "How long to wait for the Owner's passkey step-up when one is needed; the request stays open after a shorter wait")
	install.Flags().Bool("owner-approve", false, "Approve pinning this key for agent access to this node (a new key or owner also needs the Owner's passkey step-up)")
	install.Flags().Bool("json", false, "Emit the pinned link as a command result")
	remove := &cobra.Command{
		Use:   "remove",
		Short: "Delete the pinned Gateway key; every agent envelope is refused afterwards",
		Args:  cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: runIdentityAgentLinkRemove,
	}
	remove.Flags().Bool("owner-approve", false, "Approve removing agent access to this node")
	remove.Flags().Bool("json", false, "Emit the result as a command result")
	status := &cobra.Command{
		Use:   "status",
		Short: "Show the pinned node, server, key thumbprint, generation and mapped accounts",
		Args:  cobra.NoArgs, SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			link, err := identity.LoadHomeAgentLink(getWorkDir())
			if errors.Is(err, identity.ErrHomeAgentLinkAbsent) {
				return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"linked": false})
			}
			if err != nil {
				return err
			}
			return writeCommandResult(cmd, cmd.CommandPath(), agentLinkResult(link))
		},
	}
	group.AddCommand(install, remove, status)
	identityCmd.AddCommand(group)
}

func runIdentityAgentLinkInstall(cmd *cobra.Command, _ []string) error {
	root := getWorkDir()
	approved, _ := cmd.Flags().GetBool("owner-approve")
	file, _ := cmd.Flags().GetString("file")
	if !approved || file == "" {
		return errors.New("agent-link install requires --file and --owner-approve")
	}
	custody, err := localevidence.LoadOwnerCustody(root)
	if err != nil {
		return errors.New("agent-link install runs on the host that holds this node's Owner custody")
	}
	if !filepath.IsLocal(file) {
		return errors.New("--file must be workspace-relative")
	}
	raw, err := os.ReadFile(filepath.Join(root, file)) //nolint:gosec // workspace-relative, checked above
	if err != nil {
		return err
	}
	digest := identity.HomeAgentLinkRequestDigest(raw)
	if expected, _ := cmd.Flags().GetString("expect-sha256"); expected != digest {
		return errors.New("the agent link request is not the one dispatched (--expect-sha256 is missing or differs)")
	}
	request, err := identity.DecodeHomeAgentLinkRequest(raw)
	if err != nil {
		return err
	}
	key, err := localevidence.ReadBasementRuntimePocketIDAdminKey(root)
	if err != nil {
		return errors.New("agent-link install needs this Authority's Pocket ID to resolve the linked members")
	}
	client := pocketid.NewClient(localowner.PocketIDLocalAPI(), key)
	accounts := identity.HomeAgentAccounts{
		// The owner's kombify subject maps to the Owner whose custody this host
		// holds, never to a username the request names.
		OwnerUsername: custody.PocketID.Username,
		Resolve: func(username string) (string, error) {
			users, findErr := client.FindUsersByUsername(cmd.Context(), username)
			if findErr != nil {
				return "", findErr
			}
			if len(users) != 1 || users[0].Disabled || users[0].ID == "" {
				return "", errors.New("no single enabled Pocket ID user has this username")
			}
			return users[0].ID, nil
		},
	}
	if identity.HomeAgentLinkPinned(root, request, accounts) {
		link, loadErr := identity.LoadHomeAgentLink(root)
		if loadErr != nil {
			return loadErr
		}
		result := agentLinkResult(link)
		result["unchanged"], result["requestSHA256"] = true, digest
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	// A request that could never be pinned (another server, an unknown or
	// escalating member, a lower generation, a retired key) is refused before
	// the Owner is asked anything.
	if err := identity.CheckHomeAgentLink(root, request, accounts); err != nil {
		return err
	}
	if identity.HomeAgentLinkNeedsOwner(root, request) {
		// Pinning a key, re-keying and changing the owner decide which
		// agents act for the Owner on this node: an Owner act on homelab
		// identity, approved with a passkey step-up at the Authority (homelab
		// identity target §10), never by Techstack's assertion alone.
		wait, _ := cmd.Flags().GetDuration("wait")
		stepUp, approved, err := awaitAgentLinkStepUp(cmd, root, request, wait)
		if err != nil {
			return err
		}
		if !approved {
			result := map[string]any{"linked": false, "pending": "owner_step_up", "stepUpRequest": stepUp.ID,
				"stepUpExpiresAt": stepUp.ExpiresAt, "requestSHA256": digest, "unchanged": false}
			if current, loadErr := identity.LoadHomeAgentLink(root); loadErr == nil {
				result["linked"], result["nodeId"], result["keyThumbprint"] = true, current.Key.Aud, current.Key.JKT
			}
			return writeCommandResult(cmd, cmd.CommandPath(), result)
		}
	}
	link, err := identity.InstallHomeAgentLink(root, request, accounts, time.Now())
	if err != nil {
		return err
	}
	result := agentLinkResult(link)
	result["unchanged"], result["requestSHA256"] = false, digest
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

// awaitAgentLinkStepUp raises, or reuses, the Owner step-up request bound to
// exactly this link request and waits up to wait for the Owner's passkey
// approval in the Hub. An approval is verified here (single use, at most 60
// seconds old). Without an answer in time the request stays open for its
// remaining life and the caller reports the install as pending.
func awaitAgentLinkStepUp(cmd *cobra.Command, root string, request identity.HomeAgentLinkRequest, wait time.Duration) (identity.HomeOwnerStepUpRequest, bool, error) {
	action, err := identity.HomeAgentLinkStepUpAction(request)
	if err != nil {
		return identity.HomeOwnerStepUpRequest{}, false, err
	}
	stepUp, open := identity.OpenHomeOwnerStepUpRequest(root, action, time.Now())
	if !open {
		jkt := request.AgentEnvelope.JKT
		if len(jkt) > 12 {
			jkt = jkt[:12]
		}
		summary := fmt.Sprintf("Let kombify agents reach this node as you: pin Gateway key %s (generation %d) for %d linked member(s)",
			jkt, request.AgentEnvelope.BindingGeneration, len(request.Members))
		if stepUp, err = identity.RequestHomeOwnerStepUp(root, action, summary, time.Now()); err != nil {
			return identity.HomeOwnerStepUpRequest{}, false, err
		}
		hub := "the Hub"
		if address, addressErr := localevidence.LocalIdentityRuntimeAddress(root); addressErr == nil {
			hub = "https://" + address.ServiceHost("base") + identity.HomeHubPathPrefix
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Approve \"%s\" with your passkey on an enrolled Owner device: open %s and choose Approve (request %s, valid for 5 minutes).\n", summary, hub, stepUp.ID)
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), wait)
	defer cancel()
	if _, err := identity.WaitHomeOwnerStepUp(ctx, root, stepUp, time.Second); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return stepUp, false, nil
		}
		return stepUp, false, fmt.Errorf("the Owner step-up for the agent link was not accepted: %w", err)
	}
	return stepUp, true, nil
}

func runIdentityAgentLinkRemove(cmd *cobra.Command, _ []string) error {
	root := getWorkDir()
	if approved, _ := cmd.Flags().GetBool("owner-approve"); !approved {
		return errors.New("agent-link remove requires --owner-approve")
	}
	if _, err := localevidence.LoadOwnerCustody(root); err != nil {
		return errors.New("agent-link remove runs on the host that holds this node's Owner custody")
	}
	removed, err := identity.RemoveHomeAgentLink(root)
	if err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), map[string]any{"removed": removed, "linked": false})
}

func agentLinkResult(link identity.HomeAgentLink) map[string]any {
	accounts := make([]string, 0, len(link.Subjects))
	for _, subject := range link.Subjects {
		accounts = append(accounts, subject.Account)
	}
	return map[string]any{
		"linked": true, "nodeId": link.Key.Aud, "serverId": link.ServerID, "tenantId": link.TenantID,
		"keyThumbprint": link.Key.JKT, "bindingGeneration": link.Key.BindingGeneration, "accounts": accounts, "installedAt": link.InstalledAt,
	}
}
