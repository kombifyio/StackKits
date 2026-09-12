package backupexec

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/localbackuppolicy"
)

// CleanupCloudVerificationRestore removes only the disposable Cloud verification
// operation, after revalidating the exact Kopia container and staging mount.
// Owner recovery operation IDs cannot cross this closed namespace.
func CleanupCloudVerificationRestore(ctx context.Context, policy localbackuppolicy.Policy, operation string) error {
	return cleanupCloudVerificationRestore(ctx, policy, operation, func(timeout time.Duration) dockerV2Client { return docker.NewLocalClient(docker.WithTimeout(timeout)) })
}

func cleanupCloudVerificationRestore(ctx context.Context, policy localbackuppolicy.Policy, operation string, newClient dockerV2ClientFactory) error {
	leaf := strings.TrimPrefix(operation, "cloud-offsite-")
	decoded, err := hex.DecodeString(leaf)
	if err != nil || len(decoded) != 32 || leaf != strings.ToLower(leaf) || operation != "cloud-offsite-"+leaf {
		return errors.New("cleanup requires a Cloud verification operation")
	}
	if _, err := localbackuppolicy.ArtifactBytes(policy); err != nil {
		return err
	}
	if policy.Source.CoreModuleRef != localbackuppolicy.CloudCoreModuleRef {
		return errors.New("cleanup requires the exact Cloud backup source")
	}
	client, container, err := inspectDockerV2SourceRuntime(ctx, newClient, policy.SourceProjection())
	if err != nil {
		return err
	}
	// rm never follows links within the tree. Reject a substituted root/leaf,
	// and pass the derived path as an argument rather than shell interpolation.
	const cleanup = `target=$1
test -d /restore-staging && test ! -L /restore-staging
test ! -L "$target"
rm -rf -- "$target"
test ! -e "$target" && test ! -L "$target"`
	_, err = client.ExecWithStdin(ctx, container.ID, []string{"/bin/sh", "-ceu", cleanup, "--", localbackuppolicy.RestorePathForOperation(operation)}, nil)
	if err != nil {
		return errors.New("Cloud verification staging cleanup did not confirm absence")
	}
	return nil
}
