package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/fleetmember"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

const (
	fleetMemberAdmissionResultSchema = "stackkit.fleet-member-admission-result/v1"
	fleetMemberJoinResultSchema      = "stackkit.fleet-member-join-result/v1"
	fleetMemberAdmissionRoot         = ".stackkit/fleet/member-admissions"
)

type fleetMemberCommandDeps struct {
	workspace func() string
	now       func() time.Time
	mutate    func(string, string, func() error) error
}

type fleetAdmitMemberOptions struct {
	node      string
	inventory string
	output    string
	validFor  time.Duration
}

type fleetJoinOptions struct {
	homeKeyID string
}

type fleetMemberAdmissionResult struct {
	SchemaVersion string                       `json:"schemaVersion"`
	AdmissionPath string                       `json:"admissionPath"`
	StackID       string                       `json:"stackId"`
	PlanHash      string                       `json:"planHash"`
	Member        fleetmember.ExecutionBinding `json:"member"`
	Authority     fleetmember.ExecutionBinding `json:"authority"`
	HomeKeyID     string                       `json:"homeKeyId"`
	ValidUntil    string                       `json:"validUntil"`
}

type fleetMemberJoinResult struct {
	SchemaVersion string                     `json:"schemaVersion"`
	Outcome       string                     `json:"outcome"`
	StackID       string                     `json:"stackId"`
	PlanHash      string                     `json:"planHash"`
	Binding       localevidence.LocalBinding `json:"localBinding"`
	Authority     localevidence.LocalBinding `json:"authorityBinding"`
	HomeKeyID     string                     `json:"homeKeyId"`
	Grants        fleetmember.Grants         `json:"grants"`
}

var fleetMemberDeps = fleetMemberCommandDeps{
	workspace: getWorkDir,
	now:       time.Now,
	mutate:    withLifecycleMutation,
}

func init() {
	rootCmd.AddCommand(newFleetCommand(fleetMemberDeps))
}

func newFleetCommand(deps fleetMemberCommandDeps) *cobra.Command {
	command := &cobra.Command{
		Use:   "fleet",
		Short: "Admit and join member hosts of one StackInstance",
		Long: `Bind a physical host to an existing member node of this StackInstance.

The Home owner on the Foundation Node signs a member admission for one enabled
non-controller node at a Cloud Site. The member host verifies it against the
pinned Home key, recompiles the same plan from the admitted StackSpec and
Inventory, and keeps verify-only member custody: no enrollment, signing,
credential issuance, or ControlAuthority. Both hosts must run the same
StackKits version.`,
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	admitOptions := fleetAdmitMemberOptions{}
	admit := &cobra.Command{
		Use:   "admit-member",
		Short: "Owner-sign a member admission for one Cloud edge node",
		Example: `  # On the Foundation Node, admit the Cloud edge declared in the plan
  stackkit fleet admit-member --node cloud-edge`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeFleetFailure(cmd, runFleetAdmitMember(cmd, deps, admitOptions))
		},
	}
	admit.Flags().StringVar(&admitOptions.node, "node", "", "Member node ID from the StackSpec (an enabled worker or edge at a Cloud Site)")
	admit.Flags().StringVar(&admitOptions.inventory, "inventory", "", "Inventory declaring both execution channels (otherwise one conventional inventory file is selected)")
	admit.Flags().StringVar(&admitOptions.output, "output", "", "Workspace-confined admission output (default .stackkit/fleet/member-admissions/<node>.json)")
	admit.Flags().DurationVar(&admitOptions.validFor, "valid-for", fleetmember.MaxAdmissionValidity, "Join window of the admission (at most 24h)")
	joinOptions := fleetJoinOptions{}
	join := &cobra.Command{
		Use:   "join <admission-file>",
		Short: "Join this host as the verify-only member named by an admission",
		Example: `  # On the Cloud host, in an empty deployment directory
  stackkit fleet join member-admission.json --home-key-id <homeKeyId>`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeFleetFailure(cmd, runFleetJoin(cmd, deps, args[0], joinOptions))
		},
	}
	join.Flags().StringVar(&joinOptions.homeKeyID, "home-key-id", "", "Home owner key ID from admit-member, compared out of band")
	command.AddCommand(admit, join)
	return command
}

func writeFleetFailure(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	return writeMachineCommandFailure(cmd, err)
}

