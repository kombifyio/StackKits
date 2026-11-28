package installer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Options configures one Go `stackkit install` execution replacing the legacy
// shell installers (base-install.sh, cloud-install.sh, install-host-bootstrap.sh)
// per Blueprint v5.1 §6.2 & ADR-0044 (zero local docker build).
type Options struct {
	Kit              string   `json:"kit"`              // basement-kit | cloud-kit | modern-homelab
	AddressZone      string   `json:"addressZone"`      // custom-domain | kombify-me | local-only
	Domain           string   `json:"domain,omitempty"` // e.g. example.com, demo.kombify.me, lab.home
	SetupStyle       string   `json:"setupStyle"`       // bootstrapped | clean
	UseCases         []string `json:"useCases,omitempty"`
	WorkspaceDir     string   `json:"workspaceDir"`
	OwnerEmail       string   `json:"ownerEmail,omitempty"`
	Language         string   `json:"language"`
	CLIOnly          bool     `json:"cliOnly"`
	PrepareHost      bool     `json:"prepareHost"`
	InstallSystemd   bool     `json:"installSystemd"`
	NonInteractive   bool     `json:"nonInteractive"`
	PlanOnly         bool     `json:"planOnly"`
	AllowInteractive bool     `json:"allowInteractiveSudo"`
}

// Step describes one deterministic host or workspace step in the install plan.
type Step struct {
	ID           string   `json:"id"`
	Summary      string   `json:"summary"`
	RequiresRoot bool     `json:"requiresRoot"`
	Command      []string `json:"command,omitempty"`
	Skipped      bool     `json:"skipped,omitempty"`
	SkipReason   string   `json:"skipReason,omitempty"`
}

// Plan is the secret-free, machine-readable installation plan built before any
// host mutation runs.
type Plan struct {
	SchemaVersion    string   `json:"schemaVersion"`
	Kit              string   `json:"kit"`
	AddressZone      string   `json:"addressZone"`
	Domain           string   `json:"domain"`
	SetupStyle       string   `json:"setupStyle"`
	Language         string   `json:"language"`
	WorkspaceDir     string   `json:"workspaceDir"`
	OS               string   `json:"os"`
	Arch             string   `json:"arch"`
	LocalDockerBuild bool     `json:"localDockerBuild"` // ALWAYS false (ADR-0044)
	Steps            []Step   `json:"steps"`
	NextCommands     []string `json:"nextCommands"`
}

