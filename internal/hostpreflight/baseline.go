package hostpreflight

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Baseline is scoped evidence, never host-wide absence or permission to adopt.
// Its own version lets consumers distinguish older reports without this evidence.
type Baseline struct {
	SchemaVersion    string          `json:"schemaVersion"`
	Revision         string          `json:"revision"`
	PlanHash         string          `json:"planHash,omitempty"`
	NodeRef          string          `json:"nodeRef,omitempty"`
	ObservedAt       time.Time       `json:"observedAt"`
	ExpiresAt        time.Time       `json:"expiresAt"`
	BootID           string          `json:"bootId,omitempty"`
	NetworkNamespace string          `json:"networkNamespace,omitempty"`
	Scopes           []BaselineScope `json:"scopes"`
}

type BaselineScope struct {
	Kind     string   `json:"kind"`
	Coverage string   `json:"coverage"`
	Source   string   `json:"source"`
	Records  []string `json:"records"`
}

// boundedProbeOutput limits retained data as well as command time; exceeding the
// budget is incomplete evidence, not a successful empty response.
func boundedProbeOutput(ctx context.Context, directory, command string, args ...string) ([]byte, bool) {
	bounded, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(bounded, command, args...)
	cmd.Dir = directory
	buffer := &probeBuffer{}
	cmd.Stdout = buffer
	err := cmd.Run()
	return buffer.Bytes(), err == nil && bounded.Err() == nil
}

type probeBuffer struct{ bytes.Buffer }

func (b *probeBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, errors.New("host evidence limit exceeded")
	}
	return b.Buffer.Write(p)
}

func observeBaseline(ctx context.Context, request ObserveRequest) *Baseline {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	now := time.Now().UTC()
	baseline := &Baseline{SchemaVersion: "stackkit.host-baseline/v1", PlanHash: request.PlanHash, NodeRef: request.NodeRef, ObservedAt: now, ExpiresAt: now.Add(90 * time.Second)}
	boot, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	baseline.BootID = strings.TrimSpace(string(boot))
	baseline.NetworkNamespace, _ = os.Readlink("/proc/self/ns/net")
	for _, probe := range []struct {
		kind, command string
		args          []string
	}{
		{"listeners", "ss", []string{"-H", "-l", "-n", "-t", "-u"}},
		{"services", "systemctl", []string{"list-units", "--all", "--type=service", "--no-legend", "--no-pager", "--plain"}},
		{"socket-intents", "systemctl", []string{"list-sockets", "--all", "--no-legend", "--no-pager", "--plain"}},
		{"containers", "docker", []string{"ps", "--all", "--no-trunc", "--format", "{{.ID}}\t{{.Names}}\t{{.Image}}\t{{.State}}\t{{.Ports}}"}},
		{"volumes", "docker", []string{"volume", "ls", "--format", "{{.Name}}\t{{.Driver}}\t{{.Scope}}"}},
	} {
		data, ok := boundedProbeOutput(ctx, request.WorkspacePath, probe.command, probe.args...)
		scope := BaselineScope{Kind: probe.kind, Source: probe.command, Coverage: "unknown", Records: []string{}}
		if ok {
			// These commands cover their named runtime/namespace, not every
			// possible application, inactive unit file, or network namespace.
			scope.Coverage = "scoped"
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				if line != "" {
					scope.Records = append(scope.Records, line)
				}
			}
		}
		baseline.Scopes = append(baseline.Scopes, scope)
	}
	// Dependencies and external exposure need their own owning adapters. Never
	// infer either from a port number, service name, or wildcard bind address.
	for _, kind := range []string{"dependencies", "exposure"} {
		baseline.Scopes = append(baseline.Scopes, BaselineScope{Kind: kind, Coverage: "unknown", Source: "unobserved", Records: []string{}})
	}
	raw, _ := json.Marshal(baseline)
	digest := sha256.Sum256(raw)
	baseline.Revision = "sha256:" + hex.EncodeToString(digest[:])
	return baseline
}