func runFleetAdmitMember(cmd *cobra.Command, deps fleetMemberCommandDeps, options fleetAdmitMemberOptions) error {
	node := strings.TrimSpace(options.node)
	if node == "" {
		return errors.New("fleet admit-member requires --node")
	}
	workspace, err := federationWorkspace(deps.workspace())
	if err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return fmt.Errorf("fleet admit-member requires the Home owner custody: %w", err)
	}
	ownerRef, keyID, public, err := localevidence.OwnerVerificationKey(workspace)
	if err != nil {
		return err
	}
	output := strings.TrimSpace(options.output)
	if output == "" {
		output = fleetMemberAdmissionRoot + "/" + node + ".json"
	}
	outputPath, err := federationWorkspacePath(workspace, output, "member admission output")
	if err != nil {
		return err
	}
	var result fleetMemberAdmissionResult
	err = deps.mutate(workspace, "fleet admit-member", func() error {
		spec, _, err := readFleetStackSpec(workspace)
		if err != nil {
			return err
		}
		inventory, inventoryPath, err := locateArchitectureV2Inventory(workspace, options.inventory)
		if err != nil {
			return err
		}
		if len(inventory) == 0 {
			return errors.New("fleet admit-member requires an Inventory that declares the Home and member execution channels")
		}
		service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
		if err != nil {
			return fmt.Errorf("load embedded Architecture v2 authority: %w", err)
		}
		resolved, err := service.Resolve(architecturev2.ResolveInput{StackSpec: spec, Inventory: inventory})
		if err != nil {
			return err
		}
		admission, err := fleetmember.Issue(fleetmember.IssueRequest{
			Plan: resolved.Plan, StackSpec: spec, Inventory: inventory,
			InventoryFormat: fleetInventoryFormat(inventoryPath),
			MemberNodeRef:   node, Authority: owner.Binding,
			OwnerRef: ownerRef, KeyID: keyID, PublicKey: public,
			Now: deps.now(), ValidFor: options.validFor,
		})
		if err != nil {
			return err
		}
		signing, err := admission.SigningBytes()
		if err != nil {
			return err
		}
		admission.Signature, err = localevidence.SignOwnerMemberAdmission(workspace, signing)
		if err != nil {
			return err
		}
		document, err := json.MarshalIndent(admission, "", "  ")
		if err != nil {
			return err
		}
		document = append(document, '\n')
		if err := writeFederationPrivateAtomic(workspace, outputPath, document); err != nil {
			return fmt.Errorf("persist member admission: %w", err)
		}
		result = fleetMemberAdmissionResult{
			SchemaVersion: fleetMemberAdmissionResultSchema, AdmissionPath: outputPath,
			StackID: admission.StackID, PlanHash: admission.PlanHash,
			Member: admission.Member, Authority: admission.Authority, HomeKeyID: keyID,
			ValidUntil: admission.ValidUntil.Format(time.RFC3339),
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

func runFleetJoin(cmd *cobra.Command, deps fleetMemberCommandDeps, admissionPath string, options fleetJoinOptions) error {
	if strings.TrimSpace(options.homeKeyID) == "" {
		return errors.New("fleet join requires --home-key-id from the Foundation Node's admit-member result")
	}
	workspace, err := federationWorkspace(deps.workspace())
	if err != nil {
		return err
	}
	raw, err := readFleetAdmission(workspace, admissionPath)
	if err != nil {
		return err
	}
	verified, err := fleetmember.Verify(raw, options.homeKeyID, deps.now())
	if err != nil {
		return err
	}
	if existing, joined, err := currentFleetMember(workspace, verified); err != nil || joined {
		if err != nil {
			return err
		}
		return writeCommandResult(cmd, cmd.CommandPath(), newFleetMemberJoinResult("already-joined", existing))
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return fmt.Errorf("load embedded Architecture v2 authority: %w", err)
	}
	resolved, err := service.Resolve(architecturev2.ResolveInput{StackSpec: verified.StackSpec, Inventory: verified.Inventory})
	if err != nil {
		return fmt.Errorf("recompile the admitted plan: %w", err)
	}
	if err := verified.BindPlan(resolved.Plan); err != nil {
		return err
	}
	specPath, _, _, err := config.NewLoader(workspace).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return err
	}
	// Persist holds the lifecycle lock itself and accepts only an absent or
	// identical canonical StackSpec, so a foreign workspace is never replaced.
	if _, err := stackspecintent.Persist(stackspecintent.Request{
		WorkspaceRoot: workspace, SpecPath: specPath, Candidate: verified.StackSpec,
		BuildVersion: version, Authority: service,
	}); err != nil {
		return fmt.Errorf("persist the admitted StackSpec: %w", err)
	}
	var custody fleetmember.Custody
	err = deps.mutate(workspace, "fleet join", func() error {
		if _, joined, err := currentFleetMember(workspace, verified); err != nil || joined {
			if err != nil {
				return err
			}
			return errors.New("another join of this workspace completed concurrently")
		}
		if err := persistFleetMemberInventory(workspace, verified); err != nil {
			return err
		}
		custody = fleetmember.NewCustody(verified, deps.now())
		return fleetmember.Persist(workspace, custody)
	})
	if err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), newFleetMemberJoinResult("joined", custody))
}

