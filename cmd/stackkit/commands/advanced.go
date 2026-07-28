package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/advancedchangeset"
	"github.com/kombifyio/stackkits/internal/advancedtrust"
	"github.com/kombifyio/stackkits/internal/architecturev2"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

const (
	maxAdvancedTrustBundleBytes = 64 * 1024
	maxAdvancedCandidateBytes   = 4 * 1024 * 1024
)

var (
	advancedTrustBundlePath    string
	advancedTrustDigest        string
	advancedTrustOwnerOK       bool
	advancedTrustJSON          bool
	advancedCapabilityPath     string
	advancedCandidatePath      string
	advancedChangeSetJSON      bool
	advancedChangeSetID        string
	advancedChangeSetDigest    string
	advancedChangeSetApplyJSON bool
	advancedSHA256Pattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

var advancedCmd = &cobra.Command{
	Use:   "advanced",
	Short: "Manage offline-authorized Advanced lifecycle operations",
	Annotations: map[string]string{
		noDeployObservabilityAnnotation: "true",
	},
}

var advancedTrustCmd = &cobra.Command{
	Use:   "trust",
	Short: "Manage the local Owner-approved Advanced issuer trust",
}

var advancedTrustImportCmd = &cobra.Command{
	Use:   "import",
	Short: "Import an exact pinned Advanced issuer trust bundle",
	Args:  cobra.NoArgs,
	RunE:  runAdvancedTrustImport,
}

var advancedTrustInspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect the verified local Advanced issuer trust",
	Args:  cobra.NoArgs,
	RunE:  runAdvancedTrustInspect,
}

var advancedChangeSetCmd = &cobra.Command{
	Use:   "change-set",
	Short: "Manage offline-authorized Terramate change sets",
}

var advancedChangeSetCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create an Owner-signed Terramate change set",
	Args:  cobra.NoArgs,
	RunE:  runAdvancedChangeSetCreate,
}

var advancedChangeSetApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply an exact Owner-approved Terramate change set",
	Args:  cobra.NoArgs,
	RunE:  runAdvancedChangeSetApply,
}

func init() {
	advancedTrustImportCmd.Flags().StringVar(
		&advancedTrustBundlePath,
		"bundle",
		"",
		"Path to a canonical stackkit.advanced-trust-bundle/v1 file",
	)
	advancedTrustImportCmd.Flags().StringVar(
		&advancedTrustDigest,
		"expect-sha256",
		"",
		"Required exact sha256:<hex> digest of the trust bundle",
	)
	advancedTrustImportCmd.Flags().BoolVar(
		&advancedTrustOwnerOK,
		"owner-approve",
		false,
		"Explicitly approve changing local Advanced issuer trust",
	)
	advancedTrustImportCmd.Flags().BoolVar(
		&advancedTrustJSON,
		"json",
		false,
		"Emit stackkit.command-result/v1 JSON",
	)
	advancedTrustInspectCmd.Flags().BoolVar(
		&advancedTrustJSON,
		"json",
		false,
		"Emit stackkit.command-result/v1 JSON",
	)
	advancedChangeSetCreateCmd.Flags().StringVar(
		&advancedCapabilityPath, "capability", "",
		"Path to a canonical stackkit.advanced-capability/v1 file",
	)
	advancedChangeSetCreateCmd.Flags().StringVar(
		&advancedCandidatePath, "candidate-spec", "",
		"Path to the candidate StackSpec v2 using generation.target=terramate",
	)
	advancedChangeSetCreateCmd.Flags().BoolVar(
		&advancedChangeSetJSON, "json", false,
		"Emit stackkit.command-result/v1 JSON",
	)
	advancedChangeSetApplyCmd.Flags().StringVar(
		&advancedCapabilityPath, "capability", "",
		"Path to the canonical stackkit.advanced-capability/v1 used to create the change set",
	)
	advancedChangeSetApplyCmd.Flags().StringVar(
		&advancedCandidatePath, "candidate-spec", "",
		"Path to the exact candidate StackSpec v2 using generation.target=terramate",
	)
	advancedChangeSetApplyCmd.Flags().StringVar(
		&advancedChangeSetID, "change-set", "",
		"Exact sha256:<hex> Owner-signed change-set content address",
	)
	advancedChangeSetApplyCmd.Flags().StringVar(
		&advancedChangeSetDigest, "expect-sha256", "",
		"Required exact sha256:<hex> digest of the canonical stored change-set bytes",
	)
	advancedChangeSetApplyCmd.Flags().BoolVar(
		&advancedChangeSetApplyJSON, "json", false,
		"Emit stackkit.command-result/v1 JSON",
	)
	advancedTrustCmd.AddCommand(advancedTrustImportCmd, advancedTrustInspectCmd)
	advancedChangeSetCmd.AddCommand(advancedChangeSetCreateCmd, advancedChangeSetApplyCmd)
	advancedCmd.AddCommand(advancedTrustCmd, advancedChangeSetCmd)
	rootCmd.AddCommand(advancedCmd)
}

type advancedChangeSetAdmission struct {
	workspace        string
	baselineService  *architecturev2.Service
	candidateService *architecturev2.Service
	baselineCurrent  architecturev2.CurrentResolution
	candidateCurrent architecturev2.CurrentResolution
	baseline         architecturev2.Result
	candidate        architecturev2.Result
	grant            advancedcapability.Grant
	capabilityRaw    []byte
	candidateRaw     []byte
	owner            localevidence.OwnerCustody
	trustSHA256      string
}

