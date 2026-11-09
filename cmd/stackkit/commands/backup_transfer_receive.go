package commands

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/backuplifecycle"
	"github.com/kombifyio/stackkits/internal/config"
	"github.com/kombifyio/stackkits/internal/confinedfs"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/kombifyio/stackkits/internal/restoreactivation"
	"github.com/kombifyio/stackkits/internal/stackspecintent"
	"github.com/spf13/cobra"
)

// Receiving steps a Techstack receive capability names. Each is authorized
// separately: one grant never admits another step.
const (
	applicationTransferReceiveAdmit   = "admit"
	applicationTransferReceivePrepare = "prepare"
	applicationTransferReceiveImport  = "import"

	applicationTransferIdentityDir = ".stackkit/backups/transfer-identities"
	applicationTransferIntentDir   = ".stackkit/backups/transfer-intents"
)

// applicationTransferIntent is the current canonical desired intent of this
// node and its declared install mode.
type applicationTransferIntent struct {
	Mode      string
	SpecPath  string
	SpecHash  string
	Canonical []byte
}

func currentApplicationTransferIntent(workspace string) (applicationTransferIntent, error) {
	specPath, _, _, err := config.NewLoader(workspace).ResolveStackSpecPathForRead(specFile)
	if err != nil {
		return applicationTransferIntent{}, err
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		return applicationTransferIntent{}, err
	}
	author, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return applicationTransferIntent{}, err
	}
	current, err := author.ValidateStackSpec(raw)
	if err != nil {
		return applicationTransferIntent{}, err
	}
	var intent struct {
		Install struct {
			Mode string `json:"mode"`
		} `json:"install"`
	}
	if err = json.Unmarshal(current.CanonicalStackSpec, &intent); err != nil {
		return applicationTransferIntent{}, err
	}
	return applicationTransferIntent{Mode: intent.Install.Mode, SpecPath: specPath, SpecHash: current.SpecHash, Canonical: current.CanonicalStackSpec}, nil
}

// applicationTransferManaged reports whether this node is Techstack-managed,
// through the one managed-intent detector the other intent changes use
// (install.mode advanced or an imported Advanced trust record). A managed
// node receives and retires a transfer only under the exact Techstack
// capability; a standalone node stays Standard Mode.
func applicationTransferManaged(workspace string) (bool, error) {
	intent, err := currentApplicationTransferIntent(workspace)
	if err != nil {
		return false, err
	}
	return advancedManagedIntent(workspace, intent.Mode)
}

// admitApplicationTransferReceive verifies the Techstack receive capability
// of one receiving step on a managed node. Every scope field is reconstructed
// here: the candidate is this node's exact desired intent and the source Owner
// is the one the authenticated transfer (or the admit request) names. A
// standalone node receives in Standard Mode without a capability.
func admitApplicationTransferReceive(cmd *cobra.Command, workspace, stackID, step, sourceOwner, candidateSpecHash string) error {
	managed, err := applicationTransferManaged(workspace)
	if err != nil {
		return err
	}
	path, _ := cmd.Flags().GetString("capability")
	if !managed {
		if path != "" {
			return errors.New("a standalone node receives a transfer without a Techstack capability; this node has no Advanced trust")
		}
		return nil
	}
	if path == "" {
		return &advancedcapability.Denial{Code: advancedcapability.ReasonCapabilityRequired, Field: "capability", Detail: "managed receiving transfer step " + step + " requires its exact Techstack capability; nothing was received"}
	}
	raw, err := readAdvancedRegular(resolvePathFromWorkDir(workspace, path), maxAdvancedTrustBundleBytes, "Advanced capability")
	if err != nil {
		return err
	}
	owner, err := localevidence.LoadOwnerCustody(workspace)
	if err != nil {
		return err
	}
	trusted, err := advancedtrust.Load(workspace)
	if err != nil {
		return &advancedcapability.Denial{Code: advancedcapability.ReasonTrustBundleUnavailable, Field: "trustBundle", Detail: "verified Owner-approved local trust is required"}
	}
	trust := trusted.TrustBundle()
	_, err = advancedcapability.Verify(raw, advancedcapability.Request{
		Now: time.Now().UTC(), TrustBundle: &trust, StackID: stackID, OwnerRef: owner.OwnerRef,
		Operation:       advancedcapability.OperationApplicationTransferReceive,
		TransferReceive: &advancedcapability.TransferReceive{CandidateSpecHash: candidateSpecHash, SourceOwnerRef: sourceOwner, Step: step},
	})
	return err
}

