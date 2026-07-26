package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/releaseindex"
	"github.com/kombifyio/stackkits/pkg/models"
	"github.com/spf13/cobra"
)

var (
	doctorJSON         bool
	doctorCheckUpdates bool
)

var doctorCmd = &cobra.Command{
	Use:         "doctor",
	Short:       "Check host and spec readiness before production apply",
	Annotations: map[string]string{legacyV06BeforeObservabilityAnnotation: "doctor"},
	Long: `Check the current StackKit spec and host assumptions before prepare/apply.

Doctor is read-only. For the Base Kit local production reference it verifies
that the target is the Fresh Ubuntu local gate, SSH hardening can proceed via a
non-root key user, and the Photos + Vault reference slice is selected.`,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "Emit machine-readable doctor report")
	doctorCmd.Flags().BoolVar(&doctorCheckUpdates, "check-updates", false,
		"Also resolve the latest verified public release in the current channel")
}

type doctorReport struct {
	Status string        `json:"status"`
	Gate   string        `json:"gate"`
	Checks []doctorCheck `json:"checks"`
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

func runDoctor(cmd *cobra.Command, args []string) error {
	wd := getWorkDir()
	loader := config.NewLoader(wd)
	spec, err := loadLegacyOperationalStackSpec(wd, specFile, architectureV2Doctor)
	if err != nil {
		return fmt.Errorf("doctor: failed to load spec: %w", err)
	}

	report := buildDoctorReport(spec)

	if doctorCheckUpdates {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		stateFile := filepath.Join(wd, ".stackkit", "state.yaml")
		state, sErr := loader.LoadDeploymentState(stateFile)
		appendUpdateChecks(ctx, &report, state, sErr)
	}

	if doctorJSON {
		data, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	} else {
		printDoctorReport(cmd, report)
	}
	if report.Status == "fail" {
		return fmt.Errorf("doctor failed: fix readiness blockers before production apply")
	}
	return nil
}

// appendUpdateChecks resolves the public GitHub release index. It is
// best-effort: unavailable release metadata is diagnostic, not a readiness
// failure.
func appendUpdateChecks(ctx context.Context, report *doctorReport, state *models.DeploymentState, stateErr error) {
	add := func(name, status, message string) {
		report.Checks = append(report.Checks, doctorCheck{Name: name, Status: status, Message: message})
		if status == "fail" {
			report.Status = "fail"
		} else if status == "warn" && report.Status == "pass" {
			report.Status = "warn"
		}
	}

	if stateErr != nil || state == nil {
		add("updates", "warn", "no .stackkit/state.yaml — run 'stackkit apply' first to enable update checks")
		return
	}
	if strings.TrimSpace(state.StackKit) == "" {
		add("updates", "warn", "deployment state has no StackKit identity — re-apply with the current CLI")
		return
	}
	channel := releaseindex.Channel(strings.ToLower(strings.TrimSpace(state.KitChannel)))
	if channel == "" {
		channel = releaseindex.ChannelStable
	}
	if channel != releaseindex.ChannelStable && channel != releaseindex.ChannelBeta && channel != releaseindex.ChannelEdge {
		add("updates", "warn", fmt.Sprintf("deployment state has invalid release channel %q", state.KitChannel))
		return
	}
	resolution, err := (releaseindex.Resolver{
		Source: newPublicReleaseSource(), Attestations: newPublicAttestationVerifier(),
	}).Resolve(ctx, releaseindex.ResolveRequest{
		Kit: state.StackKit, Target: "channel:" + string(channel), OS: runtime.GOOS, Arch: runtime.GOARCH,
	})
	if err != nil {
		add("updates", "warn", fmt.Sprintf("public release-index query failed: %v", err))
		return
	}
	current := strings.TrimPrefix(strings.TrimSpace(state.KitSemver), "v")
	latest := strings.TrimPrefix(resolution.Asset.Version, "v")
	if current != "" && current == latest {
		add("updates", "pass", fmt.Sprintf("kit %s is at latest %s in channel %s", state.StackKit, resolution.Asset.Version, channel))
		return
	}
	add("updates", "warn", fmt.Sprintf("%s available in channel %s", resolution.Asset.Version, channel))
	add("updates-cta", "warn", fmt.Sprintf("run 'stackkit upgrade --to=channel:%s --dry-run' to plan", channel))
}

func buildDoctorReport(spec *models.StackSpec) doctorReport {
	report := doctorReport{
		Status: "pass",
		Gate:   "fresh-ubuntu-local",
	}
	add := func(name, status, message string) {
		report.Checks = append(report.Checks, doctorCheck{Name: name, Status: status, Message: message})
		if status == "fail" {
			report.Status = "fail"
		} else if status == "warn" && report.Status == "pass" {
			report.Status = "warn"
		}
	}

	if spec == nil {
		add("spec", "fail", "stack spec is nil")
		return report
	}
	add("spec", "pass", "stack spec loaded")

	if isBaseKitProductionReferenceSpec(spec) {
		add("fresh-ubuntu-local", "pass", "local context with Kombify Point home domain")
	} else {
		add("fresh-ubuntu-local", "warn", "not the Base Kit local production reference gate")
	}

	if isBaseKitProductionReferenceSpec(spec) {
		if strings.EqualFold(spec.SSH.User, "root") {
			add("ssh-non-root", "fail", "security-baseline must not lock down SSH until a non-root user is configured")
		} else if strings.TrimSpace(spec.SSH.User) == "" {
			add("ssh-non-root", "fail", "security-baseline requires ssh.user before lock-down")
		} else {
			add("ssh-non-root", "pass", "non-root SSH user configured")
		}

		keyPath := strings.TrimSpace(spec.SSH.KeyPath)
		if keyPath == "" {
			add("ssh-key", "fail", "security-baseline requires ssh.keyPath before password/root lock-down")
		} else if keyExists(keyPath) {
			add("ssh-key", "pass", "SSH key path exists locally")
		} else {
			add("ssh-key", "warn", "SSH key path is configured but not readable from this workstation")
		}

		if serviceExplicitlyDisabled(spec.Services, "media") || serviceExplicitlyDisabled(spec.Services, "jellyfin") {
			add("media-gate", "pass", "media/Jellyfin is outside this production-ready gate")
		} else {
			add("media-gate", "warn", "media/Jellyfin is not part of the current production-ready claim")
		}

		if serviceExplicitlyDisabled(spec.Services, "photos") || serviceExplicitlyDisabled(spec.Services, "immich") {
			add("photos", "fail", "Base Kit production reference requires Photos/Immich")
		} else {
			add("photos", "pass", "Photos/Immich reference use case selected")
		}
		if serviceExplicitlyDisabled(spec.Services, "vault") || serviceExplicitlyDisabled(spec.Services, "vaultwarden") {
			add("vault", "fail", "Base Kit production reference requires Vaultwarden")
		} else {
			add("vault", "pass", "Vaultwarden reference use case selected")
		}
		if serviceExplicitlyDisabled(spec.Services, "files") {
			add("files", "fail", "Base Kit production reference requires Files/Cloudreve")
		} else {
			add("files", "pass", "Files/Cloudreve reference use case selected")
		}
	}

	return report
}

func printDoctorReport(cmd *cobra.Command, report doctorReport) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Doctor: %s (%s)\n", report.Status, report.Gate)
	for _, check := range report.Checks {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "- %s: %s - %s\n", check.Name, check.Status, check.Message)
	}
}

func isBaseKitProductionReferenceSpec(spec *models.StackSpec) bool {
	if spec == nil || spec.StackKit != "basement-kit" {
		return false
	}
	domain := spec.Domain
	if domain == "" {
		domain = models.DomainHomeLab
	}
	return spec.Context == string(models.ContextLocal) &&
		models.IsLocalDomain(domain) &&
		!models.IsKombifyMeDomain(domain)
}

func serviceExplicitlyDisabled(services map[string]any, name string) bool {
	return !serviceEnabledValue(services, name, true)
}

func serviceEnabledValue(services map[string]any, name string, fallback bool) bool {
	if services == nil {
		return fallback
	}
	raw, ok := services[name]
	if !ok {
		return fallback
	}
	mapped, ok := raw.(map[string]any)
	if !ok {
		return fallback
	}
	enabled, ok := mapped["enabled"].(bool)
	if !ok {
		return fallback
	}
	return enabled
}

func keyExists(path string) bool {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			path = home
		}
	} else if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	_, err := os.Stat(path)
	return err == nil
}