// BuildPlan validates the 3-axis onboarding inputs and produces a complete
// zero-local-docker-build installation plan.
func BuildPlan(opts Options) (Plan, error) {
	lang := ResolveLanguage(opts.Language)
	kit := strings.ToLower(strings.TrimSpace(opts.Kit))
	if kit == "" {
		kit = "basement-kit"
	}
	if kit == "base-kit" {
		return Plan{}, errors.New(Msg(lang, "retired_base"))
	}
	switch kit {
	case "basement-kit", "cloud-kit", "modern-homelab":
	default:
		return Plan{}, errors.New(Msg(lang, "invalid_kit", kit))
	}

	zone := strings.ToLower(strings.TrimSpace(opts.AddressZone))
	if zone == "" {
		if kit == "cloud-kit" {
			zone = "custom-domain"
		} else {
			zone = "local-only"
		}
	}
	domain := strings.ToLower(strings.TrimSpace(opts.Domain))
	switch zone {
	case "local-only", "local":
		zone = "local-only"
		if domain == "" {
			domain = "lab.home"
		}
	case "custom-domain", "custom", "own-domain":
		zone = "custom-domain"
		if domain == "" && !opts.CLIOnly {
			return Plan{}, errors.New("address zone custom-domain requires --domain (e.g. example.com)")
		}
		if strings.HasSuffix(domain, ".kombify.me") || domain == "kombify.me" {
			return Plan{}, errors.New("custom-domain must not use kombify.me; use --address-zone=kombify-me instead")
		}
	case "kombify-me", "kombify.me":
		zone = "kombify-me"
		if domain == "" {
			domain = "home.kombify.me"
		}
	default:
		return Plan{}, fmt.Errorf("invalid address zone %q: expected custom-domain, kombify-me, or local-only", zone)
	}

	style := strings.ToLower(strings.TrimSpace(opts.SetupStyle))
	if style == "" {
		style = "bootstrapped"
	}
	switch style {
	case "bootstrapped", "clean":
	default:
		return Plan{}, fmt.Errorf("invalid setup style %q: expected bootstrapped or clean", style)
	}

	ws := strings.TrimSpace(opts.WorkspaceDir)
	if ws == "" {
		ws = "my-homelab"
	}
	absWS, err := filepath.Abs(ws)
	if err != nil {
		return Plan{}, err
	}

	steps := []Step{
		{
			ID:           "verify-host-runtime",
			Summary:      "Verify Linux container runtime (Docker/Compose) and release-pinned OpenTofu binary (zero local docker build)",
			RequiresRoot: false,
		},
	}
	if opts.PrepareHost {
		steps = append(steps, Step{
			ID:           "prepare-host-packages",
			Summary:      "Ensure host Docker Engine / Compose plugin and runtime prerequisites via interactive sudo",
			RequiresRoot: true,
			Command:      []string{"stackkit", "host", "prepare"},
		})
	}
	if opts.InstallSystemd {
		steps = append(steps, Step{
			ID:           "install-systemd-units",
			Summary:      "Install host-side ADR-0051 stackkit-watchdog.timer and stackkit-host-exec.path units",
			RequiresRoot: true,
		})
	}
	if !opts.CLIOnly {
		initCmd := []string{
			"stackkit", "init", kit,
			"--catalog-defaults",
			"--owner-source=local",
			"--domain=" + domain,
		}
		if strings.TrimSpace(opts.OwnerEmail) != "" {
			initCmd = append(initCmd, "--owner-email="+strings.TrimSpace(opts.OwnerEmail))
		}
		if style == "clean" {
			initCmd = append(initCmd, "--clean-rollout")
		}
		if opts.NonInteractive {
			initCmd = append(initCmd, "--non-interactive")
		}
		steps = append(steps,
			Step{
				ID:      "init-workspace",
				Summary: fmt.Sprintf("Initialize %s workspace (%s, %s, %s)", kit, zone, domain, style),
				Command: initCmd,
			},
			Step{
				ID:      "apply-stack",
				Summary: "Converge Core and selected curated apps via OpenTofu Stage 1",
				Command: []string{"stackkit", "apply"},
			},
		)
	}

	return Plan{
		SchemaVersion:    "stackkit.install-plan/v1",
		Kit:              kit,
		AddressZone:      zone,
		Domain:           domain,
		SetupStyle:       style,
		Language:         lang,
		WorkspaceDir:     absWS,
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		LocalDockerBuild: false,
		Steps:            steps,
		NextCommands: []string{
			fmt.Sprintf("cd %s", absWS),
			"stackkit apply",
			"stackkit status",
		},
	}, nil
}

// RunElevated runs argv directly when already UID 0, or via interactive `sudo`
// when permitted (failing closed in non-interactive mode without root/sudo -n).
func RunElevated(ctx context.Context, argv []string, nonInteractive bool) error {
	if len(argv) == 0 {
		return errors.New("elevated command argv is empty")
	}
	if os.Geteuid() == 0 {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	sudoPath, err := exec.LookPath("sudo")
	if err != nil {
		return errors.New("root privileges required and sudo is not installed")
	}
	sudoArgs := make([]string, 0, len(argv)+2)
	if nonInteractive {
		sudoArgs = append(sudoArgs, "-n")
	}
	sudoArgs = append(sudoArgs, "--")
	sudoArgs = append(sudoArgs, argv...)
	cmd := exec.CommandContext(ctx, sudoPath, sudoArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
