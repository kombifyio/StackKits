package servicecontrol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/logging"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/fallback/platformdeploy"
	"github.com/kombifyio/stackkits/internal/runtimeexecutor/nativehost"
)

// coolifyServiceIDPattern bounds the signed Coolify service UUID before it
// becomes a Docker label filter: Coolify runs every service as its own Compose
// project named by that UUID.
var coolifyServiceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func runCoolifyService(ctx context.Context, request runtimeCommandRequest) (runtimeCommandOutput, error) {
	// Re-read signed custody after target resolution, including for logs. A
	// transfer, removal or replacement invalidates the queued request.
	state, err := platformdeploy.LoadOwnerHostDeployment(request.Workspace, "stackkit-"+request.RuntimeRef)
	if err != nil {
		return runtimeCommandOutput{}, err
	}
	if err := platformdeploy.RequireHostDeploymentManager(state, "coolify"); err != nil {
		return runtimeCommandOutput{}, err
	}
	if state.Signature == nil || request.CustodyDigest == "" || hostDeploymentDigest(state) != request.CustodyDigest || state.Deployment.ExternalID != request.ExternalID {
		return runtimeCommandOutput{}, errors.New("Coolify service custody changed during service control")
	}
	cfg, ref, err := nativehost.LoadOwnedCoolifyServiceConfig(ctx, request.Workspace, "stackkit-"+request.RuntimeRef)
	if err != nil {
		return runtimeCommandOutput{}, err
	}
	if ref.Platform != state.Deployment.Platform || ref.AppName != state.Deployment.AppName || ref.ExternalID != state.Deployment.ExternalID {
		return runtimeCommandOutput{}, errors.New("Coolify service identity changed during service control")
	}
	request.HTTPConfig = cfg
	action := request.Args[0]
	if action == ActionLogs {
		return runCoolifyLogs(ctx, request)
	}
	if action != ActionStop && action != ActionStart && action != ActionRestart {
		return runtimeCommandOutput{}, errors.New("Coolify service control requires logs, start, stop or restart")
	}
	adapter := platformdeploy.NewCoolifyAdapter(request.HTTPConfig)
	// ServiceComponents includes the service's database components (for example
	// Immich's postgres), which Coolify lists outside its applications; the
	// quiesce path controls the same set.
	components, err := adapter.ServiceComponents(ctx, ref)
	if err != nil {
		return runtimeCommandOutput{}, err
	}
	selected := map[string]string{}
	for _, name := range request.ComponentRefs {
		for _, component := range components {
			if component.Name == name {
				if selected[name] != "" {
					return runtimeCommandOutput{}, errors.New("Coolify service component is ambiguous")
				}
				selected[name] = component.UUID
			}
		}
		if selected[name] == "" {
			return runtimeCommandOutput{}, errors.New("Coolify service has no declared component")
		}
	}
	phases := []string{action}
	if action == ActionRestart {
		phases = []string{ActionStop, ActionStart}
	}
	for _, phase := range phases {
		for _, name := range request.ComponentRefs {
			if err := adapter.ControlServiceComponent(ctx, ref, selected[name], phase); err != nil {
				return runtimeCommandOutput{}, err
			}
		}
		if err := waitCoolifyServicePhase(ctx, adapter, ref, selected, phase); err != nil {
			return runtimeCommandOutput{}, err
		}
	}
	observedState := DesiredRunning
	if action == ActionStop {
		observedState = DesiredStopped
	}
	return runtimeCommandOutput{ObservedState: observedState}, nil
}

