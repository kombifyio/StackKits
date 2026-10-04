package commands

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kombifyio/stackkits/internal/advancedcapability"
	"github.com/kombifyio/stackkits/internal/originca"
	"github.com/spf13/cobra"
)

var (
	advancedOriginCertificateCapability string
	advancedOriginCertificateHosts      string
	advancedOriginCertificateFile       string
	advancedOriginCertificateJSON       bool
)

var advancedOriginCertificateCmd = &cobra.Command{
	Use:   "origin-certificate",
	Short: "Manage the Cloudflare Origin CA certificate of managed kombify.me origins",
}

var advancedOriginCertificateRequestCmd = &cobra.Command{
	Use:   "request",
	Short: "Generate the node-local origin key pair and print its CSR",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAdvancedOriginCertificate(cmd, advancedcapability.OperationOriginCertificateRequest)
	},
}

var advancedOriginCertificateInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Verify and install the delivered origin certificate",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		return runAdvancedOriginCertificate(cmd, advancedcapability.OperationOriginCertificateInstall)
	},
}

func init() {
	for _, command := range []*cobra.Command{advancedOriginCertificateRequestCmd, advancedOriginCertificateInstallCmd} {
		command.Flags().StringVar(&advancedOriginCertificateCapability, "capability", "",
			"Path to a canonical stackkit.advanced-capability/v1 file that allows the origin-certificate operation")
		command.Flags().BoolVar(&advancedOriginCertificateJSON, "json", false, "Emit stackkit.command-result/v1 JSON")
	}
	advancedOriginCertificateRequestCmd.Flags().StringVar(&advancedOriginCertificateHosts, "hosts", "",
		"Comma-separated managed kombify.me hostnames the certificate covers")
	advancedOriginCertificateInstallCmd.Flags().StringVar(&advancedOriginCertificateFile, "certificate-file", "",
		"Path to the PEM certificate Cloudflare issued for the pending CSR")
	advancedOriginCertificateCmd.AddCommand(advancedOriginCertificateRequestCmd, advancedOriginCertificateInstallCmd)
	advancedCmd.AddCommand(advancedOriginCertificateCmd)
}

func runAdvancedOriginCertificate(cmd *cobra.Command, operation string) error {
	workspace := getWorkDir()
	now := time.Now().UTC().Truncate(time.Second)
	if err := admitAdvancedOriginCertificate(workspace, resolvePathFromWorkDir(workspace, advancedOriginCertificateCapability), operation, now); err != nil {
		return writeAdvancedOriginCertificateDenial(cmd, operation, err)
	}
	installer := originca.Installer{Workspace: workspace}
	var (
		result any
		err    error
	)
	switch operation {
	case advancedcapability.OperationOriginCertificateRequest:
		result, err = installer.Request(strings.Split(advancedOriginCertificateHosts, ","))
	default:
		var certificate []byte
		certificate, err = readAdvancedRegular(resolvePathFromWorkDir(workspace, advancedOriginCertificateFile), 64*1024, "origin certificate")
		if err == nil {
			result, err = installer.Install(certificate)
		}
	}
	if err != nil {
		return writeAdvancedOriginCertificateDenial(cmd, operation, &originCertificateInvalidError{cause: err})
	}
	if advancedOriginCertificateJSON {
		return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", result)
	}
	_, werr := fmt.Fprintf(cmd.OutOrStdout(), "Origin certificate %s completed\n", strings.TrimPrefix(operation, "public-tls.origin-certificate."))
	return werr
}

type originCertificateInvalidError struct{ cause error }

func (e *originCertificateInvalidError) Error() string { return e.cause.Error() }
func (e *originCertificateInvalidError) Unwrap() error { return e.cause }

// admitAdvancedOriginCertificate verifies the Techstack-signed capability
// offline against the local Owner-approved trust before any custody write.
func admitAdvancedOriginCertificate(workspace, capabilityPath, operation string, now time.Time) error {
	capabilityRaw, err := readAdvancedRegular(capabilityPath, maxAdvancedTrustBundleBytes, "Advanced capability")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &advancedcapability.Denial{Code: advancedcapability.ReasonCapabilityRequired, Field: "capability", Detail: "file is required"}
		}
		return err
	}
	scope, err := advancedRestoreDrillScopeFromWorkspace(workspace)
	if err != nil {
		return err
	}
	trust := scope.Trust
	_, err = advancedcapability.Verify(capabilityRaw, advancedcapability.Request{
		Now: now, TrustBundle: &trust, StackID: scope.StackID, OwnerRef: scope.OwnerRef, Operation: operation,
	})
	return err
}

func writeAdvancedOriginCertificateDenial(cmd *cobra.Command, operation string, cause error) error {
	code := "origin_certificate_invalid"
	if reason, ok := advancedcapability.Reason(cause); ok {
		code = string(reason)
	} else if !errors.As(cause, new(*originCertificateInvalidError)) {
		return cause
	}
	if advancedOriginCertificateJSON {
		denial := driftOperationDenial{
			SchemaVersion: operationDenialSchemaVersion, Operation: operation, Mode: "advanced",
			ReasonCode: code, Message: cause.Error(),
		}
		if err := writeCommandResultStatus(cmd, cmd.CommandPath(), "denied", denial); err != nil {
			return errors.Join(cause, err)
		}
	}
	return cause
}
