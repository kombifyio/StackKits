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
	fleetMemberAdmissionResultSchema   = "stackkit.fleet-member-admission-result/v1"
	fleetMemberJoinResultSchema        = "stackkit.fleet-member-join-result/v1"
	fleetMemberKeyCertifyResultSchema  = "stackkit.fleet-member-key-certify-result/v1"
	fleetMemberKeyImportResultSchema   = "stackkit.fleet-member-key-import-result/v1"
	fleetMemberAdmissionRoot           = ".stackkit/fleet/member-admissions"
	fleetMemberEvidenceKeyRequestPath  = ".stackkit/fleet/member-evidence-key-request.json"
	fleetMemberEvidenceKeyRequestLabel = "member evidence key request"
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

type fleetCertifyMemberKeyOptions struct {
	inventory string
	output    string
	validFor  time.Duration
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
	// EvidenceKeyRequestPath is the one file the member relays to the
	// Foundation Node for `stackkit fleet certify-member-key`.
	EvidenceKeyRequestPath string `json:"evidenceKeyRequestPath"`
	EvidenceKeyID          string `json:"evidenceKeyId"`
}

type fleetMemberKeyCertifyResult struct {
	SchemaVersion   string                       `json:"schemaVersion"`
	CertificatePath string                       `json:"certificatePath"`
	RecordPath      string                       `json:"recordPath"`
	StackID         string                       `json:"stackId"`
	PlanHash        string                       `json:"planHash"`
	Member          fleetmember.ExecutionBinding `json:"member"`
	EvidenceKeyID   string                       `json:"evidenceKeyId"`
	Capability      string                       `json:"capability"`
	ValidUntil      string                       `json:"validUntil"`
}

type fleetMemberKeyImportResult struct {
	SchemaVersion string                     `json:"schemaVersion"`
	StackID       string                     `json:"stackId"`
	PlanHash      string                     `json:"planHash"`
	Binding       localevidence.LocalBinding `json:"localBinding"`
	EvidenceKeyID string                     `json:"evidenceKeyId"`
	Capability    string                     `json:"capability"`
	ValidUntil    string                     `json:"validUntil"`
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
StackKits version.

Join also generates a member evidence key and writes one request file. The
Home owner certifies it with certify-member-key; after import-member-key the
member applies and verifies only its own Site/node/channel tuple and signs that
evidence with the certified key.`,
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
	certifyOptions := fleetCertifyMemberKeyOptions{}
	certify := &cobra.Command{
		Use:   "certify-member-key <request-file>",
		Short: "Owner-certify a joined member's evidence key for its own tuple",
		Long: `Certify the member evidence key a joined member generated at join.

The certificate lets the member sign Apply and Verify evidence for its own
Site/node/execution-channel tuple and nothing else: no enrollment, identity
signing, credential issuance, ControlAuthority, or Owner authority. The
Foundation Node records every certificate it issues and from then on leaves
that member's local runtime targets to the member.`,
		Example: `  # On the Foundation Node, with the request file the member relayed
  stackkit fleet certify-member-key member-evidence-key-request.json --output member-evidence-key.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeFleetFailure(cmd, runFleetCertifyMemberKey(cmd, deps, args[0], certifyOptions))
		},
	}
	certify.Flags().StringVar(&certifyOptions.inventory, "inventory", "", "Inventory declaring both execution channels (otherwise one conventional inventory file is selected)")
	certify.Flags().StringVar(&certifyOptions.output, "output", "", "Additional workspace-confined certificate output to relay to the member")
	certify.Flags().DurationVar(&certifyOptions.validFor, "valid-for", fleetmember.DefaultEvidenceKeyValidity, "Certificate validity (at most 2160h)")
	importKey := &cobra.Command{
		Use:   "import-member-key <certificate-file>",
		Short: "Import the Home owner certificate for this member's evidence key",
		Example: `  # On the Cloud host, with the certificate the Foundation Node returned
  stackkit fleet import-member-key member-evidence-key.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return writeFleetFailure(cmd, runFleetImportMemberKey(cmd, deps, args[0]))
		},
	}
	command.AddCommand(admit, join, certify, importKey)
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
		spec, inventory, inventoryPath, resolved, err := compileFleetPlan(workspace, options.inventory, "fleet admit-member")
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
		result := newFleetMemberJoinResult("already-joined", existing)
		if err := deps.mutate(workspace, "fleet join", func() error {
			return issueFleetMemberEvidenceKeyRequest(workspace, deps.now(), existing, &result)
		}); err != nil {
			return err
		}
		return writeCommandResult(cmd, cmd.CommandPath(), result)
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
	result := newFleetMemberJoinResult("joined", custody)
	if err := deps.mutate(workspace, "fleet join", func() error {
		return issueFleetMemberEvidenceKeyRequest(workspace, deps.now(), custody, &result)
	}); err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

// issueFleetMemberEvidenceKeyRequest establishes the member evidence key
// once and writes the proof-of-possession request the member relays to the
// Foundation Node. The private key never leaves member custody.
func issueFleetMemberEvidenceKeyRequest(workspace string, now time.Time, custody fleetmember.Custody, result *fleetMemberJoinResult) error {
	key, err := localevidence.EstablishMemberEvidenceKey(workspace, custody.StackID, custody.Binding)
	if err != nil {
		return err
	}
	request, err := fleetmember.NewEvidenceKeyRequest(custody, key, now)
	if err != nil {
		return err
	}
	document, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return err
	}
	if err := writeFederationPrivateAtomic(workspace, fleetMemberEvidenceKeyRequestPath, append(document, '\n')); err != nil {
		return fmt.Errorf("persist %s: %w", fleetMemberEvidenceKeyRequestLabel, err)
	}
	result.EvidenceKeyRequestPath, result.EvidenceKeyID = fleetMemberEvidenceKeyRequestPath, key.KeyID
	return nil
}