// runCoolifyLogs reads one bounded, non-following log tail of the exact
// component containers. Coolify exposes no log endpoint for service
// components, so the Docker engine of the node is the reader, on the Compose
// project that the signed Coolify service UUID names.
func runCoolifyLogs(ctx context.Context, request runtimeCommandRequest) (runtimeCommandOutput, error) {
	if !coolifyServiceIDPattern.MatchString(request.ExternalID) || len(request.Args) < 6 {
		return runtimeCommandOutput{}, errors.New("Coolify service logs require the retained service identity")
	}
	tail, err := strconv.Atoi(request.Args[4])
	if err != nil || tail < 1 || tail > 200 {
		return runtimeCommandOutput{}, errors.New("Coolify service logs require a tail between 1 and 200")
	}
	type line struct {
		at   time.Time
		text string
	}
	var lines []line
	remaining := maxCoolifyLogBytes
	retainedBytes := 0
	for _, name := range request.ComponentRefs {
		ids, err := dockerOutput(ctx, &remaining, "ps", "--all", "--quiet", "--no-trunc",
			"--filter", "label=com.docker.compose.project="+request.ExternalID,
			"--filter", "label=com.docker.compose.service="+name)
		if err != nil {
			return runtimeCommandOutput{}, err
		}
		containers := strings.Fields(string(ids))
		if len(containers) == 0 {
			return runtimeCommandOutput{}, errors.New("Coolify service has no container for a declared component")
		}
		for _, id := range containers {
			raw, err := dockerOutput(ctx, &remaining, "logs", "--timestamps", "--tail", strconv.Itoa(tail), id)
			if err != nil {
				return runtimeCommandOutput{}, err
			}
			for text := range strings.SplitSeq(strings.TrimRight(string(raw), "\n"), "\n") {
				if text == "" {
					continue
				}
				entry := line{text: name + " | " + text}
				retainedBytes += len(entry.text) + 1
				if retainedBytes > maxCoolifyLogBytes || len(lines) >= maxCoolifyLogLines {
					return runtimeCommandOutput{}, errors.New("Coolify service log tail exceeds its total output bound")
				}
				if field, _, ok := strings.Cut(text, " "); ok {
					if parsed, err := time.Parse(time.RFC3339Nano, field); err == nil {
						entry.at = parsed
					}
				}
				lines = append(lines, entry)
			}
		}
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].at.Before(lines[j].at) })
	var out strings.Builder
	for _, entry := range lines {
		out.WriteString(entry.text)
		out.WriteByte('\n')
	}
	return runtimeCommandOutput{Bytes: []byte(out.String())}, nil
}

const (
	maxCoolifyLogBytes   = 4 << 20
	maxCoolifyLogLines   = 4096
	maxCoolifyErrorBytes = 4096
)

func hostDeploymentDigest(state platformdeploy.HostDeployment) string {
	payload, _ := json.Marshal(state)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// Docker stdout and stderr share one writer and one budget across all
// components/containers. CombinedOutput would allocate before applying a cap.
type coolifyOutputBuffer struct {
	buffer    bytes.Buffer
	remaining *int
}

func (b *coolifyOutputBuffer) Write(p []byte) (int, error) {
	if len(p) > *b.remaining {
		return 0, errors.New("Coolify Docker output exceeds its total byte bound")
	}
	n, err := b.buffer.Write(p)
	*b.remaining -= n
	return n, err
}

func dockerOutput(ctx context.Context, remaining *int, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "docker", args...) //nolint:gosec // finite arguments built from validated labels and container IDs
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output := &coolifyOutputBuffer{remaining: remaining}
	command.Stdout, command.Stderr = output, output
	if err := command.Run(); err != nil {
		excerpt := logging.RedactText(output.buffer.String())
		if len(excerpt) > maxCoolifyErrorBytes {
			excerpt = excerpt[:maxCoolifyErrorBytes] + " [truncated]"
		}
		return nil, fmt.Errorf("docker %s: %w: %s", args[0], err, excerpt)
	}
	return output.buffer.Bytes(), nil
}

func waitCoolifyServicePhase(ctx context.Context, adapter *platformdeploy.CoolifyAdapter, ref platformdeploy.DeploymentRef, selected map[string]string, phase string) error {
	for {
		components, err := adapter.ServiceComponents(ctx, ref)
		if err != nil {
			return err
		}
		ready := true
		for name, uuid := range selected {
			found := false
			for _, component := range components {
				if component.UUID != uuid || component.Name != name {
					continue
				}
				found = true
				if phase == ActionStop {
					ready = ready && strings.HasPrefix(component.Status, "exited")
				} else {
					ready = ready && strings.HasPrefix(component.Status, "running")
				}
			}
			if !found {
				return errors.New("Coolify service component identity changed")
			}
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
