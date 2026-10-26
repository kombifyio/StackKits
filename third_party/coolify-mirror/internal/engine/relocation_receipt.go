package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mtalavi/coolify-mirror/internal/coolify"
	"strings"
	"time"
)

const RelocationRestoreFormat = "kombify.io/coolify-relocation-restore/v1"

// RelocationRestoreReceipt exposes only identities and observations; archive
// contents and platform credentials remain on the destination node.
type RelocationRestoreReceipt struct {
	ArchiveSHA256 string                       `json:"archive_sha256"`
	TransferID    string                       `json:"transfer_id"`
	PlanSHA256    string                       `json:"plan_sha256"`
	Format        string                       `json:"format"`
	Tool          ToolInfo                     `json:"tool"`
	VerifiedAt    time.Time                    `json:"verified_at"`
	Resources     []RelocationRestoredResource `json:"resources"`
}

type RelocationRestoredResource struct {
	ExpectedServices []string `json:"expected_services,omitempty"`
	SourceUUID       string   `json:"source_uuid"`
	UUID             string   `json:"uuid"`
	Kind             string   `json:"kind"`
	WasRunning       bool     `json:"was_running"`
	ObservedState    string   `json:"observed_state"`
}

// MatchRelocationManifest refuses a changed selection before any destination
// mutation. The checked plan and encrypted archive must name the same closure.
func MatchRelocationManifest(raw []byte, manifest *Manifest) error {
	var plan RelocationPlan
	if json.Unmarshal(raw, &plan) != nil || plan.Format != PlanFormat || plan.Tool != Tool() || len(plan.Blockers) != 0 {
		return errors.New("invalid or blocked expected relocation plan")
	}
	if manifest.Mode != plan.Manifest.Mode || manifest.Source != plan.Manifest.Source {
		return errors.New("archive source or mode differs from the checked relocation plan")
	}
	expected := map[string]string{}
	for _, r := range plan.Manifest.Resources {
		if r.Local() {
			expected[r.UUID] = r.Table
		}
	}
	if len(expected) == 0 {
		return errors.New("relocation plan has no local resources")
	}
	for _, r := range manifest.Resources {
		if !r.Local() {
			continue
		}
		if expected[r.UUID] != r.Table {
			return fmt.Errorf("archive resource %s differs from the checked relocation plan", r.UUID)
		}
		delete(expected, r.UUID)
	}
	if len(expected) != 0 {
		return errors.New("archive omits resources from the checked relocation plan")
	}
	return nil
}

// RelocationReceipt reuses StartResources' real container stability, health and
// proxy checks. A held resource is never a verified move.
func RelocationReceipt(results []StartResult, plan []byte, transferID string) (RelocationRestoreReceipt, error) {
	receipt := RelocationRestoreReceipt{Format: RelocationRestoreFormat, Tool: Tool(), VerifiedAt: time.Now().UTC(), Resources: []RelocationRestoredResource{}}
	if transferID == "" {
		return receipt, errors.New("relocation receipt requires transfer identity")
	}
	receipt.TransferID, receipt.PlanSHA256 = transferID, fmt.Sprintf("sha256:%x", sha256.Sum256(plan))
	seen := map[string]bool{}
	for _, r := range results {
		if !r.OK || r.Resource.Hold != "" || r.Resource.UUID == "" || r.Resource.SourceUUID == "" || seen[r.Resource.SourceUUID] || (r.Resource.WasRunning && r.Stopped) {
			return receipt, errors.New("relocation has an unverified, held or ambiguous resource")
		}
		seen[r.Resource.SourceUUID] = true
		state := "running"
		if r.Stopped {
			state = "stopped"
		}
		receipt.Resources = append(receipt.Resources, RelocationRestoredResource{SourceUUID: r.Resource.SourceUUID, UUID: r.Resource.UUID, Kind: r.Resource.Table, WasRunning: r.Resource.WasRunning, ObservedState: state, ExpectedServices: r.Resource.Expect})
	}
	if len(receipt.Resources) == 0 {
		return receipt, errors.New("relocation restored no resources")
	}
	return receipt, nil
}