func runFleetCertifyMemberKey(cmd *cobra.Command, deps fleetMemberCommandDeps, requestPath string, options fleetCertifyMemberKeyOptions) error {
	workspace, err := federationWorkspace(deps.workspace())
	if err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return fmt.Errorf("fleet certify-member-key requires the Home owner custody: %w", err)
	}
	ownerRef, keyID, public, err := localevidence.OwnerVerificationKey(workspace)
	if err != nil {
		return err
	}
	raw, err := fleetmember.ReadBounded(resolveFleetInputPath(workspace, requestPath))
	if err != nil {
		return fmt.Errorf("read %s: %w", fleetMemberEvidenceKeyRequestLabel, err)
	}
	var output string
	if strings.TrimSpace(options.output) != "" {
		output, err = federationWorkspacePath(workspace, options.output, "member evidence key certificate output")
		if err != nil {
			return err
		}
	}
	var result fleetMemberKeyCertifyResult
	err = deps.mutate(workspace, "fleet certify-member-key", func() error {
		_, _, _, resolved, err := compileFleetPlan(workspace, options.inventory, "fleet certify-member-key")
		if err != nil {
			return err
		}
		certificate, err := fleetmember.Certify(fleetmember.CertifyRequest{
			Request: raw, Plan: resolved.Plan, Authority: owner.Binding,
			OwnerRef: ownerRef, KeyID: keyID, PublicKey: public,
			Now: deps.now(), ValidFor: options.validFor,
		})
		if err != nil {
			return err
		}
		signing, err := certificate.SigningBytes()
		if err != nil {
			return err
		}
		certificate.Signature, err = localevidence.SignOwnerMemberEvidenceKey(workspace, signing)
		if err != nil {
			return err
		}
		document, err := json.MarshalIndent(certificate, "", "  ")
		if err != nil {
			return err
		}
		document = append(document, '\n')
		record := fleetmember.IssuedEvidenceKeysRoot + "/" + certificate.Member.NodeRef + ".json"
		if err := writeFederationPrivateAtomic(workspace, record, document); err != nil {
			return fmt.Errorf("record issued member evidence key certificate: %w", err)
		}
		certificatePath := record
		if output != "" {
			if err := writeFederationPrivateAtomic(workspace, output, document); err != nil {
				return fmt.Errorf("persist member evidence key certificate: %w", err)
			}
			certificatePath = output
		}
		result = fleetMemberKeyCertifyResult{
			SchemaVersion: fleetMemberKeyCertifyResultSchema, CertificatePath: certificatePath, RecordPath: record,
			StackID: certificate.StackID, PlanHash: certificate.PlanHash, Member: certificate.Member,
			EvidenceKeyID: certificate.Key.KeyID, Capability: certificate.Capability,
			ValidUntil: certificate.ValidUntil.Format(time.RFC3339),
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

func runFleetImportMemberKey(cmd *cobra.Command, deps fleetMemberCommandDeps, certificatePath string) error {
	workspace, err := federationWorkspace(deps.workspace())
	if err != nil {
		return err
	}
	raw, err := fleetmember.ReadBounded(resolveFleetInputPath(workspace, certificatePath))
	if err != nil {
		return fmt.Errorf("read member evidence key certificate: %w", err)
	}
	var result fleetMemberKeyImportResult
	err = deps.mutate(workspace, "fleet import-member-key", func() error {
		evidence, err := fleetmember.ImportEvidenceKeyCertificate(workspace, raw, deps.now())
		if err != nil {
			return err
		}
		result = fleetMemberKeyImportResult{
			SchemaVersion: fleetMemberKeyImportResultSchema,
			StackID:       evidence.Certificate.StackID, PlanHash: evidence.Certificate.PlanHash,
			Binding: evidence.Custody.Binding, EvidenceKeyID: evidence.Key.KeyID,
			Capability: evidence.Certificate.Capability, Grants: evidence.Certificate.Grants,
			ValidUntil: evidence.Certificate.ValidUntil.Format(time.RFC3339),
		}
		return nil
	})
	if err != nil {
		return err
	}
	return writeCommandResult(cmd, cmd.CommandPath(), result)
}

// compileFleetPlan compiles the current plan of a Foundation Node exactly from
// its persisted StackSpec and the shared Inventory.
func compileFleetPlan(workspace, inventoryFlag, operation string) ([]byte, []byte, string, architecturev2.Result, error) {
	spec, _, err := readFleetStackSpec(workspace)
	if err != nil {
		return nil, nil, "", architecturev2.Result{}, err
	}
	inventory, inventoryPath, err := locateArchitectureV2Inventory(workspace, inventoryFlag)
	if err != nil {
		return nil, nil, "", architecturev2.Result{}, err
	}
	if len(inventory) == 0 {
		return nil, nil, "", architecturev2.Result{}, fmt.Errorf("%s requires an Inventory that declares the Home and member execution channels", operation)
	}
	service, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return nil, nil, "", architecturev2.Result{}, fmt.Errorf("load embedded Architecture v2 authority: %w", err)
	}
	resolved, err := service.Resolve(architecturev2.ResolveInput{StackSpec: spec, Inventory: inventory})
	if err != nil {
		return nil, nil, "", architecturev2.Result{}, err
	}
	return spec, inventory, inventoryPath, resolved, nil
}

func resolveFleetInputPath(workspace, candidate string) string {
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(workspace, candidate)
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