// requireApplicationTransferAdmittedSource keeps a managed admission to the
// one source Owner its admit grant named.
func requireApplicationTransferAdmittedSource(workspace, nonce, sourceOwner string) error {
	record, found, err := readApplicationTransferAdmittedIntent(workspace, nonce)
	if err != nil || !found {
		return err
	}
	if record.SourceOwnerRef != sourceOwner {
		return errors.New("transfer source differs from the source Owner this admission was granted for")
	}
	return nil
}

// cleanupApplicationTransferCustody removes the node-held receiving identity
// and the fetched ciphertext once the transfer is activated or stopped; the
// activation and its recovery use the signed journal, never the archive.
func cleanupApplicationTransferCustody(cmd *cobra.Command, workspace, nonce, archiveDigest string) {
	paths := []string{}
	if relative, err := applicationTransferIdentityPath(nonce); err == nil {
		paths = append(paths, relative)
	}
	if applicationTransferSHA256Pattern.MatchString(archiveDigest) {
		paths = append(paths, applicationTransferInboxDir+"/"+trimSHA256(archiveDigest)+".age")
	}
	for _, relative := range paths {
		if err := os.Remove(filepath.Join(workspace, filepath.FromSlash(relative))); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(cmd.ErrOrStderr(), "transfer custody cleanup: %v\n", err)
		}
	}
}

// applicationTransferPlanStackID is the stackId of a verified plan.
func applicationTransferPlanStackID(canonical []byte) (string, error) {
	var plan struct {
		StackID string `json:"stackId"`
	}
	if err := json.Unmarshal(canonical, &plan); err != nil {
		return "", err
	}
	if plan.StackID == "" {
		return "", errors.New("verified plan names no stackId")
	}
	return plan.StackID, nil
}

// newApplicationTransferIdentity creates the receiving age identity inside
// the node. Its private half never leaves this workspace.
func newApplicationTransferIdentity() (*age.X25519Identity, error) {
	return age.GenerateX25519Identity()
}

func applicationTransferIdentityPath(nonce string) (string, error) {
	if len(nonce) != 64 {
		return "", errors.New("invalid receiving admission nonce")
	}
	if _, err := hex.DecodeString(nonce); err != nil {
		return "", err
	}
	return applicationTransferIdentityDir + "/" + nonce + ".age", nil
}

func persistApplicationTransferIdentity(workspace, nonce string, identity *age.X25519Identity) error {
	relative, err := applicationTransferIdentityPath(nonce)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	if err = tx.MkdirAll(applicationTransferIdentityDir, 0o700); err != nil {
		return err
	}
	return tx.WriteFileExclusive(relative, []byte(identity.String()+"\n"), 0o600)
}

// applicationTransferIdentityFile names the identity the operator passed or
// the node-held identity of the exact destination admission.
func applicationTransferIdentityFile(workspace, explicit, nonce string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	relative, err := applicationTransferIdentityPath(nonce)
	if err != nil {
		return "", err
	}
	path := filepath.Join(workspace, filepath.FromSlash(relative))
	if _, err = os.Lstat(path); err != nil {
		return "", errors.New("no node-held receiving identity for this admission; pass --identity-file")
	}
	return path, nil
}

// applicationTransferAdmittedIntent records, beside the destination admission,
// the desired intent a managed admit replaced. An abort before any receiving
// runtime existed restores exactly that intent.
type applicationTransferAdmittedIntent struct {
	APIVersion        string                                  `json:"apiVersion"`
	Nonce             string                                  `json:"nonce"`
	BaselineSpecHash  string                                  `json:"baselineSpecHash"`
	BaselineSpec      []byte                                  `json:"baselineSpec"`
	CandidateSpecHash string                                  `json:"candidateSpecHash"`
	SourceOwnerRef    string                                  `json:"sourceOwnerRef"`
	Signature         localevidence.OwnerPolicyStateSignature `json:"signature"`
}

