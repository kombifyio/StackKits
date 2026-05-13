package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	stackverify "github.com/kombifyio/stackkits/internal/verify"
)

func TestVerifyCommandRegistered(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"verify"})
	if err != nil {
		t.Fatalf("rootCmd.Find verify: %v", err)
	}
	if cmd.Name() != "verify" {
		t.Fatalf("command name = %q", cmd.Name())
	}
	for _, flag := range []string{"json", "http", "strict", "host", "user", "key", "port", "remote-dir"} {
		if cmd.Flag(flag) == nil {
			t.Fatalf("verify flag --%s is not registered", flag)
		}
	}
}

func TestApplyCommandHasPostDeployVerifyFlags(t *testing.T) {
	cmd, _, err := rootCmd.Find([]string{"apply"})
	if err != nil {
		t.Fatalf("rootCmd.Find apply: %v", err)
	}
	for _, flag := range []string{"verify", "verify-http", "verify-strict"} {
		if cmd.Flag(flag) == nil {
			t.Fatalf("apply flag --%s is not registered", flag)
		}
	}
}

func TestPrintVerifyReportText(t *testing.T) {
	var buf bytes.Buffer
	report := stackverify.Report{
		Status:      stackverify.StatusWarn,
		StackKit:    "base-kit",
		Mode:        "simple",
		GeneratedAt: time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC),
		Checks: []stackverify.Check{
			{Name: "spec", Status: stackverify.StatusPass, Message: "stack spec loaded"},
			{Name: "health:pocketid", Status: stackverify.StatusWarn, Target: "pocketid", Message: "container has no Docker healthcheck"},
		},
	}

	printVerifyReport(&buf, report)

	got := buf.String()
	for _, want := range []string{"Verify: warn", "StackKit: base-kit", "- spec: pass", "- health:pocketid: warn"} {
		if !strings.Contains(got, want) {
			t.Fatalf("verify output missing %q:\n%s", want, got)
		}
	}
}

func TestEmitVerifyReportJSONReturnsErrorOnFailure(t *testing.T) {
	var buf bytes.Buffer
	report := stackverify.Report{
		Status: stackverify.StatusFail,
		Checks: []stackverify.Check{{
			Name:    "deployment-state",
			Status:  stackverify.StatusFail,
			Message: "missing",
		}},
	}

	err := emitVerifyReport(&buf, report, true)
	if err == nil {
		t.Fatal("expected failed report to return an error")
	}
	var decoded stackverify.Report
	if decodeErr := json.Unmarshal(buf.Bytes(), &decoded); decodeErr != nil {
		t.Fatalf("invalid json output: %v", decodeErr)
	}
	if decoded.Status != stackverify.StatusFail {
		t.Fatalf("decoded status = %q", decoded.Status)
	}
}

func TestBuildRemoteVerifyCommandQuotesInputs(t *testing.T) {
	command := buildRemoteVerifyCommand(remoteVerifyOptions{
		RemoteDir: "/opt/stack kit",
		SpecFile:  "spec's.yaml",
		HTTP:      true,
		Strict:    true,
	})

	want := "cd '/opt/stack kit' && stackkit verify --json --spec 'spec'\"'\"'s.yaml' --http --strict"
	if command != want {
		t.Fatalf("command = %q, want %q", command, want)
	}
}

func TestRunRemoteVerifyUsesSSHClientAndReturnsParsedReport(t *testing.T) {
	fake := &fakeVerifySSHClient{
		stdout: `{"status":"pass","stackkit":"base-kit","generatedAt":"2026-05-07T12:00:00Z","checks":[{"name":"spec","status":"pass","message":"loaded"}]}`,
	}
	var gotOptions remoteVerifyOptions
	withVerifySSHClientFactory(t, func(options remoteVerifyOptions) verifySSHClient {
		gotOptions = options
		return fake
	})

	report := runRemoteVerify(context.Background(), remoteVerifyOptions{
		Host:      "203.0.113.10",
		User:      "ubuntu",
		KeyPath:   "/tmp/id_ed25519",
		Port:      2222,
		RemoteDir: "/srv/stack kit",
		SpecFile:  "custom spec.yaml",
		HTTP:      true,
		Strict:    true,
	})

	if !fake.connected {
		t.Fatal("expected SSH client to connect")
	}
	if !fake.closed {
		t.Fatal("expected SSH client to close")
	}
	if gotOptions.Host != "203.0.113.10" || gotOptions.User != "ubuntu" || gotOptions.KeyPath != "/tmp/id_ed25519" || gotOptions.Port != 2222 {
		t.Fatalf("factory options = %#v", gotOptions)
	}
	wantCommand := "cd '/srv/stack kit' && stackkit verify --json --spec 'custom spec.yaml' --http --strict"
	if len(fake.commands) != 1 || fake.commands[0] != wantCommand {
		t.Fatalf("commands = %#v, want %q", fake.commands, wantCommand)
	}
	if report.Status != stackverify.StatusPass {
		t.Fatalf("status = %q, want pass", report.Status)
	}
	if !report.Remote || report.TargetHost != "203.0.113.10" {
		t.Fatalf("remote metadata = remote:%v target:%q", report.Remote, report.TargetHost)
	}
}