// ObserveRelocation reuses the engine's container and route judges without
// invoking a start, restore, database import or deployment.
func ObserveRelocation(ctx context.Context, in *coolify.Instance, raw []byte, phase string, drain *DrainRecord) error {
	if phase == "resumed" {
		var data struct {
			PlanJSON   string `json:"plan_json"`
			TransferID string `json:"transfer_id"`
			PlanSHA256 string `json:"plan_sha256"`
		}
		var plan RelocationPlan
		if json.Unmarshal(raw, &data) != nil || json.Unmarshal([]byte(data.PlanJSON), &plan) != nil || data.PlanSHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(data.PlanJSON))) {
			return errors.New("invalid source resume plan")
		}
		if drain != nil && (drain.TransferID != data.TransferID || drain.PlanSHA256 != data.PlanSHA256) {
			return errors.New("source resume drain differs")
		}
		here, err := in.ListResources(ctx)
		if err != nil {
			return err
		}
		by := map[string]coolify.Resource{}
		for _, r := range here {
			by[r.UUID] = r
		}
		for _, r := range plan.Manifest.Resources {
			if !r.Local() {
				continue
			}
			actual, ok := by[r.UUID]
			if !ok || actual.Table != r.Table || !actual.Local() {
				return errors.New("resumed source resource no longer matches original plan")
			}
			_, running, err := upServices(ctx, r.UUID)
			if err != nil {
				return err
			}
			want := r.Running()
			if drain != nil {
				for _, d := range drain.Resources {
					if d.UUID == r.UUID {
						want = d.WasRunning
					}
				}
			}
			if drain != nil {
				for _, original := range drain.Resources {
					if original.UUID != r.UUID {
						continue
					}
					containers, err := resourceContainers(ctx, r.UUID)
					if err != nil {
						return err
					}
					policies := map[string]ContainerRestartPolicy{}
					for _, policy := range original.RestartPolicies {
						policies[policy.ID] = policy
					}
					for _, container := range containers {
						policy, exists := policies[container.ID]
						actual := container.HostConfig.RestartPolicy
						if exists && (actual.Name != policy.Name || actual.MaximumRetryCount != policy.MaximumRetryCount) {
							return fmt.Errorf("source resource %s did not restore its original restart policies", r.UUID)
						}
						// Coolify may recreate the selected resource while starting it.
						// Its new container ID follows the manager's restart policy;
						// it must not still carry our temporary drain fence.
						if !exists && actual.Name != "always" && actual.Name != "unless-stopped" && actual.Name != "on-failure" {
							return fmt.Errorf("source resource %s has a replacement container with an unconfirmed restart policy", r.UUID)
						}
					}
				}
			}
			if running != want {
				return fmt.Errorf("source resource %s did not return to its original running state", r.UUID)
			}
		}
		return nil
	}
	if phase == "sent" || phase == "prepared" {
		var sent struct {
			TransferID string `json:"transfer_id"`
			PlanSHA256 string `json:"plan_sha256"`
		}
		if json.Unmarshal(raw, &sent) != nil || drain == nil || drain.TransferID != sent.TransferID || drain.PlanSHA256 != sent.PlanSHA256 || drain.Server != in.Hostname || !drain.Complete() {
			return errors.New("source relocation drain is unconfirmed")
		}
		if err := VerifySourceDrain(ctx, drain); err != nil {
			return err
		}
		resources, err := in.ListResources(ctx)
		if err != nil {
			return err
		}
		present := map[string]string{}
		selected := map[string]bool{}
		for _, resource := range drain.Resources {
			selected[resource.UUID] = true
		}
		var retained []coolify.Resource
		for _, r := range resources {
			if r.Local() {
				present[r.UUID] = r.Table
				if selected[r.UUID] {
					retained = append(retained, r)
				}
			}
		}
		if blockers, err := relocationAutomationBlockers(ctx, in, retained); err != nil {
			return err
		} else if len(blockers) > 0 {
			return errors.New(strings.Join(blockers, "; "))
		}
		for _, r := range drain.Resources {
			if present[r.UUID] != r.Table {
				return fmt.Errorf("source resource %s is no longer retained on this server", r.UUID)
			}
			_, running, err := upServices(ctx, r.UUID)
			if err != nil {
				return err
			}
			if running {
				return fmt.Errorf("source resource %s is running again", r.UUID)
			}
		}
		return nil
	}
	var receipt RelocationRestoreReceipt
	if phase != "received" || json.Unmarshal(raw, &receipt) != nil || receipt.Format != RelocationRestoreFormat || receipt.Tool != Tool() || len(receipt.Resources) == 0 {
		return errors.New("invalid destination relocation receipt")
	}
	resources, err := in.ListResources(ctx)
	if err != nil {
		return err
	}
	byUUID := map[string]coolify.Resource{}
	for _, r := range resources {
		byUUID[r.UUID] = r
	}
	for _, r := range receipt.Resources {
		actual, ok := byUUID[r.UUID]
		if !ok || actual.Table != r.Kind || !actual.Local() {
			return errors.New("restored resource no longer belongs to this destination")
		}
		if r.ObservedState == "stopped" {
			_, running, err := upServices(ctx, r.UUID)
			if err != nil {
				return err
			}
			if running {
				return fmt.Errorf("restored stopped resource %s is now running", r.UUID)
			}
			continue
		}
		containers, err := resourceContainers(ctx, r.UUID)
		if err != nil {
			return err
		}
		if ok, detail := judge(containers, r.ExpectedServices); !ok {
			return fmt.Errorf("destination resource %s: %s", r.UUID, detail)
		}
		if problems := checkRoutes(ctx, actual.Domains); len(problems) > 0 {
			return fmt.Errorf("destination route verification failed: %s", strings.Join(problems, "; "))
		}
	}
	return nil
}
