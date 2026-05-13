package commands

// kit_upgrade.go implements `stackkit kit upgrade` (kit-update-phase-1, ADR-0018).
//
// Flow (single-node):
//
//   1. Load .stackkit/state.yaml — fail if no current deployment.
//   2. Pre-flight: Kopia repo configured (ADR-0018 §3 mandatory).
//   3. Pre-flight: list available kit-versions in target channel from Admin.
//   4. Resolve --to into a concrete kit-version-id.
//   5. Resolver call → channel-map for module-versions.
//   6. (TODO future) contract_hash verify.
//   7. tofu plan with new templates+vars.
//   8. Confirm-Gate (skipped under --auto-approve).
//   9. Atomic-Snapshot: kopia + tfstate copy + manifest.
//  10. tofu apply.
//  11. Persist new version into state.yaml.
//  12. PATCH /api/v1/sk/node-deployments/<id> (best-effort).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/iac"
	"github.com/kombifyio/stackkits/internal/registry"
	"github.com/kombifyio/stackkits/internal/snapshot"
	"github.com/kombifyio/stackkits/pkg/models"
)

// upgrade-command flag-bound variables. They live as package-level vars
// so the cobra wiring stays consistent with the rest of cmd/stackkit.
var (
	kitUpgradeTo            string
	kitUpgradeKitChannel    string
	kitUpgradeModuleChannel string
	kitUpgradeAllowMismatch bool
	kitUpgradeDryRun        bool
	kitUpgradeAutoApprove   bool
	kitUpgradeSnapshotID    string
	kitUpgradeVolumes       []string
	kitUpgradeEndpoint      string
	kitUpgradeToken         string
)

var kitUpgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade an installed kit to a newer version (ADR-0018)",
	Long: `Upgrade an installed kit to a newer version on this node.

The upgrade flow always:
  - takes a Kopia snapshot of every persistent volume (--volumes)
  - copies deploy/terraform.tfstate to .stackkit/snapshots/<ts>/
  - runs tofu apply with the new templates

Both anchors are required and the operator cannot opt out of the snapshot
step. Run 'stackkit backup configure' before the first upgrade.

Default behavior selects the latest stable kit-version. Use --kit-channel
to target beta or edge; --module-channel pins a different channel for the
module set inside the kit.

Examples:
  stackkit kit upgrade --dry-run
  stackkit kit upgrade --to=channel:stable --auto-approve
  stackkit kit upgrade --to=1.2.0 --kit-channel=beta --volumes=/var/lib/postgres,/var/lib/vaultwarden
`,
	RunE: runKitUpgrade,
}

func init() {
	kitUpgradeCmd.Flags().StringVar(&kitUpgradeTo, "to", "channel:stable",
		"Target version. Either a semver (e.g. 1.2.0) or 'channel:<edge|beta|stable>'.")
	kitUpgradeCmd.Flags().StringVar(&kitUpgradeKitChannel, "kit-channel", "",
		"Kit release channel (edge|beta|stable). Defaults to the channel implied by --to.")
	kitUpgradeCmd.Flags().StringVar(&kitUpgradeModuleChannel, "module-channel", "",
		"Override the module channel used by the resolver. Empty = inherit from --kit-channel.")
	kitUpgradeCmd.Flags().BoolVar(&kitUpgradeAllowMismatch, "allow-channel-mismatch", false,
		"Allow modules whose chosen version is in a different channel than --module-channel.")
	kitUpgradeCmd.Flags().BoolVar(&kitUpgradeDryRun, "dry-run", false,
		"Print the plan + channel-map and exit without snapshotting or applying.")
	kitUpgradeCmd.Flags().BoolVar(&kitUpgradeAutoApprove, "auto-approve", false,
		"Skip the interactive confirm gate before atomic-snapshot + apply.")
	kitUpgradeCmd.Flags().StringVar(&kitUpgradeSnapshotID, "snapshot-id", "",
		"Override the auto-generated snapshot directory name (.stackkit/snapshots/<id>).")
	kitUpgradeCmd.Flags().StringSliceVar(&kitUpgradeVolumes, "volumes", nil,
		"Comma-separated list of persistent volume paths to snapshot (e.g. /var/lib/postgres).")
	kitUpgradeCmd.Flags().StringVar(&kitUpgradeEndpoint, "endpoint", "",
		"Admin API base URL. Defaults to $STACKKIT_ADMIN_ENDPOINT.")
	kitUpgradeCmd.Flags().StringVar(&kitUpgradeToken, "token", "",
		"Bearer token. Defaults to $STACKKIT_ADMIN_TOKEN.")

	kitCmd.AddCommand(kitUpgradeCmd)
}

