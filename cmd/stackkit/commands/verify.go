package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/docker"
	"github.com/kombifyio/stackkits/internal/rollout"
	"github.com/kombifyio/stackkits/internal/ssh"
	stackverify "github.com/kombifyio/stackkits/internal/verify"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	verifyJSON      bool
	verifyHTTP      bool
	verifyStrict    bool
	verifyHost      string
	verifyUser      string
	verifyKey       string
	verifyPort      int
	verifyRemoteDir string
)

type verifySSHClient interface {
	Connect() error
	Close() error
	Run(context.Context, string) (string, string, error)
}

type remoteVerifyOptions struct {
	Host      string
	User      string
	KeyPath   string
	Port      int
	RemoteDir string
	SpecFile  string
	HTTP      bool
	Strict    bool
}

var newVerifySSHClient = func(options remoteVerifyOptions) verifySSHClient {
	opts := []ssh.ClientOption{
		ssh.WithHost(options.Host),
		ssh.WithPort(options.Port),
	}
	if options.User != "" {
		opts = append(opts, ssh.WithUser(options.User))
	}
	if options.KeyPath != "" {
		opts = append(opts, ssh.WithKeyPath(options.KeyPath))
	}
	return ssh.NewClient(opts...)
}

var verifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Run post-deployment verification checks",
	Long: `Run read-only post-deployment checks from the host where the StackKit is deployed.

The verifier checks the stack spec, deployment state, Docker daemon, StackKit
containers, Docker health status, and optionally HTTP routes from the generated
access summary.

Examples:
  stackkit verify
  stackkit verify --http
  stackkit verify --json --strict
  stackkit verify --host 203.0.113.10 --user ubuntu --remote-dir /opt/stackkit --json`,
	RunE: runVerify,
}

func init() {
	verifyCmd.Flags().BoolVar(&verifyJSON, "json", false, "Emit machine-readable JSON")
	verifyCmd.Flags().BoolVar(&verifyHTTP, "http", false, "Verify generated service URLs with HTTP requests")
	verifyCmd.Flags().BoolVar(&verifyStrict, "strict", false, "Treat warnings as verification failure")
	verifyCmd.Flags().StringVar(&verifyHost, "host", "", "Remote host to verify over SSH")
	verifyCmd.Flags().StringVar(&verifyUser, "user", "", "SSH username for remote verification")
	verifyCmd.Flags().StringVar(&verifyKey, "key", "", "SSH private key path for remote verification")
	verifyCmd.Flags().IntVar(&verifyPort, "port", 22, "SSH port for remote verification")
	verifyCmd.Flags().StringVar(&verifyRemoteDir, "remote-dir", "/opt/stackkit", "Remote StackKit working directory")
}

func runVerify(cmd *cobra.Command, args []string) (retErr error) {
	ctx := context.Background()
	rolloutEvent("verify", "started", "verify started", map[string]string{
		"remote_host": verifyHost,
	})
	defer func() {
		if retErr != nil {
			rolloutFailure("verify", retErr)
			closeRolloutRecorder(rollout.Summary{
				Status:  "failed",
				Message: retErr.Error(),
			})
			return
		}
		rolloutEvent("verify", "succeeded", "verify succeeded", nil)
	}()
	if strings.TrimSpace(verifyHost) != "" {
		report := runRemoteVerify(ctx, remoteVerifyOptions{
			Host:      verifyHost,
			User:      verifyUser,
			KeyPath:   verifyKey,
			Port:      verifyPort,
			RemoteDir: verifyRemoteDir,
			SpecFile:  specFile,
			HTTP:      verifyHTTP,
			Strict:    verifyStrict,
		})
		return emitVerifyReport(cmd.OutOrStdout(), report, verifyJSON)
	}

	wd := getWorkDir()
	loader := config.NewLoader(wd)

	rolloutEvent("spec.load", "started", "loading stack spec", map[string]string{
		"spec_file": specFile,
	})
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		rolloutFailure("spec.load", err)
		return fmt.Errorf("verify: failed to load spec: %w", err)
	}
	rolloutEvent("spec.load", "succeeded", "stack spec loaded", map[string]string{
		"stackkit": spec.StackKit,
		"mode":     spec.Mode,
		"domain":   spec.Domain,
	})

	state, err := loader.LoadDeploymentState(filepath.Join(wd, ".stackkit", "state.yaml"))
	if err != nil {
		return fmt.Errorf("verify: failed to load deployment state: %w", err)
	}

	report := buildLocalVerifyReport(ctx, wd, spec, state, stackverify.Options{
		HTTP:   verifyHTTP,
		Strict: verifyStrict,
	})

	return emitVerifyReport(cmd.OutOrStdout(), report, verifyJSON)
}

