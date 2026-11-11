// Package routerdiagnostics reads the Home router only through the node daemon
// and the container's loopback. It exposes no arbitrary URL or container input.
package routerdiagnostics

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const HomePing = "/ping"
const HomeRouters = "/api/http/routers?per_page=1000"
const HomeMCPRoute = "/api/http/routers/stackkit-mcp@docker"

func ReadHome(ctx context.Context, resource string) ([]byte, error) {
	switch resource {
	case HomePing, HomeRouters, HomeMCPRoute:
	default:
		return nil, errors.New("unadmitted Home router diagnostic")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	query := exec.CommandContext(ctx, "docker", "--host", "unix:///var/run/docker.sock", "ps", "--filter", "label=com.docker.compose.project=stackkit-basement-core", "--filter", "label=com.docker.compose.service=router", "--format", "{{.ID}}", "--no-trunc")
	query.Env = []string{"LANG=C", "LC_ALL=C"}
	ids := boundedOutput{remaining: 256}
	query.Stdout = &ids
	if e := query.Run(); e != nil {
		return nil, errors.New("observe the exact Home router for node-local diagnostics")
	}
	id := strings.TrimSpace(ids.buffer.String())
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(id) {
		return nil, errors.New("exactly one running Home router is required")
	}
	command := exec.CommandContext(ctx, "docker", "--host", "unix:///var/run/docker.sock", "exec", id, "wget", "-qO-", "-T", "5", "http://127.0.0.1:8080"+resource)
	command.Env = []string{"LANG=C", "LC_ALL=C"}
	output := boundedOutput{remaining: 4 << 20}
	command.Stdout = &output
	if e := command.Run(); e != nil {
		return nil, errors.New("read the router API through its container loopback")
	}
	if output.buffer.Len() == 0 {
		return nil, errors.New("empty Home router API response")
	}
	return output.buffer.Bytes(), nil
}

type boundedOutput struct {
	buffer    bytes.Buffer
	remaining int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > b.remaining {
		return 0, errors.New("router diagnostics output exceeds its bound")
	}
	b.remaining -= len(p)
	return b.buffer.Write(p)
}
