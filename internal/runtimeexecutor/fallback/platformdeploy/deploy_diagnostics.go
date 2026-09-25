package platformdeploy

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// diagRunner runs a single diagnostic command and returns its combined output.
// Injectable so the bundle assembly is unit-testable without a live host.
type diagRunner func(ctx context.Context, name string, args ...string) (string, error)

// dockerDiagRunner returns a diagRunner that shells out using the adapter's
// docker environment, so DOCKER_HOST/context match the actual deploy path.
func dockerDiagRunner(dockerEnv []string) diagRunner {
	return func(ctx context.Context, name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed diagnostic commands, no user input
		cmd.Env = dockerCommandEnv(dockerEnv)
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
}

// gatherDeployFailureDiagnostics collects an on-host diagnostic bundle for a
// failed platform deploy so the failure names its cause. It captures host disk
// usage, docker's disk usage, and the failing compose project's containers plus
// their recent logs — the exact facts (e.g. "No space left on device" during an
// image pull) that are otherwise buried in Coolify's internal database and made
// a disk-full failure look like a bare "docker:missing containers". Best-effort,
// bounded, and redacted; returns "" if nothing useful was collected.
func gatherDeployFailureDiagnostics(ctx context.Context, ref DeploymentRef, run diagRunner) string {
	if run == nil {
		return ""
	}
	var b strings.Builder
	section := func(title, name string, args ...string) {
		out, err := run(ctx, name, args...)
		if strings.TrimSpace(out) == "" && err != nil {
			out = err.Error()
		}
		if strings.TrimSpace(out) == "" {
			return
		}
		fmt.Fprintf(&b, "## %s\n%s\n\n", title, redactDiag(truncateDiag(out, 2000)))
	}

	// Always-safe, high-signal: disk is the #1 silent cause of "missing containers".
	section("host disk (df -h)", "df", "-h")
	section("docker disk (docker system df)", "docker", "system", "df")

	if id := strings.TrimSpace(ref.ExternalID); id != "" {
		filter := "label=com.docker.compose.project=" + id
		section("failed project containers (docker ps -a)", "docker", "ps", "-a",
			"--filter", filter, "--format", "{{.Names}}\t{{.Status}}")
		// Recent logs of the failed project's containers (bounded).
		if idsOut, err := run(ctx, "docker", "ps", "-aq", "--filter", filter); err == nil {
			ids := strings.Fields(idsOut)
			if len(ids) > 6 {
				ids = ids[:6]
			}
			for _, cid := range ids {
				section("logs "+cid, "docker", "logs", "--tail", "40", cid)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// diagSecretRE masks obvious credentials that may appear in container logs.
var diagSecretRE = regexp.MustCompile(`(?i)(password|passwd|secret|token|api[_-]?key|authorization|bearer)([=:"'\s]+)\S+`)

func redactDiag(s string) string {
	return diagSecretRE.ReplaceAllString(s, "$1$2***")
}

func truncateDiag(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… (truncated)"
}