func runKitUpgrade(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	wd := getWorkDir()
	loader := config.NewLoader(wd)

	// Step 1 — load current state
	stateFile := filepath.Join(wd, ".stackkit", "state.yaml")
	state, err := loader.LoadDeploymentState(stateFile)
	if err != nil || state == nil {
		return fmt.Errorf("no deployment state at %s — run 'stackkit apply' first", stateFile)
	}
	if state.KitVersionID == "" {
		printWarning("state.yaml has no KitVersionID — older CLI version applied this kit")
		printWarning("re-apply once with the current CLI to pin the kit-version, then upgrade")
		return fmt.Errorf("KitVersionID missing in state.yaml")
	}

	// Resolve channels.
	to, kitChannel, err := parseUpgradeTarget(kitUpgradeTo, kitUpgradeKitChannel)
	if err != nil {
		return err
	}

	moduleChannel := kitUpgradeModuleChannel
	if moduleChannel == "" {
		moduleChannel = kitChannel
	}

	if kitChannel == "edge" {
		printWarning("upgrade target is on the 'edge' channel — this is pre-release")
		if !kitUpgradeAutoApprove && !kitUpgradeDryRun {
			ok, err := confirm(cmd, "Continue with edge upgrade? [y/N] ")
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("aborted by operator")
			}
		}
	}

	// Step 2 — Pre-flight: Kopia configured?
	kopia := snapshot.NewKopia()
	status, err := kopia.Status(ctx)
	if err != nil {
		return fmt.Errorf("kopia pre-flight: %w", err)
	}
	if !status.Configured {
		printError("kopia repository not configured")
		printInfo("run 'stackkit backup configure' first (ADR-0016)")
		return snapshot.ErrKopiaNotConfigured
	}

	// Step 3 — Resolve target kit-version-id.
	adminClient, _, adminErr := loadAdminClient(kitUpgradeEndpoint, kitUpgradeToken)
	if adminErr != nil {
		// We need the admin endpoint to enumerate versions and call the
		// resolver. Surface the error directly.
		return fmt.Errorf("admin endpoint required for kit upgrade: %w", adminErr)
	}
	_ = adminClient // reserved for future calls; resolver path uses its own client below

	endpoint := kitUpgradeEndpoint
	if endpoint == "" {
		endpoint = os.Getenv("STACKKIT_ADMIN_ENDPOINT")
	}
	if endpoint == "" {
		endpoint = os.Getenv("ADMIN_PUBLIC_API_URL")
	}
	endpoint = strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/api/v1")

	token := kitUpgradeToken
	if token == "" {
		token = os.Getenv("STACKKIT_ADMIN_TOKEN")
	}

	targetVersion, err := resolveTargetVersion(ctx, endpoint, token, state.StackKit, kitChannel, to)
	if err != nil {
		return fmt.Errorf("resolve target version: %w", err)
	}
	printInfo("target kit-version: %s (%s, channel=%s)", targetVersion.Semver, targetVersion.ID, kitChannel)

	if targetVersion.ID == state.KitVersionID {
		printSuccess("already on %s — nothing to do", targetVersion.Semver)
		return nil
	}

	// Step 5 — Channel-resolver call
	resolver := registry.NewChannelResolver(endpoint, token)
	resolverResult, err := resolver.Resolve(ctx, registry.ResolveRequest{
		KitSlug:       state.StackKit,
		KitVersionID:  targetVersion.ID,
		KitChannel:    kitChannel,
		ModuleChannel: moduleChannel,
	})
	if err != nil {
		return fmt.Errorf("channel resolver: %w", err)
	}

	mismatches := summarizeMismatches(resolverResult, moduleChannel)
	if len(mismatches) > 0 && !kitUpgradeAllowMismatch {
		printWarning("the resolver fell back outside --module-channel=%s for %d module(s):",
			moduleChannel, len(mismatches))
		for _, m := range mismatches {
			fmt.Printf("    %s → %s (channel=%s, reason=%s)\n",
				m.ModuleSlug, m.ModuleSemver, m.Channel, m.Reason)
		}
		printInfo("re-run with --allow-channel-mismatch to proceed, or pin --module-channel differently")
		return errors.New("channel mismatches present; re-run with --allow-channel-mismatch")
	}

	// Step 7 — tofu plan (re-render skipped for first-cut; assumes the
	// operator runs `stackkit generate` first in the working directory).
	spec, err := loader.LoadStackSpec(specFile)
	if err != nil {
		return fmt.Errorf("load stack spec: %w", err)
	}
	executor, err := iac.NewExecutorFromSpec(spec, wd)
	if err != nil {
		return fmt.Errorf("iac executor: %w", err)
	}
	if !executor.IsInstalled() {
		return fmt.Errorf("iac engine (%s) not installed on this node", executor.Mode())
	}

	planResult, err := executor.Plan(ctx, "", false)
	if err != nil {
		return fmt.Errorf("tofu plan: %w", err)
	}

	printPlan(planResult, resolverResult)

	if kitUpgradeDryRun {
		printSuccess("dry-run complete — no snapshot, no apply")
		return nil
	}

	// Step 8 — confirm
	if !kitUpgradeAutoApprove {
		ok, err := confirm(cmd, "Apply this upgrade? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("aborted by operator")
		}
	}

	// Step 9 — Atomic snapshot
	channelMap := buildChannelMap(resolverResult)
	tofuStateSrc := filepath.Join(wd, "deploy", "terraform.tfstate")
	snapshotsDir := filepath.Join(wd, ".stackkit", "snapshots")
	a := &snapshot.AtomicSnapshotter{
		Kopia:        kopia,
		SnapshotsDir: snapshotsDir,
		NodeName:     state.StackKit,
	}
	dir, manifest, err := a.CreateBundle(ctx, snapshot.BundleOptions{
		OldKitVersion: state.KitSemver,
		NewKitVersion: targetVersion.Semver,
		VolumePaths:   kitUpgradeVolumes,
		TofuStateSrc:  tofuStateSrc,
		ChannelMap:    channelMap,
	})
	if err != nil {
		return fmt.Errorf("atomic-snapshot: %w", err)
	}
	printSuccess("snapshot bundle written to %s", dir)
	_ = manifest

	// Step 10 — tofu apply
	if _, err := executor.Apply(ctx, true, ""); err != nil {
		printError("tofu apply failed — your bundle at %s holds the rollback anchors", dir)
		return fmt.Errorf("tofu apply: %w", err)
	}

	// Step 11 — update state.yaml
	state.KitVersionID = targetVersion.ID
	state.KitSemver = targetVersion.Semver
	state.KitChannel = kitChannel
	state.LastApplied = time.Now()
	state.Status = models.StatusRunning
	state.LastSnapshotDir = dir
	if err := writeDeploymentState(stateFile, state); err != nil {
		printWarning("failed to update state.yaml: %v", err)
	}

	// Step 12 — best-effort admin notification.
	if perr := postNodeDeployment(ctx, endpoint, token, state, channelMap, manifest); perr != nil {
		printWarning("admin sk_node_deployment update failed: %v", perr)
	}

	printSuccess("kit %s upgraded %s → %s", state.StackKit, manifest.OldKitVersion, manifest.NewKitVersion)
	return nil
}

