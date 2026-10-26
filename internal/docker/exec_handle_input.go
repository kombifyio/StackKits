package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"time"
)

// PrepareInput is a dormant exact command like Prepare, with stdin enabled.
// Sensitive input is NOT part of this request or Docker's retained ExecConfig.
// Callers persist this handle and public command/nonce before StartInput ONCE.
func (c *ExecHandleClient) PrepareInput(ctx context.Context, container string, command []string) (string, error) {
	if !fullDockerID.MatchString(container) || len(command) == 0 {
		return "", errors.New("secret-input exec requires exact container/command")
	}
	if err := c.Qualify(ctx); err != nil {
		return "", err
	}
	var reply struct {
		ID string `json:"Id"`
	}
	err := c.request(ctx, http.MethodPost, "containers/"+container+"/exec", struct {
		Cmd                                                      []string
		AttachStdin, AttachStdout, AttachStderr, Tty, Privileged bool
	}{Cmd: command, AttachStdin: true, AttachStdout: true, AttachStderr: true}, &reply)
	if err != nil {
		return "", err
	}
	if !fullDockerID.MatchString(reply.ID) {
		return "", errors.New("missing exact input exec handle")
	}
	return reply.ID, nil
}

// StartInput uses the SAME qualified local Docker exec API and a bounded
// attached stream, not a shell/docker-exec replacement. It never retries or
// captures stdout/stderr. Lost upgrade/write/response remains unknown. Socket
// closure cancels observation, and does not assert that the child was killed.
func (c *ExecHandleClient) StartInput(ctx context.Context, handle string, command []string, input []byte) error {
	if c == nil || !fullDockerID.MatchString(handle) || len(input) == 0 || len(input) > 65536 {
		return errors.New("invalid bounded exec input")
	}
	for _, secret := range sensitiveValues(input) {
		for _, arg := range command {
			if bytes.Contains([]byte(arg), secret) {
				return errors.New("secret must not appear in retained exec command")
			}
		}
	}
	// Start is deliberately one raw local-socket request, with no redirects,
	// proxy environment, reconnect or retry. Qualified Engine supports hijack.
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", "/var/run/docker.sock")
	if err != nil {
		return errors.New("local exec input connection failed")
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Minute)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	body, _ := json.Marshal(struct{ Detach, Tty bool }{false, false})
	request, _ := http.NewRequest(http.MethodPost, "http://docker/v1.40/exec/"+handle+"/start", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "tcp")
	if err = request.Write(conn); err != nil {
		return errors.New("exec start write unconfirmed")
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, request)
	if err != nil {
		return errors.New("exec start acknowledgment unconfirmed")
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		return fmt.Errorf("qualified attached exec upgrade refused HTTP%d", response.StatusCode)
	}
	// No payload exists before the upgrade; neither Docker's create JSON nor
	// argv/environment/journal ever contains the credential.
	copyInput := append([]byte(nil), input...)
	defer clear(copyInput)
	if _, err = conn.Write(copyInput); err != nil {
		return errors.New("exec secret delivery unconfirmed")
	}
	if unix, ok := conn.(*net.UnixConn); ok {
		if err = unix.CloseWrite(); err != nil {
			return errors.New("exec input EOF unconfirmed")
		}
	} else {
		return errors.New("exec transport is not local unix")
	}
	// Hard output cap; fixed helper emits only a small public result. Discard
	// even protocol errors, so no echoed credential reaches logs/receipts.
	_, err = io.Copy(io.Discard, io.LimitReader(reader, 1<<20))
	if err != nil {
		return errors.New("exec completion stream unconfirmed")
	}
	return nil
}

// InputExit observes the original attached-input handle, retaining Complete's
// exact container/command checks. A dormant exit0 and a running process are
// unknown. Only the qualified closed helper's nonzero exit can establish that
// the outer process ended; its exit code alone proves no native API actuation.
// Legacy Complete semantics and its callers are unchanged.
func (c *ExecHandleClient) InputExit(ctx context.Context, handle, container string, command []string) (int, bool, error) {
	if !fullDockerID.MatchString(handle) || !fullDockerID.MatchString(container) || len(command) == 0 {
		return 0, false, errors.New("input handle binding incomplete")
	}
	var observed struct {
		ID            string `json:"ID"`
		ContainerID   string
		Running       bool
		ExitCode      *int
		ProcessConfig struct {
			Entrypoint string   `json:"entrypoint"`
			Arguments  []string `json:"arguments"`
			Privileged bool     `json:"privileged"`
			Tty        bool     `json:"tty"`
		}
	}
	if err := c.request(ctx, http.MethodGet, "exec/"+handle+"/json", nil, &observed); err != nil {
		return 0, false, err
	}
	if observed.ID != handle || observed.ContainerID != container || observed.ProcessConfig.Privileged || observed.ProcessConfig.Tty || observed.ProcessConfig.Entrypoint != command[0] || !reflect.DeepEqual(observed.ProcessConfig.Arguments, command[1:]) {
		return 0, false, errors.New("input handle differs from exact retained command/container")
	}
	if observed.Running || observed.ExitCode == nil || *observed.ExitCode == 0 {
		return 0, false, nil
	}
	return *observed.ExitCode, true, nil
}

// InspectMountProcess is a closed read-only projection for the fixed local
// daemon's exact container. A dead/unknown PID cannot prove a writable bind's
// kernel source. It does not create/start an exec or expose container secrets.
func (c *ExecHandleClient) InspectMountProcess(ctx context.Context, container string) (int, error) {
	if c == nil || !fullDockerID.MatchString(container) {
		return 0, errors.New("exact mount-observation container required")
	}
	var out struct {
		ID    string `json:"Id"`
		State struct {
			PID                         int `json:"Pid"`
			Running, Paused, Restarting bool
		}
	}
	if err := c.request(ctx, http.MethodGet, "containers/"+container+"/json", nil, &out); err != nil {
		return 0, errors.New("container mount process inspection unconfirmed")
	}
	if out.ID != container || out.State.PID < 1 || !out.State.Running && !out.State.Paused && !out.State.Restarting {
		return 0, errors.New("container mount process is unavailable")
	}
	return out.State.PID, nil
}

// ListMountContainers enumerates every local container without label/status
// filters. Exact IDs and a bounded decoded response prevent a partial inventory
// from qualifying writer absence. This is a read-only owner inspection.
func (c *ExecHandleClient) ListMountContainers(ctx context.Context) ([]string, error) {
	var rows []struct {
		ID string `json:"Id"`
	}
	if err := c.request(ctx, http.MethodGet, "containers/json?all=true", nil, &rows); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if !fullDockerID.MatchString(row.ID) || seen[row.ID] {
			return nil, errors.New("local mount inventory has ambiguous container identity")
		}
		seen[row.ID] = true
		ids = append(ids, row.ID)
	}
	return ids, nil
}