// currentFleetMember refuses a Foundation Node workspace and reports whether
// this exact member binding and plan already joined.
func currentFleetMember(workspace string, verified fleetmember.Verified) (fleetmember.Custody, bool, error) {
	if _, err := localevidence.LoadOwnerKey(workspace); !errors.Is(err, localevidence.ErrOwnerKeyMissing) {
		if err != nil {
			return fleetmember.Custody{}, false, err
		}
		return fleetmember.Custody{}, false, errors.New("this workspace holds Home owner signing custody; join a member from a separate host workspace")
	}
	existing, err := fleetmember.LoadCustody(workspace)
	if fleetmember.IsNotMember(err) {
		return fleetmember.Custody{}, false, nil
	}
	if err != nil {
		return fleetmember.Custody{}, false, err
	}
	if existing.Binding != verified.Admission.Member.LocalBinding() ||
		existing.StackID != verified.Admission.StackID || existing.PlanHash != verified.Admission.PlanHash {
		return fleetmember.Custody{}, false, fmt.Errorf(
			"this workspace already joined as member %s/%s/%s at plan %s; a changed plan or binding needs a new member lifecycle operation",
			existing.Binding.SiteRef, existing.Binding.NodeRef, existing.Binding.ChannelRef, existing.PlanHash,
		)
	}
	return existing, true, nil
}

func newFleetMemberJoinResult(outcome string, custody fleetmember.Custody) fleetMemberJoinResult {
	return fleetMemberJoinResult{
		SchemaVersion: fleetMemberJoinResultSchema, Outcome: outcome,
		StackID: custody.StackID, PlanHash: custody.PlanHash,
		Binding: custody.Binding, Authority: custody.Authority,
		HomeKeyID: custody.Verifier.KeyID, Grants: custody.Grants,
	}
}

func readFleetStackSpec(workspace string) ([]byte, string, error) {
	specPath, _, _, err := config.NewLoader(workspace).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return nil, "", err
	}
	spec, err := os.ReadFile(specPath) //nolint:gosec // workspace StackSpec selected by the shared loader
	if err != nil {
		return nil, "", fmt.Errorf("read StackSpec %s: %w", specPath, err)
	}
	return spec, specPath, nil
}

func readFleetAdmission(workspace, candidate string) ([]byte, error) {
	path := candidate
	if !filepath.IsAbs(path) {
		path = filepath.Join(workspace, path)
	}
	file, err := os.Open(path) //nolint:gosec // operator-selected admission, read only and bounded
	if err != nil {
		return nil, fmt.Errorf("open member admission: %w", err)
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, fleetmember.MaxAdmissionBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read member admission: %w", err)
	}
	return raw, nil
}

// persistFleetMemberInventory installs the exact admitted Inventory bytes. An
// existing conventional Inventory must already be byte-identical.
func persistFleetMemberInventory(workspace string, verified fleetmember.Verified) error {
	existing, existingPath, err := locateArchitectureV2Inventory(workspace, "")
	if err != nil {
		return err
	}
	if existingPath != "" {
		if !bytes.Equal(existing, verified.Inventory) {
			return fmt.Errorf("existing Inventory %s differs from the admitted Inventory", existingPath)
		}
		return nil
	}
	target := filepath.Join(workspace, ".stackkit", "inventory."+verified.Admission.Inventory.Format)
	return persistInventoryDocument(target, verified.Inventory)
}

func fleetInventoryFormat(path string) string {
	if strings.EqualFold(filepath.Ext(path), ".json") {
		return "json"
	}
	return "yaml"
}