// ----------------------------------------------------------------------------
// helpers
// ----------------------------------------------------------------------------

// kitVersionMeta is the minimal shape we need from
// `/api/v1/sk/registry/stackkits/<slug>/versions`. The admin endpoint
// returns more fields; we ignore them.
type kitVersionMeta struct {
	ID         string    `json:"id"`
	Semver     string    `json:"semver"`
	Channel    string    `json:"releaseChannel"`
	ReleasedAt time.Time `json:"releasedAt,omitempty"`
}

// parseUpgradeTarget normalizes --to + --kit-channel into a concrete
// (semver | "") and a channel value.
func parseUpgradeTarget(to, channelFlag string) (semver string, channel string, err error) {
	to = strings.TrimSpace(to)
	if to == "" {
		to = "channel:stable"
	}
	if strings.HasPrefix(to, "channel:") {
		ch := strings.TrimPrefix(to, "channel:")
		if !isValidChannelLocal(ch) {
			return "", "", fmt.Errorf("invalid channel in --to=channel:%s", ch)
		}
		if channelFlag != "" && channelFlag != ch {
			return "", "", fmt.Errorf("--to=channel:%s and --kit-channel=%s disagree", ch, channelFlag)
		}
		return "", ch, nil
	}
	// Treat as explicit semver. Channel defaults to --kit-channel or stable.
	if channelFlag == "" {
		channelFlag = "stable"
	}
	if !isValidChannelLocal(channelFlag) {
		return "", "", fmt.Errorf("invalid --kit-channel=%s", channelFlag)
	}
	return to, channelFlag, nil
}