func persistApplicationTransferAdmittedIntent(workspace string, record applicationTransferAdmittedIntent) error {
	record.APIVersion = "stackkit.application-transfer-admitted-intent/v1"
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	canonical, err := json.Marshal(record)
	if err != nil {
		return err
	}
	record.Signature, err = localevidence.SignOwnerPolicyState(workspace, canonical)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return err
	}
	defer tx.Close()
	if err = tx.MkdirAll(applicationTransferIntentDir, 0o700); err != nil {
		return err
	}
	return tx.WriteFileExclusive(applicationTransferIntentDir+"/"+record.Nonce+".json", encoded, 0o600)
}

func readApplicationTransferAdmittedIntent(workspace, nonce string) (applicationTransferAdmittedIntent, bool, error) {
	if _, err := applicationTransferIdentityPath(nonce); err != nil {
		return applicationTransferAdmittedIntent{}, false, err
	}
	root, err := confinedfs.Open(workspace)
	if err != nil {
		return applicationTransferAdmittedIntent{}, false, err
	}
	defer root.Close()
	tx, err := root.BeginTransaction()
	if err != nil {
		return applicationTransferAdmittedIntent{}, false, err
	}
	defer tx.Close()
	raw, _, err := tx.ReadStable(applicationTransferIntentDir + "/" + nonce + ".json")
	if errors.Is(err, os.ErrNotExist) {
		return applicationTransferAdmittedIntent{}, false, nil
	}
	if err != nil {
		return applicationTransferAdmittedIntent{}, false, err
	}
	var record applicationTransferAdmittedIntent
	if err = json.Unmarshal(raw, &record); err != nil {
		return record, false, err
	}
	signature := record.Signature
	record.Signature = localevidence.OwnerPolicyStateSignature{}
	canonical, err := json.Marshal(record)
	if err != nil {
		return record, false, err
	}
	if err = localevidence.VerifyOwnerPolicyState(workspace, canonical, signature); err != nil {
		return record, false, err
	}
	if record.APIVersion != "stackkit.application-transfer-admitted-intent/v1" || record.Nonce != nonce {
		return record, false, errors.New("admitted receiving intent record differs from its admission")
	}
	return record, true, nil
}