func buildLocalVerifyReport(
	ctx context.Context,
	wd string,
	spec *models.StackSpec,
	state *models.DeploymentState,
	options stackverify.Options,
) stackverify.Report {
	var verifyAccess *stackverify.AccessSummary
	if access, accessErr := buildAccessSummary(wd, spec); accessErr == nil {
		verifyAccess = toVerifyAccessSummary(access)
	}

	return stackverify.RunLocal(ctx, stackverify.Input{
		Spec:    spec,
		State:   state,
		Docker:  docker.NewClient(),
		Access:  verifyAccess,
		Options: options,
	})
}

func runRemoteVerify(ctx context.Context, options remoteVerifyOptions) stackverify.Report {
	options = normalizeRemoteVerifyOptions(options)
	client := newVerifySSHClient(options)
	if err := client.Connect(); err != nil {
		return remoteVerifyFailureReport(options, "remote-ssh", remoteVerifyTarget(options), fmt.Sprintf("failed to connect: %v", err))
	}
	defer func() { _ = client.Close() }()

	command := buildRemoteVerifyCommand(options)
	stdout, stderr, runErr := client.Run(ctx, command)
	report, parseErr := parseRemoteVerifyReport(stdout)
	if parseErr == nil {
		report.Remote = true
		report.TargetHost = options.Host
		return report
	}

	message := fmt.Sprintf("invalid verify JSON: %v", parseErr)
	if runErr != nil {
		message += fmt.Sprintf("; remote command failed: %v", runErr)
	}
	if strings.TrimSpace(stderr) != "" {
		message += "; stderr: " + strings.TrimSpace(stderr)
	}
	return remoteVerifyFailureReport(options, "remote-command", command, message)
}

func normalizeRemoteVerifyOptions(options remoteVerifyOptions) remoteVerifyOptions {
	options.Host = strings.TrimSpace(options.Host)
	options.User = strings.TrimSpace(options.User)
	options.KeyPath = strings.TrimSpace(options.KeyPath)
	options.RemoteDir = strings.TrimSpace(options.RemoteDir)
	options.SpecFile = strings.TrimSpace(options.SpecFile)
	if options.Port == 0 {
		options.Port = 22
	}
	if options.RemoteDir == "" {
		options.RemoteDir = "/opt/stackkit"
	}
	if options.SpecFile == "" {
		options.SpecFile = "stack-spec.yaml"
	}
	return options
}

func buildRemoteVerifyCommand(options remoteVerifyOptions) string {
	options = normalizeRemoteVerifyOptions(options)
	parts := []string{
		"cd " + remoteShellQuote(options.RemoteDir),
		"stackkit verify --json --spec " + remoteShellQuote(options.SpecFile),
	}
	if options.HTTP {
		parts[1] += " --http"
	}
	if options.Strict {
		parts[1] += " --strict"
	}
	return strings.Join(parts, " && ")
}

func remoteShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func parseRemoteVerifyReport(stdout string) (stackverify.Report, error) {
	var report stackverify.Report
	data := strings.TrimSpace(stdout)
	if data == "" {
		return report, fmt.Errorf("empty remote verify output")
	}
	if err := json.Unmarshal([]byte(data), &report); err != nil {
		return report, err
	}
	return report, nil
}

func remoteVerifyFailureReport(options remoteVerifyOptions, name, target, message string) stackverify.Report {
	return stackverify.Report{
		Status:      stackverify.StatusFail,
		Remote:      true,
		TargetHost:  options.Host,
		GeneratedAt: time.Now().UTC(),
		Checks: []stackverify.Check{{
			Name:    name,
			Status:  stackverify.StatusFail,
			Target:  target,
			Message: message,
		}},
	}
}

func remoteVerifyTarget(options remoteVerifyOptions) string {
	if options.Port == 0 {
		options.Port = 22
	}
	return fmt.Sprintf("%s:%d", options.Host, options.Port)
}

func toVerifyAccessSummary(summary *accessSummary) *stackverify.AccessSummary {
	if summary == nil {
		return nil
	}
	out := &stackverify.AccessSummary{
		Services: make([]stackverify.AccessService, 0, len(summary.Services)),
	}
	for _, service := range summary.Services {
		out.Services = append(out.Services, stackverify.AccessService{
			Key: service.Key,
			URL: service.URL,
		})
	}
	return out
}

func emitVerifyReport(w io.Writer, report stackverify.Report, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printVerifyReport(w, report)
	}
	if report.Status == stackverify.StatusFail {
		return fmt.Errorf("verify failed")
	}
	return nil
}

func printVerifyReport(w io.Writer, report stackverify.Report) {
	_, _ = fmt.Fprintf(w, "Verify: %s\n", report.Status)
	if report.StackKit != "" {
		_, _ = fmt.Fprintf(w, "StackKit: %s\n", report.StackKit)
	}
	if report.Mode != "" {
		_, _ = fmt.Fprintf(w, "Mode: %s\n", report.Mode)
	}
	for _, check := range report.Checks {
		target := ""
		if check.Target != "" {
			target = " [" + check.Target + "]"
		}
		_, _ = fmt.Fprintf(w, "- %s: %s%s - %s\n", check.Name, check.Status, target, check.Message)
	}
}