func isValidChannelLocal(c string) bool {
	switch c {
	case "edge", "beta", "stable":
		return true
	}
	return false
}

// resolveTargetVersion fetches the version-list and picks either the
// explicit semver or the latest released_at row in the target channel.
func resolveTargetVersion(ctx context.Context, endpoint, token, kitSlug, channel, semver string) (kitVersionMeta, error) {
	versions, err := fetchVersions(ctx, endpoint, token, kitSlug, channel)
	if err != nil {
		return kitVersionMeta{}, err
	}
	if len(versions) == 0 {
		return kitVersionMeta{}, fmt.Errorf("no versions found for %s/%s", kitSlug, channel)
	}

	if semver != "" {
		for _, v := range versions {
			if v.Semver == semver {
				return v, nil
			}
		}
		return kitVersionMeta{}, fmt.Errorf("version %s not found in channel %s", semver, channel)
	}

	// Pick latest by released_at — admin returns versions sorted but we
	// guard ourselves anyway.
	latest := versions[0]
	for _, v := range versions[1:] {
		if v.ReleasedAt.After(latest.ReleasedAt) {
			latest = v
		}
	}
	return latest, nil
}

// fetchVersions hits GET /api/v1/sk/registry/stackkits/<slug>/versions?channel=<c>
// and returns the parsed list. Empty list is a valid response (no
// versions in this channel yet); caller decides what to do.
func fetchVersions(ctx context.Context, endpoint, token, kitSlug, channel string) ([]kitVersionMeta, error) {
	if endpoint == "" {
		return nil, errors.New("admin endpoint not configured")
	}
	url := fmt.Sprintf("%s/api/v1/sk/registry/stackkits/%s/versions?channel=%s",
		strings.TrimRight(endpoint, "/"), kitSlug, channel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) // #nosec G107 G704 -- endpoint is an operator-supplied admin URL.
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G107 G704 -- request URL is operator-supplied CLI configuration.
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("admin %s: status=%d body=%s", url, resp.StatusCode, string(body))
	}

	var versions []kitVersionMeta
	if err := json.NewDecoder(resp.Body).Decode(&versions); err != nil {
		return nil, fmt.Errorf("decode versions: %w", err)
	}
	return versions, nil
}

