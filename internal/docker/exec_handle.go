package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"runtime"
	"time"
)

var fullDockerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ExecHandleClient exposes Docker's dormant exec handle separately from its
// single start request. It is local-only and ignores proxy/DOCKER_HOST state.
// It never controls the lifetime of the container in which the command runs.
type ExecHandleClient struct{ client *http.Client }

func NewLocalExecHandleClient() (*ExecHandleClient, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("durable native exec requires the installed Linux node's local Docker socket")
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}
	return &ExecHandleClient{client: &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *ExecHandleClient) request(ctx context.Context, method, path string, input, output any) error {
	if c == nil || c.client == nil {
		return errors.New("local Docker exec client is unavailable")
	}
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker/v1.40/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("local Docker exec %s returned HTTP%d", method, resp.StatusCode)
	}
	if output == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return err
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(output)
}

// Prepare creates a dormant exec only. The caller persists the returned full
// handle and expected command in its existing signed operation before Start.
func (c *ExecHandleClient) Prepare(ctx context.Context, container string, command []string) (string, error) {
	if !fullDockerID.MatchString(container) || len(command) == 0 {
		return "", errors.New("native exec requires an exact container and command")
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
	}{Cmd: command}, &reply)
	if err != nil {
		return "", err
	}
	if !fullDockerID.MatchString(reply.ID) {
		return "", errors.New("Docker returned no exact exec handle")
	}
	return reply.ID, nil
}

// Start is one request, with no retries. A lost response must be inspected
// through the same handle; it must not trigger another Start or Prepare.
func (c *ExecHandleClient) Start(ctx context.Context, handle string) error {
	if !fullDockerID.MatchString(handle) {
		return errors.New("invalid native exec handle")
	}
	return c.request(ctx, http.MethodPost, "exec/"+handle+"/start", struct{ Detach, Tty bool }{Detach: true}, nil)
}

// Complete admits only a distinctive command-defined successful exit code.
// A dormant exec's Running=false/ExitCode=0 is deliberately not success.
// The command must emit that code only after synchronous native actuation.
func (c *ExecHandleClient) Complete(ctx context.Context, handle, container string, command []string, successCode int) (bool, error) {
	if !fullDockerID.MatchString(handle) || !fullDockerID.MatchString(container) || len(command) == 0 || successCode < 1 || successCode > 125 {
		return false, errors.New("native completion binding is incomplete")
	}
	var inspect struct {
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
	if err := c.request(ctx, http.MethodGet, "exec/"+handle+"/json", nil, &inspect); err != nil {
		return false, err
	}
	if inspect.ID != handle || inspect.ContainerID != container || inspect.ProcessConfig.Privileged || inspect.ProcessConfig.Tty || inspect.ProcessConfig.Entrypoint != command[0] || !reflect.DeepEqual(inspect.ProcessConfig.Arguments, command[1:]) {
		return false, errors.New("native exec handle differs from the retained container/command")
	}
	if inspect.Running || inspect.ExitCode == nil || *inspect.ExitCode == 0 {
		return false, nil
	}
	if *inspect.ExitCode != successCode {
		return false, fmt.Errorf("native control exited without its completion acknowledgement (code%d)", *inspect.ExitCode)
	}
	return true, nil
}

// Qualify rejects unreviewed exec semantics before a lifecycle records intent.
func (c *ExecHandleClient) Qualify(ctx context.Context) error {
	var version struct{ Version, GitCommit, ApiVersion string }
	if err := c.request(ctx, http.MethodGet, "version", nil, &version); err != nil {
		return err
	}
	if !(version.Version == "29.9.0" && version.GitCommit == "a5b58c9" || version.Version == "29.8.0" && version.GitCommit == "3ce5872") {
		return errors.New("native completion requires a qualified Docker exec implementation")
	}

	return nil
}