func TestRunRemoteVerifyKeepsFailedJSONReportFromNonZeroCommand(t *testing.T) {
	fake := &fakeVerifySSHClient{
		stdout: `{"status":"fail","stackkit":"base-kit","generatedAt":"2026-05-07T12:00:00Z","checks":[{"name":"deployment-state","status":"fail","message":"missing"}]}`,
		runErr: errors.New("exit status 1"),
	}
	withVerifySSHClientFactory(t, func(options remoteVerifyOptions) verifySSHClient {
		return fake
	})

	report := runRemoteVerify(context.Background(), remoteVerifyOptions{
		Host:      "203.0.113.10",
		Port:      22,
		RemoteDir: "/opt/stackkit",
		SpecFile:  "stack-spec.yaml",
	})

	if report.Status != stackverify.StatusFail {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	if len(report.Checks) != 1 || report.Checks[0].Name != "deployment-state" {
		t.Fatalf("checks = %#v", report.Checks)
	}
	if !report.Remote || report.TargetHost != "203.0.113.10" {
		t.Fatalf("remote metadata = remote:%v target:%q", report.Remote, report.TargetHost)
	}
}

func TestRunRemoteVerifyInvalidJSONReturnsFailReport(t *testing.T) {
	fake := &fakeVerifySSHClient{
		stdout: "not json",
		stderr: "stackkit: command not found",
		runErr: errors.New("exit status 127"),
	}
	withVerifySSHClientFactory(t, func(options remoteVerifyOptions) verifySSHClient {
		return fake
	})

	report := runRemoteVerify(context.Background(), remoteVerifyOptions{
		Host:      "203.0.113.10",
		Port:      22,
		RemoteDir: "/opt/stackkit",
		SpecFile:  "stack-spec.yaml",
	})

	if report.Status != stackverify.StatusFail {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	if len(report.Checks) != 1 {
		t.Fatalf("checks = %#v", report.Checks)
	}
	check := report.Checks[0]
	if check.Name != "remote-command" || check.Status != stackverify.StatusFail {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(check.Message, "invalid verify JSON") || !strings.Contains(check.Message, "stackkit: command not found") {
		t.Fatalf("message = %q", check.Message)
	}
}

func TestRunRemoteVerifyConnectErrorReturnsFailReport(t *testing.T) {
	fake := &fakeVerifySSHClient{connectErr: errors.New("permission denied")}
	withVerifySSHClientFactory(t, func(options remoteVerifyOptions) verifySSHClient {
		return fake
	})

	report := runRemoteVerify(context.Background(), remoteVerifyOptions{
		Host:      "203.0.113.10",
		Port:      22,
		RemoteDir: "/opt/stackkit",
		SpecFile:  "stack-spec.yaml",
	})

	if report.Status != stackverify.StatusFail {
		t.Fatalf("status = %q, want fail", report.Status)
	}
	if len(report.Checks) != 1 || report.Checks[0].Name != "remote-ssh" {
		t.Fatalf("checks = %#v", report.Checks)
	}
	if fake.closed {
		t.Fatal("client should not close when connect fails before connection is established")
	}
}

type fakeVerifySSHClient struct {
	connectErr error
	stdout     string
	stderr     string
	runErr     error
	commands   []string
	connected  bool
	closed     bool
}

func (f *fakeVerifySSHClient) Connect() error {
	if f.connectErr != nil {
		return f.connectErr
	}
	f.connected = true
	return nil
}

func (f *fakeVerifySSHClient) Close() error {
	f.closed = true
	return nil
}

func (f *fakeVerifySSHClient) Run(_ context.Context, command string) (string, string, error) {
	f.commands = append(f.commands, command)
	return f.stdout, f.stderr, f.runErr
}

func withVerifySSHClientFactory(t *testing.T, factory func(remoteVerifyOptions) verifySSHClient) {
	t.Helper()
	previous := newVerifySSHClient
	newVerifySSHClient = factory
	t.Cleanup(func() {
		newVerifySSHClient = previous
	})
}