// summarizeMismatches returns the resolver entries whose channel does
// not equal `desired`. A non-empty list is the operator-confirm gate.
func summarizeMismatches(r *registry.ResolveResult, desired string) []registry.ResolvedModule {
	if r == nil {
		return nil
	}
	out := make([]registry.ResolvedModule, 0)
	for _, m := range r.Modules {
		if m.Channel != desired && m.Reason == "fallback" {
			out = append(out, m)
		}
	}
	return out
}

func buildChannelMap(r *registry.ResolveResult) []snapshot.ChannelMapEntry {
	if r == nil {
		return nil
	}
	out := make([]snapshot.ChannelMapEntry, 0, len(r.Modules))
	for _, m := range r.Modules {
		out = append(out, snapshot.ChannelMapEntry{
			ModuleSlug:    m.ModuleSlug,
			ModuleVersion: m.ModuleSemver,
			Channel:       m.Channel,
			Reason:        m.Reason,
		})
	}
	return out
}

func printPlan(p *iac.PlanResult, r *registry.ResolveResult) {
	fmt.Println()
	fmt.Println(bold("Plan summary"))
	if p != nil {
		fmt.Printf("  tofu: +%d ~%d -%d (changes=%v)\n", p.Add, p.Change, p.Destroy, p.HasChanges)
	}
	if r != nil {
		s := r.SummarizeReasons()
		fmt.Printf("  modules: %d matched, %d fallback, %d override\n",
			s["matched"], s["fallback"], s["override"])
	}
	fmt.Println()
}

// confirm reads y/Y/yes from stdin. Used only when --auto-approve is off.
func confirm(cmd *cobra.Command, prompt string) (bool, error) {
	fmt.Print(prompt)
	var resp string
	if _, err := fmt.Fscanln(cmd.InOrStdin(), &resp); err != nil {
		// Treat scanln-on-empty-line errors as "no" rather than blowing up.
		return false, nil
	}
	resp = strings.ToLower(strings.TrimSpace(resp))
	return resp == "y" || resp == "yes", nil
}

// writeDeploymentState writes the state file atomically (write-to-tmp + rename).
func writeDeploymentState(path string, state *models.DeploymentState) error {
	loader := config.NewLoader(filepath.Dir(filepath.Dir(path)))
	return loader.SaveDeploymentState(state, path)
}

// postNodeDeployment is best-effort — failure does not abort the
// upgrade. The endpoint is part of kit-update-phase-1 admin work (T5);
// when not yet deployed, we silently skip.
func postNodeDeployment(ctx context.Context, endpoint, token string, state *models.DeploymentState, channelMap []snapshot.ChannelMapEntry, manifest *snapshot.SnapshotManifest) error {
	if endpoint == "" {
		return nil
	}
	url := strings.TrimRight(endpoint, "/") + "/api/v1/sk/node-deployments"
	body := map[string]interface{}{
		"kitSlug":             state.StackKit,
		"nodeName":            state.StackKit, // upgraded by Phase-2 multi-node; single-node uses kit-slug
		"kitVersionId":        state.KitVersionID,
		"kitChannel":          state.KitChannel,
		"moduleVersions":      channelMap,
		"lastAppliedAt":       state.LastApplied,
		"lastKopiaSnapshotId": firstKopiaID(manifest),
		"lastTofuStatePath":   manifest.TofuStatePath,
		"status":              string(state.Status),
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(raw))) // #nosec G107 G704 -- endpoint is an operator-supplied admin URL.
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G107 G704 -- request URL is operator-supplied CLI configuration.
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, string(respBody))
	}
	return nil
}

func firstKopiaID(m *snapshot.SnapshotManifest) string {
	if m == nil || len(m.KopiaSnapshots) == 0 {
		return ""
	}
	return m.KopiaSnapshots[0].SnapshotID
}
