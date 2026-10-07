package appsetup

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// pinnedGameImage matches a curated game image pinned by digest.
var pinnedGameImage = regexp.MustCompile(`^([a-z0-9][a-z0-9._/-]*)(?::[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?@(sha256):([a-f0-9]{64})$`)

// localGameImage is the digest-derived local name of a pinned game image.
// Calagopus Wings splits an image reference at its last colon, so it cannot
// pull "name@sha256:..." (ADR-0048). The setup action pulls the exact digest
// and tags it under this name; Wings' pull of the unresolvable registry host
// fails and it uses the local image, which stays exactly the pinned digest.
func localGameImage(reference string) (string, string, error) {
	match := pinnedGameImage.FindStringSubmatch(reference)
	if match == nil {
		return "", "", fmt.Errorf("game image %q is not pinned by digest", reference)
	}
	pinned := match[1] + "@" + match[2] + ":" + match[3]
	return pinned, "stackkit.local/" + match[1] + ":" + match[2] + "-" + match[3], nil
}

// prepareLocalGameImage pulls the pinned digest on the node's Docker daemon
// and tags it under its local name.
func prepareLocalGameImage(ctx context.Context, reference string) (string, error) {
	pinned, local, err := localGameImage(reference)
	if err != nil {
		return "", err
	}
	for _, args := range [][]string{{"pull", "--quiet", pinned}, {"tag", pinned, local}} {
		command := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...) //nolint:gosec // validated pinned reference
		if output, err := command.CombinedOutput(); err != nil {
			return "", fmt.Errorf("prepare game image %s: docker %s: %w: %s", pinned, args[0], err, strings.TrimSpace(string(output)))
		}
	}
	return local, nil
}
