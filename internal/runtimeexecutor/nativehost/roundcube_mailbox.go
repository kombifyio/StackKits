package nativehost

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/kombifyio/stackkits/internal/architecturev2renderer"
)

// roundcubeMailboxEndpointPattern mirrors the governed mailbox.sh check: a
// Roundcube host URI with an explicit TLS mode, plain DNS name or IPv4
// address, and port. Nothing else ever reaches the container argument list.
var roundcubeMailboxEndpointPattern = regexp.MustCompile(`^(ssl|tls)://[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?:[0-9]{1,5}$`)

// ConfigureStandaloneComposeRoundcubeMailbox stores the owner's IMAP and SMTP
// endpoints on the Mail workload's persistent mailbox volume through the
// governed mailbox.sh, keeping the previous endpoints for Revert. The call is
// closed: only the admitted Roundcube deployment, its fixed component and
// script, and two validated endpoint URIs. No password is ever passed.
func ConfigureStandaloneComposeRoundcubeMailbox(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, imapURI, smtpURI string) error {
	if !roundcubeMailboxEndpointPattern.MatchString(imapURI) || !roundcubeMailboxEndpointPattern.MatchString(smtpURI) {
		return errors.New("mailbox endpoints must be ssl:// or tls:// host:port URIs")
	}
	return runRoundcubeMailboxScript(ctx, workspace, deployment, "set", imapURI, smtpURI)
}

// RevertStandaloneComposeRoundcubeMailbox restores the endpoints that were
// active before the last Configure call, or removes them when none were.
func RevertStandaloneComposeRoundcubeMailbox(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment) error {
	return runRoundcubeMailboxScript(ctx, workspace, deployment, "revert")
}

func runRoundcubeMailboxScript(ctx context.Context, workspace string, deployment SelectedPaaSWorkloadDeployment, operation ...string) error {
	operations, err := NewOSStandaloneComposeWorkloadOperations(workspace)
	if err != nil {
		return err
	}
	o, ok := operations.(*osStandaloneComposeWorkloadOperations)
	if !ok {
		return errors.New("standalone Compose operations have an unexpected implementation")
	}
	if deployment.ModuleRef != roundcubeWorkloadModuleRef {
		return errors.New("mailbox endpoints belong only to the Roundcube Mail workload")
	}
	project, err := o.prepare(ctx, deployment)
	if err != nil {
		return err
	}
	if err := o.verifyPersisted(project); err != nil {
		return err
	}
	if project.bundle.ModuleRef != roundcubeWorkloadModuleRef || project.bundle.EntryComponent != "roundcube" {
		return errors.New("mailbox endpoints belong only to the Roundcube Mail workload")
	}
	args := []string{
		"compose", "--project-name", project.name, "--env-file", filepath.Join(project.directory, ".env"),
		"-f", filepath.Join(project.directory, "compose.yaml"),
		"exec", "-T", "roundcube", "/bin/sh", architecturev2renderer.RoundcubeMailboxScriptPath,
	}
	args = append(args, operation...)
	if _, err := o.runner.Run(ctx, args, project.directory); err != nil {
		return fmt.Errorf("store the mailbox endpoints in the Roundcube workload: %w", err)
	}
	return nil
}