// admitManagedApplicationTransferCandidate persists the Techstack-approved
// receiving candidate as this node's desired intent and generates it. No
// runtime is applied: the receiving application's first Apply follows the
// credential adoption (transfer-prepare). The grant binds the exact candidate
// bytes and the source Owner; the candidate must keep this node's stack.
func admitManagedApplicationTransferCandidate(cmd *cobra.Command, workspace, candidatePath, sourceOwner, expectedSpec, portableDigest, nonce string) (string, error) {
	candidateRaw, err := readAdvancedRegular(resolvePathFromWorkDir(workspace, candidatePath), maxAdvancedCandidateBytes, "receiving candidate StackSpec")
	if err != nil {
		return "", err
	}
	author, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return "", err
	}
	candidate, err := author.ValidateStackSpec(candidateRaw)
	if err != nil {
		return "", err
	}
	baseline, err := currentApplicationTransferIntent(workspace)
	if err != nil {
		return "", err
	}
	// The candidate replaces exactly the intent it was authored from; a
	// change since then is never overwritten.
	if baseline.SpecHash != expectedSpec || baseline.SpecHash == candidate.SpecHash {
		return "", errors.New("receiving intent changed since the candidate was authored, or the candidate adds nothing; nothing was received")
	}
	current, err := inspectNativeV2GeneratedAuthority(cmd.Context(), workspace, specFile)
	if err != nil {
		return "", err
	}
	stackID, err := applicationTransferPlanStackID(current.Plan.Canonical())
	if err != nil {
		return "", err
	}
	if err = admitApplicationTransferReceive(cmd, workspace, stackID, applicationTransferReceiveAdmit, sourceOwner, candidate.SpecHash); err != nil {
		return "", err
	}
	// Record the replaced intent before replacing it, so every later failure,
	// a withdraw or an abort before any receiving runtime can restore it.
	if err = persistApplicationTransferAdmittedIntent(workspace, applicationTransferAdmittedIntent{Nonce: nonce, BaselineSpecHash: baseline.SpecHash, BaselineSpec: baseline.Canonical, CandidateSpecHash: candidate.SpecHash, SourceOwnerRef: sourceOwner}); err != nil {
		return "", err
	}
	if _, err = stackspecintent.Persist(stackspecintent.Request{WorkspaceRoot: workspace, SpecPath: baseline.SpecPath, Candidate: candidate.CanonicalStackSpec, ExpectedSpecHash: baseline.SpecHash, BuildVersion: version, Authority: author}); err != nil {
		return "", err
	}
	restore := func(cause error) error {
		if _, restoreErr := restoreApplicationTransferAdmittedIntent(cmd.Context(), workspace, nonce); restoreErr != nil {
			return errors.Join(cause, fmt.Errorf("restore the prior receiving intent: %w", restoreErr))
		}
		return cause
	}
	if err = generateApplicationTransferIntent(cmd.Context(), workspace); err != nil {
		return "", restore(err)
	}
	generated, err := inspectNativeV2GeneratedAuthority(cmd.Context(), workspace, specFile)
	if err != nil {
		return "", restore(err)
	}
	generatedStackID, err := applicationTransferPlanStackID(generated.Plan.Canonical())
	if err != nil || generated.Plan.Binding().SpecHash != candidate.SpecHash || generatedStackID != stackID {
		return "", restore(errors.Join(err, errors.New("generated receiving plan differs from the admitted candidate or this node's stack")))
	}
	// The generated receiving plan must carry exactly the source portable
	// configuration the candidate was authored from; a candidate whose settings
	// or profiles the compiler resolved differently is refused before the
	// source is held.
	added, err := addedTransferWorkloads(baseline.Canonical, candidate.CanonicalStackSpec)
	if err != nil {
		return "", restore(err)
	}
	configuration, err := restoreactivation.ApplicationTransferConfiguration(generated.Plan, added)
	if err != nil {
		return "", restore(err)
	}
	digest, err := backuplifecycle.ApplicationTransferConfigurationDigest(configuration)
	if err != nil {
		return "", restore(err)
	}
	if digest != portableDigest {
		return "", restore(errors.New("the generated receiving plan's portable application configuration differs from the source configuration (a different pinned StackKits release or a source configuration changed since the candidate was authored); nothing was received"))
	}
	return generated.Plan.Binding().PlanHash, nil
}

func generateApplicationTransferIntent(ctx context.Context, workspace string) error {
	// The receiving commands answer with one JSON document on stdout; the
	// in-process generation must not interleave its human progress lines.
	previous := machineOutputCommandActive
	machineOutputCommandActive = true
	defer func() { machineOutputCommandActive = previous }()
	gate := newArchitectureV2ExecutionGate()
	gate.rejectV1 = true
	handled, err := gate.preflight(workspace, specFile, architectureV2Generate, architectureV2ExecutionCLIOptions{context: ctx})
	if err != nil {
		return err
	}
	if !handled {
		return errors.New("receiving intent is not a canonical StackSpec v2")
	}
	return nil
}

// restoreApplicationTransferAdmittedIntent returns a managed receiving node to
// the intent its admit replaced, only while no receiving runtime exists and
// the current intent is still exactly the admitted candidate.
func restoreApplicationTransferAdmittedIntent(ctx context.Context, workspace, nonce string) (bool, error) {
	record, found, err := readApplicationTransferAdmittedIntent(workspace, nonce)
	if err != nil || !found {
		return false, err
	}
	current, err := currentApplicationTransferIntent(workspace)
	if err != nil {
		return false, err
	}
	if current.SpecHash == record.BaselineSpecHash {
		return true, nil
	}
	if current.SpecHash != record.CandidateSpecHash {
		return false, errors.New("receiving intent changed after the transfer admission; the prior intent is not restored")
	}
	author, err := architecturev2.NewEmbeddedService(architecturev2.StackKitsV2Contract(version))
	if err != nil {
		return false, err
	}
	if _, err = stackspecintent.Persist(stackspecintent.Request{WorkspaceRoot: workspace, SpecPath: current.SpecPath, Candidate: record.BaselineSpec, ExpectedSpecHash: record.CandidateSpecHash, BuildVersion: version, Authority: author}); err != nil {
		return false, err
	}
	return true, generateApplicationTransferIntent(ctx, workspace)
}

func trimSHA256(value string) string {
	return strings.TrimPrefix(value, "sha256:")
}