type advancedChangeSetResult struct {
	SchemaVersion string                             `json:"schemaVersion"`
	ChangeSetID   string                             `json:"changeSetId"`
	Path          string                             `json:"path"`
	PlanHash      string                             `json:"candidatePlanHash"`
	Changes       []advancedchangeset.ArtifactChange `json:"changes"`
}

type advancedChangeSetCreateDeps struct {
	admit  func(context.Context, string, string, string, time.Time) (advancedChangeSetAdmission, error)
	mutate func(string, string, func() error) error
	create func(context.Context, advancedChangeSetAdmission, time.Time) (advancedChangeSetResult, error)
	now    func() time.Time
}

var advancedChangeSetDeps = advancedChangeSetCreateDeps{
	admit:  admitAdvancedChangeSetCreate,
	mutate: withLifecycleMutation,
	create: createAdvancedChangeSet,
	now:    time.Now,
}

func runAdvancedChangeSetCreate(cmd *cobra.Command, _ []string) error {
	capabilityPath := strings.TrimSpace(advancedCapabilityPath)
	candidatePath := strings.TrimSpace(advancedCandidatePath)
	if capabilityPath == "" || candidatePath == "" {
		return errors.New("advanced change-set create requires --capability and --candidate-spec")
	}
	now := advancedChangeSetDeps.now().UTC().Truncate(time.Second)
	workspace := getWorkDir()
	admission, err := advancedChangeSetDeps.admit(
		cmd.Context(), workspace,
		resolvePathFromWorkDir(workspace, capabilityPath),
		resolvePathFromWorkDir(workspace, candidatePath),
		now,
	)
	if err != nil {
		return writeAdvancedChangeSetDenial(cmd, err)
	}
	var result advancedChangeSetResult
	err = advancedChangeSetDeps.mutate(workspace, "advanced change-set create", func() error {
		revalidated, revalidateErr := advancedChangeSetDeps.admit(
			cmd.Context(), workspace,
			resolvePathFromWorkDir(workspace, capabilityPath),
			resolvePathFromWorkDir(workspace, candidatePath),
			now,
		)
		if revalidateErr != nil {
			return revalidateErr
		}
		if !equalAdvancedAdmission(admission, revalidated) {
			return errors.New("advanced change-set inputs changed after admission")
		}
		var createErr error
		result, createErr = advancedChangeSetDeps.create(cmd.Context(), revalidated, now)
		return createErr
	})
	if err != nil {
		return writeAdvancedChangeSetDenial(cmd, err)
	}
	if advancedChangeSetJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), result)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Advanced change set: %s\nPath: %s\nChanges: %d\n", result.ChangeSetID, result.Path, len(result.Changes))
	return err
}

func runAdvancedTrustImport(cmd *cobra.Command, _ []string) error {
	if !advancedTrustOwnerOK {
		return errors.New("advanced trust import requires explicit --owner-approve")
	}
	expected := strings.TrimSpace(advancedTrustDigest)
	if !advancedSHA256Pattern.MatchString(expected) {
		return errors.New("advanced trust import requires --expect-sha256 sha256:<64 lowercase hex>")
	}
	bundlePath := strings.TrimSpace(advancedTrustBundlePath)
	if bundlePath == "" {
		return errors.New("advanced trust import requires --bundle")
	}
	workspace := getWorkDir()
	raw, err := readAdvancedTrustBundle(resolvePathFromWorkDir(workspace, bundlePath))
	if err != nil {
		return err
	}
	if _, err := advancedtrust.Decode(raw); err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	actual := "sha256:" + hex.EncodeToString(digest[:])
	if actual != expected {
		return errors.New("advanced trust import SHA-256 pin does not match")
	}

	var inspection advancedtrust.Inspection
	if err := withLifecycleMutation(workspace, "advanced trust import", func() error {
		if _, err := advancedtrust.Import(workspace, raw, expected, time.Now().UTC()); err != nil {
			return err
		}
		var inspectErr error
		inspection, inspectErr = advancedtrust.Inspect(workspace)
		return inspectErr
	}); err != nil {
		return err
	}
	return writeAdvancedTrustResult(cmd, inspection)
}

func runAdvancedTrustInspect(cmd *cobra.Command, _ []string) error {
	inspection, err := advancedtrust.Inspect(getWorkDir())
	if err != nil {
		return err
	}
	return writeAdvancedTrustResult(cmd, inspection)
}

func writeAdvancedTrustResult(cmd *cobra.Command, inspection advancedtrust.Inspection) error {
	if advancedTrustJSON {
		return writeCommandResult(cmd, cmd.CommandPath(), inspection)
	}
	if _, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Advanced trust: %s (%d issuers/keys), Owner %s\n",
		inspection.BundleSHA256,
		len(inspection.Keys),
		inspection.OwnerRef,
	); err != nil {
		return err
	}
	for _, key := range inspection.Keys {
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "- %s %s\n", key.IssuerID, key.KeyID); err != nil {
			return err
		}
	}
	return nil
}

func readAdvancedTrustBundle(name string) ([]byte, error) {
	absolute, err := filepath.Abs(name)
	if err != nil {
		return nil, fmt.Errorf("resolve Advanced trust bundle path: %w", err)
	}
	file, err := os.Open(filepath.Clean(absolute))
	if err != nil {
		return nil, fmt.Errorf("open Advanced trust bundle: %w", err)
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, maxAdvancedTrustBundleBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Advanced trust bundle: %w", err)
	}
	if len(raw) > maxAdvancedTrustBundleBytes {
		return nil, errors.New("Advanced trust bundle exceeds 65536 bytes")
	}
	return raw, nil
}
