package commands

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kombifyio/stackkits/internal/actionableerror"
	"github.com/kombifyio/stackkits/internal/clientsetup"
	"github.com/kombifyio/stackkits/internal/localevidence"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use: "client-trust", Short: "Verify public Owner-CA and scoped DNS custody for a client",
		Long:        "Verify the signed public CA and LAN resolver handoff on this installed host. With --export, prepare the Linux OS trust adapter. This does not verify the application runtime, approve client trust, issue device credentials or establish a service session.",
		Annotations: map[string]string{noDeployObservabilityAnnotation: "true"},
	}
	var export, asJSON bool
	cmd.Flags().BoolVar(&export, "export", false, "Export .stackkit/client-trust/enroll-linux.sh from authenticated public custody")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Emit the prepared public trust handoff as JSON")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		wd := getWorkDir()
		custody, err := localevidence.LoadBasementRuntimeCustody(wd)
		if err != nil {
			return machineAwareCommandError(cmd, actionableerror.WithDiagnostic("client_trust_profile_invalid", err))
		}
		access := &accessSummary{Domain: custody.Domain}
		attachAccessClientTrust(wd, access)
		if access.ClientTrust == nil || !strings.Contains(custody.Domain, ".") {
			return machineAwareCommandError(cmd, actionableerror.WithDiagnostic("client_trust_profile_invalid", errors.New("private-zone public custody is unavailable")))
		}
		access.ClientTrust.RemoteAccess.Status = "unverified"
		profile := ""
		if export {
			if err := writeAccessClientProfile(wd, access); err != nil {
				if _, known := actionableerror.FromError(err); !known {
					err = actionableerror.WithDiagnostic("client_trust_profile_invalid", err)
				}
				return machineAwareCommandError(cmd, err)
			}
			profile = access.ClientTrust.LinuxProfilePath
		}
		report := struct {
			SchemaVersion       string             `json:"schemaVersion"`
			Status              string             `json:"status"`
			RuntimeVerification string             `json:"runtimeVerification"`
			ClientApproval      string             `json:"clientApproval"`
			ProfilePath         string             `json:"profilePath,omitempty"`
			ClientTrust         *accessClientTrust `json:"clientTrust"`
		}{"stackkit.client-trust-handoff/v1", "prepared", "not-requested", "pending", profile, access.ClientTrust}
		if asJSON {
			return writeCommandResultStatus(cmd, cmd.CommandPath(), "success", report)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Public Owner-CA custody verified for %s; resolver %s; CA %s.\nRuntime verification: not requested. Client OS approval: pending.\n", custody.Domain, access.ClientTrust.ResolverAddress, access.ClientTrust.CAFingerprint)
		if export {
			fmt.Fprintf(cmd.OutOrStdout(), "Profile prepared: %s. Copy through trusted server access, review the CA fingerprint, then approve on the client with sudo sh enroll-linux.sh --approve.\n", filepath.Join(wd, profile))
		}
		return nil
	}
	verifyCmd.AddCommand(cmd)
}

// writeAccessClientProfile exports only public trust from authenticated local
// custody. Access JSON is advisory and cannot supply a substitute trust root.
func writeAccessClientProfile(wd string, summary *accessSummary) error {
	if summary.ClientTrust == nil {
		return nil
	}
	trust := summary.ClientTrust
	profile, err := clientsetup.Load(wd)
	if err != nil {
		return fmt.Errorf("client_trust_profile_invalid: %w", err)
	}
	if profile.Domain != summary.Domain || profile.Domain != trust.CanonicalZone || profile.Address != trust.ResolverAddress || profile.Fingerprint != trust.CAFingerprint {
		return errors.New("client_trust_profile_invalid: profile differs from authenticated runtime custody")
	}
	trust.LinuxProfileSHA256 = profile.LinuxSHA256()
	body := profile.Linux
	dir := filepath.Join(wd, ".stackkit", "client-trust")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create public client trust profile: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".enroll-linux-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.WriteString(body); err == nil {
		err = temp.Chmod(0700)
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write public client trust profile: %w", err)
	}
	if err := os.Rename(temp.Name(), filepath.Join(dir, "enroll-linux.sh")); err != nil {
		return fmt.Errorf("publish public client trust profile: %w", err)
	}
	return nil
}
